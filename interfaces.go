package vm

import (
    "context"
    "github.com/ava-labs/hypersdk/chain"
    "github.com/rhombus-tech/hypersdk/coordination"
    "github.com/ava-labs/hypersdk/state"
    "github.com/rhombus-tech/vm/types"
)

type StateManager interface {
    chain.StateManager
    
    // Object operations
    GetObject(ctx context.Context, mu state.Mutable, id string) (*types.ObjectState, error)
    SetObject(ctx context.Context, mu state.Mutable, id string, obj *types.ObjectState) error
    ObjectExists(ctx context.Context, mu state.Mutable, id string) (bool, error)
    
    // Event operations
    SetEvent(ctx context.Context, mu state.Mutable, id string, event *types.Event) error
    
    // Region operations
    GetRegion(ctx context.Context, mu state.Mutable, id string) (map[string]interface{}, error)
    SetRegion(ctx context.Context, mu state.Mutable, id string, region map[string]interface{}) error
    RegionExists(ctx context.Context, mu state.Mutable, id string) (bool, error)
    
    // Input object operations
    SetInputObject(ctx context.Context, mu state.Mutable, id string) error

    // Coordination
    GetCoordinator() *coordination.Coordinator
}