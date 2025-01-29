package coordination

import (
	"context"
	"errors"
	"fmt"
	"math"
	"math/rand"
	"sync"
	"sync/atomic"
	"time"

	"github.com/ava-labs/avalanchego/x/merkledb"
	"github.com/ethereum/go-ethereum/signer/storage"
)

var (
    ErrChannelExists     = errors.New("secure channel already exists")
    ErrInvalidWorkerPair = errors.New("invalid worker pair")
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
    db         merkledb.MerkleDB

    // Task management
    tasks      chan *Task
    taskStatus map[string]*TaskInfo
    done       chan struct{}
    
    // Storage and view management
    viewLock   sync.Mutex
    changes    merkledb.ViewChanges
    store      Storage
    
    // Concurrency control
    mu         sync.RWMutex
    ctx        context.Context
    cancel     context.CancelFunc

    // TEE and region management
    teePairs      map[string][]TEEPairInfo
    regionMetrics map[string]*RegionMetrics  // Add this field
    regionLock    sync.RWMutex
}


func NewCoordinator(config *Config, db merkledb.MerkleDB, store BaseStorage) (*Coordinator, error) {
    if config.TaskCleanupInterval <= 0 {
        config.TaskCleanupInterval = time.Minute
    }

    ctx, cancel := context.WithCancel(context.Background())
    
    return &Coordinator{
        config:     config,
        workers:    make(map[WorkerID]*Worker),
        db:         db,
        store:      NewStorageWrapper(store),
        tasks:      make(chan *Task, config.MaxTasks),
        taskStatus: make(map[string]*TaskInfo),
        done:       make(chan struct{}),
        ctx:        ctx,
        cancel:     cancel,
        teePairs:   make(map[string][]TEEPairInfo),
        regionMetrics: make(map[string]*RegionMetrics),
    }, nil
}

// Core operations
func (c *Coordinator) Start() error {
    if err := c.restoreWorkers(); err != nil {
        return fmt.Errorf("failed to restore workers: %w", err)
    }
    
    go c.processTasks()
    go c.cleanupTasks()
    return nil
}

func (c *Coordinator) Stop() error {
    c.cancel()
    close(c.done)
    
    c.mu.Lock()
    defer c.mu.Unlock()
    
    // Cleanup running tasks
    for _, info := range c.taskStatus {
        if info.Status == TaskStatusRunning {
            info.Status = TaskStatusFailed
            info.Error = errors.New("coordinator stopped")
            info.EndTime = time.Now()
        }
    }
    
    // Stop all workers
    for _, worker := range c.workers {
        if err := worker.Stop(); err != nil {
            // Log error but continue cleanup
            fmt.Printf("error stopping worker %s: %v\n", worker.ID, err)
        }
    }

    return nil
}

// Task Management
func (c *Coordinator) SubmitTask(ctx context.Context, task *Task) error {
    if task == nil {
        return errors.New("nil task")
    }

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

    // Submit to task channel with timeout
    select {
    case c.tasks <- task:
        return nil
    case <-ctx.Done():
        c.mu.Lock()
        delete(c.taskStatus, task.ID)
        c.mu.Unlock()
        return ctx.Err()
    case <-time.After(c.config.TaskTimeout):
        c.mu.Lock()
        delete(c.taskStatus, task.ID)
        c.mu.Unlock()
        return fmt.Errorf("task submission timed out")
    }
}

func (c *Coordinator) processTasks() {
    for {
        select {
        case task := <-c.tasks:
            c.mu.Lock()
            taskInfo := c.taskStatus[task.ID]
            if taskInfo == nil {
                // Task was cancelled/cleaned up
                c.mu.Unlock()
                continue
            }
            taskInfo.Status = TaskStatusRunning
            c.mu.Unlock()

            // Execute task
            err := c.executeTask(c.ctx, task)

            // Update task status
            c.mu.Lock()
            if taskInfo = c.taskStatus[task.ID]; taskInfo != nil {
                if err != nil {
                    taskInfo.Status = TaskStatusFailed
                    taskInfo.Error = err
                } else {
                    taskInfo.Status = TaskStatusComplete
                }
                taskInfo.EndTime = time.Now()
            }
            c.mu.Unlock()

        case <-c.done:
            return
        }
    }
}

