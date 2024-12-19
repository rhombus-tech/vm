// Copyright (C) 2024, Ava Labs, Inc. All rights reserved.
// See the file LICENSE for licensing terms.
package storage

import (
    "context"
    "fmt"

    "github.com/ava-labs/hypersdk/chain"
    "github.com/ava-labs/hypersdk/codec"
    "github.com/ava-labs/hypersdk/state"
    
    "github.com/rhombus-tech/vm/actions"
)

const (
    ObjectPrefix = "object:"
    EventPrefix  = "event:"
    InputObject  = "input_object"
)

var _ (chain.StateManager) = (*StateManager)(nil)

type StateManager struct{}

// Existing methods
func (*StateManager) HeightKey() []byte {
    return HeightKey()
}

func (*StateManager) TimestampKey() []byte {
    return TimestampKey()
}

func (*StateManager) FeeKey() []byte {
    return FeeKey()
}

func (*StateManager) SponsorStateKeys(addr codec.Address) state.Keys {
    return state.Keys{
        string(BalanceKey(addr)): state.Read | state.Write,
    }
}

func (*StateManager) CanDeduct(
    ctx context.Context,
    addr codec.Address,
    im state.Immutable,
    amount uint64,
) error {
    bal, err := GetBalance(ctx, im, addr)
    if err != nil {
        return err
    }
    if bal < amount {
        return ErrInvalidBalance
    }
    return nil
}

func (*StateManager) Deduct(
    ctx context.Context,
    addr codec.Address,
    mu state.Mutable,
    amount uint64,
) error {
    _, err := SubBalance(ctx, mu, addr, amount)
    return err
}

func (*StateManager) AddBalance(
    ctx context.Context,
    addr codec.Address,
    mu state.Mutable,
    amount uint64,
    createAccount bool,
) error {
    _, err := AddBalance(ctx, mu, addr, amount, createAccount)
    return err
}

// GetObject retrieves an object from state
func (*StateManager) GetObject(ctx context.Context, mu state.Immutable, id string) (map[string][]byte, error) {
    key := []byte(ObjectPrefix + id)
    objBytes, err := mu.GetValue(ctx, key)
    if err != nil {
        return nil, err
    }
    if objBytes == nil {
        return nil, nil
    }

    var obj map[string][]byte
    if err := codec.Unmarshal(objBytes, &obj); err != nil {
        return nil, err
    }

    return obj, nil
}

// SetObject stores an object in state
func (*StateManager) SetObject(ctx context.Context, mu state.Mutable, id string, obj map[string][]byte) error {
    key := []byte(ObjectPrefix + id)
    objBytes, err := codec.Marshal(obj)
    if err != nil {
        return err
    }

    return mu.SetValue(ctx, key, objBytes)
}

// QueueEvent adds an event to the state
func (*StateManager) QueueEvent(ctx context.Context, mu state.Mutable, event *actions.SendEventAction) error {
    key := []byte(fmt.Sprintf("%s%s:%s", EventPrefix, roughtime.Now(), event.IDTo))
    
    eventData := map[string]interface{}{
        "function_call": event.FunctionCall,
        "parameters":    event.Parameters,
    }

    eventBytes, err := codec.Marshal(eventData)
    if err != nil {
        return err
    }

    return mu.SetValue(ctx, key, eventBytes)
}

// GetInputObject retrieves the current input object ID
func (*StateManager) GetInputObject(ctx context.Context, im state.Immutable) (string, error) {
    inputBytes, err := im.GetValue(ctx, []byte(InputObject))
    if err != nil {
        return "", err
    }
    if inputBytes == nil {
        return "", nil
    }
    return string(inputBytes), nil
}

// SetInputObject sets the current input object ID
func (*StateManager) SetInputObject(ctx context.Context, mu state.Mutable, id string) error {
    return mu.SetValue(ctx, []byte(InputObject), []byte(id))
}

// Helper functions for object state management
func (*StateManager) ObjectExists(ctx context.Context, im state.Immutable, id string) (bool, error) {
    key := []byte(ObjectPrefix + id)
    return im.HasValue(ctx, key)
}

// Additional state keys for ShuttleVM actions
func (*StateManager) GetShuttleStateKeys(id string) state.Keys {
    keys := state.Keys{
        string([]byte(ObjectPrefix + id)): state.Read | state.Write,
        string([]byte(EventPrefix + id)): state.Read | state.Write,
        string([]byte(InputObject)): state.Read | state.Write,
    }
    return keys
}
