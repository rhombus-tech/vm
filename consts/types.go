package consts

var (
    // Existing errors
    ErrObjectExists     = "object already exists"
    ErrObjectNotFound   = "object not found"
    ErrInvalidID        = "invalid object ID"
    ErrCodeTooLarge     = "code size exceeds maximum"
    ErrStorageTooLarge  = "storage size exceeds maximum"
    ErrInvalidFunction  = "invalid function call"
    ErrRegionExists     = "region already exists"
    ErrRegionNotFound   = "region not found"
    ErrInvalidTEE       = "invalid TEE address"
    
    // New TEE-related errors
    ErrTEEConnectionFailed  = "failed to connect to TEE service"
    ErrTEEExecutionFailed  = "TEE execution failed"
    ErrInvalidAttestation   = "invalid attestation"
    ErrAttestationMismatch = "attestation mismatch"
    ErrStaleTimestamp      = "timestamp outside valid window"
    ErrInvalidTimestamp    = "invalid Roughtime stamp"
)
