package coordination

import (
	"encoding/json"
	"fmt"
	"sync"
	"time"
)

type Worker struct {
    ID         WorkerID                    `json:"id"`
    EnclaveID  []byte                     `json:"enclaveID"`
    Status     WorkerStatus               `json:"status"`
    Type       string                     `json:"type"`      // Add TEE type: "SGX" or "SEV"
    RegionID   string                     `json:"regionID"`  // Add region assignment
    Channels   map[WorkerID]*SecureChannel `json:"channels"`
    LastActive time.Time                   `json:"lastActive"`
    
    // Metrics and monitoring
    TaskCount        uint64    `json:"taskCount"`
    SuccessCount     uint64    `json:"successCount"`
    ErrorCount       uint64    `json:"errorCount"`
    LoadFactor       float64   `json:"loadFactor"`
    
    // Unexported fields
    msgCh           chan *Message
    doneCh          chan struct{}
    coord           *Coordinator
    mu              sync.RWMutex
}


func NewWorker(id WorkerID, enclaveID []byte, workerType string, coord *Coordinator) *Worker {
    return &Worker{
        ID:         id,
        EnclaveID:  enclaveID,
        Status:     WorkerStatusIdle,
        Type:       workerType,
        Channels:   make(map[WorkerID]*SecureChannel),
        LastActive: time.Now(),
        
        // Initialize metrics
        TaskCount:    0,
        SuccessCount: 0,
        ErrorCount:   0,
        LoadFactor:   0.0,
        
        // Initialize channels
        msgCh:        make(chan *Message, 100),
        doneCh:       make(chan struct{}),
        coord:        coord,
    }
}

// Add methods to handle metrics
func (w *Worker) UpdateMetrics(success bool) {
    w.mu.Lock()
    defer w.mu.Unlock()
    
    w.TaskCount++
    if success {
        w.SuccessCount++
    } else {
        w.ErrorCount++
    }
    
    // Update load factor (simple calculation, can be made more sophisticated)
    w.LoadFactor = float64(w.TaskCount-w.SuccessCount) / float64(w.TaskCount)
    w.LastActive = time.Now()
}

// Add method to check if worker can accept new tasks
func (w *Worker) CanAcceptTask() bool {
    w.mu.RLock()
    defer w.mu.RUnlock()
    
    return w.Status == WorkerStatusActive && 
           w.LoadFactor < 0.8 && // Arbitrary threshold
           w.RegionID != ""
}

// Add method to assign worker to region
func (w *Worker) AssignToRegion(regionID string) {
    w.mu.Lock()
    defer w.mu.Unlock()
    
    w.RegionID = regionID
    w.Status = WorkerStatusActive
    w.LastActive = time.Now()
}

// Add method to unassign worker from region
func (w *Worker) UnassignFromRegion() {
    w.mu.Lock()
    defer w.mu.Unlock()
    
    w.RegionID = ""
    w.Status = WorkerStatusIdle
}

// Add method to get worker metrics
func (w *Worker) GetMetrics() map[string]interface{} {
    w.mu.RLock()
    defer w.mu.RUnlock()
    
    return map[string]interface{}{
        "taskCount":    w.TaskCount,
        "successCount": w.SuccessCount,
        "errorCount":   w.ErrorCount,
        "loadFactor":   w.LoadFactor,
        "lastActive":   w.LastActive,
        "status":       w.Status,
        "regionID":     w.RegionID,
        "type":         w.Type,
    }
}


func (w *Worker) Start() error {
    w.mu.Lock()
    defer w.mu.Unlock()
    
    w.Status = WorkerStatusActive
    w.LastActive = time.Now()
    
    // Start message processing
    go w.processMessages()
    
    return nil
}


func (w *Worker) Stop() error {
    w.mu.Lock()
    defer w.mu.Unlock()
    
    w.Status = WorkerStatusIdle
    close(w.doneCh)
    
    // Clear region assignment
    w.RegionID = ""
    
    return nil
}

func (w *Worker) SendMessage(msg *Message) error {
    // Add message to channel instead of direct handling
    select {
    case w.msgCh <- msg:
        return nil
    case <-w.doneCh:
        return fmt.Errorf("worker stopped")
    default:
        return fmt.Errorf("message channel full")
    }
}

func (w *Worker) processMessages() {
    for {
        select {
        case msg := <-w.msgCh:
            if err := w.HandleMessage(msg); err != nil {
                // Log or handle error
                fmt.Printf("Error handling message: %v\n", err)
            }
        case <-w.doneCh:
            return
        }
    }
}

