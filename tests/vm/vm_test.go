// tests/vm/vm_test.go
package vm_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net/http"
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

type healthResponse struct {
	Checks  map[string]interface{} `json:"checks"`
	Healthy bool                   `json:"healthy"`
}

// Less strict health check
func checkHealth(endpoint string) error {
	resp, err := http.Get(fmt.Sprintf("http://%s/ext/health", endpoint))
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	body, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return err
	}

	// Just check if we can reach the endpoint
	var health healthResponse
	if err := json.Unmarshal(body, &health); err != nil {
		return err
	}

	// Consider it healthy if we can at least reach it
	if resp.StatusCode == http.StatusOK {
		return nil
	}

	return fmt.Errorf("health check failed: %s", string(body))
}

func NewTestVM(t *testing.T) *TestVM {
	endpoints := []string{
		"devnet:9650", // Try Docker network first
	}

	opts := []grpc.DialOption{
		grpc.WithInsecure(),
		grpc.WithBlock(),
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
	var err error
	var connectedEndpoint string

	// Try each endpoint
	for _, endpoint := range endpoints {
		t.Logf("Attempting to connect to %s...", endpoint)

		// Check basic connectivity first
		if err := checkHealth(endpoint); err != nil {
			t.Logf("Health check failed for %s: %v", endpoint, err)
			continue
		}

		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		conn, err = grpc.DialContext(ctx, endpoint, opts...)
		cancel()

		if err == nil {
			connectedEndpoint = endpoint
			break
		}
		t.Logf("Failed to connect to %s: %v", endpoint, err)
	}

	if conn == nil || err != nil {
		t.Logf("Failed to connect to any endpoint: %v", err)
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
	// Set longer timeout for the entire test
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	vm := NewTestVM(t)
	if vm == nil {
		t.Fatal("Failed to create TestVM")
		return
	}
	defer vm.conn.Close()

	// Wait for connection with timeout
	t.Log("Waiting for gRPC connection to be ready...")
	if err := vm.waitForConnection(ctx); err != nil {
		t.Fatalf("Failed to establish connection: %v", err)
	}

	// Simple ping test first
	t.Run("Ping", func(t *testing.T) {
		err := vm.ping(ctx)
		require.NoError(t, err)
	})

	// Test getting regions
	t.Run("GetRegions", func(t *testing.T) {
		err := vm.testGetRegions(ctx)
		require.NoError(t, err)
	})

	// Test execution only if previous tests pass
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