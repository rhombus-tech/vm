// storage/state_manager.go
package storage

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/ava-labs/avalanchego/database"
	"github.com/ava-labs/avalanchego/utils/math"
	"github.com/ava-labs/avalanchego/x/merkledb"
	"github.com/ava-labs/hypersdk/chain"
	"github.com/ava-labs/hypersdk/codec"
	"github.com/ava-labs/hypersdk/state"

	"github.com/rhombus-tech/vm/coordination"
	"github.com/rhombus-tech/vm/core"
)

var (
    ErrInvalidRegionID = errors.New("invalid region ID")
)

// StateManager wraps lower-level storage operations
type StateManager struct {
    backingStore state.Mutable
    coordinator  *coordination.Coordinator
    regionStores map[string]state.Mutable
    regionMu     sync.RWMutex
}

// Add region-aware key creation
func makeRegionKey(regionID, prefix, id string) []byte {
    return []byte(fmt.Sprintf("%s/%s/%s", regionID, prefix, id))
}

// NewStateManager creates a new state manager
func NewStateManager(
    store state.Mutable,
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
        regionStores: make(map[string]state.Mutable),
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
    newBal, err := math.Add64(oldBal, amount)
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
        return st.Remove(ctx, createBalanceKey(addr))
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
        string(createBalanceKey(addr)): state.Read | state.Write,
    }
}

// Helper function to create balance key
func createBalanceKey(addr codec.Address) []byte {
    k := make([]byte, 1+len(addr))
    k[0] = balancePrefix
    copy(k[1:], addr[:])
    return k
}

// Region-aware object operations
func (s *StateManager) GetObject(
    ctx context.Context,
    mu state.Mutable,
    id string,
    regionID string,
) (*core.ObjectState, error) {
    if regionID == "" {
        return nil, ErrInvalidRegionID
    }

    store := s.getRegionalStore(regionID, mu)
    objKey := makeRegionKey(regionID, "object", id)
    
    value, err := store.GetValue(ctx, objKey)
    if err != nil {
        if errors.Is(err, database.ErrNotFound) {
            return nil, nil
        }
        return nil, err
    }

    var obj map[string][]byte
    if err := unmarshalState(value, &obj); err != nil {
        return nil, err
    }

    return &core.ObjectState{
        Code:     obj["code"],
        Storage:  obj["storage"],
        RegionID: regionID,
        Status:   string(obj["status"]),
    }, nil
}

func (s *StateManager) SetObject(
    ctx context.Context,
    mu state.Mutable,
    id string,
    obj *core.ObjectState,
) error {
    if obj.RegionID == "" {
        return ErrInvalidRegionID
    }

    store := s.getRegionalStore(obj.RegionID, mu)
    objKey := makeRegionKey(obj.RegionID, "object", id)

    objMap := map[string][]byte{
        "code":      obj.Code,
        "storage":   obj.Storage,
        "status":    []byte(obj.Status),
    }

    value, err := marshalState(objMap)
    if err != nil {
        return err
    }

    return store.Insert(ctx, objKey, value)
}

func (s *StateManager) ObjectExists(
    ctx context.Context,
    mu state.Mutable,
    id string,
    regionID string,
) (bool, error) {
    if regionID == "" {
        return false, ErrInvalidRegionID
    }

    // Use the existing regional store getter
    store := s.getRegionalStore(regionID, mu)
    objKey := makeRegionKey(regionID, "object", id)
    
    value, err := store.GetValue(ctx, objKey)
    if err != nil {
        if errors.Is(err, database.ErrNotFound) {
            return false, nil
        }
        return false, err
    }
    
    return value != nil, nil
}
// Event operations
func (s *StateManager) SetEvent(
    ctx context.Context,
    mu state.Mutable,
    id string,
    event *core.Event,
    regionID string,
) error {
    if regionID == "" {
        return ErrInvalidRegionID
    }

    store := s.getRegionalStore(regionID, mu)
    eventKey := makeRegionKey(regionID, "event", id)

    // Create event data
    eventData := map[string]interface{}{
        "function_call": event.FunctionCall,
        "parameters":    event.Parameters,
        "attestations": event.Attestations,
        "timestamp":    event.Timestamp,
        "status":       event.Status,
    }

    // Marshal event data
    value, err := marshalState(eventData)
    if err != nil {
        return fmt.Errorf("failed to marshal event: %w", err)
    }

    // Store using the regional event key
    return store.Insert(ctx, eventKey, value)
}

