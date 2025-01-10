package compute

import (
    "context"
    "github.com/ava-labs/hypersdk/chain"
    pb "github.com/rhombus-tech/vm/tee/proto/pb"
    "google.golang.org/grpc"
)

// NodeClient handles communication with compute nodes
type NodeClient struct {
    client  pb.TeeExecutionClient
    conn    *grpc.ClientConn
    endpoint string
}

// ExecutionResult represents the result from a compute node
type ExecutionResult struct {
    Result       []byte
    StateHash    []byte
    Attestations [2]TEEAttestation
    Timestamp    string
}

func NewNodeClient(endpoint string) (*NodeClient, error) {
    conn, err := grpc.Dial(endpoint, grpc.WithInsecure())
    if err != nil {
        return nil, err
    }

    return &NodeClient{
        client:   pb.NewTeeExecutionClient(conn),
        conn:     conn,
        endpoint: endpoint,
    }, nil
}

func (c *NodeClient) Execute(ctx context.Context, action chain.Action) (*ExecutionResult, error) {
    // Convert action to ExecutionRequest
    req := &pb.ExecutionRequest{
        // Fill request fields based on action
    }

    resp, err := c.client.Execute(ctx, req)
    if err != nil {
        return nil, err
    }

    // Convert response to ExecutionResult
    return &ExecutionResult{
        Result:       resp.Result,
        StateHash:    resp.StateHash,
        Attestations: convertAttestations(resp.Attestations),
        Timestamp:    resp.Timestamp,
    }, nil
}

func (c *NodeClient) ValidateConnection(ctx context.Context) error {
    // Implement connection validation
    return nil
}

func (c *NodeClient) Close() error {
    if c.conn != nil {
        return c.conn.Close()
    }
    return nil
}