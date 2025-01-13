package compute

import (
    "github.com/ava-labs/avalanchego/x/merkledb"
    "github.com/rhombus-tech/vm/core"
    "github.com/rhombus-tech/vm/regions"
)

type Config struct {
    MaxTasks       int
    TEEConfig      *core.Config
    RegionConfig   *regions.RegionConfig
    DB             merkledb.MerkleDB
    WasmPath       string
    ControllerPath string
    Debug          bool
    RegionID       string  // Add this field
}

func DefaultConfig() *Config {
    return &Config{
        MaxTasks: 100,
        TEEConfig: core.DefaultConfig(),
        RegionConfig: &regions.RegionConfig{
            MaxObjects: 1000,
            MaxEvents: 1000,
            LoadBalancer: regions.LoadBalancerConfig{
                MaxLoadFactor: 0.8,
                MaxLatencyMs: 1000,
                MaxErrorRate: 0.01,
                MinActiveWorkers: 2,
                MaxPendingTasks: 100,
                HealthCheckWindow: 300,
            },
        },
        WasmPath: "/usr/local/bin/tee-wasm-module.wasm",
        ControllerPath: "/usr/local/bin/tee-controller",
        Debug: false,
        RegionID: "",
    }
}