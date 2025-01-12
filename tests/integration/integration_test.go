// tests/integration/integration_test.go
package integration_test

import (
    "context"
    "encoding/json"
    "testing"
    "time"

    "github.com/ava-labs/avalanchego/snow"
    "github.com/ava-labs/avalanchego/utils/block"
    "github.com/ava-labs/hypersdk/chain"
    "github.com/ava-labs/hypersdk/codec"
    "github.com/ava-labs/hypersdk/tests/integration"
    "github.com/ava-labs/hypersdk/auth"
    "github.com/ava-labs/hypersdk/crypto/ed25519"
    "github.com/ava-labs/hypersdk/state"
    "github.com/stretchr/testify/require"
    ginkgo "github.com/onsi/ginkgo/v2"

    "github.com/rhombus-tech/vm/actions"
    "github.com/rhombus-tech/vm/consts"
    "github.com/rhombus-tech/vm/compute"
    "github.com/rhombus-tech/vm/core"
    "github.com/rhombus-tech/vm/tee"
    "github.com/rhombus-tech/vm/verifier"
    morpheusWorkload "github.com/rhombus-tech/vm/tests/workload"
)

type mockTEE struct {
    attestations [2]core.TEEAttestation
    results     map[string]*core.ExecutionResult
}

func newMockTEE() *mockTEE {
    now := time.Now()
    attestations := [2]core.TEEAttestation{
        {
            EnclaveID:   []byte("sgx-test"),
            Measurement: []byte("sgx-measurement"),
            Timestamp:   now,
            Data:        []byte("sgx-data"),
            RegionProof: []byte("region-1"),
            Signature:   []byte("sgx-sig"),
        },
        {
            EnclaveID:   []byte("sev-test"),
            Measurement: []byte("sev-measurement"),
            Timestamp:   now,
            Data:        []byte("sev-data"),
            RegionProof: []byte("region-1"),
            Signature:   []byte("sev-sig"),
        },
    }

    return &mockTEE{
        attestations: attestations,
        results:     make(map[string]*core.ExecutionResult),
    }
}

func (m *mockTEE) Execute(_ context.Context, input []byte) (*core.ExecutionResult, error) {
    result := &core.ExecutionResult{
        Output:       []byte("test result"),
        StateHash:    []byte("test hash"),
        Attestations: m.attestations,
        Timestamp:    time.Now(),
    }
    
    resultID := string(input)
    m.results[resultID] = result
    
    return result, nil
}

type MockVM struct {
    config     *compute.Config
    state      state.Mutable
    teeClient  *tee.Client
    mockTEE    *mockTEE
    parser     chain.Parser
}

func (vm *MockVM) Initialize(
    ctx context.Context,
    snowCtx *snow.Context,
    state state.Mutable,
    _ []byte,
    _ []byte,
    _ []byte,
    _ chan<- error,
    _ chan<- error,
    _ []*chain.StatelessBlock,
) (chain.Rules, error) {
    vm.state = state
    return chain.NewDefaultRules(), nil
}

func (vm *MockVM) Accepted(_ context.Context, _ *chain.StatefulBlock) {}
func (vm *MockVM) Rejected(_ context.Context, _ *chain.StatefulBlock) {}
func (vm *MockVM) Shutdown(_ context.Context) error { return nil }
func (vm *MockVM) Rules(_ context.Context, _ int64) chain.Rules { return chain.NewDefaultRules() }
func (vm *MockVM) AcceptedSyncableBlock(_ context.Context, _ *chain.SyncableBlock) (block.StateSyncMode, error) {
    return block.StateSyncStatic, nil
}
func (vm *MockVM) RejectedSyncableBlock(_ context.Context, _ *chain.SyncableBlock) error { return nil }
func (vm *MockVM) VerifyState(_ context.Context, _ chain.Rules, _ *chain.StatefulBlock) error { return nil }
func (vm *MockVM) StateManager() chain.StateManager { return nil }
func (vm *MockVM) Parser() chain.Parser { return vm.parser }

type TestParser struct {
    actionParser *codec.TypeParser[chain.Action]
    outputParser *codec.TypeParser[codec.Typed]
    authParser   *codec.TypeParser[chain.Auth]
}

func NewTestParser() *TestParser {
    actionParser := codec.NewTypeParser[chain.Action]()
    outputParser := codec.NewTypeParser[codec.Typed]()
    authParser := codec.NewTypeParser[chain.Auth]()

    // Register actions
    actionParser.Register(&actions.CreateObjectAction{}, actions.UnmarshalCreateObject)
    actionParser.Register(&actions.SendEventAction{}, actions.UnmarshalSendEvent)
    actionParser.Register(&actions.SetInputObjectAction{}, actions.UnmarshalSetInputObject)

    // Register auth types
    authParser.Register(&auth.ED25519{}, auth.UnmarshalED25519)

    return &TestParser{
        actionParser: actionParser,
        outputParser: outputParser,
        authParser:   authParser,
    }
}

