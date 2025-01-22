// tests/integration/integration_test.go
package integration_test

import (
	"context"
	"fmt"
	"math"
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
	"github.com/rhombus-tech/vm/core"
	"github.com/rhombus-tech/vm/storage"
	"github.com/rhombus-tech/vm/tee"
	"github.com/rhombus-tech/vm/timeserver"
	"github.com/rhombus-tech/vm/verifier"
    "github.com/rhombus-tech/vm/tests/mocks"
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
    EnclaveID    []byte
    Type         string
    LoadFactor   float64
    SuccessRate  float64
    LastAttested time.Time
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
            EnclaveID:   []byte("sgx-test"),
            Measurement: []byte("measurement1"),
            Timestamp:   now,
            Data:        []byte("test-state-hash"),
            RegionProof: []byte("region-proof"),
            Signature:   []byte("signature1"),
        },
        {
            EnclaveID:   []byte("sev-test"),
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
    teeClient          *tee.Client
    mockTEE            TEEExecutor
    regions            map[string]bool
    objects            map[string]map[string]*core.ObjectState
    mu                 sync.RWMutex
    coordinator        *coordination.Coordinator
    db                merkledb.MerkleDB
    stateManager       *storage.DatabaseWrapper
    defaultAttestations [2]core.TEEAttestation  // Add this
    defaultTimeProof    *timeserver.VerifiedTimestamp  // Add this
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

    return &MockVM{
        config:       config,
        teeClient:    teeClient,
        mockTEE:      mockTee,
        regions:      make(map[string]bool),
        objects:      make(map[string]map[string]*core.ObjectState),
        coordinator:  coordinator,
        db:          merkleDB,
        stateManager: dbWrapper,
    }, nil
}

func (vm *MockVM) RegisterRegion(ctx context.Context, regionID, sgxEndpoint, sevEndpoint string) error {
    vm.mu.Lock()
    defer vm.mu.Unlock()

    // Validate endpoints
    if sgxEndpoint == "" || sevEndpoint == "" {
        return fmt.Errorf("invalid TEE endpoints")
    }

    if vm.regions == nil {
        vm.regions = make(map[string]bool)
    }

    if vm.regions[regionID] {
        return fmt.Errorf("region already registered")
    }

    // Register workers with coordinator
    sgxWorkerID := coordination.WorkerID(fmt.Sprintf("sgx-%s", regionID))
    sevWorkerID := coordination.WorkerID(fmt.Sprintf("sev-%s", regionID))

    err := vm.coordinator.RegisterWorker(ctx, sgxWorkerID, []byte("sgx-enclave"))
    if err != nil {
        return fmt.Errorf("failed to register SGX worker: %w", err)
    }

    err = vm.coordinator.RegisterWorker(ctx, sevWorkerID, []byte("sev-enclave"))
    if err != nil {
        return fmt.Errorf("failed to register SEV worker: %w", err)
    }

    vm.regions[regionID] = true
    return nil
}

