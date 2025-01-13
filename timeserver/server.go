// timeserver/server.go
package timeserver

import (
    "context"
    "crypto/ed25519"
    "encoding/base64"
    "encoding/binary"
    "encoding/json"
    "fmt"
    "net/http"
    "time"
)

// TimeServer represents a single time server instance
type TimeServer struct {
    ID        string
    RegionID  string
    PublicKey ed25519.PublicKey
    privKey   ed25519.PrivateKey
    Address   string
    client    *http.Client
}

// NewTimeServer creates a new time server instance
func NewTimeServer(id string, regionID string, address string) (*TimeServer, error) {
    pub, priv, err := ed25519.GenerateKey(nil)
    if err != nil {
        return nil, fmt.Errorf("failed to generate keys: %w", err)
    }

    return &TimeServer{
        ID:        id,
        RegionID:  regionID,
        PublicKey: pub,
        privKey:   priv,
        Address:   address,
        client:    &http.Client{Timeout: 5 * time.Second},
    }, nil
}

// GetTimestamp gets a timestamp with proof
func (ts *TimeServer) GetTimestamp(ctx context.Context, req *VerificationRequest) (*VerificationResponse, error) {
    if req == nil {
        return nil, fmt.Errorf("nil request")
    }

    // Get current time
    now := time.Now().UTC()

    // Create message to sign
    msg := make([]byte, 8+len(req.Nonce))
    binary.BigEndian.PutUint64(msg[:8], uint64(now.UnixNano()))
    copy(msg[8:], req.Nonce)

    // Sign the message
    sig := ed25519.Sign(ts.privKey, msg)

    // Create timestamp
    timestamp := &SignedTimestamp{
        ServerID:  ts.ID,
        Time:     now,
        Signature: sig,
        Nonce:    req.Nonce,
    }

    return &VerificationResponse{
        Timestamp:   timestamp,
        ServerProof: sig,
        Delay:      time.Since(now),
    }, nil
}

// Serve starts the time server HTTP endpoint
func (ts *TimeServer) Serve(addr string) error {
    mux := http.NewServeMux()
    mux.HandleFunc("/timestamp", ts.handleTimestamp)
    return http.ListenAndServe(addr, mux)
}

// HTTP handler for timestamp requests
func (ts *TimeServer) handleTimestamp(w http.ResponseWriter, r *http.Request) {
    if r.Method != http.MethodPost {
        http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
        return
    }

    // Get nonce from header
    nonceStr := r.Header.Get("X-Nonce")
    nonce, err := base64.StdEncoding.DecodeString(nonceStr)
    if err != nil {
        http.Error(w, "invalid nonce", http.StatusBadRequest)
        return
    }

    // Create verification request
    req := &VerificationRequest{
        Nonce:    nonce,
        RegionID: r.Header.Get("X-Region"),
    }

    // Get timestamp
    resp, err := ts.GetTimestamp(r.Context(), req)
    if err != nil {
        http.Error(w, err.Error(), http.StatusInternalServerError)
        return
    }

    // Return response
    json.NewEncoder(w).Encode(map[string]interface{}{
        "time":      resp.Timestamp.Time.Format(time.RFC3339Nano),
        "signature": base64.StdEncoding.EncodeToString(resp.Timestamp.Signature),
        "server_id": ts.ID,
    })
}