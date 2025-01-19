// tests/vm/vm_test.go
package vm_test

import (
	"context"
	"fmt"
	"io/ioutil"
	"net"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/connectivity"
	"google.golang.org/grpc/keepalive"

	"github.com/rhombus-tech/vm/core"
	"github.com/rhombus-tech/vm/tee/proto"
	"github.com/rhombus-tech/vm/verifier"
)

// TestVM struct stays the same
type TestVM struct {
    t        *testing.T
    client   proto.TeeExecutionClient
    conn     *grpc.ClientConn
    verifier *verifier.StateVerifier
}

func NewTestVM(t *testing.T) *TestVM {
    // Get endpoint from environment variable or use default list
    testEndpoint := os.Getenv("TEST_VM_ENDPOINT")
    endpoints := []string{
        testEndpoint,
        "localhost:9650",
        "127.0.0.1:9650",
        "devnet:9650",
        "host.docker.internal:9650",
        "172.17.0.1:9650",
    }

    // Filter out empty endpoints
    var validEndpoints []string
    for _, ep := range endpoints {
        if ep != "" {
            validEndpoints = append(validEndpoints, ep)
        }
    }

    opts := []grpc.DialOption{
        grpc.WithInsecure(),
        grpc.WithBlock(),
        grpc.WithTimeout(5 * time.Second),
        grpc.WithDefaultCallOptions(
            grpc.MaxCallRecvMsgSize(16 * 1024 * 1024),
            grpc.MaxCallSendMsgSize(16 * 1024 * 1024),
        ),
        grpc.WithKeepaliveParams(keepalive.ClientParameters{
            Time:                10 * time.Second,
            Timeout:             5 * time.Second,
            PermitWithoutStream: true,
        }),
    }

    var conn *grpc.ClientConn
    var connectedEndpoint string

    // Try each endpoint
    for _, endpoint := range validEndpoints {
        t.Logf("Testing endpoint: %s", endpoint)
        
        // Try TCP connection first to check basic connectivity
        tcpConn, tcpErr := net.DialTimeout("tcp", endpoint, 5*time.Second)
        if tcpErr != nil {
            t.Logf("TCP connection failed: %v", tcpErr)
            continue
        }
        tcpConn.Close()
        t.Logf("TCP connection successful to %s", endpoint)

        // Try HTTP health check
        healthURL := fmt.Sprintf("http://%s/ext/health", endpoint)
        resp, httpErr := http.Post(
            healthURL,
            "application/json",
            strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"health.health"}`),
        )
        if httpErr != nil {
            t.Logf("Health check failed: %v", httpErr)
        } else {
            body, _ := ioutil.ReadAll(resp.Body)
            resp.Body.Close()
            t.Logf("Health check response: %s", string(body))
        }

        // Try gRPC connection
        ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
        var err error
        conn, err = grpc.DialContext(ctx, endpoint, opts...)
        cancel()

        if err == nil {
            // Check if connection is actually ready
            state := conn.GetState()
            t.Logf("gRPC connection state: %s", state)
            
            if state == connectivity.Ready {
                // Try a simple RPC call
                client := proto.NewTeeExecutionClient(conn)
                ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
                _, rpcErr := client.GetRegions(ctx, &proto.GetRegionsRequest{})
                cancel()
                
                if rpcErr != nil {
                    t.Logf("GetRegions RPC failed: %v", rpcErr)
                    conn.Close()
                    continue
                }

                connectedEndpoint = endpoint
                break
            }
            conn.Close()
            continue
        }

        t.Logf("Failed to connect to %s: %v", endpoint, err)
    }

    if conn == nil || connectedEndpoint == "" {
        t.Skip("No available endpoints to test against. Set TEST_VM_ENDPOINT environment variable or ensure local node is running.")
        return nil
    }

    t.Logf("Successfully connected to %s", connectedEndpoint)

    client := proto.NewTeeExecutionClient(conn)
    verifier := verifier.New(nil)

    return &TestVM{
        t:        t,
        client:   client,
        conn:     conn,
        verifier: verifier,
    }
}

func TestVMOperations(t *testing.T) {
    if testing.Short() {
        t.Skip("Skipping integration test in short mode")
    }

    ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
    defer cancel()

    vm := NewTestVM(t)
    if vm == nil {
        t.Skip("TestVM creation skipped - no available endpoints")
        return
    }
    defer vm.conn.Close()

    // Run subtests only if we have a connection
    t.Run("Ping", func(t *testing.T) {
        err := vm.ping(ctx)
        if err != nil {
            t.Skipf("Ping failed, skipping further tests: %v", err)
            return
        }
        require.NoError(t, err)
    })

    t.Run("GetRegions", func(t *testing.T) {
        err := vm.testGetRegions(ctx)
        require.NoError(t, err)
    })

    t.Run("ExecuteInRegion", func(t *testing.T) {
        err := vm.testExecution(ctx)
        require.NoError(t, err)
    })
}


func (vm *TestVM) waitForConnection(ctx context.Context) error {
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		state := vm.conn.GetState()
		vm.t.Logf("Connection state: %s", state)
		
		if state == connectivity.Ready {
			return nil
		}
		
		if !vm.conn.WaitForStateChange(ctx, state) {
			return fmt.Errorf("connection state didn't change, current state: %s", state)
		}
	}
	return fmt.Errorf("connection timeout after 10 seconds")
}

// Simple ping test
func (vm *TestVM) ping(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	req := &proto.GetRegionsRequest{}
	_, err := vm.client.GetRegions(ctx, req)
	return err
}

func (vm *TestVM) testGetRegions(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	vm.t.Log("Testing GetRegions...")
	regions, err := vm.client.GetRegions(ctx, &proto.GetRegionsRequest{})
	if err != nil {
		return fmt.Errorf("GetRegions failed: %w", err)
	}

	require.NotNil(vm.t, regions)
	require.NotNil(vm.t, regions.Regions)
	return nil
}

func (vm *TestVM) testExecution(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	vm.t.Log("Testing execution...")
	execReq := &proto.ExecutionRequest{
		IdTo:         "test-object",
		FunctionCall: "test-function",
		Parameters:   []byte("test-params"),
		RegionId:     "test-region",
	}

	result, err := vm.client.Execute(ctx, execReq)
	if err != nil {
		return fmt.Errorf("Execute failed: %w", err)
	}

	require.NotNil(vm.t, result)
	require.NotEmpty(vm.t, result.StateHash)
	require.Len(vm.t, result.Attestations, 2)

	attestations := [2]core.TEEAttestation{
		{
			EnclaveID:   result.Attestations[0].EnclaveId,
			Measurement: result.Attestations[0].Measurement,
			Timestamp:   time.Now(),
			Data:        result.Attestations[0].Data,
			RegionProof: result.Attestations[0].RegionProof,
		},
		{
			EnclaveID:   result.Attestations[1].EnclaveId,
			Measurement: result.Attestations[1].Measurement,
			Timestamp:   time.Now(),
			Data:        result.Attestations[1].Data,
			RegionProof: result.Attestations[1].RegionProof,
		},
	}

	if err := vm.verifier.VerifyAttestationPair(ctx, attestations, nil); err != nil {
		return fmt.Errorf("attestation verification failed: %w", err)
	}

	return nil
}