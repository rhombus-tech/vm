package compute

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/ioutil"
	"log"
	"net"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/ava-labs/avalanchego/x/merkledb"
	"github.com/rhombus-tech/vm/coordination"
	"github.com/rhombus-tech/vm/core"
	"github.com/rhombus-tech/vm/storage"
	"github.com/rhombus-tech/vm/tee"
	"github.com/rhombus-tech/vm/tee/proto"
	"google.golang.org/grpc"
)

var (
    ErrNoRegion       = errors.New("no region specified")
    ErrNodeBusy       = errors.New("node at capacity")
    ErrInvalidRequest = errors.New("invalid execution request")
)

type ComputeNode struct {
    proto.UnimplementedTeeExecutionServer

    regionID    string
    maxTasks    int
    activeTasks int
    taskLock    sync.Mutex

    bridge      *tee.RustBridge
    coordinator *coordination.Coordinator
    workers     map[string]*coordination.Worker
    workerLock  sync.RWMutex
}

// TempFile can stay the same as it's still useful for handling temporary files
type TempFile struct {
    *os.File
    path string
}

// Keep createTempFile and cleanupTempFile helpers
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

// Update AttestationReport to match RustBridge format
type AttestationReport struct {
    EnclaveType    string    `json:"enclave_type"`
    EnclaveID      []byte    `json:"enclave_id"`
    Measurement    []byte    `json:"measurement"`  // Changed from [32]byte to []byte to be more flexible
    Timestamp      time.Time `json:"timestamp"`    // Changed from uint64 to time.Time
    PlatformData   []byte    `json:"platform_data"`
    RegionProof    []byte    `json:"region_proof"` // Added for region verification
}

func NewComputeNode(config *Config) (*ComputeNode, error) {
    // Create database wrapper
    dbWrapper := storage.NewDatabaseWrapper(config.DB)

    // Initialize merkleDB
    merkleDB, err := merkledb.New(
        context.Background(),
        config.DB,
        merkledb.Config{
            HistoryLength: 256,
        },
    )
    if err != nil {
        return nil, fmt.Errorf("failed to create merkledb: %w", err)
    }

    // Create coordination storage wrapper specifically for coordination package
    coordStorage := storage.NewCoordinationStorageWrapper(dbWrapper)

    // Create coordinator config
    coordConfig := &coordination.Config{
        MinWorkers:         2,
        MaxWorkers:         10,
        WorkerTimeout:      30 * time.Second,
        ChannelTimeout:     10 * time.Second,
        TaskTimeout:        5 * time.Minute,
        MaxTasks:          100,
        TaskQueueSize:     1000,
        EncryptionEnabled: true,
        RequireAttestation: true,
        AttestationTimeout: 5 * time.Second,
        StoragePath:       fmt.Sprintf("/tmp/coordinator-%s", config.RegionID),
        PersistenceEnabled: true,
    }

    // Create coordinator using coordination storage wrapper
    coordinator, err := coordination.NewCoordinator(
        coordConfig,
        merkleDB,
        coordStorage, // Use coordStorage instead of storageWrapper
    )
    if err != nil {
        return nil, fmt.Errorf("failed to create coordinator: %w", err)
    }

    // Create RustBridge
    bridge := tee.NewRustBridge(
        config.ControllerPath,
        config.WasmPath,
    )

    // Initialize node with correct fields
    node := &ComputeNode{
        regionID:    config.RegionID,
        maxTasks:    config.MaxTasks,
        activeTasks: 0,
        bridge:      bridge,
        coordinator: coordinator,
        workers:     make(map[string]*coordination.Worker),
    }

    return node, nil
}

// Add helper to register workers
func (n *ComputeNode) registerWorkers() error {
    n.workerLock.Lock()
    defer n.workerLock.Unlock()

    // Register SGX worker
    sgxWorker := coordination.WorkerID(fmt.Sprintf("sgx-%s", n.regionID))
    sgx := &coordination.Worker{
        ID: sgxWorker,
        EnclaveID: []byte("sgx-enclave"),
    }
    n.workers[string(sgxWorker)] = sgx

    // Register SEV worker
    sevWorker := coordination.WorkerID(fmt.Sprintf("sev-%s", n.regionID))
    sev := &coordination.Worker{
        ID: sevWorker,
        EnclaveID: []byte("sev-enclave"),
    }
    n.workers[string(sevWorker)] = sev

    // Register with coordinator
    for _, worker := range n.workers {
        if err := n.coordinator.RegisterWorker(context.Background(), worker.ID, worker.EnclaveID); err != nil {
            return fmt.Errorf("failed to register worker %s: %w", worker.ID, err)
        }
    }

    return nil
}

