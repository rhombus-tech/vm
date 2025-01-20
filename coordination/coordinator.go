// coordination/coordinator.go
package coordination

import (
	"context"
	"fmt"
	"math"
	"sync"
	"sync/atomic"
	"time"

	"github.com/ava-labs/avalanchego/x/merkledb"
)

const (
    TaskStatusPending  = "pending"
    TaskStatusRunning  = "running"
    TaskStatusComplete = "complete"
    TaskStatusFailed   = "failed"
)

type Coordinator struct {
    config     *Config
    workers    map[WorkerID]*Worker
    storage    Storage
    db         merkledb.MerkleDB

    // Keep existing channel for task processing
    tasks      chan *Task
    // Add map for status tracking
    taskStatus map[string]*TaskInfo
    done       chan struct{}
    
    viewLock   sync.Mutex
    changes    merkledb.ViewChanges
    
    mu         sync.RWMutex
    ctx        context.Context
    cancel     context.CancelFunc

    teePairs    map[string][]TEEPairInfo
    regionLock  sync.RWMutex
}

type TEEPairInfo struct {
    ID          string
    SGXWorker   WorkerID
    SEVWorker   WorkerID
    Channel     *SecureChannel
    TaskCount   int32
    LastUsed    time.Time
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

type TaskInfo struct {
    Task      *Task
    Status    string
    StartTime time.Time
    EndTime   time.Time
    Error     error
    Results   [][]byte
}

func (c *Coordinator) selectTEEPair(ctx context.Context, regionID string) (*TEEPairInfo, error) {
    c.mu.RLock()
    pairs := c.teePairs[regionID]
    c.mu.RUnlock()

    if len(pairs) == 0 {
        return nil, fmt.Errorf("no TEE pairs available for region %s", regionID)
    }

    // Select pair based on load and health
    var selectedPair *TEEPairInfo
    minTasks := int32(math.MaxInt32)

    for i := range pairs {
        pair := &pairs[i]
        taskCount := atomic.LoadInt32(&pair.TaskCount)
        
        if taskCount < minTasks {
            minTasks = taskCount
            selectedPair = pair
        }
    }

    if selectedPair == nil {
        return nil, fmt.Errorf("no available TEE pairs in region %s", regionID)
    }

    atomic.AddInt32(&selectedPair.TaskCount, 1)
    return selectedPair, nil
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


func NewCoordinator(config *Config, db merkledb.MerkleDB) (*Coordinator, error) {
    if config.TaskCleanupInterval <= 0 {
        config.TaskCleanupInterval = time.Minute
    }

    ctx, cancel := context.WithCancel(context.Background())
    
    return &Coordinator{
        config:     config,
        workers:    make(map[WorkerID]*Worker),
        db:         db,
        tasks:      make(chan *Task, config.MaxTasks),  // Use MaxTasks here
        taskStatus: make(map[string]*TaskInfo),
        done:       make(chan struct{}),
        ctx:        ctx,
        cancel:     cancel,
        teePairs:   make(map[string][2]WorkerID),
    }, nil
}



func (c *Coordinator) Start() error {
    go c.processTasks()
    go c.cleanupTasks()
    return nil
}

func (c *Coordinator) Stop() error {
    close(c.done)
    c.mu.Lock()
    defer c.mu.Unlock()
    c.taskStatus = make(map[string]*TaskInfo)
    return nil
}

func (c *Coordinator) AddWorker(worker *Worker) {
    c.mu.Lock()
    defer c.mu.Unlock()
    c.workers[worker.ID] = worker
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
    c.mu.Lock()
    if len(c.taskStatus) >= c.config.MaxTasks {
        c.mu.Unlock()
        return fmt.Errorf("maximum number of concurrent tasks reached")
    }
    
    // Create initial task status
    c.taskStatus[task.ID] = &TaskInfo{
        Task:      task,
        Status:    TaskStatusPending,
        StartTime: time.Now(),
    }
    c.mu.Unlock()

    // Submit to task channel
    select {
    case c.tasks <- task:
        return nil
    case <-ctx.Done():
        return ctx.Err()
    default:
        return fmt.Errorf("task queue full")
    }
}

func (c *Coordinator) CompleteTask(taskID string) {
    c.mu.Lock()
    defer c.mu.Unlock()

    if taskInfo, exists := c.taskStatus[taskID]; exists {
        taskInfo.Status = "complete"
        taskInfo.EndTime = time.Now()
    }
}


func (c *Coordinator) ClearTasks() {
    c.mu.Lock()
    defer c.mu.Unlock()
    c.taskStatus = make(map[string]*TaskInfo)
}



func (c *Coordinator) processTasks() {
    for {
        select {
        case task := <-c.tasks:
            // Create task info
            c.mu.Lock()
            taskInfo := &TaskInfo{
                Task:      task,
                Status:    TaskStatusRunning,
                StartTime: time.Now(),
            }
            c.taskStatus[task.ID] = taskInfo
            c.mu.Unlock()

            var err error
            if task.RegionID != "" {
                err = c.handleRegionalTask(c.ctx, task, task.RegionID)
            } else {
                err = c.handleTask(c.ctx, task)
            }

            // Update task status
            c.mu.Lock()
            if err != nil {
                taskInfo.Status = TaskStatusFailed
                taskInfo.Error = err
            } else {
                taskInfo.Status = TaskStatusComplete
            }
            taskInfo.EndTime = time.Now()
            c.mu.Unlock()

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
        channel, err := c.getChannel(ctx, workers[i].ID, workers[j].ID) // Updated to .ID
        if err != nil || channel == nil {
            channel = NewSecureChannel(workers[i].ID, workers[j].ID)  // Updated to .ID
            if err := channel.EstablishSecure(); err != nil {
                continue
            }
            // Save new channel state
            if err := c.saveChannelState(ctx, channel); err != nil {
                continue
            }
        }
        workers[i].Channels[workers[j].ID] = channel  // Updated to .Channels and .ID
        workers[j].Channels[workers[i].ID] = channel  // Updated to .Channels and .ID
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

func (c *Coordinator) cleanupTasks() {
    // Use a default interval if not set
    interval := c.config.TaskCleanupInterval
    if interval <= 0 {
        interval = time.Minute // Default to 1 minute if not set
    }
    
    ticker := time.NewTicker(interval)
    defer ticker.Stop()

    for {
        select {
        case <-ticker.C:
            c.mu.Lock()
            for id, taskInfo := range c.taskStatus {
                if (taskInfo.Status == TaskStatusComplete || 
                    taskInfo.Status == TaskStatusFailed) &&
                    time.Since(taskInfo.EndTime) > interval {
                    delete(c.taskStatus, id)
                }
            }
            c.mu.Unlock()
        case <-c.done:
            return
        }
    }
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

func (c *Coordinator) GetTEEPair(regionID string) ([2]WorkerID, error) {
    c.regionLock.RLock()
    defer c.regionLock.RUnlock()
    
    teePair, exists := c.teePairs[regionID]
    if !exists {
        return [2]WorkerID{}, ErrRegionNotFound
    }
    
    return teePair, nil
}

func (c *Coordinator) GetSecureChannel(ctx context.Context, worker1, worker2 WorkerID) (*SecureChannel, error) {
    // First try to load existing channel
    channel, err := c.storage.LoadChannel(ctx, worker1, worker2)
    if err != nil || channel == nil {
        // Create new channel if none exists
        channel = NewSecureChannel(worker1, worker2)
        if err := channel.EstablishSecure(); err != nil {
            return nil, fmt.Errorf("failed to establish secure channel: %w", err)
        }
        // Save new channel
        if err := c.storage.SaveChannel(ctx, channel); err != nil {
            return nil, fmt.Errorf("failed to save channel: %w", err)
        }
    }
    return channel, nil
}

func (c *Coordinator) GetTaskStatus(taskID string) (string, error) {
    c.mu.RLock()
    defer c.mu.RUnlock()

    taskInfo, exists := c.taskStatus[taskID]
    if !exists {
        return "", fmt.Errorf("task %s not found", taskID)
    }

    return taskInfo.Status, nil
}

func (c *Coordinator) GetTaskError(taskID string) error {
    c.mu.RLock()
    defer c.mu.RUnlock()

    taskInfo, exists := c.taskStatus[taskID]
    if !exists {
        return fmt.Errorf("task %s not found", taskID)
    }

    return taskInfo.Error
}

func (c *Coordinator) GetTaskResults(taskID string) ([][]byte, error) {
    c.mu.RLock()
    defer c.mu.RUnlock()

    taskInfo, exists := c.taskStatus[taskID]
    if !exists {
        return nil, fmt.Errorf("task %s not found", taskID)
    }

    if taskInfo.Status != TaskStatusComplete {
        return nil, fmt.Errorf("task %s not complete: %s", taskID, taskInfo.Status)
    }

    return taskInfo.Results, nil
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