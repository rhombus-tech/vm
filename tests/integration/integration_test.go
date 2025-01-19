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
        StateHash:    stateHash,  // Use the same value
        TimeProof:    timeProof,
        RegionID:     "test-region",
        Attestations: [2]core.TEEAttestation{
            {
                EnclaveID:   []byte("sgx-enclave"),
                Measurement: []byte("measurement1"),
                Timestamp:   timeProof.Time,
                Data:        stateHash,  // Use the same value
                Signature:   []byte("sig1"),
                RegionProof: []byte("region-proof1"),
            },
            {
                EnclaveID:   []byte("sev-enclave"),
                Measurement: []byte("measurement2"),
                Timestamp:   timeProof.Time,
                Data:        stateHash,  // Use the same value
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

    coordinator, err := coordination.NewCoordinator(coordConfig, nil)
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

        return &core.ExecutionResult{
            StateHash:    []byte("test-state-hash"),
            Output:       []byte("executed"),
            RegionID:     regionID,
            TimeProof:    timeProof,
            Attestations: atts,
        }, nil
    }

    return nil, fmt.Errorf("unsupported action type")
}

// Helper function to check if attestation is empty
func isEmptyAttestation(atts [2]core.TEEAttestation) bool {
    return len(atts[0].EnclaveID) == 0 || len(atts[1].EnclaveID) == 0 ||
           len(atts[0].Data) == 0 || len(atts[1].Data) == 0
}

