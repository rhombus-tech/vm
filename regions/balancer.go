// regions/balancer.go
package regions

import (
	"context"
	"errors"
	"sync"
	"time"
)

var (
	ErrNoHealthyRegions = errors.New("no healthy regions available")
	ErrRegionOverloaded = errors.New("region is overloaded")
	ErrRegionUnhealthy  = errors.New("region is unhealthy")
)

type RegionBalancer struct {
	regions    map[string]*Region
	metrics    map[string]*RegionMetrics
	thresholds *BalancerConfig
	mu         sync.RWMutex
}

type Region struct {
	ID          string
	WorkerIDs   []string
	IsHealthy   bool
	LastUpdated time.Time
}

type RegionMetrics struct {
	LoadFactor      float64   // 0.0-1.0
	LatencyMs       float64   // Average latency in milliseconds
	ErrorRate       float64   // Error rate in last window
	ActiveWorkers   int       // Number of active workers
	PendingTasks    int       // Number of pending tasks
	LastHealthCheck time.Time // Last successful health check
}

type BalancerConfig struct {
	MaxLoadFactor     float64
	MaxLatencyMs      float64
	MaxErrorRate      float64
	MinActiveWorkers  int
	MaxPendingTasks   int
	HealthCheckWindow time.Duration
}

func NewRegionBalancer(config *BalancerConfig) *RegionBalancer {
	if config == nil {
		config = &BalancerConfig{
			MaxLoadFactor:     0.8,
			MaxLatencyMs:      1000,
			MaxErrorRate:      0.1,
			MinActiveWorkers:  2,
			MaxPendingTasks:   1000,
			HealthCheckWindow: 5 * time.Minute,
		}
	}

	return &RegionBalancer{
		regions:    make(map[string]*Region),
		metrics:    make(map[string]*RegionMetrics),
		thresholds: config,
	}
}

func (b *RegionBalancer) SelectRegion(ctx context.Context) (string, error) {
	b.mu.RLock()
	defer b.mu.RUnlock()

	selected := ""
	lowestLoad := 1.0

	for id, region := range b.regions {
		if !b.isRegionHealthy(id, region) {
			continue
		}

		metrics := b.metrics[id]
		if metrics == nil {
			continue
		}

		// Check if region is within thresholds
		if !b.isRegionWithinThresholds(metrics) {
			continue
		}

		// Select region with lowest load
		if metrics.LoadFactor < lowestLoad {
			selected = id
			lowestLoad = metrics.LoadFactor
		}
	}

	if selected == "" {
		return "", ErrNoHealthyRegions
	}

	return selected, nil
}

func (b *RegionBalancer) UpdateMetrics(regionID string, metrics *RegionMetrics) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.metrics[regionID] = metrics
}

func (b *RegionBalancer) AddRegion(region *Region) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.regions[region.ID] = region
}

func (b *RegionBalancer) RemoveRegion(regionID string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	delete(b.regions, regionID)
	delete(b.metrics, regionID)
}

func (b *RegionBalancer) GetRegionHealth(regionID string) (bool, error) {
	b.mu.RLock()
	defer b.mu.RUnlock()

	region, ok := b.regions[regionID]
	if !ok {
		return false, errors.New("region not found")
	}

	return b.isRegionHealthy(regionID, region), nil
}

func (b *RegionBalancer) isRegionHealthy(regionID string, region *Region) bool {
	if !region.IsHealthy {
		return false
	}

	metrics, ok := b.metrics[regionID]
	if !ok {
		return false
	}

	// Check if health check is recent enough
	if time.Since(metrics.LastHealthCheck) > b.thresholds.HealthCheckWindow {
		return false
	}

	return true
}

func (b *RegionBalancer) isRegionWithinThresholds(metrics *RegionMetrics) bool {
	return metrics.LoadFactor <= b.thresholds.MaxLoadFactor &&
		metrics.LatencyMs <= b.thresholds.MaxLatencyMs &&
		metrics.ErrorRate <= b.thresholds.MaxErrorRate &&
		metrics.ActiveWorkers >= b.thresholds.MinActiveWorkers &&
		metrics.PendingTasks <= b.thresholds.MaxPendingTasks
}

func (b *RegionBalancer) GetRegionMetrics(regionID string) (*RegionMetrics, error) {
	b.mu.RLock()
	defer b.mu.RUnlock()

	metrics, ok := b.metrics[regionID]
	if !ok {
		return nil, errors.New("region metrics not found")
	}

	return metrics, nil
}

// For testing/monitoring
func (b *RegionBalancer) GetAllRegionMetrics() map[string]*RegionMetrics {
	b.mu.RLock()
	defer b.mu.RUnlock()

	// Return copy to avoid concurrent map access
	metrics := make(map[string]*RegionMetrics, len(b.metrics))
	for id, m := range b.metrics {
		metrics[id] = m
	}
	return metrics
}
