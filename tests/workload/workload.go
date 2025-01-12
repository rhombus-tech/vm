// tests/workload/workload.go
package workload

import (
	"context"
	"errors"
	"math"
	"time"

	"github.com/ava-labs/avalanchego/ids"
	"github.com/ava-labs/hypersdk/api/indexer"
	"github.com/ava-labs/hypersdk/api/jsonrpc"
	"github.com/ava-labs/hypersdk/auth"
	"github.com/ava-labs/hypersdk/chain"
	"github.com/ava-labs/hypersdk/codec"
	"github.com/ava-labs/hypersdk/crypto/bls"
	"github.com/ava-labs/hypersdk/crypto/ed25519"
	"github.com/ava-labs/hypersdk/crypto/secp256r1"
	"github.com/ava-labs/hypersdk/fees"
	"github.com/ava-labs/hypersdk/genesis"
	sdkworkload "github.com/ava-labs/hypersdk/tests/workload"
	"github.com/stretchr/testify/require"

	"github.com/rhombus-tech/vm/actions"
	"github.com/rhombus-tech/vm/consts"
)

const (
    initialBalance  uint64 = 10_000_000_000_000
    txCheckInterval        = 100 * time.Millisecond
)

var (
    ed25519HexKeys = []string{
        "323b1d8f4eed5f0da9da93071b034f2dce9d2d22692c172f3cb252a64ddfafd01b057de320297c29ad0c1f589ea216869cf1938d88c9fbd70d6748323dbf2fa7",
        "8a7be2e0c9a2d09ac2861c34326d6fe5a461d920ba9c2b345ae28e603d517df148735063f8d5d8ba79ea4668358943e5c80bc09e9b2b9a15b5b15db6c1862e88",
    }
    ed25519PrivKeys      = make([]ed25519.PrivateKey, len(ed25519HexKeys))
    ed25519Addrs         = make([]codec.Address, len(ed25519HexKeys))
    ed25519AuthFactories = make([]*auth.ED25519Factory, len(ed25519HexKeys))
)

// Verify interfaces are implemented correctly
var (
    _ sdkworkload.TxWorkloadFactory = (*workloadFactory)(nil)
    _ sdkworkload.TxWorkloadIterator = (*simpleTxWorkload)(nil)
    _ sdkworkload.TxWorkloadIterator = (*mixedAuthWorkload)(nil)
)

func init() {
    for i, keyHex := range ed25519HexKeys {
        privBytes, err := codec.LoadHex(keyHex, ed25519.PrivateKeyLen)
        if err != nil {
            panic(err)
        }
        priv := ed25519.PrivateKey(privBytes)
        ed25519PrivKeys[i] = priv
        ed25519AuthFactories[i] = auth.NewED25519Factory(priv)
        addr := auth.NewED25519Address(priv.PublicKey())
        ed25519Addrs[i] = addr
    }
}

type workloadFactory struct {
    factories []*auth.ED25519Factory
    addrs     []codec.Address
}

type BalanceRequest struct {
    Address codec.Address `json:"address"`
}

type BalanceResponse struct {
    Amount uint64 `json:"amount"`
}

func New(minBlockGap int64) (*genesis.DefaultGenesis, sdkworkload.TxWorkloadFactory, *auth.PrivateKey, error) {
    customAllocs := make([]*genesis.CustomAllocation, 0, len(ed25519Addrs))
    for _, prefundedAddr := range ed25519Addrs {
        customAllocs = append(customAllocs, &genesis.CustomAllocation{
            Address: prefundedAddr,
            Balance: initialBalance,
        })
    }

    spamKey := &auth.PrivateKey{
        Address: ed25519Addrs[0],
        Bytes:   ed25519PrivKeys[0][:],
    }

    gen := genesis.NewDefaultGenesis(customAllocs)
    gen.Rules.WindowTargetUnits = fees.Dimensions{
        math.MaxUint64,
        math.MaxUint64,
        math.MaxUint64,
        math.MaxUint64,
        math.MaxUint64,
    }
    gen.Rules.MaxBlockUnits = fees.Dimensions{
        1800000,
        math.MaxUint64,
        math.MaxUint64,
        math.MaxUint64,
        math.MaxUint64,
    }
    gen.Rules.MinBlockGap = minBlockGap

    return gen, &workloadFactory{
        factories: ed25519AuthFactories,
        addrs:     ed25519Addrs,
    }, spamKey, nil
}

