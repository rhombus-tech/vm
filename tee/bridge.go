// tee/bridge.go
package tee

import (
	"context"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"os"
	"os/exec"
	"time"

	pb "github.com/rhombus-tech/vm/tee/proto/pb"
)

type RustBridge struct {
    controllerPath string
    wasmPath      string // Path to WASM module
}

func NewRustBridge(controllerPath string, wasmPath string) *RustBridge {
    return &RustBridge{
        controllerPath: controllerPath,
        wasmPath:      wasmPath,
    }
}

type rustExecutionResult struct {
    ResultHash   [32]byte          `json:"result_hash"`
    Result       []byte            `json:"result"`
    Attestation  rustAttestation   `json:"attestation"`
}

type rustAttestation struct {
    EnclaveType    string    `json:"enclave_type"`
    Measurement    [32]byte  `json:"measurement"`
    Timestamp      uint64    `json:"timestamp"`
    PlatformData   []byte    `json:"platform_data"`
}

func (rb *RustBridge) Execute(ctx context.Context, req *pb.ExecutionRequest) (*pb.ExecutionResult, error) {
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

    // Convert to protobuf format
    attestations := []*pb.TEEAttestation{
        convertRustAttestation(rustResult.Attestation, "sgx"),
        convertRustAttestation(rustResult.Attestation, "sev"),
    }

    return &pb.ExecutionResult{
        StateHash:    rustResult.ResultHash[:],
        Result:       rustResult.Result,
        Attestations: attestations,
        Timestamp:    time.Unix(int64(rustResult.Attestation.Timestamp), 0).Format(time.RFC3339),
    }, nil
}

func convertRustAttestation(att rustAttestation, teeType string) *pb.TEEAttestation {
    return &pb.TEEAttestation{
        EnclaveId:   []byte(teeType), // You may want to generate proper enclave IDs
        Measurement: att.Measurement[:],
        Timestamp:   time.Unix(int64(att.Timestamp), 0).Format(time.RFC3339),
        Data:        att.PlatformData,
        RegionProof: []byte{}, // Add region proof if needed
    }
}

// Helper functions from your compute package
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
    // Nothing to clean up for now
    return nil
}