// File: tee/client.go
package tee

import (
    "bytes"
    "context"
    "fmt"

    "google.golang.org/grpc"

    // Make sure your generated code is at this path:
    pb "github.com/rhombus-tech/vm/tee/proto/pb"

    // Example references:
    "github.com/rhombus-tech/vm/actions"
    "github.com/rhombus-tech/vm/verifier"
)

// Client holds TEE connections and a verifier
type Client struct {
    sgxClient pb.TeeExecutionClient
    sevClient pb.TeeExecutionClient
    sgxConn   *grpc.ClientConn
    sevConn   *grpc.ClientConn

    verifier *verifier.StateVerifier // must have exported method(s)
}

// NewClient must return both *Client and error
func NewClient(
    sgxEndpoint, sevEndpoint string,
    v *verifier.StateVerifier,
) (*Client, error) {
    // Connect to the SGX TEE
    sgxConn, err := grpc.Dial(sgxEndpoint, grpc.WithInsecure())
    if err != nil {
        return nil, fmt.Errorf("failed to dial SGX: %w", err)
    }

    // Connect to the SEV TEE
    sevConn, err := grpc.Dial(sevEndpoint, grpc.WithInsecure())
    if err != nil {
        // If SEV fails, close SGX too:
        _ = sgxConn.Close()
        return nil, fmt.Errorf("failed to dial SEV: %w", err)
    }

    client := &Client{
        sgxClient: pb.NewTeeExecutionClient(sgxConn),
        sevClient: pb.NewTeeExecutionClient(sevConn),
        sgxConn:   sgxConn,
        sevConn:   sevConn,
        verifier:  v,
    }
    return client, nil
}

// Close closes both SGX and SEV connections.
func (c *Client) Close() error {
    var errs []error
    if err := c.sgxConn.Close(); err != nil {
        errs = append(errs, fmt.Errorf("closing SGX: %w", err))
    }
    if err := c.sevConn.Close(); err != nil {
        errs = append(errs, fmt.Errorf("closing SEV: %w", err))
    }
    if len(errs) > 0 {
        return fmt.Errorf("TEE client close errors: %v", errs)
    }
    return nil
}

// ExecuteAction calls the TEE gRPC method 'Execute' on both SGX and SEV
func (c *Client) ExecuteAction(ctx context.Context, action *actions.SendEventAction) error {
    // Build the request proto
    req := &pb.ExecutionRequest{
        IdTo:         action.IDTo,
        FunctionCall: action.FunctionCall,
        Parameters:   action.Parameters,
        RegionId:     action.RegionID,
    }

    // Call SGX
    sgxResult, err := c.sgxClient.Execute(ctx, req)
    if err != nil {
        return fmt.Errorf("SGX Execute failed: %w", err)
    }

    // Call SEV
    sevResult, err := c.sevClient.Execute(ctx, req)
    if err != nil {
        return fmt.Errorf("SEV Execute failed: %w", err)
    }

    // Convert attestations and verify
    sgxAtts, err := protoToCoreAttestations(sgxResult.Attestations)
    if err != nil {
        return fmt.Errorf("failed to convert SGX attestations: %w", err)
    }
    
    sevAtts, err := protoToCoreAttestations(sevResult.Attestations)
    if err != nil {
        return fmt.Errorf("failed to convert SEV attestations: %w", err)
    }

    // Verify both attestation sets
    if err := c.verifier.VerifyAttestationPair(ctx, sgxAtts, nil); err != nil {
        return fmt.Errorf("SGX attestation verify failed: %w", err)
    }
    if err := c.verifier.VerifyAttestationPair(ctx, sevAtts, nil); err != nil {
        return fmt.Errorf("SEV attestation verify failed: %w", err)
    }

    // Compare results to ensure they match
    if err := c.compareResults(sgxResult, sevResult); err != nil {
        return err
    }

    return nil
}

func (c *Client) compareResults(sgxRes, sevRes *pb.ExecutionResult) error {
    if !bytes.Equal(sgxRes.StateHash, sevRes.StateHash) {
        return fmt.Errorf("state hash mismatch between SGX and SEV results")
    }
    if !bytes.Equal(sgxRes.Result, sevRes.Result) {
        return fmt.Errorf("execution result mismatch between SGX and SEV")
    }
    return nil
}
