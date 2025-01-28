// tests/integration/integration_test.go
package integration_test

import (
	"bytes"
	"context"
	"fmt"
	"math"
	"math/rand"
	"sync"
	"testing"
	"time"

	"github.com/ava-labs/avalanchego/ids"
	"github.com/ava-labs/avalanchego/utils/maybe"
	"github.com/ava-labs/avalanchego/x/merkledb"
	"github.com/ava-labs/hypersdk/chain"
	ginkgo "github.com/onsi/ginkgo/v2"
	"github.com/stretchr/testify/require"

	"github.com/rhombus-tech/vm/actions"
	"github.com/rhombus-tech/vm/compute"
	"github.com/rhombus-tech/vm/coordination"
	"github.com/rhombus-tech/vm/coordination/xregion"
	"github.com/rhombus-tech/vm/core"
	"github.com/rhombus-tech/vm/storage"
	"github.com/rhombus-tech/vm/tee"
	"github.com/rhombus-tech/vm/tests/mocks"
	"github.com/rhombus-tech/vm/timeserver"
	"github.com/rhombus-tech/vm/verifier"
)

type RegionMetrics struct {
    LoadFactor     float64
    LatencyMs      float64
    ErrorRate      float64
    ActiveWorkers  int
    PendingTasks   int
    LastHealthCheck time.Time
    NetworkLatency map[string]float64
    TEEMetrics     map[string]*TEEMetrics
}

type TEEMetrics struct {
    EnclaveID        []byte    `json:"enclave_id"`
    Type             string    `json:"type"`         // "SGX" or "SEV"
    LoadFactor       float64   `json:"load_factor"`  // 0.0 to 1.0
    SuccessRate      float64   `json:"success_rate"` // 0.0 to 1.0
    LastAttested     time.Time `json:"last_attested"`
    LastHealthCheck  time.Time `json:"last_health_check"`
    ExecutionTime    float64   `json:"execution_time_ms"`  // Average execution time in milliseconds
    ErrorCount       uint64    `json:"error_count"`
    TaskCount        uint64    `json:"task_count"`
    ConsecutiveErrors uint64   `json:"consecutive_errors"`
    Status           string    `json:"status"`  // "healthy", "degraded", "failed"
}

type TEEExecutor interface {
    Execute(ctx context.Context, input []byte) (*core.ExecutionResult, error)
}

type mockTEE struct {
    attestations [2]core.TEEAttestation
    results     map[string]*core.ExecutionResult
    mu          sync.RWMutex
}


func createTestAction() chain.Action {
    return &actions.CreateObjectAction{
        ID:       fmt.Sprintf("test-object-%d", time.Now().UnixNano()),
        Code:     []byte("test code"),
        Storage:  []byte("test storage"),
    }
}

func setupTestEnvironment(t *testing.T) (*MockVM, string) {
    require := require.New(t)

    // Create mock database
    mockDB := mocks.NewMockDB()

    // Create database wrapper
    dbWrapper := storage.NewDatabaseWrapper(mockDB)

    // Create merkleDB config
    merkleConfig := merkledb.Config{
        BranchFactor:  16,
        HistoryLength: 256,
    }

    // Initialize MerkleDB
    merkleDB, err := merkledb.New(context.Background(), dbWrapper, merkleConfig)
    require.NoError(err)

    // Create VM config with TEE settings
    vmConfig := &compute.Config{
        MaxTasks:    100,
        Debug:       true,
        DB:         merkleDB,
        RegionID:   "test-region",
        TEEConfig:  &core.Config{
            EnclaveType:    core.PlatformTypeSGX,
            TeeEndpoint:    "mock://tee",
            AttestationKey: []byte("test-attestation-key"),
            Debug:         true,
        },
    }

    // Create MockVM
    vm, err := NewMockVM(vmConfig)
    require.NoError(err)

    // Initialize default attestations
    now := time.Now().UTC()
    defaultAttestations := [2]core.TEEAttestation{
        {
            EnclaveID:   []byte("sgx-test-enclave-id"),  // Only changed these two lines
            Measurement: []byte("measurement1"),
            Timestamp:   now,
            Data:        []byte("test-state-hash"),
            RegionProof: []byte("region-proof"),
            Signature:   []byte("signature1"),
        },
        {
            EnclaveID:   []byte("sev-test-enclave-id"),  // Only changed these two lines
            Measurement: []byte("measurement2"),
            Timestamp:   now,
            Data:        []byte("test-state-hash"),
            RegionProof: []byte("region-proof"),
            Signature:   []byte("signature2"),
        },
    }

    // Initialize time proof
    timeProof := &timeserver.VerifiedTimestamp{
        Time: now,
        Proofs: []*timeserver.TimestampProof{
            {
                ServerID:  "server1",
                Signature: []byte("sig1"),
                Delay:     100 * time.Millisecond,
            },
            {
                ServerID:  "server2",
                Signature: []byte("sig2"),
                Delay:     100 * time.Millisecond,
            },
        },
        RegionID:   "test-region",
        QuorumSize: 2,
    }

    // Store these in the MockVM for use in tests
    vm.mu.Lock()
    vm.defaultAttestations = defaultAttestations
    vm.defaultTimeProof = timeProof
    vm.mu.Unlock()

    // Start the coordinator
    err = vm.coordinator.Start()
    require.NoError(err)

    // Register test region with proper TEE configuration
    regionID := "test-region"
    err = vm.RegisterRegion(context.Background(), regionID, "mock://sgx", "mock://sev")
    require.NoError(err)

    // Initialize region state
    vm.mu.Lock()
    vm.regions = make(map[string]bool)
    vm.regions[regionID] = true
    vm.objects = make(map[string]map[string]*core.ObjectState)
    vm.objects[regionID] = make(map[string]*core.ObjectState)
    vm.mu.Unlock()

    // Setup cleanup for test
    t.Cleanup(func() {
        if vm.coordinator != nil {
            vm.coordinator.Stop()
        }
    })

    return vm, regionID
}

func verifyTestResult(t *testing.T, result *core.ExecutionResult) {
    require := require.New(t)
    
    require.NotNil(result)
    require.NotEmpty(result.StateHash)
    require.Equal(2, len(result.Attestations))
    require.NotEmpty(result.Attestations[0].EnclaveID)
    require.NotEmpty(result.Attestations[1].EnclaveID)
}


func newMockTEE() *mockTEE {
    now := time.Now().UTC()
    attestations := [2]core.TEEAttestation{
        {
            EnclaveID:   []byte("sgx-test"),
            Measurement: []byte("sgx-measurement"),
            Timestamp:   now,
            Data:        []byte("test-state-hash"),
            RegionProof: []byte("region-1"),
            Signature:   []byte("sgx-sig"),
        },
        {
            EnclaveID:   []byte("sev-test"),
            Measurement: []byte("sev-measurement"),
            Timestamp:   now,
            Data:        []byte("test-state-hash"),
            RegionProof: []byte("region-1"),
            Signature:   []byte("sev-sig"),
        },
    }

    return &mockTEE{
        attestations: attestations,
        results:     make(map[string]*core.ExecutionResult),
    }
}
func (m *mockTEE) Execute(_ context.Context, input []byte) (*core.ExecutionResult, error) {
    m.mu.Lock()
    defer m.mu.Unlock()

    timeProof := &timeserver.VerifiedTimestamp{
        Time: time.Now(),
        Proofs: []*timeserver.TimestampProof{
            {
                ServerID:  "server1",
                Signature: []byte("signature1"),
                Delay:     100 * time.Millisecond,
            },
            {
                ServerID:  "server2",
                Signature: []byte("signature2"),
                Delay:     100 * time.Millisecond,
            },
        },
        RegionID:   "test-region",
        QuorumSize: 2,
    }

    // Use consistent test-state-hash value
    stateHash := []byte("test-state-hash")

    result := &core.ExecutionResult{
        Output:       []byte("test output"),
        StateHash:    stateHash,
        TimeProof:    timeProof,
        RegionID:     "test-region",
        Attestations: [2]core.TEEAttestation{
            {
                EnclaveID:   []byte("sgx-enclave"),
                Measurement: []byte("measurement1"),
                Timestamp:   timeProof.Time,
                Data:        stateHash,
                Signature:   []byte("sig1"),
                RegionProof: []byte("region-proof1"),
            },
            {
                EnclaveID:   []byte("sev-enclave"),
                Measurement: []byte("measurement2"),
                Timestamp:   timeProof.Time,
                Data:        stateHash,
                Signature:   []byte("sig2"),
                RegionProof: []byte("region-proof2"),
            },
        },
    }
   
    return result, nil
}

var _ chain.VM = &MockVM{}

type MockVM struct {
    chain.VM
    config              *compute.Config
    teeClient           *tee.Client
    mockTEE             TEEExecutor
    regions             map[string]bool
    objects             map[string]map[string]*core.ObjectState
    mu                  sync.RWMutex
    coordinator         *coordination.Coordinator
    db                  merkledb.MerkleDB
    stateManager        *storage.DatabaseWrapper
    defaultAttestations [2]core.TEEAttestation
    defaultTimeProof    *timeserver.VerifiedTimestamp
    networkLatency      map[string]time.Duration
    partitionedRegions  map[string]bool
    regionMetrics       map[string]*RegionMetrics
    teePairs           map[string]*TEEPairInfo    
    teeMetrics         map[string]map[string]*TEEMetrics  
    regionLoads        map[string]*RegionLoad     
}

type TEEPairInfo struct {
    SGXEnclaveID []byte
    SEVEnclaveID []byte
    Status       string
    LastUpdate   time.Time
}

type RegionLoad struct {
    SGX struct {
        LoadFactor     float64
        TaskCount      uint64
        LastOperation  time.Time
    }
    SEV struct {
        LoadFactor     float64
        TaskCount      uint64
        LastOperation  time.Time
    }
}


