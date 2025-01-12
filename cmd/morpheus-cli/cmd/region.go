// Copyright (C) 2024, Ava Labs, Inc. All rights reserved.
// See the file LICENSE for licensing terms.

package cmd

import (
    "context"
    
    "github.com/spf13/cobra"
    "github.com/ava-labs/hypersdk/utils"
    "github.com/ava-labs/hypersdk/chain"
    "github.com/rhombus-tech/vm/actions"
)

var regionCmd = &cobra.Command{
    Use: "region",
    Short: "Manage regions",
    RunE: func(*cobra.Command, []string) error {
        return ErrMissingSubcommand
    },
}

var createRegionCmd = &cobra.Command{
    Use: "create [id]",
    Short: "Create a new region",
    Args: cobra.ExactArgs(1),
    RunE: createRegion,
}

var listRegionsCmd = &cobra.Command{
    Use: "list",
    Short: "List all regions",
    RunE: listRegions,
}

func init() {
    regionCmd.AddCommand(
        createRegionCmd,
        listRegionsCmd,
    )
    
    // Add regionCmd to root command
    rootCmd.AddCommand(regionCmd)
}

func createRegion(_ *cobra.Command, args []string) error {
    ctx := context.Background()
    _, _, factory, cli, bcli, ws, err := handler.DefaultActor()
    if err != nil {
        return err
    }

    // Get region ID from args
    regionID := args[0]

    // Generate and send transaction
    cont, txID, err := sendAndWait(
        ctx,
        []chain.Action{&actions.CreateRegionAction{
            RegionID: regionID,
        }},
        cli,
        bcli,
        ws,
        factory,
        true,
    )
    if err != nil {
        return err
    }

    if !cont {
        utils.Outf("{{red}}region creation failed:{{/}} %s\n", txID)
        return nil
    }

    utils.Outf("{{green}}region created:{{/}} %s\n", regionID)
    return nil
}

func listRegions(_ *cobra.Command, _ []string) error {
    ctx := context.Background()
    _, _, _, _, bcli, _, err := handler.DefaultActor()
    if err != nil {
        return err
    }

    // Get regions from VM
    resp, err := bcli.GetRegions(ctx)
    if err != nil {
        return err
    }

    if len(resp.Regions) == 0 {
        utils.Outf("{{yellow}}no regions found{{/}}\n")
        return nil
    }

    utils.Outf("{{cyan}}Regions:{{/}}\n")
    for _, region := range resp.Regions {
        utils.Outf("- ID: %s\n", region.Id)  // Note: field might be 'Id' not 'ID'
        utils.Outf("  Created: %s\n", region.CreatedAt)
        utils.Outf("  Worker Count: %d\n", len(region.WorkerIds))  // Note: field might be 'WorkerIds'
    }

    return nil
}

var addTEECmd = &cobra.Command{
    Use: "add-tee [region-id] [sgx-endpoint] [sev-endpoint]",
    Short: "Add TEE endpoints to a region",
    Args: cobra.ExactArgs(3),
    RunE: addTEE,
}

var attestCmd = &cobra.Command{
    Use: "attest [region-id]",
    Short: "Get attestations from region TEEs",
    Args: cobra.ExactArgs(1), 
    RunE: getAttestations,
}

func init() {
    createRegionCmd.Flags().String("name", "", "Region name")
    createRegionCmd.Flags().String("sgx", "", "SGX endpoint")  
    createRegionCmd.Flags().String("sev", "", "SEV endpoint")
    createRegionCmd.MarkFlagRequired("name")

    addTEECmd.Flags().String("sgx", "", "SGX endpoint")
    addTEECmd.Flags().String("sev", "", "SEV endpoint")
    addTEECmd.MarkFlagRequired("sgx")
    addTEECmd.MarkFlagRequired("sev")

    regionCmd.AddCommand(
        createRegionCmd,
        listRegionsCmd, 
        addTEECmd,
        attestCmd,
    )
}

func addTEE(_ *cobra.Command, args []string) error {
    ctx := context.Background()
    _, _, factory, cli, bcli, ws, err := handler.DefaultActor()
    if err != nil {
        return err
    }

    regionID := args[0]
    sgxEndpoint := args[1] 
    sevEndpoint := args[2]

    // Create update region action
    action := &actions.UpdateRegionAction{
        RegionID: regionID,
        SGXEndpoint: sgxEndpoint,
        SEVEndpoint: sevEndpoint,
    }

    // Send transaction
    cont, txID, err := sendAndWait(
        ctx,
        []chain.Action{action},
        cli,
        bcli,
        ws,
        factory,
        true,
    )
    if err != nil {
        return err
    }

    if !cont {
        utils.Outf("{{red}}TEE update failed:{{/}} %s\n", txID)
        return nil
    }

    utils.Outf("{{green}}TEE endpoints added:{{/}} %s\n", regionID)
    return nil
}

func getAttestations(_ *cobra.Command, args []string) error {
    ctx := context.Background()
    _, _, _, _, bcli, _, err := handler.DefaultActor()
    if err != nil {
        return err
    }

    regionID := args[0]
    attestations, err := bcli.GetRegionAttestations(ctx, regionID)
    if err != nil {
        return err
    }

    utils.Outf("{{cyan}}Region Attestations:{{/}}\n")
    
    if len(attestations) >= 2 {
        // First attestation is SGX
        utils.Outf("SGX Attestation:\n")
        utils.Outf("  EnclaveID: %x\n", attestations[0].EnclaveId)
        utils.Outf("  Measurement: %x\n", attestations[0].Measurement)
        utils.Outf("  Timestamp: %s\n", attestations[0].Timestamp)

        // Second attestation is SEV
        utils.Outf("\nSEV Attestation:\n")
        utils.Outf("  EnclaveID: %x\n", attestations[1].EnclaveId)
        utils.Outf("  Measurement: %x\n", attestations[1].Measurement)
        utils.Outf("  Timestamp: %s\n", attestations[1].Timestamp)
    } else {
        utils.Outf("{{yellow}}insufficient attestations found{{/}}\n")
    }
    
    return nil
}



