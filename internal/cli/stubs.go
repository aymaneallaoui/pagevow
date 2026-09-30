package cli

import "github.com/spf13/cobra"

func stub(use, short string, phase int, configure func(*cobra.Command)) *cobra.Command {
	cmd := &cobra.Command{
		Use:   use,
		Short: short,
		Args:  cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return notImplemented(cmd, phase)
		},
	}
	if configure != nil {
		configure(cmd)
	}
	return cmd
}

func (a *app) newInstallCmd() *cobra.Command {
	return stub("install", "Download the browser, and optionally a local model", 5, func(cmd *cobra.Command) {
		cmd.Flags().Bool("browser", false, "download the pinned Chrome for Testing build")
		cmd.Flags().String("model", "", "local model to install: a path or a private Hugging Face repository")
	})
}

func (a *app) newUpdateCmd() *cobra.Command {
	return stub("update", "Replace the binary with the latest release", 5, nil)
}
