package consts

const (
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
)

var (
    ErrObjectExists     = "object already exists"
    ErrObjectNotFound   = "object not found"
    ErrInvalidID        = "invalid object ID"
    ErrCodeTooLarge     = "code size exceeds maximum"
    ErrStorageTooLarge  = "storage size exceeds maximum"
    ErrInvalidFunction  = "invalid function call"
    ErrRegionExists     = "region already exists"
    ErrRegionNotFound   = "region not found"
    ErrInvalidTEE       = "invalid TEE address"
    // New attestation errors
    ErrMissingAttestation    = "missing TEE attestation"
    ErrInvalidAttestation    = "invalid TEE attestation"
    ErrAttestationMismatch   = "attestation pair mismatch"
    ErrInvalidTimestamp      = "invalid Roughtime stamp"
    ErrStaleTimestamp        = "timestamp outside valid window"
    ErrInvalidRegionExec     = "invalid regional execution"
)
// Define attestation types
type AttestationType uint8

const (
    AttestationSGX AttestationType = iota
    AttestationSEV
)
// Maximum allowed drift for Roughtime stamps
const MaxTimeDrift = 5 * 60 // 5 minutes in seconds
