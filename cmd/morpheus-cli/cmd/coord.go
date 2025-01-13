// cmd/morpheus-cli/cmd/coord.go

package cmd

import (
    "context"
    "fmt"
    "time"

    "github.com/spf13/cobra"
    "github.com/ava-labs/hypersdk/utils"
)

var (
    coordCmd = &cobra.Command{
        Use:   "coord",
        Short: "Coordination operations",
        RunE: func(*cobra.Command, []string) error {
            return ErrMissingSubcommand
        },
    }

    listWorkersCmd = &cobra.Command{
        Use:   "workers [region-id]",
        Short: "List active workers",
        Args:  cobra.ExactArgs(1),
        RunE:  listWorkers,
    }

    showTasksCmd = &cobra.Command{
        Use:   "tasks [region-id]",
        Short: "Show pending tasks",
        Args:  cobra.ExactArgs(1),
        RunE:  showTasks,
    }
)

func init() {
    // Add subcommands to coord command
    coordCmd.AddCommand(
        listWorkersCmd,
        showTasksCmd,
    )

    // Add coord command to root
    rootCmd.AddCommand(coordCmd)
}

func listWorkers(_ *cobra.Command, args []string) error {
    ctx := context.Background()
    _, _, _, apiClient, _, err := handler.DefaultActor()
    if err != nil {
        return err
    }

    regionID := args[0]

    workers, err := apiClient.GetRegionWorkers(ctx, regionID)
    if err != nil {
        return fmt.Errorf("failed to get workers: %w", err)
    }

    utils.Outf("{{cyan}}Active Workers in Region %s:{{/}}\n", regionID)
    for _, worker := range workers {
        utils.Outf("Worker ID:     %s\n", worker.ID)
        utils.Outf("Enclave Type:  %s\n", worker.EnclaveType)
        utils.Outf("Status:        %s\n", worker.Status)
        utils.Outf("Active Since:  %s\n", worker.ActiveSince.Format(time.RFC3339))
        utils.Outf("Tasks Handled: %d\n", worker.TasksHandled)
        utils.Outf("\n")
    }

    return nil
}

func showTasks(_ *cobra.Command, args []string) error {
    ctx := context.Background()
    _, _, _, apiClient, _, err := handler.DefaultActor()
    if err != nil {
        return err
    }

    regionID := args[0]

    tasks, err := apiClient.GetRegionTasks(ctx, regionID)
    if err != nil {
        return fmt.Errorf("failed to get tasks: %w", err)
    }

    utils.Outf("{{cyan}}Pending Tasks in Region %s:{{/}}\n", regionID)
    for _, task := range tasks {
        utils.Outf("Task ID:      %s\n", task.ID)
        utils.Outf("Status:       %s\n", task.Status)
        utils.Outf("Created:      %s\n", task.CreatedAt.Format(time.RFC3339))
        utils.Outf("Workers:\n")
        for _, workerID := range task.WorkerIDs {
            utils.Outf("  - %s\n", workerID)
        }
        utils.Outf("Progress:     %.1f%%\n", task.Progress*100)
        if task.Error != "" {
            utils.Outf("{{red}}Error:        %s{{/}}\n", task.Error)
        }
        utils.Outf("\n")
    }

    return nil
}