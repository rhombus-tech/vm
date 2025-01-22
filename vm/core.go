// vm/core.go
package vm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"math"
	"net/http"
	"sync"
	"time"

	"github.com/ava-labs/avalanchego/database"
	"github.com/ava-labs/avalanchego/ids"
	"github.com/ava-labs/avalanchego/snow"
	"github.com/ava-labs/avalanchego/snow/engine/common"
	"github.com/ava-labs/avalanchego/utils/logging"
	"github.com/ava-labs/avalanchego/x/merkledb"
	"github.com/ava-labs/hypersdk/api"
	"github.com/ava-labs/hypersdk/chain"
	"go.uber.org/zap"

	"github.com/rhombus-tech/vm/actions"
	"github.com/rhombus-tech/vm/api/jsonrpc"
	"github.com/rhombus-tech/vm/compute"
	"github.com/rhombus-tech/vm/consts"
	"github.com/rhombus-tech/vm/coordination"
	"github.com/rhombus-tech/vm/core"
	"github.com/rhombus-tech/vm/regions"
	"github.com/rhombus-tech/vm/storage"
	"github.com/rhombus-tech/vm/tee/proto"
	"github.com/rhombus-tech/vm/verifier"
    "github.com/rhombus-tech/vm/interfaces"
)

// ShuttleVM represents a validator node in the network
type ShuttleVM struct {
    chainID          ids.ID
    config           *Config
    ctx             *snow.Context
    db              database.Database
    appSender       common.AppSender
    stateManager    interfaces.StateManager
    verifier        *verifier.StateVerifier
    computeNodes    map[string]*compute.NodeClient
    logger          logging.Logger
    codeValidator   *CodeValidator
    teeValidator    *Validator
    validatorMgr    *validatorManager
    regionManager   *regions.RegionManager
    coordinator     *coordination.Coordinator
    monitoringCtx   context.Context
    monitoringCancel context.CancelFunc
    teeRegistry *compute.TEERegistry
}

func New(ctx context.Context, config *Config, logger logging.Logger, db database.Database) (*ShuttleVM, error) {
    // Create database wrapper with Close method
    dbWrapper := storage.NewDatabaseWrapper(db)

    // Initialize merkleDB
    merkleDB, err := merkledb.New(
        ctx,
        db,
        merkledb.Config{
            HistoryLength: 256,
        },
    )
    if err != nil {
        return nil, fmt.Errorf("failed to create merkledb: %w", err)
    }

    // Create state manager
    stateManager, err := storage.NewStateManager(db, dbWrapper, merkleDB)
    if err != nil {
        return nil, fmt.Errorf("failed to create state manager: %w", err)
    }

    // Initialize compute node connections
    computeNodes := make(map[string]*compute.NodeClient)
    for region, nodeConfig := range config.ComputeNodeEndpoints {
        client, err := compute.NewNodeClient(nodeConfig)
        if err != nil {
            // Clean up any existing connections before returning
            for _, existing := range computeNodes {
                existing.Close()
            }
            return nil, fmt.Errorf("failed to connect to compute node for region %s: %w", region, err)
        }
        computeNodes[region] = client
    }

    // Create verifier with state manager
    stateVerifier := verifier.New(stateManager)

    // Create code validator with configured max size
    codeValidator := NewCodeValidator(config.MaxCodeSize)

    // Create TEE validator
    teeValidator := NewValidator(stateVerifier)

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
        StoragePath:       fmt.Sprintf("/tmp/coordinator-%s", config.ChainID),
        PersistenceEnabled: true,
    }

    // Create coordinator using state manager's base storage
    coordinator, err := coordination.NewCoordinator(
        coordConfig,
        merkleDB,
        stateManager.GetBaseStorage(),
    )
    if err != nil {
        // Clean up compute nodes
        for _, client := range computeNodes {
            client.Close()
        }
        return nil, fmt.Errorf("failed to create coordinator: %w", err)
    }

    // Create region store directly with state manager
    regionStore := storage.NewRegionStateStore(stateManager)

    // Create region manager
    regionManager := regions.NewRegionManager(regionStore)

    // Convert chainID to proper type
    chainID, err := ids.FromString(config.ChainID)
    if err != nil {
        return nil, fmt.Errorf("invalid chain ID: %w", err)
    }

    // Create monitoring context with timeout
    monitoringCtx, monitoringCancel := context.WithTimeout(context.Background(), 24*time.Hour)

    vm := &ShuttleVM{
        chainID:          chainID,
        config:           config,
        db:              db,
        computeNodes:    computeNodes,
        verifier:        stateVerifier,
        logger:          logger,
        codeValidator:   codeValidator,
        teeValidator:    teeValidator,
        stateManager:    stateManager, // stateManager already implements the interface
        coordinator:     coordinator,
        regionManager:   regionManager,
        monitoringCtx:   monitoringCtx,
        monitoringCancel: monitoringCancel,
    }

    // Start monitoring if not in verification-only mode
    if !config.VerificationOnly {
        if err := vm.startMonitoring(); err != nil {
            // Clean up all resources
            vm.cleanup()
            return nil, fmt.Errorf("failed to start monitoring: %w", err)
        }
    }

    return vm, nil
}

