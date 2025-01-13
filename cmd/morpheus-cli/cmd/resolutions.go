// Copyright (C) 2024, Ava Labs, Inc. All rights reserved.
// See the file LICENSE for licensing terms.

package cmd

import (
	"context"
	"fmt"
	"reflect"
	"time"

	"github.com/ava-labs/avalanchego/ids"
	"github.com/ava-labs/hypersdk/api/ws"
	"github.com/ava-labs/hypersdk/chain"
	"github.com/ava-labs/hypersdk/utils"
	"github.com/rhombus-tech/vm/actions"
	"github.com/rhombus-tech/vm/consts"
	"github.com/rhombus-tech/vm/core"
	hyperjsonrpc "github.com/ava-labs/hypersdk/api/jsonrpc"
    apirpc "github.com/rhombus-tech/vm/api/jsonrpc"
)

// sendAndWait may not be used concurrently
func sendAndWait(
    ctx context.Context,
    actions []chain.Action,
    standardClient *hyperjsonrpc.JSONRPCClient,
    apiClient *apirpc.JSONRPCClient,
    wsClient *ws.WebSocketClient,
    factory chain.AuthFactory,
    printStatus bool,
) (bool, ids.ID, error) {
    parser, err := apiClient.Parser(ctx)
    if err != nil {
        return false, ids.Empty, err
    }

    // Use standardClient for transaction generation
    _, tx, _, err := standardClient.GenerateTransaction(ctx, parser, actions, factory)
    if err != nil {
        return false, ids.Empty, err
    }

    if err := wsClient.RegisterTx(tx); err != nil {
        return false, ids.Empty, err
    }

    var result *chain.Result
    for {
        txID, txErr, txResult, err := wsClient.ListenTx(ctx)
        if err != nil {
            return false, ids.Empty, err
        }
        if txErr != nil {
            return false, ids.Empty, txErr
        }
        if txID == tx.ID() {
            result = txResult
            break
        }
        utils.Outf("{{yellow}}skipping unexpected transaction:{{/}} %s\n", tx.ID())
    }

    if printStatus {
        status := "❌"
        if result.Success {
            status = "✅"
        }
        utils.Outf("%s {{yellow}}txID:{{/}} %s\n", status, tx.ID())
    }

    return result.Success, tx.ID(), nil
}

func handleTx(tx *chain.Transaction, result *chain.Result) {
	actor := tx.Auth.Actor()
	if !result.Success {
		utils.Outf(
			"%s {{yellow}}%s{{/}} {{yellow}}actor:{{/}} %s {{yellow}}error:{{/}} [%s] {{yellow}}fee (max %.2f%%):{{/}} %s %s {{yellow}}consumed:{{/}} [%s]\n",
			"❌",
			tx.ID(),
			actor,
			result.Error,
			float64(result.Fee)/float64(tx.Base.MaxFee)*100,
			utils.FormatBalance(result.Fee),
			consts.Symbol,
			result.Units,
		)
		return
	}

	for _, action := range tx.Actions {
		var summaryStr string
		switch act := action.(type) {
		case *actions.Transfer:
			summaryStr = fmt.Sprintf("%s %s -> %s", utils.FormatBalance(act.Value), consts.Symbol, actor)
		case *actions.CreateRegionAction:
			summaryStr = fmt.Sprintf("Created region %s with %d TEEs", act.RegionID, len(act.TEEs))
		case *actions.UpdateRegionAction:
			summaryStr = fmt.Sprintf("Updated region %s attestations", act.RegionID)
		case *actions.SendEventAction:
			summaryStr = fmt.Sprintf("Event %s -> %s in region %s", act.FunctionCall, act.IDTo, act.RegionID)
		case *actions.CreateObjectAction:
			summaryStr = fmt.Sprintf("Created object %s in region %s", act.ID, act.RegionID)
		case *actions.SetInputObjectAction:
			summaryStr = fmt.Sprintf("Set input object %s in region %s", act.ID, act.RegionID)
		default:
			summaryStr = fmt.Sprintf("Unknown action type: %T", act)
		}

		utils.Outf(
			"%s {{yellow}}%s{{/}} {{yellow}}actor:{{/}} %s {{yellow}}summary (%s):{{/}} %s {{yellow}}fee (max %.2f%%):{{/}} %s %s {{yellow}}consumed:{{/}} [%s]\n",
			"✅",
			tx.ID(),
			actor,
			reflect.TypeOf(action),
			summaryStr,
			float64(result.Fee)/float64(tx.Base.MaxFee)*100,
			utils.FormatBalance(result.Fee),
			consts.Symbol,
			result.Units,
		)

		// Add detailed attestation info for relevant actions
		if teeAction, ok := action.(interface{ GetAttestations() [2]core.TEEAttestation }); ok {
			attestations := teeAction.GetAttestations()
			utils.Outf("{{cyan}}Attestations:{{/}}\n")
			
			for i, att := range attestations {
				teeType := "SGX"
				if i == 1 {
					teeType = "SEV"
				}
				utils.Outf("  %s:\n", teeType)
				utils.Outf("    EnclaveID: %x\n", att.EnclaveID)
				utils.Outf("    Measurement: %x\n", att.Measurement)
				utils.Outf("    Timestamp: %s\n", att.Timestamp.Format(time.RFC3339))
				utils.Outf("    Data Hash: %x\n", att.Data)
			}
		}
	}
}
