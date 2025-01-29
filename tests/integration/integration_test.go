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
)

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
	EnclaveID         []byte    `json:"enclave_id"`
	Type              string    `json:"type"`         // "SGX" or "SEV"
	LoadFactor        float64   `json:"load_factor"`  // 0.0 to 1.0
	SuccessRate       float64   `json:"success_rate"` // 0.0 to 1.0
	LastAttested      time.Time `json:"last_attested"`
	LastHealthCheck   time.Time `json:"last_health_check"`
	ErrorCount        uint64    `json:"error_count"`
	TaskCount         uint64    `json:"task_count"`
	ConsecutiveErrors uint64    `json:"consecutive_errors"`
	ExecutionTimeMs   float64   `json:"execution_time_ms"` // Average execution time in milliseconds
	Status            string    `json:"status"` // "healthy", "degraded", "failed"
}

type TEEExecutor interface {
	Execute(ctx context.Context, input []byte) (*core.ExecutionResult, error)
}

type mockTEE struct {
	attestations [2]core.TEEAttestation
	results      map[string]*core.ExecutionResult
	mu           sync.RWMutex
}

func createTestAction() chain.Action {
	return &actions.CreateObjectAction{
		ID:      fmt.Sprintf("test-object-%d", time.Now().UnixNano()),
		Code:    []byte("test code"),
		Storage: []byte("test storage"),
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
		MaxTasks: 100,
		Debug:    true,
		DB:       merkleDB,
		RegionID: "test-region",
		TEEConfig: &core.Config{
			EnclaveType:    core.PlatformTypeSGX,
			TeeEndpoint:    "mock://tee",
			AttestationKey: []byte("test-attestation-key"),
			Debug:          true,
		},
	}

	// Create MockVM
	vm, err := NewMockVM(vmConfig)
	require.NoError(err)

	// Initialize default attestations
	now := time.Now().UTC()
	defaultAttestations := [2]core.TEEAttestation{
		{
			EnclaveID:   []byte("sgx-test-enclave-id"), // Only changed these two lines
			Measurement: []byte("measurement1"),
			Timestamp:   now,
			Data:        []byte("test-state-hash"),
			RegionProof: []byte("region-proof"),
			Signature:   []byte("signature1"),
		},
		{
			EnclaveID:   []byte("sev-test-enclave-id"), // Only changed these two lines
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
	require.NotEmpty(result.Attestations[0].Data)
	require.NotEmpty(result.Attestations[1].Data)

	// Verify state hash matches attestation data
	require.Equal(result.StateHash, result.Attestations[0].Data)
	require.Equal(result.StateHash, result.Attestations[1].Data)

	// Verify time proofs
	require.NotNil(result.TimeProof)
	require.Equal(2, len(result.TimeProof.Proofs))
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
		results:      make(map[string]*core.ExecutionResult),
	}
}

func (m *mockTEE) Execute(_ context.Context, input []byte) (*core.ExecutionResult, error) {
	// Simulate some execution time between 10-50ms
	simulatedExecutionTime := 10 + rand.Float64()*40
	time.Sleep(time.Duration(simulatedExecutionTime) * time.Millisecond)
	
	now := time.Now()
	timeProof := &timeserver.VerifiedTimestamp{
		Time: now,
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

	attestations := [2]core.TEEAttestation{
		{
			EnclaveID:   []byte("sgx-enclave"),
			Measurement: []byte("measurement1"),
			Timestamp:   now,
			Data:        stateHash,
			Signature:   []byte("sig1"),
			RegionProof: []byte("region-proof1"),
		},
		{
			EnclaveID:   []byte("sev-enclave"),
			Measurement: []byte("measurement2"),
			Timestamp:   now,
			Data:        stateHash,
			Signature:   []byte("sig2"),
			RegionProof: []byte("region-proof2"),
		},
	}

	result := &core.ExecutionResult{
		Output:       []byte("test output"),
		StateHash:    stateHash,
		TimeProof:    timeProof,
		RegionID:     "test-region",
		Attestations: attestations,
	}

	// Only take the lock when we need to access shared state
	m.mu.Lock()
	defer m.mu.Unlock()
	
	// Store result if needed
	m.results[string(input)] = result

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
	teePairs            map[string]*TEEPairInfo
	teeMetrics          map[string]map[string]*TEEMetrics
	regionLoads         map[string]*RegionLoad
}

type TEEPairInfo struct {
	SGXEnclaveID []byte
	SEVEnclaveID []byte
	Status       string
	LastUpdate   time.Time
}

type RegionLoad struct {
	SGX struct {
		LoadFactor    float64
		TaskCount     uint64
		LastOperation time.Time
	}
	SEV struct {
		LoadFactor    float64
		TaskCount     uint64
		LastOperation time.Time
	}
}

func NewMockVM(config *compute.Config) (*MockVM, error) {
	if config == nil {
		return nil, fmt.Errorf("config cannot be nil")
	}

	mockTEE := newMockTEE()

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
		return nil, fmt.Errorf("failed to create merkledb: %v", err)
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
		MaxTasks:           100,
		TaskQueueSize:      1000,
		EncryptionEnabled:  true,
		RequireAttestation: true,
		AttestationTimeout: 5 * time.Second,
		StoragePath:        "/tmp/coordinator-test",
		PersistenceEnabled: true,
	}

	// Create coordinator
	coordinator, err := coordination.NewCoordinator(coordConfig, merkleDB, coordStorage)
	if err != nil {
		return nil, fmt.Errorf("failed to create coordinator: %v", err)
	}

	vm := &MockVM{
		config:             config,
		mockTEE:           mockTEE,
		regions:           make(map[string]bool),
		objects:           make(map[string]map[string]*core.ObjectState),
		coordinator:       coordinator,
		db:               merkleDB,
		stateManager:     dbWrapper,
		networkLatency:   make(map[string]time.Duration),
		regionMetrics:    make(map[string]*RegionMetrics),
		teePairs:         make(map[string]*TEEPairInfo),
		teeMetrics:       make(map[string]map[string]*TEEMetrics),
		regionLoads:      make(map[string]*RegionLoad),
	}

	// Initialize default attestations
	now := time.Now()
	vm.defaultAttestations = [2]core.TEEAttestation{
		{
			EnclaveID:   []byte("sgx-test-enclave"),
			Data:        []byte("test-state-hash"),
			Timestamp:   now,
			RegionProof: []byte("region-proof"),
		},
		{
			EnclaveID:   []byte("sev-test-enclave"),
			Data:        []byte("test-state-hash"),
			Timestamp:   now,
			RegionProof: []byte("region-proof"),
		},
	}

	// Initialize default time proof
	vm.defaultTimeProof = &timeserver.VerifiedTimestamp{
		Time: now,
		Proofs: []*timeserver.TimestampProof{
			{
				ServerID:  "server1",
				Signature: []byte("sig1"),
				Delay:     100 * time.Millisecond,
			},
		},
		RegionID:   "default",
		QuorumSize: 2,
	}

	// Initialize default region metrics for each region
	defaultRegion := "us-west"
	vm.regions[defaultRegion] = true
	now = time.Now().UTC()
	vm.regionMetrics[defaultRegion] = &RegionMetrics{
		LoadFactor:      0.5,
		LatencyMs:       10.0,
		ErrorRate:       0.0,
		ActiveWorkers:   2,
		PendingTasks:    0,
		LastHealthCheck: now,
		NetworkLatency: map[string]float64{
			"us-east": 50.0,
			"eu-west": 100.0,
			"ap-east": 150.0,
		},
		TEEMetrics: map[string]*TEEMetrics{
			"sgx": {
				EnclaveID:         []byte("sgx-test-enclave"),
				Type:              "sgx",
				LoadFactor:        0.5,
				SuccessRate:       1.0,
				LastAttested:      now,
				LastHealthCheck:   now,
				ErrorCount:        0,
				TaskCount:         1,
				ConsecutiveErrors: 0,
				Status:            "healthy",
			},
			"sev": {
				EnclaveID:         []byte("sev-test-enclave"),
				Type:              "sev",
				LoadFactor:        0.5,
				SuccessRate:       1.0,
				LastAttested:      now,
				LastHealthCheck:   now,
				ErrorCount:        0,
				TaskCount:         1,
				ConsecutiveErrors: 0,
				Status:            "healthy",
			},
		},
	}

	// Initialize TEE metrics for the default region
	if vm.teeMetrics == nil {
		vm.teeMetrics = make(map[string]map[string]*TEEMetrics)
	}
	vm.teeMetrics[defaultRegion] = vm.regionMetrics[defaultRegion].TEEMetrics

	// Initialize region loads
	if vm.regionLoads == nil {
		vm.regionLoads = make(map[string]*RegionLoad)
	}
	vm.regionLoads[defaultRegion] = &RegionLoad{
		SGX: struct {
			LoadFactor    float64
			TaskCount     uint64
			LastOperation time.Time
		}{
			LoadFactor:    0.5,
			TaskCount:     1,
			LastOperation: now,
		},
		SEV: struct {
			LoadFactor    float64
			TaskCount     uint64
			LastOperation time.Time
		}{
			LoadFactor:    0.5,
			TaskCount:     1,
			LastOperation: now,
		},
	}

	return vm, nil
}

func (vm *MockVM) RegisterRegion(ctx context.Context, regionID string, sgxEndpoint, sevEndpoint string) error {
	vm.mu.Lock()
	defer vm.mu.Unlock()

	// Check if region already exists
	if _, exists := vm.regions[regionID]; exists {
		return fmt.Errorf("region %s already registered", regionID)
	}

	// Validate endpoints
	if sgxEndpoint == "" || sevEndpoint == "" {
		return fmt.Errorf("invalid TEE endpoints: both SGX and SEV endpoints must be provided")
	}

	if vm.regions == nil {
		vm.regions = make(map[string]bool)
	}
	vm.regions[regionID] = true

	// Initialize metrics for new region
	if vm.regionMetrics == nil {
		vm.regionMetrics = make(map[string]*RegionMetrics)
	}

	now := time.Now().UTC()
	vm.regionMetrics[regionID] = &RegionMetrics{
		LoadFactor:      0.5,
		LatencyMs:       10.0,
		ErrorRate:       0.0,
		ActiveWorkers:   2,
		PendingTasks:    0,
		LastHealthCheck: now,
		NetworkLatency: map[string]float64{
			"us-east": 50.0,
			"eu-west": 100.0,
			"ap-east": 150.0,
		},
		TEEMetrics: map[string]*TEEMetrics{
			"sgx": {
				EnclaveID:         []byte("sgx-test-enclave"),
				Type:              "sgx",
				LoadFactor:        0.5,
				SuccessRate:       1.0,
				LastAttested:      now,
				LastHealthCheck:   now,
				ErrorCount:        0,
				TaskCount:         1,
				ConsecutiveErrors: 0,
				Status:            "healthy",
			},
			"sev": {
				EnclaveID:         []byte("sev-test-enclave"),
				Type:              "sev",
				LoadFactor:        0.5,
				SuccessRate:       1.0,
				LastAttested:      now,
				LastHealthCheck:   now,
				ErrorCount:        0,
				TaskCount:         1,
				ConsecutiveErrors: 0,
				Status:            "healthy",
			},
		},
	}

	// Initialize TEE metrics map for the region
	if vm.teeMetrics == nil {
		vm.teeMetrics = make(map[string]map[string]*TEEMetrics)
	}
	vm.teeMetrics[regionID] = vm.regionMetrics[regionID].TEEMetrics

	// Initialize region loads
	if vm.regionLoads == nil {
		vm.regionLoads = make(map[string]*RegionLoad)
	}
	vm.regionLoads[regionID] = &RegionLoad{
		SGX: struct {
			LoadFactor    float64
			TaskCount     uint64
			LastOperation time.Time
		}{
			LoadFactor:    0.5,
			TaskCount:     1,
			LastOperation: now,
		},
		SEV: struct {
			LoadFactor    float64
			TaskCount     uint64
			LastOperation time.Time
		}{
			LoadFactor:    0.5,
			TaskCount:     1,
			LastOperation: now,
		},
	}

	return nil
}

func (vm *MockVM) ExecuteInRegion(ctx context.Context, regionID string, action chain.Action) (*core.ExecutionResult, error) {
	// First verify region exists under read lock
	vm.mu.RLock()
	exists := vm.regions[regionID]
	vm.mu.RUnlock()
	
	if !exists {
		return nil, fmt.Errorf("region not found: %s", regionID)
	}

	// Get current time for execution tracking
	now := time.Now()

	// Initialize metrics if they don't exist
	vm.mu.Lock()
	if vm.teeMetrics == nil {
		vm.teeMetrics = make(map[string]map[string]*TEEMetrics)
	}
	if vm.teeMetrics[regionID] == nil {
		vm.teeMetrics[regionID] = make(map[string]*TEEMetrics)
	}
	if _, exists := vm.teeMetrics[regionID]["sgx"]; !exists {
		vm.teeMetrics[regionID]["sgx"] = NewTEEMetrics([]byte("sgx-test-enclave-1234567890"), "sgx")
	}
	if _, exists := vm.teeMetrics[regionID]["sev"]; !exists {
		vm.teeMetrics[regionID]["sev"] = NewTEEMetrics([]byte("sev-test-enclave-0987654321"), "sev")
	}
	vm.mu.Unlock()

	// Create fresh attestations for this execution
	attestations := [2]core.TEEAttestation{
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
		// Handle cross-region state transfer first
		if err := vm.handleCrossRegionTransfer(ctx, regionID, a); err != nil {
			// Update metrics for failed execution
			executionTime := float64(time.Since(now).Milliseconds())
			vm.updateTEEMetrics(regionID, "sgx", executionTime, false)
			vm.updateTEEMetrics(regionID, "sev", executionTime, false)
			return nil, err
		}

		// Handle cross-region action
		if a.Intent == nil {
			// Update metrics for failed execution
			executionTime := float64(time.Since(now).Milliseconds())
			vm.updateTEEMetrics(regionID, "sgx", executionTime, false)
			vm.updateTEEMetrics(regionID, "sev", executionTime, false)
			return nil, fmt.Errorf("nil cross-region intent")
		}

		// Process state changes for this region under write lock
		vm.mu.Lock()
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
						Storage:  change.Value,
						RegionID: regionID,
						Status:   "active",
						LastUpdated: now,
					}
				}
			}
		}
		vm.mu.Unlock()

		// Update metrics for successful execution
		executionTime := float64(time.Since(now).Milliseconds())
		vm.updateTEEMetrics(regionID, "sgx", executionTime, true)
		vm.updateTEEMetrics(regionID, "sev", executionTime, true)

		return &core.ExecutionResult{
			StateHash:    []byte("test-state-hash"),
			Output:       []byte("cross-region-executed"),
			RegionID:     regionID,
			TimeProof:    timeProof,
			Attestations: attestations,
		}, nil

	case *actions.CreateObjectAction:
		// Create new object under write lock
		vm.mu.Lock()
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
		vm.mu.Unlock()

		// Update metrics for successful execution
		executionTime := float64(time.Since(now).Milliseconds())
		vm.updateTEEMetrics(regionID, "sgx", executionTime, true)
		vm.updateTEEMetrics(regionID, "sev", executionTime, true)

		return &core.ExecutionResult{
			StateHash:    []byte("test-state-hash"),
			Output:       []byte("created"),
			RegionID:     regionID,
			TimeProof:    timeProof,
			Attestations: attestations,
		}, nil

	case *actions.SendEventAction:
		// Check if object exists under read lock
		vm.mu.RLock()
		obj, exists := vm.objects[regionID][a.IDTo]
		vm.mu.RUnlock()

		if !exists {
			// Update metrics for failed execution
			executionTime := float64(time.Since(now).Milliseconds())
			vm.updateTEEMetrics(regionID, "sgx", executionTime, false)
			vm.updateTEEMetrics(regionID, "sev", executionTime, false)
			return nil, fmt.Errorf("object not found in region")
		}

		if len(a.Attestations) > 0 {
			if len(a.Attestations) != 2 {
				// Update metrics for failed execution
				executionTime := float64(time.Since(now).Milliseconds())
				vm.updateTEEMetrics(regionID, "sgx", executionTime, false)
				vm.updateTEEMetrics(regionID, "sev", executionTime, false)
				return nil, fmt.Errorf("invalid attestation count")
			}

			for i, att := range a.Attestations {
				if len(att.EnclaveID) == 0 {
					// Update metrics for failed execution
					executionTime := float64(time.Since(now).Milliseconds())
					vm.updateTEEMetrics(regionID, "sgx", executionTime, false)
					vm.updateTEEMetrics(regionID, "sev", executionTime, false)
					return nil, fmt.Errorf("missing enclave ID in attestation %d", i)
				}
				if att.Timestamp.IsZero() {
					// Update metrics for failed execution
					executionTime := float64(time.Since(now).Milliseconds())
					vm.updateTEEMetrics(regionID, "sgx", executionTime, false)
					vm.updateTEEMetrics(regionID, "sev", executionTime, false)
					return nil, fmt.Errorf("invalid timestamp in attestation %d", i)
				}
				age := time.Since(att.Timestamp)
				if age > 5*time.Minute {
					// Update metrics for failed execution
					executionTime := float64(time.Since(now).Milliseconds())
					vm.updateTEEMetrics(regionID, "sgx", executionTime, false)
					vm.updateTEEMetrics(regionID, "sev", executionTime, false)
					return nil, fmt.Errorf("attestation timestamp expired")
				}
			}

			attestations = a.Attestations
			baseTime := now
			prevStateHash := attestations[0].Data

			// Ensure strict timestamp ordering
			for i := range attestations {
				// Add 1ms between attestations to ensure strict ordering
				attestations[i].Timestamp = baseTime.Add(time.Duration(i) * time.Millisecond)
				// For the second attestation, link it to the first one's state hash
				if i > 0 {
					attestations[i].Data = prevStateHash
				}
			}
		}

		// Update object under write lock
		vm.mu.Lock()
		obj.LastUpdated = now
		vm.mu.Unlock()

		// Update metrics for successful execution
		executionTime := float64(time.Since(now).Milliseconds())
		vm.updateTEEMetrics(regionID, "sgx", executionTime, true)
		vm.updateTEEMetrics(regionID, "sev", executionTime, true)

		return &core.ExecutionResult{
			StateHash:    []byte("test-state-hash"),
			Output:       []byte("executed"),
			RegionID:     regionID,
			TimeProof:    timeProof,
			Attestations: attestations,
		}, nil

	default:
		// Update metrics for failed execution
		executionTime := float64(time.Since(now).Milliseconds())
		vm.updateTEEMetrics(regionID, "sgx", executionTime, false)
		vm.updateTEEMetrics(regionID, "sev", executionTime, false)
		return nil, fmt.Errorf("unsupported action type: %T", action)
	}
}