func NewMockVM(config *compute.Config) (*MockVM, error) {
    // Create mock database
    mockDB := mocks.NewMockDB()
    dbWrapper := storage.NewDatabaseWrapper(mockDB)

    // Initialize MerkleDB
    merkleDB, err := merkledb.New(
        context.Background(),
        dbWrapper,
        merkledb.Config{
            BranchFactor:  16,
            HistoryLength: 256,
        },
    )
    if err != nil {
        return nil, fmt.Errorf("failed to create merkledb: %w", err)
    }

    // Create coordination storage
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
        StoragePath:       "/tmp/coordinator-test",
        PersistenceEnabled: true,
    }

    // Create coordinator
    coordinator, err := coordination.NewCoordinator(coordConfig, merkleDB, coordStorage)
    if err != nil {
        return nil, fmt.Errorf("failed to create coordinator: %w", err)
    }

    mockTee := newMockTEE()
    stateVerifier := verifier.New(dbWrapper)

    teeClient, err := tee.NewClient("mock://sgx", "mock://sev", stateVerifier)
    if err != nil {
        return nil, err
    }

    // Initialize default attestations
    now := time.Now().UTC()
    defaultAttestations := [2]core.TEEAttestation{
        {
            EnclaveID:   []byte("sgx-test-enclave-1234567890"),
            Measurement: []byte("measurement1"),
            Timestamp:   now,
            Data:        []byte("test-state-hash"),
            RegionProof: []byte("region-proof"),
            Signature:   []byte("signature1"),
        },
        {
            EnclaveID:   []byte("sev-test-enclave-0987654321"),
            Measurement: []byte("measurement2"),
            Timestamp:   now,
            Data:        []byte("test-state-hash"),
            RegionProof: []byte("region-proof"),
            Signature:   []byte("signature2"),
        },
    }

    // Initialize default time proof
    defaultTimeProof := &timeserver.VerifiedTimestamp{
        Time: now,
        Proofs: []*timeserver.TimestampProof{
            {
                ServerID:  "server1",
                Signature: []byte("sig1"),
                Delay:     100 * time.Millisecond,
            },
            {
                ServerID:  "server2",
                Signature: []byte("sig2"),
                Delay:     100 * time.Millisecond,
            },
        },
        RegionID:   "default",
        QuorumSize: 2,
    }

    vm := &MockVM{
        config:             config,
        teeClient:          teeClient,
        mockTEE:            mockTee,
        regions:            make(map[string]bool),
        objects:            make(map[string]map[string]*core.ObjectState),
        coordinator:        coordinator,
        db:                merkleDB,
        stateManager:      dbWrapper,
        defaultAttestations: defaultAttestations,
        defaultTimeProof:   defaultTimeProof,
        networkLatency:     make(map[string]time.Duration),
        partitionedRegions: make(map[string]bool),
        regionMetrics:      make(map[string]*RegionMetrics),
        teePairs:           make(map[string]*TEEPairInfo),
        teeMetrics:         make(map[string]map[string]*TEEMetrics),
        regionLoads:        make(map[string]*RegionLoad),
    }

    // Initialize base metrics for each TEE type
    defaultTEEMetrics := map[string]*TEEMetrics{
        "sgx": NewTEEMetrics([]byte("sgx-test-enclave-1234567890"), "SGX"),
        "sev": NewTEEMetrics([]byte("sev-test-enclave-0987654321"), "SEV"),
    }

    // Set default load values
    defaultLoad := &RegionLoad{
        SGX: struct {
            LoadFactor     float64
            TaskCount      uint64
            LastOperation  time.Time
        }{
            LoadFactor:    0.1,
            TaskCount:     0,
            LastOperation: now,
        },
        SEV: struct {
            LoadFactor     float64
            TaskCount      uint64
            LastOperation  time.Time
        }{
            LoadFactor:    0.1,
            TaskCount:     0,
            LastOperation: now,
        },
    }

    // Create default TEE pair info
    defaultPair := &TEEPairInfo{
        SGXEnclaveID: []byte("sgx-test-enclave-1234567890"),
        SEVEnclaveID: []byte("sev-test-enclave-0987654321"),
        Status:       "healthy",
        LastUpdate:   now,
    }

    // Initialize default region
    defaultRegionID := "default"
    vm.regions[defaultRegionID] = true
    vm.teeMetrics[defaultRegionID] = defaultTEEMetrics
    vm.regionLoads[defaultRegionID] = defaultLoad
    vm.teePairs[defaultRegionID] = defaultPair
    vm.regionMetrics[defaultRegionID] = &RegionMetrics{
        TEEMetrics: defaultTEEMetrics,
    }

    return vm, nil
}

func (vm *MockVM) RegisterRegion(ctx context.Context, regionID string, sgxEndpoint, sevEndpoint string) error {
    vm.mu.Lock()
    defer vm.mu.Unlock()

    // Initialize maps if needed
    if vm.regions == nil {
        vm.regions = make(map[string]bool)
    }
    if vm.objects == nil {
        vm.objects = make(map[string]map[string]*core.ObjectState)
    }
    if vm.regionMetrics == nil {
        vm.regionMetrics = make(map[string]*RegionMetrics)
    }

    // Register region
    vm.regions[regionID] = true
    vm.objects[regionID] = make(map[string]*core.ObjectState)
    vm.regionMetrics[regionID] = &RegionMetrics{
        TEEMetrics: map[string]*TEEMetrics{
            "sgx": {LoadFactor: 0.1, SuccessRate: 1.0},
            "sev": {LoadFactor: 0.1, SuccessRate: 1.0},
        },
    }

    time.Sleep(100 * time.Millisecond) // Allow registration to complete
    return nil
}

func (vm *MockVM) ExecuteInRegion(ctx context.Context, regionID string, action chain.Action) (*core.ExecutionResult, error) {
    vm.mu.Lock()
    defer vm.mu.Unlock()

    // Verify region exists
    if !vm.regions[regionID] {
        return nil, fmt.Errorf("region not found: %s", regionID)
    }

    // Get current time
    now := time.Now().UTC()

    // Create fresh attestations for this execution
    attestations := [2]core.TEEAttestation{
        {
            EnclaveID:   []byte("sgx-test-enclave-1234567890"), // Updated enclave ID
            Measurement: []byte("measurement1"),
            Timestamp:   now,
            Data:        []byte("test-state-hash"),
            RegionProof: []byte("region-proof"),
            Signature:   []byte("signature1"),
        },
        {
            EnclaveID:   []byte("sev-test-enclave-0987654321"), // Updated enclave ID
            Measurement: []byte("measurement2"),
            Timestamp:   now,
            Data:        []byte("test-state-hash"),
            RegionProof: []byte("region-proof"),
            Signature:   []byte("signature2"),
        },
    }

    // Initialize timeProof
    timeProof := &timeserver.VerifiedTimestamp{
        Time: now,
        Proofs: []*timeserver.TimestampProof{
            {
                ServerID:  "server1",
                Signature: []byte("sig1"),
                Delay:     100 * time.Millisecond,
            },
            {
                ServerID:  "server2",
                Signature: []byte("sig2"),
                Delay:     100 * time.Millisecond,
            },
        },
        RegionID:   regionID,
        QuorumSize: 2,
    }

    // Handle specific action types
    switch a := action.(type) {
    case *actions.CrossRegionAction:
        // Handle cross-region action
        if a.Intent == nil {
            return nil, fmt.Errorf("nil cross-region intent")
        }

        // Process state changes for this region
        changes := a.Intent.StateChanges[regionID]
        if len(changes) > 0 {
            if vm.objects == nil {
                vm.objects = make(map[string]map[string]*core.ObjectState)
            }
            if vm.objects[regionID] == nil {
                vm.objects[regionID] = make(map[string]*core.ObjectState)
            }

            for _, change := range changes {
                key := string(change.Key)
                switch change.Operation {
                case xregion.StateOpSet:
                    vm.objects[regionID][key] = &core.ObjectState{
                        Storage:     change.Value,
                        RegionID:    regionID,
                        Status:      "active",
                        LastUpdated: now,
                    }
                case xregion.StateOpTransferOut:
                    delete(vm.objects[regionID], key)
                case xregion.StateOpTransferIn:
                    vm.objects[regionID][key] = &core.ObjectState{
                        Storage:     change.Value,
                        RegionID:    regionID,
                        Status:      "active",
                        LastUpdated: now,
                    }
                }
            }
        }

        return &core.ExecutionResult{
            StateHash:    []byte("test-state-hash"),
            Output:       []byte("cross-region-executed"),
            RegionID:     regionID,
            TimeProof:    timeProof,
            Attestations: attestations,
        }, nil

    case *actions.CreateObjectAction:
        // Existing CreateObjectAction handling...
        if vm.objects == nil {
            vm.objects = make(map[string]map[string]*core.ObjectState)
        }
        if vm.objects[regionID] == nil {
            vm.objects[regionID] = make(map[string]*core.ObjectState)
        }

        vm.objects[regionID][a.ID] = &core.ObjectState{
            Code:        a.Code,
            Storage:     a.Storage,
            RegionID:    regionID,
            Status:      "active",
            LastUpdated: now,
        }

        return &core.ExecutionResult{
            StateHash:    []byte("test-state-hash"),
            Output:       []byte("created"),
            RegionID:     regionID,
            TimeProof:    timeProof,
            Attestations: attestations,
        }, nil

    case *actions.SendEventAction:
        // Existing SendEventAction handling...
        obj, exists := vm.objects[regionID][a.IDTo]
        if !exists {
            return nil, fmt.Errorf("object not found in region")
        }

        if len(a.Attestations) > 0 {
            if len(a.Attestations) != 2 {
                return nil, fmt.Errorf("invalid attestation count")
            }

            for i, att := range a.Attestations {
                if len(att.EnclaveID) == 0 {
                    return nil, fmt.Errorf("missing enclave ID in attestation %d", i)
                }
                if att.Timestamp.IsZero() {
                    return nil, fmt.Errorf("invalid timestamp in attestation %d", i)
                }
                age := time.Since(att.Timestamp)
                if age > 5*time.Minute {
                    return nil, fmt.Errorf("attestation timestamp expired")
                }
            }

            attestations = a.Attestations
            for i := range attestations {
                attestations[i].Timestamp = now
                attestations[i].Data = []byte("test-state-hash")
            }
        }

        obj.LastUpdated = now

        return &core.ExecutionResult{
            StateHash:    []byte("test-state-hash"),
            Output:       []byte("executed"),
            RegionID:     regionID,
            TimeProof:    timeProof,
            Attestations: attestations,
        }, nil

    default:
        return nil, fmt.Errorf("unsupported action type: %T", action)
    }
}

func (vm *MockVM) updateTEEMetrics(regionID string, teeType string, executionTime float64, success bool) {
    if metrics, ok := vm.teeMetrics[regionID][teeType]; ok {
        metrics.UpdateMetrics(executionTime, success)
        
        // Update region load
        if load, ok := vm.regionLoads[regionID]; ok {
            if teeType == "SGX" {
                load.SGX.TaskCount++
                load.SGX.LastOperation = time.Now()
            } else {
                load.SEV.TaskCount++
                load.SEV.LastOperation = time.Now()
            }
        }
    }
}

func (vm *MockVM) getTEEHealth(regionID string, teeType string) string {
    if metrics, ok := vm.teeMetrics[regionID][teeType]; ok {
        if metrics.IsHealthy() {
            return "healthy"
        }
        return metrics.Status
    }
    return "unknown"
}

func TestIntegration(t *testing.T) {
    ginkgo.RunSpecs(t, "morpheusvm integration test suites")
}

func TestRegionalTEE(t *testing.T) {
    testVM, regionID := setupTestEnvironment(t)
    require := require.New(t)
    ctx := context.Background()

    // First create an object
    createAction := &actions.CreateObjectAction{
        ID:       "test-object",
        RegionID: regionID,
        Code:     []byte("test code"),
        Storage:  []byte("test storage"),
    }
   
    result, err := testVM.ExecuteInRegion(ctx, regionID, createAction)
    if err != nil {
        require.NoError(err)
    }
    require.NotNil(result)
   
    // Verify attestations
    require.NotNil(result.Attestations)
    require.Equal(2, len(result.Attestations[:]))
    require.NotEmpty(result.Attestations[0].EnclaveID)
    require.NotEmpty(result.Attestations[1].EnclaveID)
    require.NotEmpty(result.Attestations[0].Data)
    require.NotEmpty(result.Attestations[1].Data)
   
    // Verify state hash matches attestation data
    require.Equal(result.StateHash, result.Attestations[0].Data)
    require.Equal(result.StateHash, result.Attestations[1].Data)

    // Verify time proofs
    require.NotNil(result.TimeProof)
    require.Equal(2, len(result.TimeProof.Proofs))
}

