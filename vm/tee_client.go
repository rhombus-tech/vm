// Copyright (C) 2024, Ava Labs, Inc. All rights reserved.
// See the file LICENSE for licensing terms.

package vm

import (
	"context"
	"fmt"

	"github.com/rhombus-tech/vm/compute"
	"github.com/rhombus-tech/vm/tee/proto"
	"google.golang.org/grpc"
	"google.golang.org/grpc/connectivity"
)

// TEEType redeclared as uint8 to match existing constants
type TEEType = uint8

// TEEClient handles communication with a TEE endpoint
type TEEClient struct {
    client  proto.TeeExecutionClient
    conn    *grpc.ClientConn
    teeType TEEType
}

// NewTEEClient creates a new client connection to a TEE endpoint
func NewTEEClient(endpoint string, teeType TEEType) (*TEEClient, error) {
    conn, err := grpc.Dial(endpoint, grpc.WithInsecure())
    if err != nil {
        return nil, fmt.Errorf("failed to dial TEE endpoint: %w", err)
    }

    client := proto.NewTeeExecutionClient(conn)

    return &TEEClient{
        client:  client,
        conn:    conn,
        teeType: teeType,
    }, nil
}

// Close closes the client connection
func (c *TEEClient) Close() error {
    if c.conn != nil {
        return c.conn.Close()
    }
    return nil
}

// GetType returns the TEE type of this client
func (c *TEEClient) GetType() TEEType {
    return c.teeType
}

// Execute executes code in the TEE
func (c *TEEClient) Execute(ctx context.Context, req *proto.ExecutionRequest) (*proto.ExecutionResult, error) {
    resp, err := c.client.Execute(ctx, req)
    if err != nil {
        return nil, fmt.Errorf("TEE execution failed: %w", err)
    }
    return resp, nil
}

func (vm *ShuttleVM) InitializeTEEPairs(ctx context.Context) error {
    regions := vm.regionManager.ListRegions()

    for _, regionID := range regions {
        pairs, err := vm.regionManager.GetTEEPairs(regionID)
        if err != nil {
            return err
        }

        for _, pair := range pairs {
            // Create SGX client
            sgxConfig := compute.NodeClientConfig{
                Endpoint:       pair.SGXEndpoint,
                ControllerPath: vm.config.ControllerPath,
                WasmPath:      vm.config.WasmPath,
            }
            sgxClient, err := compute.NewNodeClient(sgxConfig)
            if err != nil {
                return fmt.Errorf("failed to create SGX client: %w", err)
            }

            // Create SEV client
            sevConfig := compute.NodeClientConfig{
                Endpoint:       pair.SEVEndpoint,
                ControllerPath: vm.config.ControllerPath,
                WasmPath:      vm.config.WasmPath,
            }
            sevClient, err := compute.NewNodeClient(sevConfig)
            if err != nil {
                sgxClient.Close()
                return fmt.Errorf("failed to create SEV client: %w", err)
            }

            vm.computeNodes[pair.SGXEndpoint] = sgxClient
            vm.computeNodes[pair.SEVEndpoint] = sevClient
        }
    }
    return nil
}

// Implement necessary interface methods
func (c *TEEClient) ValidateConnection(ctx context.Context) error {
    if c.conn.GetState() != connectivity.Ready {
        return fmt.Errorf("connection not ready: %s", c.conn.GetState())
    }
    return nil
}