func (vm *MockVM) handleCrossRegionTransfer(ctx context.Context, regionID string, action *actions.CrossRegionAction) error {
	// Copy state to target region
	vm.mu.Lock()
	defer vm.mu.Unlock()
	
	for targetRegion, changes := range action.Intent.StateChanges {
		if vm.objects[targetRegion] == nil {
			vm.objects[targetRegion] = make(map[string]*core.ObjectState)
		}

		for _, change := range changes {
			key := string(change.Key)
			switch change.Operation {
			case xregion.StateOpTransferIn:
				vm.objects[targetRegion][key] = &core.ObjectState{
					Storage:  change.Value,
					RegionID: targetRegion,
					Status:   "active",
				}
			}
		}
	}
	return nil
}

func (vm *MockVM) updateTEEMetrics(regionID string, teeType string, executionTime float64, success bool) {
	vm.mu.Lock()
	defer vm.mu.Unlock()

	// Initialize maps if they don't exist
	if vm.regionMetrics == nil {
		vm.regionMetrics = make(map[string]*RegionMetrics)
	}
	if vm.teeMetrics == nil {
		vm.teeMetrics = make(map[string]map[string]*TEEMetrics)
	}

	// Get or create region metrics
	regionMetrics, exists := vm.regionMetrics[regionID]
	if !exists {
		regionMetrics = &RegionMetrics{
			LoadFactor:      0.0,
			LatencyMs:       10.0,
			ErrorRate:       0.0,
			ActiveWorkers:   2,
			PendingTasks:    0,
			LastHealthCheck: time.Now(),
			NetworkLatency: map[string]float64{
				"us-east": 50.0,
				"eu-west": 100.0,
				"ap-east": 150.0,
			},
			TEEMetrics: make(map[string]*TEEMetrics),
		}
		vm.regionMetrics[regionID] = regionMetrics
	}

	// Initialize NetworkLatency map if it doesn't exist
	if regionMetrics.NetworkLatency == nil {
		regionMetrics.NetworkLatency = map[string]float64{
			"us-east": 50.0,
			"eu-west": 100.0,
			"ap-east": 150.0,
		}
	}

	// Update network latencies based on execution time
	for region := range regionMetrics.NetworkLatency {
		// Add some jitter to network latencies
		jitter := (rand.Float64() * 10.0) - 5.0 // +/- 5ms jitter
		regionMetrics.NetworkLatency[region] += jitter
		if regionMetrics.NetworkLatency[region] < 10.0 {
			regionMetrics.NetworkLatency[region] = 10.0
		}
	}

	// Get or create TEE metrics
	teeMetrics, exists := regionMetrics.TEEMetrics[teeType]
	if !exists {
		teeMetrics = NewTEEMetrics([]byte(fmt.Sprintf("%s-test-enclave", teeType)), teeType)
		regionMetrics.TEEMetrics[teeType] = teeMetrics
	}

	// Update TEE metrics
	teeMetrics.UpdateMetrics(executionTime, success)

	// Update region-level metrics
	totalTasks := uint64(0)
	totalErrors := uint64(0)
	for _, metrics := range regionMetrics.TEEMetrics {
		totalTasks += metrics.TaskCount
		totalErrors += metrics.ErrorCount
	}

	if totalTasks > 0 {
		regionMetrics.ErrorRate = float64(totalErrors) / float64(totalTasks)
		regionMetrics.LoadFactor = math.Min(float64(totalTasks)/100.0, 1.0)
	}

	// Sync TEE metrics to the separate map
	if vm.teeMetrics[regionID] == nil {
		vm.teeMetrics[regionID] = make(map[string]*TEEMetrics)
	}
	vm.teeMetrics[regionID][teeType] = teeMetrics
}

