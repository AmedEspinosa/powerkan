package cli

import (
	"context"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/amedespinosa/powerkan/internal/bootstrap"
)

func newWebhookCommand(flags *rootFlags) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "webhook",
		Short: "Run webhook-related commands",
	}

	cmd.AddCommand(newWebhookSprintEndCommand(flags))
	return cmd
}

func newWebhookSprintEndCommand(flags *rootFlags) *cobra.Command {
	var sprintID int64
	var force bool

	cmd := &cobra.Command{
		Use:   "sprint-end",
		Short: "Post sprint end summaries",
		RunE: func(cmd *cobra.Command, args []string) error {
			rt, err := bootstrap.Init(context.Background(), bootstrap.Options{
				ConfigPath: flags.configPath,
				Verbose:    flags.verbose,
			})
			if err != nil {
				return err
			}
			defer func() { _ = rt.Close() }()

			rt.Logger.Info("webhook sprint-end invoked", "sprint_id", sprintID, "force", force)
			return fmt.Errorf("webhook sprint-end command is %w", errPhase0NotImplemented)
		},
	}

	cmd.Flags().Int64Var(&sprintID, "sprint", 0, "specific sprint ID to evaluate")
	cmd.Flags().BoolVar(&force, "force", false, "bypass idempotency and post again")

	return cmd
}
