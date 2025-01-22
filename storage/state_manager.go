// storage/state_manager.go
package storage

import (
	"bytes"
	"context"
	"encoding/json"
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
	"github.com/rhombus-tech/vm/interfaces"
)

var (
    ErrInvalidRegionID = errors.New("invalid region ID")
)

const (
    validEnclavesPrefix = "valid_enclaves/"
    regionMeasurementsPrefix = "region_measurements/"
)

type EnclaveInfo struct {
    Measurement []byte    `json:"measurement"`
    ValidFrom   time.Time `json:"valid_from"`
    ValidUntil  time.Time `json:"valid_until"`
    EnclaveType string    `json:"enclave_type"` // "SGX" or "SEV"
    RegionID    string    `json:"region_id"`
}

type Region struct {
    ID        string    `json:"id"`
    CreatedAt time.Time `json:"created_at"`
    Workers   []string  `json:"worker_ids"`
    Status    string    `json:"status"`
}

// StateManager wraps lower-level storage operations
type StateManager struct {
    db              database.Database
    backingStore    state.Mutable
    coordinator     *coordination.Coordinator
    regionStores    map[string]*MerkleStore  
    regionMu        sync.RWMutex
    regionalManager *RegionalStateManager
    merkleDB        merkledb.MerkleDB
    baseStorage     coordination.BaseStorage
}


func (s *StateManager) GetValidEnclave(
    ctx context.Context, 
    mu state.Mutable,
    enclaveID []byte,
) (*EnclaveInfo, error) {
    key := append([]byte(validEnclavesPrefix), enclaveID...)
    value, err := mu.GetValue(ctx, key)
    if err != nil {
        if errors.Is(err, database.ErrNotFound) {
            return nil, nil
        }
        return nil, err
    }

    var info EnclaveInfo
    if err := json.Unmarshal(value, &info); err != nil {
        return nil, err
    }
    return &info, nil
}

func (s *StateManager) SetValidEnclave(
    ctx context.Context,
    mu state.Mutable,
    enclaveID []byte,
    info *EnclaveInfo,
) error {
    value, err := json.Marshal(info)
    if err != nil {
        return err
    }
    
    key := append([]byte(validEnclavesPrefix), enclaveID...)
    return mu.Insert(ctx, key, value)
}

func (s *StateManager) GetRegionMeasurement(
    ctx context.Context,
    mu state.Mutable,
    regionID string,
) ([]byte, error) {
    key := append([]byte(regionMeasurementsPrefix), []byte(regionID)...)
    return mu.GetValue(ctx, key)
}

func (s *StateManager) SetRegionMeasurement(
    ctx context.Context,
    mu state.Mutable,
    regionID string,
    measurement []byte,
) error {
    key := append([]byte(regionMeasurementsPrefix), []byte(regionID)...)
    return mu.Insert(ctx, key, measurement)
}

// Add region-aware key creation
func makeRegionKey(regionID, prefix, id string) []byte {
    return []byte(fmt.Sprintf("%s/%s/%s", regionID, prefix, id))
}

// NewStateManager creates a new state manager
func NewStateManager(
    db database.Database,
    store state.Mutable,
    dbForCoord merkledb.MerkleDB,
) (*StateManager, error) {
    // Create base storage wrapper
    dbWrapper := NewDatabaseWrapper(db)
    
    // Create coordination storage wrapper
    coordStorage := NewCoordinationStorageWrapper(dbWrapper)

    // Create coordinator config
    coordCfg := &coordination.Config{
        MinWorkers:      2,
        MaxWorkers:      10,
        WorkerTimeout:   30 * time.Second,
        ChannelTimeout:  10 * time.Second,
        TaskTimeout:     5 * time.Minute,
        MaxTasks:       100,
        TaskQueueSize:  1000,
        EncryptionEnabled: true,
        RequireAttestation: true,
        AttestationTimeout: 5 * time.Second,
        StoragePath:       "/tmp/coordinator",
        PersistenceEnabled: true,
    }

    // Create coordinator with coordination storage
    coord, err := coordination.NewCoordinator(coordCfg, dbForCoord, coordStorage)
    if err != nil {
        return nil, fmt.Errorf("failed to init coordinator: %w", err)
    }
    
    // Start coordinator
    if err := coord.Start(); err != nil {
        return nil, fmt.Errorf("failed to start coordinator: %w", err)
    }

    // Create regional manager
    regionalManager := NewRegionalStateManager(dbForCoord, store)

    // Create state manager instance
    sm := &StateManager{
        db:              db,
        backingStore:    store,
        coordinator:     coord,
        regionStores:    make(map[string]*MerkleStore),
        regionalManager: regionalManager,
        merkleDB:        dbForCoord,
        baseStorage:     coordStorage,
    }

    return sm, nil
}