func TestTEEPairOperations(t *testing.T) {
    testVM, regionID := setupTestEnvironment(t)
    require := require.New(t)
    ctx := context.Background()

    tests := []struct {
        name string
        test func(t *testing.T)
    }{
        {
            name: "TEE Pair Creation",
            test: func(t *testing.T) {
                // Create TEE pair
                err := testVM.RegisterRegion(ctx, "new-region", "mock://sgx-new", "mock://sev-new")
                if err != nil {
                    require.NoError(err)
                }

                // Verify workers were registered with coordinator
                sgxWorkerID := coordination.WorkerID("sgx-new-region")
                sevWorkerID := coordination.WorkerID("sev-new-region")
                
                task := &coordination.Task{
                    ID:        "test-task",
                    WorkerIDs: []coordination.WorkerID{sgxWorkerID, sevWorkerID},
                    Data:      []byte("test"),
                    RegionID:  "new-region",
                    Timeout:   5 * time.Second,
                }

                err = testVM.coordinator.SubmitTask(ctx, task)
                if err != nil {
                    require.NoError(err)
                }
            },
        },
        {
            name: "TEE Pair Execution",
            test: func(t *testing.T) {
                // Create object in region
                createAction := &actions.CreateObjectAction{
                    ID:       "pair-test",
                    RegionID: regionID,
                    Code:     []byte("test code"),
                    Storage:  []byte("test storage"),
                }

                // Execute across TEE pair
                result, err := testVM.ExecuteInRegion(ctx, regionID, createAction)
                if err != nil {
                    require.NoError(err)
                }
                require.NotNil(result)

                // Verify both TEEs produced attestations
                require.Equal(2, len(result.Attestations[:]))
                require.Equal(result.Attestations[0].Data, result.Attestations[1].Data)

                // Verify time proof
                require.NotNil(result.TimeProof)
                require.Equal(regionID, result.TimeProof.RegionID)
                require.Equal(2, len(result.TimeProof.Proofs))
            },
        },
        {
            name: "TEE Pair Attestation Chain",
            test: func(t *testing.T) {
                // Create initial state
                create := &actions.CreateObjectAction{
                    ID:       "chain-test",
                    RegionID: regionID,
                    Code:     []byte("test code"),
                    Storage:  []byte("test storage"),
                }

                result1, err := testVM.ExecuteInRegion(ctx, regionID, create)
                if err != nil {
                    require.NoError(err)
                }

                // Use attestations in next action
                event := &actions.SendEventAction{
                    IDTo:         "chain-test",
                    RegionID:     regionID,
                    FunctionCall: "test",
                    Parameters:   []byte("test"),
                    Attestations: result1.Attestations,
                }

                result2, err := testVM.ExecuteInRegion(ctx, regionID, event)
                if err != nil {
                    require.NoError(err)
                }

                // Verify attestation chain
                require.Equal(result1.StateHash, result2.Attestations[0].Data)
                require.True(result2.Attestations[0].Timestamp.After(result1.Attestations[0].Timestamp))
            },
        },
        {
            name: "TEE Pair Concurrent Execution",
            test: func(t *testing.T) {
                var wg sync.WaitGroup
                results := make([]*core.ExecutionResult, 5)
                errs := make([]error, 5)

                for i := 0; i < 5; i++ {
                    wg.Add(1)
                    go func(idx int) {
                        defer wg.Done()
                        action := &actions.CreateObjectAction{
                            ID:       fmt.Sprintf("concurrent-test-%d", idx),
                            RegionID: regionID,
                            Code:     []byte("test code"),
                            Storage:  []byte("test storage"),
                        }
                        result, err := testVM.ExecuteInRegion(ctx, regionID, action)
                        results[idx] = result
                        errs[idx] = err
                    }(i)
                }

                wg.Wait()

                // Verify all executions
                for i := 0; i < 5; i++ {
                    if errs[i] != nil {
                        require.NoError(errs[i])
                    }
                    require.NotNil(results[i])
                    require.Equal(2, len(results[i].Attestations[:]))
                }
            },
        },
    }

    for _, tt := range tests {
        t.Run(tt.name, tt.test)
    }
}

func verifyAttestationPair(t *testing.T, attestations [2]core.TEEAttestation) {
    require := require.New(t)
    
    require.Equal(2, len(attestations[:]))
    require.NotEmpty(attestations[0].EnclaveID)
    require.NotEmpty(attestations[1].EnclaveID)
    require.Equal(attestations[0].Timestamp, attestations[1].Timestamp)
    require.Equal(attestations[0].Data, attestations[1].Data)
}

// Helper function for load balancing tests
func verifyLoadDistribution(t *testing.T, metrics *RegionMetrics) {
    require := require.New(t)
    
    sgxLoad := metrics.TEEMetrics["sgx"].LoadFactor
    sevLoad := metrics.TEEMetrics["sev"].LoadFactor
    
    // Check load difference is within 20%
    loadDiff := math.Abs(sgxLoad - sevLoad)
    require.LessOrEqual(loadDiff, 0.2)
}

func TestRegionManagement(t *testing.T) {
    testVM, _ := setupTestEnvironment(t)
    require := require.New(t)
    ctx := context.Background()

    // Test registering new region
    newRegionID := "new-region"
    err := testVM.RegisterRegion(ctx, newRegionID, "mock://sgx2", "mock://sev2")
    require.NoError(err)

    // Try to register the same region again - this should fail
    err = testVM.RegisterRegion(ctx, newRegionID, "mock://sgx2", "mock://sev2")
    require.Error(err)
    require.Contains(err.Error(), "region already registered")

    // Test invalid endpoints
    err = testVM.RegisterRegion(ctx, "bad-region", "", "")
    require.Error(err)
    require.Contains(err.Error(), "invalid TEE endpoints")
}

func TestTimeProofVerification(t *testing.T) {
    testVM, regionID := setupTestEnvironment(t)
    require := require.New(t)
    ctx := context.Background()

    // Create result1 with proper time proof
    result1, err := testVM.ExecuteInRegion(ctx, regionID, createTestAction())
    if err != nil {
        require.NoError(err)
    }
    require.NotNil(result1)
    require.NotNil(result1.TimeProof)

    time.Sleep(100 * time.Millisecond)

    // Create result2 with proper time proof
    result2, err := testVM.ExecuteInRegion(ctx, regionID, createTestAction())
    if err != nil {
        require.NoError(err)
    }
    require.NotNil(result2)
    require.NotNil(result2.TimeProof)

    // Verify timestamps are monotonically increasing
    require.Greater(result2.TimeProof.Time.UnixNano(), result1.TimeProof.Time.UnixNano())
    require.Equal(2, len(result1.TimeProof.Proofs))
}


func TestConcurrentRegionOperations(t *testing.T) {
    testVM, regionID := setupTestEnvironment(t)
    require := require.New(t)
    ctx := context.Background()

    var wg sync.WaitGroup
    numOperations := 10
    errors := make(chan error, numOperations)
    results := make(chan *core.ExecutionResult, numOperations)

    for i := 0; i < numOperations; i++ {
        wg.Add(1)
        go func(idx int) {
            defer wg.Done()
            action := &actions.CreateObjectAction{
                ID:       fmt.Sprintf("concurrent-obj-%d", idx),
                RegionID: regionID,
                Code:     []byte("test code"),
                Storage:  []byte("test storage"),
            }
            result, err := testVM.ExecuteInRegion(ctx, regionID, action)
            if err != nil {
                errors <- err
                return
            }
            results <- result
        }(i)
    }

    wg.Wait()
    close(errors)
    close(results)

    // Check for errors
    for err := range errors {
        require.NoError(err)
    }

    // Verify results
    resultCount := 0
    for result := range results {
        require.NotNil(result)
        require.Len(result.Attestations, 2)
        require.NotNil(result.TimeProof)
        resultCount++
    }
    require.Equal(numOperations, resultCount)
}

func TestTEEPairFailover(t *testing.T) {
    testVM, regionID := setupTestEnvironment(t)
    require := require.New(t)
    ctx := context.Background()

    // Create initial state with valid attestations
    initialAction := &actions.CreateObjectAction{
        ID:       "failover-test",
        RegionID: regionID,
        Code:     []byte("test code"),
        Storage:  []byte("test storage"),
    }

    initialResult, err := testVM.ExecuteInRegion(ctx, regionID, initialAction)
    require.NoError(err)
    require.NotNil(initialResult)
    require.Len(initialResult.Attestations, 2)

    // Simulate TEE failure by temporarily disabling region
    err = testVM.SimulateRegionFailure(regionID)
    require.NoError(err)

    // Wait for recovery
    time.Sleep(200 * time.Millisecond)

    // Try execution after recovery
    recoveryAction := &actions.SendEventAction{
        IDTo:         "failover-test",
        RegionID:     regionID,
        FunctionCall: "test",
        Parameters:   []byte("test"),
        Attestations: initialResult.Attestations,
    }

    recoveryResult, err := testVM.ExecuteInRegion(ctx, regionID, recoveryAction)
    require.NoError(err)
    require.NotNil(recoveryResult)
    require.Len(recoveryResult.Attestations, 2)

    // Verify new attestations
    verifyTEEStateConsistency(t, recoveryResult.Attestations)
}

func TestRegionStateProofs(t *testing.T) {
    testVM, regionID := setupTestEnvironment(t)
    require := require.New(t)
    ctx := context.Background()

    // Create state to verify
    action := &actions.CreateObjectAction{
        ID:       "proof-test",
        RegionID: regionID,
        Code:     []byte("test code"),
        Storage:  []byte("test storage"),
    }

    result, err := testVM.ExecuteInRegion(ctx, regionID, action)
    require.NoError(err)
    require.NotNil(result)

    // Get proof for the created state
    proof, err := testVM.GetRegionStateProof(ctx, regionID, "proof-test")
    require.NoError(err)
    require.NotNil(proof)

    // Verify the proof
    valid, err := testVM.VerifyRegionStateProof(ctx, regionID, proof)
    require.NoError(err)
    require.True(valid)

    // Try invalid region
    _, err = testVM.GetRegionStateProof(ctx, "invalid-region", "proof-test")
    require.Error(err)
    require.Contains(err.Error(), "region not found")
}
func TestRegionMetrics(t *testing.T) {
    testVM, regionID := setupTestEnvironment(t)
    require := require.New(t)
    ctx := context.Background()

    // Generate load for metrics
    for i := 0; i < 5; i++ {
        action := &actions.CreateObjectAction{
            ID:       fmt.Sprintf("metrics-test-%d", i),
            RegionID: regionID,
            Code:     []byte("test code"),
            Storage:  []byte("test storage"),
        }
        result, err := testVM.ExecuteInRegion(ctx, regionID, action)
        require.NoError(err)
        require.NotNil(result)
    }

    // Get metrics
    metrics, err := testVM.GetRegionMetrics(ctx, regionID)
    require.NoError(err)
    require.NotNil(metrics)

    // Verify metric values
    require.True(metrics.LoadFactor > 0 && metrics.LoadFactor <= 1.0)
    require.True(metrics.LatencyMs > 0)
    require.GreaterOrEqual(metrics.ActiveWorkers, 2)
    require.NotNil(metrics.TEEMetrics["sgx"])
    require.NotNil(metrics.TEEMetrics["sev"])
    require.True(metrics.TEEMetrics["sgx"].SuccessRate > 0)
    require.True(metrics.TEEMetrics["sev"].SuccessRate > 0)
}

func verifyMetrics(t *testing.T, metrics *RegionMetrics) {
    require := require.New(t)
    
    require.NotNil(metrics)
    require.True(metrics.LoadFactor >= 0 && metrics.LoadFactor <= 1.0)
    require.True(metrics.LatencyMs > 0)
    require.GreaterOrEqual(metrics.ActiveWorkers, 2)
    require.NotNil(metrics.TEEMetrics["sgx"])
    require.NotNil(metrics.TEEMetrics["sev"])
}


