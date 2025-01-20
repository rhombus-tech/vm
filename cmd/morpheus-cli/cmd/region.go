// cmd/morpheus-cli/cmd/region.go
package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"time"

	"github.com/ava-labs/hypersdk/chain"
	"github.com/ava-labs/hypersdk/utils"
	"github.com/rhombus-tech/vm/actions"
	"github.com/rhombus-tech/vm/core"
	"github.com/rhombus-tech/vm/vm"
	"github.com/spf13/cobra"
)

var (
    sgxEndpoint string
    sevEndpoint string
)

var regionCmd = &cobra.Command{
    Use: "region",
    Short: "Manage regions",
    RunE: func(*cobra.Command, []string) error {
        return ErrMissingSubcommand
    },
}

var createRegionCmd = &cobra.Command{
    Use: "create [region-id]",
    Short: "Create new region",
    Args: cobra.ExactArgs(1),
    RunE: func(_ *cobra.Command, args []string) error {
        ctx := context.Background()
        _, authFactory, cli, vmClient, wsClient, err := handler.DefaultActor()
        if err != nil {
            return err
        }

        regionID := args[0]

        // Get attestations for both TEEs
        attestations, err := vmClient.GetRegionAttestations(ctx, regionID)
        if err != nil {
            return err
        }

        // Convert to core.TEEAttestation array
        var teeAttestations [2]core.TEEAttestation
        for i, att := range attestations[:2] {
            // Parse timestamp string into time.Time
            timestamp, err := time.Parse(time.RFC3339, att.Timestamp)
            if err != nil {
                return fmt.Errorf("failed to parse attestation timestamp: %w", err)
            }

            teeAttestations[i] = core.TEEAttestation{
                EnclaveID:   att.EnclaveId,
                Measurement: att.Measurement,
                Timestamp:   timestamp,  // Now using parsed time.Time
                Data:        att.Data,
                Signature:   att.Signature,
                RegionProof: att.RegionProof,
            }
        }

        // Create TEE addresses from endpoints
        tees := []core.TEEAddress{
            []byte(sgxEndpoint),
            []byte(sevEndpoint),
        }

        action := &actions.CreateRegionAction{
            RegionID:     regionID,
            TEEs:         tees,
            Attestations: teeAttestations,
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
            utils.Outf("{{red}}region creation failed:{{/}} %s\n", txID)
            return nil
        }

        utils.Outf("{{green}}region created:{{/}} %s\n", regionID)
        return nil
    },
}

var listRegionsCmd = &cobra.Command{
    Use: "list",
    Short: "List existing regions",
    RunE: func(_ *cobra.Command, _ []string) error {
        ctx := context.Background()
        _, _, _, vmClient, _, err := handler.DefaultActor()
        if err != nil {
            return err
        }

        regions, err := vmClient.GetRegions(ctx)
        if err != nil {
            return err
        }

        if len(regions.Regions) == 0 {
            utils.Outf("{{yellow}}no regions found{{/}}\n")
            return nil
        }

        utils.Outf("{{cyan}}Regions:{{/}}\n")
        for _, region := range regions.Regions {
            utils.Outf("- ID: %s\n", region.Id)
            utils.Outf("  Created: %s\n", region.CreatedAt)
            utils.Outf("  Worker Count: %d\n", len(region.WorkerIds))
        }
        return nil
    },
}

var addTEECmd = &cobra.Command{
    Use: "add-tee [region-id] [sgx-endpoint] [sev-endpoint]",
    Short: "Add TEE endpoints to region",
    Args: cobra.ExactArgs(3),
    RunE: func(_ *cobra.Command, args []string) error {
        ctx := context.Background()
        _, authFactory, cli, vmClient, wsClient, err := handler.DefaultActor()
        if err != nil {
            return err
        }

        action := &actions.UpdateRegionAction{
            RegionID: args[0],
            SGXEndpoint: args[1],
            SEVEndpoint: args[2],
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
            utils.Outf("{{red}}TEE update failed:{{/}} %s\n", txID)
            return nil
        }

        utils.Outf("{{green}}TEE endpoints added{{/}}\n")
        return nil
    },
}

