// vm/validator.go
package vm

import (
    "context"
    "errors"
    "fmt"
    "bytes"
    "google.golang.org/grpc"
    
	"github.com/rhombus-tech/vm/actions"
    "github.com/rhombus-tech/vm/core"
    "github.com/rhombus-tech/vm/verifier"
    "github.com/rhombus-tech/vm/tee/proto"
)

type Validator struct {
    // Map of regionID to TEE clients
    regionTEEs map[string]struct {
        sgxClient proto.TeeExecutionClient
        sevClient proto.TeeExecutionClient
    }
    verifier   *verifier.StateVerifier
    grpcConns  []*grpc.ClientConn // Track connections for cleanup
}

// NewValidator creates a validator instance
func NewValidator(verifier *verifier.StateVerifier) *Validator {
    return &Validator{
        regionTEEs: make(map[string]struct {
            sgxClient proto.TeeExecutionClient
            sevClient proto.TeeExecutionClient
        }),
        verifier:  verifier,
        grpcConns: make([]*grpc.ClientConn, 0),
    }
}

// AddRegion adds a region's compute nodes to the validator
func (v *Validator) AddRegion(regionID string, sgxEndpoint, sevEndpoint string) error {
    // Connect to SGX endpoint
    sgxConn, err := grpc.Dial(sgxEndpoint, grpc.WithInsecure())
    if err != nil {
        return fmt.Errorf("failed to connect to SGX node: %w", err)
    }
    v.grpcConns = append(v.grpcConns, sgxConn)

    // Connect to SEV endpoint
    sevConn, err := grpc.Dial(sevEndpoint, grpc.WithInsecure())
    if err != nil {
        // Clean up SGX connection if SEV fails
        sgxConn.Close()
        return fmt.Errorf("failed to connect to SEV node: %w", err)
    }
    v.grpcConns = append(v.grpcConns, sevConn)

    // Store clients
    v.regionTEEs[regionID] = struct {
        sgxClient proto.TeeExecutionClient
        sevClient proto.TeeExecutionClient
    }{
        sgxClient: proto.NewTeeExecutionClient(sgxConn),
        sevClient: proto.NewTeeExecutionClient(sevConn),
    }

    return nil
}

// ValidateRegionalAction validates an action by checking with compute nodes
func (v *Validator) ValidateRegionalAction(ctx context.Context, action *actions.SendEventAction) error {
    region, ok := v.regionTEEs[action.RegionID]
    if !ok {
        return errors.New("region not found")
    }

    // Create execution request
    req := &proto.ExecutionRequest{
        RegionId:     action.RegionID,
        IdTo:         action.IDTo,
        FunctionCall: action.FunctionCall,
        Parameters:   action.Parameters,
    }

    // Execute on both TEEs
    sgxResult, err := region.sgxClient.Execute(ctx, req)
    if err != nil {
        return fmt.Errorf("SGX verification failed: %w", err)
    }

    sevResult, err := region.sevClient.Execute(ctx, req)
    if err != nil {
        return fmt.Errorf("SEV verification failed: %w", err)
    }

    // Verify results match
    if !bytes.Equal(sgxResult.StateHash, sevResult.StateHash) {
        return errors.New("state hash mismatch between TEEs")
    }

    // Verify attestations
    attestations := [2]core.TEEAttestation{
        {
            EnclaveID:   sgxResult.Attestations[0].EnclaveId,
            Measurement: sgxResult.Attestations[0].Measurement,
            Data:       sgxResult.Attestations[0].Data,
        },
        {
            EnclaveID:   sevResult.Attestations[0].EnclaveId,
            Measurement: sevResult.Attestations[0].Measurement,
            Data:       sevResult.Attestations[0].Data,
        },
    }

    if err := v.verifier.VerifyAttestationPair(ctx, attestations, nil); err != nil {
        return fmt.Errorf("attestation verification failed: %w", err)
    }

    return nil
}
// Close cleans up validator connections
func (v *Validator) Close() error {
    var errs []error
    for _, conn := range v.grpcConns {
        if err := conn.Close(); err != nil {
            errs = append(errs, err)
        }
    }
    if len(errs) > 0 {
        return fmt.Errorf("errors closing connections: %v", errs)
    }
    return nil
}
