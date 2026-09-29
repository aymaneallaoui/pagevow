package cli

import (
	"errors"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/aymaneallaoui/pagevow/internal/server"
)

func (a *app) newSuperviseCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:    "supervise --spec FILE",
		Short:  "Run one managed process (used by pagevow start)",
		Hidden: true,
		Args:   cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			spec, err := cmd.Flags().GetString("spec")
			if err != nil {
				return fmt.Errorf("read --spec: %w", err)
			}
			if spec == "" {
				return a.refuseBareSupervise(cmd)
			}
			procs, err := service[Processes](a)
			if err != nil {
				return err
			}
			code, err := procs.Supervise(commandContext(cmd), spec)
			if err != nil {
				return &ExitError{Code: max(code, ExitFailure), Err: err}
			}
			switch code {
			case ExitOK:
				return nil
			case server.GuardExitCode:
				return &ExitError{Code: code, Err: errors.New("the managed process was stopped by the GPU guard")}
			}
			return &ExitError{Code: code, Err: fmt.Errorf("the managed process ended with exit code %d", code)}
		},
	}
	cmd.Flags().String("spec", "", "spec file written by pagevow start")
	return cmd
}

func (a *app) refuseBareSupervise(cmd *cobra.Command) error {
	interactive, err := service[StdinInteractive](a)
	if err != nil {
		return err
	}
	if interactive(cmd.InOrStdin()) {
		return errors.New("supervise is started by pagevow start and is not meant to be run by hand; run: pagevow start")
	}
	return errors.New("supervise needs --spec FILE")
}
