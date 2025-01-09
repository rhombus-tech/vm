package compute

import (
    "context"
    "encoding/json"
    "errors"
    "fmt"
    "io/ioutil"
    "os"
    "os/exec"
    "sync"
    "time"

    "github.com/rhombus-tech/vm/coordination"
    pb "github.com/rhombus-tech/vm/tee/proto"
)

var (
    ErrNoRegion       = errors.New("no region specified")
    ErrNodeBusy       = errors.New("node at capacity")
    ErrInvalidRequest = errors.New("invalid execution request")
)

type ComputeNode struct {
    pb.UnimplementedTeeExecutionServer

    regionID    string
    maxTasks    int
    activeTasks int
    taskLock    sync.Mutex
    
    wasmPath    string
    controller  string

    coordinator *coordination.Coordinator
    workers     map[string]*coordination.Worker
    workerLock  sync.RWMutex
}

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

type AttestationReport struct {
    EnclaveType    string    `json:"enclave_type"`
    EnclaveID      []byte    `json:"enclave_id"`
    Measurement    [32]byte  `json:"measurement"`
    Timestamp      uint64    `json:"timestamp"`
    PlatformData   []byte    `json:"platform_data"`
}

func NewComputeNode(regionID string, config *Config) (*ComputeNode, error) {
    coordConfig := &coordination.Config{
        MinWorkers:      2,
        MaxWorkers:      2,
        WorkerTimeout:   30 * time.Second,
        ChannelTimeout:  10 * time.Second,
    }

    coord, err := coordination.NewCoordinator(coordConfig, config.DB)
    if err != nil {
        return nil, fmt.Errorf("failed to create coordinator: %w", err)
    }

    node := &ComputeNode{
        regionID:    regionID,
        maxTasks:    config.MaxTasks,
        wasmPath:    config.WasmPath,
        controller:  config.ControllerPath,
        coordinator: coord,
        workers:     make(map[string]*coordination.Worker),
    }

    if err := node.registerWorkers(); err != nil {
        return nil, err
    }

    if err := coord.Start(); err != nil {
        return nil, fmt.Errorf("failed to start coordinator: %w", err)
    }

    return node, nil
}

func (n *ComputeNode) registerWorkers() error {
    sgxWorker := coordination.WorkerID(fmt.Sprintf("sgx-%s", n.regionID))
    if err := n.coordinator.RegisterWorker(context.Background(), sgxWorker, []byte("sgx-enclave")); err != nil {
        return fmt.Errorf("failed to register SGX worker: %w", err)
    }

    sevWorker := coordination.WorkerID(fmt.Sprintf("sev-%s", n.regionID))
    if err := n.coordinator.RegisterWorker(context.Background(), sevWorker, []byte("sev-enclave")); err != nil {
        return fmt.Errorf("failed to register SEV worker: %w", err)
    }

    return nil
}

func (n *ComputeNode) acquireTaskSlot() bool {
    n.taskLock.Lock()
    defer n.taskLock.Unlock()

    if n.activeTasks >= n.maxTasks {
        return false
    }
    n.activeTasks++
    return true
}

func (n *ComputeNode) releaseTaskSlot() {
    n.taskLock.Lock()
    n.activeTasks--
    n.taskLock.Unlock()
}

func (n *ComputeNode) Execute(ctx context.Context, req *pb.ExecutionRequest) (*pb.ExecutionResult, error) {
    if req.RegionId == "" {
        return nil, ErrNoRegion
    }
    if req.RegionId != n.regionID {
        return nil, fmt.Errorf("wrong region: want %s, got %s", n.regionID, req.RegionId)
    }

    if !n.acquireTaskSlot() {
        return nil, ErrNodeBusy
    }
    defer n.releaseTaskSlot()

    // Create coordination task
    task := &coordination.Task{
        ID: fmt.Sprintf("task-%s-%d", n.regionID, time.Now().UnixNano()),
        WorkerIDs: []coordination.WorkerID{
            coordination.WorkerID(fmt.Sprintf("sgx-%s", n.regionID)),
            coordination.WorkerID(fmt.Sprintf("sev-%s", n.regionID)),
        },
        Data:    req.Parameters,
        Timeout: 5 * time.Minute,
    }

    // Submit task to coordinator
    if err := n.coordinator.SubmitTask(ctx, task); err != nil {
        return nil, fmt.Errorf("coordination failed: %w", err)
    }

    // Execute in TEEs
    results, err := n.executeInTEEs(ctx, req.Parameters)
    if err != nil {
        return nil, err
    }

    // Establish secure channels
    channels, err := n.establishSecureChannels(ctx, results)
    if err != nil {
        return nil, fmt.Errorf("failed to establish secure channels: %w", err)
    }

    // Exchange verification messages
    if err := n.exchangeVerification(ctx, channels, results); err != nil {
        return nil, fmt.Errorf("verification failed: %w", err)
    }

    return n.createExecutionResult(results), nil
}

