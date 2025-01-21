// coordination/coordinator_types.go
package coordination

import (
	"context"
	"time"
)

type MessageType uint8

const (
    MessageTypeSync MessageType = iota
    MessageTypeData
    MessageTypeAttestation
    MessageTypeVerification
    MessageTypeComplete
)

type WorkerStatus uint8

const (
    WorkerStatusIdle WorkerStatus = iota
    WorkerStatusActive
    WorkerStatusError
)

type TaskStatus string


type Message struct {
    Type       MessageType  `json:"type"`
    From       WorkerID     `json:"from"`
    To         WorkerID     `json:"to"`
    Data       []byte       `json:"data"`
    Timestamp  time.Time    `json:"timestamp"`
}

type TaskInfo struct {
    Task      *Task
    Status    TaskStatus
    StartTime time.Time
    EndTime   time.Time
    Error     error
    Results   [][]byte
}

type TEEPairInfo struct {
    ID          string
    SGXWorker   WorkerID
    SEVWorker   WorkerID
    Channel     *SecureChannel
    TaskCount   int32
    LastUsed    time.Time
}

type TEEPairMetrics struct {
    PairID            string        `json:"pair_id"`
    LastHealthCheck   time.Time     `json:"last_health_check"`
    LastHealthy       time.Time     `json:"last_healthy"`
    SuccessRate       float64       `json:"success_rate"`
    LoadFactor        float64       `json:"load_factor"`
    ExecutionTime     time.Duration `json:"execution_time"`
    ConsecutiveErrors uint64        `json:"consecutive_errors"`
}


type Region struct {
    ID         string     
    Workers    [2]WorkerID
    CreatedAt  time.Time  
}

type Attestation struct {
    EnclaveID   []byte    
    Measurement []byte    
    Timestamp   time.Time 
    Data        []byte    
    Signature   []byte    
    RegionProof []byte    
}

// CoordinationStorage defines the interface for storage operations
type CoordinationStorage interface {
    SaveWorker(ctx context.Context, worker *Worker) error
    LoadWorker(ctx context.Context, id WorkerID) (*Worker, error)
    DeleteWorker(ctx context.Context, id WorkerID) error
    SaveChannel(ctx context.Context, channel *SecureChannel) error
    LoadChannel(ctx context.Context, worker1, worker2 WorkerID) (*SecureChannel, error)
    SaveRegion(ctx context.Context, region *Region) error
    SaveCoordinatorState(ctx context.Context, state map[string]interface{}) error
    LoadCoordinatorState(ctx context.Context) (map[string]interface{}, error)
}
