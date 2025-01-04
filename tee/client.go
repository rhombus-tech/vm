// tee/client.go
package tee

import (
    "context"
    "fmt"
    "time"
    "bytes"
    "google.golang.org/grpc"
    
    pb "github.com/rhombus-tech/vm/tee"
    "github.com/rhombus-tech/vm/actions"
    "github.com/rhombus-tech/vm/verifier"
)

type Client struct {
    // gRPC connections for each TEE type
    sgxClient pb.TeeExecutionClient
    sevClient pb.TeeExecutionClient
    
    // Connection management
    sgxConn   *grpc.ClientConn
    sevConn   *grpc.ClientConn
    
    verifier  *verifier.StateVerifier
}

func NewClient(sgxEndpoint, sevEndpoint string, v *verifier.StateVerifier) (*Client, error) {
    // Connect to SGX service
    sgxConn, err := grpc.Dial(sgxEndpoint, grpc.WithInsecure())
    if err != nil {
        return nil, fmt.Errorf("failed to connect to SGX service: %w", err)
    }

    // Connect to SEV service
    sevConn, err := grpc.Dial(sevEndpoint, grpc.WithInsecure())
    if err != nil {
        sgxConn.Close()
        return nil, fmt.Errorf("failed to connect to SEV service: %w", err)
    }
    
    return &Client{
        sgxClient: pb.NewTeeExecutionClient(sgxConn),
        sevClient: pb.NewTeeExecutionClient(sevConn),
        sgxConn: sgxConn,
        sevConn: sevConn,
        verifier: v,
    }, nil
}

func (c *Client) Close() error {
    if err := c.sgxConn.Close(); err != nil {
        return fmt.Errorf("failed to close SGX connection: %w", err)
    }
    if err := c.sevConn.Close(); err != nil {
        return fmt.Errorf("failed to close SEV connection: %w", err)
    }
    return nil
}

func (c *Client) ExecuteAction(ctx context.Context, action *actions.SendEventAction) error {
    // Create execution request
    req := &pb.ExecutionRequest{
        IdTo:         action.IDTo,
        RegionId:     action.RegionID,
        Parameters:   action.Parameters,
        FunctionCall: action.FunctionCall,
    }

    // Execute in both TEEs
    sgxResult, err := c.sgxClient.Execute(ctx, req)
    if err != nil {
        return fmt.Errorf("SGX execution failed: %w", err)
    }

    sevResult, err := c.sevClient.Execute(ctx, req)
    if err != nil {
        return fmt.Errorf("SEV execution failed: %w", err)
    }

    // Convert to internal event format with proper time handling
    sgxEvent, err := convertEvent(&pb.Event{
        Id:           action.IDTo,
        FunctionCall: action.FunctionCall,
        Parameters:   action.Parameters,
        Timestamp:    sgxResult.Timestamp,
        Attestations: sgxResult.Attestations,
        RegionId:     action.RegionID,
    })
    if err != nil {
        return fmt.Errorf("failed to convert SGX event: %w", err)
    }

    sevEvent, err := convertEvent(&pb.Event{
        Id:           action.IDTo,
        FunctionCall: action.FunctionCall,
        Parameters:   action.Parameters,
        Timestamp:    sevResult.Timestamp,
        Attestations: sevResult.Attestations,
        RegionId:     action.RegionID,
    })
    if err != nil {
        return fmt.Errorf("failed to convert SEV event: %w", err)
    }

    // Verify both attestation pairs
    if err := c.verifier.verifyAttestationPair(ctx, sgxEvent.Attestations, nil); err != nil {
        return fmt.Errorf("SGX attestation verification failed: %w", err)
    }

    if err := c.verifier.verifyAttestationPair(ctx, sevEvent.Attestations, nil); err != nil {
        return fmt.Errorf("SEV attestation verification failed: %w", err)
    }

    // Verify results match
    if err := c.verifyResults(sgxResult, sevResult); err != nil {
        return fmt.Errorf("result verification failed: %w", err)
    }

    return nil
}

func (c *Client) verifyResults(sgxResult, sevResult *pb.ExecutionResult) error {
    // Compare state hashes
    if !bytes.Equal(sgxResult.StateHash, sevResult.StateHash) {
        return ErrResultMismatch
    }

    // Compare raw results if available
    if !bytes.Equal(sgxResult.Result, sevResult.Result) {
        return ErrResultMismatch
    }

    return nil
}


// Helper to convert proto attestations to our format
func convertProtoAttestations(protos []*pb.TEEAttestation) [2]actions.TEEAttestation {
    var result [2]actions.TEEAttestation
    for i, p := range protos {
        // Parse timestamp string into time.Time
        timestamp, err := time.Parse(time.RFC3339, p.Timestamp)
        if err != nil {
            // Use current time as fallback if parsing fails
            timestamp = time.Now().UTC()
        }
        
        result[i] = actions.TEEAttestation{
            EnclaveID:   p.EnclaveId,
            Measurement: p.Measurement,
            Timestamp:   timestamp,
            Data:        p.Data,
            Signature:   p.Signature,
            RegionProof: p.RegionProof,
        }
    }
    return result
}