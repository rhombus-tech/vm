// vm/core.go
package vm

import (
    "context"
    "fmt"

    "github.com/ava-labs/avalanchego/ids"
    "github.com/ava-labs/avalanchego/utils/logging"
    "github.com/ava-labs/hypersdk/state"
    "github.com/ava-labs/hypersdk/chain"
    "go.uber.org/zap"

    "github.com/rhombus-tech/vm/verifier"
    "github.com/rhombus-tech/vm/compute"
)

// ShuttleVM represents a validator node in the network
type ShuttleVM struct {
    chainID       ids.ID
    stateManager  state.Mutable
    verifier      *verifier.StateVerifier
    computeNodes  map[string]*compute.NodeClient // Map of regionID to compute node client
    config        *Config
    logger        logging.Logger
}

func New(ctx context.Context, config *Config, logger logging.Logger) (*ShuttleVM, error) {
    // Create verifier for attestation checking
    stateVerifier := verifier.New(nil) // Will set state manager later

    // Initialize compute node connections
    computeNodes := make(map[string]*compute.NodeClient)
    for region, endpoint := range config.ComputeNodeEndpoints {
        client, err := compute.NewNodeClient(endpoint)
        if err != nil {
            return nil, fmt.Errorf("failed to connect to compute node for region %s: %w", region, err)
        }
        computeNodes[region] = client
    }

    vm := &ShuttleVM{
        config:       config,
        computeNodes: computeNodes,
        verifier:     stateVerifier,
        logger:       logger,
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

    // Execute on compute node
    result, err := client.Execute(ctx, action)
    if err != nil {
        return nil, err
    }

    // Verify the attestation
    if err := vm.verifier.VerifyAttestationPair(ctx, result.Attestations, nil); err != nil {
        return nil, fmt.Errorf("attestation verification failed: %w", err)
    }

    return result, nil
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