type JSONRPCClient struct {
    requester *jsonrpc.JSONRPCClient
    parser    chain.Parser
    chainID   ids.ID
}

func NewJSONRPCClient(uri string) *JSONRPCClient {
    return &JSONRPCClient{
        requester: jsonrpc.NewJSONRPCClient(uri),
        chainID:   ids.Empty,
    }
}

func (c *JSONRPCClient) ExecuteActions(
    ctx context.Context,
    actions []chain.Action,
) ([][]byte, error) {
    return c.requester.ExecuteActions(ctx, codec.EmptyAddress, actions)
}


func (c *JSONRPCClient) Parser(ctx context.Context) (chain.Parser, error) {
    if c.parser == nil {
        actionParser := codec.NewTypeParser[chain.Action]()
        actionParser.Register(&actions.CreateObjectAction{}, nil)
        actionParser.Register(&actions.SendEventAction{}, nil)
        actionParser.Register(&actions.SetInputObjectAction{}, nil)
        actionParser.Register(&actions.Transfer{}, nil)

        outputParser := codec.NewTypeParser[codec.Typed]()
        outputParser.Register(&actions.CreateObjectResult{}, nil)
        outputParser.Register(&actions.SendEventResult{}, nil)
        outputParser.Register(&actions.SetInputObjectResult{}, nil)
        outputParser.Register(&actions.TransferResult{}, nil)

        authParser := codec.NewTypeParser[chain.Auth]()
        authParser.Register(&auth.ED25519{}, auth.UnmarshalED25519)
        authParser.Register(&auth.SECP256R1{}, auth.UnmarshalSECP256R1)
        authParser.Register(&auth.BLS{}, auth.UnmarshalBLS)

        c.parser = &Parser{
            actionParser: actionParser,
            outputParser: outputParser,
            authParser:   authParser,
        }
    }
    return c.parser, nil
}


func (c *JSONRPCClient) Balance(ctx context.Context, addr codec.Address) (uint64, error) {
    // Use ExecuteActions for balance query
    results, err := c.requester.ExecuteActions(
        ctx,
        addr,
        []chain.Action{
            &actions.Transfer{
                To:    addr,
                Value: 0, // Zero value for query
            },
        },
    )
    if err != nil {
        return 0, err
    }
    
    if len(results) == 0 {
        return 0, errors.New("no results returned")
    }
    
    // Parse balance from result
    reader := codec.NewReader(results[0], len(results[0]))
    balance := reader.UnpackUint64(false)
    if reader.Err() != nil {
        return 0, reader.Err()
    }
    
    return balance, nil
}


func (c *JSONRPCClient) Network(ctx context.Context) (uint32, uint32, ids.ID, error) {
    networkID, subnetID, chainID, err := c.requester.Network(ctx)
    if err != nil {
        return 0, 0, ids.Empty, err
    }
    return networkID, uint32(subnetID[0]), chainID, nil // Access the first byte if needed
}

func (c *JSONRPCClient) GenerateTransactionManual(
    parser chain.Parser,
    actions []chain.Action,
    auth chain.AuthFactory,
    maxUnits uint64,
) (uint64, *chain.Transaction, error) {
    submitFunc, tx, err := c.requester.GenerateTransactionManual(
        parser,
        actions,
        auth,
        maxUnits,
    )
    if err != nil {
        return 0, nil, err
    }

    // Ignore submitFunc as we don't need it here
    _ = submitFunc

    return maxUnits, tx, nil
}


