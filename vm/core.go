// vm/core.go
package vm

import (
	"bytes"
	"context"
	"fmt"
	"time"

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
    validatorMgr *validatorManager
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

    vm.initializeValidators()

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

// In vm/core.go

// In vm/core.go

// In vm/core.go

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

// Add this type to help with attestation verification
type ExecutionVerifier struct {
    verifier *verifier.StateVerifier
}

// Add this method to ShuttleVM
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