// Region operations
func (s *StateManager) GetRegion(
    ctx context.Context,
    mu state.Mutable,
    regionID string,
) (map[string]interface{}, error) {
    if regionID == "" {
        return nil, ErrInvalidRegionID
    }

    store := s.getRegionalStore(regionID, mu)
    return GetRegion(ctx, store, regionID)
}

func (s *StateManager) SetRegion(
    ctx context.Context,
    mu state.Mutable,
    regionID string,
    region map[string]interface{},
) error {
    if regionID == "" {
        return ErrInvalidRegionID
    }

    store := s.getRegionalStore(regionID, mu)
    return SetRegion(ctx, store, regionID, region)
}

func (s *StateManager) RegionExists(
    ctx context.Context,
    mu state.Mutable,
    regionID string,
) (bool, error) {
    if regionID == "" {
        return false, ErrInvalidRegionID
    }

    store := s.getRegionalStore(regionID, mu)
    region, err := GetRegion(ctx, store, regionID)
    if err != nil {
        return false, err
    }
    return region != nil, nil
}

// Input object operations
func (s *StateManager) SetInputObject(
    ctx context.Context,
    mu state.Mutable,
    id string,
) error {
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

// Region-aware GetValue implementation
func (s *StateManager) GetValue(ctx context.Context, key []byte) ([]byte, error) {
    if isRegionalKey(key) {
        regionID := extractRegionID(key)
        store, err := s.getRegionStore(regionID)
        if err != nil {
            return nil, err
        }
        return store.GetValue(ctx, key)
    }
    return s.backingStore.GetValue(ctx, key)
}

// Region-aware Insert implementation
func (s *StateManager) Insert(ctx context.Context, key []byte, value []byte) error {
    if isRegionalKey(key) {
        regionID := extractRegionID(key)
        store, err := s.getRegionStore(regionID)
        if err != nil {
            return err
        }
        return store.Insert(ctx, key, value)
    }
    return s.backingStore.Insert(ctx, key, value)
}

func (s *StateManager) Remove(ctx context.Context, key []byte) error {
    if isRegionalKey(key) {
        regionID := extractRegionID(key)
        store, err := s.getRegionStore(regionID)
        if err != nil {
            return err
        }
        return store.Remove(ctx, key)
    }
    return s.backingStore.Remove(ctx, key)
}

// Region store management
func (s *StateManager) getRegionStore(regionID string) (state.Mutable, error) {
    s.regionMu.RLock()
    store, exists := s.regionStores[regionID]
    s.regionMu.RUnlock()

    if !exists {
        return nil, ErrRegionNotFound
    }
    return store, nil
}

func (s *StateManager) getRegionalStore(regionID string, defaultStore state.Mutable) state.Mutable {
    s.regionMu.RLock()
    store, exists := s.regionStores[regionID]
    s.regionMu.RUnlock()
    
    if !exists {
        return defaultStore
    }
    return store
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

// Helper functions
func isRegionalKey(key []byte) bool {
    return len(key) > 2 && key[0] == 'r' && key[1] == '/'
}

func extractRegionID(key []byte) string {
    parts := bytes.Split(key, []byte("/"))
    if len(parts) < 3 {
        return ""
    }
    return string(parts[1])
}

// Verify StateManager implements all required interfaces
var (
    _ chain.StateManager = (*StateManager)(nil)
    _ state.Mutable = (*StateManager)(nil)
)