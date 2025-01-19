// tests/integration/integration_test.go
package integration_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/ava-labs/hypersdk/chain"
	ginkgo "github.com/onsi/ginkgo/v2"
	"github.com/stretchr/testify/require"

	"github.com/rhombus-tech/vm/actions"
	"github.com/rhombus-tech/vm/compute"
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
}

func NewMockVM(config *compute.Config) (*MockVM, error) {
    mockTee := newMockTEE()
    stateVerifier := verifier.New(nil)
    
    teeClient, err := tee.NewClient("mock://sgx", "mock://sev", stateVerifier)
    if err != nil {
        return nil, err
    }

    return &MockVM{
        config:    config,
        teeClient: teeClient,
        mockTEE:   mockTee,
        regions:   make(map[string]bool),
        objects:   make(map[string]map[string]*core.ObjectState),
    }, nil
}


func (vm *MockVM) RegisterRegion(ctx context.Context, regionID, sgxEndpoint, sevEndpoint string) error {
    vm.mu.Lock()
    defer vm.mu.Unlock()

    if vm.regions[regionID] {
        return errors.New("region already registered")
    }

    if sgxEndpoint == "" || sevEndpoint == "" {
        return errors.New("invalid TEE endpoints")
    }

    if err := vm.teeClient.AddRegion(regionID, sgxEndpoint, sevEndpoint); err != nil {
        return err
    }

    vm.regions[regionID] = true
    return nil
}

func (vm *MockVM) ExecuteInRegion(ctx context.Context, regionID string, action chain.Action) (*core.ExecutionResult, error) {
    vm.mu.RLock()
    if !vm.regions[regionID] {
        vm.mu.RUnlock()
        return nil, errors.New("region not found")
    }
    vm.mu.RUnlock()

    switch a := action.(type) {
    case *actions.CreateObjectAction:
        vm.mu.Lock()
        defer vm.mu.Unlock()
        
        if _, exists := vm.objects[regionID]; !exists {
            vm.objects[regionID] = make(map[string]*core.ObjectState)
        }
        vm.objects[regionID][a.ID] = &core.ObjectState{
            Code:    a.Code,
            Storage: a.Storage,
        }

    case *actions.SendEventAction:
        // Check attestations first
        if len(a.Attestations) != 2 {  // Changed from == 0 to != 2
            return nil, errors.New("invalid attestation count")
        }

        // Check object existence
        vm.mu.RLock()
        regionObjects, exists := vm.objects[regionID]
        if !exists || len(regionObjects) == 0 {
            vm.mu.RUnlock()
            return nil, errors.New("object not found in region")
        }
        
        _, exists = regionObjects[a.IDTo]
        vm.mu.RUnlock()
        if !exists {
            return nil, errors.New("object not found in region")
        }

        // Check attestation timestamps
        for _, att := range a.Attestations {
            if att.Timestamp.IsZero() {
                return nil, errors.New("invalid attestation timestamp")
            }
            if time.Since(att.Timestamp) > 24*time.Hour {
                return nil, errors.New("attestation timestamp expired")
            }
        }
    }

    return vm.mockTEE.Execute(ctx, []byte("test"))
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

    // Test object creation
    createAction := &actions.CreateObjectAction{
        ID:       "test-object",
        Code:     []byte("test code"),
        Storage:  []byte("test storage"),
        RegionID: regionID,
    }
    
    result, err := testVM.ExecuteInRegion(ctx, regionID, createAction)
    require.NoError(err)
    require.NotNil(result)
    require.Equal([]byte("test-state-hash"), result.StateHash)

    // Verify attestations
    require.Len(result.Attestations, 2)
    for _, att := range result.Attestations {  // Removed unused index variable 'i'
        require.NotEmpty(att.EnclaveID)
        require.NotEmpty(att.Measurement)
        require.False(att.Timestamp.IsZero())
        require.Equal([]byte("test-state-hash"), att.Data)
        require.NotEmpty(att.Signature)
        require.NotEmpty(att.RegionProof)
    }

    // Test event execution
    eventAction := &actions.SendEventAction{
        RegionID:     regionID,
        IDTo:         "test-object",
        FunctionCall: "test",
        Parameters:   []byte("test params"),
        Attestations: result.Attestations,
    }
    
    result, err = testVM.ExecuteInRegion(ctx, regionID, eventAction)
    require.NoError(err)
    require.NotNil(result)
    require.Equal([]byte("test-state-hash"), result.StateHash)
    require.Equal([]byte("test output"), result.Output)
}