func TestTEEPairMetrics(t *testing.T) {
    testVM, regionID := setupTestEnvironment(t)
    require := require.New(t)
    ctx := context.Background()

    tests := []struct {
        name string
        test func(t *testing.T)
    }{
        {
            name: "Basic Metrics Collection",
            test: func(t *testing.T) {
                // Execute some operations
                for i := 0; i < 3; i++ {
                    action := &actions.CreateObjectAction{
                        ID:       fmt.Sprintf("metrics-obj-%d", i),
                        RegionID: regionID,
                        Code:     []byte("test code"),
                        Storage:  []byte("test storage"),
                    }
                    _, err := testVM.ExecuteInRegion(ctx, regionID, action)
                    if err != nil {
                        require.NoError(err)
                    }
                }

                // Get metrics
                metrics, err := testVM.GetRegionMetrics(ctx, regionID)
                if err != nil {
                    require.NoError(err)
                }
                require.NotNil(metrics)

                // Verify TEE-specific metrics
                sgxMetrics, ok := metrics.TEEMetrics["sgx"]
                require.True(ok)
                sevMetrics, ok := metrics.TEEMetrics["sev"]
                require.True(ok)
                
                require.Less(sgxMetrics.LoadFactor, float64(1.0))
                require.Less(sevMetrics.LoadFactor, float64(1.0))
            },
        },
        {
            name: "Performance Metrics",
            test: func(t *testing.T) {
                // Execute concurrent operations
                var wg sync.WaitGroup
                errs := make(chan error, 5)
                
                for i := 0; i < 5; i++ {
                    wg.Add(1)
                    go func(idx int) {
                        defer wg.Done()
                        action := &actions.CreateObjectAction{
                            ID:       fmt.Sprintf("perf-test-%d", idx),
                            RegionID: regionID,
                            Code:     []byte("test code"),
                            Storage:  []byte("test storage"),
                        }
                        _, err := testVM.ExecuteInRegion(ctx, regionID, action)
                        if err != nil {
                            errs <- err
                        }
                    }(i)
                }
                
                wg.Wait()
                close(errs)
                
                // Check for errors
                for err := range errs {
                    require.NoError(err)
                }

                // Get updated metrics
                metrics, err := testVM.GetRegionMetrics(ctx, regionID)
                if err != nil {
                    require.NoError(err)
                }
                require.NotNil(metrics)
                
                // Verify performance metrics
                require.Greater(metrics.LatencyMs, float64(0))
                require.Greater(metrics.LoadFactor, float64(0))
                
                sgxMetrics, ok := metrics.TEEMetrics["sgx"]
                require.True(ok)
                require.Greater(sgxMetrics.SuccessRate, float64(0))
                
                sevMetrics, ok := metrics.TEEMetrics["sev"]
                require.True(ok)
                require.Greater(sevMetrics.SuccessRate, float64(0))
            },
        },
        {
            name: "Network Latency Metrics",
            test: func(t *testing.T) {
                metrics, err := testVM.GetRegionMetrics(ctx, regionID)
                if err != nil {
                    require.NoError(err)
                }
                require.NotNil(metrics)
                
                // Verify network latency measurements
                require.NotEmpty(metrics.NetworkLatency)
                for _, latency := range metrics.NetworkLatency {
                    require.Greater(latency, float64(0))
                }
            },
        },
    }

    for _, tt := range tests {
        t.Run(tt.name, tt.test)
    }
}

// Helper function to mock TEE metrics
func (vm *MockVM) generateMockMetrics(regionID string) *RegionMetrics {
    return &RegionMetrics{
        LoadFactor:    0.5,
        LatencyMs:     100,
        ErrorRate:     0.01,
        ActiveWorkers: 2,
        PendingTasks:  5,
        LastHealthCheck: time.Now(),
        NetworkLatency: map[string]float64{
            "region-1": 50,
            "region-2": 75,
        },
        TEEMetrics: map[string]*TEEMetrics{
            "sgx": {
                EnclaveID:    []byte("sgx-test"),
                Type:         "SGX",
                LoadFactor:   0.4,
                SuccessRate:  0.99,
                LastAttested: time.Now(),
            },
            "sev": {
                EnclaveID:    []byte("sev-test"),
                Type:         "SEV",
                LoadFactor:   0.3,
                SuccessRate:  0.98,
                LastAttested: time.Now(),
            },
        },
    }
}

// Add helper function to simulate TEE pair health check
func (vm *MockVM) performHealthCheck(ctx context.Context, regionID string) error {
    vm.mu.Lock()
    defer vm.mu.Unlock()

    if !vm.regions[regionID] {
        return fmt.Errorf("region not found")
    }

    // Simulate health check
    action := &actions.SendEventAction{
        IDTo:         "health-check",
        RegionID:     regionID,
        FunctionCall: "health_check",
        Parameters:   []byte("check"),
    }

    _, err := vm.ExecuteInRegion(ctx, regionID, action)
    return err
}

func (vm *MockVM) GetRegionHealth(ctx context.Context, regionID string) (*HealthStatus, error) {
    vm.mu.RLock()
    defer vm.mu.RUnlock()

    if !vm.regions[regionID] {
        return &HealthStatus{
            Status:         "failed",
            LastCheck:      time.Now(),
            ErrorCount:     1,
            SuccessRate:    0.0,
            LoadFactor:     0.0,
            AverageLatency: 1 * time.Second,
        }, nil
    }

    return &HealthStatus{
        Status:         "healthy",
        LastCheck:      time.Now(),
        ErrorCount:     0,
        SuccessRate:    1.0,
        LoadFactor:     0.5,
        AverageLatency: 100 * time.Millisecond,
    }, nil
}

func (vm *MockVM) SimulateStateDesync(regionID string) error {
    vm.mu.Lock()
    defer vm.mu.Unlock()

    if !vm.regions[regionID] {
        return fmt.Errorf("region not found")
    }

    // Simulate state desync by corrupting local state
    regionObjects := vm.objects[regionID]
    if regionObjects != nil {
        for _, obj := range regionObjects {
            obj.Storage = append(obj.Storage, []byte("corrupted")...)
        }
    }

    // Auto-recover after brief delay
    go func() {
        time.Sleep(100 * time.Millisecond)
        vm.mu.Lock()
        defer vm.mu.Unlock()
        // Reset state to original
        if regionObjects != nil {
            for _, obj := range regionObjects {
                obj.Storage = bytes.TrimSuffix(obj.Storage, []byte("corrupted"))
            }
        }
    }()

    return nil
}



// Helper function to get object state
func (vm *MockVM) GetObject(ctx context.Context, objectID string, regionID string) (*core.ObjectState, error) {
    vm.mu.RLock()
    defer vm.mu.RUnlock()

    regionObjects, exists := vm.objects[regionID]
    if !exists {
        return nil, fmt.Errorf("region not found")
    }

    obj, exists := regionObjects[objectID]
    if !exists {
        return nil, fmt.Errorf("object not found")
    }

    return obj, nil
}

// Helper function for state proofs
func (vm *MockVM) GetRegionStateProof(ctx context.Context, regionID string, objectID string) (*merkledb.Proof, error) {
    vm.mu.RLock()
    defer vm.mu.RUnlock()

    if !vm.regions[regionID] {
        return nil, fmt.Errorf("region not found")
    }

    // Create key bytes
    keyBytes := []byte(fmt.Sprintf("%s/%s", regionID, objectID))
   
    // Use ToKey to create proper Key type
    key := merkledb.ToKey(keyBytes)

    // Create mock proof node
    mockNode := merkledb.ProofNode{
        Key:         key,
        ValueOrHash: maybe.Some([]byte("test hash")),
        Children:    make(map[byte]ids.ID),
    }

    return &merkledb.Proof{
        Key:   key,
        Value: maybe.Some([]byte("mock proof")),
        Path:  []merkledb.ProofNode{mockNode},
    }, nil
}

// Helper function to verify state proofs
func (vm *MockVM) VerifyRegionStateProof(ctx context.Context, regionID string, proof *merkledb.Proof) (bool, error) {
    vm.mu.RLock()
    defer vm.mu.RUnlock()

    if !vm.regions[regionID] {
        return false, fmt.Errorf("region not found")
    }

    // Mock verification - in real implementation would verify against MerkleDB
    return true, nil
}

// Helper function to get region metrics
func (vm *MockVM) GetRegionMetrics(ctx context.Context, regionID string) (*RegionMetrics, error) {
    vm.mu.RLock()
    defer vm.mu.RUnlock()

    if !vm.regions[regionID] {
        return nil, fmt.Errorf("region not found")
    }

    // Return stored metrics if they exist
    if metrics, exists := vm.regionMetrics[regionID]; exists {
        return metrics, nil
    }

    // Return default metrics
    return &RegionMetrics{
        TEEMetrics: map[string]*TEEMetrics{
            "sgx": {LoadFactor: 0.5, SuccessRate: 1.0},
            "sev": {LoadFactor: 0.5, SuccessRate: 1.0},
        },
    }, nil
}

func (vm *MockVM) SimulateRegionFailure(regionID string) error {
    vm.mu.Lock()
    defer vm.mu.Unlock()

    if !vm.regions[regionID] {
        return fmt.Errorf("region not found: %s", regionID)
    }

    // Simulate failure by temporarily removing region
    delete(vm.regions, regionID)

    // Simulate recovery after brief delay
    go func() {
        time.Sleep(100 * time.Millisecond)
        vm.mu.Lock()
        vm.regions[regionID] = true
        vm.mu.Unlock()
    }()

    return nil
}

func (vm *MockVM) SimulatePartialConnectivity(regionID string) error {
    vm.mu.Lock()
    defer vm.mu.Unlock()

    if !vm.regions[regionID] {
        return fmt.Errorf("region not found: %s", regionID)
    }

    // Add network latency simulation
    go func() {
        for i := 0; i < 5; i++ {
            vm.mu.Lock()
            // Temporarily disable region
            delete(vm.regions, regionID)
            vm.mu.Unlock()

            // Random delay between 50-150ms
            delay := 50 + rand.Intn(100)
            time.Sleep(time.Duration(delay) * time.Millisecond)

            vm.mu.Lock()
            // Re-enable region
            vm.regions[regionID] = true
            vm.mu.Unlock()
        }
    }()

    return nil
}

// Add network partition simulation
func (vm *MockVM) SimulateNetworkPartition(regionID string) error {
    vm.mu.Lock()
    defer vm.mu.Unlock()

    if !vm.regions[regionID] {
        return fmt.Errorf("region not found: %s", regionID)
    }

    // Simulate network partition by temporarily removing region
    delete(vm.regions, regionID)

    // Auto-heal partition after delay
    go func() {
        time.Sleep(100 * time.Millisecond)
        vm.mu.Lock()
        defer vm.mu.Unlock()
        vm.regions[regionID] = true
    }()

    return nil
}



func generateTestTasks(t *testing.T, vm *MockVM, regionID string, count int) []*core.ExecutionResult {
    require := require.New(t)
    results := make([]*core.ExecutionResult, count)
    
    for i := 0; i < count; i++ {
        action := &actions.CreateObjectAction{
            ID:       fmt.Sprintf("test-obj-%d", i),
            RegionID: regionID,
            Code:     []byte("test code"),
            Storage:  []byte("test storage"),
        }
        
        result, err := vm.ExecuteInRegion(context.Background(), regionID, action)
        require.NoError(err)
        results[i] = result
    }
    
    return results
}

// Helper function to simulate concurrent task execution
func simulateConcurrentTasks(t *testing.T, vm *MockVM, regionID string, numTasks int) []*core.ExecutionResult {
    require := require.New(t)
    ctx := context.Background()

    results := make([]*core.ExecutionResult, numTasks)
    var wg sync.WaitGroup
    var mu sync.Mutex

    for i := 0; i < numTasks; i++ {
        wg.Add(1)
        go func(idx int) {
            defer wg.Done()

            action := &actions.CreateObjectAction{
                ID:       fmt.Sprintf("concurrent-test-%d", idx),
                RegionID: regionID,
                Code:     []byte("test code"),
                Storage:  []byte("test storage"),
            }

            result, err := vm.ExecuteInRegion(ctx, regionID, action)
            require.NoError(err)

            mu.Lock()
            results[idx] = result
            mu.Unlock()
        }(i)
    }

    wg.Wait()
    return results
}

