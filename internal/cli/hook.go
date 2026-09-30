package cli

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/aymaneallaoui/pagevow/internal/hook"
)

func (a *app) newHookCmd() *cobra.Command {
	group := &cobra.Command{
		Use:   "hook",
		Short: "Claude Code hook entry points",
	}
	group.AddCommand(a.newHookStopCmd())
	return group
}

func (a *app) newHookStopCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "stop",
		Short: "Stop hook: run the browser tests and block Claude while they fail",
		Long: "The Claude Code Stop hook. It reads the hook JSON on stdin, runs the tests file of the project with pagevow run,\n" +
			"and blocks Claude from stopping while the tests fail, at most PAGEVOW_HOOK_MAX_BLOCKS times in a row (default 2).\n" +
			"It skips the run when the project is unchanged since the last pass. PAGEVOW_HOOK=0 turns it off.\n" +
			"It never writes to stdout and never prompts.\n\n" +
			"Exit codes: 0 Claude may stop, 1 the tests were skipped or the block limit was reached, 2 Claude is blocked.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error { return a.runHookStop(cmd) },
	}
}

func (a *app) runHookStop(cmd *cobra.Command) error {
	lookup, err := service[LookupEnv](a)
	if err != nil {
		return err
	}
	interactive, err := service[StdinInteractive](a)
	if err != nil {
		return err
	}
	git, err := service[hook.Git](a)
	if err != nil {
		return err
	}
	suite, err := a.hookRunner()
	if err != nil {
		return err
	}
	var input hook.Input
	if stdin := cmd.InOrStdin(); !interactive(stdin) {
		input = hook.ReadInput(stdin)
	}
	code := hook.Stop(commandContext(cmd), hook.Config{
		LookupEnv: lookup,
		Getwd:     os.Getwd,
		Runner:    suite,
		Git:       git,
		Stderr:    cmd.ErrOrStderr(),
	}, input)
	if code == ExitOK {
		return nil
	}
	return &ExitError{Code: code, Err: errors.New("browser tests did not pass"), Silent: true}
}

func (a *app) hookRunner() (hook.Runner, error) {
	override, err := service[hookRunnerOverride](a)
	if err != nil {
		return nil, err
	}
	if override.runner != nil {
		return override.runner, nil
	}
	executable, err := service[Executable](a)
	if err != nil {
		return nil, err
	}
	runner := subprocessHookRunner{executable: executable}
	if a.configFlag != "" {
		if runner.configFile, err = filepath.Abs(a.configFlag); err != nil {
			return nil, fmt.Errorf("resolve --config: %w", err)
		}
	}
	return runner, nil
}
