// storage/region_store.go
package storage

import (
    "context"
    "encoding/json"
    "fmt"

    "github.com/rhombus-tech/vm/regions"
)

type RegionStateStore struct {
    stateManager *StateManager
}

func NewRegionStateStore(sm *StateManager) *RegionStateStore {
    return &RegionStateStore{stateManager: sm}
}

func (r *RegionStateStore) SetRegionConfig(regionID string, config *regions.RegionConfig) error {
    ctx := context.Background()
    value, err := json.Marshal(config)
    if err != nil {
        return err
    }
    key := []byte(fmt.Sprintf("region/%s/config", regionID))
    return r.stateManager.Insert(ctx, key, value)
}

func (r *RegionStateStore) GetRegionConfig(regionID string) (*regions.RegionConfig, error) {
    ctx := context.Background()
    key := []byte(fmt.Sprintf("region/%s/config", regionID))
    value, err := r.stateManager.GetValue(ctx, key)
    if err != nil {
        return nil, err
    }
    var config regions.RegionConfig
    if err := json.Unmarshal(value, &config); err != nil {
        return nil, err
    }
    return &config, nil
}

func (r *RegionStateStore) DeleteRegionConfig(regionID string) error {
    ctx := context.Background()
    key := []byte(fmt.Sprintf("region/%s/config", regionID))
    return r.stateManager.Remove(ctx, key)
}

