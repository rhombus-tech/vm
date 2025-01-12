// Copyright (C) 2024, Ava Labs, Inc. All rights reserved.
// See the file LICENSE for licensing terms.

package main

import (
	"context"
	"fmt"
	"os"

	"github.com/ava-labs/avalanchego/utils/logging"
	"github.com/ava-labs/avalanchego/utils/ulimit"
	"github.com/ava-labs/avalanchego/vms/rpcchainvm"
	"github.com/spf13/cobra"

	"github.com/rhombus-tech/vm/cmd/morpheusvm/version"
	"github.com/rhombus-tech/vm/compute"
	"github.com/rhombus-tech/vm/vm"
)

var rootCmd = &cobra.Command{
	Use:        "morpheusvm",
	Short:      "BaseVM agent",
	SuggestFor: []string{"morpheusvm"},
	RunE:       runFunc,
}

func init() {
	cobra.EnablePrefixMatching = true
}

func init() {
	rootCmd.AddCommand(
		version.NewCommand(),
	)
}

func main() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintf(os.Stderr, "morpheusvm failed %v\n", err)
		os.Exit(1)
	}
	os.Exit(0)
}

func runFunc(*cobra.Command, []string) error {
    if err := ulimit.Set(ulimit.DefaultFDLimit, logging.NoLog{}); err != nil {
        return fmt.Errorf("%w: failed to set fd limit", err)
    }

    vmConfig := &vm.Config{
        NetworkID: 0,
        Regions: []vm.RegionConfig{},
    }
    
    // Add TEE configuration from env
    if teeEndpoint := os.Getenv("TEE_ENDPOINT"); teeEndpoint != "" {
        vmConfig.ComputeNodeEndpoints = map[string]compute.NodeClientConfig{
            "default": {
                Endpoint: teeEndpoint,
                ControllerPath: os.Getenv("TEE_CONTROLLER_PATH"),
                WasmPath: os.Getenv("TEE_WASM_PATH"),
            },
        }
    }

    vm, err := vm.New(vmConfig)
    if err != nil {
        return err
    }
    return rpcchainvm.Serve(context.TODO(), vm)
}
