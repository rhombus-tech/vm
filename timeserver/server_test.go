// timeserver/server_test.go
package timeserver

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/binary"
	"testing"
	"time"
)

func TestTimeServer(t *testing.T) {
    // Create new server
    server, err := NewTimeServer("test-server", "test-region", "http://localhost:8080")
    if err != nil {
        t.Fatalf("Failed to create server: %v", err)
    }

    // Create test request
    nonce := make([]byte, 32)
    if _, err := rand.Read(nonce); err != nil {
        t.Fatalf("Failed to generate nonce: %v", err)
    }

    req := &VerificationRequest{
        Nonce:    nonce,
        RegionID: "test-region",
    }

    // Get timestamp
    ctx := context.Background()
    resp, err := server.GetTimestamp(ctx, req)
    if err != nil {
        t.Fatalf("Failed to get timestamp: %v", err)
    }

    // Verify response
    if resp.Timestamp == nil {
        t.Error("Expected timestamp in response")
    }

    if resp.Timestamp.ServerID != server.ID {
        t.Errorf("Expected server ID %s, got %s", server.ID, resp.Timestamp.ServerID)
    }

    // Verify signature
    msg := make([]byte, 8+len(nonce))
    binary.BigEndian.PutUint64(msg[:8], uint64(resp.Timestamp.Time.UnixNano()))
    copy(msg[8:], nonce)

    if !ed25519.Verify(server.PublicKey, msg, resp.Timestamp.Signature) {
        t.Error("Invalid signature")
    }

    // Verify delay is reasonable
    if resp.Delay > time.Second {
        t.Errorf("Delay too high: %v", resp.Delay)
    }
}