// vm/core.go
package vm

import (
	"bytes"
	"context"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/ava-labs/avalanchego/database"
	"github.com/ava-labs/avalanchego/ids"
	"github.com/ava-labs/avalanchego/snow"
	"github.com/ava-labs/avalanchego/snow/engine/common"
	"github.com/ava-labs/avalanchego/utils/logging"
	"github.com/ava-labs/avalanchego/x/merkledb"
	"github.com/ava-labs/hypersdk/chain"
	"go.uber.org/zap"

	"github.com/rhombus-tech/vm/actions"
	"github.com/rhombus-tech/vm/compute"
	"github.com/rhombus-tech/vm/coordination"
	"github.com/rhombus-tech/vm/core"
	"github.com/rhombus-tech/vm/regions"
	"github.com/rhombus-tech/vm/storage"
	"github.com/rhombus-tech/vm/tee/proto"
	"github.com/rhombus-tech/vm/verifier"
)

// ShuttleVM represents a validator node in the network
type ShuttleVM struct {
    chainID       ids.ID
    ctx          *snow.Context
    db           database.Database
    appSender    common.AppSender
    stateManager *storage.StateManager
    verifier     *verifier.StateVerifier
    computeNodes map[string]*compute.NodeClient // Use the correct type
    config       *Config
    logger       logging.Logger
    codeValidator *CodeValidator
    teeValidator *Validator
    validatorMgr *validatorManager
    regionManager *regions.RegionManager
    coordinator  *coordination.Coordinator
}

func New(ctx context.Context, config *Config, logger logging.Logger) (*ShuttleVM, error) {
    // Create verifier for attestation checking
    stateVerifier := verifier.New(nil) 

    // Initialize compute node connections
    // Declare the map first!
    computeNodes := make(map[string]*compute.NodeClient)
    
    for region, nodeConfig := range config.ComputeNodeEndpoints {
        // Set default paths if they're empty
        if nodeConfig.ControllerPath == "" {
            nodeConfig.ControllerPath = "/usr/local/bin/tee-controller"
        }
        if nodeConfig.WasmPath == "" {
            nodeConfig.WasmPath = "/usr/local/bin/tee-wasm-module.wasm"
        }
        
        client, err := compute.NewNodeClient(nodeConfig)
        if err != nil {
            return nil, fmt.Errorf("failed to connect to compute node for region %s: %w", region, err)
        }
        computeNodes[region] = client
    }

    // Create code validator with configured max size
    codeValidator := NewCodeValidator(config.MaxCodeSize)

    // Create TEE validator
    teeValidator := NewValidator(stateVerifier)

    vm := &ShuttleVM{
        config:        config,
        computeNodes:  computeNodes,  // Now computeNodes is defined
        verifier:      stateVerifier,
        logger:        logger,
        codeValidator: codeValidator,
        teeValidator:  teeValidator,
    }

    return vm, nil
}


func (vm *ShuttleVM) Initialize(
    ctx context.Context,
    snowCtx *snow.Context,
    db database.Database,  // You should have this from the VM initialization
    genesisBytes []byte,
    upgradeBytes []byte,
    configBytes []byte,
    toEngine chan<- common.Message,
    fxs []*common.Fx,
    appSender common.AppSender,
) error {
    // Store core dependencies
    vm.ctx = snowCtx
    vm.db = db  // Store the database reference
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

    // Create state manager with all three required arguments
    stateManager, err := storage.NewStateManager(
        vm.db,       // database.Database
        dbWrapper,   // state.Mutable
        merkleDB,    // merkledb.MerkleDB
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
        MaxMessageSize:     1024 * 1024, // 1MB
        EncryptionEnabled:  true,
        RequireAttestation: true,
        AttestationTimeout: 5 * time.Second,
        StoragePath:        fmt.Sprintf("/tmp/coordinator-%s", vm.chainID),
        PersistenceEnabled: true,
    }

    coordinator, err := coordination.NewCoordinator(coordConfig, merkleDB)
    if err != nil {
        return fmt.Errorf("failed to create coordinator: %w", err)
    }
    vm.coordinator = coordinator

    // Start coordinator
    if err := vm.coordinator.Start(); err != nil {
        return fmt.Errorf("failed to start coordinator: %w", err)
    }

    // Set up verifier with coordination-aware state management
    if vm.verifier == nil {
        vm.verifier = verifier.New(dbWrapper)
    }
    vm.verifier.SetState(dbWrapper)

    // Initialize compute node connections if not in verification-only mode
    if !vm.config.VerificationOnly {
        if err := vm.initializeComputeConnections(ctx); err != nil {
            return fmt.Errorf("failed to initialize compute connections: %w", err)
        }
    }

    regionStore := storage.NewRegionStateStore(vm.stateManager)
    vm.regionManager = regions.NewRegionManager(regionStore)

    // Initialize validators with coordinator awareness
    vm.initializeValidators()

    // Register compute nodes as workers and set up regions
    for regionID := range vm.computeNodes {
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

// ExecuteInRegion handles sending computation requests to C-nodes
func (vm *ShuttleVM) ExecuteInRegion(
    ctx context.Context,
    regionID string,
    action chain.Action,
) (*compute.ExecutionResult, error) {
    startTime := time.Now() // Define start time explicitly

    // Get region config to access TEE pairs
    _, err := vm.regionManager.GetRegionConfig(regionID) // Changed to _ since region isn't used
    if err != nil {
        return nil, fmt.Errorf("failed to get region config: %w", err)
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
        // Remove PairId as it's not in the proto definition
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

    // Execute on both TEEs in parallel
    var wg sync.WaitGroup
    var sgxResult, sevResult *proto.ExecutionResult
    var sgxErr, sevErr error

    wg.Add(2)
    go func() {
        defer wg.Done()
        sgxResult, sgxErr = sgxClient.Execute(ctx, req)
    }()
    go func() {
        defer wg.Done()
        sevResult, sevErr = sevClient.Execute(ctx, req)
    }()
    wg.Wait()

    // Check for errors
    if sgxErr != nil {
        return nil, fmt.Errorf("SGX execution failed: %w", sgxErr)
    }
    if sevErr != nil {
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
        return nil, fmt.Errorf("attestation verification failed: %w", err)
    }

    // Verify results match
    if !bytes.Equal(sgxResult.StateHash, sevResult.StateHash) {
        return nil, fmt.Errorf("state hash mismatch between SGX and SEV")
    }

    // Update metrics for the pair
    metrics := &regions.TEEPairMetrics{
        PairID:         selectedPair.ID,
        SGXEndpoint:    selectedPair.SGXEndpoint,
        SEVEndpoint:    selectedPair.SEVEndpoint,
        LoadFactor:     0.0, // Calculate based on your requirements
        SuccessRate:    1.0, // This execution was successful
        ExecutionTime:  time.Since(startTime),
        LastHealthCheck: time.Now(),
        LastHealthy:    time.Now(),
    }

    if err := vm.regionManager.GetBalancer().UpdatePairMetrics(ctx, regionID, selectedPair.ID, metrics); err != nil {
        // Log the error but don't fail the execution
        log.Printf("Failed to update metrics: %v", err)
    }

    return &compute.ExecutionResult{
        StateHash:    sgxResult.StateHash,
        Output:       sgxResult.Result,
        Attestations: attestations,
        Timestamp:    sgxResult.Timestamp,
        ID:          selectedPair.ID,  // Using ID instead of PairID
        RegionID:     regionID,
    }, nil
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