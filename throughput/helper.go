// Copyright (C) 2024, Ava Labs, Inc. All rights reserved.
// See the file LICENSE for licensing terms.
package throughput

import (
   "context"
   "github.com/ava-labs/hypersdk-starter-kit/actions"
   "github.com/ava-labs/hypersdk-starter-kit/vm"
   "github.com/ava-labs/hypersdk/api/ws"
   "github.com/ava-labs/hypersdk/auth"
   "github.com/ava-labs/hypersdk/chain"
   "github.com/ava-labs/hypersdk/codec"
   "github.com/ava-labs/hypersdk/pubsub"
   "github.com/ava-labs/hypersdk/throughput"
   "github.com/cloudflare/roughtime"
   mauth "github.com/ava-labs/hypersdk-starter-kit/auth"
)

type SpamHelper struct {
   KeyType     string
   RegionID    string              // Added for regional testing
   cli         *vm.JSONRPCClient
   ws          *ws.WebSocketClient
   teePairs    map[string][2][]byte // Map of region to TEE pair IDs
}

var _ throughput.SpamHelper = &SpamHelper{}

func (sh *SpamHelper) CreateAccount() (*auth.PrivateKey, error) {
   return mauth.GeneratePrivateKey(sh.KeyType)
}

func (sh *SpamHelper) CreateClient(uri string) error {
   sh.cli = vm.NewJSONRPCClient(uri)
   ws, err := ws.NewWebSocketClient(uri, ws.DefaultHandshakeTimeout, pubsub.MaxPendingMessages, pubsub.MaxReadMessageSize)
   if err != nil {
       return err
   }
   sh.ws = ws
   return nil
}

func (sh *SpamHelper) GetParser(ctx context.Context) (chain.Parser, error) {
   return sh.cli.Parser(ctx)
}

func (sh *SpamHelper) LookupBalance(address codec.Address) (uint64, error) {
   balance, err := sh.cli.Balance(context.TODO(), address)
   if err != nil {
       return 0, err
   }
   return balance, err
}

// CreateTestAttestation creates a test attestation for throughput testing
func (sh *SpamHelper) CreateTestAttestation(data []byte) [2]actions.TEEAttestation {
   timestamp := roughtime.Now()
   teePair := sh.teePairs[sh.RegionID]

   return [2]actions.TEEAttestation{
       {
           EnclaveID:   teePair[0],
           Measurement: []byte("test-measurement-1"),
           Timestamp:   timestamp,
           Data:        data,
           Signature:   []byte("test-signature-1"),
       },
       {
           EnclaveID:   teePair[1],
           Measurement: []byte("test-measurement-2"),
           Timestamp:   timestamp,
           Data:        data,
           Signature:   []byte("test-signature-2"),
       },
   }
}

// GetRegionalEvent creates a test event for regional throughput testing
func (sh *SpamHelper) GetRegionalEvent(targetID string, functionCall string, params []byte) []chain.Action {
   attestations := sh.CreateTestAttestation(params)
   return []chain.Action{&actions.SendEventAction{
       IDTo:         targetID,
       FunctionCall: functionCall,
       Parameters:   params,
       Attestations: attestations,
   }}
}

func (*SpamHelper) GetTransfer(address codec.Address, amount uint64, memo []byte) []chain.Action {
   return []chain.Action{&actions.Transfer{
       To:    address,
       Value: amount,
       Memo:  memo,
   }}
}

// SetRegion sets the region for testing
func (sh *SpamHelper) SetRegion(regionID string, teePair [2][]byte) {
   sh.RegionID = regionID
   sh.teePairs[regionID] = teePair
}

// NewSpamHelper creates a new SpamHelper with TEE support
func NewSpamHelper(keyType string) *SpamHelper {
   return &SpamHelper{
       KeyType:  keyType,
       teePairs: make(map[string][2][]byte),
   }
}
