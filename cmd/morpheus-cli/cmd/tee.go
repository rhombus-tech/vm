package cmd

import (
    "context"
    "github.com/spf13/cobra"
    "github.com/ava-labs/hypersdk/utils"
)

var (
    controllerPath string
    wasmPath      string
)

var teeCmd = &cobra.Command{
    Use: "tee",
    Short: "TEE operations",
    RunE: func(*cobra.Command, []string) error {
        return ErrMissingSubcommand
    },
}

var verifyTEECmd = &cobra.Command{
    Use: "verify [enclave-id]",
    Short: "Verify TEE attestation",
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

        utils.Outf("{{cyan}}TEE Verification:{{/}}\n")
        for i, att := range attestations {
            teeType := "SGX"
            if i == 1 {
                teeType = "SEV" 
            }
            utils.Outf("  %s Attestation:\n", teeType)
            utils.Outf("    Enclave ID: %x\n", att.EnclaveId)
            utils.Outf("    Measurement: %x\n", att.Measurement)
            utils.Outf("    Timestamp: %s\n", att.Timestamp)
        }
        return nil
    },
}

func init() {
    teeCmd.AddCommand(verifyTEECmd)

    teeCmd.PersistentFlags().StringVar(
        &controllerPath,
        "controller",
        "/usr/local/bin/tee-controller",
        "TEE controller path",
    )
    teeCmd.PersistentFlags().StringVar(
        &wasmPath,
        "wasm",
        "/usr/local/bin/tee-wasm.wasm",
        "WASM module path",
    )
}