func executeConcurrent(t *testing.T, vm *MockVM, regionID string, count int) []*core.ExecutionResult {
    require := require.New(t)
    results := make([]*core.ExecutionResult, count)
    var wg sync.WaitGroup
    var mu sync.Mutex
    
    for i := 0; i < count; i++ {
        wg.Add(1)
        go func(idx int) {
            defer wg.Done()
            
            action := &actions.CreateObjectAction{
                ID:       fmt.Sprintf("concurrent-test-%d", idx),
                RegionID: regionID,
                Code:     []byte("test code"),
                Storage:  []byte("test storage"),
            }
            
            result, err := vm.ExecuteInRegion(context.Background(), regionID, action)
            require.NoError(err)
            
            mu.Lock()
            results[idx] = result
            mu.Unlock()
        }(i)
    }
    
    wg.Wait()
    return results
}

// Helper function to verify TEE attestation chain
func verifyAttestationChain(t *testing.T, results []*core.ExecutionResult) {
    require := require.New(t)

    for i := 1; i < len(results); i++ {
        prev := results[i-1]
        curr := results[i]

        // Verify timestamps are monotonically increasing
        require.True(curr.Attestations[0].Timestamp.After(prev.Attestations[0].Timestamp))
        require.True(curr.Attestations[1].Timestamp.After(prev.Attestations[1].Timestamp))

        // Verify state consistency
        require.Equal(prev.StateHash, curr.Attestations[0].Data)
        require.Equal(prev.StateHash, curr.Attestations[1].Data)
    }
}

// Helper function to verify TEE pair health
func verifyTEEPairHealth(t *testing.T, metrics *RegionMetrics) {
    require := require.New(t)

    // Verify SGX metrics
    require.Contains(metrics.TEEMetrics, "sgx")
    sgxMetrics := metrics.TEEMetrics["sgx"]
    require.True(sgxMetrics.LoadFactor >= 0 && sgxMetrics.LoadFactor <= 1.0)
    require.True(sgxMetrics.SuccessRate >= 0 && sgxMetrics.SuccessRate <= 1.0)
    require.False(sgxMetrics.LastAttested.IsZero())

    // Verify SEV metrics
    require.Contains(metrics.TEEMetrics, "sev")
    sevMetrics := metrics.TEEMetrics["sev"]
    require.True(sevMetrics.LoadFactor >= 0 && sevMetrics.LoadFactor <= 1.0)
    require.True(sevMetrics.SuccessRate >= 0 && sevMetrics.SuccessRate <= 1.0)
    require.False(sevMetrics.LastAttested.IsZero())
}

// Helper function to verify time proofs
func verifyTimeProofs(t *testing.T, results []*core.ExecutionResult) {
    require := require.New(t)

    for i := 1; i < len(results); i++ {
        prev := results[i-1]
        curr := results[i]

        require.NotNil(prev.TimeProof)
        require.NotNil(curr.TimeProof)
        require.True(curr.TimeProof.Time.After(prev.TimeProof.Time))
        require.Len(curr.TimeProof.Proofs, 2)
    }
}
// Test TEE pair coordination and synchronization
func TestTEEPairCoordination(t *testing.T) {
    testVM, regionID := setupTestEnvironment(t)
    require := require.New(t)
    ctx := context.Background()

    tests := []struct {
        name string
        test func(t *testing.T)
    }{
        {
            name: "TEE Pair State Synchronization",
            test: func(t *testing.T) {
                // Create initial state with explicit enclave IDs
                action := &actions.CreateObjectAction{
                    ID:       "sync-test",
                    RegionID: regionID,
                    Code:     []byte("test code"),
                    Storage:  []byte("test storage"),
                }

                result1, err := testVM.ExecuteInRegion(ctx, regionID, action)
                require.NoError(err)
                require.NotNil(result1)
                
                // Verify enclave IDs and state
                require.NotEmpty(result1.Attestations[0].EnclaveID)
                require.NotEmpty(result1.Attestations[1].EnclaveID)
                require.Equal(result1.Attestations[0].Data, result1.Attestations[1].Data)

                // Execute another action with verified attestations
                event := &actions.SendEventAction{
                    IDTo:         "sync-test",
                    RegionID:     regionID,
                    FunctionCall: "test",
                    Parameters:   []byte("test"),
                    Attestations: result1.Attestations,
                }

                result2, err := testVM.ExecuteInRegion(ctx, regionID, event)
                require.NoError(err)
                require.NotNil(result2)
                require.Equal(result2.Attestations[0].Data, result2.Attestations[1].Data)
            },
        },
        {
            name: "TEE Pair Load Distribution",
            test: func(t *testing.T) {
                metrics := make([]*RegionMetrics, 5)
                
                for i := 0; i < 5; i++ {
                    action := &actions.CreateObjectAction{
                        ID:       fmt.Sprintf("load-test-%d", i),
                        RegionID: regionID,
                        Code:     []byte("test code"),
                        Storage:  []byte("test storage"),
                    }
                    
                    result, err := testVM.ExecuteInRegion(ctx, regionID, action)
                    require.NoError(err)
                    require.NotEmpty(result.Attestations[0].EnclaveID)
                    require.NotEmpty(result.Attestations[1].EnclaveID)

                    m, err := testVM.GetRegionMetrics(ctx, regionID)
                    require.NoError(err)
                    metrics[i] = m
                }

                sgxLoad := metrics[len(metrics)-1].TEEMetrics["sgx"].LoadFactor
                sevLoad := metrics[len(metrics)-1].TEEMetrics["sev"].LoadFactor
                require.InDelta(sgxLoad, sevLoad, 0.2)
            },
        },
        {
            name: "TEE Pair Attestation Renewal",
            test: func(t *testing.T) {
                // Create initial attestations with explicit enclave IDs
                action1 := &actions.CreateObjectAction{
                    ID:       "renewal-test",
                    RegionID: regionID,
                    Code:     []byte("test code"),
                    Storage:  []byte("test storage"),
                }

                result1, err := testVM.ExecuteInRegion(ctx, regionID, action1)
                require.NoError(err)
                require.NotEmpty(result1.Attestations[0].EnclaveID)
                require.NotEmpty(result1.Attestations[1].EnclaveID)

                initialAttestations := [2]core.TEEAttestation{
                    {
                        EnclaveID:   []byte("sgx-test-enclave"),
                        Measurement: result1.Attestations[0].Measurement,
                        Timestamp:   result1.Attestations[0].Timestamp,
                        Data:        result1.Attestations[0].Data,
                        RegionProof: result1.Attestations[0].RegionProof,
                        Signature:   result1.Attestations[0].Signature,
                    },
                    {
                        EnclaveID:   []byte("sev-test-enclave"),
                        Measurement: result1.Attestations[1].Measurement,
                        Timestamp:   result1.Attestations[1].Timestamp,
                        Data:        result1.Attestations[1].Data,
                        RegionProof: result1.Attestations[1].RegionProof,
                        Signature:   result1.Attestations[1].Signature,
                    },
                }

                time.Sleep(100 * time.Millisecond)

                action2 := &actions.SendEventAction{
                    IDTo:         "renewal-test",
                    RegionID:     regionID,
                    FunctionCall: "test",
                    Parameters:   []byte("test"),
                    Attestations: initialAttestations,
                }

                result2, err := testVM.ExecuteInRegion(ctx, regionID, action2)
                require.NoError(err)
                require.NotEmpty(result2.Attestations[0].EnclaveID)
                require.NotEmpty(result2.Attestations[1].EnclaveID)
                require.NotEqual(initialAttestations[0].Timestamp, result2.Attestations[0].Timestamp)
                require.NotEqual(initialAttestations[1].Timestamp, result2.Attestations[1].Timestamp)
            },
        },
        {
            name: "TEE Pair Error Recovery",
            test: func(t *testing.T) {
                action := &actions.CreateObjectAction{
                    ID:       "recovery-test",
                    RegionID: regionID,
                    Code:     []byte("test code"),
                    Storage:  []byte("test storage"),
                }

                result1, err := testVM.ExecuteInRegion(ctx, regionID, action)
                require.NoError(err)
                require.NotEmpty(result1.Attestations[0].EnclaveID)
                require.NotEmpty(result1.Attestations[1].EnclaveID)

                err = testVM.SimulateRegionFailure(regionID)
                require.NoError(err)

                time.Sleep(200 * time.Millisecond)

                event := &actions.SendEventAction{
                    IDTo:         "recovery-test",
                    RegionID:     regionID,
                    FunctionCall: "test",
                    Parameters:   []byte("test"),
                    Attestations: result1.Attestations,
                }

                result2, err := testVM.ExecuteInRegion(ctx, regionID, event)
                require.NoError(err)
                require.NotNil(result2)
                require.Len(result2.Attestations, 2)
                require.NotEmpty(result2.Attestations[0].EnclaveID)
                require.NotEmpty(result2.Attestations[1].EnclaveID)
            },
        },
        {
            name: "TEE Pair Performance Monitoring",
            test: func(t *testing.T) {
                var latencies []time.Duration
                var loadFactors []float64

                for i := 0; i < 5; i++ {
                    start := time.Now()
                    action := &actions.CreateObjectAction{
                        ID:       fmt.Sprintf("perf-test-%d", i),
                        RegionID: regionID,
                        Code:     []byte("test code"),
                        Storage:  []byte("test storage"),
                    }

                    result, err := testVM.ExecuteInRegion(ctx, regionID, action)
                    require.NoError(err)
                    require.NotEmpty(result.Attestations[0].EnclaveID)
                    require.NotEmpty(result.Attestations[1].EnclaveID)

                    latencies = append(latencies, time.Since(start))

                    metrics, err := testVM.GetRegionMetrics(ctx, regionID)
                    require.NoError(err)
                    loadFactors = append(loadFactors, metrics.LoadFactor)
                }

                for i := range latencies {
                    require.Less(latencies[i], 1*time.Second)
                    require.True(loadFactors[i] >= 0 && loadFactors[i] <= 1.0)
                }
            },
        },
    }

    for _, tt := range tests {
        t.Run(tt.name, tt.test)
    }
}
// Test TEE pair security and attestation verification
func TestTEEPairSecurity(t *testing.T) {
    testVM, regionID := setupTestEnvironment(t)
    ctx := context.Background()

    t.Run("Attestation Chain Verification", func(t *testing.T) {
        // Create a chain of attestations
        var attestationChain [][2]core.TEEAttestation
        var lastResult *core.ExecutionResult

        // First create the object
        createAction := &actions.CreateObjectAction{
            ID:       "test-object",
            RegionID: regionID,
            Code:     []byte("test code"),
            Storage:  []byte("test storage"),
        }

        // Create initial object
        result, err := testVM.ExecuteInRegion(ctx, regionID, createAction)
        if err != nil {
            t.Fatalf("failed to create object: %v", err)
        }
        if result == nil {
            t.Fatal("create object result is nil")
        }

        attestationChain = append(attestationChain, result.Attestations)
        lastResult = result

        // Create chain of events
        for i := 0; i < 3; i++ {
            event := &actions.SendEventAction{
                IDTo:         "test-object", // Use the same object ID we created
                RegionID:     regionID,
                FunctionCall: fmt.Sprintf("test-%d", i),
                Parameters:   []byte("test"),
                Attestations: lastResult.Attestations,
            }

            result, err := testVM.ExecuteInRegion(ctx, regionID, event)
            if err != nil {
                t.Fatalf("failed to execute event %d: %v", i, err)
            }
            if result == nil {
                t.Fatalf("result is nil for event %d", i)
            }
            
            attestationChain = append(attestationChain, result.Attestations)
            lastResult = result
        }

        // Verify attestation chain
        verifyAttestationChainIntegrity(t, attestationChain)
    })

    t.Run("Concurrent Attestation Verification", func(t *testing.T) {
        var wg sync.WaitGroup
        numGoroutines := 5
        results := make([][2]core.TEEAttestation, numGoroutines)
        errors := make([]error, numGoroutines)

        for i := 0; i < numGoroutines; i++ {
            wg.Add(1)
            go func(idx int) {
                defer wg.Done()
                
                // Create unique object for each goroutine
                createAction := &actions.CreateObjectAction{
                    ID:       fmt.Sprintf("test-object-%d", idx),
                    RegionID: regionID,
                    Code:     []byte("test code"),
                    Storage:  []byte("test storage"),
                }
                
                result, err := testVM.ExecuteInRegion(ctx, regionID, createAction)
                if err != nil {
                    errors[idx] = err
                    return
                }
                results[idx] = result.Attestations
            }(i)
        }

        wg.Wait()

        // Verify all attestations
        for i := 0; i < numGoroutines; i++ {
            if errors[i] != nil {
                t.Fatalf("error in goroutine %d: %v", i, errors[i])
            }
            if len(results[i][0].EnclaveID) == 0 {
                t.Fatalf("empty SGX enclave ID in result %d", i)
            }
            if !bytes.Equal(results[i][0].EnclaveID, []byte("sgx-test-enclave-1234567890")) {
                t.Fatalf("unexpected SGX enclave ID in result %d", i)
            }
            if len(results[i][1].EnclaveID) == 0 {
                t.Fatalf("empty SEV enclave ID in result %d", i)
            }
            if !bytes.Equal(results[i][1].EnclaveID, []byte("sev-test-enclave-0987654321")) {
                t.Fatalf("unexpected SEV enclave ID in result %d", i)
            }
        }
    })

    t.Run("TEE Pair State Consistency", func(t *testing.T) {
        // Create initial object
        createAction := &actions.CreateObjectAction{
            ID:       "consistency-test-object",
            RegionID: regionID,
            Code:     []byte("test code"),
            Storage:  []byte("test storage"),
        }

        // Create initial state
        result1, err := testVM.ExecuteInRegion(ctx, regionID, createAction)
        if err != nil {
            t.Fatalf("failed to create object: %v", err)
        }
        if result1 == nil {
            t.Fatal("initial result is nil")
        }

        // Verify state consistency between TEEs
        verifyTEEStateConsistency(t, result1.Attestations)

        // Execute multiple operations
        for i := 0; i < 3; i++ {
            event := &actions.SendEventAction{
                IDTo:         "consistency-test-object",
                RegionID:     regionID,
                FunctionCall: fmt.Sprintf("test-%d", i),
                Parameters:   []byte("test"),
                Attestations: result1.Attestations,
            }

            result2, err := testVM.ExecuteInRegion(ctx, regionID, event)
            if err != nil {
                t.Fatalf("failed to execute event %d: %v", i, err)
            }
            if result2 == nil {
                t.Fatalf("result is nil for event %d", i)
            }
            verifyTEEStateConsistency(t, result2.Attestations)
        }
    })
}