func (c *Coordinator) executeTask(ctx context.Context, task *Task) error {
    // Handle regional tasks differently
    if task.RegionID != "" {
        return c.handleRegionalTask(ctx, task, task.RegionID)
    }
    return c.handleTask(ctx, task)
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

    // Create view for atomic state updates
    if err := c.withView(ctx, func(view merkledb.View) error {
        // Establish channels between workers
        for i := 0; i < len(workers); i++ {
            for j := i + 1; j < len(workers); j++ {
                channel, err := c.GetSecureChannel(ctx, workers[i].ID, workers[j].ID)
                if err != nil {
                    return fmt.Errorf("failed to establish channel: %w", err)
                }
                workers[i].Channels[workers[j].ID] = channel
                workers[j].Channels[workers[i].ID] = channel
            }
        }

        // Distribute task data
        msg := &Message{
            Type: MessageTypeData,
            Data: task.Data,
        }

        for _, worker := range workers {
            if err := worker.SendMessage(msg); err != nil {
                return fmt.Errorf("failed to send task to worker %s: %w", worker.ID, err)
            }
        }

        return nil
    }); err != nil {
        return err
    }

    return nil
}

func (c *Coordinator) handleRegionalTask(ctx context.Context, task *Task, regionID string) error {
    // Get TEE pair for region
    pair, err := c.selectTEEPair(ctx, regionID)
    if err != nil {
        return err
    }

    // Set task workers to region's TEE pair
    task.WorkerIDs = []WorkerID{pair.SGXWorker, pair.SEVWorker}

    // Execute task
    err = c.handleTask(ctx, task)

    // Update pair usage
    atomic.AddInt32(&pair.TaskCount, -1)
    pair.LastUsed = time.Now()

    return err
}

func (c *Coordinator) cleanupTasks() {
    interval := c.config.TaskCleanupInterval
    if interval <= 0 {
        interval = time.Minute
    }
    
    ticker := time.NewTicker(interval)
    defer ticker.Stop()

    for {
        select {
        case <-ticker.C:
            c.mu.Lock()
            now := time.Now()
            for id, info := range c.taskStatus {
                if (info.Status == TaskStatusComplete || info.Status == TaskStatusFailed) &&
                    now.Sub(info.EndTime) > interval {
                    delete(c.taskStatus, id)
                }
            }
            c.mu.Unlock()

        case <-c.done:
            return
        }
    }
}
// Worker Management
func (c *Coordinator) RegisterWorker(ctx context.Context, id WorkerID, enclaveID []byte) error {
    c.mu.Lock()
    defer c.mu.Unlock()

    if _, exists := c.workers[id]; exists {
        return fmt.Errorf("worker %s already registered", id)
    }

    // Create worker
    worker := &Worker{
        ID:         id,
        EnclaveID:  enclaveID,
        Status:     WorkerStatusIdle,
        LastActive: time.Now(),
        Channels:   make(map[WorkerID]*SecureChannel),
    }

    // Store worker state first
    if err := c.store.SaveWorker(ctx, worker); err != nil {
        return fmt.Errorf("failed to save worker: %w", err)
    }

    // Start worker after successful storage
    if err := worker.Start(); err != nil {
        // Clean up stored state if start fails
        _ = c.store.DeleteWorker(ctx, id)
        return fmt.Errorf("failed to start worker: %w", err)
    }

    // Add to in-memory map only after successful storage and start
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
    if err := c.store.DeleteWorker(ctx, id); err != nil {
        return fmt.Errorf("failed to delete worker: %w", err)
    }

    delete(c.workers, id)
    return nil
}