func (f *workloadFactory) NewSizedTxWorkload(uri string, size int) (sdkworkload.TxWorkloadIterator, error) {
    return &simpleTxWorkload{
        factory: f.factories[0],
        cli:     jsonrpc.NewJSONRPCClient(uri),
        lcli:    NewJSONRPCClient(uri),
        size:    size,
    }, nil
}

type simpleTxWorkload struct {
    factory *auth.ED25519Factory
    cli     *jsonrpc.JSONRPCClient
    lcli    *JSONRPCClient
    count   int
    size    int
}

func (g *simpleTxWorkload) Next() bool {
    return g.count < g.size
}

func (g *simpleTxWorkload) GenerateTx(ctx context.Context) (*chain.Transaction, error) {
    tx, _, err := g.GenerateTxWithAssertion(ctx)
    return tx, err
}

func (g *simpleTxWorkload) GenerateTxWithAssertion(ctx context.Context) (*chain.Transaction, sdkworkload.TxAssertion, error) {
    g.count++
    other, err := ed25519.GeneratePrivateKey()
    if err != nil {
        return nil, nil, err
    }

    aother := auth.NewED25519Address(other.PublicKey())
    parser, err := g.lcli.Parser(ctx) // Now using the exported Parser method
    if err != nil {
        return nil, nil, err
    }

    tx, err := generateTransaction(
        ctx,
        g.lcli.chainID,
        []chain.Action{&actions.Transfer{
            To:    aother,
            Value: 1,
        }},
        g.factory,
        parser.ActionCodec(),
        parser.AuthCodec(),
    )
    if err != nil {
        return nil, nil, err
    }

    assertion := func(ctx context.Context, require *require.Assertions, uri string) {
        confirmTx(ctx, require, uri, tx.ID(), aother, 1)
    }

    return tx, assertion, nil
}

func generateTransaction(
    ctx context.Context,
    chainID ids.ID,
    actions []chain.Action,
    authFactory chain.AuthFactory,
    actionParser *codec.TypeParser[chain.Action],
    authParser *codec.TypeParser[chain.Auth],
) (*chain.Transaction, error) {
    client := jsonrpc.NewJSONRPCClient("")
    
    parser := &Parser{
        actionParser: actionParser,
        outputParser: codec.NewTypeParser[codec.Typed](),
        authParser:   authParser,
    }
    
    _, tx, err := client.GenerateTransactionManual(
        parser,
        actions,
        authFactory,
        0,
    )
    if err != nil {
        return nil, err
    }
    
    if err := tx.Verify(ctx); err != nil {
        return nil, err
    }

    return tx, nil
}

func (f *workloadFactory) NewWorkloads(uri string) ([]sdkworkload.TxWorkloadIterator, error) {
    blsPriv, err := bls.GeneratePrivateKey()
    if err != nil {
        return nil, err
    }
    blsPub := bls.PublicFromPrivateKey(blsPriv)
    blsAddr := auth.NewBLSAddress(blsPub)
    blsFactory := auth.NewBLSFactory(blsPriv)

    secpPriv, err := secp256r1.GeneratePrivateKey()
    if err != nil {
        return nil, err
    }
    secpPub := secpPriv.PublicKey()
    secpAddr := auth.NewSECP256R1Address(secpPub)
    secpFactory := auth.NewSECP256R1Factory(secpPriv)

    cli := jsonrpc.NewJSONRPCClient(uri)
    networkID, _, blockchainID, err := cli.Network(context.Background())
    if err != nil {
        return nil, err
    }

    lcli := NewJSONRPCClient(uri)
    lcli.chainID = blockchainID

    generator := &mixedAuthWorkload{
        addressAndFactories: []addressAndFactory{
            {address: f.addrs[1], authFactory: f.factories[1]},
            {address: blsAddr, authFactory: blsFactory},
            {address: secpAddr, authFactory: secpFactory},
        },
        balance:   initialBalance,
        cli:       cli,
        lcli:      lcli,
        networkID: networkID,
        chainID:   blockchainID,
    }

    return []sdkworkload.TxWorkloadIterator{generator}, nil
}

