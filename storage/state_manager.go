package storage

import (
    "context"
    "fmt"
    "time"

    // Import the HyperSDK state package that defines KeyValueReader, state.Mutable, etc.
    "github.com/ava-labs/hypersdk/state"
    "github.com/ava-labs/hypersdk/codec"
    "github.com/ava-labs/hypersdk/chain"

    // If your coordination package is separate:
    "github.com/rhombus-tech/vm/coordination"

    // If you need a TEE client
    "github.com/rhombus-tech/vm/tee"

    // If you have a merkleDB for the coordinator
    "github.com/ava-labs/avalanchego/x/merkledb"
)

// StateManager implements chain.StateManager.
var _ chain.StateManager = (*StateManager)(nil)

type StateManager struct {
    // The underlying state
    // note: if you plan to do writes, store a `state.Mutable`
    // or store both an Immutable + a separate Mutable if needed
    backingStore state.KeyValueReader

    // TEE client if needed
    teeClient *tee.Client

    // coordinator if needed
    coordinator *coordination.Coordinator
}

// NewStateManager is your constructor. 
// (dbForCoord must be typed properly, e.g. a *merkledb.MerkleDB if needed)
func NewStateManager(
    store state.KeyValueReader,
    teeEndpoint string,
    dbForCoord *merkledb.MerkleDB,
) (*StateManager, error) {
    // TEE client creation (assuming tee.NewClient wants 3 args)
    // if you don't need region or a verifier, pass empty/nil
    teeClient, err := tee.NewClient(teeEndpoint, "", nil)
    if err != nil {
        return nil, fmt.Errorf("failed to create TEE client: %w", err)
    }

    // If coordinator.NewCoordinator returns (coord, error) 
    // but wants a *merkledb.MerkleDB, do:
    coordCfg := &coordination.Config{
        // fill in
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

// If your chain.StateManager requires the following for balance ops:
func (sm *StateManager) AddBalance(
    ctx context.Context,
    addr codec.Address,
    st state.Mutable,
    amount uint64,
    createAccount bool,
) error {
    // call your local "AddBalance" free function
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

// If your chain.StateManager requires “CanDeduct”
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

// If you need to supply which keys you’ll sponsor
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

//----------------------------------------
// Helpers

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