func convertToRustRequest(req *proto.ExecutionRequest) *tee.ExecutionRequest {
    return &tee.ExecutionRequest{
        IdTo:         req.IdTo,
        FunctionCall: req.FunctionCall,
        Parameters:   req.Parameters,
        RegionId:     req.RegionId,
    }
}

func convertToProtoResult(result *core.ExecutionResult) *proto.ExecutionResult {
    attestations := make([]*proto.TEEAttestation, len(result.Attestations))
    for i, att := range result.Attestations {
        attestations[i] = &proto.TEEAttestation{
            EnclaveId:   att.EnclaveID,
            Measurement: att.Measurement,
            Timestamp:   att.Timestamp.Format(time.RFC3339),
            Data:        att.Data,
            RegionProof: att.RegionProof,
        }
    }

    return &proto.ExecutionResult{
        Timestamp:    time.Now().UTC().Format(time.RFC3339),
        Attestations: attestations,
        StateHash:    result.StateHash,
        Result:       result.Output,
    }
}

// Add conversion helpers
func (n *ComputeNode) convertToRustAttestation(att *proto.TEEAttestation) (*AttestationReport, error) {
    timestamp, err := time.Parse(time.RFC3339, att.Timestamp)
    if err != nil {
        return nil, fmt.Errorf("invalid timestamp format: %w", err)
    }

    return &AttestationReport{
        EnclaveType:  determineEnclaveType(att.EnclaveId),
        EnclaveID:    att.EnclaveId,
        Measurement:  att.Measurement,
        Timestamp:    timestamp,
        PlatformData: att.Data,
        RegionProof:  att.RegionProof,
    }, nil
}

