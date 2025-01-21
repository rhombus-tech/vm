package regions

import (
	"context"
	"errors"
	"fmt"
	"log"
	"math"
	"sync"
	"time"

	"github.com/rhombus-tech/vm/core"
)

var (
    ErrNoHealthyRegions = errors.New("no healthy regions available")
    ErrRegionOverloaded = errors.New("region is overloaded")
    ErrRegionUnhealthy  = errors.New("region is unhealthy")
)

type Region struct {
    ID          string
    WorkerIDs   []string
    IsHealthy   bool
    LastUpdated time.Time
    Location    *GeoLocation
    TEEPairs    [][2]core.TEEAddress
}

type GeoLocation struct {
    Latitude    float64
    Longitude   float64
    DataCenter  string
    Country     string
    Region      string // e.g., "us-east-1"
}

type RegionMetrics struct {
    LoadFactor      float64   // 0.0-1.0
    LatencyMs       float64   // Average latency in milliseconds
    ErrorRate       float64   // Error rate in last window
    ActiveWorkers   int       // Number of active workers
    PendingTasks    int       // Number of pending tasks
    LastHealthCheck time.Time // Last successful health check
    NetworkLatency  map[string]float64  // Latency to other regions
    TEEMetrics      map[string]*TEEMetrics
}

type TEEMetrics struct {
    EnclaveID    []byte
    Type         string  // "SGX" or "SEV"
    LoadFactor   float64
    SuccessRate  float64
    LastAttested time.Time
}

type BalancerConfig struct {
    MaxLoadFactor     float64          // Maximum load factor (0.0-1.0)
    MaxLatencyMs      float64          // Maximum latency in milliseconds
    MaxErrorRate      float64          // Maximum error rate threshold
    MinSuccessRate    float64          // Minimum success rate required (0.0-1.0)
    MinActiveWorkers  int              // Minimum number of active workers
    MaxPendingTasks   int              // Maximum number of pending tasks
    HealthCheckWindow time.Duration    // Health check window duration
    GeoPreference     bool             // Whether to prefer geographically closer regions
    MaxDistance       float64          // Maximum acceptable distance in km
}

type RegionBalancer struct {
    regions      map[string]*Region
    metrics      map[string]*RegionMetrics
    pairMetrics  map[string][]TEEPairMetrics
    thresholds   *BalancerConfig
    mu           sync.RWMutex
}


func (rb *RegionBalancer) GetHealthyPairs(
    ctx context.Context,
    regionID string,
) []string {
    rb.mu.RLock()
    defer rb.mu.RUnlock()

    healthy := make([]string, 0)
    for _, metrics := range rb.pairMetrics[regionID] {
        if time.Since(metrics.LastHealthy) < rb.thresholds.HealthCheckWindow &&
           metrics.LoadFactor < rb.thresholds.MaxLoadFactor &&
           metrics.SuccessRate > rb.thresholds.MinSuccessRate {
            healthy = append(healthy, metrics.PairID)
        }
    }
    return healthy
}


func NewRegionBalancer(config *BalancerConfig) *RegionBalancer {
    if config == nil {
        config = &BalancerConfig{
            MaxLoadFactor:     0.8,
            MaxLatencyMs:      1000,
            MaxErrorRate:      0.1,
            MinSuccessRate:    0.95,  // Add this default
            MinActiveWorkers:  2,
            MaxPendingTasks:   1000,
            HealthCheckWindow: 5 * time.Minute,
            // Add new defaults
            GeoPreference:     true,
            MaxDistance:       5000, // 5000km
        }
    }

    return &RegionBalancer{
        regions:     make(map[string]*Region),
        metrics:     make(map[string]*RegionMetrics),
        pairMetrics: make(map[string][]TEEPairMetrics), // Add this field
        thresholds:  config,
    }
}


func (b *RegionBalancer) SelectRegionWithLocation(ctx context.Context, preferredLocation *GeoLocation) (string, error) {
    b.mu.RLock()
    defer b.mu.RUnlock()

    if preferredLocation == nil || !b.thresholds.GeoPreference {
        // Fall back to existing selection logic if no location preference
        return b.selectRegionByLoad()
    }

    selected := ""
    bestScore := -1.0

    for id, region := range b.regions {
        if !b.isRegionHealthy(id, region) {
            continue
        }

        metrics := b.metrics[id]
        if metrics == nil || !b.isRegionWithinThresholds(metrics) {
            continue
        }

        // Calculate score based on both load and distance
        distance := calculateDistance(preferredLocation, region.Location)
        if distance > b.thresholds.MaxDistance {
            continue
        }

        score := calculateRegionScore(metrics, distance, b.thresholds)
        if score > bestScore {
            selected = id
            bestScore = score
        }
    }

    if selected == "" {
        return "", ErrNoHealthyRegions
    }

    return selected, nil
}