// Add cleanup helper method
func (vm *ShuttleVM) cleanup() {
    // Cancel monitoring
    if vm.monitoringCancel != nil {
        vm.monitoringCancel()
    }

    // Close compute nodes
    for _, client := range vm.computeNodes {
        client.Close()
    }

    // Stop coordinator
    if vm.coordinator != nil {
        vm.coordinator.Stop()
    }

    // Stop region manager
    if vm.regionManager != nil {
        vm.regionManager.Stop()
    }
}


func (vm *ShuttleVM) startMonitoring() error {
    // Start region monitoring
    if err := vm.regionManager.StartMonitoring(vm.monitoringCtx); err != nil {
        return fmt.Errorf("failed to start region monitoring: %w", err)
    }

    // Start TEE health monitoring
    go vm.MonitorTEEHealth(vm.monitoringCtx)

    // Start metrics collection
    if balancer := vm.regionManager.GetBalancer(); balancer != nil {
        go balancer.CollectMetrics(vm.monitoringCtx)
    }

    return nil
}


func (vm *ShuttleVM) Initialize(
    ctx context.Context,
    snowCtx *snow.Context,
    db database.Database,
    genesisBytes []byte,
    upgradeBytes []byte,
    configBytes []byte,
    toEngine chan<- common.Message,
    fxs []*common.Fx,
    appSender common.AppSender,
) error {
    // Store core dependencies
    vm.ctx = snowCtx
    vm.db = db
    vm.appSender = appSender
    vm.chainID = snowCtx.ChainID

    // Create database wrapper that implements state.Mutable
    dbWrapper := storage.NewDatabaseWrapper(db)

    // Initialize MerkleDB
    merkleDB, err := merkledb.New(
        ctx,
        db,
        merkledb.Config{
            HistoryLength: 256,
        },
    )
    if err != nil {
        return fmt.Errorf("failed to create merkledb: %w", err)
    }

    // Create state manager
    stateManager, err := storage.NewStateManager(
        vm.db,
        dbWrapper,
        merkleDB,
    )
    if err != nil {
        return fmt.Errorf("failed to create state manager: %w", err)
    }
    vm.stateManager = stateManager

    // Initialize coordinator
    coordConfig := &coordination.Config{
        MinWorkers:         2,
        MaxWorkers:         10,
        WorkerTimeout:      30 * time.Second,
        ChannelTimeout:     10 * time.Second,
        MaxMessageSize:     1024 * 1024,
        EncryptionEnabled:  true,
        RequireAttestation: true,
        AttestationTimeout: 5 * time.Second,
        StoragePath:        fmt.Sprintf("/tmp/coordinator-%s", vm.chainID),
        PersistenceEnabled: true,
    }

    // Create coordinator using state manager's base storage
    coordinator, err := coordination.NewCoordinator(
        coordConfig,
        merkleDB,
        vm.stateManager.GetBaseStorage(), // Use state manager's base storage
    )
    if err != nil {
        return fmt.Errorf("failed to create coordinator: %w", err)
    }
    vm.coordinator = coordinator
    
    // Start coordinator
    if err := vm.coordinator.Start(); err != nil {
        return fmt.Errorf("failed to start coordinator: %w", err)
    }

    // Set up verifier
    if vm.verifier == nil {
        vm.verifier = verifier.New(dbWrapper)
    }
    vm.verifier.SetState(dbWrapper)

    // Create monitoring context with cancellation
    vm.monitoringCtx, vm.monitoringCancel = context.WithCancel(context.Background())

    // Initialize region store and manager
    regionStore := storage.NewRegionStateStore(vm.stateManager)  // Now accepts interface
    vm.regionManager = regions.NewRegionManager(regionStore)

    // Initialize validators
    vm.initializeValidators()

    // Initialize compute connections and register workers
    if !vm.config.VerificationOnly {
        if err := vm.initializeComputeConnections(ctx); err != nil {
            return fmt.Errorf("failed to initialize compute connections: %w", err)
        }

        // Register compute nodes as workers and set up regions
        for regionID := range vm.computeNodes {
            if err := vm.setupRegionWorkers(ctx, regionID); err != nil {
                return err
            }
        }
    }

    // Start region monitoring
    if err := vm.regionManager.StartMonitoring(vm.monitoringCtx); err != nil {
        return fmt.Errorf("failed to start region monitoring: %w", err)
    }

    // Start TEE health monitoring
    go vm.MonitorTEEHealth(vm.monitoringCtx)

    // Start metrics collection for the balancer
    if balancer := vm.regionManager.GetBalancer(); balancer != nil {
        go balancer.CollectMetrics(vm.monitoringCtx)
    }

    return nil
}