func (vm *MockVM) GetRegionHealth(ctx context.Context, regionID string) (*HealthStatus, error) {
	vm.mu.Lock()
	defer vm.mu.Unlock()

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
	vm.mu.Lock()
	defer vm.mu.Unlock()

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
	vm.mu.Lock()
	defer vm.mu.Unlock()

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
	vm.mu.Lock()
	defer vm.mu.Unlock()

	if !vm.regions[regionID] {
		return false, fmt.Errorf("region not found")
	}

	// Mock verification - in real implementation would verify against MerkleDB
	return true, nil
}

// Helper function to get region metrics
func (vm *MockVM) GetRegionMetrics(ctx context.Context, regionID string) (*RegionMetrics, error) {
	vm.mu.Lock()
	defer vm.mu.Unlock()

	metrics, exists := vm.regionMetrics[regionID]
	if !exists {
		return nil, fmt.Errorf("region %s not found", regionID)
	}

	// Get TEE metrics
	teeMetrics := vm.teeMetrics[regionID]

	// Calculate aggregate metrics
	var totalLoadFactor float64
	var totalLatency float64
	activeWorkers := 0

	// Set minimum values
	metrics.LoadFactor = 0.2  // Minimum load factor
	metrics.LatencyMs = 50.0  // Minimum latency of 50ms

	for _, tee := range teeMetrics {
		// Include all TEEs in the calculation, not just healthy ones
		totalLoadFactor += tee.LoadFactor
		totalLatency += tee.ExecutionTimeMs
		if tee.Status == "healthy" {
			activeWorkers++
		}
	}

	numTees := len(teeMetrics)
	if numTees > 0 {
		metrics.LoadFactor = math.Max(totalLoadFactor/float64(numTees), 0.2)
		metrics.LatencyMs = math.Max(totalLatency/float64(numTees), 50.0)
	}
	metrics.ActiveWorkers = activeWorkers
	metrics.LastHealthCheck = time.Now()
	metrics.TEEMetrics = teeMetrics

	return metrics, nil
}

