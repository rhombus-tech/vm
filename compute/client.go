package compute

import (
   "bytes"
   "context"
   "fmt"

   "github.com/rhombus-tech/vm/tee"
   pb "github.com/rhombus-tech/vm/tee/proto/pb"
   "google.golang.org/grpc"
   "google.golang.org/grpc/connectivity"
)

// Add default paths
const (
    DefaultControllerPath = "/usr/local/bin/tee-controller"
    DefaultWasmPath      = "/usr/local/bin/tee-wasm-module.wasm"
)

type NodeClientConfig struct {
    Endpoint        string
    ControllerPath  string
    WasmPath        string
}

func DefaultNodeClientConfig() NodeClientConfig {
    return NodeClientConfig{
        ControllerPath: "/usr/local/bin/tee-controller",
        WasmPath:      "/usr/local/bin/tee-wasm-module.wasm",
    }
}

func NewNodeClient(config NodeClientConfig) (*NodeClient, error) {
    // Setup gRPC connection
    conn, err := grpc.Dial(config.Endpoint, grpc.WithInsecure())
    if err != nil {
        return nil, fmt.Errorf("failed to connect: %w", err)
    }

    // Initialize TEE bridge
    bridge := tee.NewRustBridge(config.ControllerPath, config.WasmPath)

    return &NodeClient{
        endpoint:   config.Endpoint,
        teeBridge:  bridge,
        grpcClient: pb.NewTeeExecutionClient(conn),
        conn:       conn,
    }, nil
}


// Execute sends a computation request to the compute node
func (c *NodeClient) Execute(ctx context.Context, req *pb.ExecutionRequest) (*pb.ExecutionResult, error) {
    // First execute in TEEs via Rust bridge
    teeResult, err := c.teeBridge.Execute(ctx, req)
    if err != nil {
        return nil, fmt.Errorf("TEE execution failed: %w", err)
    }

    // Then submit to compute node for coordination
    result, err := c.grpcClient.Execute(ctx, req)
    if err != nil {
        return nil, fmt.Errorf("compute node execution failed: %w", err)
    }

    // Verify TEE result matches compute node result 
    if !bytes.Equal(teeResult.StateHash, result.StateHash) {
        return nil, fmt.Errorf("result mismatch between TEE and compute node")
    }

    return result, nil
}

func (c *NodeClient) ValidateConnection(ctx context.Context) error {
    // Validate gRPC connection
    state := c.conn.GetState()
    if state != connectivity.Ready {
        return fmt.Errorf("connection not ready: %s", state)
    }

    // Validate TEE capabilities
    if err := c.teeBridge.ValidatePlatforms(ctx); err != nil {
        return fmt.Errorf("TEE validation failed: %w", err)
    }

    return nil
}

func (c *NodeClient) Close() error {
    var errs []error

    // Close gRPC connection
    if err := c.conn.Close(); err != nil {
        errs = append(errs, fmt.Errorf("failed to close gRPC connection: %w", err))
    }

    // Close TEE bridge
    if err := c.teeBridge.Close(); err != nil {
        errs = append(errs, fmt.Errorf("failed to close TEE bridge: %w", err))
    }

    if len(errs) > 0 {
        return fmt.Errorf("multiple close errors: %v", errs)
    }
    return nil
}