// Move region worker setup to separate method for clarity
func (vm *ShuttleVM) setupRegionWorkers(ctx context.Context, regionID string) error {
    sgxWorkerID := coordination.WorkerID(fmt.Sprintf("sgx-%s", regionID))
    sevWorkerID := coordination.WorkerID(fmt.Sprintf("sev-%s", regionID))

    // Register SGX worker
    if err := vm.coordinator.RegisterWorker(ctx, sgxWorkerID, []byte("sgx-enclave")); err != nil {
        return fmt.Errorf("failed to register SGX worker for region %s: %w", regionID, err)
    }

    // Register SEV worker
    if err := vm.coordinator.RegisterWorker(ctx, sevWorkerID, []byte("sev-enclave")); err != nil {
        return fmt.Errorf("failed to register SEV worker for region %s: %w", regionID, err)
    }

    // Register region with worker pair
    if err := vm.coordinator.RegisterRegion(ctx, regionID, [2]coordination.WorkerID{sgxWorkerID, sevWorkerID}); err != nil {
        return fmt.Errorf("failed to register region %s: %w", regionID, err)
    }

    // Set up secure channel between workers
    channel := coordination.NewSecureChannel(sgxWorkerID, sevWorkerID)
    if err := channel.EstablishSecure(); err != nil {
        return fmt.Errorf("failed to establish secure channel for region %s: %w", regionID, err)
    }

    return nil
}


func (vm *ShuttleVM) initializeComputeConnections(ctx context.Context) error {
    for region, client := range vm.computeNodes {
        // Test connection and verify TEE capabilities
        if err := client.ValidateConnection(ctx); err != nil {
            return fmt.Errorf("compute node validation failed for region %s: %w", region, err)
        }
    }
    return nil
}



func (vm *ShuttleVM) ValidateTransaction(ctx context.Context, tx *chain.Transaction) error {
    // Verify transaction format and auth
    if err := tx.Verify(ctx); err != nil {
        return err
    }
    
    unsignedBytes, err := tx.UnsignedBytes()
    if err != nil {
        return err
    }
    if err := tx.Auth.Verify(ctx, unsignedBytes); err != nil {
        return err
    }

    // For each action that includes TEE execution results
    for _, action := range tx.Actions {
        if execAction, ok := action.(*actions.SendEventAction); ok {
            // Construct execution result from action's attestations
            result := &compute.ExecutionResult{
                StateHash:    execAction.Attestations[0].Data, // Use first attestation's data as state hash
                Attestations: execAction.Attestations,
            }
            
            // Verify the TEE execution
            if err := vm.verifyTEEExecution(ctx, action, result); err != nil {
                return fmt.Errorf("TEE execution verification failed: %w", err)
            }
        }
        
        // Verify other aspects of state transition
        if err := vm.verifier.VerifyStateTransition(ctx, action); err != nil {
            return err
        }
    }

    return nil
}

