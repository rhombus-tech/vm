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

    result := &core.ExecutionResult{
        Output:       []byte("test output"),
        StateHash:    []byte("test hash"),
        TimeProof:    timeProof,
        RegionID:     "test-region",
        Attestations: [2]core.TEEAttestation{
            {
                EnclaveID:   []byte("sgx-enclave"),
                Measurement: []byte("measurement1"),
                Timestamp:   timeProof.Time,
                Data:        []byte("data1"),
                Signature:   []byte("sig1"),
                RegionProof: []byte("region-proof1"),
            },
            {
                EnclaveID:   []byte("sev-enclave"),
                Measurement: []byte("measurement2"),
                Timestamp:   timeProof.Time,
                Data:        []byte("data2"),
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
    for i, att := range result.Attestations {
        require.NotEmpty(att.EnclaveID)
        require.NotEmpty(att.Measurement)
        require.False(att.Timestamp.IsZero())
        require.Equal([]byte("test-state-hash"), att.Data)
        require.NotEmpty(att.Signature)
        require.Equal([]byte("region-1"), att.RegionProof)
        
        if i == 0 {
            require.Equal([]byte("sgx-test"), att.EnclaveID)
        } else {
            require.Equal([]byte("sev-test"), att.EnclaveID)
        }
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
    require.Equal([]byte("test result"), result.Output)
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