func (b *RegionBalancer) selectRegionByLoad() (string, error) {
    selected := ""
    lowestLoad := 1.0

    for id, region := range b.regions {
        if !b.isRegionHealthy(id, region) {
            continue
        }

        metrics := b.metrics[id]
        if metrics == nil || !b.isRegionWithinThresholds(metrics) {
            continue
        }

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

func calculateDistance(l1, l2 *GeoLocation) float64 {
    if l1 == nil || l2 == nil {
        return 0
    }
    // Implement Haversine formula for actual distance calculation
    // This is a simplified placeholder
    return 0
}

func calculateRegionScore(metrics *RegionMetrics, distance float64, thresholds *BalancerConfig) float64 {
    const (
        loadWeight     = 0.4
        latencyWeight  = 0.3
        distanceWeight = 0.3
    )

    loadScore := 1 - metrics.LoadFactor
    latencyScore := 1 - (metrics.LatencyMs / thresholds.MaxLatencyMs)
    distanceScore := 1 - (distance / thresholds.MaxDistance)

    return loadScore*loadWeight + latencyScore*latencyWeight + distanceScore*distanceWeight
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

func (rb *RegionBalancer) GetRegionMetrics(regionID string) []TEEPairMetrics {
    rb.mu.RLock()
    defer rb.mu.RUnlock()
    
    if metrics, exists := rb.pairMetrics[regionID]; exists {
        return metrics
    }
    return nil
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

func (rb *RegionBalancer) SelectOptimalPair(ctx context.Context, regionID string) (*TEEPair, error) {
    rb.mu.RLock()
    defer rb.mu.RUnlock()

    pairs := rb.pairMetrics[regionID]
    if len(pairs) == 0 {
        return nil, fmt.Errorf("no TEE pairs available for region %s", regionID)
    }

    var selectedPair *TEEPair
    var minLoad float64 = math.MaxFloat64

    for _, metrics := range pairs {
        if metrics.LoadFactor < minLoad &&
           metrics.SuccessRate > rb.thresholds.MinSuccessRate &&
           time.Since(metrics.LastHealthy) < rb.thresholds.HealthCheckWindow {
            minLoad = metrics.LoadFactor
            selectedPair = &TEEPair{
                ID:          metrics.PairID,
                SGXEndpoint: metrics.SGXEndpoint,
                SEVEndpoint: metrics.SEVEndpoint,
                Status:      "active",
            }
        }
    }

    if selectedPair == nil {
        return nil, fmt.Errorf("no healthy TEE pairs available for region %s", regionID)
    }

    return selectedPair, nil
}

// regions/balancer.go

// Add these helper functions
func calculateLoadFactor(metrics *TEEPairMetrics) float64 {
    // Example implementation - customize based on your needs
    // This could consider:
    // - Number of active tasks
    // - CPU usage
    // - Memory usage
    // - Network load
    // Returns a value between 0.0 and 1.0
    
    // Simple example:
    if metrics.ExecutionTime == 0 {
        return 0.0
    }
    
    // Higher execution time means higher load
    loadFactor := float64(metrics.ExecutionTime.Milliseconds()) / 1000.0 // Convert to seconds
    if loadFactor > 1.0 {
        loadFactor = 1.0
    }
    return loadFactor
}

func calculateSuccessRate(metrics *TEEPairMetrics) float64 {
    if metrics.TasksProcessed == 0 {
        return 1.0 // No tasks processed yet
    }
    successfulTasks := metrics.TasksProcessed - metrics.FailedTasks
    return float64(successfulTasks) / float64(metrics.TasksProcessed)
}



func (rb *RegionBalancer) UpdatePairMetrics(ctx context.Context, regionID string, pairID string, metrics *TEEPairMetrics) error {
    rb.mu.Lock()
    defer rb.mu.Unlock()

    if rb.pairMetrics[regionID] == nil {
        rb.pairMetrics[regionID] = make([]TEEPairMetrics, 0)
    }

    // Update existing metrics or add new ones
    found := false
    for i := range rb.pairMetrics[regionID] {
        if rb.pairMetrics[regionID][i].PairID == pairID {
            rb.pairMetrics[regionID][i] = *metrics
            found = true
            break
        }
    }

    if !found {
        rb.pairMetrics[regionID] = append(rb.pairMetrics[regionID], *metrics)
    }

    return nil
}

func (rb *RegionBalancer) CollectMetrics(ctx context.Context) {
    ticker := time.NewTicker(rb.thresholds.HealthCheckWindow)
    defer ticker.Stop()

    for {
        select {
        case <-ticker.C:
            rb.mu.Lock()
            for regionID, pairs := range rb.pairMetrics {
                // Log region metrics collection
                log.Printf("Collecting metrics for region: %s", regionID)
                
                for i := range pairs {
                    metrics := &pairs[i]
                    // Update load factor
                    metrics.LoadFactor = calculateLoadFactor(metrics)
                    // Update success rate
                    metrics.SuccessRate = calculateSuccessRate(metrics)
                    // Update last health check
                    metrics.LastHealthCheck = time.Now()
                    
                    // Store updated metrics
                    rb.pairMetrics[regionID][i] = *metrics
                }
            }
            rb.mu.Unlock()
        case <-ctx.Done():
            return
        }
    }
}

func (rb *RegionBalancer) UpdateSpecificPairMetrics(regionID string, pairID string, updateFn func(*TEEPairMetrics)) error {
    rb.mu.Lock()
    defer rb.mu.Unlock()

    pairs, exists := rb.pairMetrics[regionID]
    if !exists {
        return fmt.Errorf("region %s not found", regionID)
    }

    for i := range pairs {
        if pairs[i].PairID == pairID {
            updateFn(&pairs[i])
            rb.pairMetrics[regionID][i] = pairs[i]
            return nil
        }
    }

    return fmt.Errorf("pair %s not found in region %s", pairID, regionID)
}
