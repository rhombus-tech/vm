package compute

import (
	"time"

	"github.com/rhombus-tech/vm/core"
	"github.com/rhombus-tech/vm/timeserver"
)

type ExecutionRequest struct {
    IdTo         string                        `json:"id_to"`
    FunctionCall string                        `json:"function_call"`
    Parameters   []byte                        `json:"parameters"`
    RegionId     string                        `json:"region_id"`
    TimeProof    *timeserver.VerifiedTimestamp `json:"time_proof"` // Changed from Timestamp to TimeProof
}


type ExecutionResult struct {
    StateHash    []byte                 `json:"state_hash"`
    Output       []byte                 `json:"output"`
    Attestations [2]core.TEEAttestation `json:"attestations"`
    Timestamp    string                 `json:"timestamp"`
    ID           string                 `json:"id"`      // Changed from PairID to ID
    RegionID     string                 `json:"region_id"`
}

type TEEAttestation struct {
    EnclaveID   []byte    `json:"enclave_id"`
    Measurement []byte    `json:"measurement"`
    Timestamp   time.Time `json:"timestamp"`
    Data        []byte    `json:"data"`
    RegionProof []byte    `json:"region_proof"`
}

