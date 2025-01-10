package compute

import "github.com/rhombus-tech/vm/core"

type ExecutionResult struct {
    StateHash    []byte
    Result       []byte
    Attestations [2]core.TEEAttestation
    Timestamp    string
}