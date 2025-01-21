// File: tee/client.go
package tee

import (
    "bytes"
    "context"
    "fmt"

    "google.golang.org/grpc"

    "github.com/rhombus-tech/vm/tee/proto"
    "github.com/rhombus-tech/vm/actions"
    "github.com/rhombus-tech/vm/verifier"
)

const (
    TEEPairStatusActive   = "active"
    TEEPairStatusDegraded = "degraded"
    TEEPairStatusFailed   = "failed"
)

// TEEPair holds SGX and SEV clients for a region
type TEEPair struct {
    sgxClient proto.TeeExecutionClient
    sevClient proto.TeeExecutionClient
    sgxConn   *grpc.ClientConn
    sevConn   *grpc.ClientConn
}

// Client holds TEE connections and a verifier
type Client struct {
    // Default (non-regional) TEE clients
    sgxClient proto.TeeExecutionClient
    sevClient proto.TeeExecutionClient
    sgxConn   *grpc.ClientConn
    sevConn   *grpc.ClientConn

    // Regional TEE clients
    regionTEEs map[string]*TEEPair

    verifier *verifier.StateVerifier
}

func CreateTEEPair(config *TEEPairConfig) (*TEEPair, error) {
    // Connect to SGX endpoint
    sgxConn, err := grpc.Dial(config.SGXEndpoint, grpc.WithInsecure())
    if err != nil {
        return nil, fmt.Errorf("failed to connect to SGX: %w", err)
    }

    // Connect to SEV endpoint
    sevConn, err := grpc.Dial(config.SEVEndpoint, grpc.WithInsecure())
    if err != nil {
        // If SEV fails, close SGX too
        _ = sgxConn.Close()
        return nil, fmt.Errorf("failed to connect to SEV: %w", err)
    }

    return &TEEPair{
        sgxClient: proto.NewTeeExecutionClient(sgxConn),
        sevClient: proto.NewTeeExecutionClient(sevConn),
        sgxConn:   sgxConn,
        sevConn:   sevConn,
    }, nil
}

// NewClient creates a new client with default TEE connections
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
        sgxClient:  proto.NewTeeExecutionClient(sgxConn),
        sevClient:  proto.NewTeeExecutionClient(sevConn),
        sgxConn:    sgxConn,
        sevConn:    sevConn,
        verifier:   v,
        regionTEEs: make(map[string]*TEEPair),
    }
    return client, nil
}

// AddRegion adds a new region's TEE endpoints
func (c *Client) AddRegion(
    regionID string,
    sgxEndpoint string,
    sevEndpoint string,
) error {
    // Connect to the SGX TEE
    sgxConn, err := grpc.Dial(sgxEndpoint, grpc.WithInsecure())
    if err != nil {
        return fmt.Errorf("failed to dial SGX for region %s: %w", regionID, err)
    }

    // Connect to the SEV TEE
    sevConn, err := grpc.Dial(sevEndpoint, grpc.WithInsecure())
    if err != nil {
        // If SEV fails, close SGX too
        _ = sgxConn.Close()
        return fmt.Errorf("failed to dial SEV for region %s: %w", regionID, err)
    }

    c.regionTEEs[regionID] = &TEEPair{
        sgxClient: proto.NewTeeExecutionClient(sgxConn),
        sevClient: proto.NewTeeExecutionClient(sevConn),
        sgxConn:   sgxConn,
        sevConn:   sevConn,
    }

    return nil
}

// Close closes all TEE connections
func (c *Client) Close() error {
    var errs []error
    
    // Close default connections
    if err := c.sgxConn.Close(); err != nil {
        errs = append(errs, fmt.Errorf("closing default SGX: %w", err))
    }
    if err := c.sevConn.Close(); err != nil {
        errs = append(errs, fmt.Errorf("closing default SEV: %w", err))
    }

    // Close regional connections
    for regionID, pair := range c.regionTEEs {
        if err := pair.sgxConn.Close(); err != nil {
            errs = append(errs, fmt.Errorf("closing SGX for region %s: %w", regionID, err))
        }
        if err := pair.sevConn.Close(); err != nil {
            errs = append(errs, fmt.Errorf("closing SEV for region %s: %w", regionID, err))
        }
    }

    if len(errs) > 0 {
        return fmt.Errorf("TEE client close errors: %v", errs)
    }
    return nil
}

// ExecuteAction maintains original functionality while adding regional support
func (c *Client) ExecuteAction(ctx context.Context, action *actions.SendEventAction) error {
    // Build the request proto
    req := &proto.ExecutionRequest{
        IdTo:         action.IDTo,
        FunctionCall: action.FunctionCall,
        Parameters:   action.Parameters,
        RegionId:     action.RegionID,
    }

    var sgxResult, sevResult *proto.ExecutionResult
    var err error

    // Check if this is a regional execution
    if action.RegionID != "" {
        if pair, ok := c.regionTEEs[action.RegionID]; ok {
            // Execute in specific region
            sgxResult, err = pair.sgxClient.Execute(ctx, req)
            if err != nil {
                return fmt.Errorf("regional SGX Execute failed: %w", err)
            }

            sevResult, err = pair.sevClient.Execute(ctx, req)
            if err != nil {
                return fmt.Errorf("regional SEV Execute failed: %w", err)
            }
        }
    }

    // Fall back to default TEE clients if no region specified or not found
    if sgxResult == nil {
        sgxResult, err = c.sgxClient.Execute(ctx, req)
        if err != nil {
            return fmt.Errorf("SGX Execute failed: %w", err)
        }

        sevResult, err = c.sevClient.Execute(ctx, req)
        if err != nil {
            return fmt.Errorf("SEV Execute failed: %w", err)
        }
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

func (c *Client) compareResults(sgxRes, sevRes *proto.ExecutionResult) error {
    if !bytes.Equal(sgxRes.StateHash, sevRes.StateHash) {
        return fmt.Errorf("state hash mismatch between SGX and SEV results")
    }
    if !bytes.Equal(sgxRes.Result, sevRes.Result) {
        return fmt.Errorf("execution result mismatch between SGX and SEV")
    }
    return nil
}

func (p *TEEPair) Close() error {
    var errs []error
    if p.sgxConn != nil {
        if err := p.sgxConn.Close(); err != nil {
            errs = append(errs, fmt.Errorf("failed to close SGX connection: %w", err))
        }
    }
    if p.sevConn != nil {
        if err := p.sevConn.Close(); err != nil {
            errs = append(errs, fmt.Errorf("failed to close SEV connection: %w", err))
        }
    }
    if len(errs) > 0 {
        return fmt.Errorf("errors closing connections: %v", errs)
    }
    return nil
}