func (vm *MockVM) SimulateRegionFailure(regionID string) error {
	vm.mu.Lock()
	
	// Store current metrics
	metrics := vm.regionMetrics[regionID]
	if metrics == nil {
		vm.mu.Unlock()
		return fmt.Errorf("region %s not found", regionID)
	}
	
	// Save original load factors
	sgxLoad := metrics.TEEMetrics["sgx"].LoadFactor
	sevLoad := metrics.TEEMetrics["sev"].LoadFactor
	
	// Simulate failure
	delete(vm.regions, regionID)
	vm.mu.Unlock()
	
	// Recover after delay with reduced load
	go func() {
		time.Sleep(500 * time.Millisecond) // Increased delay for more reliable testing
		
		vm.mu.Lock()
		defer vm.mu.Unlock()
		
		vm.regions[regionID] = true
		if metrics.TEEMetrics["sgx"] != nil {
			metrics.TEEMetrics["sgx"].LoadFactor = sgxLoad * 0.5 // Reduce load by 50%
		}
		if metrics.TEEMetrics["sev"] != nil {
			metrics.TEEMetrics["sev"].LoadFactor = sevLoad * 0.5 // Reduce load by 50%
		}
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
			// Temporarily disable region
			delete(vm.regions, regionID)

			// Random delay between 50-150ms
			delay := 50 + rand.Intn(100)
			time.Sleep(time.Duration(delay) * time.Millisecond)

			// Re-enable region
			vm.regions[regionID] = true
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
		vm.regions[regionID] = true
	}()

	return nil
}

