// regions/manager.go

package regions

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/ava-labs/avalanchego/x/merkledb"
)

type RegionManager struct {
    configs    map[string]*RegionConfig
    cache      map[string]*RegionConfig
    balancer   *RegionBalancer
    store      Storage
    states     map[string]map[string]*TEEPairState
    metrics    map[string]map[string]*TEEPairMetrics
    cacheLock  sync.RWMutex  // Changed from mu to cacheLock to match existing code
}

// RegionManager handles region configuration management
// Storage interface defines methods required for region configuration persistence
type Storage interface {
    SetRegionConfig(regionID string, config *RegionConfig) error
    GetRegionConfig(regionID string) (*RegionConfig, error)
    DeleteRegionConfig(ctx context.Context, regionID string) error
    GetRegionProof(ctx context.Context, regionID string) (*merkledb.Proof, error)
    
    SaveTEEState(ctx context.Context, regionID, pairID string, state *TEEPairState) error
    GetTEEState(ctx context.Context, regionID, pairID string) (*TEEPairState, error)
    SaveMetrics(ctx context.Context, regionID, pairID string, metrics *TEEPairMetrics) error
    GetMetrics(ctx context.Context, regionID, pairID string) (*TEEPairMetrics, error)
}

// RegisterTEEPair registers a new TEE pair in a region
func (rm *RegionManager) RegisterTEEPair(ctx context.Context, regionID string, config *RegionConfig) error {
    rm.cacheLock.Lock()
    defer rm.cacheLock.Unlock()

    // Initialize region maps if they don't exist
    if _, exists := rm.states[regionID]; !exists {
        rm.states[regionID] = make(map[string]*TEEPairState)
        rm.metrics[regionID] = make(map[string]*TEEPairMetrics)
    }

    // Initialize state
    state := &TEEPairState{
        Status:      "active",
        LastHealthy: time.Now(),
        LoadFactor:  0.0,
        SuccessRate: 1.0,
    }

    // Initialize metrics
    metrics := &TEEPairMetrics{
        SuccessRate:    1.0,
        LastHealthy:    time.Now(),
        LoadFactor:     0.0,
        TasksProcessed: 0,
    }

    // Store in memory
    rm.configs[regionID] = config
    rm.states[regionID][config.ID] = state
    rm.metrics[regionID][config.ID] = metrics

    // Store in persistent storage
    if err := rm.store.SetRegionConfig(regionID, config); err != nil {
        return fmt.Errorf("failed to save config: %w", err)
    }
    if err := rm.store.SaveTEEState(ctx, regionID, config.ID, state); err != nil {
        return fmt.Errorf("failed to save state: %w", err)
    }
    if err := rm.store.SaveMetrics(ctx, regionID, config.ID, metrics); err != nil {
        return fmt.Errorf("failed to save metrics: %w", err)
    }

    return nil
}


func (rm *RegionManager) UpdateTEEState(ctx context.Context, regionID string, pairID string, update func(*TEEPairState)) error {
    rm.cacheLock.Lock()
    defer rm.cacheLock.Unlock()

    // Get region config first
    config, exists := rm.configs[regionID]
    if !exists {
        return fmt.Errorf("region not found: %s", regionID)
    }

    // Verify TEE pair exists in region
    var foundPair bool
    for _, pair := range config.TEEPairs {
        if pair.ID == pairID {
            foundPair = true
            break
        }
    }
    if !foundPair {
        return fmt.Errorf("TEE pair %s not found in region %s", pairID, regionID)
    }

    // Initialize state maps if needed
    if _, exists := rm.states[regionID]; !exists {
        rm.states[regionID] = make(map[string]*TEEPairState)
    }

    state, exists := rm.states[regionID][pairID]
    if !exists {
        state = &TEEPairState{
            Status:      "active",
            LastHealthy: time.Now(),
        }
        rm.states[regionID][pairID] = state
    }

    update(state)
    
    if state.Status == "active" {
        state.LastHealthy = time.Now()
    }

    return rm.store.SaveTEEState(ctx, regionID, pairID, state)
}



func (rm *RegionManager) UpdateMetrics(ctx context.Context, regionID string, pairID string, update func(*TEEPairMetrics)) error {
    rm.cacheLock.Lock()
    defer rm.cacheLock.Unlock()

    // Get region config first
    config, exists := rm.configs[regionID]
    if !exists {
        return fmt.Errorf("region not found: %s", regionID)
    }

    // Initialize metrics maps if needed
    if _, exists := rm.metrics[regionID]; !exists {
        rm.metrics[regionID] = make(map[string]*TEEPairMetrics)
    }

    metrics, exists := rm.metrics[regionID][pairID]
    if !exists {
        metrics = &TEEPairMetrics{
            SuccessRate:    1.0,
            LastHealthy:    time.Now(),
        }
        rm.metrics[regionID][pairID] = metrics
    }

    update(metrics)
    
    // Update state based on metrics and load balancer config
    if state, exists := rm.states[regionID][pairID]; exists {
        state.LoadFactor = metrics.LoadFactor
        state.SuccessRate = metrics.SuccessRate
        state.AverageLatency = metrics.AverageLatency
        
        // Use load balancer config for thresholds
        if metrics.LoadFactor > config.LoadBalancer.MaxLoadFactor {
            state.Status = "degraded"
        } else {
            state.Status = "active"
        }

        if err := rm.store.SaveTEEState(ctx, regionID, pairID, state); err != nil {
            return fmt.Errorf("failed to save updated state: %w", err)
        }
    }

    return rm.store.SaveMetrics(ctx, regionID, pairID, metrics)
}



// GetTEEState gets the current state of a TEE pair
func (rm *RegionManager) GetTEEState(ctx context.Context, regionID string, pairID string) (*TEEPairState, error) {
    rm.cacheLock.RLock()
    defer rm.cacheLock.RUnlock()

    states, exists := rm.states[regionID]
    if !exists {
        return nil, fmt.Errorf("region not found: %s", regionID)
    }

    state, exists := states[pairID]
    if !exists {
        return nil, fmt.Errorf("TEE pair not found: %s", pairID)
    }

    return state, nil
}


// GetMetrics gets the current metrics of a TEE pair
func (rm *RegionManager) GetMetrics(ctx context.Context, regionID string, pairID string) (*TEEPairMetrics, error) {
    rm.cacheLock.RLock()
    defer rm.cacheLock.RUnlock()

    metrics, exists := rm.metrics[regionID]
    if !exists {
        return nil, fmt.Errorf("region not found: %s", regionID)
    }

    metric, exists := metrics[pairID]
    if !exists {
        return nil, fmt.Errorf("TEE pair not found: %s", pairID)
    }

    return metric, nil
}
func (rm *RegionManager) UpdateCache(regionID string, config *RegionConfig) {
    rm.cacheLock.Lock()  // Using cacheLock
    defer rm.cacheLock.Unlock()
    rm.cache[regionID] = config
}

func (rm *RegionManager) ClearCache() {
    rm.cacheLock.Lock()  // Using cacheLock
    defer rm.cacheLock.Unlock()
    rm.cache = make(map[string]*RegionConfig)
}

func (rm *RegionManager) InvalidateCache(regionID string) {
    rm.cacheLock.Lock()  // Using cacheLock
    defer rm.cacheLock.Unlock()
    delete(rm.cache, regionID)
}