// Helper function to verify TEE state consistency
func verifyTEEStateConsistency(t *testing.T, attestations [2]core.TEEAttestation) {
    // Verify both TEEs produced valid attestations
    if len(attestations[0].EnclaveID) == 0 {
        t.Fatal("empty SGX enclave ID")
    }
    if len(attestations[1].EnclaveID) == 0 {
        t.Fatal("empty SEV enclave ID")
    }

    // Verify timestamps match
    if !attestations[0].Timestamp.Equal(attestations[1].Timestamp) {
        t.Fatal("timestamp mismatch between attestations")
    }

    // Verify state hashes match
    if !bytes.Equal(attestations[0].Data, attestations[1].Data) {
        t.Fatal("state hash mismatch between attestations")
    }

    // Verify region proofs
    if len(attestations[0].RegionProof) == 0 {
        t.Fatal("missing SGX region proof")
    }
    if len(attestations[1].RegionProof) == 0 {
        t.Fatal("missing SEV region proof")
    }
}

func verifyAttestationChainIntegrity(t *testing.T, chain [][2]core.TEEAttestation) {
    if len(chain) < 2 {
        t.Fatal("attestation chain too short")
    }

    for i := 1; i < len(chain); i++ {
        prev := chain[i-1]
        curr := chain[i]

        // Verify timestamps are monotonically increasing
        if !curr[0].Timestamp.After(prev[0].Timestamp) {
            t.Fatal("SGX attestation timestamps not monotonically increasing")
        }
        if !curr[1].Timestamp.After(prev[1].Timestamp) {
            t.Fatal("SEV attestation timestamps not monotonically increasing")
        }

        // Verify enclave IDs remain consistent
        if !bytes.Equal(prev[0].EnclaveID, curr[0].EnclaveID) {
            t.Fatal("SGX enclave ID changed in chain")
        }
        if !bytes.Equal(prev[1].EnclaveID, curr[1].EnclaveID) {
            t.Fatal("SEV enclave ID changed in chain")
        }

        // Verify state transitions
        if !bytes.Equal(prev[0].Data, curr[0].Data) {
            t.Fatal("SGX state hash mismatch in chain")
        }
        if !bytes.Equal(prev[1].Data, curr[1].Data) {
            t.Fatal("SEV state hash mismatch in chain")
        }
    }
}


// Test TEE pair performance and scaling
func TestTEEPairPerformance(t *testing.T) {
    testVM, regionID := setupTestEnvironment(t)
    require := require.New(t)
    ctx := context.Background()

    tests := []struct {
        name string
        test func(t *testing.T)
    }{
        {
            name: "TEE Pair Load Balancing",
            test: func(t *testing.T) {
                type teeMetrics struct {
                    executions int
                    loadFactor float64
                    latency    time.Duration
                }
                
                metrics := make(map[string]*teeMetrics)
                metrics["sgx"] = &teeMetrics{}
                metrics["sev"] = &teeMetrics{}

                numOperations := 20
                results := make([]*core.ExecutionResult, numOperations)
                var wg sync.WaitGroup
                var mu sync.Mutex // Add mutex for concurrent map access

                for i := 0; i < numOperations; i++ {
                    wg.Add(1)
                    go func(idx int) {
                        defer wg.Done()
                        start := time.Now()
                        
                        action := &actions.CreateObjectAction{
                            ID:       fmt.Sprintf("load-test-%d", idx),
                            RegionID: regionID,
                            Code:     []byte("test code"),
                            Storage:  []byte("test storage"),
                        }

                        result, err := testVM.ExecuteInRegion(ctx, regionID, action)
                        if err != nil {
                            require.NoError(err)
                            return
                        }
                        results[idx] = result

                        elapsed := time.Since(start)
                        mu.Lock()
                        for j := 0; j < len(result.Attestations); j++ {
                            teeType := "sgx"
                            if j == 1 {
                                teeType = "sev"
                            }
                            metrics[teeType].executions++
                            metrics[teeType].latency += elapsed
                        }
                        mu.Unlock()
                    }(i)
                }

                wg.Wait()

                // Get final region metrics
                regionMetrics, err := testVM.GetRegionMetrics(ctx, regionID)
                if err != nil {
                    require.NoError(err)
                }

                // Verify load distribution
                sgxLoad := regionMetrics.TEEMetrics["sgx"].LoadFactor
                sevLoad := regionMetrics.TEEMetrics["sev"].LoadFactor
                loadDiff := math.Abs(sgxLoad - sevLoad)
                require.Less(loadDiff, 0.2, "Load should be balanced between TEEs")

                // Verify execution distribution
                sgxExecs := metrics["sgx"].executions
                sevExecs := metrics["sev"].executions
                execDiff := math.Abs(float64(sgxExecs - sevExecs))
                maxDiff := float64(numOperations) * 0.2
                require.Less(execDiff, maxDiff, "Executions should be evenly distributed")
            },
        },
        {
            name: "TEE Pair Latency Analysis",
            test: func(t *testing.T) {
                latencyStats := struct {
                    min   time.Duration
                    max   time.Duration
                    total time.Duration
                    count int
                }{}

                numOperations := 10
                for i := 0; i < numOperations; i++ {
                    start := time.Now()
                    action := &actions.CreateObjectAction{
                        ID:       fmt.Sprintf("latency-test-%d", i),
                        RegionID: regionID,
                        Code:     []byte("test code"),
                        Storage:  []byte("test storage"),
                    }

                    _, err := testVM.ExecuteInRegion(ctx, regionID, action)
                    if err != nil {
                        require.NoError(err)
                    }

                    latency := time.Since(start)
                    
                    if latencyStats.count == 0 || latency < latencyStats.min {
                        latencyStats.min = latency
                    }
                    if latency > latencyStats.max {
                        latencyStats.max = latency
                    }
                    latencyStats.total += latency
                    latencyStats.count++
                }

                avgLatency := latencyStats.total / time.Duration(latencyStats.count)
                require.Less(avgLatency, 1*time.Second)
                require.Less(latencyStats.max-latencyStats.min, 500*time.Millisecond)
            },
        },
        {
            name: "TEE Pair Resource Utilization",
            test: func(t *testing.T) {
                type resourceMetrics struct {
                    cpuUsage    float64
                    memoryUsage float64
                    timestamp   time.Time
                }

                var metrics []resourceMetrics
                interval := 100 * time.Millisecond
                duration := 1 * time.Second
                ticker := time.NewTicker(interval)
                defer ticker.Stop()

                done := make(chan struct{})
                go func() {
                    for i := 0; ; i++ {
                        select {
                        case <-done:
                            return
                        default:
                            action := &actions.CreateObjectAction{
                                ID:       fmt.Sprintf("resource-test-%d", i),
                                RegionID: regionID,
                                Code:     []byte("test code"),
                                Storage:  []byte("test storage"),
                            }
                            if _, err := testVM.ExecuteInRegion(ctx, regionID, action); err != nil {
                                continue
                            }
                        }
                    }
                }()

                timeout := time.After(duration)
                for {
                    select {
                    case <-ticker.C:
                        regionMetrics, err := testVM.GetRegionMetrics(ctx, regionID)
                        if err != nil {
                            require.NoError(err)
                            continue
                        }
                        
                        metrics = append(metrics, resourceMetrics{
                            cpuUsage:    regionMetrics.TEEMetrics["sgx"].LoadFactor,
                            memoryUsage: regionMetrics.TEEMetrics["sev"].LoadFactor,
                            timestamp:   time.Now(),
                        })
                    case <-timeout:
                        close(done)
                        goto analysis
                    }
                }

            analysis:
                var totalCPU, totalMemory float64
                for _, m := range metrics {
                    totalCPU += m.cpuUsage
                    totalMemory += m.memoryUsage
                }
                
                if len(metrics) > 0 {
                    avgCPU := totalCPU / float64(len(metrics))
                    avgMemory := totalMemory / float64(len(metrics))

                    require.Greater(avgCPU, 0.0)
                    require.Less(avgCPU, 1.0)
                    require.Greater(avgMemory, 0.0)
                    require.Less(avgMemory, 1.0)
                }
            },
        },
    }

    for _, tt := range tests {
        t.Run(tt.name, tt.test)
    }
}

