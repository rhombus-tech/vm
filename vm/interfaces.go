// vm/interfaces.go
package vm

import (
	"context"

	"github.com/ava-labs/hypersdk/chain"
	"github.com/ava-labs/hypersdk/state"
	"github.com/rhombus-tech/vm/coordination"
	"github.com/rhombus-tech/vm/core"
	"github.com/rhombus-tech/vm/regions"
)

//go:generate go run github.com/golang/mock/mockgen@v1.6.0 -destination=mocks/mock_state_manager.go -package=mocks -source=$GOFILE
//go:generate go run github.com/golang/mock/mockgen@v1.6.0 -destination=mocks/mock_vm.go -package=mocks -source=$GOFILE

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
    
    // Input object operations
    SetInputObject(ctx context.Context, mu state.Mutable, id string) error

    // Coordination
    GetCoordinator() *coordination.Coordinator

    GetKeysByPrefix(ctx context.Context, prefix []byte) ([][]byte, error)
}

type VM interface {
    chain.VM

    // Coordinator returns the VM's coordinator instance
    Coordinator() *coordination.Coordinator

    // Region management
    RegisterRegion(ctx context.Context, config RegionConfig) error
    GetRegionClient(regionID string) (*RegionClient, error)
    RemoveRegion(ctx context.Context, regionID string) error
    
    // Region queries
    GetRegionConfig(regionID string) (RegionConfig, error)
    ListRegionConfigs() []RegionConfig
    
    // Regional execution
    ExecuteInRegion(ctx context.Context, regionID string, action chain.Action) error
    ValidateRegionalAction(ctx context.Context, regionID string, action chain.Action) error

    GetRegionHealth(ctx context.Context, regionID string) (*regions.HealthStatus, error)
    GetRegionMetrics(ctx context.Context, regionID string, pairID string) (*regions.TEEPairMetrics, error)
}