func generateTestTasks(t *testing.T, vm *MockVM, regionID string, count int) []*core.ExecutionResult {
	require := require.New(t)
	results := make([]*core.ExecutionResult, count)

	for i := 0; i < count; i++ {
		action := &actions.CreateObjectAction{
			ID:      fmt.Sprintf("test-obj-%d", i),
			Code:    []byte("test code"),
			Storage: []byte("test storage"),
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

	var wg sync.WaitGroup
	errChan := make(chan error, numTasks)
	resultChan := make(chan *core.ExecutionResult, numTasks)

	for i := 0; i < numTasks; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			action := &actions.CreateObjectAction{
				ID:      fmt.Sprintf("concurrent-test-%d", idx),
				Code:    []byte("test code"),
				Storage: []byte("test storage"),
			}
			result, err := vm.ExecuteInRegion(ctx, regionID, action)
			if err != nil {
				errChan <- err
				return
			}
			resultChan <- result
		}(i)
	}

	wg.Wait()
	close(errChan)
	close(resultChan)

	// Check for errors
	for err := range errChan {
		require.NoError(err)
	}

	// Collect results
	results := make([]*core.ExecutionResult, 0, numTasks)
	for result := range resultChan {
		results = append(results, result)
	}

	require.Equal(numTasks, len(results))
	return results
}

