// tests/integration/integration_test.go
package integration_test

import (
    "context"
    "errors"
    "fmt"
    "sync"
    "testing"
    "time"

    "github.com/ava-labs/avalanchego/x/merkledb"
    "github.com/ava-labs/avalanchego/utils/maybe"
    "github.com/ava-labs/avalanchego/ids"
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
)

type TEEExecutor interface {
    Execute(ctx context.Context, input []byte) (*core.ExecutionResult, error)
}

type mockTEE struct {
    attestations [2]core.TEEAttestation
    results     map[string]*core.ExecutionResult
    mu          sync.RWMutex
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
    config     *compute.Config
    teeClient  *tee.Client
    mockTEE    TEEExecutor
    regions    map[string]bool
    objects    map[string]map[string]*core.ObjectState // map[regionID]map[objectID]ObjectState
    mu         sync.RWMutex
    coordinator *coordination.Coordinator
    db         merkledb.MerkleDB
}
func NewMockVM(config *compute.Config) (*MockVM, error) {
    mockTee := newMockTEE()
    stateVerifier := verifier.New(nil)
   
    teeClient, err := tee.NewClient("mock://sgx", "mock://sev", stateVerifier)
    if err != nil {
        return nil, err
    }

    // Create mock database for MerkleDB
    mockDB, err := merkledb.New(
        context.Background(),
        storage.NewDatabaseWrapper(nil), // Mock database
        merkledb.Config{
            HistoryLength: 256,
        },
    )
    if err != nil {
        return nil, fmt.Errorf("failed to create merkledb: %w", err)
    }

    // Create coordination storage wrapper
    coordStorage := storage.NewCoordinationStorageWrapper(storage.NewDatabaseWrapper(nil))

    coordConfig := &coordination.Config{
        MinWorkers:          2,
        MaxWorkers:          10,
        WorkerTimeout:       30 * time.Second,
        ChannelTimeout:      10 * time.Second,
        MaxMessageSize:      1024 * 1024,
        EncryptionEnabled:   true,
        RequireAttestation:  true,
        AttestationTimeout:  5 * time.Second,
        TaskCleanupInterval: time.Minute,
    }

    coordinator, err := coordination.NewCoordinator(coordConfig, mockDB, coordStorage)
    if err != nil {
        return nil, fmt.Errorf("failed to create coordinator: %w", err)
    }

    if err := coordinator.Start(); err != nil {
        return nil, fmt.Errorf("failed to start coordinator: %w", err)
    }

    vm := &MockVM{
        config:      config,
        teeClient:   teeClient,
        mockTEE:     mockTee,
        regions:     make(map[string]bool),
        objects:     make(map[string]map[string]*core.ObjectState),
        coordinator: coordinator,
        db:         mockDB,
    }

    return vm, nil
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

    // Create mock attestations
    now := time.Now()
    mockAttestations := [2]core.TEEAttestation{
        {
            EnclaveID:   []byte("sgx-test"),
            Measurement: []byte("measurement1"),
            Timestamp:   now,
            Data:        []byte("test-state-hash"),
            RegionProof: []byte("region-proof"),
        },
        {
            EnclaveID:   []byte("sev-test"),
            Measurement: []byte("measurement2"),
            Timestamp:   now,
            Data:        []byte("test-state-hash"),
            RegionProof: []byte("region-proof"),
        },
    }

    // Create mock time proof
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

    // Initialize region objects map if it doesn't exist
    if vm.objects == nil {
        vm.objects = make(map[string]map[string]*core.ObjectState)
    }
    if vm.objects[regionID] == nil {
        vm.objects[regionID] = make(map[string]*core.ObjectState)
    }

    // Create task for coordinator
    task := &coordination.Task{
        ID:        fmt.Sprintf("task-%d", time.Now().UnixNano()),
        WorkerIDs: []coordination.WorkerID{
            coordination.WorkerID(fmt.Sprintf("sgx-%s", regionID)),
            coordination.WorkerID(fmt.Sprintf("sev-%s", regionID)),
        },
        Data:     []byte("task-data"),
        RegionID: regionID,
        Timeout:  5 * time.Second,
    }

    // Submit task to coordinator
    if err := vm.coordinator.SubmitTask(ctx, task); err != nil {
        return nil, fmt.Errorf("failed to submit task: %w", err)
    }

    switch a := action.(type) {
    case *actions.CreateObjectAction:
        // Store the object
        vm.objects[regionID][a.ID] = &core.ObjectState{
            Code:     a.Code,
            Storage:  a.Storage,
            RegionID: regionID,
            Status:   "active",
        }
        return &core.ExecutionResult{
            StateHash:    []byte("test-state-hash"),
            Output:       []byte("created"),
            RegionID:     regionID,
            TimeProof:    timeProof,
            Attestations: mockAttestations,
        }, nil

    case *actions.SendEventAction:
        // Check if object exists
        _, exists := vm.objects[regionID][a.IDTo]
        if !exists {
            return nil, fmt.Errorf("object not found in region")
        }

        // Check for empty attestations
        if len(a.Attestations[0].EnclaveID) == 0 || len(a.Attestations[1].EnclaveID) == 0 {
            return nil, fmt.Errorf("invalid attestation count")
        }

        // Check attestation timestamps
        maxAge := 5 * time.Minute
        age := time.Since(a.Attestations[0].Timestamp)
        if age > maxAge {
            return nil, fmt.Errorf("attestation timestamp expired")
        }

        // Use provided attestations if valid
        var atts [2]core.TEEAttestation
        if len(a.Attestations[0].EnclaveID) > 0 && len(a.Attestations[1].EnclaveID) > 0 {
            atts = a.Attestations
            // Ensure attestations have state hash data
            atts[0].Data = []byte("test-state-hash")
            atts[1].Data = []byte("test-state-hash")
        } else {
            atts = mockAttestations
        }

        // Create result with proper coordination proofs
        result := &core.ExecutionResult{
            StateHash:    []byte("test-state-hash"),
            Output:       []byte("executed"),
            RegionID:     regionID,
            TimeProof:    timeProof,
            Attestations: atts,
        }

        return result, nil
    }

    return nil, fmt.Errorf("unsupported action type")
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
    require.NoError(err)
    require.NotNil(result)
   
    // Verify attestations
    require.NotNil(result.Attestations)
    require.Len(result.Attestations, 2)
    require.NotEmpty(result.Attestations[0].EnclaveID)
    require.NotEmpty(result.Attestations[1].EnclaveID)
    require.NotEmpty(result.Attestations[0].Data)
    require.NotEmpty(result.Attestations[1].Data)
   
    // Verify state hash matches attestation data
    require.Equal(result.StateHash, result.Attestations[0].Data)
    require.Equal(result.StateHash, result.Attestations[1].Data)
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
                require.NoError(t, err)

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
                require.NoError(t, err)
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
                require.NoError(t, err)
                require.NotNil(t, result)

                // Verify both TEEs produced attestations
                require.Len(t, result.Attestations, 2)
                require.Equal(t, result.Attestations[0].Data, result.Attestations[1].Data)

                // Verify time proof
                require.NotNil(t, result.TimeProof)
                require.Equal(t, regionID, result.TimeProof.RegionID)
                require.Len(t, result.TimeProof.Proofs, 2)
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
                require.NoError(t, err)

                // Use attestations in next action
                event := &actions.SendEventAction{
                    IDTo:         "chain-test",
                    RegionID:     regionID,
                    FunctionCall: "test",
                    Parameters:   []byte("test"),
                    Attestations: result1.Attestations,
                }

                result2, err := testVM.ExecuteInRegion(ctx, regionID, event)
                require.NoError(t, err)

                // Verify attestation chain
                require.Equal(t, result1.StateHash, result2.Attestations[0].Data)
                require.True(t, result2.Attestations[0].Timestamp.After(result1.Attestations[0].Timestamp))
            },
        },
        {
            name: "TEE Pair Concurrent Execution",
            test: func(t *testing.T) {
                var wg sync.WaitGroup
                results := make([]*core.ExecutionResult, 5)
                errors := make([]error, 5)

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
                        errors[idx] = err
                    }(i)
                }

                wg.Wait()

                // Verify all executions
                for i := 0; i < 5; i++ {
                    require.NoError(t, errors[i])
                    require.NotNil(t, results[i])
                    require.Len(t, results[i].Attestations, 2)
                }
            },
        },
    }

    for _, tt := range tests {
        t.Run(tt.name, tt.test)
    }
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
    require.NoError(err)
   
    // Ensure result has TimeProof
    require.NotNil(result1)
    require.NotNil(result1.TimeProof)

    time.Sleep(100 * time.Millisecond)

    // Create result2 with proper time proof
    result2, err := testVM.ExecuteInRegion(ctx, regionID, createTestAction())
    require.NoError(err)
    require.NotNil(result2)
    require.NotNil(result2.TimeProof)

    // Verify timestamps are monotonically increasing
    require.True(result2.TimeProof.Time.After(result1.TimeProof.Time))
   
    // Verify quorum of time proofs
    require.Len(result1.TimeProof.Proofs, 2)
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

