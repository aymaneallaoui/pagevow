// Package cli defines the pagevow command tree and wires services from the container.
package cli

import (
	"fmt"
	"io"

	"github.com/samber/do/v2"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"github.com/aymaneallaoui/pagevow/internal/config"
	"github.com/aymaneallaoui/pagevow/internal/keys"
	"github.com/aymaneallaoui/pagevow/internal/ui"
)

type app struct {
	injector   do.Injector
	configFlag string
}

// NewRootCommand builds the full command tree on top of a container.
func NewRootCommand(injector do.Injector) *cobra.Command {
	a := &app{injector: injector}
	root := &cobra.Command{
		Use:           "pagevow",
		Short:         "Run browser tests written as goals",
		Long:          "pagevow runs browser tests written as goals. A decision model drives a real Chromium step by step,\nan independent verifier checks the final page, and every test leaves screenshots.",
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	root.CompletionOptions.DisableDefaultCmd = true
	root.PersistentFlags().StringVar(&a.configFlag, "config", "", "config file (default: pagevow/config.yaml in the user config directory)")
	root.AddCommand(
		a.newInstallCmd(),
		a.newUseCmd(),
		a.newStartCmd(),
		a.newStopCmd(),
		a.newStatusCmd(),
		a.newDoctorCmd(),
		a.newInitCmd(),
		a.newRunCmd(),
		a.newSuperviseCmd(),
		a.newHookCmd(),
		a.newPluginCmd(),
		a.newKeysCmd(),
		a.newUpdateCmd(),
		a.newVersionCmd(),
	)
	return root
}

func service[T any](a *app) (T, error) {
	value, err := do.Invoke[T](a.injector)
	if err != nil {
		var zero T
		return zero, fmt.Errorf("resolve service: %w", err)
	}
	return value, nil
}

func (a *app) configPath() (string, error) {
	if a.configFlag != "" {
		return a.configFlag, nil
	}
	path, err := service[ConfigPath](a)
	if err != nil {
		return "", err
	}
	return string(path), nil
}

func (a *app) loadConfig() (config.Config, string, error) {
	return a.loadConfigWithFlags(nil)
}

func (a *app) loadConfigWithFlags(flags map[string]*pflag.Flag) (config.Config, string, error) {
	path, err := a.configPath()
	if err != nil {
		return config.Config{}, "", err
	}
	lookup, err := service[LookupEnv](a)
	if err != nil {
		return config.Config{}, "", err
	}
	cfg, err := config.Load(config.Options{File: path, Flags: flags, LookupEnv: lookup})
	if err != nil {
		return config.Config{}, "", fmt.Errorf("load config %s: %w", path, err)
	}
	return cfg, path, nil
}

func (a *app) printer(w io.Writer) (*ui.Printer, error) {
	lookup, err := service[LookupEnv](a)
	if err != nil {
		return nil, err
	}
	return ui.Detect(w, lookup), nil
}

func (a *app) resolver() (*keys.Resolver, error) { return service[*keys.Resolver](a) }
