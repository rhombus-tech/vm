// Copyright (C) 2024, Ava Labs, Inc. All rights reserved.
// See the file LICENSE for licensing terms.
package vm

import (
   "fmt"

   "github.com/ava-labs/avalanchego/utils/wrappers"
   "github.com/ava-labs/hypersdk/auth"
   "github.com/ava-labs/hypersdk/chain"
   "github.com/ava-labs/hypersdk/codec"
   "github.com/ava-labs/hypersdk/genesis"
   "github.com/ava-labs/hypersdk/vm"
   "github.com/ava-labs/hypersdk/vm/defaultvm"
   "github.com/ava-labs/hypersdk/coordination"

   "github.com/rhombus-tech/vm/actions"
   "github.com/rhombus-tech/vm/consts"
   "github.com/rhombus-tech/vm/storage"
   "github.com/rhombus-tech/vm/verifier"
   "github.com/cloudflare/roughtime"
)

var (
   ActionParser *codec.TypeParser[chain.Action]
   AuthParser   *codec.TypeParser[chain.Auth]
   OutputParser *codec.TypeParser[codec.Typed]
)

// Setup types
func init() {
   ActionParser = codec.NewTypeParser[chain.Action]()
   AuthParser = codec.NewTypeParser[chain.Auth]()
   OutputParser = codec.TypeParser[codec.Typed]()
   
   errs := &wrappers.Errs{}
   errs.Add(
       // Register ShuttleVM actions with TEE attestations
       ActionParser.Register(&actions.CreateObjectAction{}, actions.UnmarshalCreateObject),
       ActionParser.Register(&actions.SendEventAction{}, actions.UnmarshalSendEvent),
       ActionParser.Register(&actions.SetInputObjectAction{}, actions.UnmarshalSetInputObject),
       ActionParser.Register(&actions.CreateRegionAction{}, actions.UnmarshalCreateRegion),
       ActionParser.Register(&actions.UpdateRegionAction{}, actions.UnmarshalUpdateRegion),
       ActionParser.Register(&actions.TEEAttestation{}, actions.UnmarshalTEEAttestation),

       // Register auth methods for transaction signatures
       AuthParser.Register(&auth.ED25519{}, auth.UnmarshalED25519),
       AuthParser.Register(&auth.SECP256R1{}, auth.UnmarshalSECP256R1),
       AuthParser.Register(&auth.BLS{}, auth.UnmarshalBLS),

       // Register output types with TEE attestation results
       OutputParser.Register(&actions.CreateObjectResult{}, actions.UnmarshalCreateObjectResult),
       OutputParser.Register(&actions.SendEventResult{}, actions.UnmarshalSendEventResult),
       OutputParser.Register(&actions.SetInputObjectResult{}, actions.UnmarshalSetInputObjectResult),
       OutputParser.Register(&actions.CreateRegionResult{}, actions.UnmarshalCreateRegionResult),
       OutputParser.Register(&actions.UpdateRegionResult{}, actions.UnmarshalUpdateRegionResult),
   )
   if errs.Errored() {
       panic(errs.Err)
   }
}

type Config struct {
   InputObjectID string
   
   // Coordination settings
   MinWorkers          int
   MaxWorkers          int
   WorkerTimeout       int64
   ChannelTimeout      int64
   MaxMessageSize      int
   RequireAttestation  bool
   StoragePath         string
}

func DefaultConfig() *Config {
    return &Config{
        InputObjectID:      "input",
        MinWorkers:         2,
        MaxWorkers:         10,
        WorkerTimeout:      30, // seconds
        ChannelTimeout:     10, // seconds
        MaxMessageSize:     1024 * 1024, // 1MB
        RequireAttestation: true,
        StoragePath:        "/tmp/coordinator",
    }
}

// With returns the ShuttleVM-specific options with TEE support
func With() vm.Option {
   return WithConfig(DefaultConfig())
}

// WithConfig returns ShuttleVM options with custom configuration
func WithConfig(config *Config) vm.Option {
   return func(v *vm.VM) error {
       ctx := v.Context()

       // Verify Roughtime server availability
       if _, err := roughtime.Now(); err != nil {
           return fmt.Errorf("failed to initialize Roughtime: %w", err)
       }

       // Initialize coordinator
       coordConfig := &coordination.Config{
           MinWorkers:         config.MinWorkers,
           MaxWorkers:         config.MaxWorkers,
           WorkerTimeout:      config.WorkerTimeout,
           ChannelTimeout:     config.ChannelTimeout,
           MaxMessageSize:     config.MaxMessageSize,
           RequireAttestation: config.RequireAttestation,
           StoragePath:        config.StoragePath,
       }

       coordinator, err := coordination.NewCoordinator(coordConfig, v.DB)
       if err != nil {
           return fmt.Errorf("failed to create coordinator: %w", err)
       }

       // Start coordinator
       if err := coordinator.Start(); err != nil {
           return fmt.Errorf("failed to start coordinator: %w", err)
       }

       // Create state manager with coordinator
       stateManager := &storage.StateManager{
           Coordinator: coordinator,
       }

       // Create batch verifier with coordinator
       batchVerifier := verifier.NewBatchVerifier(v.State, coordinator)
       
       // Set custom input object
       if err := storage.SetInputObject(ctx, v.State, config.InputObjectID); err != nil {
           return fmt.Errorf("failed to set input object: %w", err)
       }

       // Store components in VM
       v.State = stateManager
       v.SetVerifier(batchVerifier)

       return nil
   }
}

func (v *VM) Coordinator() *coordination.Coordinator {
    if stateManager, ok := v.State().(*storage.StateManager); ok {
        return stateManager.GetCoordinator()
    }
    return nil
}

// New creates a new VM with default configuration
func New(options ...vm.Option) (*vm.VM, error) {
    // Add TEE options
    options = append(options, 
        With(),
        vm.WithBuilder(),
        vm.WithGossiper(),
    )
    
    return defaultvm.New(
        consts.Version,
        genesis.DefaultGenesisFactory{},
        &storage.StateManager{},
        ActionParser,
        AuthParser,
        OutputParser,
        auth.Engines(),
        options...,
    )
}

// NewWithConfig creates a new VM with custom configuration
func NewWithConfig(config *Config, options ...vm.Option) (*vm.VM, error) {
   options = append(options, WithConfig(config))
   
   return defaultvm.New(
       consts.Version,
       genesis.DefaultGenesisFactory{},
       &storage.StateManager{},
       ActionParser,
       AuthParser,
       OutputParser,
       auth.Engines(),
       options...,
   )
}

// Cleanup handles VM shutdown
func (v *vm.VM) Cleanup() error {
    // Stop coordinator
    if coord := v.State.(*storage.StateManager).Coordinator; coord != nil {
        if err := coord.Stop(); err != nil {
            return fmt.Errorf("failed to stop coordinator: %w", err)
        }
    }
    return nil
}