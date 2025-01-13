// storage/region_store.go
package storage

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/rhombus-tech/vm/regions"
)

// RegionMetric defines a region metric
type RegionMetric struct {
    Name      string    `json:"name"`
    Value     float64   `json:"value"`
    Timestamp time.Time `json:"timestamp"`
}

type RegionStateStore struct {
    stateManager *StateManager // Change to pointer to fix lock by value
}

func NewRegionStateStore(sm *StateManager) *RegionStateStore {
    return &RegionStateStore{
        stateManager: sm,
    }
}

// createRegionKey creates a key for region storage
func (r *RegionStateStore) createRegionKey(regionID string, suffix string) []byte {
    return []byte(fmt.Sprintf("region/%s/%s", regionID, suffix))
}

// GetRegionConfig retrieves a region's configuration
func (r *RegionStateStore) GetRegionConfig(regionID string) (*regions.RegionConfig, error) {
    ctx := context.Background()
    if !isValidRegionID(regionID) {
        return nil, ErrInvalidRegionID
    }

    key := r.createRegionKey(regionID, "config")
    value, err := (*r.stateManager).GetValue(ctx, key)
    if err != nil {
        return nil, fmt.Errorf("failed to get region config: %w", err)
    }

    var config regions.RegionConfig
    if err := json.Unmarshal(value, &config); err != nil {
        return nil, fmt.Errorf("failed to unmarshal config: %w", err)
    }

    return &config, nil
}

// ListRegions returns all region IDs
func (r *RegionStateStore) ListRegions() ([]string, error) {
    ctx := context.Background()
    prefix := r.createRegionKey("", "")
    
    value, err := (*r.stateManager).GetValue(ctx, prefix)
    if err != nil {
        return nil, fmt.Errorf("failed to list regions: %w", err)
    }

    var regionList []string
    if err := json.Unmarshal(value, &regionList); err != nil {
        return nil, fmt.Errorf("failed to unmarshal region list: %w", err)
    }

    return regionList, nil
}

// SetRegionConfig stores a region configuration
func (r *RegionStateStore) SetRegionConfig(regionID string, config *regions.RegionConfig) error {
    ctx := context.Background()
    if !isValidRegionID(regionID) {
        return ErrInvalidRegionID
    }
    if config == nil {
        return fmt.Errorf("config cannot be nil") 
    }

    value, err := json.Marshal(config)
    if err != nil {
        return fmt.Errorf("failed to marshal config: %w", err)
    }

    key := r.createRegionKey(regionID, "config")
    if err := (*r.stateManager).Insert(ctx, key, value); err != nil {
        return fmt.Errorf("failed to store region config: %w", err)
    }
    return nil
}

// DeleteRegionConfig removes a region's configuration
func (r *RegionStateStore) DeleteRegionConfig(regionID string) error {
    ctx := context.Background()
    if !isValidRegionID(regionID) {
        return ErrInvalidRegionID
    }

    key := r.createRegionKey(regionID, "config")
    if err := (*r.stateManager).Remove(ctx, key); err != nil {
        return fmt.Errorf("failed to delete region config: %w", err)
    }
    return nil
}

// GetRegionStatus gets the current status of a region
func (r *RegionStateStore) GetRegionStatus(ctx context.Context, regionID string) (string, error) {
    if !isValidRegionID(regionID) {
        return "", ErrInvalidRegionID
    }

    key := r.createRegionKey(regionID, "status")
    value, err := (*r.stateManager).GetValue(ctx, key)
    if err != nil {
        return "", fmt.Errorf("failed to get region status: %w", err)
    }

    return string(value), nil
}

// SetRegionStatus updates a region's status
func (r *RegionStateStore) SetRegionStatus(ctx context.Context, regionID string, status string) error {
    if !isValidRegionID(regionID) {
        return ErrInvalidRegionID
    }

    key := r.createRegionKey(regionID, "status")
    if err := (*r.stateManager).Insert(ctx, key, []byte(status)); err != nil {
        return fmt.Errorf("failed to set region status: %w", err)
    }
    return nil
}


func isValidRegionID(id string) bool {
    if len(id) == 0 || len(id) > 64 {
        return false
    }
    return !strings.ContainsAny(id, "/\\?#[]{}") 
}