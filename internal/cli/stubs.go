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

func (a *app) newStartCmd() *cobra.Command {
	return stub("start", "Start what the active backend needs, and the browser", 3, nil)
}

func (a *app) newStopCmd() *cobra.Command {
	return stub("stop", "Stop everything pagevow started", 3, nil)
}

func (a *app) newDoctorCmd() *cobra.Command {
	return stub("doctor", "Check the setup and say how to fix each problem", 3, nil)
}

func (a *app) newUpdateCmd() *cobra.Command {
	return stub("update", "Replace the binary with the latest release", 5, nil)
}

func (a *app) newHookCmd() *cobra.Command {
	hook := &cobra.Command{
		Use:   "hook",
		Short: "Claude Code hook entry points",
	}
	hook.AddCommand(stub("stop", "Stop hook: reads the Claude Code hook JSON on stdin", 4, nil))
	return hook
}

func (a *app) newPluginCmd() *cobra.Command {
	plugin := &cobra.Command{
		Use:   "plugin",
		Short: "Manage the Claude Code plugin",
	}
	plugin.AddCommand(
		stub("install", "Install the Claude Code plugin", 4, nil),
		stub("uninstall", "Remove the Claude Code plugin", 4, nil),
		stub("path", "Print where the plugin is installed", 4, nil),
	)
	return plugin
}