// ValidateAndExecute validates and executes code in TEEs
func (vm *ShuttleVM) ValidateAndExecute(ctx context.Context, code []byte, action *actions.SendEventAction) error {
    // First validate code format
    if err := vm.codeValidator.ValidateCode(code); err != nil {
        return err
    }
    
    // Then validate TEE execution
    if err := vm.teeValidator.ValidateRegionalAction(ctx, action); err != nil {
        return err
    }
    
    return nil
}

func (vm *ShuttleVM) ExecuteInRegion(
    ctx context.Context,
    regionID string,
    action chain.Action,
) (*compute.ExecutionResult, error) {
    // Start timing the entire execution
    execStart := time.Now()

    // Check region health first
    health, err := vm.regionManager.GetRegionHealth(ctx, regionID)
    if err != nil {
        return nil, fmt.Errorf("failed to get region health: %w", err)
    }
    if health.Status != "healthy" {
        return nil, fmt.Errorf("region %s is not healthy: %s", regionID, health.Status)
    }

    // Select optimal TEE pair using load balancer
    selectedPair, err := vm.regionManager.GetBalancer().SelectOptimalPair(ctx, regionID)
    if err != nil {
        return nil, fmt.Errorf("failed to select TEE pair: %w", err)
    }

    // Get compute clients for selected pair
    sgxClient, exists := vm.computeNodes[selectedPair.SGXEndpoint]
    if !exists {
        return nil, fmt.Errorf("SGX compute node not found for pair %s", selectedPair.ID)
    }

    sevClient, exists := vm.computeNodes[selectedPair.SEVEndpoint]
    if !exists {
        return nil, fmt.Errorf("SEV compute node not found for pair %s", selectedPair.ID)
    }

    // Convert chain.Action to ExecutionRequest
    req := &proto.ExecutionRequest{
        RegionId: regionID,
        DetailedProof: true,
    }

    // Add action-specific fields
    switch a := action.(type) {
    case *actions.SendEventAction:
        req.IdTo = a.IDTo
        req.FunctionCall = a.FunctionCall
        req.Parameters = a.Parameters
    case *actions.CreateObjectAction:
        req.IdTo = a.ID
        req.Parameters = a.Code
    default:
        return nil, fmt.Errorf("unsupported action type: %T", action)
    }

    // Execute on both TEEs in parallel with monitoring
    var wg sync.WaitGroup
    var sgxResult, sevResult *proto.ExecutionResult
    var sgxErr, sevErr error

    wg.Add(2)
    go func() {
        defer wg.Done()
        sgxStart := time.Now()
        sgxResult, sgxErr = sgxClient.Execute(ctx, req)
        if sgxErr == nil {
            vm.updateTEEMetrics(ctx, regionID, selectedPair.ID, "sgx", time.Since(sgxStart))
        }
    }()
    go func() {
        defer wg.Done()
        sevStart := time.Now()
        sevResult, sevErr = sevClient.Execute(ctx, req)
        if sevErr == nil {
            vm.updateTEEMetrics(ctx, regionID, selectedPair.ID, "sev", time.Since(sevStart))
        }
    }()
    wg.Wait()

    // Calculate total execution time
    totalExecTime := time.Since(execStart)

    // Handle errors with metric updates
    if sgxErr != nil {
        vm.updateTEEError(ctx, regionID, selectedPair.ID, "sgx", sgxErr)
        return nil, fmt.Errorf("SGX execution failed: %w", sgxErr)
    }
    if sevErr != nil {
        vm.updateTEEError(ctx, regionID, selectedPair.ID, "sev", sevErr)
        return nil, fmt.Errorf("SEV execution failed: %w", sevErr)
    }

    // Convert attestations
    attestations := [2]core.TEEAttestation{
        {
            EnclaveID:   sgxResult.Attestations[0].EnclaveId,
            Measurement: sgxResult.Attestations[0].Measurement,
            Timestamp:   mustParseTime(sgxResult.Attestations[0].Timestamp),
            Data:        sgxResult.Attestations[0].Data,
            RegionProof: sgxResult.Attestations[0].RegionProof,
        },
        {
            EnclaveID:   sevResult.Attestations[0].EnclaveId,
            Measurement: sevResult.Attestations[0].Measurement,
            Timestamp:   mustParseTime(sevResult.Attestations[0].Timestamp),
            Data:        sevResult.Attestations[0].Data,
            RegionProof: sevResult.Attestations[0].RegionProof,
        },
    }

    // Verify attestations
    if err := vm.verifier.VerifyAttestationPair(ctx, attestations, nil); err != nil {
        vm.updateVerificationError(ctx, regionID, selectedPair.ID, err)
        return nil, fmt.Errorf("attestation verification failed: %w", err)
    }

    // Verify results match
    if !bytes.Equal(sgxResult.StateHash, sevResult.StateHash) {
        err := fmt.Errorf("state hash mismatch between SGX and SEV")
        vm.updateVerificationError(ctx, regionID, selectedPair.ID, err)
        return nil, err
    }

    // Update final metrics
    if err := vm.regionManager.UpdateMetrics(ctx, regionID, selectedPair.ID, func(metrics *regions.TEEPairMetrics) {
        metrics.PairID = selectedPair.ID
        metrics.SGXEndpoint = selectedPair.SGXEndpoint
        metrics.SEVEndpoint = selectedPair.SEVEndpoint
        metrics.LoadFactor = calculateLoadFactor(totalExecTime)
        metrics.SuccessRate = 1.0
        metrics.ExecutionTime = totalExecTime
        metrics.LastHealthCheck = time.Now()
        metrics.LastHealthy = time.Now()
        metrics.CPUUsage = getCPUUsage(sgxResult, sevResult)
        metrics.MemoryUsage = getMemoryUsage(sgxResult, sevResult)
        metrics.NetworkLatency = getAverageLatency(sgxResult, sevResult)
        metrics.PendingTasks = 0
    }); err != nil {
        log.Printf("Warning: failed to update metrics: %v", err)
    }

    return &compute.ExecutionResult{
        StateHash:    sgxResult.StateHash,
        Output:       sgxResult.Result,
        Attestations: attestations,
        Timestamp:    sgxResult.Timestamp,
        ID:          selectedPair.ID,
        RegionID:    regionID,
    }, nil
}


