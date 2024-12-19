// Copyright (C) 2024, Ava Labs, Inc. All rights reserved.
// See the file LICENSE for licensing terms.
package storage

import (
    "context"
    "encoding/binary"
    "errors"
    "fmt"

    "github.com/ava-labs/avalanchego/database"
    "github.com/ava-labs/hypersdk/codec"
    "github.com/ava-labs/hypersdk/consts"
    "github.com/ava-labs/hypersdk/state"
    smath "github.com/ava-labs/avalanchego/utils/math"
)

type ReadState func(context.Context, [][]byte) ([][]byte, []error)

// State
// / (height) => store in root
//   -> [heightPrefix] => height
// 0x0/ (balance)
//   -> [owner] => balance
// 0x1/ (hypersdk-height)
// 0x2/ (hypersdk-timestamp)
// 0x3/ (hypersdk-fee)
// 0x4/ (object)
//   -> [id] => object
// 0x5/ (event)
//   -> [priority][id] => event
// 0x6/ (input)
//   -> input object id

const (
    // Active state
    balancePrefix   = 0x0
    heightPrefix    = 0x1
    timestampPrefix = 0x2
    feePrefix       = 0x3

    // ShuttleVM state
    objectPrefix    = 0x4
    eventPrefix     = 0x5
    inputPrefix     = 0x6
)

const BalanceChunks uint16 = 1

var (
    heightKey    = []byte{heightPrefix}
    timestampKey = []byte{timestampPrefix}
    feeKey      = []byte{feePrefix}
)

// [balancePrefix] + [address]
func BalanceKey(addr codec.Address) (k []byte) {
    k = make([]byte, 1+codec.AddressLen+consts.Uint16Len)
    k[0] = balancePrefix
    copy(k[1:], addr[:])
    binary.BigEndian.PutUint16(k[1+codec.AddressLen:], BalanceChunks)
    return
}

// If locked is 0, then account does not exist
func GetBalance(
    ctx context.Context,
    im state.Immutable,
    addr codec.Address,
) (uint64, error) {
    _, bal, _, err := getBalance(ctx, im, addr)
    return bal, err
}

func getBalance(
    ctx context.Context,
    im state.Immutable,
    addr codec.Address,
) ([]byte, uint64, bool, error) {
    k := BalanceKey(addr)
    bal, exists, err := innerGetBalance(im.GetValue(ctx, k))
    return k, bal, exists, err
}

// Used to serve RPC queries
func GetBalanceFromState(
    ctx context.Context,
    f ReadState,
    addr codec.Address,
) (uint64, error) {
    k := BalanceKey(addr)
    values, errs := f(ctx, [][]byte{k})
    bal, _, err := innerGetBalance(values[0], errs[0])
    return bal, err
}

func innerGetBalance(
    v []byte,
    err error,
) (uint64, bool, error) {
    if errors.Is(err, database.ErrNotFound) {
        return 0, false, nil
    }
    if err != nil {
        return 0, false, err
    }
    val, err := database.ParseUInt64(v)
    if err != nil {
        return 0, false, err
    }
    return val, true, nil
}

func SetBalance(
    ctx context.Context,
    mu state.Mutable,
    addr codec.Address,
    balance uint64,
) error {
    k := BalanceKey(addr)
    return setBalance(ctx, mu, k, balance)
}

func setBalance(
    ctx context.Context,
    mu state.Mutable,
    key []byte,
    balance uint64,
) error {
    return mu.Insert(ctx, key, binary.BigEndian.AppendUint64(nil, balance))
}

func AddBalance(
    ctx context.Context,
    mu state.Mutable,
    addr codec.Address,
    amount uint64,
    create bool,
) (uint64, error) {
    key, bal, exists, err := getBalance(ctx, mu, addr)
    if err != nil {
        return 0, err
    }
    if !exists && !create {
        return 0, nil
    }
    nbal, err := smath.Add(bal, amount)
    if err != nil {
        return 0, fmt.Errorf(
            "%w: could not add balance (bal=%d, addr=%v, amount=%d)",
            ErrInvalidBalance,
            bal,
            addr,
            amount,
        )
    }
    return nbal, setBalance(ctx, mu, key, nbal)
}

func SubBalance(
    ctx context.Context,
    mu state.Mutable,
    addr codec.Address,
    amount uint64,
) (uint64, error) {
    key, bal, ok, err := getBalance(ctx, mu, addr)
    if !ok {
        return 0, ErrInvalidAddress
    }
    if err != nil {
        return 0, err
    }
    nbal, err := smath.Sub(bal, amount)
    if err != nil {
        return 0, fmt.Errorf(
            "%w: could not subtract balance (bal=%d, addr=%v, amount=%d)",
            ErrInvalidBalance,
            bal,
            addr,
            amount,
        )
    }
    if nbal == 0 {
        return 0, mu.Remove(ctx, key)
    }
    return nbal, setBalance(ctx, mu, key, nbal)
}

func HeightKey() (k []byte) {
    return heightKey
}

func TimestampKey() (k []byte) {
    return timestampKey
}

func FeeKey() (k []byte) {
    return feeKey
}

// ShuttleVM Storage Functions

func ObjectKey(id string) []byte {
    k := make([]byte, 1+len(id))
    k[0] = objectPrefix
    copy(k[1:], []byte(id))
    return k
}

func EventKey(priority uint64, id string) []byte {
    k := make([]byte, 1+consts.Uint64Len+len(id))
    k[0] = eventPrefix
    binary.BigEndian.PutUint64(k[1:], priority)
    copy(k[1+consts.Uint64Len:], []byte(id))
    return k
}

func InputObjectKey() []byte {
    return []byte{inputPrefix}
}

func GetObject(
    ctx context.Context,
    im state.Immutable,
    id string,
) (map[string][]byte, error) {
    k := ObjectKey(id)
    v, err := im.GetValue(ctx, k)
    if errors.Is(err, database.ErrNotFound) {
        return nil, nil
    }
    if err != nil {
        return nil, err
    }

    var obj map[string][]byte
    if err := codec.Unmarshal(v, &obj); err != nil {
        return nil, err
    }
    return obj, nil
}

func SetObject(
    ctx context.Context,
    mu state.Mutable,
    id string,
    obj map[string][]byte,
) error {
    k := ObjectKey(id)
    v, err := codec.Marshal(obj)
    if err != nil {
        return err
    }
    return mu.Insert(ctx, k, v)
}

func DeleteObject(
    ctx context.Context,
    mu state.Mutable,
    id string,
) error {
    k := ObjectKey(id)
    return mu.Remove(ctx, k)
}

func QueueEvent(
    ctx context.Context,
    mu state.Mutable,
    priority uint64,
    id string,
    functionCall string,
    parameters []byte,
) error {
    k := EventKey(priority, id)
    event := map[string]interface{}{
        "function_call": functionCall,
        "parameters":    parameters,
    }
    v, err := codec.Marshal(event)
    if err != nil {
        return err
    }
    return mu.Insert(ctx, k, v)
}

func GetInputObject(
    ctx context.Context,
    im state.Immutable,
) (string, error) {
    k := InputObjectKey()
    v, err := im.GetValue(ctx, k)
    if errors.Is(err, database.ErrNotFound) {
        return "", nil
    }
    if err != nil {
        return "", err
    }
    return string(v), nil
}

func SetInputObject(
    ctx context.Context,
    mu state.Mutable,
    id string,
) error {
    k := InputObjectKey()
    return mu.Insert(ctx, k, []byte(id))
}
