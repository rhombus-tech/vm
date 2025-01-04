package state

import (
    "context"
    "sync"
    "time"

    "github.com/ava-labs/avalanchego/ids"
    "github.com/ava-labs/avalanchego/x/merkledb"
)

type MerkleStore struct {
    db        merkledb.MerkleDB
    view      merkledb.View
    
    cache     *stateCache
    cacheLock sync.RWMutex
}

type stateCache struct {
    entries map[string]cacheEntry
    size    int
}

type cacheEntry struct {
    value     []byte
    proof     *merkledb.Proof
    timestamp time.Time
}

// NewMerkleStore creates a new merkledb-backed store
func NewMerkleStore(db merkledb.MerkleDB) (*MerkleStore, error) {
    // Get initial view
    view, err := db.NewView(context.Background(), merkledb.ViewChanges{})
    if err != nil {
        return nil, err
    }

    return &MerkleStore{
        db:    db,
        view:  view,
        cache: &stateCache{
            entries: make(map[string]cacheEntry),
        },
    }, nil
}

// Get retrieves a value from merkledb
func (s *MerkleStore) Get(ctx context.Context, key []byte) ([]byte, error) {
    // Check cache
    if entry, ok := s.checkCache(key); ok {
        return entry.value, nil
    }

    value, err := s.view.GetValue(ctx, key)
    if err != nil {
        return nil, err
    }

    // Cache the value
    s.updateCache(key, value, nil)

    return value, nil
}

// GetProof gets a merkle proof
func (s *MerkleStore) GetProof(ctx context.Context, key []byte) (*merkledb.Proof, error) {
    // Check cache
    if entry, ok := s.checkCache(key); ok && entry.proof != nil {
        return entry.proof, nil
    }

    return s.view.GetProof(ctx, key)
}

// GetRoot gets current merkle root
func (s *MerkleStore) GetRoot(ctx context.Context) (ids.ID, error) {
    return s.view.GetMerkleRoot(ctx)
}

// Commit commits a set of changes
func (s *MerkleStore) Commit(ctx context.Context, changes merkledb.ViewChanges) error {
    newView, err := s.db.NewView(ctx, changes)
    if err != nil {
        return err
    }

    s.view = newView
    s.clearCache()

    return nil
}

// Helper methods

func (s *MerkleStore) checkCache(key []byte) (cacheEntry, bool) {
    s.cacheLock.RLock()
    defer s.cacheLock.RUnlock()
    
    entry, ok := s.cache.entries[string(key)]
    return entry, ok
}

func (s *MerkleStore) updateCache(key []byte, value []byte, proof *merkledb.Proof) {
    s.cacheLock.Lock()
    defer s.cacheLock.Unlock()
    
    s.cache.entries[string(key)] = cacheEntry{
        value:     value,
        proof:     proof,
        timestamp: time.Now(),
    }
}

func (s *MerkleStore) clearCache() {
    s.cacheLock.Lock()
    defer s.cacheLock.Unlock()
    s.cache.entries = make(map[string]cacheEntry)
}