// storage/region_state_store.go
package storage

import (
    "context"
    "encoding/json"
    "fmt"
)

// RegionStateStore provides storage operations for region state
type RegionStateStore struct {
    stateManager *StateManager
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
func NewRegionStateStore(sm *StateManager) *RegionStateStore {
    return &RegionStateStore{
        stateManager: sm,
    }
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
func (s *RegionStateStore) DeleteRegion(ctx context.Context, id string) error {
    key := makeRegionKey("config", id, "")
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

type Iterator interface {
    Next() bool
    Key() []byte
    Value() []byte
    Error() error
    Close()
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


// Helper function to increment byte slice for iteration
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