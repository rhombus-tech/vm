// cmd/morpheus-cli/cmd/verify.go

package cmd

import (
    "context"
    "fmt"
    "time"

    "github.com/spf13/cobra"
    "github.com/ava-labs/hypersdk/utils"
)

var (
    verifyCmd = &cobra.Command{
        Use:   "verify",
        Short: "Verification operations",
        RunE: func(*cobra.Command, []string) error {
            return ErrMissingSubcommand
        },
    }

    verifyStateCmd = &cobra.Command{
        Use:   "state [object-id] [region-id]",
        Short: "Verify object state",
        Args:  cobra.ExactArgs(2),
        RunE:  verifyState,
    }

    verifyAttestationCmd = &cobra.Command{
        Use:   "attestation [enclave-id] [region-id]",
        Short: "Verify TEE attestation",
        Args:  cobra.ExactArgs(2),
        RunE:  verifyAttestation,
    }

    // Add flags for verification
    expectedHash []byte
    timeWindow   time.Duration
)

func init() {
    // Add subcommands to verify command
    verifyCmd.AddCommand(
        verifyStateCmd,
        verifyAttestationCmd,
    )

    // Add flags for state verification
    verifyStateCmd.Flags().BytesHexVar(
        &expectedHash,
        "hash",
        nil,
        "Expected state hash (hex)",
    )

    // Add flags for attestation verification
    verifyAttestationCmd.Flags().DurationVar(
        &timeWindow,
        "window",
        5*time.Minute,
        "Maximum attestation age",
    )

    // Add verify command to root
    rootCmd.AddCommand(verifyCmd)
}

func verifyState(_ *cobra.Command, args []string) error {
    ctx := context.Background()
    _, _, _, apiClient, _, err := handler.DefaultActor()
    if err != nil {
        return err
    }

    objectID := args[0]
    regionID := args[1]

    obj, err := apiClient.GetObject(ctx, objectID, regionID)
    if err != nil {
        return fmt.Errorf("failed to get object: %w", err)
    }

    if obj == nil {
        utils.Outf("{{red}}object not found{{/}}\n")
        return nil
    }

    // Get latest attestations
    attestations, err := apiClient.GetRegionAttestations(ctx, regionID)
    if err != nil {
        return fmt.Errorf("failed to get attestations: %w", err)
    }

    utils.Outf("{{green}}Object State Verification:{{/}}\n")
    utils.Outf("ID:        %s\n", objectID)
    utils.Outf("Region:    %s\n", regionID)
    utils.Outf("Status:    %s\n", obj.Status)
    utils.Outf("Updated:   %s\n", obj.LastUpdated.Format(time.RFC3339))

    utils.Outf("\n{{cyan}}Latest Attestations:{{/}}\n")
    for i, att := range attestations {
        teeType := "SGX"
        if i == 1 {
            teeType = "SEV"
        }
        utils.Outf("%s Attestation:\n", teeType)
        utils.Outf("  Enclave ID:   %x\n", att.EnclaveId)
        utils.Outf("  Measurement:  %x\n", att.Measurement)
        utils.Outf("  Timestamp:    %s\n", att.Timestamp)
        utils.Outf("  Region Proof: %x\n", att.RegionProof)
    }

    return nil
}

func verifyAttestation(_ *cobra.Command, args []string) error {
    ctx := context.Background()
    _, _, _, apiClient, _, err := handler.DefaultActor()
    if err != nil {
        return err
    }

    enclaveID := args[0]
    regionID := args[1]

    // Get valid enclave info
    enclaveInfo, err := apiClient.GetValidEnclave(ctx, enclaveID, regionID)
    if err != nil {
        return fmt.Errorf("failed to get enclave info: %w", err)
    }

    if enclaveInfo == nil {
        utils.Outf("{{red}}enclave not found or not valid{{/}}\n")
        return nil
    }

    // Get latest attestation
    attestations, err := apiClient.GetRegionAttestations(ctx, regionID)
    if err != nil {
        return fmt.Errorf("failed to get attestations: %w", err)
    }

    utils.Outf("{{green}}Attestation Verification:{{/}}\n")
    utils.Outf("Enclave ID:      %s\n", enclaveID)
    utils.Outf("Type:            %s\n", enclaveInfo.EnclaveType)
    utils.Outf("Region:          %s\n", regionID)
    utils.Outf("Valid From:      %s\n", enclaveInfo.ValidFrom.Format(time.RFC3339))
    utils.Outf("Valid Until:     %s\n", enclaveInfo.ValidUntil.Format(time.RFC3339))
    
    if len(attestations) > 0 {
        att := attestations[0]
        utils.Outf("Last Attestation: %s\n", att.Timestamp)
        utils.Outf("Measurement:      %x\n", att.Measurement)
        utils.Outf("Region Proof:     %x\n", att.RegionProof)

        // Verify attestation age if timeWindow is set
        attTime, err := time.Parse(time.RFC3339, att.Timestamp)
        if err != nil {
            return fmt.Errorf("failed to parse attestation timestamp: %w", err)
        }

        age := time.Since(attTime)
        if age > timeWindow {
            utils.Outf("{{red}}Warning: Attestation is older than specified window (%s){{/}}\n", timeWindow)
        } else {
            utils.Outf("{{green}}✓ Attestation age within specified window{{/}}\n")
        }
    } else {
        utils.Outf("{{red}}No current attestations found{{/}}\n")
    }

    return nil
}