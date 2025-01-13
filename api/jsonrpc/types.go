// api/jsonrpc/types.go
package jsonrpc

import "time"

// Worker information
type Worker struct {
    ID           string    `json:"id"`
    EnclaveType  string    `json:"enclave_type"`
    Status       string    `json:"status"`
    ActiveSince  time.Time `json:"active_since"`
    TasksHandled uint64    `json:"tasks_handled"`
}

// Task information
type Task struct {
    ID        string    `json:"id"`
    Status    string    `json:"status"`
    CreatedAt time.Time `json:"created_at"`
    WorkerIDs []string  `json:"worker_ids"`
    Progress  float64   `json:"progress"`
    Error     string    `json:"error,omitempty"`
}

// Coordination Request/Response types
type GetRegionWorkersRequest struct {
    RegionID string `json:"region_id"`
}

type GetRegionWorkersResponse struct {
    Workers []*Worker `json:"workers"`
}

type GetRegionTasksRequest struct {
    RegionID string `json:"region_id"`
}

type GetRegionTasksResponse struct {
    Tasks []*Task `json:"tasks"`
}