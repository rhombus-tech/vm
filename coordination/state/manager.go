package state

import (
    "context"
    "errors"
    "sync"
    "time"

    "github.com/ava-labs/avalanchego/ids"
    "github.com/ava-labs/avalanchego/database"
    "github.com/ava-labs/avalanchego/x/merkledb"
)

var (
    ErrSyncTimeout = errors.New("state sync timeout")
    ErrInvalidProof = errors.New("invalid merkle proof")
)

// StateStore defines the interface for merkledb-backed state storage
type StateStore interface {
    Get(ctx context.Context, key []byte) ([]byte, error)
    GetProof(ctx context.Context, key []byte) (*merkledb.Proof, error)
    GetRoot(ctx context.Context) (ids.ID, error)
    VerifyProof(key []byte, value []byte, proof *merkledb.Proof) error
    NewView(ctx context.Context, changes merkledb.ViewChanges) error
}

// StateAccess tracks how state is accessed during coordination
type StateAccess struct {
    ReadKeys    map[string]time.Time
    WriteKeys   map[string]time.Time
    LastAccess  time.Time
}

// CoordStateManager manages state access for coordinated execution
type CoordStateManager struct {
    store      StateStore
    syncer     *StateSyncer
    
    // Track accessed state
    accessed   map[string]*StateAccess
    accessLock sync.RWMutex

    // Coordination
    coordLock  sync.RWMutex
    height     uint64
    root       ids.ID

    // Configuration
    syncTimeout time.Duration
}

// NewCoordStateManager creates a new coordinated state manager
func NewCoordStateManager(store StateStore, syncTimeout time.Duration) *CoordStateManager {
    return &CoordStateManager{
        store:       store,
        syncer:      NewStateSyncer(store),
        accessed:    make(map[string]*StateAccess),
        syncTimeout: syncTimeout,
    }
}

// GetValue gets a value ensuring coordination requirements
func (m *CoordStateManager) GetValue(ctx context.Context, key []byte) ([]byte, error) {
    // Track access
    m.trackAccess(key, false)

    // Get value from store
    value, err := m.store.Get(ctx, key)
    if err != nil {
        return nil, err
    }

    // Get and verify proof
    proof, err := m.store.GetProof(ctx, key)
    if err != nil {
        return nil, err
    }

    if err := m.store.VerifyProof(key, value, proof); err != nil {
        return nil, ErrInvalidProof
    }

    return value, nil
}

// SyncState synchronizes state with other workers
func (m *CoordStateManager) SyncState(ctx context.Context, workers []string, keys [][]byte) error {
    ctx, cancel := context.WithTimeout(ctx, m.syncTimeout)
    defer cancel()

    // Get current state root
    root, err := m.store.GetRoot(ctx)
    if err != nil {
        return err
    }

    // Create sync request
    req := &SyncRequest{
        Root:     root,
        Keys:     keys,
        Workers:  workers,
    }

    // Execute sync
    if err := m.syncer.Sync(ctx, req); err != nil {
        return err
    }

    return nil
}

// UpdateRoot updates the current state root
func (m *CoordStateManager) UpdateRoot(height uint64, root ids.ID) {
    m.coordLock.Lock()
    defer m.coordLock.Unlock()

    m.height = height
    m.root = root
}

// GetStateRoot gets the current state root
func (m *CoordStateManager) GetStateRoot() ids.ID {
    m.coordLock.RLock()
    defer m.coordLock.RUnlock()
    return m.root
}

// GetHeight gets current state height
func (m *CoordStateManager) GetHeight() uint64 {
    m.coordLock.RLock()
    defer m.coordLock.RUnlock()
    return m.height 
}

// GetAccessed gets keys accessed during coordination
func (m *CoordStateManager) GetAccessed() map[string]*StateAccess {
    m.accessLock.RLock()
    defer m.accessLock.RUnlock()

    // Make copy to avoid concurrent access
    accessed := make(map[string]*StateAccess, len(m.accessed))
    for k, v := range m.accessed {
        access := &StateAccess{
            ReadKeys:   make(map[string]time.Time),
            WriteKeys:  make(map[string]time.Time),
            LastAccess: v.LastAccess,
        }
        for rk, rt := range v.ReadKeys {
            access.ReadKeys[rk] = rt
        }
        for wk, wt := range v.WriteKeys {
            access.WriteKeys[wk] = wt
        }
        accessed[k] = access
    }

    return accessed
}

// Internal methods

