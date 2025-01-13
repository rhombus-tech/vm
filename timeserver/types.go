// timeserver/types.go
package timeserver

import (
	"context"
	"time"
)

type SignedTimestamp struct {
    ServerID  string
    Time      time.Time
    Signature []byte
    Nonce     []byte
}

type VerificationRequest struct {
    Nonce    []byte
    RegionID string
    TxID     []byte
}

type VerificationResponse struct {
    Timestamp   *SignedTimestamp
    ServerProof []byte
    Delay      time.Duration
}

type TimeVerifier struct {
    network *TimeNetwork
}

func NewTimeVerifier() (*TimeVerifier, error) {
    network := NewTimeNetwork(2, 100*time.Millisecond)
    if err := network.Start(); err != nil {
        return nil, err
    }
    
    return &TimeVerifier{
        network: network,
    }, nil
}

func (tv *TimeVerifier) VerifyExecutionTime(ctx context.Context, regionID string) (*VerifiedTimestamp, error) {
    return tv.network.GetVerifiedTimestamp(ctx, regionID)
} 