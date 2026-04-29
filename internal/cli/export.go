package cli

import (
	"context"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/amedespinosa/powerkan/internal/bootstrap"
	"github.com/amedespinosa/powerkan/internal/kanban"
)

func newExportCommand(flags *rootFlags) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "export",
		Short: "Export data from powerkan",
	}

	cmd.AddCommand(newExportTicketCommand(flags))
	return cmd
}

func newExportTicketCommand(flags *rootFlags) *cobra.Command {
	var ticketID string
	var format string
	var out string

	cmd := &cobra.Command{
		Use:   "ticket",
		Short: "Export a single ticket",
		RunE: func(cmd *cobra.Command, args []string) error {
			if normalized := strings.ToLower(format); normalized != "md" && normalized != "csv" {
				return fmt.Errorf("invalid --format %q: must be md or csv", format)
			}

			rt, err := bootstrap.Init(context.Background(), bootstrap.Options{
				ConfigPath: flags.configPath,
				Verbose:    flags.verbose,
			})
			if err != nil {
				return err
			}
			defer func() { _ = rt.Close() }()

			rt.Logger.Info("export ticket invoked", "ticket_id", ticketID, "format", format, "out", out)
			service := kanban.NewService(rt.DB, rt.Config)

			var exportedPath string
			switch strings.ToLower(format) {
			case "md":
				exportedPath, err = service.ExportTicketMarkdown(context.Background(), ticketID, out)
			case "csv":
				exportedPath, err = service.ExportTicketCSV(context.Background(), ticketID, out)
			}
			if err != nil {
				return err
			}

			_, _ = fmt.Fprintln(cmd.OutOrStdout(), exportedPath)
			return nil
		},
	}

	cmd.Flags().StringVar(&ticketID, "id", "", "ticket ID to export")
	cmd.Flags().StringVar(&format, "format", "", "export format: md or csv")
	cmd.Flags().StringVar(&out, "out", "", "output file path")
	_ = cmd.MarkFlagRequired("id")
	_ = cmd.MarkFlagRequired("format")

	return cmd
}