// TEE Pair Management
func (c *Coordinator) RegisterTEEPair(ctx context.Context, regionID string, pair *TEEPair) error {
    c.mu.Lock()
    defer c.mu.Unlock()

    // Register SGX worker
    sgxWorker := &Worker{
        ID:        WorkerID(pair.SGXID),
        EnclaveID: pair.SGXID,
        Status:    WorkerStatusIdle,
        Channels:  make(map[WorkerID]*SecureChannel),
    }
    
    // Register SEV worker
    sevWorker := &Worker{
        ID:        WorkerID(pair.SEVID),
        EnclaveID: pair.SEVID,
        Status:    WorkerStatusIdle,
        Channels:  make(map[WorkerID]*SecureChannel),
    }

    // Register workers
    if err := c.RegisterWorker(ctx, sgxWorker.ID, sgxWorker.EnclaveID); err != nil {
        return fmt.Errorf("failed to register SGX worker: %w", err)
    }
    
    if err := c.RegisterWorker(ctx, sevWorker.ID, sevWorker.EnclaveID); err != nil {
        // Cleanup SGX worker if SEV fails
        c.UnregisterWorker(ctx, sgxWorker.ID)
        return fmt.Errorf("failed to register SEV worker: %w", err)
    }

    // Create and store TEE pair info
    pairInfo := TEEPairInfo{
        ID:        fmt.Sprintf("%s-%s", pair.SGXID, pair.SEVID),
        SGXWorker: sgxWorker.ID,
        SEVWorker: sevWorker.ID,
        LastUsed:  time.Now(),
    }

    // Establish secure channel
    channel, err := c.GetSecureChannel(ctx, sgxWorker.ID, sevWorker.ID)
    if err != nil {
        // Cleanup workers if channel establishment fails
        c.UnregisterWorker(ctx, sgxWorker.ID)
        c.UnregisterWorker(ctx, sevWorker.ID)
        return fmt.Errorf("failed to establish secure channel: %w", err)
    }
    pairInfo.Channel = channel

    // Add to region pairs
    c.regionLock.Lock()
    if c.teePairs[regionID] == nil {
        c.teePairs[regionID] = make([]TEEPairInfo, 0)
    }
    c.teePairs[regionID] = append(c.teePairs[regionID], pairInfo)
    c.regionLock.Unlock()

    return nil
}

func (c *Coordinator) selectTEEPair(ctx context.Context, regionID string) (*TEEPairInfo, error) {
    c.regionLock.RLock()
    pairs := c.teePairs[regionID]
    c.regionLock.RUnlock()

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
    selectedPair.LastUsed = time.Now()
    
    return selectedPair, nil
}

// Region Management
func (c *Coordinator) RegisterRegion(ctx context.Context, regionID string, teeWorkers [2]WorkerID) error {
    c.regionLock.Lock()
    defer c.regionLock.Unlock()

    // Validate region ID
    if regionID == "" {
        return fmt.Errorf("invalid region ID")
    }

    // Check if region already exists
    existing, err := c.store.LoadRegion(ctx, regionID)
    if err != nil && !errors.Is(err, storage.ErrNotFound) {
        return fmt.Errorf("failed to check existing region: %w", err)
    }
    if existing != nil {
        return fmt.Errorf("region %s already exists", regionID)
    }

    // Verify both TEE workers are provided
    if len(teeWorkers) != 2 {
        return fmt.Errorf("exactly 2 TEE workers required, got %d", len(teeWorkers))
    }

    // Verify workers exist
    for _, workerID := range teeWorkers {
        if _, exists := c.workers[workerID]; !exists {
            return fmt.Errorf("worker %s not found", workerID)
        }
    }

    // Create region record
    region := &Region{
        ID:        regionID,
        Workers:   teeWorkers,
        CreatedAt: time.Now().UTC(),
    }

    // Save to storage
    if err := c.store.SaveRegion(ctx, region); err != nil {
        return fmt.Errorf("failed to save region: %w", err)
    }

    // Establish secure channel between workers
    channel, err := c.GetSecureChannel(ctx, teeWorkers[0], teeWorkers[1])
    if err != nil {
        return fmt.Errorf("failed to establish secure channel: %w", err)
    }

    // Create TEE pair info
    pairInfo := TEEPairInfo{
        ID:        fmt.Sprintf("%s-%s", teeWorkers[0], teeWorkers[1]),
        SGXWorker: teeWorkers[0],
        SEVWorker: teeWorkers[1],
        Channel:   channel,
        LastUsed:  time.Now(),
    }

    // Add to TEE pairs map
    if c.teePairs[regionID] == nil {
        c.teePairs[regionID] = make([]TEEPairInfo, 0)
    }
    c.teePairs[regionID] = append(c.teePairs[regionID], pairInfo)

    return nil
}