func (s *StateManager) Iterator(ctx context.Context, prefix []byte) interfaces.Iterator {
    if bytes.HasPrefix(prefix, []byte("r/")) {
        parts := bytes.SplitN(prefix, []byte("/"), 3)
        if len(parts) >= 2 {
            regionID := string(parts[1])
            store, err := s.regionalManager.GetRegionalStore(regionID)
            if err == nil {
                // Convert to interfaces.Iterator
                return &RegionIterator{
                    ctx:    ctx,
                    iter:   store.db.NewIteratorWithPrefix(prefix),
                    prefix: prefix,
                }
            }
        }
    }
    // Convert to interfaces.Iterator
    return &RegionIterator{
        ctx:    ctx,
        iter:   s.db.NewIteratorWithPrefix(prefix),
        prefix: prefix,
    }
}

// Update balance operations to be region-aware if needed
func (s *StateManager) AddBalance(
    ctx context.Context, 
    addr codec.Address, 
    st state.Mutable,
    amount uint64,
    createAccount bool,
) error {
    // Balance operations remain global, not region-specific
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

    store, err := s.getRegionalStoreInternal(regionID, mu)
    if err != nil {
        return nil, fmt.Errorf("failed to get regional store: %w", err)
    }

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

    store, err := s.getRegionalStoreInternal(obj.RegionID, mu)
    if err != nil {
        return fmt.Errorf("failed to get regional store: %w", err)
    }

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

    store, err := s.getRegionalStoreInternal(regionID, mu)
    if err != nil {
        return false, fmt.Errorf("failed to get regional store: %w", err)
    }

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

    store, err := s.getRegionalStoreInternal(regionID, mu)
    if err != nil {
        return fmt.Errorf("failed to get regional store: %w", err)
    }

    eventKey := makeRegionKey(regionID, "event", id)
    value, err := marshalState(event)
    if err != nil {
        return fmt.Errorf("failed to marshal event: %w", err)
    }

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

    // Use internal method to get store with mutable state
    store, err := s.getRegionalStoreInternal(regionID, mu)
    if err != nil {
        return nil, fmt.Errorf("failed to get regional store: %w", err)
    }

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

    // Use internal method to get store with mutable state
    store, err := s.getRegionalStoreInternal(regionID, mu)
    if err != nil {
        return fmt.Errorf("failed to get regional store: %w", err)
    }

    return SetRegion(ctx, store, regionID, region)
}

func (s *StateManager) LoadRegion(ctx context.Context, id string) (*interfaces.Region, error) {
    key := []byte(fmt.Sprintf("region/%s", id))
    data, err := s.GetValue(ctx, key)
    if err != nil {
        return nil, err
    }
    
    var region interfaces.Region
    if err := json.Unmarshal(data, &region); err != nil {
        return nil, err
    }
    return &region, nil
}

func (s *StateManager) SaveRegion(ctx context.Context, region *interfaces.Region) error {
    data, err := json.Marshal(region)
    if err != nil {
        return fmt.Errorf("failed to marshal region: %w", err)
    }
    
    key := []byte(fmt.Sprintf("region/%s", region.ID))
    // Use Insert instead of GetValue since we're saving data
    if err := s.Insert(ctx, key, data); err != nil {
        return fmt.Errorf("failed to save region: %w", err)
    }
    
    return nil
}

