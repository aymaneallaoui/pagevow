package cli

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"runtime"
	"time"

	"github.com/samber/do/v2"

	"github.com/aymaneallaoui/pagevow/internal/browser"
	"github.com/aymaneallaoui/pagevow/internal/config"
	"github.com/aymaneallaoui/pagevow/internal/hook"
	"github.com/aymaneallaoui/pagevow/internal/keys"
	"github.com/aymaneallaoui/pagevow/internal/server"
	"github.com/aymaneallaoui/pagevow/internal/ui"
	"github.com/aymaneallaoui/pagevow/internal/version"
)

const (
	downloadTimeout = 15 * time.Minute
	maxRedirects    = 3
)

// LookupEnv reads one environment variable.
type LookupEnv func(string) (string, bool)

// StdinInteractive reports whether a reader is an interactive terminal.
type StdinInteractive func(io.Reader) bool

// UserConfigDir returns the user configuration directory.
type UserConfigDir func() (string, error)

// CommandRunner runs an external command and returns its combined output.
type CommandRunner interface {
	Run(ctx context.Context, name string, args ...string) ([]byte, error)
}

// Clock returns the current time.
type Clock func() time.Time

// Exit ends the process with a code; only the second interrupt of a run calls it.
type Exit func(code int)

// ConfigPath is the default config file location.
type ConfigPath string

// BrowserBaseURL is where install --browser downloads Chrome for Testing; empty means the official storage.
type BrowserBaseURL string

type browserPinOverride struct{ pin *browser.Pin }

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
	Processes        Processes
	ManagedBrowsers  ManagedBrowsers
	GPU              GPUReader
	Executable       Executable
	CacheDir         CacheDir
	UserConfigDir    UserConfigDir
	HomeDir          HomeDir
	LookPath         LookPath
	OS               GOOS
	Arch             GOARCH
	HTTPClient       *http.Client
	BrowserBaseURL   string
	BrowserPin       *browser.Pin
	CommandRunner    CommandRunner
	HookRunner       hook.Runner
	HookGit          hook.Git
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
		ManagedBrowsers:  systemManagedBrowsers{},
		GPU:              server.NvidiaSMI{},
		Executable:       os.Executable,
		CacheDir:         os.UserCacheDir,
		UserConfigDir:    os.UserConfigDir,
		HomeDir:          os.UserHomeDir,
		LookPath:         exec.LookPath,
		OS:               GOOS(runtime.GOOS),
		Arch:             GOARCH(runtime.GOARCH),
		HTTPClient:       downloadClient(),
		CommandRunner:    execCommandRunner{},
	}
}

func downloadClient() *http.Client {
	return &http.Client{
		Timeout: downloadTimeout,
		CheckRedirect: func(_ *http.Request, via []*http.Request) error {
			if len(via) > maxRedirects {
				return fmt.Errorf("stopped after %d redirects", maxRedirects)
			}
			return nil
		},
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
	do.ProvideValue(injector, opts.ManagedBrowsers)
	do.ProvideValue(injector, opts.GPU)
	do.ProvideValue(injector, opts.Executable)
	do.ProvideValue(injector, opts.CacheDir)
	do.ProvideValue(injector, opts.UserConfigDir)
	do.ProvideValue(injector, opts.CommandRunner)
	do.ProvideValue(injector, hookRunnerOverride{runner: opts.HookRunner})
	do.ProvideValue(injector, opts.HomeDir)
	do.ProvideValue(injector, opts.LookPath)
	do.ProvideValue(injector, opts.OS)
	do.ProvideValue(injector, opts.Arch)
	do.ProvideValue(injector, opts.HTTPClient)
	do.ProvideValue(injector, BrowserBaseURL(opts.BrowserBaseURL))
	do.ProvideValue(injector, browserPinOverride{pin: opts.BrowserPin})
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
	do.Provide(injector, func(i do.Injector) (hook.Git, error) {
		if opts.HookGit != nil {
			return opts.HookGit, nil
		}
		lookPath, err := do.Invoke[LookPath](i)
		if err != nil {
			return nil, fmt.Errorf("resolve program lookup: %w", err)
		}
		if _, err := lookPath("git"); err != nil {
			return nil, nil
		}
		return execGit{}, nil
	})
	do.Provide(injector, func(do.Injector) (Processes, error) {
		if opts.Processes != nil {
			return opts.Processes, nil
		}
		dir, err := config.StateDir(opts.CacheDir)
		if err != nil {
			return nil, err
		}
		return newSystemProcesses(dir, opts.Executable, opts.GPU), nil
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
	if opts.ManagedBrowsers == nil {
		opts.ManagedBrowsers = system.ManagedBrowsers
	}
	if opts.GPU == nil {
		opts.GPU = system.GPU
	}
	if opts.Executable == nil {
		opts.Executable = system.Executable
	}
	if opts.CacheDir == nil {
		opts.CacheDir = system.CacheDir
	}
	if opts.UserConfigDir == nil {
		opts.UserConfigDir = system.UserConfigDir
	}
	if opts.CommandRunner == nil {
		opts.CommandRunner = system.CommandRunner
	}
	if opts.HomeDir == nil {
		opts.HomeDir = system.HomeDir
	}
	if opts.LookPath == nil {
		opts.LookPath = system.LookPath
	}
	if opts.OS == "" {
		opts.OS = system.OS
	}
	if opts.Arch == "" {
		opts.Arch = system.Arch
	}
	if opts.HTTPClient == nil {
		opts.HTTPClient = system.HTTPClient
	}
	return opts
}