// Helper function to generate load on TEE pair
func generateTEELoad(ctx context.Context, vm *MockVM, regionID string, duration time.Duration) <-chan struct{} {
    done := make(chan struct{})
    go func() {
        defer close(done)
        
        ticker := time.NewTicker(50 * time.Millisecond)
        defer ticker.Stop()
        
        timeout := time.After(duration)
        
        for {
            select {
            case <-ticker.C:
                action := &actions.CreateObjectAction{
                    ID:       fmt.Sprintf("load-test-%d", time.Now().UnixNano()),
                    RegionID: regionID,
                    Code:     []byte("test code"),
                    Storage:  []byte("test storage"),
                }
                _, _ = vm.ExecuteInRegion(ctx, regionID, action)
            case <-timeout:
                return
            case <-ctx.Done():
                return
            }
        }
    }()
    return done
}

// Helper function to collect performance metrics
func collectPerformanceMetrics(ctx context.Context, vm *MockVM, regionID string, interval time.Duration) []RegionMetrics {
    var metrics []RegionMetrics
    ticker := time.NewTicker(interval)
    defer ticker.Stop()

    for {
        select {
        case <-ticker.C:
            if m, err := vm.GetRegionMetrics(ctx, regionID); err == nil {
                metrics = append(metrics, *m)
            }
        case <-ctx.Done():
            return metrics
        }
    }
}


// Test TEE pair failover and recovery mechanisms
func TestTEEPairFailoverRecovery(t *testing.T) {
    testVM, regionID := setupTestEnvironment(t)
    ctx := context.Background()

    tests := []struct {
        name string
        test func(t *testing.T)
    }{
        {
            name: "TEE Pair Graceful Failover",
            test: func(t *testing.T) {
                // Setup initial state
                initialAction := &actions.CreateObjectAction{
                    ID:       "failover-test",
                    RegionID: regionID,
                    Code:     []byte("test code"),
                    Storage:  []byte("test storage"),
                }
                
                initialResult, err := testVM.ExecuteInRegion(ctx, regionID, initialAction)
                if err != nil {
                    t.Fatalf("failed to create initial state: %v", err)
                }

                // Create event with valid attestations
                event := &actions.SendEventAction{
                    IDTo:         "failover-test",
                    RegionID:     regionID,
                    FunctionCall: "test",
                    Parameters:   []byte("test"),
                    Attestations: initialResult.Attestations,  // Use original attestations
                }

                // Execute with valid attestations first
                result, err := testVM.ExecuteInRegion(ctx, regionID, event)
                if err != nil {
                    t.Fatalf("failed with valid attestations: %v", err)
                }

                // Use the successful attestations for next test
                validAttestations := result.Attestations

                // Create new event with simulated SGX failure
                failoverEvent := &actions.SendEventAction{
                    IDTo:         "failover-test",
                    RegionID:     regionID,
                    FunctionCall: "test",
                    Parameters:   []byte("test"),
                    Attestations: [2]core.TEEAttestation{
                        {
                            EnclaveID:   []byte("sgx-test-enclave-1234567890"),
                            Measurement: []byte("measurement1"),
                            Timestamp:   time.Now().UTC(),
                            Data:        []byte("test-state-hash"),
                            RegionProof: []byte("region-proof"),
                            Signature:   []byte("signature1"),
                        },
                        validAttestations[1], // Keep valid SEV attestation
                    },
                }

                failoverResult, err := testVM.ExecuteInRegion(ctx, regionID, failoverEvent)
                if err != nil {
                    t.Fatalf("should handle SGX failure gracefully: %v", err)
                }
                if len(failoverResult.Attestations[0].EnclaveID) == 0 {
                    t.Fatal("should have new SGX attestation")
                }
            },
        },
        {
            name: "TEE Pair Recovery After Failure",
            test: func(t *testing.T) {
                // Create initial load
                for i := 0; i < 5; i++ {
                    action := &actions.CreateObjectAction{
                        ID:       fmt.Sprintf("load-test-%d", i),
                        RegionID: regionID,
                        Code:     []byte("test code"),
                        Storage:  []byte("test storage"),
                    }
                    _, err := testVM.ExecuteInRegion(ctx, regionID, action)
                    if err != nil {
                        t.Fatalf("failed to create load: %v", err)
                    }
                }

                // Get initial metrics
                metrics, err := testVM.GetRegionMetrics(ctx, regionID)
                if err != nil {
                    t.Fatalf("failed to get initial metrics: %v", err)
                }
                initialSGXLoad := metrics.TEEMetrics["sgx"].LoadFactor
                initialSEVLoad := metrics.TEEMetrics["sev"].LoadFactor

                // Simulate failure and wait for recovery
                err = testVM.SimulateRegionFailure(regionID)
                if err != nil {
                    t.Fatalf("failed to simulate failure: %v", err)
                }
                time.Sleep(200 * time.Millisecond)

                // Get post-recovery metrics
                recoveryMetrics, err := testVM.GetRegionMetrics(ctx, regionID)
                if err != nil {
                    t.Fatalf("failed to get recovery metrics: %v", err)
                }

                // The load should be reset after recovery
                if recoveryMetrics.TEEMetrics["sgx"].LoadFactor >= initialSGXLoad {
                    t.Error("SGX load should be lower after recovery")
                }
                if recoveryMetrics.TEEMetrics["sev"].LoadFactor >= initialSEVLoad {
                    t.Error("SEV load should be lower after recovery")
                }

                // Test execution after recovery
                postRecoveryAction := &actions.CreateObjectAction{
                    ID:       "recovery-test",
                    RegionID: regionID,
                    Code:     []byte("test code"),
                    Storage:  []byte("test storage"),
                }

                result, err := testVM.ExecuteInRegion(ctx, regionID, postRecoveryAction)
                if err != nil {
                    t.Fatalf("should execute successfully after recovery: %v", err)
                }
                if result == nil {
                    t.Fatal("result is nil")
                }
                if len(result.Attestations) != 2 {
                    t.Fatal("expected 2 attestations")
                }
            },
        },
        {
            name: "TEE Pair State Recovery",
            test: func(t *testing.T) {
                // Create initial state
                initialState := make(map[string]*core.ObjectState)
                for i := 0; i < 3; i++ {
                    action := &actions.CreateObjectAction{
                        ID:       fmt.Sprintf("state-recovery-%d", i),
                        RegionID: regionID,
                        Code:     []byte("test code"),
                        Storage:  []byte("test storage"),
                    }
                    result, err := testVM.ExecuteInRegion(ctx, regionID, action)
                    if err != nil {
                        t.Fatalf("failed to create object %d: %v", i, err)
                    }
                    if result == nil {
                        t.Fatalf("nil result for object %d", i)
                    }

                    // Store initial state
                    obj, err := testVM.GetObject(ctx, fmt.Sprintf("state-recovery-%d", i), regionID)
                    if err != nil {
                        t.Fatalf("failed to get object %d: %v", i, err)
                    }
                    initialState[fmt.Sprintf("state-recovery-%d", i)] = obj
                }

                // Simulate failure and recovery
                err := testVM.SimulateRegionFailure(regionID)
                if err != nil {
                    t.Fatalf("failed to simulate failure: %v", err)
                }
                time.Sleep(200 * time.Millisecond)

                // Verify state after recovery
                for id, initialObj := range initialState {
                    recoveredObj, err := testVM.GetObject(ctx, id, regionID)
                    if err != nil {
                        t.Fatalf("failed to get recovered object %s: %v", id, err)
                    }
                    if !bytes.Equal(initialObj.Code, recoveredObj.Code) {
                        t.Errorf("code mismatch for object %s", id)
                    }
                    if !bytes.Equal(initialObj.Storage, recoveredObj.Storage) {
                        t.Errorf("storage mismatch for object %s", id)
                    }
                }
            },
        },
        {
            name: "TEE Pair Partial Failure Recovery",
            test: func(t *testing.T) {
                // Setup monitoring channels
                metricsChan := make(chan *RegionMetrics, 100)
                done := make(chan struct{})
                
                // Start metrics collection
                go func() {
                    defer close(metricsChan)
                    ticker := time.NewTicker(50 * time.Millisecond)
                    defer ticker.Stop()
                    
                    for {
                        select {
                        case <-ticker.C:
                            metrics, err := testVM.GetRegionMetrics(ctx, regionID)
                            if err == nil {
                                metricsChan <- metrics
                            }
                        case <-done:
                            return
                        }
                    }
                }()

                // Create initial state
                action := &actions.CreateObjectAction{
                    ID:       "partial-failure",
                    RegionID: regionID,
                    Code:     []byte("test code"),
                    Storage:  []byte("test storage"),
                }
                
                initialResult, err := testVM.ExecuteInRegion(ctx, regionID, action)
                if err != nil {
                    t.Fatalf("failed to create initial state: %v", err)
                }

                // Simulate partial failure (SGX only)
                testVM.mu.Lock()
                initialResult.Attestations[0] = core.TEEAttestation{} // Corrupt SGX attestation
                testVM.mu.Unlock()

                // Execute during partial failure
                failureAction := &actions.SendEventAction{
                    IDTo:         "partial-failure",
                    RegionID:     regionID,
                    FunctionCall: "test",
                    Parameters:   []byte("test"),
                    Attestations: initialResult.Attestations,
                }

                result, err := testVM.ExecuteInRegion(ctx, regionID, failureAction)
                if err != nil {
                    t.Fatalf("execution during partial failure failed: %v", err)
                }
                if result == nil {
                    t.Fatal("should get result even during partial failure")
                }

                // Stop metrics collection
                close(done)

                // Analyze metrics during failure and recovery
                var metrics []*RegionMetrics
                for metric := range metricsChan {
                    metrics = append(metrics, metric)
                }

                if len(metrics) == 0 {
                    t.Fatal("no metrics collected")
                }
                
                lastMetric := metrics[len(metrics)-1]
                if lastMetric.TEEMetrics["sgx"].SuccessRate <= 0 {
                    t.Error("SGX success rate should be positive")
                }
                if lastMetric.TEEMetrics["sev"].SuccessRate <= 0 {
                    t.Error("SEV success rate should be positive")
                }
            },
        },
    }

    for _, tt := range tests {
        t.Run(tt.name, tt.test)
    }
}


// Helper function to monitor TEE pair health
func monitorTEEHealth(ctx context.Context, vm *MockVM, regionID string) <-chan *HealthStatus {
    healthChan := make(chan *HealthStatus)
    
    go func() {
        defer close(healthChan)
        ticker := time.NewTicker(100 * time.Millisecond)
        defer ticker.Stop()

        for {
            select {
            case <-ticker.C:
                if status, err := vm.GetRegionHealth(ctx, regionID); err == nil {
                    healthChan <- status
                }
            case <-ctx.Done():
                return
            }
        }
    }()

    return healthChan
}

// Helper function to verify TEE pair recovery
func verifyTEERecovery(t *testing.T, healthStatuses []*HealthStatus) {
    require := require.New(t)
    
    // Verify recovery pattern
    var (
        sawFailure  bool
        sawRecovery bool
    )

    for _, status := range healthStatuses {
        if status.Status != "healthy" {
            sawFailure = true
        } else if sawFailure && status.Status == "healthy" {
            sawRecovery = true
            break
        }
    }

    require.True(sawFailure, "Should have detected failure")
    require.True(sawRecovery, "Should have detected recovery")
}

