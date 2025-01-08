// Copyright (C) 2024, Ava Labs, Inc. All rights reserved.
// See the file LICENSE for licensing terms.

package vm

import (
    "context"
    "fmt"

    "github.com/ava-labs/avalanchego/ids"
    "github.com/ava-labs/avalanchego/utils/logging"
    "github.com/ava-labs/hypersdk/state"
    "go.uber.org/zap"
)

// ShuttleVM implements the VM interface
type ShuttleVM struct {
    chainID      ids.ID
    stateManager state.Mutable
    regionClients map[string]*RegionClient
    config       *Config
    logger       logging.Logger
}

func New(ctx context.Context, config *Config, logger logging.Logger) (*ShuttleVM, error) {
    regionClients := make(map[string]*RegionClient)
    for _, rc := range config.Regions {
        client, err := NewRegionClient(rc)
        if err != nil {
            // Clean up any already created clients
            for _, c := range regionClients {
                c.Close()
            }
            return nil, fmt.Errorf("failed to create region client for %s: %w", rc.ID, err)
        }
        regionClients[rc.ID] = client
    }

    vm := &ShuttleVM{
        config:        config,
        regionClients: regionClients,
        logger:       logger,
    }

    return vm, nil
}

func (vm *ShuttleVM) Initialize(
    ctx context.Context,
    chainID ids.ID,
    stateManager state.Mutable,
) error {
    vm.chainID = chainID
    vm.stateManager = stateManager
    return nil
}

func (vm *ShuttleVM) Shutdown(ctx context.Context) error {
    for _, client := range vm.regionClients {
        if err := client.Close(); err != nil {
            vm.logger.Error(
                "failed to close region client",
                zap.Error(err),
            )
        }
    }
    return nil
}

func (vm *ShuttleVM) GetRegionClient(regionID string) (*RegionClient, error) {
    client, exists := vm.regionClients[regionID]
    if !exists {
        return nil, fmt.Errorf("no client found for region %s", regionID)
    }
    return client, nil
}
