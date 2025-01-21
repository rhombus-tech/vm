// interfaces/tee.go
package interfaces

import (
    "context"
    "time"
)

// TEEPair represents the core pair configuration
type TEEPair struct {
    ID          string `json:"id"`
    SGXID       []byte `json:"sgx_id"`
    SEVID       []byte `json:"sev_id"`
}

// TEEPairConfig represents configuration for creating a new TEE pair
type TEEPairConfig struct {
    ID          string `json:"id"`
    SGXEndpoint string `json:"sgx_endpoint"`
    SEVEndpoint string `json:"sev_endpoint"`
    Status      string `json:"status"`
}

// TEEPairInfo represents runtime information about a TEE pair
type TEEPairInfo struct {
    ID          string    `json:"id"`
    SGXEndpoint string    `json:"sgx_endpoint"`
    SEVEndpoint string    `json:"sev_endpoint"`
    Status      string    `json:"status"`
    LastUpdate  time.Time `json:"last_update"`
}

// TEEPairMetrics represents runtime metrics for a TEE pair
type TEEPairMetrics struct {
    PairID         string    `json:"pair_id"`
    LoadFactor     float64   `json:"load_factor"`
    SuccessRate    float64   `json:"success_rate"`
    ErrorRate      float64   `json:"error_rate"`
    TasksProcessed uint64    `json:"tasks_processed"`
    LastUpdate     time.Time `json:"last_update"`
}

// Storage interface for TEE operations
type Storage interface {
    SaveTEEPairInfo(ctx context.Context, info *TEEPairInfo) error
    GetTEEPairInfo(ctx context.Context, id string) (*TEEPairInfo, error)
    SaveTEEMetrics(ctx context.Context, pairID string, metrics *TEEPairMetrics) error
    GetTEEMetrics(ctx context.Context, pairID string) (*TEEPairMetrics, error)
}

// Coordinator interface for TEE coordination
type Coordinator interface {
    RegisterWorker(ctx context.Context, workerID string, enclaveID []byte) error
    UnregisterWorker(ctx context.Context, workerID string) error
    GetWorker(workerID string) (Worker, bool)
    ListWorkers() []string
}

type Worker interface {
    ID() string
    EnclaveID() []byte
    Status() string
    LastActive() time.Time
}