func TestCrossRegionCommunication(t *testing.T) {
    testVM, regionID1 := setupTestEnvironment(t)
    ctx := context.Background()

    // Create second region
    regionID2 := "test-region-2"
    err := testVM.RegisterRegion(ctx, regionID2, "mock://sgx2", "mock://sev2")
    if err != nil {
        t.Fatalf("failed to register second region: %v", err)
    }

    // Wait for region registration
    time.Sleep(100 * time.Millisecond)

    tests := []struct {
        name string
        test func(t *testing.T)
    }{
        {
            name: "Cross Region State Transfer",
            test: func(t *testing.T) {
                // Create object in region 1
                obj1ID := "cross-region-obj"
                createAction := &actions.CreateObjectAction{
                    ID:       obj1ID,
                    RegionID: regionID1,
                    Code:     []byte("test code"),
                    Storage:  []byte("test storage"),
                }
                
                result1, err := testVM.ExecuteInRegion(ctx, regionID1, createAction)
                if err != nil {
                    t.Fatalf("failed to create object in region 1: %v", err)
                }
                if result1 == nil {
                    t.Fatal("result from region 1 is nil")
                }

                // Create cross-region intent...
                intent := xregion.NewCrossRegionIntent(
                    "transfer-intent",
                    regionID1,
                    []string{regionID2},
                )

                intent.AddStateChange(regionID1, xregion.StateChange{
                    Key:       []byte(obj1ID),
                    Value:     result1.StateHash,
                    Operation: xregion.StateOpTransferOut,
                    Source:    regionID1,
                    Target:    regionID2,
                })
                intent.AddStateChange(regionID2, xregion.StateChange{
                    Key:       []byte(obj1ID),
                    Value:     result1.StateHash,
                    Operation: xregion.StateOpSet,
                    Source:    regionID1,
                    Target:    regionID2,
                })

                action := &actions.CrossRegionAction{
                    Intent: intent,
                }

                // Execute cross-region transfer
                result2, err := testVM.ExecuteInRegion(ctx, regionID1, action)
                if err != nil {
                    t.Fatalf("failed to execute cross-region transfer: %v", err)
                }
                if result2 == nil {
                    t.Fatal("cross-region transfer result is nil")
                }
                      
                // Verify object exists in region 2
                obj2, err := testVM.GetObject(ctx, obj1ID, regionID2)
                if err != nil {
                    t.Fatalf("failed to get object from region 2: %v", err)
                }
                if obj2 == nil {
                    t.Fatal("object not found in region 2")
                }
            },
        },
        {
            name: "Cross Region Proof Verification",
            test: func(t *testing.T) {
                // Get proof from region 1
                proof1, err := testVM.GetRegionStateProof(ctx, regionID1, "test-key")
                if err != nil {
                    t.Fatalf("failed to get proof from region 1: %v", err)
                }
                
                // Verify proof in region 2
                valid, err := testVM.VerifyRegionStateProof(ctx, regionID2, proof1)
                if err != nil {
                    t.Fatalf("failed to verify proof in region 2: %v", err)
                }
                if !valid {
                    t.Fatal("proof verification failed")
                }
            },
        },
        {
            name: "Cross Region Concurrent Operations",
            test: func(t *testing.T) {
                var wg sync.WaitGroup
                errors := make(chan error, 10)
                
                for i := 0; i < 5; i++ {
                    wg.Add(2)
                    
                    // Region 1 operation
                    go func(idx int) {
                        defer wg.Done()
                        action := createTestAction()
                        _, err := testVM.ExecuteInRegion(ctx, regionID1, action)
                        if err != nil {
                            errors <- err
                        }
                    }(i)
                    
                    // Region 2 operation
                    go func(idx int) {
                        defer wg.Done()
                        action := createTestAction()
                        _, err := testVM.ExecuteInRegion(ctx, regionID2, action)
                        if err != nil {
                            errors <- err
                        }
                    }(i)
                }
                
                wg.Wait()
                close(errors)
                
                for err := range errors {
                    if err != nil {
                        t.Errorf("concurrent operation error: %v", err)
                    }
                }
            },
        },
    }

    for _, tt := range tests {
        t.Run(tt.name, tt.test)
    }
}


func TestRegionalTEEPairSync(t *testing.T) {
    testVM, regionID := setupTestEnvironment(t)
    require := require.New(t)
    ctx := context.Background()

    tests := []struct {
        name string
        test func(t *testing.T)
    }{
        {
            name: "TEE Pair State Sync",
test: func(t *testing.T) {
    // Create initial state
    result1, err := testVM.ExecuteInRegion(ctx, regionID, createTestAction())
    if err != nil {
        t.Fatalf("failed to execute initial action: %v", err)
    }
    if result1 == nil {
        t.Fatal("initial result is nil")
    }

    // Force state desync
    if err := testVM.SimulateStateDesync(regionID); err != nil {
        t.Fatalf("failed to simulate state desync: %v", err)
    }
    
    // Verify automatic resync
    result2, err := testVM.ExecuteInRegion(ctx, regionID, createTestAction())
    if err != nil {
        t.Fatalf("failed to execute after desync: %v", err)
    }
    if result2 == nil {
        t.Fatal("post-desync result is nil")
    }

    // Verify states are synced
    if !bytes.Equal(result2.Attestations[0].Data, result2.Attestations[1].Data) {
        t.Fatal("TEE states are not synced")
    }
},
        },
        {
            name: "TEE Pair Time Sync",
            test: func(t *testing.T) {
                // Execute operation and verify time synchronization
                result, err := testVM.ExecuteInRegion(ctx, regionID, createTestAction())
                require.NoError(err)
                
                // Verify timestamps are within acceptable range
                timeDiff := result.Attestations[1].Timestamp.Sub(result.Attestations[0].Timestamp)
                require.Less(timeDiff.Abs(), 100*time.Millisecond)
            },
        },
    }

    for _, tt := range tests {
        t.Run(tt.name, tt.test)
    }
}

func TestNetworkPartitionHandling(t *testing.T) {
    testVM, regionID := setupTestEnvironment(t)
    ctx := context.Background()

    // Remove the duplicate region registration since it's done in setupTestEnvironment
    /*
    if err := testVM.RegisterRegion(ctx, regionID, "mock://sgx", "mock://sev"); err != nil {
        t.Fatalf("failed to register region: %v", err)
    }
    */

    tests := []struct {
        name string
        test func(t *testing.T)
    }{
        {
            name: "Region Network Partition Recovery",
            test: func(t *testing.T) {
                // First create test object
                createAction := &actions.CreateObjectAction{
                    ID:       "partition-test-object",
                    RegionID: regionID,
                    Code:     []byte("test code"),
                    Storage:  []byte("test storage"),
                }

                // Create initial state
                result1, err := testVM.ExecuteInRegion(ctx, regionID, createAction)
                if err != nil {
                    t.Fatalf("failed to create initial object: %v", err)
                }
                if result1 == nil {
                    t.Fatal("initial result is nil")
                }

                // Create event to test with
                event := &actions.SendEventAction{
                    IDTo:         "partition-test-object",
                    RegionID:     regionID,
                    FunctionCall: "test",
                    Parameters:   []byte("test"),
                    Attestations: result1.Attestations,
                }
                
                // Simulate network partition
                if err := testVM.SimulateNetworkPartition(regionID); err != nil {
                    t.Fatalf("failed to simulate network partition: %v", err)
                }
                
                // Wait for partition healing
                time.Sleep(200 * time.Millisecond)
                
                // Verify operation after recovery
                result2, err := testVM.ExecuteInRegion(ctx, regionID, event)
                if err != nil {
                    t.Fatalf("failed to execute action after recovery: %v", err)
                }
                if result2 == nil {
                    t.Fatal("post-recovery result is nil")
                }

                // Verify state consistency
                verifyTEEStateConsistency(t, result2.Attestations)
            },
        },
        {
            name: "Partial Network Connectivity",
            test: func(t *testing.T) {
                // Create test object
                createAction := &actions.CreateObjectAction{
                    ID:       "partial-conn-test-object",
                    RegionID: regionID,
                    Code:     []byte("test code"),
                    Storage:  []byte("test storage"),
                }

                // Create initial state
                result1, err := testVM.ExecuteInRegion(ctx, regionID, createAction)
                if err != nil {
                    t.Fatalf("failed to create initial object: %v", err)
                }

                // Simulate partial connectivity
                if err := testVM.SimulatePartialConnectivity(regionID); err != nil {
                    t.Fatalf("failed to simulate partial connectivity: %v", err)
                }
                
                // Create event to test with
                event := &actions.SendEventAction{
                    IDTo:         "partial-conn-test-object",
                    RegionID:     regionID,
                    FunctionCall: "test",
                    Parameters:   []byte("test"),
                    Attestations: result1.Attestations,
                }
                
                // Verify operations still succeed with degraded performance
                result2, err := testVM.ExecuteInRegion(ctx, regionID, event)
                if err != nil {
                    t.Fatalf("failed to execute with partial connectivity: %v", err)
                }
                if result2 == nil {
                    t.Fatal("result under partial connectivity is nil")
                }

                // Verify state consistency even under partial connectivity
                verifyTEEStateConsistency(t, result2.Attestations)
            },
        },
    }

    for _, tt := range tests {
        t.Run(tt.name, tt.test)
    }
}

// Add helper methods for the struct
func (m *TEEMetrics) IsHealthy() bool {
    return m.Status == "healthy" && 
           m.SuccessRate > 0.95 && 
           m.LoadFactor < 0.9 &&
           m.ConsecutiveErrors == 0
}

func (m *TEEMetrics) UpdateMetrics(executionTime float64, success bool) {
    m.LastHealthCheck = time.Now()
    m.ExecutionTime = (m.ExecutionTime + executionTime) / 2 // Running average
    m.TaskCount++

    if success {
        m.ConsecutiveErrors = 0
        m.SuccessRate = (m.SuccessRate*float64(m.TaskCount-1) + 1) / float64(m.TaskCount)
    } else {
        m.ErrorCount++
        m.ConsecutiveErrors++
        m.SuccessRate = (m.SuccessRate*float64(m.TaskCount-1)) / float64(m.TaskCount)
    }

    // Update status based on metrics
    if m.ConsecutiveErrors > 5 {
        m.Status = "failed"
    } else if m.SuccessRate < 0.95 || m.LoadFactor > 0.9 {
        m.Status = "degraded"
    } else {
        m.Status = "healthy"
    }
}

func NewTEEMetrics(enclaveID []byte, teeType string) *TEEMetrics {
    now := time.Now()
    return &TEEMetrics{
        EnclaveID:        enclaveID,
        Type:             teeType,
        LoadFactor:       0.0,
        SuccessRate:      1.0,
        LastAttested:     now,
        LastHealthCheck:  now,
        ExecutionTime:    0,
        ErrorCount:       0,
        TaskCount:        0,
        ConsecutiveErrors: 0,
        Status:           "healthy",
    }
}

func (m *TEEMetrics) UpdateLoadFactor(newLoad float64) {
    m.LoadFactor = newLoad
    if m.LoadFactor > 0.9 {
        m.Status = "degraded"
    } else if m.Status == "degraded" && m.ConsecutiveErrors == 0 {
        m.Status = "healthy"
    }
}

func (m *TEEMetrics) ResetMetrics() {
    m.LoadFactor = 0.0
    m.SuccessRate = 1.0
    m.ErrorCount = 0
    m.ConsecutiveErrors = 0
    m.TaskCount = 0
    m.ExecutionTime = 0
    m.Status = "healthy"
    m.LastHealthCheck = time.Now()
}

func (m *TEEMetrics) NeedsAttestation() bool {
    return time.Since(m.LastAttested) > 5*time.Minute
}

func (m *TEEMetrics) UpdateAttestation() {
    m.LastAttested = time.Now()
}