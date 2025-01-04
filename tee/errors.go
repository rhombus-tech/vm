// tee/errors.go
package tee

import "errors"

var (
    ErrInvalidAttestation = errors.New("invalid TEE attestation")
    ErrTEEExecutionFailed = errors.New("TEE execution failed")
    ErrConnectionFailed = errors.New("failed to connect to TEE service")
    ErrVerificationFailed = errors.New("attestation verification failed")
    ErrResultMismatch = errors.New("result mismatch between TEEs")
)