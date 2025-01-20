// Copyright (C) 2024, Ava Labs, Inc. All rights reserved.
// See the file LICENSE for licensing terms.

package cmd

import (
	"context"
	"encoding/json"

	hyperjsonrpc "github.com/ava-labs/hypersdk/api/jsonrpc"
	"github.com/ava-labs/hypersdk/api/ws"
	"github.com/ava-labs/hypersdk/auth"
	"github.com/ava-labs/hypersdk/chain"
	"github.com/ava-labs/hypersdk/cli"
	"github.com/ava-labs/hypersdk/codec"
	"github.com/ava-labs/hypersdk/crypto/ed25519"
	"github.com/ava-labs/hypersdk/pubsub"
	"github.com/ava-labs/hypersdk/utils" // Add this alias
	apirpc "github.com/rhombus-tech/vm/api/jsonrpc"
	"github.com/rhombus-tech/vm/consts"
	"github.com/rhombus-tech/vm/vm"
)

var _ cli.Controller = (*Controller)(nil)

type Handler struct {
    h *cli.Handler
}

func NewHandler(h *cli.Handler) *Handler {
	return &Handler{h}
}

func (h *Handler) Root() *cli.Handler {
	return h.h
}

func (h *Handler) DefaultActor() (
    priv *auth.PrivateKey,
    factory chain.AuthFactory,
    standardClient *hyperjsonrpc.JSONRPCClient,  // Add back HyperSDK client
    apiClient *apirpc.JSONRPCClient,
    wsClient *ws.WebSocketClient,
    err error,
) {
    addr, privKey, err := h.h.GetDefaultKey(true)
    if err != nil {
        return nil, nil, nil, nil, nil, err
    }

    var authFactory chain.AuthFactory
    switch addr[0] {
    case auth.ED25519ID:
        authFactory = auth.NewED25519Factory(ed25519.PrivateKey(privKey))
    }

    _, uris, err := h.h.GetDefaultChain(true)
    if err != nil {
        return nil, nil, nil, nil, nil, err
    }

    // Create both clients
    standardClient = hyperjsonrpc.NewJSONRPCClient(uris[0])
    apiClient = apirpc.NewJSONRPCClient(uris[0])

    wsClient, err = ws.NewWebSocketClient(uris[0], ws.DefaultHandshakeTimeout, pubsub.MaxPendingMessages, pubsub.MaxReadMessageSize)
    if err != nil {
        return nil, nil, nil, nil, nil, err
    }

    return &auth.PrivateKey{
        Address: addr,
        Bytes:   privKey,
    }, authFactory, standardClient, apiClient, wsClient, nil
}

func (h *Handler) GetBalance(
    ctx context.Context,
    addr codec.Address,
    cli *apirpc.JSONRPCClient, // Use our API client
) (uint64, error) {
    balance, err := cli.Balance(ctx, addr)
    if err != nil {
        return 0, err
    }
    if balance == 0 {
        utils.Outf("{{red}}balance:{{/}} 0 %s\n", consts.Symbol)
        utils.Outf("{{red}}please send funds to %s{{/}}\n", addr)
        utils.Outf("{{red}}exiting...{{/}}\n")
        return 0, nil
    }
    utils.Outf(
        "{{yellow}}balance:{{/}} %s %s\n",
        utils.FormatBalance(balance),
        consts.Symbol,
    )
    return balance, nil
}


type Controller struct {
	databasePath string
}

func NewController(databasePath string) *Controller {
	return &Controller{databasePath}
}

func (c *Controller) DatabasePath() string {
	return c.databasePath
}

func (*Controller) Symbol() string {
	return consts.Symbol
}

func (*Controller) GetParser(uri string) (chain.Parser, error) {
	cli := vm.NewJSONRPCClient(uri)
	return cli.Parser(context.TODO())
}

func (*Controller) HandleTx(tx *chain.Transaction, result *chain.Result) {
	handleTx(tx, result)
}

func (*Controller) LookupBalance(address codec.Address, uri string) (uint64, error) {
	cli := vm.NewJSONRPCClient(uri)
	balance, err := cli.Balance(context.TODO(), address)
	return balance, err
}

func (h *Handler) RegionHealth(regionID string) error {
    ctx := context.Background()
    _, _, _, apiClient, _, err := h.DefaultActor()
    if err != nil {
        return err
    }
    
    health, err := apiClient.GetRegionHealth(ctx, regionID)
    if err != nil {
        return err
    }
    
    utils.Outf("{{green}}Region Health Status:{{/}}\n")
    s, err := json.MarshalIndent(health, "", "  ")
    if err != nil {
        return err
    }
    utils.Outf("%s\n", string(s))
    return nil
}

func (h *Handler) RegionMetrics(regionID, pairID string) error {
    ctx := context.Background()
    _, _, _, apiClient, _, err := h.DefaultActor()
    if err != nil {
        return err
    }
    
    metrics, err := apiClient.GetRegionMetrics(ctx, regionID, pairID)
    if err != nil {
        return err
    }
    
    utils.Outf("{{green}}Region Metrics:{{/}}\n")
    s, err := json.MarshalIndent(metrics, "", "  ")
    if err != nil {
        return err
    }
    utils.Outf("%s\n", string(s))
    return nil
}
