// coordination/coordinator.go
package coordination

import (
    "context"
    "fmt"
    "sync"
    "time"

    "github.com/ava-labs/avalanchego/x/merkledb"
)

type Coordinator struct {
    config     *Config
    workers    map[WorkerID]*Worker
    storage    Storage
    db         merkledb.MerkleDB

    tasks      chan *Task
    done       chan struct{}
    
    // Track view changes
    viewLock   sync.Mutex
    changes    merkledb.ViewChanges
    
    mu         sync.RWMutex
    ctx        context.Context
    cancel     context.CancelFunc

    teePairs    map[string][2]WorkerID  // Region -> TEE worker pair mapping
    regionLock  sync.RWMutex
}

type Region struct {
    ID         string     `json:"id"`
    Workers    [2]WorkerID `json:"workers"`
    CreatedAt  time.Time  `json:"created_at"`
}

type Attestation struct {
    EnclaveID   []byte    
    Measurement []byte    
    Timestamp   time.Time 
    Data        []byte    
    Signature   []byte    
    RegionProof []byte    
}

func (c *Coordinator) RegisterRegion(ctx context.Context, regionID string, teeWorkers [2]WorkerID) error {
    c.regionLock.Lock()
    defer c.regionLock.Unlock()

    // Verify workers exist
    for _, workerID := range teeWorkers {
        if _, exists := c.workers[workerID]; !exists {
            return fmt.Errorf("worker %s not found", workerID)
        }
    }

    // Store TEE pair for region
    c.teePairs[regionID] = teeWorkers

    // Create region record in storage
    region := &Region{
        ID:        regionID,
        Workers:   teeWorkers,
        CreatedAt: time.Now().UTC(),
    }

    return c.storage.SaveRegion(ctx, region)
}

func (c *Coordinator) ValidateRegionalOperation(ctx context.Context, regionID string, attestations [2]Attestation) error {
    c.regionLock.RLock()
    defer c.regionLock.RUnlock()

    // Get registered TEE pair for region
    teePair, exists := c.teePairs[regionID]
    if !exists {
        return ErrRegionNotFound
    }

    // Verify attestations come from registered TEEs
    for i, att := range attestations {
        workerID := WorkerID(att.EnclaveID) // Convert enclave ID to worker ID
        if workerID != teePair[i] {
            return fmt.Errorf("unauthorized TEE for region: %s", workerID)
        }
    }

    // Verify timestamps match
    if !attestations[0].Timestamp.Equal(attestations[1].Timestamp) {
        return fmt.Errorf("attestation timestamps do not match")
    }

    // Verify within time window
    now := time.Now().UTC()
    window := c.config.AttestationTimeout
    for _, att := range attestations {
        diff := now.Sub(att.Timestamp)
        if diff > window || diff < -window {
            return fmt.Errorf("attestation timestamp outside valid window")
        }
    }

    return nil
}


func NewCoordinator(cfg *Config, db merkledb.MerkleDB) (*Coordinator, error) {
    ctx, cancel := context.WithCancel(context.Background())
    
    storage, err := NewStorage(db)
    if err != nil {
        cancel()
        return nil, fmt.Errorf("failed to create storage: %w", err)
    }

    return &Coordinator{
        config:    cfg,
        workers:   make(map[WorkerID]*Worker),
        storage:   storage,
        db:        db,
        tasks:     make(chan *Task, 1000),
        done:      make(chan struct{}),
        changes:   merkledb.ViewChanges{},
        ctx:       ctx,
        cancel:    cancel,
        teePairs:   make(map[string][2]WorkerID),
        regionLock: sync.RWMutex{},
    }, nil
}

func (c *Coordinator) Start() error {
    // Restore any persisted workers
    if err := c.restoreWorkers(); err != nil {
        return fmt.Errorf("failed to restore workers: %w", err)
    }

    // Start task processing
    go c.processTasks()

    return nil
}

func (c *Coordinator) Stop() error {
    c.cancel()
    close(c.done)
    return c.storage.Close()
}

func (c *Coordinator) RegisterWorker(ctx context.Context, id WorkerID, enclaveID []byte) error {
    c.mu.Lock()
    defer c.mu.Unlock()

    // Create worker
    worker := NewWorker(id, enclaveID, c)
    if err := worker.Start(); err != nil {
        return fmt.Errorf("failed to start worker: %w", err)
    }

    // Store worker state
    if err := c.storage.SaveWorker(ctx, worker); err != nil {
        worker.Stop()
        return fmt.Errorf("failed to save worker: %w", err)
    }

    c.workers[id] = worker
    return nil
}

