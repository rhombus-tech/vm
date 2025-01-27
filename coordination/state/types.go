package state

import (
    "bytes"
    "context"
    "sync"

    "github.com/ava-labs/avalanchego/ids"
    "github.com/ava-labs/avalanchego/utils/maybe"
    "github.com/ava-labs/avalanchego/x/merkledb"
)

type MerkleStore struct {
    db        merkledb.MerkleDB
    view      merkledb.View
    
    cache     *RangeCache
    cacheLock sync.RWMutex
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
        cache: NewRangeCache(1000), // Cache up to 1000 range proofs
    }, nil
}

// Get retrieves a value from merkledb
func (s *MerkleStore) Get(ctx context.Context, key []byte) ([]byte, error) {
    return s.view.GetValue(ctx, key)
}

// GetProof gets a merkle proof
func (s *MerkleStore) GetProof(ctx context.Context, key []byte) (*merkledb.Proof, error) {
    return s.view.GetProof(ctx, key)
}

// GetRoot gets current merkle root
func (s *MerkleStore) GetRoot(ctx context.Context) (ids.ID, error) {
    return s.view.GetMerkleRoot(ctx)
}

// GetRangeProof retrieves a range proof for the specified key range
func (s *MerkleStore) GetRangeProof(ctx context.Context, start, end []byte) (*RangeProof, error) {
    // Check cache first
    root, err := s.GetRoot(ctx)
    if err != nil {
        return nil, err
    }

    s.cacheLock.RLock()
    if proof, ok := s.cache.Get(start, end, root); ok {
        s.cacheLock.RUnlock()
        return proof, nil
    }
    s.cacheLock.RUnlock()

    // Get merkle proof for the range
    proof, err := s.view.GetRangeProof(ctx, maybe.Some(start), maybe.Some(end), defaultMaxKeyLen)
    if err != nil {
        return nil, err
    }

    // Get all values in the range using merkledb's iterator
    entries := make(map[string][]byte)
    iter := s.view.NewIterator()
    defer iter.Release()

    // Manually iterate through the range
    for iter.Next() {
        key := iter.Key()
        if bytes.Compare(key, start) < 0 {
            continue
        }
        if end != nil && bytes.Compare(key, end) > 0 {
            break
        }
        
        value := iter.Value()
        entries[string(key)] = value
    }

    if err := iter.Error(); err != nil {
        return nil, err
    }

    rangeProof := &RangeProof{
        StartKey: start,
        EndKey:   end,
        Entries:  entries,
        Proof:    proof,
    }

    // Cache the proof
    s.cacheLock.Lock()
    s.cache.Put(rangeProof, root)
    s.cacheLock.Unlock()

    return rangeProof, nil
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
func (s *MerkleStore) clearCache() {
    s.cacheLock.Lock()
    defer s.cacheLock.Unlock()
    s.cache = NewRangeCache(1000)
}