// Add helper functions

func isPairHealthy(metrics *regions.TEEPairMetrics) bool {
    return metrics.SuccessRate >= 0.95 && // 95% success rate
           metrics.LoadFactor < 0.8 &&    // Under 80% load
           time.Since(metrics.LastHealthy) < 5*time.Minute
}

func (vm *ShuttleVM) updateTEEMetrics(ctx context.Context, regionID, pairID, teeType string, execTime time.Duration) {
    if err := vm.regionManager.UpdateMetrics(ctx, regionID, pairID, func(metrics *regions.TEEPairMetrics) {
        metrics.PairID = pairID
        metrics.ExecutionTime = execTime
        metrics.LastHealthy = time.Now()
        metrics.SuccessRate = 1.0
    }); err != nil {
        log.Printf("Failed to update TEE metrics: %v", err)
    }
}

func (vm *ShuttleVM) updateTEEError(ctx context.Context, regionID, pairID, teeType string, err error) {
    if err := vm.regionManager.UpdateMetrics(ctx, regionID, pairID, func(metrics *regions.TEEPairMetrics) {
        metrics.PairID = pairID
        metrics.LastHealthCheck = time.Now()
        metrics.SuccessRate = 0.0
        metrics.ExecutionTime = 0
        metrics.ConsecutiveErrors++
    }); err != nil {
        log.Printf("Failed to update TEE error metrics: %v", err)
    }
}

