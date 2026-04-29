package cli

import (
	"context"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/amedespinosa/powerkan/internal/bootstrap"
	"github.com/amedespinosa/powerkan/internal/kanban"
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
			service := kanban.NewService(rt.DB, rt.Config)
			results, err := service.PostSprintEndWebhooks(context.Background(), sprintID, force)
			if err != nil {
				return err
			}

			for _, result := range results {
				if result.Skipped {
					_, _ = fmt.Fprintf(cmd.OutOrStdout(), "skipped sprint %d\n", result.SprintID)
					continue
				}
				_, _ = fmt.Fprintf(cmd.OutOrStdout(), "posted sprint %d\n", result.SprintID)
			}
			if len(results) == 0 {
				_, _ = fmt.Fprintln(cmd.OutOrStdout(), "no ended sprints to post")
			}
			return nil
		},
	}

	cmd.Flags().Int64Var(&sprintID, "sprint", 0, "specific sprint ID to evaluate")
	cmd.Flags().BoolVar(&force, "force", false, "bypass idempotency and post again")

	return cmd
}
