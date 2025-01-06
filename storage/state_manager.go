// storage/state_manager.go
package storage

import (
    "context"
    "fmt"
    "time"

    "github.com/ava-labs/hypersdk/chain"
    "github.com/ava-labs/hypersdk/codec"
    "github.com/ava-labs/hypersdk/state"
    "github.com/ava-labs/avalanchego/database"
    "github.com/ava-labs/avalanchego/x/merkledb"

    "github.com/rhombus-tech/vm/core"
    "github.com/rhombus-tech/vm/coordination"
)

type StateManager struct {
    // The underlying state
    backingStore state.KeyValueReader

    // TEE client if needed
    teeClient *tee.Client

    // coordinator 
    coordinator *coordination.Coordinator
}

// NewStateManager creates a new state manager
func NewStateManager(
    store state.KeyValueReader,
    teeEndpoint string,
    dbForCoord *merkledb.MerkleDB,
) (*StateManager, error) {
    // TEE client creation
    teeClient, err := tee.NewClient(teeEndpoint, "", nil)
    if err != nil {
        return nil, fmt.Errorf("failed to create TEE client: %w", err)
    }

    coordCfg := &coordination.Config{
        MinWorkers: 2,
        MaxWorkers: 10,
        WorkerTimeout: 30 * time.Second,
        ChannelTimeout: 10 * time.Second,
    }
    coord, err := coordination.NewCoordinator(coordCfg, dbForCoord)
    if err != nil {
        return nil, fmt.Errorf("failed to init coordinator: %w", err)
    }
    if err := coord.Start(); err != nil {
        return nil, fmt.Errorf("failed to start coordinator: %w", err)
    }

    return &StateManager{
        backingStore: store,
        teeClient: teeClient,
        coordinator: coord,
    }, nil
}

// Ensure StateManager implements core.StateManager
var _ core.StateManager = (*StateManager)(nil)

//----------------------
// chain.StateManager methods

func (sm *StateManager) HeightKey() []byte {
    return []byte("height")
}
func (sm *StateManager) TimestampKey() []byte {
    return []byte("timestamp")
}
func (sm *StateManager) FeeKey() []byte {
    return []byte("fee")
}

// For reading
func (sm *StateManager) GetValue(ctx context.Context, key []byte) ([]byte, error) {
    return sm.backingStore.GetValue(ctx, key)
}

// For writing
func (sm *StateManager) Insert(ctx context.Context, key, value []byte) error {
    mutable, ok := sm.backingStore.(state.Mutable)
    if !ok {
        return fmt.Errorf("backing store is not mutable")
    }
    return mutable.Insert(ctx, key, value)
}

func (sm *StateManager) Remove(ctx context.Context, key []byte) error {
    mutable, ok := sm.backingStore.(state.Mutable)
    if !ok {
        return fmt.Errorf("backing store is not mutable")
    }
    return mutable.Remove(ctx, key)
}

// Core interface implementations
func (sm *StateManager) GetObject(ctx context.Context, mu state.Mutable, id string) (*core.ObjectState, error) {
    k := ObjectKey(id)
    v, err := mu.GetValue(ctx, k)
    if errors.Is(err, database.ErrNotFound) {
        return nil, nil
    }
    if err != nil {
        return nil, err
    }

    var obj core.ObjectState
    if err := codec.Unmarshal(v, &obj); err != nil {
        return nil, err
    }
    return &obj, nil
}

func (sm *StateManager) SetObject(ctx context.Context, mu state.Mutable, id string, obj *core.ObjectState) error {
    k := ObjectKey(id)
    v, err := codec.Marshal(obj)
    if err != nil {
        return err
    }
    return mu.Insert(ctx, k, v)
}

func (sm *StateManager) ObjectExists(ctx context.Context, mu state.Mutable, id string) (bool, error) {
    obj, err := sm.GetObject(ctx, mu, id)
    if err != nil {
        return false, err
    }
    return obj != nil, nil
}

func (sm *StateManager) SetEvent(ctx context.Context, mu state.Mutable, id string, event *core.Event) error {
    k := EventKey(id)
    v, err := codec.Marshal(event)
    if err != nil {
        return err
    }
    return mu.Insert(ctx, k, v)
}

func (sm *StateManager) GetRegion(ctx context.Context, mu state.Mutable, id string) (map[string]interface{}, error) {
    k := RegionKey(id)
    v, err := mu.GetValue(ctx, k)
    if errors.Is(err, database.ErrNotFound) {
        return nil, nil
    }
    if err != nil {
        return nil, err
    }

    var region map[string]interface{}
    if err := codec.Unmarshal(v, &region); err != nil {
        return nil, err
    }
    return region, nil
}

func (sm *StateManager) SetRegion(ctx context.Context, mu state.Mutable, id string, region map[string]interface{}) error {
    k := RegionKey(id)
    v, err := codec.Marshal(region)
    if err != nil {
        return err
    }
    return mu.Insert(ctx, k, v)
}

func (sm *StateManager) RegionExists(ctx context.Context, mu state.Mutable, id string) (bool, error) {
    region, err := sm.GetRegion(ctx, mu, id)
    if err != nil {
        return false, err
    }
    return region != nil, nil
}

func (sm *StateManager) SetInputObject(ctx context.Context, mu state.Mutable, id string) error {
    k := InputObjectKey()
    return mu.Insert(ctx, k, []byte(id))
}

func (sm *StateManager) GetCoordinator() core.Coordinator {
    return sm.coordinator
}

func (sm *StateManager) Close() error {
    var errs []error
    if sm.teeClient != nil {
        if err := sm.teeClient.Close(); err != nil {
            errs = append(errs, fmt.Errorf("close TEE client: %w", err))
        }
    }
    if sm.coordinator != nil {
        if err := sm.coordinator.Stop(); err != nil {
            errs = append(errs, fmt.Errorf("stop coordinator: %w", err))
        }
    }
    if len(errs) > 0 {
        return fmt.Errorf("multiple errors: %v", errs)
    }
    return nil
}

func (sm *StateManager) AddBalance(
    ctx context.Context,
    addr codec.Address,
    st state.Mutable,
    amount uint64,
    createAccount bool,
) error {
    _, err := AddBalance(ctx, st, addr, amount, createAccount)
    return err
}

func (sm *StateManager) Deduct(
    ctx context.Context,
    addr codec.Address,
    st state.Mutable,
    amount uint64,
) error {
    _, err := SubBalance(ctx, st, addr, amount)
    return err
}

func (sm *StateManager) CanDeduct(
    ctx context.Context,
    addr codec.Address,
    st state.Immutable,
    amount uint64,
) error {
    bal, err := GetBalance(ctx, st, addr)
    if err != nil {
        return err
    }
    if bal < amount {
        return fmt.Errorf("insufficient balance")  
    }
    return nil
}

func (sm *StateManager) SponsorStateKeys(addr codec.Address) state.Keys {
    return state.Keys{
        string(BalanceKey(addr)): state.Read | state.Write,
    }
}

func (sm *StateManager) GlobalStateKeys() state.Keys {
    return state.Keys{
        string(sm.HeightKey()):    state.Read | state.Write,
        string(sm.TimestampKey()): state.Read | state.Write,
        string(sm.FeeKey()):       state.Read | state.Write,
    }
}