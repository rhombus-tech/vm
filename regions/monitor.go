// regions/monitor.go
package regions

import (
	"context"
	"fmt"
	"log"
	"sync"
	"time"
)

type PerformanceMonitor struct {
    manager     *RegionManager
    interval    time.Duration
    metrics     map[string]map[string]*PerformanceMetrics
    mu          sync.RWMutex
    done        chan struct{}
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
        done:     make(chan struct{}),
    }
}

func (pm *PerformanceMonitor) Start(ctx context.Context) {
    go func() {
        ticker := time.NewTicker(pm.interval)
        defer ticker.Stop()

        for {
            select {
            case <-ticker.C:
                if err := pm.collectMetrics(ctx); err != nil {
                    log.Printf("Performance monitoring error: %v\n", err)
                }
            case <-pm.done:
                return
            case <-ctx.Done():
                return
            }
        }
    }()
}

// Stop stops the monitoring process
func (pm *PerformanceMonitor) Stop() {
    close(pm.done)
}


func (pm *PerformanceMonitor) collectMetrics(ctx context.Context) error {
    pm.mu.Lock()
    defer pm.mu.Unlock()

    regions := pm.manager.ListRegions()
    
    for _, regionID := range regions {
        pairs, err := pm.manager.GetTEEPairs(regionID)
        if err != nil {
            log.Printf("Error getting TEE pairs for region %s: %v", regionID, err)
            continue
        }

        for _, pair := range pairs {
            metrics := pm.collectPairMetrics(ctx, regionID, pair.ID)
            
            // Store metrics
            if pm.metrics[regionID] == nil {
                pm.metrics[regionID] = make(map[string]*PerformanceMetrics)
            }
            pm.metrics[regionID][pair.ID] = metrics

            // Update metrics in storage
            if err := pm.manager.store.SaveMetrics(ctx, regionID, pair.ID, &TEEPairMetrics{
                PairID:          pair.ID,
                LastHealthCheck: time.Now(),
                CPUUsage:        metrics.CPU,
                MemoryUsage:     metrics.Memory,
                NetworkLatency:  float64(metrics.NetworkLatency.Milliseconds()),
                PendingTasks:    uint64(metrics.TaskQueue),
                ExecutionTime:   metrics.NetworkLatency,
            }); err != nil {
                log.Printf("Error saving metrics for pair %s: %v", pair.ID, err)
            }
        }
    }

    return nil
}



func (pm *PerformanceMonitor) collectPairMetrics(ctx context.Context, regionID, pairID string) *PerformanceMetrics {
    // Add real metric collection logic here
    return &PerformanceMetrics{
        CPU:            0.0,
        Memory:         0.0,
        NetworkLatency: 100 * time.Millisecond,
        TaskQueue:      0,
        LastUpdate:     time.Now(),
    }
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