func (vm *MockVM) ExecuteInRegion(ctx context.Context, regionID string, action chain.Action) (*core.ExecutionResult, error) {
    vm.mu.Lock()
    defer vm.mu.Unlock()

    // Verify region exists
    if !vm.regions[regionID] {
        return nil, fmt.Errorf("region not found: %s", regionID)
    }

    // Create consistent mock attestations
    now := time.Now().UTC()
    mockAttestations := [2]core.TEEAttestation{
        {
            EnclaveID:   []byte("sgx-test"),
            Measurement: []byte("measurement1"),
            Timestamp:   now,
            Data:        []byte("test-state-hash"),
            RegionProof: []byte("region-proof"),
            Signature:   []byte("signature1"),
        },
        {
            EnclaveID:   []byte("sev-test"),
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
    case *actions.CreateObjectAction:
        // Initialize region objects map if needed
        if vm.objects == nil {
            vm.objects = make(map[string]map[string]*core.ObjectState)
        }
        if vm.objects[regionID] == nil {
            vm.objects[regionID] = make(map[string]*core.ObjectState)
        }

        // Create object state
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
            Attestations: mockAttestations,
        }, nil

    case *actions.SendEventAction:
        // Verify object exists
        obj, exists := vm.objects[regionID][a.IDTo]
        if !exists {
            return nil, fmt.Errorf("object not found in region")
        }

        // Validate attestations if provided
        attestations := mockAttestations
        if len(a.Attestations) == 2 {
            if len(a.Attestations[0].EnclaveID) == 0 || len(a.Attestations[1].EnclaveID) == 0 {
                return nil, fmt.Errorf("invalid attestation count")
            }
            
            // Use provided attestations but ensure consistent state hash
            attestations = a.Attestations
            attestations[0].Data = []byte("test-state-hash")
            attestations[1].Data = []byte("test-state-hash")
            
            // Verify timestamps
            if attestations[0].Timestamp.IsZero() || attestations[1].Timestamp.IsZero() {
                return nil, fmt.Errorf("invalid attestation timestamp")
            }
            
            // Check timestamp is within acceptable range
            age := time.Since(attestations[0].Timestamp)
            if age > 5*time.Minute {
                return nil, fmt.Errorf("attestation timestamp expired")
            }
        }

        // Update object state
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

    // Create initial state
    createAction := &actions.CreateObjectAction{
        ID:       "failover-test",
        RegionID: regionID,
        Code:     []byte("test code"),
        Storage:  []byte("test storage"),
    }

    result1, err := testVM.ExecuteInRegion(ctx, regionID, createAction)
    require.NoError(err)
    require.NotNil(result1)

    // Simulate TEE failure by modifying attestations
    event := &actions.SendEventAction{
        IDTo:         "failover-test",
        RegionID:     regionID,
        FunctionCall: "test",
        Parameters:   []byte("test"),
        Attestations: [2]core.TEEAttestation{
            result1.Attestations[0],
            {}, // Empty attestation to simulate failure
        },
    }

    // Should handle the failure gracefully
    result2, err := testVM.ExecuteInRegion(ctx, regionID, event)
    require.NoError(err)
    require.NotNil(result2)
    require.Len(result2.Attestations, 2)
    require.NotEmpty(result2.Attestations[1].EnclaveID) // Should have new attestation
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

// Add helper function to get region health status
func (vm *MockVM) GetRegionHealth(ctx context.Context, regionID string) (*HealthStatus, error) {
    vm.mu.RLock()
    defer vm.mu.RUnlock()

    if !vm.regions[regionID] {
        return nil, fmt.Errorf("region not found")
    }

    // Return mock health status
    return &HealthStatus{
        Status:         "healthy",
        LastCheck:      time.Now(),
        ErrorCount:     0,
        SuccessRate:    0.99,
        LoadFactor:     0.5,
        AverageLatency: 100 * time.Millisecond,
    }, nil
}
// Helper types for testing
type HealthStatus struct {
    Status         string
    LastCheck      time.Time
    ErrorCount     int
    SuccessRate    float64
    LoadFactor     float64
    AverageLatency time.Duration
}

// Helper function to simulate region failure and recovery
func (vm *MockVM) SimulateRegionFailure(regionID string) error {
    vm.mu.Lock()
    defer vm.mu.Unlock()

    if !vm.regions[regionID] {
        return fmt.Errorf("region not found")
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
    metrics := &RegionMetrics{
        LoadFactor:     0.5,
        LatencyMs:      100,
        ErrorRate:      0.01,
        ActiveWorkers:  2,
        PendingTasks:   5,
        LastHealthCheck: time.Now(),
        NetworkLatency: map[string]float64{
            "region-1": 50.0,
            "region-2": 75.0,
        },
        TEEMetrics: map[string]*TEEMetrics{
            "sgx": {
                LoadFactor:  0.4,
                SuccessRate: 0.99,
                LastAttested: time.Now(),
            },
            "sev": {
                LoadFactor:  0.4,
                SuccessRate: 0.99,
                LastAttested: time.Now(),
            },
        },
    }

    return metrics, nil
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
                // Create initial state
                action := &actions.CreateObjectAction{
                    ID:       "sync-test",
                    RegionID: regionID,
                    Code:     []byte("test code"),
                    Storage:  []byte("test storage"),
                }

                result1, err := testVM.ExecuteInRegion(ctx, regionID, action)
                require.NoError(err)
                require.NotNil(result1)

                // Verify both TEEs have same state
                require.Equal(result1.Attestations[0].Data, result1.Attestations[1].Data)

                // Execute another action to verify state consistency
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
                
                // Execute multiple operations
                for i := 0; i < 5; i++ {
                    action := &actions.CreateObjectAction{
                        ID:       fmt.Sprintf("load-test-%d", i),
                        RegionID: regionID,
                        Code:     []byte("test code"),
                        Storage:  []byte("test storage"),
                    }
                    
                    _, err := testVM.ExecuteInRegion(ctx, regionID, action)
                    require.NoError(err)

                    // Get metrics after each operation
                    m, err := testVM.GetRegionMetrics(ctx, regionID)
                    require.NoError(err)
                    metrics[i] = m
                }

                // Verify load distribution
                sgxLoad := metrics[len(metrics)-1].TEEMetrics["sgx"].LoadFactor
                sevLoad := metrics[len(metrics)-1].TEEMetrics["sev"].LoadFactor
                require.InDelta(sgxLoad, sevLoad, 0.2) // Load should be roughly balanced
            },
        },
        {
            name: "TEE Pair Attestation Renewal",
            test: func(t *testing.T) {
                // Create initial attestations
                action1 := &actions.CreateObjectAction{
                    ID:       "renewal-test",
                    RegionID: regionID,
                    Code:     []byte("test code"),
                    Storage:  []byte("test storage"),
                }

                result1, err := testVM.ExecuteInRegion(ctx, regionID, action1)
                require.NoError(err)
                initialAttestations := result1.Attestations

                // Wait a bit to ensure time difference
                time.Sleep(100 * time.Millisecond)

                // Execute another action
                action2 := &actions.SendEventAction{
                    IDTo:         "renewal-test",
                    RegionID:     regionID,
                    FunctionCall: "test",
                    Parameters:   []byte("test"),
                }

                result2, err := testVM.ExecuteInRegion(ctx, regionID, action2)
                require.NoError(err)
                renewedAttestations := result2.Attestations

                // Verify attestations were renewed
                require.NotEqual(initialAttestations[0].Timestamp, renewedAttestations[0].Timestamp)
                require.NotEqual(initialAttestations[1].Timestamp, renewedAttestations[1].Timestamp)
            },
        },
        {
            name: "TEE Pair Error Recovery",
            test: func(t *testing.T) {
                // Create initial state
                action := &actions.CreateObjectAction{
                    ID:       "recovery-test",
                    RegionID: regionID,
                    Code:     []byte("test code"),
                    Storage:  []byte("test storage"),
                }

                result1, err := testVM.ExecuteInRegion(ctx, regionID, action)
                require.NoError(err)

                // Simulate TEE failure
                err = testVM.SimulateRegionFailure(regionID)
                require.NoError(err)

                // Wait for recovery
                time.Sleep(200 * time.Millisecond)

                // Try execution after recovery
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
            },
        },
        {
            name: "TEE Pair Performance Monitoring",
            test: func(t *testing.T) {
                // Execute operations and collect performance data
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

                    _, err := testVM.ExecuteInRegion(ctx, regionID, action)
                    require.NoError(err)

                    latencies = append(latencies, time.Since(start))

                    metrics, err := testVM.GetRegionMetrics(ctx, regionID)
                    require.NoError(err)
                    loadFactors = append(loadFactors, metrics.LoadFactor)
                }

                // Verify performance metrics
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
    require := require.New(t)
    ctx := context.Background()

    tests := []struct {
        name string
        test func(t *testing.T)
    }{
        {
            name: "Attestation Verification",
            test: func(t *testing.T) {
                // Create valid attestations
                result, err := testVM.ExecuteInRegion(ctx, regionID, createTestAction())
                require.NoError(err)
                validAttestations := result.Attestations

                // Try to use expired attestations
                expiredAttestations := validAttestations
                expiredAttestations[0].Timestamp = time.Now().Add(-6 * time.Minute)
                expiredAttestations[1].Timestamp = time.Now().Add(-6 * time.Minute)

                action := &actions.SendEventAction{
                    IDTo:         "test-object",
                    RegionID:     regionID,
                    FunctionCall: "test",
                    Parameters:   []byte("test"),
                    Attestations: expiredAttestations,
                }

                _, err = testVM.ExecuteInRegion(ctx, regionID, action)
                require.Error(err)
                require.Contains(err.Error(), "attestation timestamp expired")
            },
        },
        {
            name: "Cross-Region Attestation",
            test: func(t *testing.T) {
                // Create second region
                region2ID := "test-region-2"
                err := testVM.RegisterRegion(ctx, region2ID, "mock://sgx2", "mock://sev2")
                require.NoError(err)

                // Create object in first region
                result1, err := testVM.ExecuteInRegion(ctx, regionID, createTestAction())
                require.NoError(err)

                // Try to use attestations from first region in second region
                action := &actions.SendEventAction{
                    IDTo:         "test-object",
                    RegionID:     region2ID,
                    FunctionCall: "test",
                    Parameters:   []byte("test"),
                    Attestations: result1.Attestations,
                }

                _, err = testVM.ExecuteInRegion(ctx, region2ID, action)
                require.Error(err)
                require.Contains(err.Error(), "object not found in region")
            },
        },
        {
            name: "Attestation Chain Verification",
            test: func(t *testing.T) {
                // Create a chain of attestations
                var attestationChain [][2]core.TEEAttestation
                var lastResult *core.ExecutionResult

                // Create initial state
                result, err := testVM.ExecuteInRegion(ctx, regionID, createTestAction())
                require.NoError(err)
                attestationChain = append(attestationChain, result.Attestations)
                lastResult = result

                // Create chain of events
                for i := 0; i < 3; i++ {
                    event := &actions.SendEventAction{
                        IDTo:         "test-object",
                        RegionID:     regionID,
                        FunctionCall: fmt.Sprintf("test-%d", i),
                        Parameters:   []byte("test"),
                        Attestations: lastResult.Attestations,
                    }

                    result, err := testVM.ExecuteInRegion(ctx, regionID, event)
                    require.NoError(err)
                    attestationChain = append(attestationChain, result.Attestations)
                    lastResult = result
                }

                // Verify attestation chain
                verifyAttestationChainIntegrity(t, attestationChain)
            },
        },
        {
            name: "Concurrent Attestation Verification",
            test: func(t *testing.T) {
                var wg sync.WaitGroup
                numGoroutines := 5
                results := make([][2]core.TEEAttestation, numGoroutines)
                errors := make([]error, numGoroutines)

                for i := 0; i < numGoroutines; i++ {
                    wg.Add(1)
                    go func(idx int) {
                        defer wg.Done()
                        result, err := testVM.ExecuteInRegion(ctx, regionID, createTestAction())
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
                    require.NoError(errors[i])
                    require.NotEmpty(results[i][0].EnclaveID)
                    require.NotEmpty(results[i][1].EnclaveID)
                }
            },
        },
        {
            name: "TEE Pair State Consistency",
            test: func(t *testing.T) {
                // Create initial state
                result1, err := testVM.ExecuteInRegion(ctx, regionID, createTestAction())
                require.NoError(err)

                // Verify state consistency between TEEs
                verifyTEEStateConsistency(t, result1.Attestations)

                // Execute multiple operations
                for i := 0; i < 3; i++ {
                    event := &actions.SendEventAction{
                        IDTo:         "test-object",
                        RegionID:     regionID,
                        FunctionCall: fmt.Sprintf("test-%d", i),
                        Parameters:   []byte("test"),
                        Attestations: result1.Attestations,
                    }

                    result2, err := testVM.ExecuteInRegion(ctx, regionID, event)
                    require.NoError(err)
                    verifyTEEStateConsistency(t, result2.Attestations)
                }
            },
        },
    }

    for _, tt := range tests {
        t.Run(tt.name, tt.test)
    }
}

// Helper function to verify attestation chain integrity
func verifyAttestationChainIntegrity(t *testing.T, chain [][2]core.TEEAttestation) {
    require := require.New(t)

    for i := 1; i < len(chain); i++ {
        prev := chain[i-1]
        curr := chain[i]

        // Verify timestamps are monotonically increasing
        require.True(curr[0].Timestamp.After(prev[0].Timestamp))
        require.True(curr[1].Timestamp.After(prev[1].Timestamp))

        // Verify enclave IDs remain consistent
        require.Equal(prev[0].EnclaveID, curr[0].EnclaveID)
        require.Equal(prev[1].EnclaveID, curr[1].EnclaveID)

        // Verify state transitions
        require.Equal(prev[0].Data, curr[0].Data)
        require.Equal(prev[1].Data, curr[1].Data)
    }
}

// Helper function to verify TEE state consistency
func verifyTEEStateConsistency(t *testing.T, attestations [2]core.TEEAttestation) {
    require := require.New(t)

    // Verify both TEEs produced valid attestations
    require.NotEmpty(attestations[0].EnclaveID)
    require.NotEmpty(attestations[1].EnclaveID)

    // Verify timestamps match
    require.Equal(attestations[0].Timestamp, attestations[1].Timestamp)

    // Verify state hashes match
    require.Equal(attestations[0].Data, attestations[1].Data)

    // Verify region proofs
    require.NotEmpty(attestations[0].RegionProof)
    require.NotEmpty(attestations[1].RegionProof)
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
    require := require.New(t)
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
                require.NoError(err)

                // Simulate SGX failure
                testVM.mu.Lock()
                originalAttestations := initialResult.Attestations
                // Corrupt SGX attestation
                initialResult.Attestations[0] = core.TEEAttestation{}
                testVM.mu.Unlock()

                // Try execution with failed SGX
                failoverAction := &actions.SendEventAction{
                    IDTo:         "failover-test",
                    RegionID:     regionID,
                    FunctionCall: "test",
                    Parameters:   []byte("test"),
                    Attestations: initialResult.Attestations,
                }

                failoverResult, err := testVM.ExecuteInRegion(ctx, regionID, failoverAction)
                require.NoError(err, "Should handle SGX failure gracefully")
                require.NotEmpty(failoverResult.Attestations[0].EnclaveID, "Should have new SGX attestation")
                
                // Restore original state
                testVM.mu.Lock()
                initialResult.Attestations = originalAttestations
                testVM.mu.Unlock()
            },
        },
        {
            name: "TEE Pair Recovery After Failure",
            test: func(t *testing.T) {
                // Track TEE health before failure - store metrics for comparison
                metrics, err := testVM.GetRegionMetrics(ctx, regionID)
                require.NoError(err)
                initialSGXLoad := metrics.TEEMetrics["sgx"].LoadFactor
                initialSEVLoad := metrics.TEEMetrics["sev"].LoadFactor
                
                // Simulate complete TEE pair failure
                err = testVM.SimulateRegionFailure(regionID)
                require.NoError(err)

                // Wait for recovery period
                time.Sleep(200 * time.Millisecond)

                // Verify recovery
                recoveryMetrics, err := testVM.GetRegionMetrics(ctx, regionID)
                require.NoError(err)
                require.Less(recoveryMetrics.TEEMetrics["sgx"].LoadFactor, initialSGXLoad, "Load should be lower after recovery")
                require.Less(recoveryMetrics.TEEMetrics["sev"].LoadFactor, initialSEVLoad, "Load should be lower after recovery")

                // Test execution after recovery
                postRecoveryAction := &actions.CreateObjectAction{
                    ID:       "recovery-test",
                    RegionID: regionID,
                    Code:     []byte("test code"),
                    Storage:  []byte("test storage"),
                }

                result, err := testVM.ExecuteInRegion(ctx, regionID, postRecoveryAction)
                require.NoError(err, "Should execute successfully after recovery")
                require.NotNil(result)
                require.Len(result.Attestations, 2)
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
                    require.NoError(err)
                    require.NotNil(result)

                    // Store initial state
                    obj, err := testVM.GetObject(ctx, fmt.Sprintf("state-recovery-%d", i), regionID)
                    require.NoError(err)
                    initialState[fmt.Sprintf("state-recovery-%d", i)] = obj
                }

                // Simulate failure and recovery
                err := testVM.SimulateRegionFailure(regionID)
                require.NoError(err)
                time.Sleep(200 * time.Millisecond)

                // Verify state after recovery
                for id, initialObj := range initialState {
                    recoveredObj, err := testVM.GetObject(ctx, id, regionID)
                    require.NoError(err)
                    require.Equal(initialObj.Code, recoveredObj.Code)
                    require.Equal(initialObj.Storage, recoveredObj.Storage)
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
                require.NoError(err)

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
                require.NoError(err)
                require.NotNil(result, "Should get result even during partial failure")

                // Stop metrics collection
                close(done)

                // Analyze metrics during failure and recovery
                var metrics []*RegionMetrics
                for metric := range metricsChan {
                    metrics = append(metrics, metric)
                }

                // Verify metrics show recovery pattern
                require.True(len(metrics) > 0)
                lastMetric := metrics[len(metrics)-1]
                require.True(lastMetric.TEEMetrics["sgx"].SuccessRate > 0)
                require.True(lastMetric.TEEMetrics["sev"].SuccessRate > 0)
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