func TestRegionManagement(t *testing.T) {
    testVM, _ := setupTestEnvironment(t)
    require := require.New(t)

    ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
    defer cancel()

    // Test registering new region
    newRegionID := "new-region"
    err := testVM.RegisterRegion(ctx, newRegionID, "mock://sgx2", "mock://sev2")
    require.NoError(err)

    // Test duplicate region
    err = testVM.RegisterRegion(ctx, newRegionID, "mock://sgx2", "mock://sev2")
    require.Error(err)
    require.Contains(err.Error(), "region already registered")

    // Test invalid endpoints
    err = testVM.RegisterRegion(ctx, "bad-region", "", "")
    require.Error(err)
    require.Contains(err.Error(), "invalid TEE endpoints")

    // Test execution in non-existent region
    _, err = testVM.ExecuteInRegion(ctx, "missing-region", &actions.CreateObjectAction{})
    require.Error(err)
    require.Contains(err.Error(), "region not found")
}

func createTestAction() chain.Action {
    return &actions.CreateObjectAction{
        ID:       "test-object",
        Code:     []byte("test code"),
        Storage:  []byte("test storage"),
    }
}

// Add new tests
func TestTimeProofVerification(t *testing.T) {
    testVM, regionID := setupTestEnvironment(t)
    require := require.New(t)
    ctx := context.Background()

    // Test timestamp ordering
    result1, err := testVM.ExecuteInRegion(ctx, regionID, createTestAction())
    require.NoError(err)
    time.Sleep(100 * time.Millisecond)
    result2, err := testVM.ExecuteInRegion(ctx, regionID, createTestAction())
    require.NoError(err)

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

    // Use attestations from first result
    action2 := &actions.SendEventAction{
        IDTo:         "chain-test",
        RegionID:     regionID,
        FunctionCall: "test",
        Parameters:   []byte("test"),
        Attestations: result1.Attestations,
    }
    result2, err := testVM.ExecuteInRegion(ctx, regionID, action2)
    require.NoError(err)
    require.Equal(result1.StateHash, result2.Attestations[0].Data)
}
func TestErrorHandling(t *testing.T) {
    testVM, regionID := setupTestEnvironment(t)
    require := require.New(t)
    ctx := context.Background()

    // Create an object first
    createAction := &actions.CreateObjectAction{
        ID:       "test-object",
        RegionID: regionID,
        Code:     []byte("test code"),
        Storage:  []byte("test storage"),
    }
    _, err := testVM.ExecuteInRegion(ctx, regionID, createAction)
    require.NoError(err)

    // Test case 1: Empty attestations array
    emptyAction := &actions.SendEventAction{
        IDTo:         "test-object",
        RegionID:     regionID,
        FunctionCall: "test",
        Parameters:   []byte("test"),
        Attestations: [2]core.TEEAttestation{}, // Empty attestations
    }
    _, err = testVM.ExecuteInRegion(ctx, regionID, emptyAction)
    require.Error(err)
    require.Contains(err.Error(), "invalid attestation count")

    // Test case 2: Invalid timestamps
    oldTime := time.Now().Add(-24 * time.Hour)
    timeAction := &actions.SendEventAction{
        IDTo:         "test-object",
        RegionID:     regionID,
        FunctionCall: "test",
        Parameters:   []byte("test"),
        Attestations: [2]core.TEEAttestation{
            {
                EnclaveID:   []byte("test"),
                Measurement: []byte("test"),
                Timestamp:   oldTime,
            },
            {
                EnclaveID:   []byte("test"),
                Measurement: []byte("test"),
                Timestamp:   oldTime,
            },
        },
    }
    _, err = testVM.ExecuteInRegion(ctx, regionID, timeAction)
    require.Error(err)
    require.Contains(err.Error(), "attestation timestamp expired")

    // Test case 3: Non-existent object
    nonExistentAction := &actions.SendEventAction{
        IDTo:         "non-existent",
        RegionID:     regionID,
        FunctionCall: "test",
        Parameters:   []byte("test"),
        Attestations: [2]core.TEEAttestation{
            {
                EnclaveID:   []byte("test"),
                Measurement: []byte("test"),
                Timestamp:   time.Now(),
            },
            {
                EnclaveID:   []byte("test"),
                Measurement: []byte("test"),
                Timestamp:   time.Now(),
            },
        },
    }
    _, err = testVM.ExecuteInRegion(ctx, regionID, nonExistentAction)
    require.Error(err)
    require.Contains(err.Error(), "object not found in region")
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
