// tee/errors.go
package tee

import "errors"

var (
    ErrTEEConnectionFailed = errors.New("failed to connect to TEE service")
    ErrTEEExecutionFailed = errors.New("TEE execution failed")
    ErrAttestationMismatch = errors.New("attestation mismatch")
    ErrInvalidAttestation = errors.New("invalid attestation")
)