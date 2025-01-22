// interfaces/state_manager.go
package interfaces

import (
	"context"

	"github.com/ava-labs/hypersdk/chain"
	"github.com/ava-labs/hypersdk/state"
	"github.com/rhombus-tech/vm/coordination"
	"github.com/rhombus-tech/vm/core"
)

type StateManager interface {
    chain.StateManager
    state.Mutable
    
    GetValue(ctx context.Context, key []byte) ([]byte, error)
    
    // Object operations
    GetObject(ctx context.Context, mu state.Mutable, id string, regionID string) (*core.ObjectState, error)
    SetObject(ctx context.Context, mu state.Mutable, id string, obj *core.ObjectState) error
    ObjectExists(ctx context.Context, mu state.Mutable, id string, regionID string) (bool, error)
    
    // Event operations
    SetEvent(ctx context.Context, mu state.Mutable, id string, event *core.Event, regionID string) error

    // Region operations
    GetRegion(ctx context.Context, mu state.Mutable, id string) (map[string]interface{}, error)
    SetRegion(ctx context.Context, mu state.Mutable, id string, region map[string]interface{}) error
    RegionExists(ctx context.Context, mu state.Mutable, id string) (bool, error)

    SaveRegion(ctx context.Context, region *Region) error
    LoadRegion(ctx context.Context, id string) (*Region, error)
    DeleteRegion(ctx context.Context, id string) error
    
    // Input object operations
    SetInputObject(ctx context.Context, mu state.Mutable, id string) error

    // Other operations
    GetKeysByPrefix(ctx context.Context, prefix []byte) ([][]byte, error)
    Iterator(ctx context.Context, prefix []byte) Iterator

    GetRegionalStore(regionID string) (core.RegionalStore, error)
    IsRegionalKey(key []byte) (bool, string)

    GetBaseStorage() coordination.BaseStorage
}