func (p *TestParser) Rules(_ int64) chain.Rules {
    return chain.NewDefaultRules()
}

func (p *TestParser) ActionCodec() *codec.TypeParser[chain.Action] {
    return p.actionParser
}

func (p *TestParser) OutputCodec() *codec.TypeParser[codec.Typed] {
    return p.outputParser
}

func (p *TestParser) AuthCodec() *codec.TypeParser[chain.Auth] {
    return p.authParser
}

func CreateParser(_ []byte) (chain.Parser, error) {
    return NewTestParser(), nil
}

func NewMockVM(config *compute.Config) (*MockVM, error) {
    mockTee := newMockTEE()
    stateVerifier := verifier.New(nil)
    
    teeClient, err := tee.NewClient("mock://sgx", "mock://sev", stateVerifier)
    if err != nil {
        return nil, err
    }

    parser := NewTestParser()

    return &MockVM{
        config:    config,
        mockTEE:   mockTee,
        teeClient: teeClient,
        parser:    parser,
    }, nil
}

func (vm *MockVM) RegisterRegion(ctx context.Context, regionID, sgxEndpoint, sevEndpoint string) error {
    return vm.teeClient.AddRegion(regionID, sgxEndpoint, sevEndpoint)
}

func (vm *MockVM) ExecuteInRegion(ctx context.Context, regionID string, action chain.Action) (*core.ExecutionResult, error) {
    return vm.mockTEE.Execute(ctx, []byte("test"))
}

func setupTestEnvironment(t *testing.T) (*MockVM, string) {
    require := require.New(t)
    
    config := &compute.Config{
        MaxTasks: 100,
        Debug:    false,
    }

    testVM, err := NewMockVM(config)
    require.NoError(err)

    regionID := "test-region"
    err = testVM.RegisterRegion(context.Background(), regionID, "mock://sgx", "mock://sev")
    require.NoError(err)

    return testVM, regionID
}

func TestIntegration(t *testing.T) {
    ginkgo.RunSpecs(t, "morpheusvm integration test suites")
}

var _ = ginkgo.BeforeSuite(func() {
    require := require.New(ginkgo.GinkgoT())
    
    randomEd25519Priv, err := ed25519.GeneratePrivateKey()
    require.NoError(err)
    randomEd25519AuthFactory := auth.NewED25519Factory(randomEd25519Priv)

    genesis := chain.NewDefaultGenesis()
    workloadFactory := morpheusWorkload.NewWorkloadFactory()

    genesisBytes, err := json.Marshal(genesis)
    require.NoError(err)

    vmConstructor := func(_ context.Context, options ...chain.Option) (chain.VM, error) {
        config := &compute.Config{
            MaxTasks: 100,
            Debug:    false,
        }
        return NewMockVM(config)
    }

    integration.Setup(
        vmConstructor,
        genesisBytes,
        consts.ID,
        CreateParser,
        workloadFactory,
        randomEd25519AuthFactory,
    )
})

func TestRegionalTEE(t *testing.T) {
    testVM, regionID := setupTestEnvironment(t)
    require := require.New(t)

    createAction := &actions.CreateObjectAction{
        ID:       "test-object",
        Code:     []byte("test code"),
        Storage:  []byte("test storage"),
        RegionID: regionID,
    }
    
    result, err := testVM.ExecuteInRegion(context.Background(), regionID, createAction)
    require.NoError(err)
    require.NotNil(result)

    eventAction := &actions.SendEventAction{
        RegionID:     regionID,
        IDTo:         "test-object",
        FunctionCall: "test",
        Parameters:   []byte("test params"),
    }
    
    result, err = testVM.ExecuteInRegion(context.Background(), regionID, eventAction)
    require.NoError(err)
    require.NotNil(result)

    require.Len(result.Attestations, 2)
    require.Equal([]byte("sgx-test"), result.Attestations[0].EnclaveID)
    require.Equal([]byte("sev-test"), result.Attestations[1].EnclaveID)
}

func TestRegionManagement(t *testing.T) {
    testVM, _ := setupTestEnvironment(t)
    require := require.New(t)

    newRegionID := "new-region"
    err := testVM.RegisterRegion(context.Background(), newRegionID, "mock://sgx2", "mock://sev2")
    require.NoError(err)

    err = testVM.RegisterRegion(context.Background(), newRegionID, "mock://sgx2", "mock://sev2")
    require.Error(err)
}