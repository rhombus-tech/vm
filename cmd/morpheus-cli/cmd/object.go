package cmd

import (
	"context"
	"fmt"
	"io/ioutil"
	"time"

	"github.com/ava-labs/hypersdk/chain"
	"github.com/ava-labs/hypersdk/utils"
	"github.com/rhombus-tech/vm/actions"
	"github.com/rhombus-tech/vm/core"
	"github.com/spf13/cobra"
)

var (
    objectCmd = &cobra.Command{
        Use:   "object",
        Short: "Manage objects",
        RunE: func(*cobra.Command, []string) error {
            return ErrMissingSubcommand
        },
    }

    // Flag variables
    codeFile       string
    initialStorage []byte
    functionCall   string
    parameters     []byte
)

var createObjectCmd = &cobra.Command{
    Use:   "create [id] [region-id]",
    Short: "Create new object",
    Args:  cobra.ExactArgs(2),
    RunE:  createObject,
}

var sendEventCmd = &cobra.Command{
    Use:   "send-event [object-id] [region-id]",
    Short: "Send event to object",
    Args:  cobra.ExactArgs(2),
    RunE:  sendEvent,
}

var setInputCmd = &cobra.Command{
    Use:   "set-input [object-id] [region-id]",
    Short: "Set input object",
    Args:  cobra.ExactArgs(2),
    RunE:  setInputObject,
}

func createObject(_ *cobra.Command, args []string) error {
    ctx := context.Background()
    _, authFactory, cli, vmClient, wsClient, err := handler.DefaultActor()
    if err != nil {
        return err
    }

    objectID := args[0]
    regionID := args[1]

    // Read code file
    code, err := ioutil.ReadFile(codeFile)
    if err != nil {
        return fmt.Errorf("failed to read code file: %w", err)
    }

    action := &actions.CreateObjectAction{
        ID:       objectID,
        Code:     code,
        Storage:  initialStorage,
        RegionID: regionID,
    }

    cont, txID, err := sendAndWait(
        ctx,
        []chain.Action{action},
        cli,
        vmClient,
        wsClient,
        authFactory,
        true,
    )
    if err != nil {
        return err
    }

    if !cont {
        utils.Outf("{{red}}object creation failed:{{/}} %s\n", txID)
        return nil
    }

    utils.Outf("{{green}}object created:{{/}} %s\n", objectID)
    return nil
}

func sendEvent(_ *cobra.Command, args []string) error {
    ctx := context.Background()
    _, authFactory, cli, vmClient, wsClient, err := handler.DefaultActor()
    if err != nil {
        return err
    }

    objectID := args[0]
    regionID := args[1]

    // Get attestations for the function call
    attestations, err := vmClient.GetRegionAttestations(ctx, regionID)
    if err != nil {
        return fmt.Errorf("failed to get attestations: %w", err)
    }

    // Convert attestations
    var teeAttestations [2]core.TEEAttestation
    for i, att := range attestations[:2] {
        timestamp, err := time.Parse(time.RFC3339, att.Timestamp)
        if err != nil {
            return fmt.Errorf("failed to parse attestation timestamp: %w", err)
        }

        teeAttestations[i] = core.TEEAttestation{
            EnclaveID:   att.EnclaveId,
            Measurement: att.Measurement,
            Timestamp:   timestamp,
            Data:        att.Data,
            Signature:   att.Signature,
            RegionProof: att.RegionProof,
        }
    }

    action := &actions.SendEventAction{
        IDTo:         objectID,
        FunctionCall: functionCall,
        Parameters:   parameters,
        Attestations: teeAttestations,
        RegionID:     regionID,
    }

    cont, txID, err := sendAndWait(
        ctx,
        []chain.Action{action},
        cli,
        vmClient,
        wsClient,
        authFactory,
        true,
    )
    if err != nil {
        return err
    }

    if !cont {
        utils.Outf("{{red}}event sending failed:{{/}} %s\n", txID)
        return nil
    }

    utils.Outf("{{green}}event sent:{{/}} %s\n", txID)
    return nil
}

func setInputObject(_ *cobra.Command, args []string) error {
    ctx := context.Background()
    _, authFactory, cli, vmClient, wsClient, err := handler.DefaultActor()
    if err != nil {
        return err
    }

    objectID := args[0]
    regionID := args[1]

    action := &actions.SetInputObjectAction{
        ID:       objectID,
        RegionID: regionID,
    }

    cont, txID, err := sendAndWait(
        ctx,
        []chain.Action{action},
        cli,
        vmClient,
        wsClient,
        authFactory,
        true,
    )
    if err != nil {
        return err
    }

    if !cont {
        utils.Outf("{{red}}failed to set input object:{{/}} %s\n", txID)
        return nil
    }

    utils.Outf("{{green}}input object set:{{/}} %s\n", objectID)
    return nil
}

func init() {
    // Add subcommands to object command
    objectCmd.AddCommand(
        createObjectCmd,
        sendEventCmd,
        setInputCmd,
    )

    // Create object flags
    createObjectCmd.Flags().StringVar(
        &codeFile,
        "code",
        "",
        "Path to code file",
    )
    createObjectCmd.Flags().BytesHexVar(
        &initialStorage,
        "storage",
        nil,
        "Initial storage (hex)",
    )
    createObjectCmd.MarkFlagRequired("code")

    // Send event flags
    sendEventCmd.Flags().StringVar(
        &functionCall,
        "function",
        "",
        "Function to call",
    )
    sendEventCmd.Flags().BytesHexVar(
        &parameters,
        "params",
        nil,
        "Function parameters (hex)",
    )
    sendEventCmd.MarkFlagRequired("function")

    // Add object command to root
    rootCmd.AddCommand(objectCmd)
}
