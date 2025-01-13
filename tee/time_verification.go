// tee/time_verification.go
package tee

import (
    "context"
    "fmt"
    "time"
    
    "github.com/rhombus-tech/vm/timeserver"
)

type TimeVerifier struct {
    network *timeserver.TimeNetwork
}

func NewTimeVerifier() (*TimeVerifier, error) {
    // Create time network with reasonable defaults
    network := timeserver.NewTimeNetwork(2, 100*time.Millisecond) // 100ms max latency
    
    if err := network.Start(); err != nil {
        return nil, fmt.Errorf("failed to start time network: %w", err)
    }

    return &TimeVerifier{
        network: network,
    }, nil
}

// VerifyExecutionTime gets a verified timestamp for execution
func (tv *TimeVerifier) VerifyExecutionTime(ctx context.Context, regionID string) (*timeserver.VerifiedTimestamp, error) {
    timestamp, err := tv.network.GetVerifiedTimestamp(ctx, regionID)
    if err != nil {
        return nil, fmt.Errorf("failed to get verified timestamp: %w", err)
    }
    return timestamp, nil
}