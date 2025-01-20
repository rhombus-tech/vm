// regions/errors.go

package regions

import "errors"

var (
    ErrTEEPairNotFound      = errors.New("TEE pair not found")
    ErrTEEPairNotHealthy    = errors.New("TEE pair not healthy")
    ErrNoHealthyPairs       = errors.New("no healthy TEE pairs available")
    ErrAttestationFailed    = errors.New("TEE attestation failed")
    ErrExecutionFailed      = errors.New("TEE execution failed")
    ErrSynchronizationFailed = errors.New("TEE pair synchronization failed")
    ErrInvalidState         = errors.New("invalid TEE pair state")
)