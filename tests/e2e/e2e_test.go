// Copyright (C) 2024, Ava Labs, Inc. All rights reserved.
// See the file LICENSE for licensing terms.

package e2e_test

import (
    "encoding/json"
    "testing"

    "github.com/ava-labs/avalanchego/tests/fixture/e2e"
    "github.com/stretchr/testify/require"

    "github.com/rhombus-tech/vm/consts"
    "github.com/rhombus-tech/vm/tests/workload"
    "github.com/rhombus-tech/vm/throughput"
    "github.com/rhombus-tech/vm/actions"
    "github.com/ava-labs/hypersdk/abi"
    "github.com/ava-labs/hypersdk/auth"
    "github.com/ava-labs/hypersdk/codec"
    "github.com/ava-labs/hypersdk/chain"
    "github.com/ava-labs/hypersdk/genesis"
    "github.com/ava-labs/hypersdk/tests/fixture"

    he2e "github.com/ava-labs/hypersdk/tests/e2e"
    ginkgo "github.com/onsi/ginkgo/v2"
)

const owner = "morpheusvm-e2e-tests"

var (
    flagVars *e2e.FlagVars
    
    actionParser *codec.TypeParser[chain.Action]
    outputParser *codec.TypeParser[codec.Typed]
    authParser   *codec.TypeParser[chain.Auth]
)

func TestE2e(t *testing.T) {
    ginkgo.RunSpecs(t, "morpheusvm e2e test suites")
}

func init() {
    flagVars = e2e.RegisterFlags()

    // Initialize parsers
    actionParser = codec.NewTypeParser[chain.Action]()
    outputParser = codec.NewTypeParser[codec.Typed]()
    authParser = codec.NewTypeParser[chain.Auth]()

    // Register action types
    actionParser.Register(&actions.CreateObjectAction{}, nil)
    actionParser.Register(&actions.SendEventAction{}, nil)
    actionParser.Register(&actions.SetInputObjectAction{}, nil)
    actionParser.Register(&actions.CreateRegionAction{}, nil)
    actionParser.Register(&actions.UpdateRegionAction{}, nil)

    // Register output types
    outputParser.Register(&actions.CreateObjectResult{}, nil)
    outputParser.Register(&actions.SendEventResult{}, nil)
    outputParser.Register(&actions.SetInputObjectResult{}, nil)
    outputParser.Register(&actions.CreateRegionResult{}, nil)
    outputParser.Register(&actions.UpdateRegionResult{}, nil)

    // Register auth types
    authParser.Register(&auth.ED25519{}, auth.UnmarshalED25519)
    authParser.Register(&auth.SECP256R1{}, auth.UnmarshalSECP256R1)
    authParser.Register(&auth.BLS{}, auth.UnmarshalBLS)
}

// Parser implements chain.Parser interface
type Parser struct {
    genesis *genesis.DefaultGenesis
    actionParser *codec.TypeParser[chain.Action]
    outputParser *codec.TypeParser[codec.Typed]
    authParser   *codec.TypeParser[chain.Auth]
}

func (p *Parser) Rules(_ int64) chain.Rules {
    return p.genesis.Rules
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

// CreateParser function for genesis
func CreateParser(genesisBytes []byte) (chain.Parser, error) {
    var gen genesis.DefaultGenesis
    if err := json.Unmarshal(genesisBytes, &gen); err != nil {
        return nil, err
    }
    return &Parser{
        genesis:      &gen,
        actionParser: actionParser,
        outputParser: outputParser,
        authParser:   authParser,
    }, nil
}

var _ = ginkgo.SynchronizedBeforeSuite(func() []byte {
    require := require.New(ginkgo.GinkgoT())

    gen, workloadFactory, spamKey, err := workload.New(100 /* minBlockGap: 100ms */)
    require.NoError(err)

    genesisBytes, err := json.Marshal(gen)
    require.NoError(err)

    expectedABI, err := abi.NewABI(actionParser.GetRegisteredTypes(), outputParser.GetRegisteredTypes())
    require.NoError(err)

    parser, err := CreateParser(genesisBytes)
    require.NoError(err)

    spamHelper := throughput.SpamHelper{
        KeyType: auth.ED25519Key,
    }

    tc := e2e.NewTestContext()
    he2e.SetWorkload(consts.Name, workloadFactory, expectedABI, parser, &spamHelper, spamKey)

    return fixture.NewTestEnvironment(tc, flagVars, owner, consts.Name, consts.ID, genesisBytes).Marshal()
}, func(envBytes []byte) {
    e2e.InitSharedTestEnvironment(ginkgo.GinkgoT(), envBytes)
})