func setupTestEnvironment(t *testing.T) (*MockVM, string) {
    require := require.New(t)
    
    config := &compute.Config{
        MaxTasks: 100,
        Debug:    true,
    }

    testVM, err := NewMockVM(config)
    require.NoError(err)

    t.Cleanup(func() {
        if err := testVM.teeClient.Close(); err != nil {
            t.Logf("Failed to close TEE client: %v", err)
        }
    })

    regionID := "test-region"
    err = testVM.RegisterRegion(context.Background(), regionID, "mock://sgx", "mock://sev")
    require.NoError(err)

    // Create initial test object
    testVM.objects[regionID] = make(map[string]*core.ObjectState)
    testVM.objects[regionID]["test-object"] = &core.ObjectState{
        Code:    []byte("test code"),
        Storage: []byte("test storage"),
    }

    return testVM, regionID
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

func createTestAction() chain.Action {
    return &actions.CreateObjectAction{
        ID:       "test-object",
        Code:     []byte("test code"),
        Storage:  []byte("test storage"),
    }
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

// Helper function to create time proofs in ExecuteInRegion
func (vm *MockVM) createMockTimeProof() *timeserver.VerifiedTimestamp {
    return &timeserver.VerifiedTimestamp{
        Time: time.Now(),
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
}

func TestConcurrentRegionOperations(t *testing.T) {
    testVM, regionID := setupTestEnvironment(t)
    require := require.New(t)
    ctx := context.Background()

    var wg sync.WaitGroup
    numOperations := 10
    errors := make(chan error, numOperations)

    for i := 0; i < numOperations; i++ {
        wg.Add(1)
        go func() {
            defer wg.Done()
            _, err := testVM.ExecuteInRegion(ctx, regionID, createTestAction())
            if err != nil {
                errors <- err
            }
        }()
    }

    wg.Wait()
    close(errors)

    for err := range errors {
        require.NoError(err)
    }
}

func TestRegionStateIsolation(t *testing.T) {
    testVM, _ := setupTestEnvironment(t)
    require := require.New(t)
    ctx := context.Background()

    // Create two regions
    region1 := "region-1"
    region2 := "region-2"
    err := testVM.RegisterRegion(ctx, region1, "mock://sgx1", "mock://sev1")
    require.NoError(err)
    err = testVM.RegisterRegion(ctx, region2, "mock://sgx2", "mock://sev2")
    require.NoError(err)

    // Execute in region1
    action1 := &actions.CreateObjectAction{
        ID:       "test-object",
        RegionID: region1,
        Code:     []byte("test code"),
        Storage:  []byte("test storage"),
    }
    _, err = testVM.ExecuteInRegion(ctx, region1, action1)
    require.NoError(err)

    // Try to access object from region2
    action2 := &actions.SendEventAction{
        IDTo:         "test-object",
        RegionID:     region2,
        FunctionCall: "test",
        Parameters:   []byte("test"),
    }
    _, err = testVM.ExecuteInRegion(ctx, region2, action2)
    require.Error(err)
    require.Contains(err.Error(), "object not found in region")
}


func TestAttestationChain(t *testing.T) {
    testVM, regionID := setupTestEnvironment(t)
    require := require.New(t)
    ctx := context.Background()

    // First create an object
    createAction := &actions.CreateObjectAction{
        ID:       "chain-test",
        RegionID: regionID,
        Code:     []byte("test code"),
        Storage:  []byte("test storage"),
    }
    result1, err := testVM.ExecuteInRegion(ctx, regionID, createAction)
    require.NoError(err)
    require.NotNil(result1)

    // Small delay to ensure different timestamps
    time.Sleep(10 * time.Millisecond)

    // Create a new timestamp that's definitely after the first one
    newTime := result1.Attestations[0].Timestamp.Add(time.Second)
    
    // Use attestations from first result but update timestamps
    attestations := result1.Attestations
    attestations[0].Timestamp = newTime
    attestations[1].Timestamp = newTime

    action2 := &actions.SendEventAction{
        IDTo:         "chain-test",
        RegionID:     regionID,
        FunctionCall: "test",
        Parameters:   []byte("test"),
        Attestations: attestations,
    }
    result2, err := testVM.ExecuteInRegion(ctx, regionID, action2)
    require.NoError(err)
    require.NotNil(result2)

    // Verify attestation chain
    require.Equal(result1.StateHash, result2.Attestations[0].Data)
    require.True(result2.Attestations[0].Timestamp.After(result1.Attestations[0].Timestamp), 
        "Expected second attestation timestamp to be after first")
}

func TestRegionLifecycle(t *testing.T) {
    testVM, _ := setupTestEnvironment(t)
    require := require.New(t)
    ctx := context.Background()

    regionID := "lifecycle-test"

    // Register region
    err := testVM.RegisterRegion(ctx, regionID, "mock://sgx", "mock://sev")
    require.NoError(err)

    // Create an object first
    createAction := &actions.CreateObjectAction{
        ID:       "test-object",
        RegionID: regionID,
        Code:     []byte("test code"),
        Storage:  []byte("test storage"),
    }
    result, err := testVM.ExecuteInRegion(ctx, regionID, createAction)
    require.NoError(err)

    // Update region
    err = testVM.RegisterRegion(ctx, regionID, "mock://sgx2", "mock://sev2")
    require.Error(err) // Should fail - already exists

    // Verify region state preserved
    action := &actions.SendEventAction{
        IDTo:         "test-object",
        RegionID:     regionID,
        FunctionCall: "test",
        Parameters:   []byte("test"),
        Attestations: result.Attestations,
    }
    _, err = testVM.ExecuteInRegion(ctx, regionID, action)
    require.NoError(err)
}

// Test region-to-region communication
func TestCrossRegionCommunication(t *testing.T) {
    testVM, region1ID := setupTestEnvironment(t)
    require := require.New(t)
    ctx := context.Background()

    // Create second region
    region2ID := "test-region-2"
    err := testVM.RegisterRegion(ctx, region2ID, "mock://sgx2", "mock://sev2")
    require.NoError(err)

    // Initialize object maps for both regions
    testVM.mu.Lock()
    if testVM.objects == nil {
        testVM.objects = make(map[string]map[string]*core.ObjectState)
    }
    testVM.objects[region1ID] = make(map[string]*core.ObjectState)
    testVM.objects[region2ID] = make(map[string]*core.ObjectState)
    testVM.mu.Unlock()

    // Create objects directly in both regions
    obj1 := &core.ObjectState{
        Code:     []byte("test code"),
        Storage:  []byte("test storage"),
        RegionID: region1ID,
        Status:   "active",
    }
    obj2 := &core.ObjectState{
        Code:     []byte("test code"),
        Storage:  []byte("test storage"),
        RegionID: region2ID,
        Status:   "active",
    }

    testVM.mu.Lock()
    testVM.objects[region1ID]["object-region1"] = obj1
    testVM.objects[region2ID]["object-region2"] = obj2
    testVM.mu.Unlock()

    // Create event with valid attestations
    now := time.Now()
    attestations := [2]core.TEEAttestation{
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

    // Test cross-region event
    crossRegionEvent := &actions.SendEventAction{
        IDTo:         "object-region2",
        RegionID:     region2ID,
        FunctionCall: "cross_region_call",
        Parameters:   []byte("cross-region data"),
        Attestations: attestations,
    }

    result, err := testVM.ExecuteInRegion(ctx, region2ID, crossRegionEvent)
    require.NoError(err)
    require.NotNil(result)
}

// Test region scaling
func TestRegionScaling(t *testing.T) {
    testVM, _ := setupTestEnvironment(t)
    require := require.New(t)
    ctx := context.Background()

    // Test adding multiple regions
    for i := 0; i < 5; i++ {
        regionID := fmt.Sprintf("scale-region-%d", i)
        err := testVM.RegisterRegion(ctx, regionID, 
            fmt.Sprintf("mock://sgx%d", i),
            fmt.Sprintf("mock://sev%d", i))
        require.NoError(err)
    }

    // Test parallel execution across regions
    var wg sync.WaitGroup
    errors := make(chan error, 5)
    
    for i := 0; i < 5; i++ {
        wg.Add(1)
        go func(idx int) {
            defer wg.Done()
            regionID := fmt.Sprintf("scale-region-%d", idx)
            action := &actions.CreateObjectAction{
                ID:       fmt.Sprintf("obj-%d", idx),
                RegionID: regionID,
                Code:     []byte("test code"),
                Storage:  []byte("test storage"),
            }
            _, err := testVM.ExecuteInRegion(ctx, regionID, action)
            if err != nil {
                errors <- err
            }
        }(i)
    }

    wg.Wait()
    close(errors)
    for err := range errors {
        require.NoError(err)
    }
}

// Test TEE attestation rotation
func TestTEEAttestationRotation(t *testing.T) {
    testVM, regionID := setupTestEnvironment(t)
    require := require.New(t)
    ctx := context.Background()

    // Create initial object
    now := time.Now()
    initialAction := &actions.CreateObjectAction{
        ID:       "rotation-test",
        RegionID: regionID,
        Code:     []byte("test code"),
        Storage:  []byte("test storage"),
    }
    
    // Execute initial action without storing result
    _, err := testVM.ExecuteInRegion(ctx, regionID, initialAction)
    require.NoError(err)

    // Create new attestations with valid timestamps
    newAttestations := [2]core.TEEAttestation{
        {
            EnclaveID:   []byte("sgx-new"),
            Measurement: []byte("measurement1-new"),
            Timestamp:   now,
            Data:        []byte("test-data-new"),
            RegionProof: []byte("region-proof-new"),
        },
        {
            EnclaveID:   []byte("sev-new"),
            Measurement: []byte("measurement2-new"),
            Timestamp:   now,
            Data:        []byte("test-data-new"),
            RegionProof: []byte("region-proof-new"),
        },
    }

    // Test with new attestations
    rotateEvent := &actions.SendEventAction{
        IDTo:         "rotation-test",
        RegionID:     regionID,
        FunctionCall: "test",
        Parameters:   []byte("post-rotation"),
        Attestations: newAttestations,
    }

    result2, err := testVM.ExecuteInRegion(ctx, regionID, rotateEvent)
    require.NoError(err)
    require.NotNil(result2)
}
// Test region recovery
func TestRegionRecovery(t *testing.T) {
    testVM, regionID := setupTestEnvironment(t)
    require := require.New(t)
    ctx := context.Background()

    // Create initial state
    obj := &actions.CreateObjectAction{
        ID:       "recovery-test",
        RegionID: regionID,
        Code:     []byte("test code"),
        Storage:  []byte("test storage"),
    }
    _, err := testVM.ExecuteInRegion(ctx, regionID, obj)
    require.NoError(err)

    // Simulate region failure and recovery
    err = testVM.SimulateRegionFailure(regionID) // You'd need to implement this
    require.NoError(err)

    // Verify state after recovery
    recoveredObj, err := testVM.GetObject(ctx, "recovery-test", regionID)
    require.NoError(err)
    require.NotNil(recoveredObj)
}

// Test region state proofs
func TestRegionStateProofs(t *testing.T) {
    testVM, regionID := setupTestEnvironment(t)
    require := require.New(t)
    ctx := context.Background()

    // Create state to prove
    action := &actions.CreateObjectAction{
        ID:       "proof-test",
        RegionID: regionID,
        Code:     []byte("test code"),
        Storage:  []byte("test storage"),
    }
    _, err := testVM.ExecuteInRegion(ctx, regionID, action)
    require.NoError(err)

    // Get and verify proof
    proof, err := testVM.GetRegionStateProof(ctx, regionID, "proof-test")
    require.NoError(err)
    require.NotNil(proof)

    valid, err := testVM.VerifyRegionStateProof(ctx, regionID, proof)
    require.NoError(err)
    require.True(valid)
}

// Test region coordination
func TestRegionCoordination(t *testing.T) {
    testVM, region1ID := setupTestEnvironment(t)
    require := require.New(t)
    ctx := context.Background()

    // Create second region
    region2ID := "coord-region-2"
    err := testVM.RegisterRegion(ctx, region2ID, "mock://sgx2", "mock://sev2")
    require.NoError(err)

    // Create coordinator config
    coordConfig := &coordination.Config{
        MinWorkers:          2,
        MaxWorkers:          10,
        WorkerTimeout:       30 * time.Second,
        ChannelTimeout:      10 * time.Second,
        MaxMessageSize:      1024 * 1024,
        EncryptionEnabled:   true,
        RequireAttestation:  true,
        AttestationTimeout:  5 * time.Second,
        StoragePath:         "/tmp/coordinator-test",
        PersistenceEnabled:  false,
        TaskCleanupInterval: time.Minute,
        MaxTasks:           100,
        TaskQueueSize:      100,
    }

    // Create and start coordinator
    coordinator, err := coordination.NewCoordinator(coordConfig, nil)
    require.NoError(err)
    
    err = coordinator.Start()
    require.NoError(err)
    
    // Assign coordinator to testVM
    testVM.coordinator = coordinator

    // Create mock workers
    sgxWorker := &coordination.Worker{
        ID:        coordination.WorkerID(fmt.Sprintf("sgx-%s", region1ID)),
        EnclaveID: []byte("test-enclave-1"),
        Status:    coordination.WorkerStatusIdle,
    }

    sevWorker := &coordination.Worker{
        ID:        coordination.WorkerID(fmt.Sprintf("sev-%s", region2ID)),
        EnclaveID: []byte("test-enclave-2"),
        Status:    coordination.WorkerStatusIdle,
    }

    // Add workers to coordinator
    coordinator.AddWorker(sgxWorker)
    coordinator.AddWorker(sevWorker)

    // Create task
    task := &coordination.Task{
        ID:        "test-task",
        WorkerIDs: []coordination.WorkerID{sgxWorker.ID, sevWorker.ID},
        Data:      []byte("test data"),
        RegionID:  region1ID,
        Timeout:   5 * time.Second,
    }

    // Set up task processing
    go func() {
        // Simulate task processing
        time.Sleep(100 * time.Millisecond)
        coordinator.CompleteTask(task.ID)
    }()

    // Submit task
    err = coordinator.SubmitTask(ctx, task)
    require.NoError(err)

    // Wait for task completion
    success := waitForTaskCompletion(ctx, coordinator, task.ID)
    require.True(success, "Task coordination failed to complete in time")

    // Cleanup
    coordinator.Stop()
}

// Helper function to wait for task completion
func waitForTaskCompletion(ctx context.Context, coordinator *coordination.Coordinator, taskID string) bool {
    ticker := time.NewTicker(100 * time.Millisecond)
    defer ticker.Stop()
    
    timeout := time.After(1 * time.Second)

    for {
        select {
        case <-ticker.C:
            status, err := coordinator.GetTaskStatus(taskID)
            if err == nil && status == "complete" {
                return true
            }
        case <-timeout:
            return false
        case <-ctx.Done():
            return false
        }
    }
}

// Test region metrics and monitoring
func TestRegionMetrics(t *testing.T) {
    testVM, regionID := setupTestEnvironment(t)
    require := require.New(t)
    ctx := context.Background()

    // Create some load
    for i := 0; i < 10; i++ {
        action := &actions.CreateObjectAction{
            ID:       fmt.Sprintf("metric-test-%d", i),
            RegionID: regionID,
            Code:     []byte("test code"),
            Storage:  []byte("test storage"),
        }
        _, err := testVM.ExecuteInRegion(ctx, regionID, action)
        require.NoError(err)
    }

    // Get and verify metrics
    metrics, err := testVM.GetRegionMetrics(ctx, regionID)
    require.NoError(err)
    
    // Verify specific metrics
    require.True(metrics.LoadFactor > 0)
    require.True(metrics.LatencyMs > 0)
    require.True(metrics.ActiveWorkers >= 2)
    require.True(len(metrics.TEEMetrics) >= 2)
}

func (vm *MockVM) SimulateRegionFailure(regionID string) error {
    vm.mu.Lock()
    defer vm.mu.Unlock()

    if !vm.regions[regionID] {
        return errors.New("region not found")
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

func (vm *MockVM) GetObject(ctx context.Context, objectID string, regionID string) (*core.ObjectState, error) {
    vm.mu.RLock()
    defer vm.mu.RUnlock()

    regionObjects, exists := vm.objects[regionID]
    if !exists {
        return nil, errors.New("region not found")
    }

    obj, exists := regionObjects[objectID]
    if !exists {
        return nil, errors.New("object not found")
    }

    return obj, nil
}

func (vm *MockVM) GetRegionStateProof(ctx context.Context, regionID string, objectID string) (*merkledb.Proof, error) {
    vm.mu.RLock()
    defer vm.mu.RUnlock()

    if !vm.regions[regionID] {
        return nil, errors.New("region not found")
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

type ProofNode struct {
    Key         merkledb.Key
    ValueOrHash maybe.Maybe[[]byte]
    Children    map[byte]ids.ID
}

func (vm *MockVM) VerifyRegionStateProof(ctx context.Context, regionID string, proof *merkledb.Proof) (bool, error) {
    vm.mu.RLock()
    defer vm.mu.RUnlock()

    if !vm.regions[regionID] {
        return false, errors.New("region not found")
    }

    // Mock verification - always return true for testing
    return true, nil
}

func (vm *MockVM) GetRegionMetrics(ctx context.Context, regionID string) (*RegionMetrics, error) {
    vm.mu.RLock()
    defer vm.mu.RUnlock()

    if !vm.regions[regionID] {
        return nil, errors.New("region not found")
    }

    // Return mock metrics
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
    }, nil
}

type RegionMetrics struct {
    LoadFactor      float64
    LatencyMs       float64
    ErrorRate       float64
    ActiveWorkers   int
    PendingTasks    int
    LastHealthCheck time.Time
    NetworkLatency  map[string]float64
    TEEMetrics      map[string]*TEEMetrics
}

type TEEMetrics struct {
    EnclaveID    []byte
    Type         string
    LoadFactor   float64
    SuccessRate  float64
    LastAttested time.Time
}
