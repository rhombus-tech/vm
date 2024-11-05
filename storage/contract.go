package storage

import (
    "context"
    "errors"
    "github.com/ava-labs/hypersdk/state"
    "github.com/ava-labs/hypersdk/codec"
)

const contractPrefix byte = 0x4

var (
    ErrContractNotFound = errors.New("contract not found")
)

func ContractKey(hash []byte) []byte {
    k := make([]byte, 1+len(hash))
    k[0] = contractPrefix
    copy(k[1:], hash)
    return k
}

func StoreContract(
    ctx context.Context,
    mu state.Mutable,
    code []byte,
    checksum []byte,
) error {
    key := ContractKey(checksum)
    return mu.Insert(ctx, key, code)
}

func GetContract(
    ctx context.Context,
    im state.Immutable,
    checksum []byte,
) ([]byte, error) {
    key := ContractKey(checksum)
    return im.GetValue(ctx, key)
}