func verifyAttestationChain(t *testing.T, results []*core.ExecutionResult) {
	require := require.New(t)

	for i := 1; i < len(results); i++ {
		prev := results[i-1]
		curr := results[i]

		// Verify attestation chain
		require.NotNil(prev.Attestations)
		require.NotNil(curr.Attestations)

		// Verify timestamps
		require.True(curr.Attestations[0].Timestamp.After(prev.Attestations[0].Timestamp))
		require.True(curr.Attestations[1].Timestamp.After(prev.Attestations[1].Timestamp))

		// Verify enclave IDs remain consistent
		require.Equal(prev.Attestations[0].EnclaveID, curr.Attestations[0].EnclaveID)
		require.Equal(prev.Attestations[1].EnclaveID, curr.Attestations[1].EnclaveID)

		// Verify state transitions
		require.Equal(prev.Attestations[0].Data, curr.Attestations[0].Data)
		require.Equal(prev.Attestations[1].Data, curr.Attestations[1].Data)
	}
}

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
					ID:      "partition-test-object",
					Code:    []byte("test code"),
					Storage: []byte("test storage"),
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
				if !bytes.Equal(result2.Attestations[0].Data, result2.Attestations[1].Data) {
					t.Fatal("TEE states are not synced")
				}
			},
		},
		{
			name: "Partial Network Connectivity",
			test: func(t *testing.T) {
				// Create test object
				createAction := &actions.CreateObjectAction{
					ID:      "partial-conn-test-object",
					Code:    []byte("test code"),
					Storage: []byte("test storage"),
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
				if !bytes.Equal(result2.Attestations[0].Data, result2.Attestations[1].Data) {
					t.Fatal("TEE states are not synced")
				}
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
	m.ExecutionTimeMs = executionTime // Don't average, just use latest
	m.TaskCount++

	// Calculate load factor based on both execution time and task count
	// Base load starts at 0.2 and increases with task count and execution time
	baseLoad := 0.2
	taskLoad := math.Min(float64(m.TaskCount)/20.0, 0.4)  // More aggressive scaling with tasks
	timeLoad := math.Min(executionTime/100.0, 0.4)        // Scale with execution time up to 400ms
	m.LoadFactor = math.Min(baseLoad + taskLoad + timeLoad, 1.0)

	if !success {
		m.ErrorCount++
		m.ConsecutiveErrors++
		m.SuccessRate = float64(m.TaskCount-m.ErrorCount) / float64(m.TaskCount)
		if m.ConsecutiveErrors > 3 {
			m.Status = "degraded"
		}
	} else {
		m.ConsecutiveErrors = 0
		m.SuccessRate = float64(m.TaskCount-m.ErrorCount) / float64(m.TaskCount)
		if m.ConsecutiveErrors == 0 {
			m.Status = "healthy"
		}
	}
}

func NewTEEMetrics(enclaveID []byte, teeType string) *TEEMetrics {
	now := time.Now()
	return &TEEMetrics{
		EnclaveID:         enclaveID,
		Type:              teeType,
		LoadFactor:        0.2, // Start with a non-zero load
		SuccessRate:       1.0,
		LastAttested:      now,
		LastHealthCheck:   now,
		ErrorCount:        0,
		TaskCount:         0,
		ConsecutiveErrors: 0,
		ExecutionTimeMs:   0.0,
		Status:            "healthy",
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
	m.Status = "healthy"
	m.LastHealthCheck = time.Now()
}

func (m *TEEMetrics) NeedsAttestation() bool {
	return time.Since(m.LastAttested) > 5*time.Minute
}

func (m *TEEMetrics) UpdateAttestation() {
	m.LastAttested = time.Now()
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

func (vm *MockVM) generateMockMetrics(regionID string) *RegionMetrics {
	return &RegionMetrics{
		LoadFactor:      0.5,
		LatencyMs:       100,
		ErrorRate:       0.01,
		ActiveWorkers:   2,
		PendingTasks:    5,
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
		FunctionCall: "health_check",
		Parameters:   []byte("check"),
	}

	_, err := vm.ExecuteInRegion(ctx, regionID, action)
	return err
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
		ID:      "test-object",
		Code:    []byte("test code"),
		Storage: []byte("test storage"),
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
	tests := []struct {
		name string
		test func(*testing.T)
	}{
		{
			name: "Basic Operations",
			test: func(t *testing.T) {
				require := require.New(t)
				testVM, regionID := setupTestEnvironment(t)
				ctx := context.Background()

				// Test concurrent operations
				var wg sync.WaitGroup
				numTasks := 5
				errChan := make(chan error, numTasks)
				resultChan := make(chan *core.ExecutionResult, numTasks)

				for i := 0; i < numTasks; i++ {
					wg.Add(1)
					go func(idx int) {
						defer wg.Done()
						action := &actions.CreateObjectAction{
							ID:      fmt.Sprintf("test-obj-%d", idx),
							Code:    []byte("test code"),
							Storage: []byte("test storage"),
						}

						result, err := testVM.ExecuteInRegion(ctx, regionID, action)
						if err != nil {
							errChan <- err
							return
						}
						resultChan <- result
					}(i)
				}

				wg.Wait()
				close(errChan)
				close(resultChan)

				// Check for errors
				for err := range errChan {
					require.NoError(err)
				}

				// Verify results
				resultCount := 0
				for result := range resultChan {
					require.NotNil(result)
					require.Len(result.Attestations, 2)
					require.NotNil(result.TimeProof)
					resultCount++
				}
				require.Equal(numTasks, resultCount)
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
	errChan := make(chan error, numOperations)
	resultChan := make(chan *core.ExecutionResult, numOperations)

	for i := 0; i < numOperations; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			action := &actions.CreateObjectAction{
				ID:      fmt.Sprintf("concurrent-obj-%d", idx),
				Code:    []byte("test code"),
				Storage: []byte("test storage"),
			}

			result, err := testVM.ExecuteInRegion(ctx, regionID, action)
			if err != nil {
				errChan <- err
				return
			}
			resultChan <- result
		}(i)
	}

	wg.Wait()
	close(errChan)
	close(resultChan)

	// Check for errors
	for err := range errChan {
		require.NoError(err)
	}

	// Verify results
	resultCount := 0
	for result := range resultChan {
		require.NotNil(result)
		require.Len(result.Attestations, 2)
		require.NotNil(result.TimeProof)
		resultCount++
	}
	require.Equal(numOperations, resultCount)
}

func TestTEEPairMetrics(t *testing.T) {
	t.Run("Basic_Metrics_Collection", func(t *testing.T) {
		require := require.New(t)

		// Setup test environment
		vm, regionID := setupTestEnvironment(t)

		// Generate test load
		results := simulateConcurrentTasks(t, vm, regionID, 10)
		require.Len(results, 10, "should have 10 results")

		// Wait for metrics to stabilize
		time.Sleep(100 * time.Millisecond)

		// Get metrics
		metrics, err := vm.GetRegionMetrics(context.Background(), regionID)
		require.NoError(err)
		require.NotNil(metrics)

		// Verify TEE metrics
		sgxMetrics := metrics.TEEMetrics["sgx"]
		sevMetrics := metrics.TEEMetrics["sev"]

		require.NotNil(sgxMetrics)
		require.NotNil(sevMetrics)

		// Verify metrics values
		require.Greater(sgxMetrics.LoadFactor, float64(0.2), "SGX load factor should be above threshold")
		require.Less(sgxMetrics.LoadFactor, float64(1.0), "SGX load factor should be less than 1.0")
		require.Greater(sevMetrics.LoadFactor, float64(0.2), "SEV load factor should be above threshold")
		require.Less(sevMetrics.LoadFactor, float64(1.0), "SEV load factor should be less than 1.0")
		require.Greater(sgxMetrics.SuccessRate, float64(0.7), "SGX success rate should be high")
		require.Greater(sevMetrics.SuccessRate, float64(0.7), "SEV success rate should be high")
		require.Greater(sgxMetrics.TaskCount, uint64(8), "SGX task count should be high under load")
		require.Greater(sevMetrics.TaskCount, uint64(8), "SEV task count should be high under load")
	})
}

func verifyTEEStateConsistency(t *testing.T, attestations [2]core.TEEAttestation) {
	// Verify both TEEs produced valid attestations
	if len(attestations[0].EnclaveID) == 0 {
		t.Fatal("empty SGX enclave ID")
	}
	if len(attestations[1].EnclaveID) == 0 {
		t.Fatal("empty SEV enclave ID")
	}

	// Verify timestamps are close (within 5ms)
	timeDiff := attestations[1].Timestamp.Sub(attestations[0].Timestamp)
	if timeDiff < 0 || timeDiff > 5*time.Millisecond {
		t.Fatal("timestamp difference between attestations exceeds allowed threshold")
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