var attestCmd = &cobra.Command{
    Use: "attest [region-id]",
    Short: "Get region attestations",
    Args: cobra.ExactArgs(1),
    RunE: func(_ *cobra.Command, args []string) error {
        ctx := context.Background()
        _, _, _, vmClient, _, err := handler.DefaultActor()
        if err != nil {
            return err
        }

        attestations, err := vmClient.GetRegionAttestations(ctx, args[0])
        if err != nil {
            return err
        }

        utils.Outf("{{cyan}}Region Attestations:{{/}}\n")
        if len(attestations) >= 2 {
            utils.Outf("SGX Attestation:\n")
            utils.Outf("  EnclaveID: %x\n", attestations[0].EnclaveId)
            utils.Outf("  Measurement: %x\n", attestations[0].Measurement)
            utils.Outf("  Timestamp: %s\n", attestations[0].Timestamp)

            utils.Outf("\nSEV Attestation:\n")
            utils.Outf("  EnclaveID: %x\n", attestations[1].EnclaveId)
            utils.Outf("  Measurement: %x\n", attestations[1].Measurement)
            utils.Outf("  Timestamp: %s\n", attestations[1].Timestamp)
        } else {
            utils.Outf("{{yellow}}insufficient attestations found{{/}}\n")
        }
        return nil
    },
}

func init() {
    // Create and add commands with their RunE functions
    createRegionCmd := &cobra.Command{
        Use:   "create [region-id]",
        Short: "Create new region",
        Args:  cobra.ExactArgs(1),
        RunE:  createRegion,  // Attach the function here
    }

    listRegionsCmd := &cobra.Command{
        Use:   "list",
        Short: "List existing regions",
        RunE:  listRegions,  // Attach the function here
    }

    addTEECmd := &cobra.Command{
        Use:   "add-tee [region-id] [sgx-endpoint] [sev-endpoint]",
        Short: "Add TEE endpoints to region",
        Args:  cobra.ExactArgs(3),
        RunE:  addTEE,  // Attach the function here
    }

    attestCmd := &cobra.Command{
        Use:   "attest [region-id]",
        Short: "Get region attestations",
        Args:  cobra.ExactArgs(1),
        RunE:  getAttestations,  // Attach the function here
    }

    // Add all subcommands to the region command
    regionCmd.AddCommand(
        createRegionCmd,
        listRegionsCmd,
        addTEECmd,
        attestCmd,
    )

    // Add flags
    createRegionCmd.Flags().StringVar(
        &sgxEndpoint,
        "sgx",
        "",
        "SGX endpoint",
    )
    createRegionCmd.Flags().StringVar(
        &sevEndpoint,
        "sev",
        "",
        "SEV endpoint",
    )
    createRegionCmd.MarkFlagRequired("sgx")
    createRegionCmd.MarkFlagRequired("sev")
}

func createRegion(_ *cobra.Command, args []string) error {
    ctx := context.Background()
    
    _, authFactory, cli, vmClient, wsClient, err := handler.DefaultActor()
    if err != nil {
        return err
    }

    regionID := args[0]

    action := &actions.CreateRegionAction{
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
        utils.Outf("{{red}}region creation failed:{{/}} %s\n", txID)
        return nil
    }

    utils.Outf("{{green}}region created:{{/}} %s\n", regionID)
    return nil
}


