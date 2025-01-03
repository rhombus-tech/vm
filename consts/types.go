package consts

const (
    // Existing constants
    TransferID                   uint8 = 0
    ContractVerificationID       uint8 = 1
    ContractVerificationResultID uint8 = 2
    CreateObjectID              uint8 = 3
    SetInputObjectID            uint8 = 4
    SendEventID                uint8 = 5
    CreateRegionID             uint8 = 6
    UpdateRegionID             uint8 = 7
    CreateObjectResultID        uint8 = 8
    SetInputObjectResultID      uint8 = 9
    SendEventResultID          uint8 = 10
    CreateRegionResultID       uint8 = 11
    UpdateRegionResultID       uint8 = 12

    // New TEE-related IDs
    ExecutionResultID         uint8 = 13
    AttestationReportID      uint8 = 14
)

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

// TEE platform types
const (
    TEEPlatformSGX uint8 = iota
    TEEPlatformSEV
)

// Maximum allowed drift for Roughtime stamps
const MaxTimeDrift = 5 * 60 // 5 minutes in seconds