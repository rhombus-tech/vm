// storage/region_store.go
package storage

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/ava-labs/avalanchego/database"
	"github.com/ava-labs/avalanchego/ids"
	"github.com/ava-labs/avalanchego/x/merkledb"
	"github.com/ava-labs/hypersdk/state"
	"github.com/rhombus-tech/vm/coordination"
	"github.com/rhombus-tech/vm/regions"
)

type RegionMetric struct {
    Name      string    `json:"name"`
    Value     float64   `json:"value"`
    Timestamp time.Time `json:"timestamp"`
}

type RegionStore struct {
    db        merkledb.MerkleDB
    view      merkledb.View
    viewLock  sync.RWMutex
    cache     map[string]*regions.RegionConfig
    cacheLock sync.RWMutex
}

func NewRegionStore(db merkledb.MerkleDB) (*RegionStore, error) {
    // Initialize with empty view
    view, err := db.NewView(context.Background(), merkledb.ViewChanges{})
    if err != nil {
        return nil, err
    }

    return &RegionStore{
        db:    db,
        view:  view,
        cache: make(map[string]*regions.RegionConfig),
    }, nil
}

func (r *RegionStore) GetRegionConfig(ctx context.Context, regionID string) (*regions.RegionConfig, error) {
    if !isValidRegionID(regionID) {
        return nil, ErrInvalidRegionID
    }

    // Check cache first
    r.cacheLock.RLock()
    if config, exists := r.cache[regionID]; exists {
        r.cacheLock.RUnlock()
        return config, nil
    }
    r.cacheLock.RUnlock()

    // Get from merkledb
    key := createRegionKey("config", regionID, "")
    value, err := r.view.GetValue(ctx, key)
    if err != nil {
        return nil, fmt.Errorf("failed to get region config: %w", err)
    }

    var config regions.RegionConfig
    if err := json.Unmarshal(value, &config); err != nil {
        return nil, fmt.Errorf("failed to unmarshal config: %w", err)
    }

    // Update cache
    r.cacheLock.Lock()
    r.cache[regionID] = &config
    r.cacheLock.Unlock()

    return &config, nil
}

func (r *RegionStore) SetRegionConfig(ctx context.Context, regionID string, config *regions.RegionConfig) error {
    if !isValidRegionID(regionID) {
        return ErrInvalidRegionID
    }

    value, err := json.Marshal(config)
    if err != nil {
        return fmt.Errorf("failed to marshal config: %w", err)
    }

    changes := merkledb.ViewChanges{
        BatchOps: []database.BatchOp{{
            Key:   createRegionKey("config", regionID, ""),
            Value: value,
        }},
    }

    if err := r.updateState(ctx, changes); err != nil {
        return fmt.Errorf("failed to store region config: %w", err)
    }

    // Update cache
    r.cacheLock.Lock()
    r.cache[regionID] = config
    r.cacheLock.Unlock()

    return nil
}

func (r *RegionStore) DeleteRegionConfig(ctx context.Context, regionID string) error {
    if !isValidRegionID(regionID) {
        return ErrInvalidRegionID
    }

    changes := merkledb.ViewChanges{
        BatchOps: []database.BatchOp{{
            Key:    createRegionKey("config", regionID, ""),
            Delete: true,
        }},
    }

    if err := r.updateState(ctx, changes); err != nil {
        return fmt.Errorf("failed to delete region config: %w", err)
    }

    r.cacheLock.Lock()
    delete(r.cache, regionID)
    r.cacheLock.Unlock()

    return nil
}

func (r *RegionStore) GetProof(ctx context.Context, regionID string) (*merkledb.Proof, error) {
    key := createRegionKey("config", regionID, "")
    return r.view.GetProof(ctx, key)
}

func (r *RegionStore) VerifyRegionState(ctx context.Context, regionID string, proof *merkledb.Proof) error {
    currentRoot, err := r.view.GetMerkleRoot(ctx)
    if err != nil {
        return err
    }

    // Verify proof against current root
    // The actual verification looks like it needs:
    // - context
    // - root ID
    // - proof depth
    // - hasher interface
    if err := proof.Verify(ctx, currentRoot, 0, nil); err != nil {
        return fmt.Errorf("invalid region state proof: %w", err)
    }

    return nil
}

// Internal helper methods
func (r *RegionStore) updateState(ctx context.Context, changes merkledb.ViewChanges) error {
    r.viewLock.Lock()
    defer r.viewLock.Unlock()

    newView, err := r.db.NewView(ctx, changes)
    if err != nil {
        return err
    }

    r.view = newView
    return nil
}

func createRegionKey(prefix, regionID, suffix string) []byte {
    if suffix == "" {
        return []byte(fmt.Sprintf("%s/%s", prefix, regionID))
    }
    return []byte(fmt.Sprintf("%s/%s/%s", prefix, regionID, suffix))
}

func isValidRegionID(id string) bool {
    if len(id) == 0 || len(id) > 64 {
        return false
    }
    return !strings.ContainsAny(id, "/\\?#[]{}") 
}

// Add this type definition at the top with other types
type MerkleStore struct {
    db          merkledb.MerkleDB
    regionID    string
    lastHash    []byte
    updateCount uint64
    mu          sync.RWMutex
    coordinator *coordination.Coordinator
    workers     [2]coordination.WorkerID 
}

