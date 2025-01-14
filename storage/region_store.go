// storage/region_store.go
package storage

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/ava-labs/hypersdk/state"
	"github.com/rhombus-tech/vm/regions"
    "github.com/ava-labs/avalanchego/x/merkledb"
)

// RegionMetric defines a region metric
type RegionMetric struct {
    Name      string    `json:"name"`
    Value     float64   `json:"value"`
    Timestamp time.Time `json:"timestamp"`
}

type RegionStateStore struct {
    stateManager *StateManager // Change to pointer to fix lock by value
}

func NewRegionStateStore(sm *StateManager) *RegionStateStore {
    return &RegionStateStore{
        stateManager: sm,
    }
}

// createRegionKey creates a key for region storage
func (r *RegionStateStore) createRegionKey(regionID string, suffix string) []byte {
    return []byte(fmt.Sprintf("region/%s/%s", regionID, suffix))
}

// GetRegionConfig retrieves a region's configuration
func (r *RegionStateStore) GetRegionConfig(regionID string) (*regions.RegionConfig, error) {
    ctx := context.Background()
    if !isValidRegionID(regionID) {
        return nil, ErrInvalidRegionID
    }

    key := r.createRegionKey(regionID, "config")
    value, err := (*r.stateManager).GetValue(ctx, key)
    if err != nil {
        return nil, fmt.Errorf("failed to get region config: %w", err)
    }

    var config regions.RegionConfig
    if err := json.Unmarshal(value, &config); err != nil {
        return nil, fmt.Errorf("failed to unmarshal config: %w", err)
    }

    return &config, nil
}

// ListRegions returns all region IDs
func (r *RegionStateStore) ListRegions() ([]string, error) {
    ctx := context.Background()
    prefix := r.createRegionKey("", "")
    
    value, err := (*r.stateManager).GetValue(ctx, prefix)
    if err != nil {
        return nil, fmt.Errorf("failed to list regions: %w", err)
    }

    var regionList []string
    if err := json.Unmarshal(value, &regionList); err != nil {
        return nil, fmt.Errorf("failed to unmarshal region list: %w", err)
    }

    return regionList, nil
}

// SetRegionConfig stores a region configuration
func (r *RegionStateStore) SetRegionConfig(regionID string, config *regions.RegionConfig) error {
    ctx := context.Background()
    if !isValidRegionID(regionID) {
        return ErrInvalidRegionID
    }
    if config == nil {
        return fmt.Errorf("config cannot be nil") 
    }

    value, err := json.Marshal(config)
    if err != nil {
        return fmt.Errorf("failed to marshal config: %w", err)
    }

    key := r.createRegionKey(regionID, "config")
    if err := (*r.stateManager).Insert(ctx, key, value); err != nil {
        return fmt.Errorf("failed to store region config: %w", err)
    }
    return nil
}

// DeleteRegionConfig removes a region's configuration
func (r *RegionStateStore) DeleteRegionConfig(regionID string) error {
    ctx := context.Background()
    if !isValidRegionID(regionID) {
        return ErrInvalidRegionID
    }

    key := r.createRegionKey(regionID, "config")
    if err := (*r.stateManager).Remove(ctx, key); err != nil {
        return fmt.Errorf("failed to delete region config: %w", err)
    }
    return nil
}

// GetRegionStatus gets the current status of a region
func (r *RegionStateStore) GetRegionStatus(ctx context.Context, regionID string) (string, error) {
    if !isValidRegionID(regionID) {
        return "", ErrInvalidRegionID
    }

    key := r.createRegionKey(regionID, "status")
    value, err := (*r.stateManager).GetValue(ctx, key)
    if err != nil {
        return "", fmt.Errorf("failed to get region status: %w", err)
    }

    return string(value), nil
}

// SetRegionStatus updates a region's status
func (r *RegionStateStore) SetRegionStatus(ctx context.Context, regionID string, status string) error {
    if !isValidRegionID(regionID) {
        return ErrInvalidRegionID
    }

    key := r.createRegionKey(regionID, "status")
    if err := (*r.stateManager).Insert(ctx, key, []byte(status)); err != nil {
        return fmt.Errorf("failed to set region status: %w", err)
    }
    return nil
}


func isValidRegionID(id string) bool {
    if len(id) == 0 || len(id) > 64 {
        return false
    }
    return !strings.ContainsAny(id, "/\\?#[]{}") 
}

type RegionalStateManager struct {
    stores       map[string]*MerkleStore
    mu           sync.RWMutex
    backingStore state.Mutable
    db           merkledb.MerkleDB
}

type MerkleStore struct {
    db           merkledb.MerkleDB
    regionID     string
    lastHash     []byte
    updateCount  uint64
    mu           sync.RWMutex
}

func NewRegionalStateManager(db merkledb.MerkleDB, backing state.Mutable) *RegionalStateManager {
    return &RegionalStateManager{
        stores:       make(map[string]*MerkleStore),
        backingStore: backing,
        db:          db,
    }
}

func NewMerkleStore(db merkledb.MerkleDB, regionID string) *MerkleStore {
    return &MerkleStore{
        db:          db,
        regionID:    regionID,
        updateCount: 0,
    }
}

// Enhanced regional store management
func (rsm *RegionalStateManager) GetRegionalStore(regionID string) (*MerkleStore, error) {
    rsm.mu.RLock()
    store, exists := rsm.stores[regionID]
    rsm.mu.RUnlock()
    
    if exists {
        return store, nil
    }

    // Create new store if doesn't exist
    rsm.mu.Lock()
    defer rsm.mu.Unlock()
    
    // Double check after acquiring write lock
    if store, exists = rsm.stores[regionID]; exists {
        return store, nil
    }

    store = NewMerkleStore(rsm.db, regionID)
    rsm.stores[regionID] = store
    return store, nil
}

// Regional state operations
func (ms *MerkleStore) Insert(ctx context.Context, key []byte, value []byte) error {
    ms.mu.Lock()
    defer ms.mu.Unlock()

    // Create region-specific key
    regionalKey := makeRegionalKey(ms.regionID, key)
    
    if err := ms.db.Insert(regionalKey, value); err != nil {
        return fmt.Errorf("failed to insert: %w", err)
    }

    // Update merkle root after modification
    if err := ms.updateMerkleRoot(); err != nil {
        return fmt.Errorf("failed to update merkle root: %w", err)
    }

    ms.updateCount++
    return nil
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

    return ms.updateMerkleRoot()
}

// Merkle proof generation
func (ms *MerkleStore) GetProof(ctx context.Context, key []byte) (*merkledb.Proof, error) {
    ms.mu.RLock()
    defer ms.mu.RUnlock()

    regionalKey := makeRegionalKey(ms.regionID, key)
    return ms.db.GetProof(regionalKey)
}

// State verification
func (ms *MerkleStore) VerifyStateUpdate(proof *merkledb.Proof, oldRoot, newRoot []byte) error {
    if !bytes.Equal(proof.RootHash, oldRoot) {
        return fmt.Errorf("invalid old root hash")
    }

    // Verify the proof leads to new root
    if err := merkledb.VerifyProof(proof, newRoot); err != nil {
        return fmt.Errorf("proof verification failed: %w", err)
    }

    return nil
}

// Helper functions
func makeRegionalKey(regionID string, key []byte) []byte {
    return []byte(fmt.Sprintf("r/%s/%s", regionID, key))
}

func (ms *MerkleStore) updateMerkleRoot() error {
    root, err := ms.db.GetMerkleRoot()
    if err != nil {
        return err
    }
    ms.lastHash = root
    return nil
}

