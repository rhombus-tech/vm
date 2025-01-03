// vm/option.go
package vm

import (
    "github.com/ava-labs/hypersdk/vm"
    "github.com/ava-labs/hypersdk/api"
)

const Namespace = "morpheusvm"

type Config struct {
    Enabled bool `json:"enabled"`
    // Add other configuration options
    MaxObjectSize uint64 `json:"maxObjectSize"`
    MaxStorageSize uint64 `json:"maxStorageSize"`
    EnableTEEFeatures bool `json:"enableTEEFeatures"`
    TEEEndpoint string `json:"teeEndpoint"` // Add TEE endpoint
}

func NewDefaultConfig() Config {
    return Config{
        Enabled: true,
        MaxObjectSize: 1024 * 1024, // 1MB
        MaxStorageSize: 1024 * 1024, // 1MB
        EnableTEEFeatures: true,
        TEEEndpoint: "localhost:50051", // Default TEE endpoint
    }
}

// WithCustomOptions allows configuring specific VM features
func WithCustomOptions(config Config) vm.Option {
    return vm.NewOption(
        Namespace,
        config,
        func(v *vm.VM, cfg Config) (vm.Opt, error) {
            if !cfg.Enabled {
                return nil, nil
            }

            // Configure VM with options
            opts := []vm.Opt{
                vm.WithVMAPIs(NewJSONRPCServer(v)),
            }

            // Add TEE features if enabled
            if cfg.EnableTEEFeatures {
                // Create TEE state manager
                sm, err := NewTEEStateManager(v.State, cfg.TEEEndpoint)
                if err != nil {
                    return nil, err
                }

                opts = append(opts,
                    vm.WithState(sm),
                    vm.WithBlockSubscriptions(NewTEEVerifier()),
                    vm.WithTxRemovedSubscriptions(NewTEECleanup()),
                )
            }

            return vm.NewOpt(opts...), nil
        },
    )
}

// Default With() function using default config
func With() vm.Option {
    return WithCustomOptions(NewDefaultConfig())
}