func (c *Coordinator) ValidateRegionalOperation(ctx context.Context, regionID string, attestations [2]Attestation) error {
    c.regionLock.RLock()
    defer c.regionLock.RUnlock()

    pairs, exists := c.teePairs[regionID]
    if !exists || len(pairs) == 0 {
        return ErrRegionNotFound
    }

    // Find matching pair
    var foundPair *TEEPairInfo
    for i := range pairs {
        if WorkerID(attestations[0].EnclaveID) == pairs[i].SGXWorker &&
           WorkerID(attestations[1].EnclaveID) == pairs[i].SEVWorker {
            foundPair = &pairs[i]
            break
        }
    }

    if foundPair == nil {
        return fmt.Errorf("unauthorized TEE pair for region")
    }

    // Verify timestamps match and are within window
    if !attestations[0].Timestamp.Equal(attestations[1].Timestamp) {
        return fmt.Errorf("attestation timestamps do not match")
    }

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

// Channel Management
func (c *Coordinator) GetSecureChannel(ctx context.Context, worker1, worker2 WorkerID) (*SecureChannel, error) {
    // First try to load existing channel
    channel, err := c.store.LoadChannel(ctx, worker1, worker2)
    if err != nil {
        // Only create new channel if one doesn't exist
        if !errors.Is(err, storage.ErrNotFound) {
            return nil, fmt.Errorf("failed to load channel: %w", err)
        }

        // Create new channel
        channel = NewSecureChannel(worker1, worker2)
        if err := channel.EstablishSecure(); err != nil {
            return nil, fmt.Errorf("failed to establish secure channel: %w", err)
        }

        // Save new channel
        if err := c.store.SaveChannel(ctx, channel); err != nil {
            return nil, fmt.Errorf("failed to save channel: %w", err)
        }
    }

    return channel, nil
}

// Storage and View Operations
func (c *Coordinator) withView(ctx context.Context, fn func(view merkledb.View) error) error {
    c.viewLock.Lock()
    defer c.viewLock.Unlock()

    // Create new view
    view, err := c.db.NewView(ctx, c.changes)
    if err != nil {
        return fmt.Errorf("failed to create view: %w", err)
    }

    // Execute operation
    if err := fn(view); err != nil {
        return err
    }

    // Commit changes
    c.changes = merkledb.ViewChanges{}
    return nil
}

func (c *Coordinator) restoreWorkers() error {
    // Implementation to restore worker state from storage
    // This would be called during startup
    return nil
}

// Status and Query Methods
func (c *Coordinator) GetWorker(id WorkerID) (*Worker, bool) {
    c.mu.RLock()
    defer c.mu.RUnlock()
    worker, exists := c.workers[id]
    return worker, exists
}

func (c *Coordinator) GetWorkerIDs() []WorkerID {
    c.mu.RLock()
    defer c.mu.RUnlock()
    
    ids := make([]WorkerID, 0, len(c.workers))
    for id := range c.workers {
        ids = append(ids, id)
    }
    return ids
}

func (c *Coordinator) GetWorkerCount() int {
    c.mu.RLock()
    defer c.mu.RUnlock()
    return len(c.workers)
}

func (c *Coordinator) GetTEEPair(regionID string) ([2]WorkerID, error) {
    c.regionLock.RLock()
    defer c.regionLock.RUnlock()
    
    pairs, exists := c.teePairs[regionID]
    if !exists || len(pairs) == 0 {
        return [2]WorkerID{}, ErrRegionNotFound
    }
    
    return [2]WorkerID{pairs[0].SGXWorker, pairs[0].SEVWorker}, nil
}

func (c *Coordinator) GetTaskStatus(taskID string) (*TaskInfo, error) {
    c.mu.RLock()
    defer c.mu.RUnlock()

    info, exists := c.taskStatus[taskID]
    if !exists {
        return nil, fmt.Errorf("task %s not found", taskID)
    }

    // Return copy to prevent concurrent modification
    return &TaskInfo{
        Task:      info.Task,
        Status:    info.Status,
        StartTime: info.StartTime,
        EndTime:   info.EndTime,
        Error:     info.Error,
        Results:   append([][]byte{}, info.Results...),
    }, nil
}

// Task Result Management
func (c *Coordinator) SetTaskResult(taskID string, result []byte, workerID WorkerID) error {
    c.mu.Lock()
    defer c.mu.Unlock()

    info, exists := c.taskStatus[taskID]
    if !exists {
        return fmt.Errorf("task %s not found", taskID)
    }

    if info.Status != TaskStatusRunning {
        return fmt.Errorf("task %s is not running", taskID)
    }

    // Append result
    info.Results = append(info.Results, result)

    // Check if we have all results
    if len(info.Results) == len(info.Task.WorkerIDs) {
        info.Status = TaskStatusComplete
        info.EndTime = time.Now()
    }

    return nil
}

// Helper Methods
func (c *Coordinator) verifyWorkerPair(worker1, worker2 WorkerID) error {
    c.mu.RLock()
    defer c.mu.RUnlock()

    // Verify both workers exist
    if _, exists := c.workers[worker1]; !exists {
        return fmt.Errorf("worker %s not found", worker1)
    }
    if _, exists := c.workers[worker2]; !exists {
        return fmt.Errorf("worker %s not found", worker2)
    }

    return nil
}

func (c *Coordinator) isWorkerActive(id WorkerID) bool {
    c.mu.RLock()
    defer c.mu.RUnlock()

    worker, exists := c.workers[id]
    if !exists {
        return false
    }
    return worker.Status == WorkerStatusActive
}

// Metrics and Monitoring
func (c *Coordinator) GetMetrics() map[string]interface{} {
    c.mu.RLock()
    defer c.mu.RUnlock()

    metrics := make(map[string]interface{})
    
    // Worker metrics
    workerMetrics := make(map[WorkerID]WorkerStatus)  // Changed type
    for id, worker := range c.workers {
        workerMetrics[id] = worker.Status
    }
    metrics["workers"] = workerMetrics

    // Task metrics
    taskCounts := make(map[TaskStatus]int)  // Changed type
    for _, info := range c.taskStatus {
        taskCounts[info.Status]++
    }
    metrics["tasks"] = taskCounts

    return metrics
}

// Cleanup Methods
func (c *Coordinator) cleanup() {
    c.mu.Lock()
    defer c.mu.Unlock()

    // Clear tasks
    c.taskStatus = make(map[string]*TaskInfo)

    // Reset workers
    for _, worker := range c.workers {
        worker.Status = WorkerStatusIdle
        worker.Channels = make(map[WorkerID]*SecureChannel)
    }

    // Clear TEE pairs
    for regionID := range c.teePairs {
        c.teePairs[regionID] = nil
    }
}

func (c *Coordinator) SendMessage(msg *Message) error {
    c.mu.RLock()
    fromWorker, exists := c.workers[msg.From]
    if !exists {
        c.mu.RUnlock()
        return fmt.Errorf("sender worker %s not found", msg.From)
    }

    toWorker, exists := c.workers[msg.To]
    if !exists {
        c.mu.RUnlock()
        return fmt.Errorf("recipient worker %s not found", msg.To)
    }
    c.mu.RUnlock()

    // Get or establish secure channel
    channel, err := c.GetSecureChannel(context.Background(), msg.From, msg.To)
    if err != nil {
        return fmt.Errorf("failed to get secure channel: %w", err)
    }

    // Send message through channel
    if err := channel.Send(msg.Data); err != nil {
        return fmt.Errorf("failed to send message: %w", err)
    }

    // Update worker statuses
    fromWorker.Status = WorkerStatusActive
    toWorker.Status = WorkerStatusActive

    return nil
}

// Worker Health Management
func (c *Coordinator) monitorWorkerHealth() {
    ticker := time.NewTicker(c.config.WorkerTimeout / 2)
    defer ticker.Stop()

    for {
        select {
        case <-ticker.C:
            c.checkWorkerHealth()
        case <-c.done:
            return
        }
    }
}

func (c *Coordinator) checkWorkerHealth() {
    c.mu.Lock()
    defer c.mu.Unlock()

    now := time.Now()
    for id, worker := range c.workers {
        if worker.Status == WorkerStatusActive {
            if now.Sub(worker.LastActive) > c.config.WorkerTimeout {
                worker.Status = WorkerStatusError
                // Handle worker timeout
                c.handleWorkerTimeout(id)
            }
        }
    }
}

func (c *Coordinator) handleWorkerTimeout(workerID WorkerID) {
    // Find affected tasks
    affectedTasks := make([]string, 0)
    for taskID, info := range c.taskStatus {
        for _, id := range info.Task.WorkerIDs {
            if id == workerID {
                affectedTasks = append(affectedTasks, taskID)
                break
            }
        }
    }

    // Handle affected tasks
    for _, taskID := range affectedTasks {
        if info := c.taskStatus[taskID]; info != nil {
            info.Status = TaskStatusFailed
            info.Error = fmt.Errorf("worker %s timeout", workerID)
            info.EndTime = time.Now()
        }
    }
}

// TEE Attestation Management
func (c *Coordinator) VerifyAttestation(att *Attestation) error {
    if att == nil {
        return errors.New("nil attestation")
    }

    // Verify timestamp is recent
    now := time.Now().UTC()
    diff := now.Sub(att.Timestamp)
    if diff > c.config.AttestationTimeout || diff < -c.config.AttestationTimeout {
        return fmt.Errorf("attestation timestamp outside valid window")
    }

    // Verify signature
    if len(att.Signature) == 0 {
        return errors.New("missing attestation signature")
    }

    // Additional platform-specific verification could be added here

    return nil
}

// Resource Management
func (c *Coordinator) getResourceUsage() map[string]float64 {
    c.mu.RLock()
    defer c.mu.RUnlock()

    usage := make(map[string]float64)
    
    // Calculate task queue usage
    usage["task_queue"] = float64(len(c.taskStatus)) / float64(c.config.MaxTasks)
    
    // Calculate worker usage
    activeWorkers := 0
    for _, worker := range c.workers {
        if worker.Status == WorkerStatusActive {
            activeWorkers++
        }
    }
    usage["worker_usage"] = float64(activeWorkers) / float64(len(c.workers))

    return usage
}

// Error Handling
func (c *Coordinator) handleError(err error, context string) {
    if err == nil {
        return
    }

    // Log error with context
    fmt.Printf("Coordinator error in %s: %v\n", context, err)

    // Handle specific error types
    switch {
    case errors.Is(err, ErrWorkerNotFound):
        // Handle missing worker
    case errors.Is(err, ErrRegionNotFound):
        // Handle missing region
    case errors.Is(err, ErrNotEnoughWorkers):
        // Handle worker shortage
    default:
        // Handle unknown error
    }
}

// State Recovery
func (c *Coordinator) saveState(ctx context.Context) error {
    c.mu.RLock()
    defer c.mu.RUnlock()

    // Save coordinator state to storage
    state := map[string]interface{}{
        "worker_count": len(c.workers),
        "task_count":   len(c.taskStatus),
        "timestamp":    time.Now().UTC(),
    }

    return c.store.SaveCoordinatorState(ctx, state)
}

func (c *Coordinator) restoreState(ctx context.Context) error {
    if _, err := c.store.LoadCoordinatorState(ctx); err != nil {
        return fmt.Errorf("failed to load coordinator state: %w", err)
    }

    // Restore workers
    if err := c.restoreWorkers(); err != nil {
        return fmt.Errorf("failed to restore workers: %w", err)
    }

    // Restore TEE pairs
    if err := c.restoreTEEPairs(); err != nil {
        return fmt.Errorf("failed to restore TEE pairs: %w", err)
    }

    return nil
}

func (c *Coordinator) restoreTEEPairs() error {
    c.regionLock.Lock()
    defer c.regionLock.Unlock()

    // Implementation would restore TEE pair state
    return nil
}

// Utility Methods
func generateTaskID() string {
    return fmt.Sprintf("task-%d-%s", time.Now().UnixNano(), randomString(8))
}

func randomString(n int) string {
    const letters = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
    b := make([]byte, n)
    for i := range b {
        b[i] = letters[rand.Intn(len(letters))]
    }
    return string(b)
}


