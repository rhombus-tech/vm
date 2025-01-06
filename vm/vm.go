package vm

import (
    "fmt"
    "time"

    "github.com/ava-labs/hypersdk/auth"
    "github.com/ava-labs/hypersdk/chain"
    "github.com/ava-labs/hypersdk/codec"
    "github.com/ava-labs/hypersdk/genesis"
    "github.com/ava-labs/hypersdk/vm"
    "github.com/ava-labs/hypersdk/vm/defaultvm"
    "github.com/ava-labs/avalanchego/utils/wrappers"

    "github.com/rhombus-tech/vm/actions" // has ParseCreateObject, ParseSendEvent, etc.
    "github.com/rhombus-tech/vm/coordination"
    "github.com/rhombus-tech/vm/storage"
    "github.com/rhombus-tech/vm/verifier"
    "github.com/rhombus-tech/vm/consts"
)

var (
    ActionParser *codec.TypeParser[chain.Action]
    AuthParser   *codec.TypeParser[chain.Auth]
    OutputParser *codec.TypeParser[codec.Typed]
)

func init() {
    ActionParser = codec.NewTypeParser[chain.Action]()
    AuthParser = codec.NewTypeParser[chain.Auth]()
    OutputParser = codec.NewTypeParser[codec.Typed]()

    errs := &wrappers.Errs{}
    errs.Add(
        ActionParser.Register(&actions.CreateObjectAction{}, actions.ParseCreateObject),
        ActionParser.Register(&actions.SendEventAction{}, actions.ParseSendEvent),
        ActionParser.Register(&actions.SetInputObjectAction{}, actions.ParseSetInputObject),
        // etc. If you have more actions, register them here.

        // If you have output types, do:
        // OutputParser.Register(&actions.CreateObjectResult{}, actions.ParseCreateObjectResult),
        // ...
    )
    if errs.Errored() {
        panic(errs.Err)
    }
}

type Config struct {
    WorkerTimeout  int
    ChannelTimeout int
    // ... other fields
}

// With returns a vm.Option
func With() vm.Option {
    return WithConfig(&Config{
        WorkerTimeout:  30,
        ChannelTimeout: 10,
        // ...
    })
}

func WithConfig(cfg *Config) vm.Option {
    return func(base *vm.VM) error {
        // Convert WorkerTimeout, ChannelTimeout to time.Duration
        workerTimeout := time.Duration(cfg.WorkerTimeout) * time.Second
        channelTimeout := time.Duration(cfg.ChannelTimeout) * time.Second

        // Create coordinator
        coordConfig := &coordination.Config{
            WorkerTimeout:  workerTimeout,
            ChannelTimeout: channelTimeout,
            // ...
        }
        coordinator, err := coordination.NewCoordinator(coordConfig, base.DB)
        if err != nil {
            return fmt.Errorf("failed to create coordinator: %w", err)
        }
        if err := coordinator.Start(); err != nil {
            return fmt.Errorf("failed to start coordinator: %w", err)
        }

        // Make a custom manager that implements chain.StateManager or embed
        sm := storage.NewStateManager(coordinator) // you'd define constructor
        // Create a batch verifier if you want
        bv := verifier.NewBatchVerifier(base.State, coordinator)
        // Then do something with them if you want to store them globally.

        return nil
    }
}

// New creates a new default VM
func New(options ...vm.Option) (*vm.VM, error) {
    // Provide a chain.StateManager that *does* implement all needed methods:
    chainStateManager := &storage.ExampleStateManager{} // must implement chain.StateManager

    // Attach default VM with your ActionParser, AuthParser, OutputParser
    return defaultvm.New(
        consts.Version,
        genesis.DefaultGenesisFactory{},
        chainStateManager,
        ActionParser,
        AuthParser,
        OutputParser,
        auth.Engines(),
        options...,
    )
}
