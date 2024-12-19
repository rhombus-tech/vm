// Copyright (C) 2024, Ava Labs, Inc. All rights reserved.
// See the file LICENSE for licensing terms.
package vm

import (
    "github.com/ava-labs/avalanchego/utils/wrappers"
    "github.com/ava-labs/hypersdk/auth"
    "github.com/ava-labs/hypersdk/chain"
    "github.com/ava-labs/hypersdk/codec"
    "github.com/ava-labs/hypersdk/genesis"
    "github.com/ava-labs/hypersdk/vm"
    "github.com/ava-labs/hypersdk/vm/defaultvm"

    "github.com/yourusername/shuttle/actions"
    "github.com/yourusername/shuttle/consts"
    "github.com/yourusername/shuttle/storage"
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
        ActionParser.Register(&actions.ChangeObjectCodeAction{}, nil),

        // Register auth methods
        AuthParser.Register(&auth.ED25519{}, auth.UnmarshalED25519),
        AuthParser.Register(&auth.SECP256R1{}, auth.UnmarshalSECP256R1),
        AuthParser.Register(&auth.BLS{}, auth.UnmarshalBLS),

        // Register output types (results from actions)
        OutputParser.Register(&actions.CreateObjectResult{}, nil),
        OutputParser.Register(&actions.SendEventResult{}, nil),
    )
    if errs.Errored() {
        panic(errs.Err)
    }
}

// With returns the ShuttleVM-specific options
func With() vm.Option {
    return func(v *vm.VM) error {
        // Add any ShuttleVM-specific initialization here
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
