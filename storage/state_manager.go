// Copyright (C) 2024, Ava Labs, Inc. All rights reserved.
// See the file LICENSE for licensing terms.
package storage

import (
    "context"
    "fmt"
    "time"

    "github.com/ava-labs/hypersdk/chain"
    "github.com/ava-labs/hypersdk/codec"
    "github.com/ava-labs/hypersdk/state"
    "github.com/ava-labs/hypersdk/coordination"
    
    "github.com/rhombus-tech/vm/actions"
    "github.com/rhombus-tech/vm/tee"  // Add TEE package
)

const (
    ObjectPrefix = "object:"
    EventPrefix  = "event:"
    InputObject  = "input_object"
    RegionPrefix = "region:"
)

var _ (chain.StateManager) = (*StateManager)(nil)

type StateManager struct {
    coordinator *coordination.Coordinator
    teeClient   *tee.Client  // Add TEE client
}

// NewStateManager creates a new state manager with TEE support
func NewStateManager(coord *coordination.Coordinator, teeEndpoint string) (*StateManager, error) {
    teeClient, err := tee.NewClient(teeEndpoint)
    if err != nil {
        return nil, fmt.Errorf("failed to create TEE client: %w", err)
    }

    return &StateManager{
        coordinator: coord,
        teeClient:   teeClient,
    }, nil
}

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
    // Use attestation timestamp instead of calling roughtime directly
    key := []byte(fmt.Sprintf("%s%s:%s", EventPrefix, event.Attestations[0].Timestamp, event.IDTo))
    
    eventData := map[string]interface{}{
        "function_call": event.FunctionCall,
        "parameters":    event.Parameters,
        "attestations": event.Attestations,
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

// GetRegion retrieves a region from state
func (*StateManager) GetRegion(ctx context.Context, im state.Immutable, id string) (map[string]interface{}, error) {
    key := []byte(RegionPrefix + id)
    regionBytes, err := im.GetValue(ctx, key)
    if err != nil {
        return nil, err
    }
    if regionBytes == nil {
        return nil, nil
    }

    var region map[string]interface{}
    if err := codec.Unmarshal(regionBytes, &region); err != nil {
        return nil, err
    }
    return region, nil
}

// SetRegion stores a region in state
func (*StateManager) SetRegion(ctx context.Context, mu state.Mutable, id string, region map[string]interface{}) error {
    key := []byte(RegionPrefix + id)
    regionBytes, err := codec.Marshal(region)
    if err != nil {
        return err
    }
    return mu.SetValue(ctx, key, regionBytes)
}

// RegionExists checks if a region exists
func (*StateManager) RegionExists(ctx context.Context, im state.Immutable, id string) (bool, error) {
    key := []byte(RegionPrefix + id)
    return im.HasValue(ctx, key)
}

func (sm *StateManager) VerifyStateTransition(ctx context.Context, action chain.Action) error {
    switch a := action.(type) {
    case *actions.SendEventAction:
        // Verify region coordination
        return sm.verifyRegionalEvent(ctx, a)
    }
    return nil
}

// Update verifyRegionalEvent to include TEE execution
func (sm *StateManager) verifyRegionalEvent(ctx context.Context, event *actions.SendEventAction) error {
    // First execute in TEEs
    if err := sm.teeClient.ExecuteAction(ctx, event); err != nil {
        return fmt.Errorf("TEE execution failed: %w", err)
    }
    
    // Then do coordination
    workers := sm.coordinator.GetWorkerIDs()
    regWorkers := filterWorkersForRegion(workers, event.RegionID)
    
    task := &coordination.Task{
        ID:           event.IDTo,
        WorkerIDs:    regWorkers,
        Data:         event.Parameters,
        Attestations: event.Attestations[:],
        Timeout:      5 * time.Second,
    }

    if err := sm.coordinator.SubmitTask(ctx, task); err != nil {
        return fmt.Errorf("coordination failed: %w", err)
    }

    return nil
}

// Add cleanup method
func (sm *StateManager) Close() error {
    if sm.teeClient != nil {
        if err := sm.teeClient.Close(); err != nil {
            return fmt.Errorf("failed to close TEE client: %w", err)
        }
    }
    return nil
}

// Additional state keys for ShuttleVM actions
func (*StateManager) GetShuttleStateKeys(id string) state.Keys {
    keys := state.Keys{
        string([]byte(ObjectPrefix + id)): state.Read | state.Write,
        string([]byte(EventPrefix + id)): state.Read | state.Write,
        string([]byte(InputObject)): state.Read | state.Write,
        string([]byte(RegionPrefix + id)): state.Read | state.Write,  // Add this
    }
    return keys
}

func (sm *StateManager) GetCoordinator() *coordination.Coordinator {
    return sm.coordinator
}