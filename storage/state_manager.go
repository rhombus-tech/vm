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
    "github.com/rhombus-tech/hypersdk/coordination"
    
    "github.com/rhombus-tech/vm"      // For interfaces
    "github.com/rhombus-tech/vm/types"
    "github.com/rhombus-tech/vm/tee"  
)

// Constants for state key prefixes
const (
    ObjectPrefix = "object:"
    EventPrefix  = "event:"
    InputObject  = "input_object"
    RegionPrefix = "region:"
)

// Interface verification
var _ chain.StateManager = (*StateManager)(nil)
var _ vm.StateManager = (*StateManager)(nil)

// StateManager structure
type StateManager struct {
    coordinator *coordination.Coordinator
    teeClient   *tee.Client
}

// Constructor
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

// Base chain.StateManager implementations
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

// Object management implementations
func (*StateManager) GetObject(ctx context.Context, mu state.Immutable, id string) (*types.ObjectState, error) {
    key := []byte(ObjectPrefix + id)
    objBytes, err := mu.GetValue(ctx, key)
    if err != nil {
        return nil, err
    }
    if objBytes == nil {
        return nil, nil
    }

    var obj types.ObjectState
    if err := codec.Unmarshal(objBytes, &obj); err != nil {
        return nil, err
    }

    return &obj, nil
}

func (*StateManager) SetObject(ctx context.Context, mu state.Mutable, id string, obj *types.ObjectState) error {
    key := []byte(ObjectPrefix + id)
    objBytes, err := codec.Marshal(obj)
    if err != nil {
        return err
    }

    return mu.SetValue(ctx, key, objBytes)
}

func (*StateManager) ObjectExists(ctx context.Context, im state.Immutable, id string) (bool, error) {
    key := []byte(ObjectPrefix + id)
    return im.HasValue(ctx, key)
}

// Event management implementations
func (*StateManager) SetEvent(ctx context.Context, mu state.Mutable, id string, event *types.Event) error {
    key := []byte(fmt.Sprintf("%s%s:%s", EventPrefix, event.Timestamp, id))
    eventBytes, err := codec.Marshal(event)
    if err != nil {
        return err
    }

    return mu.SetValue(ctx, key, eventBytes)
}

func (*StateManager) GetEvent(ctx context.Context, im state.Immutable, timestamp string, id string) (*types.Event, error) {
    key := []byte(fmt.Sprintf("%s%s:%s", EventPrefix, timestamp, id))
    eventBytes, err := im.GetValue(ctx, key)
    if err != nil {
        return nil, err
    }
    if eventBytes == nil {
        return nil, nil
    }

    var event types.Event
    if err := codec.Unmarshal(eventBytes, &event); err != nil {
        return nil, err
    }
    return &event, nil
}

// Input object management
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

func (*StateManager) SetInputObject(ctx context.Context, mu state.Mutable, id string) error {
    return mu.SetValue(ctx, []byte(InputObject), []byte(id))
}

// Region management implementations
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

func (*StateManager) SetRegion(ctx context.Context, mu state.Mutable, id string, region map[string]interface{}) error {
    key := []byte(RegionPrefix + id)
    regionBytes, err := codec.Marshal(region)
    if err != nil {
        return err
    }
    return mu.SetValue(ctx, key, regionBytes)
}

func (*StateManager) RegionExists(ctx context.Context, im state.Immutable, id string) (bool, error) {
    key := []byte(RegionPrefix + id)
    return im.HasValue(ctx, key)
}

// State keys helper
func (*StateManager) GetShuttleStateKeys(id string) state.Keys {
    keys := state.Keys{
        string([]byte(ObjectPrefix + id)): state.Read | state.Write,
        string([]byte(EventPrefix + id)): state.Read | state.Write,
        string([]byte(InputObject)): state.Read | state.Write,
        string([]byte(RegionPrefix + id)): state.Read | state.Write,
    }
    return keys
}// Event management implementations
func (*StateManager) SetEvent(ctx context.Context, mu state.Mutable, id string, event *types.Event) error {
    key := []byte(fmt.Sprintf("%s%s:%s", EventPrefix, event.Timestamp, id))
    eventBytes, err := codec.Marshal(event)
    if err != nil {
        return err
    }

    return mu.SetValue(ctx, key, eventBytes)
}

func (*StateManager) GetEvent(ctx context.Context, im state.Immutable, timestamp string, id string) (*types.Event, error) {
    key := []byte(fmt.Sprintf("%s%s:%s", EventPrefix, timestamp, id))
    eventBytes, err := im.GetValue(ctx, key)
    if err != nil {
        return nil, err
    }
    if eventBytes == nil {
        return nil, nil
    }

    var event types.Event
    if err := codec.Unmarshal(eventBytes, &event); err != nil {
        return nil, err
    }
    return &event, nil
}

// Input object management
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

func (*StateManager) SetInputObject(ctx context.Context, mu state.Mutable, id string) error {
    return mu.SetValue(ctx, []byte(InputObject), []byte(id))
}

// Region management implementations
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

func (*StateManager) SetRegion(ctx context.Context, mu state.Mutable, id string, region map[string]interface{}) error {
    key := []byte(RegionPrefix + id)
    regionBytes, err := codec.Marshal(region)
    if err != nil {
        return err
    }
    return mu.SetValue(ctx, key, regionBytes)
}

func (*StateManager) RegionExists(ctx context.Context, im state.Immutable, id string) (bool, error) {
    key := []byte(RegionPrefix + id)
    return im.HasValue(ctx, key)
}

// State keys helper
func (*StateManager) GetShuttleStateKeys(id string) state.Keys {
    keys := state.Keys{
        string([]byte(ObjectPrefix + id)): state.Read | state.Write,
        string([]byte(EventPrefix + id)): state.Read | state.Write,
        string([]byte(InputObject)): state.Read | state.Write,
        string([]byte(RegionPrefix + id)): state.Read | state.Write,
    }
    return keys
}

// State transition verification
func (sm *StateManager) VerifyStateTransition(ctx context.Context, action chain.Action) error {
    switch a := action.(type) {
    case *types.SendEventAction:  // Updated to use types package
        // Verify region coordination
        return sm.verifyRegionalEvent(ctx, a)
    }
    return nil
}

// TEE and coordination logic
func (sm *StateManager) verifyRegionalEvent(ctx context.Context, event *types.SendEventAction) error {
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

// Helper function to filter workers for a specific region
func filterWorkersForRegion(workers []coordination.WorkerID, regionID string) []coordination.WorkerID {
    var regWorkers []coordination.WorkerID
    for _, worker := range workers {
        // Add your region filtering logic here
        // For example, check if worker belongs to the region
        if isWorkerInRegion(worker, regionID) {
            regWorkers = append(regWorkers, worker)
        }
    }
    return regWorkers
}

// Helper function to check if a worker belongs to a region
func isWorkerInRegion(workerID coordination.WorkerID, regionID string) bool {
    // Implement your worker-region mapping logic here
    // This could involve checking a mapping stored in state
    // or using a naming convention for worker IDs
    return true // Placeholder implementation
}

// Coordinator access
func (sm *StateManager) GetCoordinator() *coordination.Coordinator {
    return sm.coordinator
}

// Cleanup
func (sm *StateManager) Close() error {
    if sm.teeClient != nil {
        if err := sm.teeClient.Close(); err != nil {
            return fmt.Errorf("failed to close TEE client: %w", err)
        }
    }
    return nil
}