func (vm *ShuttleVM) updateVerificationError(ctx context.Context, regionID, pairID string, err error) {
    if err := vm.regionManager.UpdateMetrics(ctx, regionID, pairID, func(metrics *regions.TEEPairMetrics) {
        metrics.PairID = pairID
        metrics.LastHealthCheck = time.Now()
        metrics.AttestationFailures++
        metrics.ConsecutiveErrors++
    }); err != nil {
        log.Printf("Failed to update verification error metrics: %v", err)
    }
}

func calculateLoadFactor(execTime time.Duration) float64 {
    // Simple load factor calculation based on execution time
    // You might want to make this more sophisticated
    return math.Min(float64(execTime.Milliseconds())/1000.0, 1.0)
}

// Add placeholders for metric extraction
func getCPUUsage(sgx, sev *proto.ExecutionResult) float64 {
    // Implement based on your proto definition
    return 0.0
}

func getMemoryUsage(sgx, sev *proto.ExecutionResult) float64 {
    // Implement based on your proto definition
    return 0.0
}

func getAverageLatency(sgx, sev *proto.ExecutionResult) float64 {
    // Implement based on your proto definition
    return 0.0
}


func getActionID(action chain.Action) string {
    switch a := action.(type) {
    case *actions.SendEventAction:
        return a.IDTo
    case *actions.CreateObjectAction:
        return a.ID
    default:
        return ""
    }
}

func getActionFunction(action chain.Action) string {
    switch a := action.(type) {
    case *actions.SendEventAction:
        return a.FunctionCall
    default:
        return ""
    }
}

func getActionParameters(action chain.Action) []byte {
    switch a := action.(type) {
    case *actions.SendEventAction:
        return a.Parameters
    case *actions.CreateObjectAction:
        return a.Code
    default:
        return nil
    }
}

// Helper function to parse timestamp
func mustParseTime(ts string) time.Time {
    t, err := time.Parse(time.RFC3339, ts)
    if err != nil {
        panic(fmt.Sprintf("invalid timestamp format: %v", err))
    }
    return t
}

func (vm *ShuttleVM) Shutdown(ctx context.Context) error {
    // Cancel monitoring context
    if vm.monitoringCancel != nil {
        vm.monitoringCancel()
    }

    // Stop region monitoring
    if vm.regionManager != nil {
        vm.regionManager.StopMonitoring()
    }

    // Close compute node connections
    for region, client := range vm.computeNodes {
        if err := client.Close(); err != nil {
            vm.logger.Error(
                "failed to close compute node client",
                zap.String("region", region),
                zap.Error(err),
            )
        }
    }

    return nil
}

// Add health/metrics API endpoints
func (vm *ShuttleVM) CreateHandlers(ctx context.Context) (map[string]http.Handler, error) {
    handlers := make(map[string]http.Handler)
    
    // Create API RPC server
    rpcServer := jsonrpc.NewJSONRPCServer(vm)
    rpcHandler, err := api.NewJSONRPCHandler(consts.Name, rpcServer)
    if err != nil {
        return nil, err
    }
    
    handlers["/rpc"] = rpcHandler

    // Add health check endpoint
    handlers["/health"] = http.HandlerFunc(vm.handleHealthCheck)

    // Add metrics endpoint
    handlers["/metrics"] = http.HandlerFunc(vm.handleMetrics)
    
    return handlers, nil
}

// Add health check handler
func (vm *ShuttleVM) handleHealthCheck(w http.ResponseWriter, r *http.Request) {
    regionID := r.URL.Query().Get("region")
    if regionID == "" {
        http.Error(w, "region ID required", http.StatusBadRequest)
        return
    }

    health, err := vm.regionManager.GetRegionHealth(r.Context(), regionID)
    if err != nil {
        http.Error(w, err.Error(), http.StatusInternalServerError)
        return
    }

    json.NewEncoder(w).Encode(health)
}