func (c *Coordinator) UnregisterWorker(ctx context.Context, id WorkerID) error {
    c.mu.Lock()
    defer c.mu.Unlock()

    worker, exists := c.workers[id]
    if !exists {
        return ErrWorkerNotFound
    }

    // Stop worker
    if err := worker.Stop(); err != nil {
        return fmt.Errorf("failed to stop worker: %w", err)
    }

    // Remove from storage
    if err := c.storage.DeleteWorker(ctx, id); err != nil {
        return fmt.Errorf("failed to delete worker: %w", err)
    }

    delete(c.workers, id)
    return nil
}

func (c *Coordinator) SubmitTask(ctx context.Context, task *Task) error {
    select {
    case c.tasks <- task:
        return nil
    case <-ctx.Done():
        return ctx.Err()
    case <-time.After(c.config.WorkerTimeout):
        return ErrTimeout
    }
}

func (c *Coordinator) processTasks() {
    for {
        select {
        case task := <-c.tasks:
            var err error
            if task.RegionID != "" {
                err = c.handleRegionalTask(c.ctx, task, task.RegionID)
            } else {
                err = c.handleTask(c.ctx, task)
            }
            if err != nil {
                // Log error but continue processing
                continue
            }
        case <-c.done:
            return
        }
    }
}

func (c *Coordinator) handleTask(ctx context.Context, task *Task) error {
    c.mu.RLock()
    workers := make([]*Worker, 0, len(task.WorkerIDs))
    for _, id := range task.WorkerIDs {
        if worker, exists := c.workers[id]; exists {
            workers = append(workers, worker)
        }
    }
    c.mu.RUnlock()

    if len(workers) < c.config.MinWorkers {
        return ErrNotEnoughWorkers
    }

    c.viewLock.Lock()
    defer c.viewLock.Unlock()

    // Create new view for atomic state updates and save it in struct
    changes := merkledb.ViewChanges{}
    if err := c.storage.NewView(ctx, changes); err != nil {
        return fmt.Errorf("failed to create view: %w", err)
    }

    // Establish channels between workers
    for i := 0; i < len(workers); i++ {
        for j := i + 1; j < len(workers); j++ {
            // Try to load existing channel first
            channel, err := c.getChannel(ctx, workers[i].id, workers[j].id)
            if err != nil || channel == nil {
                channel = NewSecureChannel(workers[i].id, workers[j].id)
                if err := channel.EstablishSecure(); err != nil {
                    continue
                }
                // Save new channel state
                if err := c.saveChannelState(ctx, channel); err != nil {
                    continue
                }
            }

            workers[i].channels[workers[j].id] = channel
            workers[j].channels[workers[i].id] = channel
        }
    }

    // Distribute task data
    for _, worker := range workers {
        msg := &Message{
            Type: MessageTypeData,
            Data: task.Data,
        }
        worker.SendMessage(msg)
    }

    return nil
}

func (c *Coordinator) handleRegionalTask(ctx context.Context, task *Task, regionID string) error {
    c.regionLock.RLock()
    teePair, exists := c.teePairs[regionID]
    c.regionLock.RUnlock()
    
    if !exists {
        return ErrRegionNotFound
    }

    // Set task workers to region's TEE pair
    task.WorkerIDs = []WorkerID{teePair[0], teePair[1]}

    return c.handleTask(ctx, task)
}

func (c *Coordinator) restoreWorkers() error {
    // Implementation would restore worker state from merkledb
    // This is placeholder until we implement worker state serialization
    return nil
}

// GetWorker returns a worker by ID
func (c *Coordinator) GetWorker(id WorkerID) (*Worker, bool) {
    c.mu.RLock()
    defer c.mu.RUnlock()
    worker, exists := c.workers[id]
    return worker, exists
}

// GetWorkerIDs returns all registered worker IDs
func (c *Coordinator) GetWorkerIDs() []WorkerID {
    c.mu.RLock()
    defer c.mu.RUnlock()
    
    ids := make([]WorkerID, 0, len(c.workers))
    for id := range c.workers {
        ids = append(ids, id)
    }
    return ids
}

// GetWorkerCount returns the number of registered workers
func (c *Coordinator) GetWorkerCount() int {
    c.mu.RLock()
    defer c.mu.RUnlock()
    return len(c.workers)
}

// Helper methods for coordination state

func (c *Coordinator) getChannel(ctx context.Context, worker1, worker2 WorkerID) (*SecureChannel, error) {
    return c.storage.LoadChannel(ctx, worker1, worker2)
}

func (c *Coordinator) saveChannelState(ctx context.Context, channel *SecureChannel) error {
    c.viewLock.Lock()
    defer c.viewLock.Unlock()
    
    return c.storage.SaveChannel(ctx, channel)
}