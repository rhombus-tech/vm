// timeserver/network.go
package timeserver

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/binary"
	"fmt"
	"sort"
	"sync"
	"time"
)

type TimeNetwork struct {
    servers       map[string][]*TimeServer
    quorumSize    int
    maxLatency    time.Duration
    mu            sync.RWMutex
    overlaps      map[string][]string
    done          chan struct{}
    started       bool
    startOnce     sync.Once
}


func NewTimeNetwork(quorumSize int, maxLatency time.Duration) *TimeNetwork {
    tn := &TimeNetwork{
        servers:    make(map[string][]*TimeServer),
        quorumSize: quorumSize,
        maxLatency: maxLatency,
        overlaps:   make(map[string][]string),
        done:      make(chan struct{}),
    }
    
    return tn
}

// Add a time server to a region
func (tn *TimeNetwork) AddServer(server *TimeServer) error {
    tn.mu.Lock()
    defer tn.mu.Unlock()

    if server.RegionID == "" {
        return fmt.Errorf("region ID required")
    }

    servers := tn.servers[server.RegionID]
    servers = append(servers, server)
    tn.servers[server.RegionID] = servers

    return nil
}

func (tn *TimeNetwork) GetVerifiedTimestamp(ctx context.Context, regionID string) (*VerifiedTimestamp, error) {
    servers := tn.getRegionalServers(regionID)
    if len(servers) < tn.quorumSize {
        return nil, fmt.Errorf("insufficient servers in region")
    }

    // First check server health
    healthyServers := make([]*TimeServer, 0)
    for _, server := range servers {
        if err := tn.checkServerHealth(ctx, server); err != nil {
            continue // Skip unhealthy servers
        }
        healthyServers = append(healthyServers, server)
    }

    if len(healthyServers) < tn.quorumSize {
        return nil, fmt.Errorf("insufficient healthy servers: got %d, need %d", 
            len(healthyServers), tn.quorumSize)
    }

    // Generate nonce for this request
    nonce := make([]byte, 32)
    if _, err := rand.Read(nonce); err != nil {
        return nil, fmt.Errorf("failed to generate nonce: %w", err)
    }

    // Collect responses from healthy servers
    responses := make(chan *VerificationResponse, len(healthyServers))
    errors := make(chan error, len(healthyServers))
    
    for _, server := range healthyServers {
        go func(s *TimeServer) {
            resp, err := s.GetTimestamp(ctx, &VerificationRequest{
                Nonce:    nonce,
                RegionID: regionID,
            })
            if err != nil {
                errors <- err
                return
            }
            responses <- resp
        }(server)
    }

    // Wait for responses with timeout
    collected := make([]*VerificationResponse, 0, tn.quorumSize)
    timeout := time.After(tn.maxLatency)

    for len(collected) < tn.quorumSize {
        select {
        case resp := <-responses:
            if err := tn.verifyResponse(resp, nonce); err != nil {
                continue
            }
            collected = append(collected, resp)
        case err := <-errors:
            // Log error but continue collecting responses
            fmt.Printf("Error collecting timestamp: %v\n", err)
        case <-timeout:
            return nil, fmt.Errorf("failed to reach quorum within timeout")
        case <-ctx.Done():
            return nil, ctx.Err()
        }
    }

    // Validate all collected responses
    if err := tn.validateResponses(collected); err != nil {
        return nil, fmt.Errorf("response validation failed: %w", err)
    }

    // Create verified timestamp from validated responses
    return tn.createVerifiedTimestamp(collected)
}

// Helper to verify a single response
func (tn *TimeNetwork) verifyResponse(resp *VerificationResponse, nonce []byte) error {
    if resp == nil || resp.Timestamp == nil {
        return fmt.Errorf("invalid response")
    }

    server := tn.findServer(resp.Timestamp.ServerID)
    if server == nil {
        return fmt.Errorf("unknown server")
    }

    // Verify signature
    if !tn.verifySignature(server, resp.Timestamp, nonce) {
        return fmt.Errorf("invalid signature")
    }

    return nil
}

type VerifiedTimestamp struct {
    Time       time.Time
    Proofs     []*TimestampProof
    RegionID   string
    QuorumSize int
}

type TimestampProof struct {
    ServerID  string
    Signature []byte
    Delay     time.Duration
}

// getRegionalServers returns all servers for a given region
func (tn *TimeNetwork) getRegionalServers(regionID string) []*TimeServer {
    tn.mu.RLock()
    defer tn.mu.RUnlock()
    return tn.servers[regionID]
}

// findServer finds a server by ID across all regions
func (tn *TimeNetwork) findServer(serverID string) *TimeServer {
    tn.mu.RLock()
    defer tn.mu.RUnlock()
    
    for _, servers := range tn.servers {
        for _, server := range servers {
            if server.ID == serverID {
                return server
            }
        }
    }
    return nil
}

