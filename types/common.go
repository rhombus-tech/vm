package types

import "time"

type ObjectState struct {
    Code        []byte    `json:"code"`
    Storage     []byte    `json:"storage"`
    RegionID    string    `json:"region_id"`
    Events      []string  `json:"events"`
    LastUpdated time.Time `json:"last_updated"`
    Status      string    `json:"status"`
}

type TEEAttestation struct {
    EnclaveID   []byte `json:"enclave_id"`
    Measurement []byte `json:"measurement"`
    Timestamp   string `json:"timestamp"`
    Data        []byte `json:"data"`
    Signature   []byte `json:"signature"`
    RegionProof []byte `json:"region_proof"`
}