// Add metrics handler
func (vm *ShuttleVM) handleMetrics(w http.ResponseWriter, r *http.Request) {
    regionID := r.URL.Query().Get("region")
    if regionID == "" {
        http.Error(w, "region ID required", http.StatusBadRequest)
        return
    }

    pairID := r.URL.Query().Get("pair")
    if pairID == "" {
        // Return all pairs for region
        pairs, err := vm.regionManager.GetTEEPairs(regionID)
        if err != nil {
            http.Error(w, err.Error(), http.StatusInternalServerError)
            return
        }
        json.NewEncoder(w).Encode(pairs)
        return
    }

    // Return specific pair metrics
    metrics, err := vm.regionManager.GetMetrics(r.Context(), regionID, pairID)
    if err != nil {
        http.Error(w, err.Error(), http.StatusInternalServerError)
        return
    }

    json.NewEncoder(w).Encode(metrics)
}


// GetComputeClient returns a compute node client for a region
func (vm *ShuttleVM) GetComputeClient(regionID string) (*compute.NodeClient, error) {
    client, exists := vm.computeNodes[regionID]
    if !exists {
        return nil, fmt.Errorf("no compute node found for region %s", regionID)
    }
    return client, nil
}

// IsVerificationOnly returns true if this node only performs verification
func (vm *ShuttleVM) IsVerificationOnly() bool {
    return vm.config.VerificationOnly
}

type ExecutionVerifier struct {
    verifier *verifier.StateVerifier
}


func (vm *ShuttleVM) verifyTEEExecution(
    ctx context.Context, 
    action chain.Action,
    result *compute.ExecutionResult,
) error {
    // First verify both attestations are present and match requirements
    if len(result.Attestations) != 2 {
        return fmt.Errorf("expected 2 attestations, got %d", len(result.Attestations))
    }

    // Verify attestation pairs (one from SGX, one from SEV)
    sgxAtt := result.Attestations[0]
    sevAtt := result.Attestations[1]

    // Verify attestation timestamps match and are recent
    now := time.Now()
    maxAge := 5 * time.Minute
    
    if !sgxAtt.Timestamp.Equal(sevAtt.Timestamp) {
        return fmt.Errorf("attestation timestamps do not match: %v != %v", 
            sgxAtt.Timestamp, sevAtt.Timestamp)
    }
    
    age := now.Sub(sgxAtt.Timestamp)
    if age > maxAge || age < -maxAge {
        return fmt.Errorf("attestation timestamp outside acceptable range: %v", age)
    }

    // Verify SGX attestation
    if err := vm.verifySGXAttestation(ctx, sgxAtt); err != nil {
        return fmt.Errorf("SGX attestation verification failed: %w", err)
    }

    // Verify SEV attestation  
    if err := vm.verifySEVAttestation(ctx, sevAtt); err != nil {
        return fmt.Errorf("SEV attestation verification failed: %w", err)
    }

    // Verify attestation data matches
    if !bytes.Equal(sgxAtt.Data, sevAtt.Data) {
        return fmt.Errorf("attestation data mismatch between SGX and SEV")
    }

    // Verify state hash matches both attestations
    if !bytes.Equal(result.StateHash, sgxAtt.Data) || !bytes.Equal(result.StateHash, sevAtt.Data) {
        return fmt.Errorf("state hash mismatch with attestation data")
    }

    return nil
}

func (vm *ShuttleVM) verifySGXAttestation(ctx context.Context, att core.TEEAttestation) error {
    // Verify enclave measurement matches expected value
    expectedMeasurement := []byte{} // Configure this based on your enclave
    if !bytes.Equal(att.Measurement, expectedMeasurement) {
        return fmt.Errorf("invalid SGX enclave measurement")
    }

    // Verify the enclave signature
    if err := vm.verifySGXSignature(att.EnclaveID, att.Data, att.Signature); err != nil {
        return fmt.Errorf("invalid SGX signature: %w", err)
    }

    return nil
}

func (vm *ShuttleVM) verifySEVAttestation(ctx context.Context, att core.TEEAttestation) error {
    // Similar to SGX but with SEV-specific verification
    expectedMeasurement := []byte{} // Configure this based on your SEV VM
    if !bytes.Equal(att.Measurement, expectedMeasurement) {
        return fmt.Errorf("invalid SEV measurement")
    }

    // Verify the SEV signature
    if err := vm.verifySEVSignature(att.EnclaveID, att.Data, att.Signature); err != nil {
        return fmt.Errorf("invalid SEV signature: %w", err)
    }

    return nil
}

