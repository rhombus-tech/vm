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
   OutputParser = codec.NewTypeParser[codec.Typed]()
   
   errs := &wrappers.Errs{}
   errs.Add(
       // Register ShuttleVM actions
       ActionParser.Register(&actions.CreateObjectAction{}, nil),
       ActionParser.Register(&actions.SendEventAction{}, nil),
       ActionParser.Register(&actions.SetInputObjectAction{}, nil),
       ActionParser.Register(&actions.CreateRegionAction{}, nil),
       ActionParser.Register(&actions.UpdateRegionAction{}, nil),

       // Register auth methods
       AuthParser.Register(&auth.ED25519{}, auth.UnmarshalED25519),
       AuthParser.Register(&auth.SECP256R1{}, auth.UnmarshalSECP256R1),
       AuthParser.Register(&auth.BLS{}, auth.UnmarshalBLS),

       // Register output types (results from actions)
       OutputParser.Register(&actions.CreateObjectResult{}, nil),
       OutputParser.Register(&actions.SendEventResult{}, nil),
       OutputParser.Register(&actions.SetInputObjectResult{}, nil),
       OutputParser.Register(&actions.CreateRegionResult{}, nil),
       OutputParser.Register(&actions.UpdateRegionResult{}, nil),
   )
   if errs.Errored() {
       panic(errs.Err)
   }
}

type Config struct {
   InputObjectID string
}

// With returns the ShuttleVM-specific options
func With() vm.Option {
   return func(v *vm.VM) error {
       ctx := v.Context()
       
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