type executionResults struct {
    SGX ExternalResult
    SEV ExternalResult
}

type ExternalResult struct {
    ResultHash   [32]byte           `json:"result_hash"`
    Result       []byte             `json:"result"`
    Attestation  AttestationReport  `json:"attestation"`
}

func (n *ComputeNode) executeInTEEs(ctx context.Context, params []byte) (*executionResults, error) {
    inputFile, err := createTempFile(params)
    if err != nil {
        return nil, fmt.Errorf("failed to create input file: %w", err)
    }
    defer cleanupTempFile(inputFile)

    outputFile, err := createTempFile(nil)
    if err != nil {
        return nil, fmt.Errorf("failed to create output file: %w", err)
    }
    defer cleanupTempFile(outputFile)

    cmd := exec.CommandContext(ctx, n.controller,
        "--wasm-module", n.wasmPath,
        "--input", inputFile.path,
        "--output", outputFile.path,
        "--verbose",
    )

    output, err := cmd.CombinedOutput()
    if err != nil {
        return nil, fmt.Errorf("execution failed: %w: %s", err, string(output))
    }

    var results executionResults
    if err := json.Unmarshal(output, &results); err != nil {
        return nil, fmt.Errorf("failed to parse result: %w", err)
    }

    return &results, nil
}

func (n *ComputeNode) establishSecureChannels(ctx context.Context, results *executionResults) (map[string]*coordination.SecureChannel, error) {
    channels := make(map[string]*coordination.SecureChannel)

    channel := coordination.NewSecureChannel(
        coordination.WorkerID(fmt.Sprintf("sgx-%s", n.regionID)),
        coordination.WorkerID(fmt.Sprintf("sev-%s", n.regionID)),
    )

    if err := channel.EstablishSecure(); err != nil {
        return nil, err
    }

    channels["sgx-sev"] = channel
    return channels, nil
}

func (n *ComputeNode) exchangeVerification(ctx context.Context, channels map[string]*coordination.SecureChannel, results *executionResults) error {
    msg := &coordination.Message{
        Type: coordination.MessageTypeVerification,
        Data: results.SGX.ResultHash[:],
    }

    for _, channel := range channels {
        if err := channel.Send(msg.Data); err != nil {
            return err
        }
    }

    return nil
}

func (n *ComputeNode) createExecutionResult(results *executionResults) *pb.ExecutionResult {
    sgxAtt := &pb.TEEAttestation{
        EnclaveId:   results.SGX.Attestation.EnclaveID,
        Measurement: results.SGX.Attestation.Measurement[:],
        Timestamp:   time.Unix(int64(results.SGX.Attestation.Timestamp), 0).Format(time.RFC3339),
        Data:        results.SGX.Attestation.PlatformData,
        RegionProof: []byte{},
    }

    sevAtt := &pb.TEEAttestation{
        EnclaveId:   results.SEV.Attestation.EnclaveID,
        Measurement: results.SEV.Attestation.Measurement[:],
        Timestamp:   time.Unix(int64(results.SEV.Attestation.Timestamp), 0).Format(time.RFC3339),
        Data:        results.SEV.Attestation.PlatformData,
        RegionProof: []byte{},
    }

    return &pb.ExecutionResult{
        Timestamp:    time.Now().UTC().Format(time.RFC3339),
        Attestations: []*pb.TEEAttestation{sgxAtt, sevAtt},
        StateHash:    results.SGX.ResultHash[:],
        Result:       results.SGX.Result,
    }
}

func (n *ComputeNode) GetRegions(_ context.Context, _ *pb.GetRegionsRequest) (*pb.GetRegionsResponse, error) {
    region := &pb.Region{
        Id:        n.regionID,
        CreatedAt: time.Now().Format(time.RFC3339),
        WorkerIds: []string{
            fmt.Sprintf("sgx-%s", n.regionID),
            fmt.Sprintf("sev-%s", n.regionID),
        },
    }
    return &pb.GetRegionsResponse{Regions: []*pb.Region{region}}, nil
}

func (n *ComputeNode) Close() error {
    if n.coordinator != nil {
        return n.coordinator.Stop()
    }
    return nil
}