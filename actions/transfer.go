// Copyright (C) 2024, Ava Labs, Inc. All rights reserved.
// See the file LICENSE for licensing terms.

package actions

import (
    "context"
    "errors"

    "github.com/ava-labs/avalanchego/ids"
    "github.com/ava-labs/hypersdk/chain"
    "github.com/ava-labs/hypersdk/codec"
    "github.com/ava-labs/hypersdk/state"

    "github.com/rhombus-tech/vm/storage"
    "github.com/rhombus-tech/vm/consts"
)

const (
    TransferComputeUnits = 1
    MaxMemoSize          = 256
)

var (
    ErrOutputValueZero    = errors.New("value is zero")
    ErrOutputMemoTooLarge = errors.New("memo is too large")
    _          chain.Action = (*Transfer)(nil)
)

type Transfer struct {
    To    codec.Address `serialize:"true" json:"to"`
    Value uint64       `serialize:"true" json:"value"`
    Memo  []byte       `serialize:"true" json:"memo"`
}

func (*Transfer) GetTypeID() uint8 {
    return consts.TransferID
}

// Updated StateKeys method signature to remove the ids.ID parameter
func (t *Transfer) StateKeys(actor codec.Address) state.Keys {
    return state.Keys{
        string(storage.BalanceKey(actor)): state.Read | state.Write,
        string(storage.BalanceKey(t.To)):  state.All,
    }
}

func (t *Transfer) Execute(
    ctx context.Context,
    _ chain.Rules,
    mu state.Mutable,
    _ int64,
    actor codec.Address,
    _ ids.ID,
) (codec.Typed, error) {
    if t.Value == 0 {
        return nil, ErrOutputValueZero
    }
    if len(t.Memo) > MaxMemoSize {
        return nil, ErrOutputMemoTooLarge
    }
    senderBalance, err := storage.SubBalance(ctx, mu, actor, t.Value)
    if err != nil {
        return nil, err
    }
    receiverBalance, err := storage.AddBalance(ctx, mu, t.To, t.Value)
    if err != nil {
        return nil, err
    }

    return &TransferResult{
        SenderBalance:   senderBalance,
        ReceiverBalance: receiverBalance,
    }, nil
}

func (*Transfer) ComputeUnits(chain.Rules) uint64 {
    return TransferComputeUnits
}

func (*Transfer) ValidRange(chain.Rules) (int64, int64) {
    return -1, -1
}

type TransferResult struct {
    SenderBalance   uint64 `serialize:"true" json:"sender_balance"`
    ReceiverBalance uint64 `serialize:"true" json:"receiver_balance"`
}

func (*TransferResult) GetTypeID() uint8 {
    return consts.TransferID
}