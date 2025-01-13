// Copyright (C) 2024, Ava Labs, Inc. All rights reserved.
// See the file LICENSE for licensing terms.

package cmd

import (
    "context"

    "github.com/spf13/cobra"

    "github.com/rhombus-tech/vm/actions"
    "github.com/ava-labs/hypersdk/chain"
    "github.com/ava-labs/hypersdk/cli/prompt"
)

var actionCmd = &cobra.Command{
    Use: "action",
    RunE: func(*cobra.Command, []string) error {
        return ErrMissingSubcommand
    },
}

var transferCmd = &cobra.Command{
    Use: "transfer",
    RunE: func(*cobra.Command, []string) error {
        ctx := context.Background()
        
        // Match the 6 return values from DefaultActor
        priv, factory, standardClient, apiClient, wsClient, err := handler.DefaultActor()
        if err != nil {
            return err
        }

        // Use apiClient for balance
        balance, err := handler.GetBalance(ctx, priv.Address, apiClient)
        if balance == 0 || err != nil {
            return err
        }

        recipient, err := prompt.Address("recipient")
        if err != nil {
            return err
        }

        amount, err := prompt.Amount("amount", balance, nil)
        if err != nil {
            return err
        }

        cont, err := prompt.Continue()
        if !cont || err != nil {
            return err
        }

        // Match the sendAndWait parameter order
        _, _, err = sendAndWait(
            ctx,
            []chain.Action{&actions.Transfer{
                To:    recipient,
                Value: amount,
            }},
            standardClient,
            apiClient,
            wsClient,
            factory,
            true,
        )
        return err
    },
}