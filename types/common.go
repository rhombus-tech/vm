// types/common.go
package types

import (
    "time"
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
    EnclaveID   []byte
    Measurement []byte
    Timestamp   string
    Data        []byte
    Signature   []byte
    RegionProof []byte
}