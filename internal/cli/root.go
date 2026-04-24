package cli

import (
	"context"
	"fmt"
	"os"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/amedespinosa/powerkan/internal/bootstrap"
	"github.com/amedespinosa/powerkan/internal/tui"
)

type rootFlags struct {
	configPath string
	verbose    bool
}

// NewRootCommand constructs the powerkan CLI tree.
func NewRootCommand() *cobra.Command {
	flags := &rootFlags{}

	cmd := &cobra.Command{
		Use:           "powerkan",
		Short:         "Powerkan launches the TUI and related CLI utilities",
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			rt, err := bootstrap.Init(context.Background(), bootstrap.Options{
				ConfigPath: flags.configPath,
				Verbose:    flags.verbose,
			})
			if err != nil {
				return err
			}
			defer func() { _ = rt.Close() }()

			p := tea.NewProgram(
				tui.NewModel(rt.Config, rt.Paths),
				tea.WithAltScreen(),
			)

			if !term.IsTerminal(int(os.Stdout.Fd())) {
				return fmt.Errorf("powerkan TUI requires an interactive terminal")
			}

			_, err = p.Run()
			return err
		},
	}

	cmd.PersistentFlags().StringVar(&flags.configPath, "config", "", "override config.yaml path")
	cmd.PersistentFlags().BoolVar(&flags.verbose, "verbose", false, "enable verbose logging")

	cmd.AddCommand(newExportCommand(flags))
	cmd.AddCommand(newWebhookCommand(flags))

	cmd.SetFlagErrorFunc(func(cmd *cobra.Command, err error) error {
		if strings.Contains(err.Error(), "invalid argument") {
			return fmt.Errorf("%w\n\n%s", err, cmd.UsageString())
		}
		return err
	})

	return cmd
}