// Add this wrapper type to combine RegionalStore and state.Mutable
type MutableWrapper struct {
    regionalStore core.RegionalStore
    mutable      state.Mutable
}

// Implement state.Mutable interface
func (w *MutableWrapper) GetValue(ctx context.Context, key []byte) ([]byte, error) {
    return w.regionalStore.GetValue(ctx, key)
}

func (w *MutableWrapper) Insert(ctx context.Context, key []byte, value []byte) error {
    return w.regionalStore.Insert(ctx, key, value)
}

func (w *MutableWrapper) Remove(ctx context.Context, key []byte) error {
    return w.regionalStore.Remove(ctx, key)
}

func (w *MutableWrapper) Get(ctx context.Context, key []byte) ([]byte, error) {
    return w.regionalStore.Get(ctx, key)
}

func (w *MutableWrapper) Delete(ctx context.Context, key []byte) error {
    return w.regionalStore.Delete(ctx, key)
}

func (w *MutableWrapper) GetProof(ctx context.Context, key []byte) (*merkledb.Proof, error) {
    return w.regionalStore.GetProof(ctx, key)
}

func (w *MutableWrapper) VerifyProof(ctx context.Context, proof *merkledb.Proof) error {
    return w.regionalStore.VerifyProof(ctx, proof)
}

func (w *MutableWrapper) GetRegionID() string {
    return w.regionalStore.GetRegionID()
}

func (w *MutableWrapper) GetRoot(ctx context.Context) ([]byte, error) {
    return w.regionalStore.GetRoot(ctx)
}

func (s *StateManager) RegionExists(
    ctx context.Context,
    mu state.Mutable,
    regionID string,
) (bool, error) {
    if regionID == "" {
        return false, ErrInvalidRegionID
    }

    store, err := s.getRegionalStoreInternal(regionID, mu)
    if err != nil {
        return false, fmt.Errorf("failed to get regional store: %w", err)
    }

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
    isRegional, regionID := s.IsRegionalKey(key)
    if isRegional {
        store, err := s.regionalManager.GetRegionalStore(regionID)
        if err != nil {
            return nil, err
        }
        return store.Get(ctx, key)
    }
    return s.backingStore.GetValue(ctx, key)
}

// Update Insert to use regionalManager
func (s *StateManager) Insert(ctx context.Context, key []byte, value []byte) error {
    isRegional, regionID := s.IsRegionalKey(key)
    if isRegional {
        store, err := s.regionalManager.GetRegionalStore(regionID)
        if err != nil {
            return err
        }
        return store.Insert(ctx, key, value)
    }
    return s.backingStore.Insert(ctx, key, value)
}

