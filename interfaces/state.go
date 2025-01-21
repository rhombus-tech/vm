// interfaces/state.go
package interfaces

import (
	"context"

	"github.com/ava-labs/avalanchego/ids"
	"github.com/ava-labs/hypersdk/chain"
	"github.com/ava-labs/hypersdk/state"
	"github.com/rhombus-tech/vm/core"
)

// StateManager interface (not a pointer to interface)
type StateManager interface {
    chain.StateManager
    
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
    
    // Other operations
    GetKeysByPrefix(ctx context.Context, prefix []byte) ([][]byte, error)
    GetRegionalStore(regionID string) (*RegionalStore, error)
    IsRegionalKey(key []byte) (bool, string)
}

// RegionalStore interface
type RegionalStore interface {
    Get(ctx context.Context, key []byte) ([]byte, error)
    Put(ctx context.Context, key []byte, value []byte) error
    Delete(ctx context.Context, key []byte) error
    GetProof(ctx context.Context, key []byte) (*merkledb.Proof, error)
    GetRoot(ctx context.Context) (ids.ID, error)
}