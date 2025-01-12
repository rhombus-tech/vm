// tests/integration/integration_test.go
package integration_test

import (
    "context"
    "encoding/json"
    "testing"

    "github.com/stretchr/testify/require"
    "github.com/ava-labs/hypersdk/chain"
    "github.com/ava-labs/hypersdk/tests/integration"
    "github.com/ava-labs/hypersdk/auth"
    "github.com/ava-labs/hypersdk/crypto/ed25519"

    "github.com/rhombus-tech/vm"
    "github.com/rhombus-tech/vm/actions"
    "github.com/rhombus-tech/vm/consts"
    "github.com/rhombus-tech/vm/compute"
    morpheusWorkload "github.com/rhombus-tech/vm/tests/workload"

    ginkgo "github.com/onsi/ginkgo/v2"
)

func TestIntegration(t *testing.T) {
    ginkgo.RunSpecs(t, "morpheusvm integration test suites")
}

var _ = ginkgo.BeforeSuite(func() {
    require := require.New(ginkgo.GinkgoT())
    
    // Generate test keys
    randomEd25519Priv, err := ed25519.GeneratePrivateKey()
    require.NoError(err)
    randomEd25519AuthFactory := auth.NewED25519Factory(randomEd25519Priv)

    // Create genesis and workload
    genesis, workloadFactory, _, err := morpheusWorkload.New(0 /* minBlockGap: 0ms */)
    require.NoError(err)

    genesisBytes, err := json.Marshal(genesis)
    require.NoError(err)

    // Create custom VM config for testing
    vmConfig := &vm.Config{
        NetworkID: 0,
        ChainID: "testChain",
        ComputeNodeEndpoints: map[string]compute.NodeClientConfig{
            "test-region": {
                Endpoint: "localhost:50051",
                ControllerPath: "/usr/local/bin/tee-controller",
                WasmPath: "/usr/local/bin/tee-wasm-module.wasm",
            },
        },
    }

    // Setup imports the integration test coverage
    integration.Setup(
        // VM constructor function
        func(ctx context.Context, opts ...chain.Option) (chain.VM, error) {
            return vm.New(vmConfig)
        },
        genesisBytes,
        consts.ID,
        // Parser creator function
        vm.CreateParser,
        workloadFactory,
        randomEd25519AuthFactory,
    )
})

func TestRegionalTEE(t *testing.T) {
    require := require.New(t)
    
    // Create VM config for testing
    vmConfig := &vm.Config{
        NetworkID: 0,
        ChainID: "testChain",
        ComputeNodeEndpoints: map[string]compute.NodeClientConfig{
            "test-region": {
                Endpoint: "localhost:50051",
                ControllerPath: "/usr/local/bin/tee-controller",
                WasmPath: "/usr/local/bin/tee-wasm-module.wasm",
            },
        },
    }

    // Create VM instance
    vm, err := vm.New(vmConfig)
    require.NoError(err)

    // Test region creation
    regionID := "test-region"
    err = vm.RegisterRegion(context.Background(), vm.RegionConfig{
        ID: regionID,
        SGXEndpoint: "localhost:50051",
        SEVEndpoint: "localhost:50052",
    })
    require.NoError(err)

    // Test regional execution
    action := &actions.SendEventAction{
        RegionID: regionID,
        IDTo: "test-object",
        FunctionCall: "test",
        Parameters: []byte("test"),
    }
    
    result, err := vm.ExecuteInRegion(context.Background(), regionID, action)
    require.NoError(err)
    require.NotNil(result)
    require.Len(result.Attestations, 2)
}