type addressAndFactory struct {
    address     codec.Address
    authFactory chain.AuthFactory
}

type mixedAuthWorkload struct {
    addressAndFactories []addressAndFactory
    balance            uint64
    cli               *jsonrpc.JSONRPCClient
    lcli              *JSONRPCClient
    networkID         uint32
    chainID           ids.ID
    count             int
}

func (g *mixedAuthWorkload) Next() bool {
    return g.count < len(g.addressAndFactories)-1
}

func (g *mixedAuthWorkload) GenerateTx(ctx context.Context) (*chain.Transaction, error) {
    tx, _, err := g.GenerateTxWithAssertion(ctx)
    return tx, err
}

func (g *mixedAuthWorkload) GenerateTxWithAssertion(ctx context.Context) (*chain.Transaction, sdkworkload.TxAssertion, error) {
    defer func() { g.count++ }()

    sender := g.addressAndFactories[g.count]
    receiver := g.addressAndFactories[g.count+1]
    expectedBalance := g.balance - 1_000_000

    parser, err := g.lcli.Parser(ctx)
    if err != nil {
        return nil, nil, err
    }

    tx, err := generateTransaction(
        ctx,
        g.chainID,
        []chain.Action{&actions.Transfer{
            To:    receiver.address,
            Value: expectedBalance,
        }},
        sender.authFactory,
        parser.ActionCodec(),
        parser.AuthCodec(),
    )
    if err != nil {
        return nil, nil, err
    }
    g.balance = expectedBalance

    assertion := func(ctx context.Context, require *require.Assertions, uri string) {
        confirmTx(ctx, require, uri, tx.ID(), receiver.address, expectedBalance)
    }

    return tx, assertion, nil
}

func confirmTx(ctx context.Context, require *require.Assertions, uri string, txID ids.ID, receiverAddr codec.Address, receiverExpectedBalance uint64) {
    indexerCli := indexer.NewClient(uri)
    success, _, err := indexerCli.WaitForTransaction(ctx, txCheckInterval, txID)
    require.NoError(err)
    require.True(success)

    lcli := NewJSONRPCClient(uri)
    balance, err := lcli.Balance(ctx, receiverAddr)
    require.NoError(err)
    require.Equal(receiverExpectedBalance, balance)

    txRes, _, err := indexerCli.GetTx(ctx, txID)
    require.NoError(err)
    require.NotZero(txRes.Fee)
    require.Len(txRes.Outputs, 1)

    transferOutputBytes := []byte(txRes.Outputs[0])
    require.Equal(consts.TransferID, transferOutputBytes[0])

    parser, err := lcli.Parser(ctx)
    require.NoError(err)

    reader := codec.NewReader(transferOutputBytes, len(transferOutputBytes))
    transferOutputTyped, err := parser.OutputCodec().Unmarshal(reader)
    require.NoError(err)

    transferOutput, ok := transferOutputTyped.(*actions.TransferResult)
    require.True(ok)
    require.Equal(receiverExpectedBalance, transferOutput.ReceiverBalance)
}

type Parser struct {
    actionParser *codec.TypeParser[chain.Action]
    outputParser *codec.TypeParser[codec.Typed]
    authParser   *codec.TypeParser[chain.Auth]
    rules        chain.Rules
}

func (p *Parser) Rules(_ int64) chain.Rules {
    return p.rules
}

func (p *Parser) ActionCodec() *codec.TypeParser[chain.Action] {
    return p.actionParser
}

func (p *Parser) OutputCodec() *codec.TypeParser[codec.Typed] {
    return p.outputParser
}

func (p *Parser) AuthCodec() *codec.TypeParser[chain.Auth] {
    return p.authParser
}