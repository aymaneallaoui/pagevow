package cli

import (
	"errors"
	"fmt"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/aymaneallaoui/pagevow/internal/plugin"
	"github.com/aymaneallaoui/pagevow/internal/ui"
	"github.com/aymaneallaoui/pagevow/internal/version"
)

const claudeProgram = "claude"

type pluginPathReport struct {
	Installed bool   `json:"installed"`
	Path      string `json:"path"`
	Root      string `json:"root"`
	ID        string `json:"id"`
}

func (a *app) newPluginCmd() *cobra.Command {
	group := &cobra.Command{
		Use:   "plugin",
		Short: "Manage the Claude Code plugin",
	}
	group.AddCommand(a.newPluginInstallCmd(), a.newPluginUninstallCmd(), a.newPluginPathCmd())
	return group
}

func (a *app) newPluginInstallCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "install",
		Short: "Install the Claude Code plugin",
		Long: "Write the pagevow plugin (skill, slash commands and the Stop hook) into the user config directory and register it with Claude Code.\n" +
			"The hook command stores the path of this binary, so run this command again after you upgrade or move pagevow.\n" +
			"Without the claude program on PATH, or with --no-register, it prints the commands to register the plugin yourself.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error { return a.runPluginInstall(cmd) },
	}
	cmd.Flags().Bool("no-register", false, "write the plugin files only and do not run the claude program")
	return cmd
}

func (a *app) newPluginUninstallCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "uninstall",
		Short: "Remove the Claude Code plugin",
		Long: "Unregister the plugin from Claude Code when the claude program is on PATH, then delete the plugin files that pagevow wrote.\n" +
			"A directory that does not carry the pagevow plugin manifest is never deleted.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error { return a.runPluginUninstall(cmd) },
	}
}

func (a *app) newPluginPathCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "path",
		Short: "Print where the plugin is installed",
		Long:  "Print the plugin directory, for claude --plugin-dir.\n\nExit codes: 0 the plugin is installed, 1 it is not.",
		Args:  cobra.NoArgs,
		RunE:  func(cmd *cobra.Command, _ []string) error { return a.runPluginPath(cmd) },
	}
	cmd.Flags().Bool("json", false, "print the result as JSON")
	return cmd
}

func (a *app) pluginRoot() (string, error) {
	userConfigDir, err := service[UserConfigDir](a)
	if err != nil {
		return "", err
	}
	dir, err := userConfigDir()
	if err != nil {
		return "", fmt.Errorf("find the user config directory: %w", err)
	}
	return filepath.Join(dir, "pagevow", plugin.RootDirName), nil
}

func (a *app) runPluginInstall(cmd *cobra.Command) error {
	noRegister, err := cmd.Flags().GetBool("no-register")
	if err != nil {
		return fmt.Errorf("read --no-register: %w", err)
	}
	root, err := a.pluginRoot()
	if err != nil {
		return err
	}
	executable, err := service[Executable](a)
	if err != nil {
		return err
	}
	binary, err := executable()
	if err != nil {
		return fmt.Errorf("find the pagevow executable: %w", err)
	}
	info, err := service[version.Info](a)
	if err != nil {
		return err
	}
	lookPath, err := service[LookPath](a)
	if err != nil {
		return err
	}
	out, err := a.printer(cmd.OutOrStdout())
	if err != nil {
		return err
	}
	ctx := commandContext(cmd)

	hookCommand, err := plugin.HookCommand(binary)
	if err != nil {
		return err
	}
	changed, err := plugin.Install(ctx, plugin.Options{Root: root, Version: info.Version, HookCommand: hookCommand})
	if err != nil {
		return err
	}
	if changed {
		out.Status(ui.OK, "plugin files written to %s", root)
	} else {
		out.Status(ui.OK, "plugin files are already up to date in %s", root)
	}

	claude, lookErr := lookPath(claudeProgram)
	switch {
	case noRegister:
		out.Status(ui.Info, "not registered with Claude Code (--no-register)")
		printManualRegistration(out, root)
	case lookErr != nil:
		out.Status(ui.Info, "the %s program is not on PATH, so the plugin was not registered", claudeProgram)
		printManualRegistration(out, root)
	default:
		commands, err := service[CommandRunner](a)
		if err != nil {
			return err
		}
		if err := plugin.Register(ctx, commands, claude, root); err != nil {
			return fmt.Errorf("register the plugin with Claude Code (the files are installed): %w", err)
		}
		out.Status(ui.OK, "registered %s with Claude Code", plugin.ID)
		out.Status(ui.Info, "start a new Claude Code session to load it")
	}
	out.Status(ui.Info, "the Stop hook stores the path of this binary: run pagevow plugin install again after you upgrade pagevow")
	return out.Err()
}

func printManualRegistration(out *ui.Printer, root string) {
	out.Blank()
	out.Line("Register it yourself:")
	out.Line("  %s plugin marketplace add %s", claudeProgram, plugin.ShellQuote(root))
	out.Line("  %s plugin install %s --scope user", claudeProgram, plugin.ID)
	out.Line("Or load it for one session only:")
	out.Line("  %s --plugin-dir %s", claudeProgram, plugin.ShellQuote(plugin.PluginDir(root)))
	out.Blank()
}

func (a *app) runPluginUninstall(cmd *cobra.Command) error {
	root, err := a.pluginRoot()
	if err != nil {
		return err
	}
	lookPath, err := service[LookPath](a)
	if err != nil {
		return err
	}
	out, err := a.printer(cmd.OutOrStdout())
	if err != nil {
		return err
	}
	ctx := commandContext(cmd)

	claude, lookErr := lookPath(claudeProgram)
	if lookErr != nil {
		out.Status(ui.Info, "the %s program is not on PATH: if the plugin was registered, run these yourself", claudeProgram)
		out.Line("  %s plugin uninstall %s", claudeProgram, plugin.ID)
		out.Line("  %s plugin marketplace remove %s", claudeProgram, plugin.Marketplace)
	} else {
		commands, err := service[CommandRunner](a)
		if err != nil {
			return err
		}
		warnings := plugin.Unregister(ctx, commands, claude)
		for _, warning := range warnings {
			out.Status(ui.Warn, "%v", warning)
		}
		if len(warnings) == 0 {
			out.Status(ui.OK, "unregistered %s from Claude Code", plugin.ID)
		}
	}

	removed, err := plugin.Remove(root)
	if err != nil {
		return err
	}
	if removed {
		out.Status(ui.OK, "removed %s", root)
	} else {
		out.Status(ui.Info, "no plugin files at %s", root)
	}
	return out.Err()
}

func (a *app) runPluginPath(cmd *cobra.Command) error {
	asJSON, err := cmd.Flags().GetBool("json")
	if err != nil {
		return fmt.Errorf("read --json: %w", err)
	}
	root, err := a.pluginRoot()
	if err != nil {
		return err
	}
	report := pluginPathReport{
		Installed: plugin.Installed(root),
		Path:      plugin.PluginDir(root),
		Root:      root,
		ID:        plugin.ID,
	}
	switch {
	case asJSON:
		if err := writeJSON(cmd, report); err != nil {
			return err
		}
	case report.Installed:
		if _, err := fmt.Fprintln(cmd.OutOrStdout(), report.Path); err != nil {
			return fmt.Errorf("write the plugin path: %w", err)
		}
	}
	if !report.Installed {
		return errors.New("the plugin is not installed; run: pagevow plugin install")
	}
	return nil
}