func (w *Worker) HandleMessage(msg *Message) error {
    w.mu.Lock()
    defer w.mu.Unlock()

    // Update last active time
    w.LastActive = time.Now()

    // Verify message is intended for this worker
    if msg.To != w.ID {
        return fmt.Errorf("message not intended for this worker")
    }

    // Handle message based on type
    switch msg.Type {
    case MessageTypeSync:
        return w.handleSyncMessage(msg)
        
    case MessageTypeData:
        return w.handleDataMessage(msg)
        
    case MessageTypeComplete:
        return w.handleCompleteMessage(msg)
        
    case MessageTypeAttestation:
        return w.handleAttestationMessage(msg)
        
    case MessageTypeVerification:
        return w.handleVerificationMessage(msg)
        
    default:
        return fmt.Errorf("unknown message type: %v", msg.Type)
    }
}

func (w *Worker) handleSyncMessage(msg *Message) error {
    // Create response message
    response := &Message{
        Type: MessageTypeComplete,
        From: w.ID,
        To:   msg.From,
        Data: nil, // Add any response data if needed
    }
    
    // Get channel and send response
    channel, err := w.GetChannel(msg.From)
    if err != nil {
        return fmt.Errorf("failed to get channel: %w", err)
    }
    
    return channel.Send(response.Data)
}

func (w *Worker) handleDataMessage(msg *Message) error {
    // Process the data
    if err := w.processData(msg.Data); err != nil {
        return fmt.Errorf("failed to process  %w", err)
    }
    
    // Send acknowledgment if needed
    response := &Message{
        Type: MessageTypeComplete,
        From: w.ID,
        To:   msg.From,
    }
    
    channel, err := w.GetChannel(msg.From)
    if err != nil {
        return fmt.Errorf("failed to get channel: %w", err)
    }
    
    return channel.Send(response.Data)
}

func (w *Worker) handleCompleteMessage(msg *Message) error {
    // Handle completion acknowledgment
    // You might want to update some state or notify other components
    return nil
}

func (w *Worker) handleAttestationMessage(msg *Message) error {
    // Handle attestation verification
    // Implement your attestation logic here
    return nil
}

func (w *Worker) handleVerificationMessage(msg *Message) error {
    // Handle verification requests/responses
    // Implement your verification logic here
    return nil
}

func (w *Worker) processData(data []byte) error {
    // Implement your data processing logic
    // This could include:
    // - Parsing the data
    // - Performing computations
    // - Updating state
    // - etc.
    return nil
}

func (w *Worker) GetChannel(otherWorker WorkerID) (*SecureChannel, error) {
    channel, exists := w.Channels[otherWorker]
    if !exists {
        // Create new channel
        channel = NewSecureChannel(w.ID, otherWorker)
        if err := channel.EstablishSecure(); err != nil {
            return nil, fmt.Errorf("failed to establish channel: %w", err)
        }
        w.Channels[otherWorker] = channel
    }
    return channel, nil
}


func (w *Worker) handleSync(msg *Message, channel *SecureChannel) {
    // Handle sync message
}

func (w *Worker) handleData(msg *Message, channel *SecureChannel) {
    // Handle data message
}

func (w *Worker) handleAttestation(msg *Message, channel *SecureChannel) {
    // Handle attestation
}

func (w *Worker) handleComplete(msg *Message, channel *SecureChannel) {
    // Handle completion
}

// Add custom marshaling methods
func (w *Worker) MarshalJSON() ([]byte, error) {
    type Alias struct {
        ID        WorkerID                        `json:"id"`
        EnclaveID []byte                         `json:"enclaveID"`
        Status    WorkerStatus                   `json:"status"`
        Channels  map[WorkerID]*SecureChannel    `json:"channels"`
    }

    return json.Marshal(&Alias{
        ID:        w.ID,
        EnclaveID: w.EnclaveID,
        Status:    w.Status,
        Channels:  w.Channels,
    })
}

func (w *Worker) UnmarshalJSON(data []byte) error {
    type Alias struct {
        ID        WorkerID                        `json:"id"`
        EnclaveID []byte                         `json:"enclaveID"`
        Status    WorkerStatus                   `json:"status"`
        Channels  map[WorkerID]*SecureChannel    `json:"channels"`
    }

    aux := &Alias{}
    if err := json.Unmarshal(data, aux); err != nil {
        return err
    }

    w.ID = aux.ID
    w.EnclaveID = aux.EnclaveID
    w.Status = aux.Status
    w.Channels = aux.Channels

    // Reinitialize channels
    w.msgCh = make(chan *Message, 100)
    w.doneCh = make(chan struct{})

    return nil
}