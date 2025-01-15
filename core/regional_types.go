// core/regional_types.go
package core

import (
    "time"
    "github.com/ava-labs/avalanchego/ids"
)

// RegionMetadata holds region-specific metadata
type RegionMetadata struct {
    ID            string    `json:"id"`
    CreatedAt     time.Time `json:"created_at"`
    LastUpdated   time.Time `json:"last_updated"`
    Root          ids.ID    `json:"root"`
    ObjectCount   uint64    `json:"object_count"`
    EventCount    uint64    `json:"event_count"`
}

// RegionalKey represents a key within a region's namespace
type RegionalKey struct {
    RegionID string
    Key      []byte
}

// RegionalBatch represents a batch of operations for a specific region
type RegionalBatch struct {
    RegionID string
    Updates  map[string][]byte
    Deletes  []string
}