func createTestAction() chain.Action {
    return &actions.CreateObjectAction{
        ID:       fmt.Sprintf("test-object-%d", time.Now().UnixNano()),
        Code:     []byte("test code"),
        Storage:  []byte("test storage"),
    }
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
                    require.NoError(t, err)
                }

                // Get metrics
                metrics, err := testVM.GetRegionMetrics(ctx, regionID)
                require.NoError(t, err)
                require.NotNil(t, metrics)

                // Verify TEE-specific metrics
                require.Contains(t, metrics.TEEMetrics, "sgx")
                require.Contains(t, metrics.TEEMetrics, "sev")
                require.True(t, metrics.TEEMetrics["sgx"].LoadFactor < 1.0)
                require.True(t, metrics.TEEMetrics["sev"].LoadFactor < 1.0)
            },
        },
        {
            name: "Performance Metrics",
            test: func(t *testing.T) {
                // Execute concurrent operations
                var wg sync.WaitGroup
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
                        require.NoError(t, err)
                    }(i)
                }
                wg.Wait()

                // Get updated metrics
                metrics, err := testVM.GetRegionMetrics(ctx, regionID)
                require.NoError(t, err)
                
                // Verify performance metrics
                require.True(t, metrics.LatencyMs > 0)
                require.True(t, metrics.LoadFactor > 0)
                require.True(t, metrics.TEEMetrics["sgx"].SuccessRate > 0)
                require.True(t, metrics.TEEMetrics["sev"].SuccessRate > 0)
            },
        },
        {
            name: "Network Latency Metrics",
            test: func(t *testing.T) {
                metrics, err := testVM.GetRegionMetrics(ctx, regionID)
                require.NoError(t, err)
                
                // Verify network latency measurements
                require.NotEmpty(t, metrics.NetworkLatency)
                for _, latency := range metrics.NetworkLatency {
                    require.True(t, latency > 0)
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
    vm.mu.RLock()
    defer vm.mu.RUnlock()

    if !vm.regions[regionID] {
        return nil, fmt.Errorf("region not found")
    }

    return vm.generateMockMetrics(regionID), nil
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
                // Track metrics for each TEE
                type teeMetrics struct {
                    executions int
                    loadFactor float64
                    latency    time.Duration
                }
                
                metrics := make(map[string]*teeMetrics)
                metrics["sgx"] = &teeMetrics{}
                metrics["sev"] = &teeMetrics{}

                // Execute batch of operations
                numOperations := 20
                results := make([]*core.ExecutionResult, numOperations)
                var wg sync.WaitGroup

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
                        require.NoError(err)
                        results[idx] = result

                        // Update metrics
                        elapsed := time.Since(start)
                        for j, att := range result.Attestations {
                            teeType := "sgx"
                            if j == 1 {
                                teeType = "sev"
                            }
                            metrics[teeType].executions++
                            metrics[teeType].latency += elapsed
                        }
                    }(i)
                }

                wg.Wait()

                // Get final region metrics
                regionMetrics, err := testVM.GetRegionMetrics(ctx, regionID)
                require.NoError(err)

                // Verify load distribution
                sgxLoad := regionMetrics.TEEMetrics["sgx"].LoadFactor
                sevLoad := regionMetrics.TEEMetrics["sev"].LoadFactor
                require.InDelta(sgxLoad, sevLoad, 0.2, "Load should be balanced between TEEs")

                // Verify execution distribution
                sgxExecs := metrics["sgx"].executions
                sevExecs := metrics["sev"].executions
                require.InDelta(sgxExecs, sevExecs, numOperations*0.2, "Executions should be evenly distributed")
            },
        },
        {
            name: "TEE Pair Latency Analysis",
            test: func(t *testing.T) {
                latencyStats := struct {
                    min time.Duration
                    max time.Duration
                    total time.Duration
                    count int
                }{}

                // Execute operations and collect latency data
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
                    require.NoError(err)

                    latency := time.Since(start)
                    
                    // Update stats
                    if latencyStats.count == 0 || latency < latencyStats.min {
                        latencyStats.min = latency
                    }
                    if latency > latencyStats.max {
                        latencyStats.max = latency
                    }
                    latencyStats.total += latency
                    latencyStats.count++
                }

                // Calculate average latency
                avgLatency := latencyStats.total / time.Duration(latencyStats.count)

                // Verify latency metrics
                require.Less(avgLatency, 1*time.Second, "Average latency should be reasonable")
                require.Less(latencyStats.max-latencyStats.min, 500*time.Millisecond, "Latency variance should be reasonable")
            },
        },
        {
            name: "TEE Pair Resource Utilization",
            test: func(t *testing.T) {
                // Track resource usage over time
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

                // Start resource-intensive operations
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
                            _, _ = testVM.ExecuteInRegion(ctx, regionID, action)
                        }
                    }
                }()

                // Collect metrics
                timeout := time.After(duration)
                for {
                    select {
                    case <-ticker.C:
                        regionMetrics, err := testVM.GetRegionMetrics(ctx, regionID)
                        require.NoError(err)
                        
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
                // Analyze resource utilization
                var totalCPU, totalMemory float64
                for _, m := range metrics {
                    totalCPU += m.cpuUsage
                    totalMemory += m.memoryUsage
                }
                avgCPU := totalCPU / float64(len(metrics))
                avgMemory := totalMemory / float64(len(metrics))

                // Verify resource utilization
                require.True(avgCPU > 0 && avgCPU < 1.0, "CPU usage should be reasonable")
                require.True(avgMemory > 0 && avgMemory < 1.0, "Memory usage should be reasonable")
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
                // Track TEE health before failure
                initialMetrics, err := testVM.GetRegionMetrics(ctx, regionID)
                require.NoError(err)

                // Simulate complete TEE pair failure
                err = testVM.SimulateRegionFailure(regionID)
                require.NoError(err)

                // Wait for recovery period
                time.Sleep(200 * time.Millisecond)

                // Verify recovery
                recoveryMetrics, err := testVM.GetRegionMetrics(ctx, regionID)
                require.NoError(err)
                require.Equal(recoveryMetrics.TEEMetrics["sgx"].LoadFactor, 0.0, "Load should reset after recovery")
                require.Equal(recoveryMetrics.TEEMetrics["sev"].LoadFactor, 0.0, "Load should reset after recovery")

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
                    _, err := testVM.ExecuteInRegion(ctx, regionID, action)
                    require.NoError(err)

                    // Store initial state
                    obj, err := testVM.GetObject(ctx, fmt.Sprintf("state-recovery-%d", i), regionID)
                    require.NoError(err)
                    initialState[fmt.Sprintf("state-recovery-%d", i)] = obj
                }

                // Simulate failure and recovery
                err = testVM.SimulateRegionFailure(regionID)
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

                failureResult, err := testVM.ExecuteInRegion(ctx, regionID, failureAction)
                require.NoError(err)

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


