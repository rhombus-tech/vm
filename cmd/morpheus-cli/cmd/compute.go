package cmd

import (
    "github.com/spf13/cobra"
    "github.com/rhombus-tech/vm/compute"
)

var computeCmd = &cobra.Command{
    Use: "compute",
    Short: "Manage compute nodes",
    RunE: func(*cobra.Command, []string) error {
        return ErrMissingSubcommand
    },
}

var startComputeCmd = &cobra.Command{
    Use: "start [region-id] [port]",
    Short: "Start compute node",
    Args: cobra.ExactArgs(2),
    RunE: func(_ *cobra.Command, args []string) error {
        regionID := args[0]
        port := args[1]

        config := compute.DefaultConfig()
        config.RegionID = regionID
        config.ControllerPath = controllerPath
        config.WasmPath = wasmPath

        node, err := compute.NewComputeNode(regionID, config)
        if err != nil {
            return err
        }

        return node.Start(port)
    },
}

func init() {
    computeCmd.AddCommand(startComputeCmd)

    computeCmd.PersistentFlags().StringVar(
        &controllerPath,
        "controller",
        "/usr/local/bin/tee-controller",
        "TEE controller path",
    )
    computeCmd.PersistentFlags().StringVar(
        &wasmPath,
        "wasm",
        "/usr/local/bin/tee-wasm.wasm",
        "WASM module path",
    )
}