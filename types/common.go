// types/common.go
package types

import (
    "time"

    "github.com/ava-labs/hypersdk/codec"
)

type TEEAddress []byte

type ObjectState struct {
    Code        []byte
    Storage     []byte
    RegionID    string
    Events      []string
    LastUpdated time.Time
    Status      string
}

type Event struct {
    FunctionCall string
    Parameters   []byte
    Attestations [2]TEEAttestation
    Timestamp    string
    Status      string
}

type TEEAttestation struct {
    EnclaveID   []byte    `serialize:"true" json:"enclave_id"`
    Measurement []byte    `serialize:"true" json:"measurement"`
    Timestamp   time.Time `serialize:"true" json:"timestamp"`
    Data        []byte    `serialize:"true" json:"data"`
    Signature   []byte    `serialize:"true" json:"signature"`
    RegionProof []byte    `serialize:"true" json:"region_proof"`
}

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

// When using timestamps in the event creation:
eventID := fmt.Sprintf("%s:%s", s.IDTo, s.Attestations[0].Timestamp.UTC().Format(time.RFC3339))