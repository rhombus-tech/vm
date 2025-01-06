// core/tee_types.go
package core

import (
    "time"
	"bytes"
	"errors"
    "github.com/ava-labs/hypersdk/codec"
)

// TEE platform types
type TEEType uint8

const (
    TEETypeSGX TEEType = iota + 1
    TEETypeSEV
)

type TEEAddress []byte

type TEEAttestation struct {
    EnclaveID   []byte    `serialize:"true" json:"enclave_id"`
    Measurement []byte    `serialize:"true" json:"measurement"`
    Timestamp   time.Time `serialize:"true" json:"timestamp"`
    Data        []byte    `serialize:"true" json:"data"`
    Signature   []byte    `serialize:"true" json:"signature"`
    RegionProof []byte    `serialize:"true" json:"region_proof"`
}

// Unmarshal implements codec.Marshaler
func (t *TEEAttestation) Unmarshal(p *codec.Packer) {
    // EnclaveID
    p.UnpackBytes(0, false, &t.EnclaveID)
    // Measurement
    p.UnpackBytes(0, false, &t.Measurement)
    // Read the Unix seconds as a uint64 and convert to time.Time
    epochSec := p.UnpackUint64(false)
    t.Timestamp = time.Unix(int64(epochSec), 0).UTC()
    // Data
    p.UnpackBytes(0, false, &t.Data)
    // Signature
    p.UnpackBytes(0, false, &t.Signature)
    // RegionProof
    p.UnpackBytes(0, false, &t.RegionProof)
}

// Marshal implements codec.Marshaler
func (t *TEEAttestation) Marshal(p *codec.Packer) {
    // EnclaveID
    p.PackBytes(t.EnclaveID)
    // Measurement
    p.PackBytes(t.Measurement)
    // Convert time.Time to Unix seconds
    p.PackUint64(uint64(t.Timestamp.Unix()))
    // Data
    p.PackBytes(t.Data)
    // Signature
    p.PackBytes(t.Signature)
    // RegionProof
    p.PackBytes(t.RegionProof)
}

// Validation helper
func (t *TEEAttestation) Validate() error {
    if len(t.EnclaveID) == 0 {
        return ErrInvalidEnclaveID
    }
    if len(t.Measurement) == 0 {
        return ErrInvalidMeasurement
    }
    if t.Timestamp.IsZero() {
        return ErrInvalidTimestamp
    }
    if len(t.Signature) == 0 {
        return ErrMissingSignature
    }
    return nil
}

// Utility methods
func (t *TEEAttestation) GetTimeUTC() string {
    return t.Timestamp.UTC().Format(time.RFC3339)
}

func (t *TEEAttestation) Equal(other *TEEAttestation) bool {
    return bytes.Equal(t.EnclaveID, other.EnclaveID) &&
        bytes.Equal(t.Measurement, other.Measurement) &&
        t.Timestamp.Equal(other.Timestamp) &&
        bytes.Equal(t.Data, other.Data) &&
        bytes.Equal(t.Signature, other.Signature) &&
        bytes.Equal(t.RegionProof, other.RegionProof)
}

// Common TEE errors
var (
    ErrInvalidEnclaveID    = errors.New("invalid enclave ID")
    ErrInvalidMeasurement  = errors.New("invalid measurement")
    ErrInvalidTimestamp    = errors.New("invalid timestamp")
    ErrMissingSignature    = errors.New("missing signature")
    ErrInvalidTEEType      = errors.New("invalid TEE type")
)