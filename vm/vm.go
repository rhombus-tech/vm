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

   "github.com/rhombus-tech/vm/actions"
   "github.com/rhombus-tech/vm/consts"
   "github.com/rhombus-tech/vm/storage"
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
}

// With returns the ShuttleVM-specific options with TEE support
func With() vm.Option {
   return func(v *vm.VM) error {
       ctx := v.Context()

       // Verify Roughtime server availability
       if _, err := roughtime.Now(); err != nil {
           return fmt.Errorf("failed to initialize Roughtime: %w", err)
       }
       
       // Set default input object
       if err := storage.SetInputObject(ctx, v.State, "input"); err != nil {
           return fmt.Errorf("failed to set input object: %w", err)
       }

       return nil
   }
}

// WithConfig returns ShuttleVM options with custom configuration
func WithConfig(config Config) vm.Option {
   return func(v *vm.VM) error {
       ctx := v.Context()

       // Verify Roughtime server availability
       if _, err := roughtime.Now(); err != nil {
           return fmt.Errorf("failed to initialize Roughtime: %w", err)
       }

       // Set custom input object
       if err := storage.SetInputObject(ctx, v.State, config.InputObjectID); err != nil {
           return fmt.Errorf("failed to set input object: %w", err)
       }

       return nil
   }
}

// NewWithOptions returns a VM with the specified options
func New(options ...vm.Option) (*vm.VM, error) {
   options = append(options, With()) // Add ShuttleVM API
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
func NewWithConfig(config Config, options ...vm.Option) (*vm.VM, error) {
   options = append(options, WithConfig(config)) // Add configured ShuttleVM API
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