func (m *CoordStateManager) trackAccess(key []byte, isWrite bool) {
    m.accessLock.Lock()
    defer m.accessLock.Unlock()

    keyStr := string(key)
    access, exists := m.accessed[keyStr]
    if !exists {
        access = &StateAccess{
            ReadKeys:  make(map[string]time.Time),
            WriteKeys: make(map[string]time.Time),
        }
        m.accessed[keyStr] = access
    }

    now := time.Now()
    if isWrite {
        access.WriteKeys[keyStr] = now
    } else {
        access.ReadKeys[keyStr] = now
    }
    access.LastAccess = now
}

// StateSyncer handles state synchronization between workers
type StateSyncer struct {
    store StateStore
}

type SyncRequest struct {
    Root     ids.ID
    Keys     [][]byte
    Workers  []string
}

func NewStateSyncer(store StateStore) *StateSyncer {
    return &StateSyncer{
        store: store,
    }
}

// Sync synchronizes state between workers
func (s *StateSyncer) Sync(ctx context.Context, req *SyncRequest) error {
    // For each key that needs syncing
    for _, key := range req.Keys {
        // Get value and proof
        value, err := s.store.Get(ctx, key) 
        if err != nil {
            return err
        }

        proof, err := s.store.GetProof(ctx, key)
        if err != nil {
            return err
        }

        // Verify proof matches root
        if err := s.store.VerifyProof(key, value, proof); err != nil {
            return err
        }
    }

    return nil
}

// StateChange represents a coordinated state change
type StateChange struct {
    updates   map[string][]byte
    deletions map[string]struct{}
}

// NewStateChange creates a new state change
func (m *CoordStateManager) NewStateChange() *StateChange {
    return &StateChange{
        updates:   make(map[string][]byte),
        deletions: make(map[string]struct{}),
    }
}

// Put adds a key-value pair to the state change
func (sc *StateChange) Put(key []byte, value []byte) {
    sc.updates[string(key)] = value
}

// Delete marks a key for deletion
func (sc *StateChange) Delete(key []byte) {
    sc.deletions[string(key)] = struct{}{}
}

// Commit applies the state changes
func (m *CoordStateManager) CommitChanges(ctx context.Context, changes *StateChange) error {
    // Create view changes
    ops := make([]database.BatchOp, 0, len(changes.updates)+len(changes.deletions))  // Changed type to database.BatchOp

    // Add updates
    for key, value := range changes.updates {
        m.trackAccess([]byte(key), true)
        ops = append(ops, database.BatchOp{  // Using database.BatchOp
            Key:   []byte(key),
            Value: value,
        })
    }

    // Add deletions
    for key := range changes.deletions {
        m.trackAccess([]byte(key), true)
        ops = append(ops, database.BatchOp{  // Using database.BatchOp
            Key:    []byte(key),
            Delete: true,
        })
    }

    // Create and apply view changes
    return m.store.NewView(ctx, merkledb.ViewChanges{
        BatchOps: ops,
        ConsumeBytes: false,
    })
}

// BatchOp represents an operation in a batch
type BatchOp struct {
    Key    []byte
    Value  []byte
    Delete bool
}

// StateBatch provides a way to batch multiple operations efficiently
type StateBatch struct {
    ops []database.BatchOp  // Using database.BatchOp type
}

// NewBatch creates a new batch of state changes
func (m *CoordStateManager) NewBatch() *StateBatch {
    return &StateBatch{
        ops: make([]database.BatchOp, 0),
    }
}

// Put adds a key-value pair to the batch
func (b *StateBatch) Put(key, value []byte) {
    b.ops = append(b.ops, database.BatchOp{
        Key:    key,
        Value:  value,
        Delete: false,
    })
}

// Delete marks a key for deletion in the batch
func (b *StateBatch) Delete(key []byte) {
    b.ops = append(b.ops, database.BatchOp{
        Key:    key,
        Delete: true,
    })
}

// Size returns the number of operations in the batch
func (b *StateBatch) Size() int {
    return len(b.ops)
}

// CommitBatch commits all changes in a batch
func (m *CoordStateManager) CommitBatch(ctx context.Context, batch *StateBatch) error {
    // Track all accessed keys
    for _, op := range batch.ops {
        m.trackAccess(op.Key, true)
    }

    // Apply changes
    return m.store.NewView(ctx, merkledb.ViewChanges{
        BatchOps:     batch.ops,  // Using database.BatchOp type
        ConsumeBytes: false,
    })
}