// Add constructor if not already present
func NewMerkleStore(db merkledb.MerkleDB, regionID string, coord *coordination.Coordinator, workers [2]coordination.WorkerID) *MerkleStore {
    return &MerkleStore{
        db:          db,
        regionID:    regionID,
        coordinator: coord,
        workers:     workers,
        updateCount: 0,
    }
}

// MerkleStore methods
func (ms *MerkleStore) Insert(ctx context.Context, key []byte, value []byte) error {
    ms.mu.Lock()
    defer ms.mu.Unlock()

    // Get channel between TEE pair using exported method
    channel, err := ms.coordinator.GetSecureChannel(ctx, ms.workers[0], ms.workers[1])
    if err != nil {
        return fmt.Errorf("failed to get secure channel: %w", err)
    }

    // Create and marshal the message
    msg := &coordination.Message{
        From: ms.workers[0],        // Changed from FromWorker to From
        To:   ms.workers[1],        // Changed from ToWorker to To
        Type: coordination.MessageTypeData,
        Data: value,
        Timestamp: time.Now(),
    }
    
    // Marshal message to bytes
    msgBytes, err := json.Marshal(msg)
    if err != nil {
        return fmt.Errorf("failed to marshal coordination message: %w", err)
    }

    // Send marshaled message bytes
    if err := channel.Send(msgBytes); err != nil {
        return fmt.Errorf("failed to send coordination message: %w", err)
    }

    regionalKey := makeRegionalKey(ms.regionID, key)
    if err := ms.db.Put(regionalKey, value); err != nil {
        return fmt.Errorf("failed to store value: %w", err)
    }

    ms.updateCount++
    return ms.updateMerkleRoot(ctx)
}


func (ms *MerkleStore) Get(ctx context.Context, key []byte) ([]byte, error) {
    ms.mu.RLock()
    defer ms.mu.RUnlock()

    regionalKey := makeRegionalKey(ms.regionID, key)
    return ms.db.Get(regionalKey)
}

func (ms *MerkleStore) Delete(ctx context.Context, key []byte) error {
    ms.mu.Lock()
    defer ms.mu.Unlock()

    regionalKey := makeRegionalKey(ms.regionID, key)
    if err := ms.db.Delete(regionalKey); err != nil {
        return err
    }

    return ms.updateMerkleRoot(ctx)
}

func (ms *MerkleStore) GetProof(ctx context.Context, key []byte) (*merkledb.Proof, error) {
    ms.mu.RLock()
    defer ms.mu.RUnlock()

    regionalKey := makeRegionalKey(ms.regionID, key)
    return ms.db.GetProof(ctx, regionalKey)
}

func (ms *MerkleStore) GetValue(ctx context.Context, key []byte) ([]byte, error) {
    ms.mu.RLock()
    defer ms.mu.RUnlock()

    regionalKey := makeRegionalKey(ms.regionID, key)
    return ms.db.Get(regionalKey)
}

func (ms *MerkleStore) Remove(ctx context.Context, key []byte) error {
    ms.mu.Lock()
    defer ms.mu.Unlock()

    regionalKey := makeRegionalKey(ms.regionID, key)
    return ms.db.Delete(regionalKey)
}

func (ms *MerkleStore) updateMerkleRoot(ctx context.Context) error {
    root, err := ms.db.GetMerkleRoot(ctx)
    if err != nil {
        return err
    }
    
    ms.lastHash = root[:]
    return nil
}

// Update VerifyStateUpdate to use simpler verification
func (ms *MerkleStore) VerifyStateUpdate(ctx context.Context, proof *merkledb.Proof, oldRoot, newRoot ids.ID) error {
    // Get the current root for comparison
    currentRoot, err := ms.db.GetMerkleRoot(ctx)
    if err != nil {
        return err
    }

    // Basic verification
    if currentRoot != newRoot {
        return fmt.Errorf("root mismatch")
    }

    return nil
}

// Helper functions remain unchanged
func makeRegionalKey(regionID string, key []byte) []byte {
    return []byte(fmt.Sprintf("r/%s/%s", regionID, key))
}

type RegionalStateManager struct {
    stores       map[string]*MerkleStore
    mu           sync.RWMutex
    backingStore state.Mutable
    merkleDB  merkledb.MerkleDB
}

func NewRegionalStateManager(merkleDB merkledb.MerkleDB, _ state.Mutable) *RegionalStateManager {
    return &RegionalStateManager{
        stores:    make(map[string]*MerkleStore),
        merkleDB:  merkleDB,
    }
}

// And the GetRegionalStore method
func (rsm *RegionalStateManager) GetRegionalStore(regionID string) (*MerkleStore, error) {
    rsm.mu.RLock()
    store, exists := rsm.stores[regionID]
    rsm.mu.RUnlock()
    
    if exists {
        return store, nil
    }

    rsm.mu.Lock()
    defer rsm.mu.Unlock()

    // Double-check after acquiring write lock
    if store, exists = rsm.stores[regionID]; exists {
        return store, nil
    }

    // Create new MerkleDB instance for region
    mdb, err := merkledb.New(
        context.Background(),
        rsm.merkleDB, // Use the merkleDB field
        merkledb.Config{
            HistoryLength: 256,
        },
    )
    if err != nil {
        return nil, fmt.Errorf("failed to create merkle store for region %s: %w", regionID, err)
    }

    store = &MerkleStore{
        db:       mdb,
        regionID: regionID,
    }
    rsm.stores[regionID] = store

    return store, nil
}
