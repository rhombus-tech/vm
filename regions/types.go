// regions/types.go
package regions

import (
    "time"
)

// TEEPairState represents the current state of a TEE pair
type TEEPairState struct {
    Status        string        `json:"status"`      // active, degraded, failed
    LastHealthy   time.Time     `json:"last_healthy"`
    ActiveTasks   int           `json:"active_tasks"`
    ErrorCount    int           `json:"error_count"`
    LoadFactor    float64       `json:"load_factor"`
    SuccessRate   float64       `json:"success_rate"`
    AverageLatency time.Duration `json:"average_latency"`
}

// Clone creates a deep copy of TEEPairState
func (s *TEEPairState) Clone() *TEEPairState {
    return &TEEPairState{
        Status:        s.Status,
        LastHealthy:   s.LastHealthy,
        ActiveTasks:   s.ActiveTasks,
        ErrorCount:    s.ErrorCount,
        LoadFactor:    s.LoadFactor,
        SuccessRate:   s.SuccessRate,
        AverageLatency: s.AverageLatency,
    }
}

// TEEPairMetrics holds performance metrics for a TEE pair
type TEEPairMetrics struct {
    // Existing fields
    SuccessRate    float64       `json:"success_rate"`
    AverageLatency time.Duration `json:"average_latency"`
    ErrorRate      float64       `json:"error_rate"`
    TasksProcessed uint64        `json:"tasks_processed"`
    LastHealthy    time.Time     `json:"last_healthy"`
    LoadFactor     float64       `json:"load_factor"`
	ExecutionTime  time.Duration `json:"execution_time"`

    // Add new fields for better monitoring
    PairID            string    `json:"pair_id"`
    SGXEndpoint       string    `json:"sgx_endpoint"`
    SEVEndpoint       string    `json:"sev_endpoint"`
    LastHealthCheck   time.Time `json:"last_health_check"`
    ConsecutiveErrors uint64    `json:"consecutive_errors"` // Changed to uint64
    Status           string    `json:"status"`
    
    // Performance metrics
    CPUUsage        float64 `json:"cpu_usage"`
    MemoryUsage     float64 `json:"memory_usage"`
    NetworkLatency  float64 `json:"network_latency"`
    
    // Attestation metrics
    LastAttestation      time.Time `json:"last_attestation"`
    AttestationFailures  uint64    `json:"attestation_failures"` // Changed to uint64
    
    // Task-specific metrics
    PendingTasks    uint64        `json:"pending_tasks"`     // Changed to uint64
    FailedTasks     uint64        `json:"failed_tasks"`      // Changed to uint64
    AverageTaskTime time.Duration `json:"average_task_time"`
}

// Clone creates a deep copy of TEEPairMetrics
func (m *TEEPairMetrics) Clone() *TEEPairMetrics {
    return &TEEPairMetrics{
        // Copy all fields
        SuccessRate:        m.SuccessRate,
        AverageLatency:     m.AverageLatency,
        ErrorRate:          m.ErrorRate,
        TasksProcessed:     m.TasksProcessed,
        LastHealthy:        m.LastHealthy,
        LoadFactor:         m.LoadFactor,
        PairID:            m.PairID,
        SGXEndpoint:       m.SGXEndpoint,
        SEVEndpoint:       m.SEVEndpoint,
        LastHealthCheck:   m.LastHealthCheck,
        ConsecutiveErrors: m.ConsecutiveErrors,
        Status:           m.Status,
        CPUUsage:        m.CPUUsage,
        MemoryUsage:     m.MemoryUsage,
        NetworkLatency:  m.NetworkLatency,
        LastAttestation: m.LastAttestation,
        AttestationFailures: m.AttestationFailures,
        PendingTasks:    m.PendingTasks,
        FailedTasks:     m.FailedTasks,
        AverageTaskTime: m.AverageTaskTime,
    }
}

