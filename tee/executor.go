package tee

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/rhombus-tech/vm/core"
	"github.com/rhombus-tech/vm/rustconnector"
)

// Executor handles regional TEE execution
type Executor struct {
    connector  *rustconnector.RustConnector
    mu         sync.RWMutex
}

// Config defines executor configuration
type ExecutorConfig struct {  
    ControllerPath string
    WasmPath      string
    Debug         bool
}

// New creates a new TEE executor
func New(cfg *ExecutorConfig) (*Executor, error) {
    // Initialize Rust connector
    connector := rustconnector.New(
        cfg.ControllerPath,
        cfg.WasmPath,
        cfg.Debug,
    )

    return &Executor{
        connector: connector,
    }, nil
}

// Execute runs code in both TEEs and verifies results
func (e *Executor) Execute(
    ctx context.Context,
    input []byte,
    regionID string,
) (*core.ExecutionResult, error) {
    e.mu.Lock()
    defer e.mu.Unlock()

    // Execute in both TEEs
    sgxResult, err := e.connector.ExecuteSGX(ctx, input)
    if err != nil {
        return nil, fmt.Errorf("SGX execution failed: %w", err)
    }

    sevResult, err := e.connector.ExecuteSEV(ctx, input)
    if err != nil {
        return nil, fmt.Errorf("SEV execution failed: %w", err)
    }

    // Verify results match
    if err := e.verifyResults(sgxResult, sevResult); err != nil {
        return nil, fmt.Errorf("result verification failed: %w", err)
    }

    // Combine attestations
    result := &core.ExecutionResult{
        Output:       sgxResult.Output,
        StateHash:    sgxResult.StateHash,
        RegionID:     regionID,
        Attestations: [2]core.TEEAttestation{
            sgxResult.Attestations[0],
            sevResult.Attestations[0],
        },
    }

    return result, nil
}

// verifyResults checks that outputs from both TEEs match
func (e *Executor) verifyResults(
    sgx *core.ExecutionResult,
    sev *core.ExecutionResult,
) error {
    if string(sgx.Output) != string(sev.Output) {
        return fmt.Errorf("output mismatch between TEEs")
    }

    if string(sgx.StateHash) != string(sev.StateHash) {
        return fmt.Errorf("state hash mismatch between TEEs")
    }

    // Verify attestations
    if err := e.verifyAttestation(sgx.Attestations[0]); err != nil {
        return fmt.Errorf("SGX attestation invalid: %w", err)
    }
    if err := e.verifyAttestation(sev.Attestations[0]); err != nil {
        return fmt.Errorf("SEV attestation invalid: %w", err)
    }

    return nil
}

func (e *Executor) verifyAttestation(att core.TEEAttestation) error {
    if len(att.EnclaveID) == 0 {
        return fmt.Errorf("missing enclave ID")
    }
    if len(att.Measurement) == 0 {
        return fmt.Errorf("missing measurement")
    }
    
    // Check timestamp
    if att.Timestamp.IsZero() {
        return fmt.Errorf("missing timestamp")
    }
    
    // Optional: Check if timestamp is within reasonable range (e.g., ±5 minutes)
    now := time.Now()
    diff := now.Sub(att.Timestamp)
    if diff > 5*time.Minute || diff < -5*time.Minute {
        return fmt.Errorf("timestamp outside acceptable range")
    }
    
    if len(att.Data) == 0 {
        return fmt.Errorf("missing attestation data")
    }
    return nil
}