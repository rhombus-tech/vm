package cmd

import (
    "context"
    "fmt"
    "time"
    "bytes"

    "github.com/spf13/cobra"
    "github.com/ava-labs/hypersdk/utils"
    "github.com/rhombus-tech/vm/core"
)

var (
    verifyCmd = &cobra.Command{
        Use:   "verify",
        Short: "Verification operations",
        RunE: func(*cobra.Command, []string) error {
            return ErrMissingSubcommand
        },
    }

    // Flag variables
    expectedHash []byte
    timeWindow   time.Duration
)

var verifyStateCmd = &cobra.Command{
    Use:   "state [object-id] [region-id]",
    Short: "Verify object state",
    Args:  cobra.ExactArgs(2),
    RunE:  verifyState,
}

var verifyAttestationCmd = &cobra.Command{
    Use:   "attestation [enclave-id] [region-id]",
    Short: "Verify TEE attestation",
    Args:  cobra.ExactArgs(2),
    RunE:  verifyAttestation,
}

func verifyState(_ *cobra.Command, args []string) error {
    ctx := context.Background()
    _, _, _, vmClient, _, err := handler.DefaultActor()
    if err != nil {
        return err
    }

    objectID := args[0]
    regionID := args[1]

    // Get object state
    obj, err := vmClient.GetObject(ctx, objectID, regionID)
    if err != nil {
        return fmt.Errorf("failed to get object: %w", err)
    }

    if obj == nil {
        utils.Outf("{{red}}object not found{{/}}\n")
        return nil
    }

    // Get latest attestations
    attestations, err := vmClient.GetRegionAttestations(ctx, regionID)
    if err != nil {
        return fmt.Errorf("failed to get attestations: %w", err)
    }

    utils.Outf("{{green}}Object State Verification:{{/}}\n")
    utils.Outf("ID:        %s\n", objectID)
    utils.Outf("Region:    %s\n", regionID)
    utils.Outf("Status:    %s\n", obj.Status)
    utils.Outf("Updated:   %s\n", obj.LastUpdated.Format(time.RFC3339))

    utils.Outf("\n{{cyan}}Latest Attestations:{{/}}\n")
    for i, protoAtt := range attestations {
        // Convert proto attestation to core attestation
        timestamp, err := time.Parse(time.RFC3339, protoAtt.Timestamp)
        if err != nil {
            return fmt.Errorf("failed to parse timestamp: %w", err)
        }

        att := core.TEEAttestation{
            EnclaveID:   protoAtt.EnclaveId,
            Measurement: protoAtt.Measurement,
            Timestamp:   timestamp,
            Data:        protoAtt.Data,
            RegionProof: protoAtt.RegionProof,
        }

        teeType := "SGX"
        if i == 1 {
            teeType = "SEV"
        }
        utils.Outf("%s Attestation:\n", teeType)
        utils.Outf("  Enclave ID:   %x\n", att.EnclaveID)
        utils.Outf("  Measurement:  %x\n", att.Measurement)
        utils.Outf("  Timestamp:    %s\n", att.Timestamp.Format(time.RFC3339))
        utils.Outf("  Region Proof: %x\n", att.RegionProof)
    }

    return nil
}


func verifyAttestation(_ *cobra.Command, args []string) error {
    ctx := context.Background()
    _, _, _, vmClient, _, err := handler.DefaultActor()
    if err != nil {
        return err
    }

    enclaveID := args[0]
    regionID := args[1]

    // Get valid enclave info
    enclaveInfo, err := vmClient.GetValidEnclave(ctx, enclaveID, regionID)
    if err != nil {
        return fmt.Errorf("failed to get enclave info: %w", err)
    }

    if enclaveInfo == nil {
        utils.Outf("{{red}}enclave not found or not valid{{/}}\n")
        return nil
    }

    // Get latest attestation
    attestations, err := vmClient.GetRegionAttestations(ctx, regionID)
    if err != nil {
        return fmt.Errorf("failed to get attestations: %w", err)
    }

    // Find matching attestation
    var matchingAtt *core.TEEAttestation
    for _, protoAtt := range attestations {
        if bytes.Equal([]byte(enclaveID), protoAtt.EnclaveId) {
            // Convert proto attestation to core attestation
            timestamp, err := time.Parse(time.RFC3339, protoAtt.Timestamp)
            if err != nil {
                return fmt.Errorf("failed to parse timestamp: %w", err)
            }

            att := &core.TEEAttestation{
                EnclaveID:   protoAtt.EnclaveId,
                Measurement: protoAtt.Measurement,
                Timestamp:   timestamp,
                Data:        protoAtt.Data,
                RegionProof: protoAtt.RegionProof,
            }
            matchingAtt = att
            break
        }
    }

    if matchingAtt == nil {
        utils.Outf("{{red}}no current attestation found for enclave{{/}}\n")
        return nil
    }

    age := time.Since(matchingAtt.Timestamp)
    if age > timeWindow {
        utils.Outf("{{red}}attestation too old:{{/}} %s\n", age)
        return nil
    }

    // Verify measurement matches
    if !bytes.Equal(matchingAtt.Measurement, enclaveInfo.Measurement) {
        utils.Outf("{{red}}measurement mismatch{{/}}\n")
        utils.Outf("Expected: %x\n", enclaveInfo.Measurement)
        utils.Outf("Got:      %x\n", matchingAtt.Measurement)
        return nil
    }

    utils.Outf("{{green}}Attestation Verification:{{/}}\n")
    utils.Outf("Enclave ID:      %s\n", enclaveID)
    utils.Outf("Type:            %s\n", enclaveInfo.EnclaveType)
    utils.Outf("Region:          %s\n", regionID)
    utils.Outf("Valid From:      %s\n", enclaveInfo.ValidFrom.Format(time.RFC3339))
    utils.Outf("Valid Until:     %s\n", enclaveInfo.ValidUntil.Format(time.RFC3339))
    utils.Outf("Last Attestation:%s\n", matchingAtt.Timestamp.Format(time.RFC3339))
    utils.Outf("Age:             %s\n", age)
    utils.Outf("\n{{green}}✓ Attestation valid{{/}}\n")

    return nil
}

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