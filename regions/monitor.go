// regions/monitor.go
package regions

import (
    "context"
    "fmt"
    "sync"
    "time"
)

type PerformanceMonitor struct {
    manager     *RegionManager
    interval    time.Duration
    metrics     map[string]map[string]*PerformanceMetrics
    mu          sync.RWMutex
}

type PerformanceMetrics struct {
    CPU           float64       `json:"cpu_usage"`
    Memory        float64       `json:"memory_usage"`
    NetworkLatency time.Duration `json:"network_latency"`
    TaskQueue     int           `json:"task_queue"`
    LastUpdate    time.Time     `json:"last_update"`
}

func NewPerformanceMonitor(manager *RegionManager, interval time.Duration) *PerformanceMonitor {
    return &PerformanceMonitor{
        manager:  manager,
        interval: interval,
        metrics:  make(map[string]map[string]*PerformanceMetrics),
    }
}

func (pm *PerformanceMonitor) StartMonitoring(ctx context.Context) {
    go func() {
        ticker := time.NewTicker(pm.interval)
        defer ticker.Stop()

        for {
            select {
            case <-ticker.C:
                if err := pm.collectMetrics(ctx); err != nil {
                    fmt.Printf("Performance monitoring error: %v\n", err)
                }
            case <-ctx.Done():
                return
            }
        }
    }()
}

func (pm *PerformanceMonitor) collectMetrics(ctx context.Context) error {
    regions := pm.manager.ListRegions()
    
    for _, regionID := range regions {
        pairs, err := pm.manager.GetTEEPairs(regionID)
        if err != nil {
            fmt.Printf("Error getting TEE pairs for region %s: %v\n", regionID, err)
            continue
        }

        for _, pair := range pairs {
            metrics, err := pm.collectPairMetrics(ctx, regionID, pair.ID)
            if err != nil {
                fmt.Printf("Error collecting metrics for pair %s: %v\n", pair.ID, err)
                continue
            }

            pm.updateMetrics(regionID, pair.ID, metrics)
        }
    }

    return nil
}

func (pm *PerformanceMonitor) collectPairMetrics(ctx context.Context, regionID, pairID string) (*PerformanceMetrics, error) {
    // Here you would implement actual metric collection from your TEE systems
    // This is a placeholder that should be replaced with real metric collection
    return &PerformanceMetrics{
        CPU:            0.5,  // Example values
        Memory:         0.6,
        NetworkLatency: time.Millisecond * 100,
        TaskQueue:      5,
        LastUpdate:     time.Now(),
    }, nil
}

func (pm *PerformanceMonitor) updateMetrics(regionID, pairID string, metrics *PerformanceMetrics) {
    pm.mu.Lock()
    defer pm.mu.Unlock()

    if pm.metrics[regionID] == nil {
        pm.metrics[regionID] = make(map[string]*PerformanceMetrics)
    }
    pm.metrics[regionID][pairID] = metrics
}

func (pm *PerformanceMonitor) GetMetrics(regionID, pairID string) (*PerformanceMetrics, error) {
    pm.mu.RLock()
    defer pm.mu.RUnlock()

    if region, exists := pm.metrics[regionID]; exists {
        if metrics, exists := region[pairID]; exists {
            return metrics, nil
        }
    }
    return nil, fmt.Errorf("no metrics found for TEE pair %s in region %s", pairID, regionID)
}