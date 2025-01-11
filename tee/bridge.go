package tee

import (
    "context"
    "encoding/json"
    "fmt"
    "io/ioutil"
    "os"
    "os/exec"
    "time"

    "github.com/rhombus-tech/vm/core"
)

type RustBridge struct {
    controllerPath string
    wasmPath      string
}

func NewRustBridge(controllerPath string, wasmPath string) *RustBridge {
    return &RustBridge{
        controllerPath: controllerPath,
        wasmPath:      wasmPath,
    }
}

// Update rust types to match core types better
type rustExecutionResult struct {
    ResultHash   [32]byte           `json:"result_hash"`
    Result       []byte             `json:"result"`
    Attestations [2]rustAttestation `json:"attestations"` // Changed to array of 2
    RegionID     string             `json:"region_id"`
}

type rustAttestation struct {
    EnclaveType    string    `json:"enclave_type"`
    EnclaveID      []byte    `json:"enclave_id"`
    Measurement    []byte    `json:"measurement"`
    Timestamp      uint64    `json:"timestamp"`
    PlatformData   []byte    `json:"platform_data"`
    RegionProof    []byte    `json:"region_proof"`
}

// Update Execute to return core.ExecutionResult
func (rb *RustBridge) Execute(ctx context.Context, req *ExecutionRequest) (*core.ExecutionResult, error) {
    // Create temporary file for input parameters
    inputFile, err := createTempFile(req.Parameters)
    if err != nil {
        return nil, fmt.Errorf("failed to create input file: %w", err)
    }
    defer cleanupTempFile(inputFile)

    // Execute the Rust controller
    cmd := exec.CommandContext(ctx, rb.controllerPath,
        "--wasm-module", rb.wasmPath,
        "--input", inputFile.path,
        "--region", req.RegionId,
        "--function", req.FunctionCall,
        "--verbose",
    )
    
    output, err := cmd.Output()
    if err != nil {
        return nil, fmt.Errorf("rust execution failed: %w", err)
    }

    // Parse Rust result
    var rustResult rustExecutionResult
    if err := json.Unmarshal(output, &rustResult); err != nil {
        return nil, fmt.Errorf("failed to parse rust result: %w", err)
    }

    // Convert to core.ExecutionResult
    return convertToCoreResult(rustResult)
}

// Add conversion helper
func convertToCoreResult(rust rustExecutionResult) (*core.ExecutionResult, error) {
    attestations := [2]core.TEEAttestation{}
    
    for i, rustAtt := range rust.Attestations {
        timestamp := time.Unix(int64(rustAtt.Timestamp), 0)
        
        attestations[i] = core.TEEAttestation{
            EnclaveID:   rustAtt.EnclaveID,
            Measurement: rustAtt.Measurement,
            Timestamp:   timestamp,
            Data:        rustAtt.PlatformData,
            RegionProof: rustAtt.RegionProof,
        }
    }

    return &core.ExecutionResult{
        StateHash:    rust.ResultHash[:],
        Output:       rust.Result,
        Attestations: attestations,
        RegionID:     rust.RegionID,
    }, nil
}

// Add ExecutionRequest type
type ExecutionRequest struct {
    IdTo         string `json:"id_to"`
    FunctionCall string `json:"function_call"`
    Parameters   []byte `json:"parameters"`
    RegionId     string `json:"region_id"`
}

// Keep helper functions
type TempFile struct {
    *os.File
    path string
}

func createTempFile(data []byte) (*TempFile, error) {
    tmpFile, err := ioutil.TempFile("", "tee-execution-*")
    if err != nil {
        return nil, err
    }

    if data != nil {
        if _, err := tmpFile.Write(data); err != nil {
            tmpFile.Close()
            os.Remove(tmpFile.Name())
            return nil, err
        }
    }

    return &TempFile{
        File: tmpFile,
        path: tmpFile.Name(),
    }, nil
}

func cleanupTempFile(f *TempFile) {
    if f != nil {
        f.Close()
        os.Remove(f.path)
    }
}

func (rb *RustBridge) ValidatePlatforms(ctx context.Context) error {
    cmd := exec.CommandContext(ctx, rb.controllerPath, "verify-platforms")
    if err := cmd.Run(); err != nil {
        return fmt.Errorf("platform validation failed: %w", err)
    }
    return nil
}

func (rb *RustBridge) Close() error {
    return nil
}