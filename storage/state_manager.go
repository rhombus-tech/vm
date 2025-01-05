package storage

import (
    "context"
    "errors"
    "fmt"
    "time"

    "github.com/ava-labs/avalanchego/database"
    "github.com/ava-labs/avalanchego/ids"
    "github.com/ava-labs/hypersdk/chain"
    "github.com/ava-labs/hypersdk/codec"
    "github.com/ava-labs/hypersdk/state"
    "github.com/ava-labs/hypersdk/consts"
    "github.com/ava-labs/hypersdk/fees"

    "github.com/rhombus-tech/vm/actions"
    "github.com/rhombus-tech/vm/tee"
    "github.com/rhombus-tech/vm/coordination"
)

type StateManager struct {
    state       state.KeyValueReader
    teeClient   *tee.Client
    coordinator *coordination.Coordinator
}

func NewStateManager(db state.KeyValueReader, teeEndpoint string) (*StateManager, error) {
    teeClient, err := tee.NewClient(teeEndpoint)
    if err != nil {
        return nil, fmt.Errorf("failed to create TEE client: %w", err)
    }

    // Initialize coordinator
    coord := coordination.NewCoordinator(&coordination.Config{
        MinWorkers: 2,
        MaxWorkers: 10,
        WorkerTimeout: 30 * time.Second,
        ChannelTimeout: 10 * time.Second,
    })
    if err := coord.Start(); err != nil {
        return nil, fmt.Errorf("failed to start coordinator: %w", err)
    }

    return &StateManager{
        state: db,
        teeClient: teeClient,
        coordinator: coord,
    }, nil
}

// Base chain.StateManager implementations
func (sm *StateManager) HeightKey() []byte {
    return HeightKey()
}

func (sm *StateManager) TimestampKey() []byte {
    return TimestampKey()
}

func (sm *StateManager) FeeKey() []byte {
    return FeeKey()
}

func (sm *StateManager) GetValue(ctx context.Context, key []byte) ([]byte, error) {
    return sm.state.GetValue(ctx, key)
}

func (sm *StateManager) Insert(ctx context.Context, key []byte, value []byte) error {
    return sm.state.Insert(ctx, key, value)
}

func (sm *StateManager) Remove(ctx context.Context, key []byte) error {
    return sm.state.Remove(ctx, key)
}

// Balance and state key management
func (sm *StateManager) SponsorStateKeys(addr codec.Address) state.Keys {
    return state.Keys{
        string(BalanceKey(addr)): state.Read | state.Write,
    }
}

func (sm *StateManager) CanDeduct(
    ctx context.Context,
    addr codec.Address,
    amount uint64,
) error {
    bal, err := GetBalance(ctx, sm.state, addr)
    if err != nil {
        return err
    }
    if bal < amount {
        return ErrInvalidBalance
    }
    return nil
}

func (sm *StateManager) Deduct(
    ctx context.Context,
    addr codec.Address,
    amount uint64,
) error {
    _, err := SubBalance(ctx, sm.state, addr, amount)
    return err
}

func (sm *StateManager) AddBalance(
    ctx context.Context, 
    addr codec.Address,
    amount uint64,
    createAccount bool,
) error {
    _, err := AddBalance(ctx, sm.state, addr, amount, createAccount)
    return err
}

// Object management with state keys
func (sm *StateManager) GetObject(ctx context.Context, id string) (map[string][]byte, error) {
    key := []byte(fmt.Sprintf("object:%s", id))
    value, err := sm.GetValue(ctx, key)
    if err != nil {
        if errors.Is(err, database.ErrNotFound) {
            return nil, nil
        }
        return nil, err
    }

    var obj map[string][]byte
    if err := codec.Unmarshal(value, &obj); err != nil {
        return nil, fmt.Errorf("failed to unmarshal object: %w", err)
    }
    return obj, nil
}

func (sm *StateManager) SetObject(ctx context.Context, id string, obj map[string][]byte) error {
    key := []byte(fmt.Sprintf("object:%s", id))
    value, err := codec.Marshal(obj)
    if err != nil {
        return fmt.Errorf("failed to marshal object: %w", err)
    }
    return sm.Insert(ctx, key, value)
}

func (sm *StateManager) ObjectExists(ctx context.Context, id string) (bool, error) {
    key := []byte(fmt.Sprintf("object:%s", id))
    return sm.state.Has(ctx, key)
}

// Event management with coordination
func (sm *StateManager) SetEvent(ctx context.Context, id string, event *actions.Event) error {
    key := []byte(fmt.Sprintf("event:%s:%s", event.Timestamp, id))
    value, err := codec.Marshal(event)
    if err != nil {
        return fmt.Errorf("failed to marshal event: %w", err)
    }

    // Update coordination state
    task := &coordination.Task{
        ID:       id,
        WorkerIDs: sm.getRegionWorkers(event.RegionID),
        Data:     event.Parameters,
        Timeout:  5 * time.Second,
    }
    if err := sm.coordinator.SubmitTask(ctx, task); err != nil {
        return fmt.Errorf("coordination failed: %w", err)
    }

    return sm.Insert(ctx, key, value)
}

