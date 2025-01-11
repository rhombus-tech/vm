package vm

import (
    "context"
    "github.com/ava-labs/hypersdk/chain"
    "github.com/ava-labs/hypersdk/state"
    "github.com/rhombus-tech/vm/coordination"
    "github.com/rhombus-tech/vm/core"
)

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

     SaveRegion(ctx context.Context, region *Region) error
     LoadRegion(ctx context.Context, id string) (*Region, error)
     DeleteRegion(ctx context.Context, id string) error

     GetObject(ctx context.Context, mu state.Mutable, id string, regionID string) (*core.ObjectState, error)
     SetObject(ctx context.Context, mu state.Mutable, id string, obj *core.ObjectState) error
     ObjectExists(ctx context.Context, mu state.Mutable, id string, regionID string) (bool, error)
 }
    
    // Input object operations
    SetInputObject(ctx context.Context, mu state.Mutable, id string) error

    // Coordination
    GetCoordinator() *coordination.Coordinator
}