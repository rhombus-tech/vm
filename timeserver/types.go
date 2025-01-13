// timeserver/types.go
package timeserver

import (
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