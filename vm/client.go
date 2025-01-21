// Copyright (C) 2024, Ava Labs, Inc. All rights reserved.
// See the file LICENSE for licensing terms.

package vm

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/ava-labs/hypersdk/api/jsonrpc"
	"github.com/ava-labs/hypersdk/chain"
	"github.com/ava-labs/hypersdk/codec"
	"github.com/ava-labs/hypersdk/genesis"
	"github.com/ava-labs/hypersdk/requester"
	"github.com/ava-labs/hypersdk/utils"
	"github.com/rhombus-tech/vm/consts"
	"github.com/rhombus-tech/vm/regions"
	"github.com/rhombus-tech/vm/storage"
	"github.com/rhombus-tech/vm/tee/proto"
)

const balanceCheckInterval = 500 * time.Millisecond

type JSONRPCClient struct {
    requester *requester.EndpointRequester
    g         *genesis.DefaultGenesis
    regionTEEs map[string]struct {
        sgxClient proto.TeeExecutionClient
        sevClient proto.TeeExecutionClient
    }
}

type Object struct {
    Code        []byte    `json:"code"`
    Storage     []byte    `json:"storage"`
    Status      string    `json:"status"`
    LastUpdated time.Time `json:"last_updated"`
}

// EnclaveInfo type for RPC responses
type EnclaveInfo struct {
    Measurement []byte    `json:"measurement"`
    ValidFrom   time.Time `json:"valid_from"`
    ValidUntil  time.Time `json:"valid_until"`
    EnclaveType string    `json:"enclave_type"`
    RegionID    string    `json:"region_id"`
    Status      string    `json:"status"`
}

type RegionResponse struct {
    ID                string   `json:"id"`
    CreatedAt         string   `json:"created_at"`
    WorkerIds         []string `json:"worker_ids"`
    SupportedTeeTypes []string `json:"supported_tee_types"`
    MaxTasks         uint32   `json:"max_tasks"`
}



// NewJSONRPCClient creates a new client object.
func NewJSONRPCClient(uri string) *JSONRPCClient {
	uri = strings.TrimSuffix(uri, "/")
	uri += JSONRPCEndpoint
	req := requester.New(uri, consts.Name)
	return &JSONRPCClient{
        requester: req,
        g:         nil,
        regionTEEs: make(map[string]struct {
            sgxClient proto.TeeExecutionClient
            sevClient proto.TeeExecutionClient
        }),
    }
}

func (cli *JSONRPCClient) Genesis(ctx context.Context) (*genesis.DefaultGenesis, error) {
	if cli.g != nil {
		return cli.g, nil
	}

	resp := new(GenesisReply)
	err := cli.requester.SendRequest(
		ctx,
		"genesis",
		nil,
		resp,
	)
	if err != nil {
		return nil, err
	}
	cli.g = resp.Genesis
	return resp.Genesis, nil
}

func (cli *JSONRPCClient) Balance(ctx context.Context, addr codec.Address) (uint64, error) {
	resp := new(BalanceReply)
	err := cli.requester.SendRequest(
		ctx,
		"balance",
		&BalanceArgs{
			Address: addr,
		},
		resp,
	)
	return resp.Amount, err
}

func (cli *JSONRPCClient) WaitForBalance(
	ctx context.Context,
	addr codec.Address,
	min uint64,
) error {
	return jsonrpc.Wait(ctx, balanceCheckInterval, func(ctx context.Context) (bool, error) {
		balance, err := cli.Balance(ctx, addr)
		if err != nil {
			return false, err
		}
		shouldExit := balance >= min
		if !shouldExit {
			utils.Outf(
				"{{yellow}}waiting for %s balance: %s{{/}}\n",
				utils.FormatBalance(min),
				addr,
			)
		}
		return shouldExit, nil
	})
}

func (cli *JSONRPCClient) Parser(ctx context.Context) (chain.Parser, error) {
	g, err := cli.Genesis(ctx)
	if err != nil {
		return nil, err
	}
	return NewParser(g), nil
}

var _ chain.Parser = (*Parser)(nil)

type Parser struct {
	genesis *genesis.DefaultGenesis
}

func (p *Parser) Rules(_ int64) chain.Rules {
	return p.genesis.Rules
}

func (*Parser) ActionCodec() *codec.TypeParser[chain.Action] {
	return ActionParser
}

func (*Parser) OutputCodec() *codec.TypeParser[codec.Typed] {
	return OutputParser
}

func (*Parser) AuthCodec() *codec.TypeParser[chain.Auth] {
	return AuthParser
}

// Add region validation to action execution
func (p *Parser) StateManager() chain.StateManager {
    // Create region-aware state manager
    return &storage.StateManager{} 
}

func NewParser(genesis *genesis.DefaultGenesis) chain.Parser {
	return &Parser{genesis: genesis}
}

// Used as a lambda function for creating ExternalSubscriberServer parser
func CreateParser(genesisBytes []byte) (chain.Parser, error) {
	var genesis genesis.DefaultGenesis
	if err := json.Unmarshal(genesisBytes, &genesis); err != nil {
		return nil, err
	}
	return NewParser(&genesis), nil
}

func (cli *JSONRPCClient) GetRegionAttestations(
    ctx context.Context,
    regionID string,
) ([]*proto.TEEAttestation, error) {
    resp := new(struct {
        Attestations []*proto.TEEAttestation `json:"attestations"`
    })
    err := cli.requester.SendRequest(
        ctx,
        "region.attestations",
        &struct {
            RegionID string `json:"region_id"`
        }{
            RegionID: regionID,
        },
        resp,
    )
    return resp.Attestations, err
}

func (cli *JSONRPCClient) GetRegions(
    ctx context.Context,
) (*proto.GetRegionsResponse, error) {  // Changed return type
    resp := new(proto.GetRegionsResponse)
    err := cli.requester.SendRequest(
        ctx,
        "region.list",
        nil,
        resp,
    )
    return resp, err
}

func (c *JSONRPCClient) GetRegionHealth(ctx context.Context, regionID string) (*regions.HealthStatus, error) {
    resp := new(struct {
        Health *regions.HealthStatus `json:"health"`
    })
    err := c.requester.SendRequest(
        ctx,
        "region.health",
        &struct {
            RegionID string `json:"region_id"`
        }{
            RegionID: regionID,
        },
        resp,
    )
    return resp.Health, err
}


func (c *JSONRPCClient) GetRegionMetrics(ctx context.Context, regionID, pairID string) (*regions.TEEPairMetrics, error) {
    resp := new(struct {
        Metrics *regions.TEEPairMetrics `json:"metrics"`
    })
    err := c.requester.SendRequest(
        ctx,
        "region.metrics",
        &struct {
            RegionID string `json:"region_id"`
            PairID   string `json:"pair_id"`
        }{
            RegionID: regionID,
            PairID:   pairID,
        },
        resp,
    )
    return resp.Metrics, err
}
