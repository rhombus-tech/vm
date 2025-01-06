// tee/executor.go
package tee

import (
    "context"
    "github.com/rhombus-tech/vm/core"
)

type Executor struct {
    config Config
    wasmModule []byte
    platform Platform 
}

func NewExecutor(config *Config) (*Executor, error) {
    platform, err := NewPlatform(config.EnclaveType, config)
    if err != nil {
        return nil, err
    }

    return &Executor{
        platform: platform,
        config:   config,
    }, nil
}

func (e *Executor) Execute(ctx context.Context, input []byte) (*core.ExecutionResult, error) {
    // Execute WASM in TEE
    result, err := e.platform.Execute(ctx, e.wasmModule, input)
    if err != nil {
        return nil, err
    }

    // Get attestation
    attestation, err := e.platform.GetAttestation(ctx)
    if err != nil {
        return nil, err
    }

    return &core.ExecutionResult{
        ResultHash: result.Hash,
        Result: result.Data,
        Attestation: attestation,
    }, nil
}