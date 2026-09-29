package cli

import (
	"fmt"
	"io"
	"os"
	"time"

	"github.com/samber/do/v2"

	"github.com/aymaneallaoui/pagevow/internal/config"
	"github.com/aymaneallaoui/pagevow/internal/keys"
	"github.com/aymaneallaoui/pagevow/internal/ui"
	"github.com/aymaneallaoui/pagevow/internal/version"
)

// LookupEnv reads one environment variable.
type LookupEnv func(string) (string, bool)

// StdinInteractive reports whether a reader is an interactive terminal.
type StdinInteractive func(io.Reader) bool

// Clock returns the current time.
type Clock func() time.Time

// Exit ends the process with a code; only the second interrupt of a run calls it.
type Exit func(code int)

// ConfigPath is the default config file location.
type ConfigPath string

// Options supplies the services the container registers; zero fields fall back to system defaults.
type Options struct {
	LookupEnv        LookupEnv
	ConfigPath       string
	Store            keys.Store
	Prompter         Prompter
	StdinInteractive StdinInteractive
	Browser          BrowserLauncher
	Interrupts       Interrupts
	Exit             Exit
	Now              Clock
}

// SystemOptions returns the options of a real run: process environment, OS keychain, terminal prompts.
func SystemOptions() Options {
	return Options{
		LookupEnv:        os.LookupEnv,
		Store:            keys.NewKeychain(),
		Prompter:         huhPrompter{},
		StdinInteractive: func(r io.Reader) bool { return ui.IsTerminal(r) },
		Browser:          SystemBrowserLauncher(),
		Interrupts:       SystemInterrupts,
		Exit:             os.Exit,
		Now:              time.Now,
	}
}

// NewContainer registers every service the commands need.
func NewContainer(opts Options) do.Injector {
	opts = withDefaults(opts)
	injector := do.New()

	do.ProvideValue(injector, opts.LookupEnv)
	do.ProvideValue(injector, opts.StdinInteractive)
	do.ProvideValue(injector, opts.Prompter)
	do.ProvideValue(injector, opts.Store)
	do.ProvideValue(injector, opts.Browser)
	do.ProvideValue(injector, opts.Interrupts)
	do.ProvideValue(injector, opts.Exit)
	do.ProvideValue(injector, opts.Now)
	do.ProvideValue(injector, version.Get())

	do.Provide(injector, func(i do.Injector) (*keys.Resolver, error) {
		store, err := do.Invoke[keys.Store](i)
		if err != nil {
			return nil, fmt.Errorf("resolve key store: %w", err)
		}
		lookup, err := do.Invoke[LookupEnv](i)
		if err != nil {
			return nil, fmt.Errorf("resolve environment lookup: %w", err)
		}
		return keys.NewResolver(store, lookup), nil
	})
	do.Provide(injector, func(do.Injector) (ConfigPath, error) {
		if opts.ConfigPath != "" {
			return ConfigPath(opts.ConfigPath), nil
		}
		path, err := config.SystemPath()
		return ConfigPath(path), err
	})
	return injector
}

func withDefaults(opts Options) Options {
	system := SystemOptions()
	if opts.LookupEnv == nil {
		opts.LookupEnv = system.LookupEnv
	}
	if opts.Store == nil {
		opts.Store = system.Store
	}
	if opts.Prompter == nil {
		opts.Prompter = system.Prompter
	}
	if opts.StdinInteractive == nil {
		opts.StdinInteractive = system.StdinInteractive
	}
	if opts.Browser == nil {
		opts.Browser = system.Browser
	}
	if opts.Interrupts == nil {
		opts.Interrupts = system.Interrupts
	}
	if opts.Exit == nil {
		opts.Exit = system.Exit
	}
	if opts.Now == nil {
		opts.Now = system.Now
	}
	return opts
}
