package tee

import (
    "bytes"
    "context"
    "fmt"
    "sync"
    "time"

    "github.com/rhombus-tech/vm/core"
    "github.com/rhombus-tech/vm/rustconnector"
    "github.com/rhombus-tech/vm/timeserver"
)

type Executor struct {
    connector    *rustconnector.RustConnector
    timeVerifier *timeserver.TimeVerifier
    mu           sync.RWMutex
}

type ExecutorConfig struct {  
    ControllerPath string
    WasmPath      string
    Debug         bool
}

func New(cfg *ExecutorConfig) (*Executor, error) {
    connector := rustconnector.New(
        cfg.ControllerPath,
        cfg.WasmPath,
        cfg.Debug,
    )

    timeVerifier, err := timeserver.NewTimeVerifier()
    if err != nil {
        return nil, fmt.Errorf("failed to create time verifier: %w", err)
    }

    return &Executor{
        connector:    connector,
        timeVerifier: timeVerifier,
    }, nil
}

// Update to use core.ExecutionRequest
func (e *Executor) Execute(ctx context.Context, req *core.ExecutionRequest) (*core.ExecutionResult, error) {
    e.mu.Lock()
    defer e.mu.Unlock()

    // Get verified timestamp if not provided
    if req.TimeProof == nil {
        timestamp, err := e.timeVerifier.VerifyExecutionTime(ctx, req.RegionId)
        if err != nil {
            return nil, fmt.Errorf("failed to verify execution time: %w", err)
        }
        req.TimeProof = timestamp
    }

    sgxResult, err := e.executeSGX(ctx, req)
    if err != nil {
        return nil, fmt.Errorf("SGX execution failed: %w", err)
    }

    sevResult, err := e.executeSEV(ctx, req)
    if err != nil {
        return nil, fmt.Errorf("SEV execution failed: %w", err)
    }

    if err := e.verifyResults(sgxResult, sevResult); err != nil {
        return nil, fmt.Errorf("result verification failed: %w", err)
    }

    result := &core.ExecutionResult{
        Output:       sgxResult.Output,
        StateHash:    sgxResult.StateHash,
        RegionID:     req.RegionId,
        Attestations: [2]core.TEEAttestation{
            sgxResult.Attestations[0],
            sevResult.Attestations[0],
        },
        TimeProof:    req.TimeProof,
    }

    return result, nil
}

// Update to use core.ExecutionRequest
func (e *Executor) executeSGX(ctx context.Context, req *core.ExecutionRequest) (*core.ExecutionResult, error) {
    result, err := e.connector.ExecuteSGX(ctx, req.Parameters)
    if err != nil {
        return nil, fmt.Errorf("SGX execution failed: %w", err)
    }

    if req.TimeProof != nil {
        result.Attestations[0].Timestamp = req.TimeProof.Time
    }

    return result, nil
}

// Update to use core.ExecutionRequest
func (e *Executor) executeSEV(ctx context.Context, req *core.ExecutionRequest) (*core.ExecutionResult, error) {
    result, err := e.connector.ExecuteSEV(ctx, req.Parameters)
    if err != nil {
        return nil, fmt.Errorf("SEV execution failed: %w", err)
    }

    if req.TimeProof != nil {
        result.Attestations[0].Timestamp = req.TimeProof.Time
    }

    return result, nil
}

func (e *Executor) verifyResults(sgx, sev *core.ExecutionResult) error {
    if !bytes.Equal(sgx.Output, sev.Output) {
        return fmt.Errorf("output mismatch between TEEs")
    }

    if !bytes.Equal(sgx.StateHash, sev.StateHash) {
        return fmt.Errorf("state hash mismatch between TEEs")
    }

    // Verify attestations
    if err := e.verifyAttestation(sgx.Attestations[0]); err != nil {
        return fmt.Errorf("SGX attestation invalid: %w", err)
    }
    if err := e.verifyAttestation(sev.Attestations[0]); err != nil {
        return fmt.Errorf("SEV attestation invalid: %w", err)
    }

    // Verify timestamps match
    if !sgx.Attestations[0].Timestamp.Equal(sev.Attestations[0].Timestamp) {
        return fmt.Errorf("timestamp mismatch between attestations")
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
    
    if att.Timestamp.IsZero() {
        return fmt.Errorf("missing timestamp")
    }
    
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