// IsHealthy returns whether the TEE pair is considered healthy
func (m *TEEPairMetrics) IsHealthy(config *TEEPairConfig) bool {
    return m.Status == TEEPairStatusActive &&
           m.SuccessRate >= config.Thresholds.MinSuccessRate &&  // Access through Thresholds
           m.ErrorRate <= config.Thresholds.MaxErrorRate &&      // Access through Thresholds
           m.AverageLatency <= config.Thresholds.MaxLatency &&   // Access through Thresholds
           m.ConsecutiveErrors == 0
}

// UpdateMetrics updates the metrics with new task results
func (m *TEEPairMetrics) UpdateMetrics(success bool, duration time.Duration) {
    m.TasksProcessed++
    
    if success {
        m.ConsecutiveErrors = 0
        m.LastHealthy = time.Now()
        // Fix the calculation using uint64
        successfulTasks := m.TasksProcessed - m.FailedTasks
        m.SuccessRate = float64(successfulTasks) / float64(m.TasksProcessed)
    } else {
        m.FailedTasks++
        m.ConsecutiveErrors++
        m.ErrorRate = float64(m.FailedTasks) / float64(m.TasksProcessed)
    }

    // Update average task time
    if m.AverageTaskTime == 0 {
        m.AverageTaskTime = duration
    } else {
        m.AverageTaskTime = (m.AverageTaskTime + duration) / 2
    }

    // Update status based on metrics
    m.updateStatus()
}

func DefaultTEEPairConfig() *TEEPairConfig {
    config := &TEEPairConfig{
        ID:          "default",
        SGXEndpoint: "localhost:50051",
        SEVEndpoint: "localhost:50052",
        Capacity:    100,
        Priority:    1,
        // Initialize the nested structs
        Thresholds: struct {
            MinSuccessRate float64       `json:"min_success_rate"`
            MaxErrorRate   float64       `json:"max_error_rate"`
            MaxLatency     time.Duration `json:"max_latency"`
            MaxLoadFactor  float64       `json:"max_load_factor"`
        }{
            MinSuccessRate: 0.95,  // 95%
            MaxErrorRate:   0.05,  // 5%
            MaxLatency:    5 * time.Second,
            MaxLoadFactor: 0.8,    // 80%
        },
        HealthCheck: struct {
            Interval     time.Duration `json:"interval"`
            MaxFailures  uint64        `json:"max_failures"`
            MinHealthy   uint64        `json:"min_healthy"`
        }{
            Interval:    30 * time.Second,
            MaxFailures: 3,
            MinHealthy:  1,
        },
    }

    return config
}

// updateStatus updates the status based on current metrics
func (m *TEEPairMetrics) updateStatus() {
    switch {
    case m.ConsecutiveErrors >= 3:
        m.Status = TEEPairStatusFailed
    case m.ErrorRate > 0.1 || m.SuccessRate < 0.9:
        m.Status = TEEPairStatusDegraded
    default:
        m.Status = TEEPairStatusActive
    }
}

// ResetMetrics resets the metrics to initial values
func (m *TEEPairMetrics) ResetMetrics() {
    m.TasksProcessed = 0
    m.FailedTasks = 0
    m.ConsecutiveErrors = 0
    m.SuccessRate = 1.0
    m.ErrorRate = 0.0
    m.AverageTaskTime = 0
    m.PendingTasks = 0
    m.Status = TEEPairStatusActive
}


// Constants for TEE pair status
const (
    TEEPairStatusActive   = "active"
    TEEPairStatusDegraded = "degraded"
    TEEPairStatusFailed   = "failed"
)

// ExecutionRequirements defines requirements for TEE pair selection
type ExecutionRequirements struct {
    MaxLatency    time.Duration `json:"max_latency"`
    MinSuccessRate float64      `json:"min_success_rate"`
    Priority      int           `json:"priority"`
    Geographic    *GeoRequirements `json:"geographic,omitempty"`
}

// GeoRequirements defines geographic requirements for TEE pair selection
type GeoRequirements struct {
    PreferredRegion string  `json:"preferred_region"`
    MaxDistance     float64 `json:"max_distance"` // in kilometers
    Required        bool    `json:"required"`
}