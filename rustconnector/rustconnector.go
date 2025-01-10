// Package rustconnector provides integration with the Rust TEE implementation
package rustconnector

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"sync"
	"time"

	"github.com/rhombus-tech/vm/core"
)

// RustConnector handles interaction with the Rust TEE implementation
type RustConnector struct {
    controllerPath string
    wasmPath      string
    verbose       bool
    mu            sync.Mutex
}

// ExecutionRequest represents input for TEE execution
type ExecutionRequest struct {
    ExecutionID uint64          `json:"execution_id"`
    Input      []byte          `json:"input"`
    Params     ExecutionParams `json:"params"`
}

// ExecutionParams defines execution parameters
type ExecutionParams struct {
    ExpectedHash  *[32]byte `json:"expected_hash,omitempty"`
    DetailedProof bool      `json:"detailed_proof"`
}

// ExecutionResult holds output from TEE execution
type ExecutionResult struct {
    ResultHash  [32]byte         `json:"result_hash"`
    Result      []byte           `json:"result"`
    Attestation AttestationProof `json:"attestation"`
}

// AttestationProof contains TEE attestation data
type AttestationProof struct {
    EnclaveType     string   `json:"enclave_type"`
    Measurement     [32]byte `json:"measurement"`
    Timestamp       uint64   `json:"timestamp"`
    PlatformData    []byte   `json:"platform_data"`
}

// New creates a new RustConnector instance
func New(controllerPath, wasmPath string, verbose bool) *RustConnector {
    return &RustConnector{
        controllerPath: controllerPath,
        wasmPath:      wasmPath,
        verbose:       verbose,
    }
}

// ExecuteSGX executes code in SGX TEE
func (rc *RustConnector) ExecuteSGX(ctx context.Context, input []byte) (*core.ExecutionResult, error) {
    rc.mu.Lock()
    defer rc.mu.Unlock()

    // Create temp file for input
    inputFile, err := os.CreateTemp("", "tee-sgx-input-*")
    if err != nil {
        return nil, fmt.Errorf("failed to create input file: %w", err)
    }
    defer os.Remove(inputFile.Name())
    defer inputFile.Close()

    // Write input data
    if _, err := inputFile.Write(input); err != nil {
        return nil, fmt.Errorf("failed to write input: %w", err)
    }

    // Execute in SGX TEE
    cmd := exec.CommandContext(ctx, rc.controllerPath,
        "--wasm-module", rc.wasmPath,
        "--input", inputFile.Name(),
        "--backend", "sgx",
        "--verbose", fmt.Sprintf("%v", rc.verbose),
    )

    output, err := cmd.Output()
    if err != nil {
        return nil, fmt.Errorf("SGX execution failed: %w", err)
    }

    // Parse result
    var rustResult ExecutionResult
    if err := json.Unmarshal(output, &rustResult); err != nil {
        return nil, fmt.Errorf("failed to parse result: %w", err)
    }

    // Convert to core.ExecutionResult
    return &core.ExecutionResult{
		Output:       rustResult.Result,
		StateHash:    rustResult.ResultHash[:],
		RegionID:     "",
		Attestations: [2]core.TEEAttestation{
			{
				EnclaveID:   []byte("sgx-enclave"),
				Measurement: rustResult.Attestation.Measurement[:],
				Timestamp:   time.Unix(int64(rustResult.Attestation.Timestamp), 0), // Convert uint64 to time.Time
				Data:        rustResult.Attestation.PlatformData,
			},
		},
	}, nil
}

// ExecuteSEV executes code in SEV TEE
func (rc *RustConnector) ExecuteSEV(ctx context.Context, input []byte) (*core.ExecutionResult, error) {
    rc.mu.Lock()
    defer rc.mu.Unlock()

    // Similar to ExecuteSGX but with SEV backend
    inputFile, err := os.CreateTemp("", "tee-sev-input-*")
    if err != nil {
        return nil, fmt.Errorf("failed to create input file: %w", err)
    }
    defer os.Remove(inputFile.Name())
    defer inputFile.Close()

    if _, err := inputFile.Write(input); err != nil {
        return nil, fmt.Errorf("failed to write input: %w", err)
    }

    cmd := exec.CommandContext(ctx, rc.controllerPath,
        "--wasm-module", rc.wasmPath,
        "--input", inputFile.Name(),
        "--backend", "sev",
        "--verbose", fmt.Sprintf("%v", rc.verbose),
    )

    output, err := cmd.Output()
    if err != nil {
        return nil, fmt.Errorf("SEV execution failed: %w", err)
    }

    var rustResult ExecutionResult
    if err := json.Unmarshal(output, &rustResult); err != nil {
        return nil, fmt.Errorf("failed to parse result: %w", err)
    }

	return &core.ExecutionResult{
		Output:       rustResult.Result,
		StateHash:    rustResult.ResultHash[:],
		RegionID:     "",
		Attestations: [2]core.TEEAttestation{
			{
				EnclaveID:   []byte("sev-enclave"),
				Measurement: rustResult.Attestation.Measurement[:],
				Timestamp:   time.Unix(int64(rustResult.Attestation.Timestamp), 0), // Convert uint64 to time.Time
				Data:        rustResult.Attestation.PlatformData,
			},
		},
	}, nil
	
}

// VerifyPlatforms checks if both TEE types are available
func (rc *RustConnector) VerifyPlatforms(ctx context.Context) (bool, bool, error) {
    cmd := exec.CommandContext(ctx, rc.controllerPath, "verify-platforms")
    output, err := cmd.Output()
    if err != nil {
        return false, false, fmt.Errorf("platform verification failed: %w", err)
    }

    var result struct {
        SGX bool `json:"sgx"`
        SEV bool `json:"sev"` 
    }
    if err := json.Unmarshal(output, &result); err != nil {
        return false, false, fmt.Errorf("failed to parse platform info: %w", err)
    }

    return result.SGX, result.SEV, nil
}