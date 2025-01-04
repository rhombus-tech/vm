package coordination

import (
    "context"
    "encoding/json"
    "sync"

    "github.com/ava-labs/avalanchego/database"
    "github.com/ava-labs/avalanchego/x/merkledb"
)

// Storage handles persistent storage for coordination state
type Storage interface {
    // Store coordination data
    Put(ctx context.Context, key []byte, value []byte) error
    Get(ctx context.Context, key []byte) ([]byte, error)
    Delete(ctx context.Context, key []byte) error

    // Store channel state
    SaveChannel(ctx context.Context, channel *SecureChannel) error
    LoadChannel(ctx context.Context, worker1, worker2 WorkerID) (*SecureChannel, error)
    DeleteChannel(ctx context.Context, worker1, worker2 WorkerID) error

    // Store worker state
    SaveWorker(ctx context.Context, worker *Worker) error
    LoadWorker(ctx context.Context, id WorkerID) (*Worker, error)
    DeleteWorker(ctx context.Context, id WorkerID) error

    // View management
    NewView(ctx context.Context, changes merkledb.ViewChanges) error

    // Lifecycle
    Close() error
}

// MerkleStorage implements Storage using merkledb
type MerkleStorage struct {
    db        merkledb.MerkleDB
    view      merkledb.View
    cache     *storageCache
}

type storageCache struct {
    channels map[string]*SecureChannel
    workers  map[WorkerID]*Worker
    mu       sync.RWMutex
}

// NewStorage creates a new storage instance
func NewStorage(db merkledb.MerkleDB) (Storage, error) {
    // Get initial view with empty changes
    view, err := db.NewView(context.Background(), merkledb.ViewChanges{})
    if err != nil {
        return nil, err
    }

    return &MerkleStorage{
        db:    db,
        view:  view,
        cache: &storageCache{
            channels: make(map[string]*SecureChannel),
            workers:  make(map[WorkerID]*Worker),
        },
    }, nil
}

// Add to MerkleStorage implementation
func (s *MerkleStorage) NewView(ctx context.Context, changes merkledb.ViewChanges) error {
    newView, err := s.db.NewView(ctx, changes)
    if err != nil {
        return err
    }

    s.view = newView
    return nil
}

// Put stores a value
func (s *MerkleStorage) Put(ctx context.Context, key []byte, value []byte) error {
    changes := merkledb.ViewChanges{
        BatchOps: []database.BatchOp{
            {
                Key:   key,
                Value: value,
            },
        },
    }
    
    newView, err := s.db.NewView(ctx, changes)
    if err != nil {
        return err
    }

    s.view = newView
    return nil
}

// Get retrieves a value
func (s *MerkleStorage) Get(ctx context.Context, key []byte) ([]byte, error) {
    return s.view.GetValue(ctx, key)
}

// Delete removes a value
func (s *MerkleStorage) Delete(ctx context.Context, key []byte) error {
    changes := merkledb.ViewChanges{
        BatchOps: []database.BatchOp{
            {
                Key:    key,
                Delete: true,
            },
        },
    }
    
    newView, err := s.db.NewView(ctx, changes)
    if err != nil {
        return err
    }

    s.view = newView
    return nil
}

// SaveChannel persists channel state
func (s *MerkleStorage) SaveChannel(ctx context.Context, channel *SecureChannel) error {
    s.cache.mu.Lock()
    defer s.cache.mu.Unlock()

    key := makeChannelKey(channel.worker1, channel.worker2)
    s.cache.channels[key] = channel

    // Marshal channel state
    data, err := json.Marshal(channel)
    if err != nil {
        return err
    }

    return s.Put(ctx, []byte(key), data)
}

// LoadChannel loads channel state
func (s *MerkleStorage) LoadChannel(ctx context.Context, worker1, worker2 WorkerID) (*SecureChannel, error) {
    s.cache.mu.RLock()
    key := makeChannelKey(worker1, worker2)
    if channel, ok := s.cache.channels[key]; ok {
        s.cache.mu.RUnlock()
        return channel, nil
    }
    s.cache.mu.RUnlock()

    // Load from storage
    data, err := s.Get(ctx, []byte(key))
    if err != nil {
        return nil, err
    }

    var channel SecureChannel
    if err := json.Unmarshal(data, &channel); err != nil {
        return nil, err
    }

    // Update cache
    s.cache.mu.Lock()
    s.cache.channels[key] = &channel
    s.cache.mu.Unlock()

    return &channel, nil
}

// DeleteChannel removes channel state
func (s *MerkleStorage) DeleteChannel(ctx context.Context, worker1, worker2 WorkerID) error {
    s.cache.mu.Lock()
    defer s.cache.mu.Unlock()

    key := makeChannelKey(worker1, worker2)
    delete(s.cache.channels, key)
    return s.Delete(ctx, []byte(key))
}

// SaveWorker persists worker state
func (s *MerkleStorage) SaveWorker(ctx context.Context, worker *Worker) error {
    s.cache.mu.Lock()
    defer s.cache.mu.Unlock()

    s.cache.workers[worker.id] = worker

    // Marshal worker state
    data, err := json.Marshal(worker)
    if err != nil {
        return err
    }

    return s.Put(ctx, []byte(worker.id), data)
}

// LoadWorker loads worker state
func (s *MerkleStorage) LoadWorker(ctx context.Context, id WorkerID) (*Worker, error) {
    s.cache.mu.RLock()
    if worker, ok := s.cache.workers[id]; ok {
        s.cache.mu.RUnlock()
        return worker, nil
    }
    s.cache.mu.RUnlock()

    // Load from storage
    data, err := s.Get(ctx, []byte(id))
    if err != nil {
        return nil, err
    }

    var worker Worker
    if err := json.Unmarshal(data, &worker); err != nil {
        return nil, err
    }

    // Update cache
    s.cache.mu.Lock()
    s.cache.workers[id] = &worker
    s.cache.mu.Unlock()

    return &worker, nil
}

// DeleteWorker removes worker state
func (s *MerkleStorage) DeleteWorker(ctx context.Context, id WorkerID) error {
    s.cache.mu.Lock()
    defer s.cache.mu.Unlock()

    delete(s.cache.workers, id)
    return s.Delete(ctx, []byte(id))
}

// Close closes the storage
func (s *MerkleStorage) Close() error {
    return nil // merkledb cleanup handled by parent
}

// Helper functions

func makeChannelKey(worker1, worker2 WorkerID) string {
    if worker1 < worker2 {
        return string(worker1) + ":" + string(worker2)
    }
    return string(worker2) + ":" + string(worker1)
}