// Update Remove to use regionalManager
func (s *StateManager) Remove(ctx context.Context, key []byte) error {
    isRegional, regionID := s.IsRegionalKey(key)
    if isRegional {
        store, err := s.regionalManager.GetRegionalStore(regionID)
        if err != nil {
            return err
        }
        return store.Delete(ctx, key)
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

func (s *StateManager) GetRegionalStore(regionID string) (core.RegionalStore, error) {
    return s.getRegionalStoreInternal(regionID, nil)
}

// Internal method that maintains the variadic state.Mutable parameter
func (s *StateManager) getRegionalStoreInternal(regionID string, mu ...state.Mutable) (core.RegionalStore, error) {
    s.regionMu.RLock()
    store, exists := s.regionStores[regionID]

    // First try using cached store
    if exists {
        s.regionMu.RUnlock()
        adapter := &RegionalStoreAdapter{
            store: store,
            baseDB: s.db,
        }
        
        // If mutable was provided, wrap it
        if len(mu) > 0 {
            return &MutableWrapper{
                regionalStore: adapter,
                mutable:      mu[0],
            }, nil
        }
        return adapter, nil
    }
    s.regionMu.RUnlock()

    // If not exists, create new store with lock
    s.regionMu.Lock()
    defer s.regionMu.Unlock()

    // Double-check after acquiring write lock
    if store, exists = s.regionStores[regionID]; exists {
        adapter := &RegionalStoreAdapter{
            store: store,
            baseDB: s.db,
        }
        
        // If mutable was provided, wrap it
        if len(mu) > 0 {
            return &MutableWrapper{
                regionalStore: adapter,
                mutable:      mu[0],
            }, nil
        }
        return adapter, nil
    }

    // Get TEE pair using exported method
    teePair, err := s.coordinator.GetTEEPair(regionID)
    if err != nil {
        return nil, fmt.Errorf("failed to get TEE pair: %w", err)
    }

    // Create new MerkleStore
    store = NewMerkleStore(
        s.merkleDB,
        regionID,
        s.coordinator,
        teePair,
    )

    // Cache the store
    s.regionStores[regionID] = store

    // Create adapter
    adapter := &RegionalStoreAdapter{
        store: store,
        baseDB: s.db,
    }

    // If mutable was provided, wrap it
    if len(mu) > 0 {
        return &MutableWrapper{
            regionalStore: adapter,
            mutable:      mu[0],
        }, nil
    }

    // Return wrapped store
    return adapter, nil
}


// Add this adapter type to bridge between MerkleStore and core.RegionalStore
type RegionalStoreAdapter struct {
    store   *MerkleStore
    baseDB  database.Database
}

func (r *RegionalStoreAdapter) GetValue(ctx context.Context, key []byte) ([]byte, error) {
    return r.store.Get(ctx, key)
}

func (r *RegionalStoreAdapter) Insert(ctx context.Context, key []byte, value []byte) error {
    return r.store.Insert(ctx, key, value)
}

func (r *RegionalStoreAdapter) Remove(ctx context.Context, key []byte) error {
    return r.store.Delete(ctx, key)
}

// Implement RegionalStore methods
func (r *RegionalStoreAdapter) Get(ctx context.Context, key []byte) ([]byte, error) {
    return r.store.Get(ctx, key)
}

func (r *RegionalStoreAdapter) Delete(ctx context.Context, key []byte) error {
    return r.store.Delete(ctx, key)
}

func (r *RegionalStoreAdapter) GetProof(ctx context.Context, key []byte) (*merkledb.Proof, error) {
    return r.store.GetProof(ctx, key)
}

func (r *RegionalStoreAdapter) VerifyProof(ctx context.Context, proof *merkledb.Proof) error {
    return r.store.VerifyProof(ctx, proof)
}

func (r *RegionalStoreAdapter) GetRegionID() string {
    return r.store.regionID
}

func (r *RegionalStoreAdapter) GetRoot(ctx context.Context) ([]byte, error) {
    return r.store.GetRoot(ctx)
}



func (m *MerkleStore) VerifyProof(ctx context.Context, proof *merkledb.Proof) error {
    // Implement proof verification
    return nil
}

func (m *MerkleStore) GetRegionID() string {
    return m.regionID
}

func (m *MerkleStore) GetRoot(ctx context.Context) ([]byte, error) {
    root, err := m.db.GetMerkleRoot(ctx)
    if err != nil {
        return nil, err
    }
    return root[:], nil
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
func (s *StateManager) IsRegionalKey(key []byte) (bool, string) {
    if bytes.HasPrefix(key, []byte("r/")) {
        parts := bytes.SplitN(key, []byte("/"), 3)
        if len(parts) >= 2 {
            return true, string(parts[1])
        }
    }
    return false, ""
}

func extractRegionID(key []byte) string {
    parts := bytes.Split(key, []byte("/"))
    if len(parts) < 3 {
        return ""
    }
    return string(parts[1])
}

type DatabaseWrapper struct {
    db database.Database
}

func NewDatabaseWrapper(db database.Database) *DatabaseWrapper {
    return &DatabaseWrapper{db: db}
}

func (d *DatabaseWrapper) GetValue(ctx context.Context, key []byte) ([]byte, error) {
    return d.db.Get(key)
}

func (d *DatabaseWrapper) Insert(ctx context.Context, key []byte, value []byte) error {
    return d.db.Put(key, value)
}

func (d *DatabaseWrapper) Remove(ctx context.Context, key []byte) error {
    return d.db.Delete(key)
}

func (d *DatabaseWrapper) Close() error {
    return d.db.Close()
}

func (d *DatabaseWrapper) Has(key []byte) (bool, error) {
    return d.db.Has(key)
}

func (d *DatabaseWrapper) Get(key []byte) ([]byte, error) {
    return d.db.Get(key)
}

func (d *DatabaseWrapper) Put(key []byte, value []byte) error {
    return d.db.Put(key, value)
}

func (d *DatabaseWrapper) Delete(key []byte) error {
    return d.db.Delete(key)
}

func (d *DatabaseWrapper) NewBatch() database.Batch {
    return d.db.NewBatch()
}

func (d *DatabaseWrapper) NewIterator() database.Iterator {
    return d.db.NewIterator()
}

func (d *DatabaseWrapper) NewIteratorWithPrefix(prefix []byte) database.Iterator {
    return d.db.NewIteratorWithPrefix(prefix)
}

func (d *DatabaseWrapper) NewIteratorWithStart(start []byte) database.Iterator {
    return d.db.NewIteratorWithStart(start)
}


func (d *DatabaseWrapper) NewIteratorWithStartAndPrefix(start, prefix []byte) database.Iterator {
    return d.db.NewIteratorWithStartAndPrefix(start, prefix)
}

func (d *DatabaseWrapper) Compact(start []byte, limit []byte) error {
    if compacter, ok := d.db.(interface{ Compact([]byte, []byte) error }); ok {
        return compacter.Compact(start, limit)
    }
    return nil
}

func (d *DatabaseWrapper) HealthCheck(ctx context.Context) (interface{}, error) {
    if healthChecker, ok := d.db.(interface{ HealthCheck(context.Context) (interface{}, error) }); ok {
        return healthChecker.HealthCheck(ctx)
    }
    return nil, nil
}

func (s *StateManager) DeleteRegion(ctx context.Context, id string) error {
    s.regionMu.Lock()
    defer s.regionMu.Unlock()
    delete(s.regionStores, id)
    // Also remove from backing store
    key := []byte(fmt.Sprintf("region/%s", id))
    return s.backingStore.Remove(ctx, key)
}

// Verify StateManager implements all required interfaces
var (
    _ chain.StateManager = (*StateManager)(nil)
    _ state.Mutable = (*StateManager)(nil)
)

var _ state.Mutable = (*MerkleStore)(nil)

func (s *StateManager) GetKeysByPrefix(ctx context.Context, prefix []byte) ([][]byte, error) {
    iter := s.db.NewIteratorWithPrefix(prefix)
    defer iter.Release()
    
    var keys [][]byte
    for iter.Next() {
        key := make([]byte, len(iter.Key()))
        copy(key, iter.Key())
        keys = append(keys, key)
    }
    return keys, iter.Error()
}

// Add this method to handle the old signature
func (s *StateManager) GetRegionalStoreWithMutable(regionID string, mu state.Mutable) (*MerkleStore, error) {
    store, err := s.GetRegionalStore(regionID)
    if err != nil {
        return nil, err
    }
    
    // Convert back to MerkleStore if needed
    if adapter, ok := store.(*RegionalStoreAdapter); ok {
        return adapter.store, nil
    }
    
    return nil, fmt.Errorf("invalid store type")
}

func (s *StateManager) GetBaseStorage() coordination.BaseStorage {
    return NewCoordinationStorageWrapper(NewDatabaseWrapper(s.db))
}