func listRegions(_ *cobra.Command, _ []string) error {
    ctx := context.Background()
    
    _, _, _, vmClient, _, err := handler.DefaultActor()
    if err != nil {
        return err
    }

    resp, err := vmClient.GetRegions(ctx)
    if err != nil {
        return err
    }

    if len(resp.Regions) == 0 {
        utils.Outf("{{yellow}}no regions found{{/}}\n")
        return nil
    }

    utils.Outf("{{cyan}}Regions:{{/}}\n")
    for _, region := range resp.Regions {
        utils.Outf("- ID: %s\n", region.Id)
        utils.Outf("  Created: %s\n", region.CreatedAt)
        utils.Outf("  Worker Count: %d\n", len(region.WorkerIds))
    }

    return nil
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
    
    _, authFactory, cli, vmClient, wsClient, err := handler.DefaultActor()
    if err != nil {
        return err
    }

    regionID := args[0]
    sgxEndpoint := args[1] 
    sevEndpoint := args[2]

    action := &actions.UpdateRegionAction{
        RegionID: regionID,
        SGXEndpoint: sgxEndpoint,
        SEVEndpoint: sevEndpoint,
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
        utils.Outf("{{red}}TEE update failed:{{/}} %s\n", txID)
        return nil
    }

    utils.Outf("{{green}}TEE endpoints added:{{/}} %s\n", regionID)
    return nil
}


func getAttestations(_ *cobra.Command, args []string) error {
    ctx := context.Background()
    
    _, _, _, vmClient, _, err := handler.DefaultActor()
    if err != nil {
        return err
    }

    regionID := args[0]
    attestations, err := vmClient.GetRegionAttestations(ctx, regionID)
    if err != nil {
        return err
    }

    utils.Outf("{{cyan}}Region Attestations:{{/}}\n")
    
    if len(attestations) >= 2 {
        utils.Outf("SGX Attestation:\n")
        utils.Outf("  EnclaveID: %x\n", attestations[0].EnclaveId)
        utils.Outf("  Measurement: %x\n", attestations[0].Measurement)
        utils.Outf("  Timestamp: %s\n", attestations[0].Timestamp)

        utils.Outf("\nSEV Attestation:\n")
        utils.Outf("  EnclaveID: %x\n", attestations[1].EnclaveId)
        utils.Outf("  Measurement: %x\n", attestations[1].Measurement)
        utils.Outf("  Timestamp: %s\n", attestations[1].Timestamp)
    } else {
        utils.Outf("{{yellow}}insufficient attestations found{{/}}\n")
    }
    
    return nil
}

var (
    regionHealthCmd = &cobra.Command{
        Use:   "health [regionID]",
        Short: "Get health status for a region",
        Args:  cobra.ExactArgs(1),
        RunE: func(cmd *cobra.Command, args []string) error {
            return handler.RegionHealth(args[0])
        },
    }

    regionMetricsCmd = &cobra.Command{
        Use:   "metrics [regionID] [pairID]",
        Short: "Get metrics for a region/pair",
        Args:  cobra.ExactArgs(2),
        RunE: func(cmd *cobra.Command, args []string) error {
            return handler.RegionMetrics(args[0], args[1])
        },
    }
)

var flagValues struct {
    URI string
    // ... other flag values ...
}

func init() {
    // Add URI flag to root command
    rootCmd.PersistentFlags().StringVar(&flagValues.URI, "uri", "http://localhost:9650", "URI of the node")
    regionCmd.AddCommand(regionHealthCmd)
    regionCmd.AddCommand(regionMetricsCmd)

    // ... other flag initialization ...
}

// Helper function to get client
func getJSONRPCClient() (*vm.JSONRPCClient, error) {
    uri := flagValues.URI
    if uri == "" {
        return nil, fmt.Errorf("uri is required")
    }
    return vm.NewJSONRPCClient(uri), nil
}

// Helper function to print JSON
func printJSON(v interface{}) error {
    encoder := json.NewEncoder(os.Stdout)
    encoder.SetIndent("", "  ")
    return encoder.Encode(v)
}

func init() {
    // Add commands to root command
    rootCmd.AddCommand(regionHealthCmd)
    rootCmd.AddCommand(regionMetricsCmd)
}