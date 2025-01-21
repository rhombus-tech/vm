package coordination

import (
	"context"
	"encoding/json"
	"fmt"
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

    SaveRegion(ctx context.Context, region *Region) error
    LoadRegion(ctx context.Context, id string) (*Region, error) 
    DeleteRegion(ctx context.Context, id string) error
    
    SaveCoordinatorState(ctx context.Context, state map[string]interface{}) error
    LoadCoordinatorState(ctx context.Context) (map[string]interface{}, error)

    // TEE pair storage methods
    SaveTEEPairInfo(ctx context.Context, info *TEEPairInfo) error
    GetTEEPairInfo(ctx context.Context, pairID string) (*TEEPairInfo, error)
    ListTEEPairInfo(ctx context.Context) ([]*TEEPairInfo, error)
    
    // Metrics storage methods
    SaveTEEMetrics(ctx context.Context, pairID string, metrics *TEEPairMetrics) error
    GetTEEMetrics(ctx context.Context, pairID string) (*TEEPairMetrics, error)

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

func (s *MerkleStorage) SaveRegion(ctx context.Context, region *Region) error {
    data, err := json.Marshal(region)
    if err != nil {
        return err
    }
    
    key := []byte(fmt.Sprintf("region:%s", region.ID))
    return s.Put(ctx, key, data)
}

func (s *MerkleStorage) LoadRegion(ctx context.Context, id string) (*Region, error) {
    key := []byte(fmt.Sprintf("region:%s", id))
    data, err := s.Get(ctx, key)
    if err != nil {
        return nil, err
    }
    
    var region Region
    if err := json.Unmarshal(data, &region); err != nil {
        return nil, err
    }
    return &region, nil
}

func (s *MerkleStorage) DeleteRegion(ctx context.Context, id string) error {
    key := []byte(fmt.Sprintf("region:%s", id))
    return s.Delete(ctx, key)
}

// NewStorage creates a new storage instance
func NewStorage(db merkledb.MerkleDB) (Storage, error) {
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

// Implementation of Storage interface methods
func (s *MerkleStorage) SaveTEEPairInfo(ctx context.Context, info *TEEPairInfo) error {
    data, err := json.Marshal(info)
    if err != nil {
        return err
    }
    key := []byte(fmt.Sprintf("tee:pair:%s", info.ID))
    return s.Put(ctx, key, data)
}

func (s *MerkleStorage) GetTEEPairInfo(ctx context.Context, pairID string) (*TEEPairInfo, error) {
    key := []byte(fmt.Sprintf("tee:pair:%s", pairID))
    data, err := s.Get(ctx, key)
    if err != nil {
        return nil, err
    }
    var info TEEPairInfo
    if err := json.Unmarshal(data, &info); err != nil {
        return nil, err
    }
    return &info, nil
}

func (s *MerkleStorage) ListTEEPairInfo(ctx context.Context) ([]*TEEPairInfo, error) {
    // Implementation for listing TEE pairs
    return nil, nil
}

func (s *MerkleStorage) SaveTEEMetrics(ctx context.Context, pairID string, metrics *TEEPairMetrics) error {
    data, err := json.Marshal(metrics)
    if err != nil {
        return err
    }
    key := []byte(fmt.Sprintf("tee:metrics:%s", pairID))
    return s.Put(ctx, key, data)
}

func (s *MerkleStorage) GetTEEMetrics(ctx context.Context, pairID string) (*TEEPairMetrics, error) {
    key := []byte(fmt.Sprintf("tee:metrics:%s", pairID))
    data, err := s.Get(ctx, key)
    if err != nil {
        return nil, err
    }
    var metrics TEEPairMetrics
    if err := json.Unmarshal(data, &metrics); err != nil {
        return nil, err
    }
    return &metrics, nil
}

func (s *MerkleStorage) SaveCoordinatorState(ctx context.Context, state map[string]interface{}) error {
    data, err := json.Marshal(state)
    if err != nil {
        return err
    }
    return s.Put(ctx, []byte("coordinator:state"), data)
}

func (s *MerkleStorage) LoadCoordinatorState(ctx context.Context) (map[string]interface{}, error) {
    data, err := s.Get(ctx, []byte("coordinator:state"))
    if err != nil {
        return nil, err
    }
    var state map[string]interface{}
    if err := json.Unmarshal(data, &state); err != nil {
        return nil, err
    }
    return state, nil
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

    key := makeChannelKey(channel.Worker1, channel.Worker2)
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

    s.cache.workers[worker.ID] = worker  // Updated to .ID

    // Marshal worker state
    data, err := json.Marshal(worker)  // This will now use Worker's custom MarshalJSON
    if err != nil {
        return fmt.Errorf("failed to marshal worker: %w", err)
    }

    // Store using the worker's ID
    return s.Put(ctx, []byte(worker.ID), data)  // Updated to .ID
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
