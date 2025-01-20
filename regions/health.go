// regions/health.go
package regions

import (
	"context"
	"fmt"
	"log"
	"time"
)

// HealthChecker handles TEE health monitoring
type HealthChecker struct {
    manager    *RegionManager
    interval   time.Duration
    thresholds HealthThresholds
}

type HealthThresholds struct {
    MaxErrorRate      float64
    MinSuccessRate    float64
    MaxLatency       time.Duration
    MaxLoadFactor    float64
    HealthyWindow    time.Duration
}

type HealthStatus struct {
    Status        string    `json:"status"`
    LastCheck     time.Time `json:"last_check"`
    ErrorCount    int       `json:"error_count"`
    SuccessRate   float64   `json:"success_rate"`
    LoadFactor    float64   `json:"load_factor"`
    AverageLatency time.Duration `json:"average_latency"`
}

func NewHealthChecker(manager *RegionManager, interval time.Duration) *HealthChecker {
    return &HealthChecker{
        manager:  manager,
        interval: interval,
        thresholds: HealthThresholds{
            MaxErrorRate:   0.1,   // 10% error rate
            MinSuccessRate: 0.95,  // 95% success rate
            MaxLatency:     time.Second * 5,
            MaxLoadFactor:  0.8,   // 80% load
            HealthyWindow:  time.Minute * 5,
        },
    }
}

func (hc *HealthChecker) StartHealthChecks(ctx context.Context) {
    go func() {
        ticker := time.NewTicker(hc.interval)
        defer ticker.Stop()

        for {
            select {
            case <-ticker.C:
                if err := hc.performHealthChecks(ctx); err != nil {
                    fmt.Printf("Health check error: %v\n", err)
                }
            case <-ctx.Done():
                return
            }
        }
    }()
}

func (hc *HealthChecker) performHealthChecks(ctx context.Context) error {
    configs := hc.manager.ListRegions()
    
    for _, regionID := range configs {
        if err := hc.checkRegion(ctx, regionID); err != nil {
            fmt.Printf("Error checking region %s: %v\n", regionID, err)
            continue
        }
    }
    
    return nil
}

func (hc *HealthChecker) checkRegion(ctx context.Context, regionID string) error {
    pairs, err := hc.manager.GetTEEPairs(regionID)
    if err != nil {
        return fmt.Errorf("failed to get TEE pairs: %w", err)
    }

    for _, pair := range pairs {
        status, err := hc.checkTEEPair(ctx, regionID, pair.ID)
        if err != nil {
            // Log error but continue checking other pairs
            log.Printf("Error checking TEE pair %s: %v", pair.ID, err)
            continue
        }

        // Update health status through manager
        if err := hc.manager.UpdateRegionHealth(ctx, regionID, status); err != nil {
            log.Printf("Error updating health status for region %s: %v", regionID, err)
        }
    }

    return nil
}

func (hc *HealthChecker) checkTEEPair(ctx context.Context, regionID, pairID string) (*HealthStatus, error) {
    metrics, err := hc.manager.GetMetrics(ctx, regionID, pairID)
    if err != nil {
        return nil, fmt.Errorf("failed to get metrics: %w", err)
    }

    state, err := hc.manager.GetTEEState(ctx, regionID, pairID)
    if err != nil {
        return nil, fmt.Errorf("failed to get state: %w", err)
    }

    status := &HealthStatus{
        LastCheck:      time.Now(),
        ErrorCount:     state.ErrorCount,
        SuccessRate:    metrics.SuccessRate,
        LoadFactor:     metrics.LoadFactor,
        AverageLatency: metrics.AverageLatency,
    }

    // Determine health status
    if hc.isHealthy(status) {
        status.Status = "healthy"
    } else {
        status.Status = "unhealthy"
    }

    return status, nil
}

func (hc *HealthChecker) isHealthy(status *HealthStatus) bool {
    return status.ErrorCount < 3 &&
           status.SuccessRate >= hc.thresholds.MinSuccessRate &&
           status.LoadFactor < hc.thresholds.MaxLoadFactor &&
           status.AverageLatency < hc.thresholds.MaxLatency
}

func (hc *HealthChecker) updatePairHealth(ctx context.Context, regionID, pairID string, status *HealthStatus) error {
    return hc.manager.UpdateTEEState(ctx, regionID, pairID, func(state *TEEPairState) {
        state.Status = status.Status
        state.LastHealthy = status.LastCheck
        state.ErrorCount = status.ErrorCount
        state.SuccessRate = status.SuccessRate
        state.LoadFactor = status.LoadFactor
        state.AverageLatency = status.AverageLatency
    })
}