func determineEnclaveType(enclaveID []byte) string {
    // Logic to determine if this is an SGX or SEV enclave
    // Could be based on ID prefix, format, etc.
    if bytes.HasPrefix(enclaveID, []byte("sgx-")) {
        return "SGX"
    }
    if bytes.HasPrefix(enclaveID, []byte("sev-")) {
        return "SEV"
    }
    return "UNKNOWN"
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

func (n *ComputeNode) executeTEE(ctx context.Context, req *proto.ExecutionRequest) (*core.ExecutionResult, error) {
    // Convert proto request to Rust format
    rustReq := convertToRustRequest(req)
    
    // Execute using RustBridge
    result, err := n.bridge.Execute(ctx, rustReq)
    if err != nil {
        return nil, fmt.Errorf("TEE execution failed: %w", err)
    }
    
    return result, nil
}

func (n *ComputeNode) Start(port string) error {
    lis, err := net.Listen("tcp", ":"+port)
    if err != nil {
        return fmt.Errorf("failed to listen: %w", err)
    }

    s := grpc.NewServer()
    proto.RegisterTeeExecutionServer(s, n)

    // Handle shutdown gracefully
    ctx, cancel := context.WithCancel(context.Background())
    defer cancel()

    // Set up signal handling
    sigCh := make(chan os.Signal, 1)
    signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

    // Handle shutdown in goroutine
    go func() {
        <-sigCh
        log.Println("Shutting down compute node...")
        cancel()
        s.GracefulStop()
    }()

    // Use context to control server shutdown
    go func() {
        <-ctx.Done()
        s.GracefulStop()
    }()

    log.Printf("Compute node starting on port %s for region %s", port, n.regionID)
    if err := s.Serve(lis); err != nil {
        if ctx.Err() != nil {
            log.Printf("Server stopped due to context cancellation")
            return nil
        }
        return fmt.Errorf("failed to serve: %w", err)
    }

    return nil
}

// Update Execute to use conversions
func (n *ComputeNode) Execute(ctx context.Context, req *proto.ExecutionRequest) (*proto.ExecutionResult, error) {
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

    // Execute in TEE using RustBridge
    coreResult, err := n.executeTEE(ctx, req)
    if err != nil {
        return nil, err
    }

    // Create coordination task
    task := &coordination.Task{
        ID: fmt.Sprintf("task-%s-%d", n.regionID, time.Now().UnixNano()),
        WorkerIDs: []coordination.WorkerID{
            coordination.WorkerID(coreResult.Attestations[0].EnclaveID),
            coordination.WorkerID(coreResult.Attestations[1].EnclaveID),
        },
        Data:    req.Parameters,
        Timeout: 5 * time.Minute,
    }

    // Submit task to coordinator
    if err := n.coordinator.SubmitTask(ctx, task); err != nil {
        return nil, fmt.Errorf("coordination failed: %w", err)
    }

    // Establish secure channels
    channels, err := n.establishSecureChannels(ctx, coreResult)
    if err != nil {
        return nil, err
    }
    defer func() {
        for _, ch := range channels {
            ch.Close()
        }
    }()

    // Exchange verification messages
    if err := n.exchangeVerification(ctx, channels, coreResult); err != nil {
        return nil, err
    }

    // Convert core result to proto result
    return convertToProtoResult(coreResult), nil
}


// Add helper for attestation verification
func (n *ComputeNode) verifyAttestations(ctx context.Context, attestations [2]core.TEEAttestation) error {
    // Create secure channels between TEEs
    channels := make(map[string]*coordination.SecureChannel)

    // Establish channel between SGX and SEV
    channel := coordination.NewSecureChannel(
        coordination.WorkerID(attestations[0].EnclaveID),
        coordination.WorkerID(attestations[1].EnclaveID),
    )
    if err := channel.EstablishSecure(); err != nil {
        return err
    }
    channels["sgx-sev"] = channel
    defer channel.Close()

    // Exchange verification messages
    verificationMsg := &coordination.Message{
        Type: coordination.MessageTypeVerification,
        Data: attestations[0].Data, // Use first attestation's data
    }

    if err := channel.Send(verificationMsg.Data); err != nil {
        return fmt.Errorf("failed to send verification message: %w", err)
    }

    return nil
}

// Add helper for converting attestations
func (n *ComputeNode) convertAttestations(attestations [2]core.TEEAttestation) []*proto.TEEAttestation {
    result := make([]*proto.TEEAttestation, 2)
    
    for i, att := range attestations {
        result[i] = &proto.TEEAttestation{
            EnclaveId:   att.EnclaveID,
            Measurement: att.Measurement,
            Timestamp:   att.Timestamp.Format(time.RFC3339),
            Data:        att.Data,
            RegionProof: att.RegionProof,
        }
    }
    
    return result
}


func (n *ComputeNode) establishSecureChannels(ctx context.Context, result *core.ExecutionResult) (map[string]*coordination.SecureChannel, error) {
    channels := make(map[string]*coordination.SecureChannel)

    channel := coordination.NewSecureChannel(
        coordination.WorkerID(result.Attestations[0].EnclaveID),
        coordination.WorkerID(result.Attestations[1].EnclaveID),
    )

    if err := channel.EstablishSecure(); err != nil {
        return nil, fmt.Errorf("failed to establish secure channel: %w", err)
    }

    channels["sgx-sev"] = channel
    return channels, nil
}

func (n *ComputeNode) exchangeVerification(ctx context.Context, channels map[string]*coordination.SecureChannel, result *core.ExecutionResult) error {
    msg := &coordination.Message{
        Type: coordination.MessageTypeVerification,
        Data: result.StateHash,
    }

    for _, channel := range channels {
        if err := channel.Send(msg.Data); err != nil {
            return fmt.Errorf("failed to send verification message: %w", err)
        }
    }

    return nil
}

// Update createExecutionResult to convert from RustBridge format to protobuf
func (n *ComputeNode) createExecutionResult(result *core.ExecutionResult) *proto.ExecutionResult {
    attestations := make([]*proto.TEEAttestation, 2)
    
    for i, att := range result.Attestations {
        attestations[i] = &proto.TEEAttestation{
            EnclaveId:   att.EnclaveID,
            Measurement: att.Measurement,
            Timestamp:   att.Timestamp.Format(time.RFC3339),
            Data:        att.Data,
            RegionProof: att.RegionProof,
        }
    }

    return &proto.ExecutionResult{
        Timestamp:    time.Now().UTC().Format(time.RFC3339),
        Attestations: attestations,
        StateHash:    result.StateHash,
        Result:       result.Output,
    }
}

// Keep GetRegions unchanged as it's part of the gRPC interface
func (n *ComputeNode) GetRegions(_ context.Context, _ *proto.GetRegionsRequest) (*proto.GetRegionsResponse, error) {
    region := &proto.Region{
        Id:        n.regionID,
        CreatedAt: time.Now().Format(time.RFC3339),
        WorkerIds: []string{
            fmt.Sprintf("sgx-%s", n.regionID),
            fmt.Sprintf("sev-%s", n.regionID),
        },
    }
    return &proto.GetRegionsResponse{Regions: []*proto.Region{region}}, nil
}

// Update Close to cleanup both bridge and coordinator
func (n *ComputeNode) Close() error {
    var errs []error
    
    if n.bridge != nil {
        if err := n.bridge.Close(); err != nil {
            errs = append(errs, fmt.Errorf("failed to close bridge: %w", err))
        }
    }

    if n.coordinator != nil {
        if err := n.coordinator.Stop(); err != nil {
            errs = append(errs, fmt.Errorf("failed to stop coordinator: %w", err))
        }
    }

    if len(errs) > 0 {
        return fmt.Errorf("multiple close errors: %v", errs)
    }
    return nil
}