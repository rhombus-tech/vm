// storage/region_state_store.go
package storage

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/ava-labs/avalanchego/x/merkledb"
	"github.com/rhombus-tech/vm/interfaces"
	"github.com/rhombus-tech/vm/regions"
)

var _ regions.Storage = (*RegionStateStore)(nil)

// RegionStateStore provides storage operations for region state
type RegionStateStore struct {
    stateManager interfaces.StateManager 
}

// RegionSettings defines region-specific settings
type RegionSettings struct {
    MaxObjects    int    `json:"max_objects"`
    MaxEvents     int    `json:"max_events"`
    LoadBalancer  LoadBalancerConfig `json:"load_balancer"`
}

type LoadBalancerConfig struct {
    MaxLoadFactor     float64 `json:"max_load_factor"`
    MaxLatencyMs      float64 `json:"max_latency_ms"`
    MaxErrorRate      float64 `json:"max_error_rate"`
    MinActiveWorkers  int     `json:"min_active_workers"`
    MaxPendingTasks   int     `json:"max_pending_tasks"`
    HealthCheckWindow int64   `json:"health_check_window"`
}

// NewRegionStateStore creates a new RegionStateStore instance
func NewRegionStateStore(sm interfaces.StateManager) *RegionStateStore {
    return &RegionStateStore{
        stateManager: sm,
    }
}


func (s *RegionStateStore) GetRegionConfig(regionID string) (*regions.RegionConfig, error) {
    ctx := context.Background()
    key := makeRegionKey("config", regionID, "")
    value, err := s.stateManager.GetValue(ctx, key)
    if err != nil {
        return nil, fmt.Errorf("failed to get region config: %w", err)
    }

    var config regions.RegionConfig
    if err := json.Unmarshal(value, &config); err != nil {
        return nil, fmt.Errorf("failed to unmarshal region config: %w", err)
    }

    return &config, nil
}


func (s *RegionStateStore) SetRegionConfig(regionID string, config *regions.RegionConfig) error {
    ctx := context.Background()
    data, err := json.Marshal(config)
    if err != nil {
        return fmt.Errorf("failed to marshal region config: %w", err)
    }

    key := makeRegionKey("config", regionID, "")
    if err := s.stateManager.Insert(ctx, key, data); err != nil {
        return fmt.Errorf("failed to store region config: %w", err)
    }

    return nil
}




// SaveRegion persists a region configuration
func (s *RegionStateStore) SaveRegion(ctx context.Context, region map[string]interface{}) error {
    data, err := json.Marshal(region)
    if err != nil {
        return fmt.Errorf("failed to marshal region config: %w", err)
    }

    key := makeRegionKey("config", region["id"].(string), "")
    if err := s.stateManager.Insert(ctx, key, data); err != nil {
        return fmt.Errorf("failed to store region config: %w", err)
    }

    return nil
}

// LoadRegion retrieves a region configuration
func (s *RegionStateStore) LoadRegion(ctx context.Context, id string) (map[string]interface{}, error) {
    key := makeRegionKey("config", id, "")
    data, err := s.stateManager.GetValue(ctx, key)
    if err != nil {
        return nil, err
    }

    var config map[string]interface{}
    if err := json.Unmarshal(data, &config); err != nil {
        return nil, fmt.Errorf("failed to unmarshal region config: %w", err)
    }

    return config, nil
}

// DeleteRegion removes a region configuration
func (s *RegionStateStore) DeleteRegionConfig(ctx context.Context, regionID string) error {
    key := makeRegionKey("config", regionID, "")
    return s.stateManager.Remove(ctx, key)
}

// GetRegionSettings loads region-specific settings
func (s *RegionStateStore) GetRegionSettings(ctx context.Context, regionID string) (*RegionSettings, error) {
    key := makeRegionKey("settings", regionID, "")
    data, err := s.stateManager.GetValue(ctx, key)
    if err != nil {
        return nil, err
    }

    var settings RegionSettings
    if err := json.Unmarshal(data, &settings); err != nil {
        return nil, fmt.Errorf("failed to unmarshal region settings: %w", err)
    }

    return &settings, nil
}