// Add signature verification helpers
func (vm *ShuttleVM) verifySGXSignature(enclaveID, data, signature []byte) error {
    // Implement SGX signature verification using the appropriate crypto library
    // This would typically involve:
    // 1. Verifying the signing key belongs to a genuine SGX enclave
    // 2. Verifying the signature over the data
    return nil
}

func (vm *ShuttleVM) verifySEVSignature(enclaveID, data, signature []byte) error {
    // Implement SEV signature verification
    // This would typically involve:
    // 1. Verifying the signing key belongs to a genuine SEV VM 
    // 2. Verifying the signature over the data
    return nil
}

func (vm *ShuttleVM) checkTEEHealth(ctx context.Context, endpoint string) error {
    client, exists := vm.computeNodes[endpoint]
    if !exists {
        return fmt.Errorf("compute node not found for endpoint %s", endpoint)
    }

    // Validate connection
    if err := client.ValidateConnection(ctx); err != nil {
        return fmt.Errorf("connection validation failed: %w", err)
    }

    // Execute a simple health check request
    req := &proto.ExecutionRequest{
        RegionId:     "health-check",
        FunctionCall: "health",
        Parameters:   []byte("health-check"),
    }

    _, err := client.Execute(ctx, req)
    if err != nil {
        return fmt.Errorf("health check execution failed: %w", err)
    }

    return nil
}

func (vm *ShuttleVM) MonitorTEEHealth(ctx context.Context) {
    ticker := time.NewTicker(30 * time.Second)
    defer ticker.Stop()

    for {
        select {
        case <-ticker.C:
            regions := vm.regionManager.ListRegions()
            
            for _, regionID := range regions {
                // Get and use the TEE pairs
                pairs, err := vm.regionManager.GetTEEPairs(regionID)
                if err != nil {
                    log.Printf("Failed to get TEE pairs for region %s: %v", regionID, err)
                    continue
                }

                // Iterate over the pairs and check each one
                for _, pair := range pairs {
                    // Check SGX health
                    if err := vm.checkTEEHealth(ctx, pair.SGXEndpoint); err != nil {
                        log.Printf("SGX health check failed for pair %s: %v", pair.ID, err)
                        vm.updateTEEStatus(ctx, regionID, pair.ID, "sgx", "unhealthy")
                    } else {
                        vm.updateTEEStatus(ctx, regionID, pair.ID, "sgx", "healthy")
                    }

                    // Check SEV health
                    if err := vm.checkTEEHealth(ctx, pair.SEVEndpoint); err != nil {
                        log.Printf("SEV health check failed for pair %s: %v", pair.ID, err)
                        vm.updateTEEStatus(ctx, regionID, pair.ID, "sev", "unhealthy")
                    } else {
                        vm.updateTEEStatus(ctx, regionID, pair.ID, "sev", "healthy")
                    }
                }
            }
        case <-ctx.Done():
            return
        }
    }
}

func (vm *ShuttleVM) updateTEEStatus(ctx context.Context, regionID, pairID, teeType, status string) {
    // Create metrics with the current status
    metrics := &regions.TEEPairMetrics{
        PairID:          pairID,
        LastHealthCheck: time.Now(),
    }

    // Set metrics based on status
    if status == "healthy" {
        metrics.SuccessRate = 1.0
        metrics.LoadFactor = 0.0
        metrics.LastHealthy = time.Now()
    } else {
        metrics.SuccessRate = 0.0
        metrics.LoadFactor = 1.0
        // Don't update LastHealthy for unhealthy status
    }

    // Add TEE-specific endpoints
    switch teeType {
    case "sgx":
        metrics.SGXEndpoint = pairID + "-sgx"
    case "sev":
        metrics.SEVEndpoint = pairID + "-sev"
    }

    // Update metrics through the balancer
    if err := vm.regionManager.GetBalancer().UpdatePairMetrics(ctx, regionID, pairID, metrics); err != nil {
        log.Printf("Failed to update TEE status metrics for %s-%s: %v", pairID, teeType, err)
    }
}