// verifySignature verifies a timestamp signature from a server
func (tn *TimeNetwork) verifySignature(server *TimeServer, timestamp *SignedTimestamp, nonce []byte) bool {
    // Reconstruct the message that was signed
    msg := make([]byte, 8+len(nonce))
    binary.BigEndian.PutUint64(msg[:8], uint64(timestamp.Time.UnixNano()))
    copy(msg[8:], nonce)
    
    // Verify using server's public key
    return ed25519.Verify(server.PublicKey, msg, timestamp.Signature)
}

// createVerifiedTimestamp creates a verified timestamp from multiple responses
func (tn *TimeNetwork) createVerifiedTimestamp(responses []*VerificationResponse) (*VerifiedTimestamp, error) {
    if len(responses) < tn.quorumSize {
        return nil, fmt.Errorf("insufficient responses for quorum")
    }

    // Extract timestamps and sort them
    timestamps := make([]time.Time, len(responses))
    for i, resp := range responses {
        timestamps[i] = resp.Timestamp.Time
    }
    sort.Slice(timestamps, func(i, j int) bool {
        return timestamps[i].Before(timestamps[j])
    })

    // Use median timestamp
    medianIdx := len(timestamps) / 2
    medianTime := timestamps[medianIdx]

    // Create proofs from responses
    proofs := make([]*TimestampProof, len(responses))
    for i, resp := range responses {
        proofs[i] = &TimestampProof{
            ServerID:  resp.Timestamp.ServerID,
            Signature: resp.Timestamp.Signature,
            Delay:     resp.Delay,
        }
    }

    return &VerifiedTimestamp{
        Time:       medianTime,
        Proofs:     proofs,
        RegionID:   responses[0].Timestamp.ServerID, // Use first response's region
        QuorumSize: tn.quorumSize,
    }, nil
}

// validateResponses validates a set of timestamp responses
func (tn *TimeNetwork) validateResponses(responses []*VerificationResponse) error {
    if len(responses) < tn.quorumSize {
        return fmt.Errorf("insufficient responses: got %d, need %d", len(responses), tn.quorumSize)
    }

    // Check time spread
    var minTime, maxTime time.Time
    for i, resp := range responses {
        if i == 0 {
            minTime = resp.Timestamp.Time
            maxTime = resp.Timestamp.Time
            continue
        }
        if resp.Timestamp.Time.Before(minTime) {
            minTime = resp.Timestamp.Time
        }
        if resp.Timestamp.Time.After(maxTime) {
            maxTime = resp.Timestamp.Time
        }
    }

    spread := maxTime.Sub(minTime)
    if spread > tn.maxLatency {
        return fmt.Errorf("time spread too large: %v", spread)
    }

    return nil
}

// checkServerHealth checks if a server is responsive
func (tn *TimeNetwork) checkServerHealth(ctx context.Context, server *TimeServer) error {
    // Generate test nonce
    nonce := make([]byte, 32)
    if _, err := rand.Read(nonce); err != nil {
        return fmt.Errorf("failed to generate nonce: %w", err)
    }

    // Try to get timestamp
    req := &VerificationRequest{
        Nonce:    nonce,
        RegionID: server.RegionID,
    }

    resp, err := server.GetTimestamp(ctx, req)
    if err != nil {
        return fmt.Errorf("server health check failed: %w", err)
    }

    // Verify response
    if !tn.verifySignature(server, resp.Timestamp, nonce) {
        return fmt.Errorf("invalid signature in health check")
    }

    return nil
}

func (tn *TimeNetwork) StartHealthChecks(interval time.Duration) {
    ticker := time.NewTicker(interval)

    go func() {
        defer ticker.Stop()
        for {
            select {
            case <-ticker.C:
                tn.mu.RLock()
                servers := make([]*TimeServer, 0)
                for _, regionalServers := range tn.servers {
                    servers = append(servers, regionalServers...)
                }
                tn.mu.RUnlock()

                for _, server := range servers {
                    ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
                    if err := tn.checkServerHealth(ctx, server); err != nil {
                        // Log unhealthy server
                        fmt.Printf("Server %s health check failed: %v\n", server.ID, err)
                    }
                    cancel()
                }
            case <-tn.done:
                return
            }
        }
    }()
}

// Start initializes and starts the time network
func (tn *TimeNetwork) Start() error {
    var startErr error
    tn.startOnce.Do(func() {
        tn.mu.Lock()
        if tn.started {
            tn.mu.Unlock()
            return
        }
        
        // Initialize done channel
        tn.done = make(chan struct{})
        
        // Start health checks
        tn.StartHealthChecks(1 * time.Minute)
        
        tn.started = true
        tn.mu.Unlock()
    })
    return startErr
}

// Stop stops the time network
func (tn *TimeNetwork) Stop() error {
    tn.mu.Lock()
    defer tn.mu.Unlock()

    if !tn.started {
        return nil
    }

    if tn.done != nil {
        close(tn.done)
    }
    
    tn.started = false
    return nil
}