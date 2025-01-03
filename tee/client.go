// tee/client.go
package tee

import (
    "context"
    "fmt"
    "google.golang.org/grpc"
    
    pb "github.com/rhombus-tech/vm/tee"
    "github.com/rhombus-tech/vm/actions"
    "github.com/rhombus-tech/vm/verifier"
)

type Client struct {
    client pb.TeeExecutionClient
    conn   *grpc.ClientConn
    verifier *verifier.StateVerifier // Use existing verifier
}

func NewClient(endpoint string, v *verifier.StateVerifier) (*Client, error) {
    conn, err := grpc.Dial(endpoint, grpc.WithInsecure())
    if err != nil {
        return nil, fmt.Errorf("failed to connect to TEE service: %w", err)
    }
    
    return &Client{
        client: pb.NewTeeExecutionClient(conn),
        conn: conn,
        verifier: v,
    }, nil
}

func (c *Client) ExecuteAction(ctx context.Context, action *actions.SendEventAction) error {
    req := &pb.ExecutionRequest{
        RegionId:     action.RegionID,
        Parameters:   action.Parameters,
        FunctionCall: action.FunctionCall,
    }

    result, err := c.client.Execute(ctx, req)
    if err != nil {
        return fmt.Errorf("TEE execution failed: %w", err)
    }

    // Convert proto attestations to our format
    attestations := convertProtoAttestations(result.Attestations)
    
    // Use existing verifier
    return c.verifier.verifyAttestationPair(ctx, attestations, nil)
}

// Helper to convert proto format to our format
func convertProtoAttestations(protos []*pb.Attestation) [2]actions.TEEAttestation {
    var result [2]actions.TEEAttestation
    for i, p := range protos {
        result[i] = actions.TEEAttestation{
            EnclaveID:   p.EnclaveId,
            Measurement: p.Measurement,
            Timestamp:   p.Timestamp,
            Data:        p.Data,
            Signature:   p.Signature,
        }
    }
    return result
}