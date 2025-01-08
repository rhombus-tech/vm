// Copyright (C) 2024, Ava Labs, Inc. All rights reserved.
// See the file LICENSE for licensing terms.

package vm

import (
    "context"

    "github.com/ava-labs/hypersdk/chain"
    "github.com/rhombus-tech/vm/coordination"
)

// StateManager extends the chain.StateManager interface to add coordination capabilities
type StateManager interface {
    chain.StateManager

    // GetCoordinator returns the coordinator instance for managing regional execution
    GetCoordinator() *coordination.Coordinator

    // Region-related methods
    GetRegion(ctx context.Context, regionID string) (*RegionConfig, error)
    ListRegions(ctx context.Context) ([]RegionConfig, error)
    ValidateRegion(ctx context.Context, regionID string) error
}

// VM extends the chain.VM interface to add coordination capabilities
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
}