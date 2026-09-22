package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/aymaneallaoui/pagevow/internal/ui"
	"github.com/aymaneallaoui/pagevow/internal/version"
)

func (a *app) newVersionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print the version",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			info, err := service[version.Info](a)
			if err != nil {
				return err
			}
			out, err := a.printer(cmd.OutOrStdout())
			if err != nil {
				return err
			}
			out.Pairs([]ui.Pair{
				{Key: "pagevow", Value: info.Version},
				{Key: "commit", Value: info.Commit},
				{Key: "built", Value: info.Date},
				{Key: "go", Value: fmt.Sprintf("%s %s/%s", info.Go, info.OS, info.Arch)},
			})
			return out.Err()
		},
	}
}
