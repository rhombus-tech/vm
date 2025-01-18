// Copyright (C) 2024, Ava Labs, Inc. All rights reserved.
// See the file LICENSE for licensing terms.

package vm

import (
    "context"
    "fmt"

    "google.golang.org/grpc"
    "github.com/rhombus-tech/vm/tee/proto"
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
func (c *TEEClient) Execute(req *proto.ExecutionRequest) (*proto.ExecutionResult, error) {
    ctx := context.Background()
    resp, err := c.client.Execute(ctx, req)
    if err != nil {
        return nil, fmt.Errorf("TEE execution failed: %w", err)
    }
    return resp, nil
}