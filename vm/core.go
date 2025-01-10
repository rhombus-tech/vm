// vm/core.go
package vm

import (
	"context"
	"fmt"

	"github.com/ava-labs/avalanchego/ids"
	"github.com/ava-labs/avalanchego/utils/logging"
	"github.com/ava-labs/hypersdk/chain"
	"github.com/ava-labs/hypersdk/state"
	"go.uber.org/zap"

    "github.com/rhombus-tech/vm/actions"
	"github.com/rhombus-tech/vm/compute"
	"github.com/rhombus-tech/vm/core"
	pb "github.com/rhombus-tech/vm/tee/proto/pb"
	"github.com/rhombus-tech/vm/verifier"
)

// ShuttleVM represents a validator node in the network
type ShuttleVM struct {
    chainID       ids.ID
    stateManager  state.Mutable
    verifier      *verifier.StateVerifier
    computeNodes  map[string]*compute.NodeClient // Map of regionID to compute node client
    config        *Config
    logger        logging.Logger
    codeValidator *CodeValidator                 
    teeValidator  *Validator
}

func New(ctx context.Context, config *Config, logger logging.Logger) (*ShuttleVM, error) {
    // Create verifier for attestation checking
    stateVerifier := verifier.New(nil) 

    // Initialize compute node connections
    computeNodes := make(map[string]*compute.NodeClient)
    for region, endpoint := range config.ComputeNodeEndpoints {
        // Create NodeClientConfig from endpoint string
        nodeConfig := compute.NodeClientConfig{
            Endpoint: endpoint,
            ControllerPath: "/usr/local/bin/tee-controller", // Use appropriate defaults
            WasmPath: "/usr/local/bin/tee-wasm-module.wasm",
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
        computeNodes:  computeNodes,
        verifier:      stateVerifier,
        logger:        logger,
        codeValidator: codeValidator,
        teeValidator:  teeValidator,
    }

    return vm, nil
}

func (vm *ShuttleVM) Initialize(
    ctx context.Context,
    chainID ids.ID,
    stateManager state.Mutable,
) error {
    vm.chainID = chainID
    vm.stateManager = stateManager
    
    // Set state manager for verifier
    vm.verifier.SetState(stateManager)

    // Initialize compute node connections if not in verification-only mode
    if !vm.config.VerificationOnly {
        if err := vm.initializeComputeConnections(ctx); err != nil {
            return fmt.Errorf("failed to initialize compute connections: %w", err)
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
    client, exists := vm.computeNodes[regionID]
    if !exists {
        return nil, fmt.Errorf("no compute node available for region %s", regionID)
    }

    // Convert chain.Action to ExecutionRequest
    req := &pb.ExecutionRequest{
        RegionId: regionID,
        // Add appropriate field mappings based on your action type
        // You may need to type assert the action to get specific fields
    }

    // Execute on compute node
    result, err := client.Execute(ctx, req)
    if err != nil {
        return nil, err
    }

    // Convert pb.TEEAttestation array to [2]core.TEEAttestation
    var attestations [2]core.TEEAttestation
    if len(result.Attestations) >= 2 {
        for i := 0; i < 2; i++ {
            attestations[i] = core.TEEAttestation{
                EnclaveID:   result.Attestations[i].EnclaveId,
                Measurement: result.Attestations[i].Measurement,
                // Add other field conversions
            }
        }
    }

    // Verify the attestations
    if err := vm.verifier.VerifyAttestationPair(ctx, attestations, nil); err != nil {
        return nil, fmt.Errorf("attestation verification failed: %w", err)
    }

    return &compute.ExecutionResult{
        StateHash:    result.StateHash,
        Result:       result.Result,
        Attestations: attestations,
        Timestamp:    result.Timestamp,
    }, nil
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