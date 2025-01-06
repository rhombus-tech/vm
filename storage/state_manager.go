// storage/state_manager.go
package storage

import (
    "context"
    "fmt"
    "time"
    "errors"

    "github.com/ava-labs/avalanchego/database"
    "github.com/ava-labs/hypersdk/codec"
    "github.com/ava-labs/hypersdk/state"
    "github.com/ava-labs/hypersdk/chain"
    "github.com/ava-labs/avalanchego/x/merkledb"
    "github.com/ava-labs/avalanchego/utils/math" // For smath
    
    "github.com/rhombus-tech/vm/coordination"
    "github.com/rhombus-tech/vm/core"
)

// StateManager wraps lower-level storage operations
type StateManager struct {
    backingStore state.Mutable // Changed from state.State
    coordinator  *coordination.Coordinator
}

// NewStateManager creates a new state manager
func NewStateManager(
    store state.Mutable, // Changed from state.State
    dbForCoord merkledb.MerkleDB,
) (*StateManager, error) {
    coordCfg := &coordination.Config{
        MinWorkers:      2,
        MaxWorkers:      10,
        WorkerTimeout:   30 * time.Second,
        ChannelTimeout:  10 * time.Second,
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
        coordinator:  coord,
    }, nil
}

// Balance operations
func (s *StateManager) AddBalance(
    ctx context.Context, 
    addr codec.Address, 
    st state.Mutable,
    amount uint64,
    createAccount bool,
) error {
    oldBal, err := GetBalance(ctx, st, addr)
    if err != nil {
        return err
    }
    if !createAccount && oldBal == 0 {
        return database.ErrNotFound
    }
    newBal, err := math.Add64(oldBal, amount) // Changed from smath to math
    if err != nil {
        return err
    }
    return SetBalance(ctx, st, addr, newBal)
}

func (s *StateManager) Deduct(
    ctx context.Context,
    addr codec.Address,
    st state.Mutable,
    amount uint64,
) error {
    oldBal, err := GetBalance(ctx, st, addr)
    if err != nil {
        return err
    }
    if oldBal < amount {
        return ErrInsufficientBalance
    }
    newBal := oldBal - amount
    if newBal == 0 {
        return st.Remove(ctx, createBalanceKey(addr)) // Changed from BalanceKey
    }
    return SetBalance(ctx, st, addr, newBal)
}

func (s *StateManager) CanDeduct(
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
        return ErrInsufficientBalance
    }
    return nil
}

func (s *StateManager) SponsorStateKeys(addr codec.Address) state.Keys {
    return state.Keys{
        string(createBalanceKey(addr)): state.Read | state.Write, // Changed from BalanceKey
    }
}

// Helper function to create balance key
func createBalanceKey(addr codec.Address) []byte {
    k := make([]byte, 1+len(addr))
    k[0] = balancePrefix
    copy(k[1:], addr[:]) // Use slice notation since codec.Address is already []byte
    return k
}

// Object operations
func (s *StateManager) GetObject(ctx context.Context, mu state.Mutable, id string) (*core.ObjectState, error) {
    obj, err := GetObject(ctx, mu, id)
    if err != nil {
        return nil, err
    }
   
    if obj == nil {
        return nil, nil
    }
 
 
    objState := &core.ObjectState{
        Code:     obj["code"],
        Storage:  obj["storage"],
        RegionID: string(obj["region_id"]),
    }
    return objState, nil
 }
 
 
 func (s *StateManager) SetObject(ctx context.Context, mu state.Mutable, id string, obj *core.ObjectState) error {
    objMap := map[string][]byte{
        "code":      obj.Code,
        "storage":   obj.Storage,
        "region_id": []byte(obj.RegionID),
    }
    return SetObject(ctx, mu, id, objMap, nil)
 }
 
 
 func (s *StateManager) ObjectExists(ctx context.Context, mu state.Mutable, id string) (bool, error) {
    obj, err := GetObject(ctx, mu, id)
    if err != nil {
        return false, err
    }
    return obj != nil, nil
 }
 
 
 // Event operations
 func (s *StateManager) SetEvent(ctx context.Context, mu state.Mutable, id string, event *core.Event) error {
    return QueueEvent(ctx, mu, id, event.FunctionCall, event.Parameters, event.Attestations, nil)
 }
 
 
 // Region operations
 func (s *StateManager) GetRegion(ctx context.Context, mu state.Mutable, id string) (map[string]interface{}, error) {
    return GetRegion(ctx, mu, id)
 }
 
 
 func (s *StateManager) SetRegion(ctx context.Context, mu state.Mutable, id string, region map[string]interface{}) error {
    return SetRegion(ctx, mu, id, region)
 }
 
 
 func (s *StateManager) RegionExists(ctx context.Context, mu state.Mutable, id string) (bool, error) {
    region, err := GetRegion(ctx, mu, id)
    if err != nil {
        return false, err
    }
    return region != nil, nil
 }
 
 
 // Input object operations
 func (s *StateManager) SetInputObject(ctx context.Context, mu state.Mutable, id string) error {
    return SetInputObject(ctx, mu, id)
 } 

// Chain rules implementations
func (s *StateManager) HeightKey() []byte {
    return heightKey
}

func (s *StateManager) TimestampKey() []byte {
    return timestampKey
}

func (s *StateManager) FeeKey() []byte {
    return feeKey
}

// GetValue implements KeyValueReader
func (s *StateManager) GetValue(ctx context.Context, key []byte) ([]byte, error) {
    return s.backingStore.GetValue(ctx, key)
}

// Insert implements mutable operations
func (s *StateManager) Insert(ctx context.Context, key []byte, value []byte) error {
    return s.backingStore.Insert(ctx, key, value)
}

func (s *StateManager) Remove(ctx context.Context, key []byte) error {
    return s.backingStore.Remove(ctx, key)
}

func (s *StateManager) GetCoordinator() *coordination.Coordinator {
    return s.coordinator
}

func (s *StateManager) Close() error {
    if s.coordinator != nil {
        if err := s.coordinator.Stop(); err != nil {
            return fmt.Errorf("stop coordinator: %w", err)
        }
    }
    return nil
}

// Verify StateManager implements all required interfaces
var (
    _ chain.StateManager = (*StateManager)(nil)
    _ state.Mutable = (*StateManager)(nil)
)