func (sm *StateManager) GetEvent(ctx context.Context, timestamp string, id string) (*actions.Event, error) {
    key := []byte(fmt.Sprintf("event:%s:%s", timestamp, id))
    value, err := sm.GetValue(ctx, key)
    if err != nil {
        if errors.Is(err, database.ErrNotFound) {
            return nil, nil
        }
        return nil, err
    }

    var event actions.Event
    if err := codec.Unmarshal(value, &event); err != nil {
        return nil, fmt.Errorf("failed to unmarshal event: %w", err)
    }
    return &event, nil
}

// Input object management
func (sm *StateManager) SetInputObject(ctx context.Context, id string) error {
    return sm.Insert(ctx, []byte("input_object"), []byte(id))
}

func (sm *StateManager) GetInputObject(ctx context.Context) (string, error) {
    value, err := sm.GetValue(ctx, []byte("input_object"))
    if err != nil {
        if errors.Is(err, database.ErrNotFound) {
            return "", nil
        }
        return "", err
    }
    return string(value), nil
}

// Region management with worker filtering
func (sm *StateManager) GetRegion(ctx context.Context, id string) (map[string]interface{}, error) {
    key := []byte(fmt.Sprintf("region:%s", id))
    value, err := sm.GetValue(ctx, key)
    if err != nil {
        if errors.Is(err, database.ErrNotFound) {
            return nil, nil
        }
        return nil, err
    }

    var region map[string]interface{}
    if err := codec.Unmarshal(value, &region); err != nil {
        return nil, fmt.Errorf("failed to unmarshal region: %w", err)
    }
    return region, nil
}

func (sm *StateManager) SetRegion(ctx context.Context, id string, region map[string]interface{}) error {
    key := []byte(fmt.Sprintf("region:%s", id))
    value, err := codec.Marshal(region)
    if err != nil {
        return fmt.Errorf("failed to marshal region: %w", err)
    }
    return sm.Insert(ctx, key, value)
}

func (sm *StateManager) RegionExists(ctx context.Context, id string) (bool, error) {
    key := []byte(fmt.Sprintf("region:%s", id))
    return sm.state.Has(ctx, key)
}

// State key helpers
func (sm *StateManager) GetShuttleStateKeys(id string) state.Keys {
    return state.Keys{
        fmt.Sprintf("object:%s", id): state.Read | state.Write,
        fmt.Sprintf("event:%s", id):  state.Read | state.Write,
        "input_object":               state.Read | state.Write,
    }
}

// TEE and coordination
func (sm *StateManager) GetCoordinator() *coordination.Coordinator {
    return sm.coordinator
}

func (sm *StateManager) ExecuteAction(ctx context.Context, action *actions.SendEventAction) error {
    // Execute in TEE
    if err := sm.teeClient.ExecuteAction(ctx, action); err != nil {
        return fmt.Errorf("TEE execution failed: %w", err)
    }

    // Coordinate between workers
    workers := sm.coordinator.GetWorkerIDs()
    regWorkers := sm.filterWorkersForRegion(workers, action.RegionID)

    task := &coordination.Task{
        ID:           action.IDTo,
        WorkerIDs:    regWorkers,
        Data:         action.Parameters,
        Attestations: action.Attestations[:],
        Timeout:      5 * time.Second,
    }

    if err := sm.coordinator.SubmitTask(ctx, task); err != nil {
        return fmt.Errorf("coordination failed: %w", err)
    }

    return nil
}

// Helper functions
func (sm *StateManager) getRegionWorkers(regionID string) []coordination.WorkerID {
    var workers []coordination.WorkerID
    allWorkers := sm.coordinator.GetWorkerIDs()
    return sm.filterWorkersForRegion(allWorkers, regionID)
}

func (sm *StateManager) filterWorkersForRegion(workers []coordination.WorkerID, regionID string) []coordination.WorkerID {
    var regWorkers []coordination.WorkerID
    for _, worker := range workers {
        if sm.isWorkerInRegion(worker, regionID) {
            regWorkers = append(regWorkers, worker)
        }
    }
    return regWorkers
}

func (sm *StateManager) isWorkerInRegion(workerID coordination.WorkerID, regionID string) bool {
    // Implementation would check worker's region assignment
    // This is a placeholder
    return true
}

func (sm *StateManager) Close() error {
    var errs []error
    if sm.teeClient != nil {
        if err := sm.teeClient.Close(); err != nil {
            errs = append(errs, fmt.Errorf("failed to close TEE client: %w", err))
        }
    }
    if sm.coordinator != nil {
        if err := sm.coordinator.Stop(); err != nil {
            errs = append(errs, fmt.Errorf("failed to stop coordinator: %w", err))
        }
    }
    if len(errs) > 0 {
        return fmt.Errorf("multiple errors during shutdown: %v", errs)
    }
    return nil
}