// SaveRegionSettings persists region-specific settings
func (s *RegionStateStore) SaveRegionSettings(ctx context.Context, regionID string, settings *RegionSettings) error {
    data, err := json.Marshal(settings)
    if err != nil {
        return fmt.Errorf("failed to marshal region settings: %w", err)
    }

    key := makeRegionKey("settings", regionID, "")
    return s.stateManager.Insert(ctx, key, data)
}

// ListRegions returns all region IDs
func (s *RegionStateStore) ListRegions(ctx context.Context) ([]string, error) {
    prefix := []byte("region/config/")
    regions := make([]string, 0)
    
    // If you have an Iterator method in your state manager:
    iter := s.stateManager.Iterator(ctx, prefix)
    defer iter.Close()
    
    for iter.Next() {
        key := iter.Key()
        if len(key) > len(prefix) {
            regionID := string(key[len(prefix):])
            regions = append(regions, regionID)
        }
    }

    if err := iter.Error(); err != nil {
        return nil, fmt.Errorf("iteration failed: %w", err)
    }

    return regions, nil
}


// incrementBytes is used for range scanning operations to get the next possible key.
// Currently unused but will be implemented when range queries are added.
// TODO: Implement range query support
func incrementBytes(b []byte) []byte {
    result := make([]byte, len(b))
    copy(result, b)
    for i := len(result) - 1; i >= 0; i-- {
        result[i]++
        if result[i] != 0 {
            break
        }
    }
    return result
}

func (s *RegionStateStore) GetRegionProof(ctx context.Context, regionID string) (*merkledb.Proof, error) {
    // Get the regional store
    store, err := s.stateManager.GetRegionalStore(regionID)
    if err != nil {
        return nil, fmt.Errorf("failed to get regional store: %w", err)
    }

    // Use the interface methods instead of accessing db directly
    key := makeRegionKey("config", regionID, "")
    proof, err := store.GetProof(ctx, key)
    if err != nil {
        return nil, fmt.Errorf("failed to get proof: %w", err)
    }

    return proof, nil
}


func (s *RegionStateStore) GetTEEState(ctx context.Context, regionID, pairID string) (*regions.TEEPairState, error) {
    key := makeRegionKey("state", regionID, pairID)
    value, err := s.stateManager.GetValue(ctx, key)
    if err != nil {
        return nil, fmt.Errorf("failed to get TEE state: %w", err)
    }

    var state regions.TEEPairState
    if err := json.Unmarshal(value, &state); err != nil {
        return nil, fmt.Errorf("failed to unmarshal TEE state: %w", err)
    }

    return &state, nil
}

func (s *RegionStateStore) SaveTEEState(ctx context.Context, regionID, pairID string, state *regions.TEEPairState) error {
    data, err := json.Marshal(state)
    if err != nil {
        return fmt.Errorf("failed to marshal TEE state: %w", err)
    }

    key := makeRegionKey("state", regionID, pairID)
    if err := s.stateManager.Insert(ctx, key, data); err != nil {
        return fmt.Errorf("failed to store TEE state: %w", err)
    }

    return nil
}

func (s *RegionStateStore) GetMetrics(ctx context.Context, regionID, pairID string) (*regions.TEEPairMetrics, error) {
    key := makeRegionKey("metrics", regionID, pairID)
    value, err := s.stateManager.GetValue(ctx, key)
    if err != nil {
        return nil, fmt.Errorf("failed to get metrics: %w", err)
    }

    var metrics regions.TEEPairMetrics
    if err := json.Unmarshal(value, &metrics); err != nil {
        return nil, fmt.Errorf("failed to unmarshal metrics: %w", err)
    }

    return &metrics, nil
}

func (s *RegionStateStore) SaveMetrics(ctx context.Context, regionID, pairID string, metrics *regions.TEEPairMetrics) error {
    data, err := json.Marshal(metrics)
    if err != nil {
        return fmt.Errorf("failed to marshal metrics: %w", err)
    }

    key := makeRegionKey("metrics", regionID, pairID)
    if err := s.stateManager.Insert(ctx, key, data); err != nil {
        return fmt.Errorf("failed to store metrics: %w", err)
    }

    return nil
}
