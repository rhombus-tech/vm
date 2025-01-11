package compute

import (
    "time"

"github.com/rhombus-tech/vm/core"

)

type ExecutionRequest struct {
    IdTo         string `json:"id_to"`
    FunctionCall string `json:"function_call"`
    Parameters   []byte `json:"parameters"`
    RegionId     string `json:"region_id"`
    // Add any additional fields needed by the Rust bridge
}

type ExecutionResult struct {
    StateHash    []byte                 `json:"state_hash"`    // Add JSON tags
    Result       []byte                 `json:"result"`
    Attestations [2]core.TEEAttestation `json:"attestations"`
    Timestamp    string                 `json:"timestamp"`
    RegionID     string                 `json:"region_id"`     // Add region tracking
}

type TEEAttestation struct {
    EnclaveID   []byte    `json:"enclave_id"`
    Measurement []byte    `json:"measurement"`
    Timestamp   time.Time `json:"timestamp"`
    Data        []byte    `json:"data"`
    RegionProof []byte    `json:"region_proof"`
}