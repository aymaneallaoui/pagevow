package cli

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/spf13/cobra"

	"github.com/aymaneallaoui/pagevow/internal/browser"
	"github.com/aymaneallaoui/pagevow/internal/config"
	"github.com/aymaneallaoui/pagevow/internal/server"
	"github.com/aymaneallaoui/pagevow/internal/ui"
)

const bytesPerMB = 1 << 20

type installReport struct {
	Version          string `json:"version"`
	Platform         string `json:"platform"`
	Executable       string `json:"executable"`
	AlreadyInstalled bool   `json:"already_installed"`
}

func (a *app) newInstallCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "install",
		Short: "Download the browser, and optionally a local model",
		Long: "Download the pinned Chrome for Testing build for this operating system and architecture into the user cache directory,\n" +
			"verify its size and SHA-256, unpack it and record the version. Run again, the command does nothing while the pinned build\n" +
			"is installed; --force installs it again. pagevow uses the installed build before any browser found on PATH.\n" +
			"The install refuses while the browser that pagevow start keeps running exists: run pagevow stop first.\n\n" +
			"Exit codes: 0 installed or already installed, 2 nothing was installed.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error { return a.runInstall(cmd) },
	}
	flags := cmd.Flags()
	flags.Bool("browser", false, "download the pinned Chrome for Testing build")
	flags.String("model", "", "local model to install: a path or a private Hugging Face repository (not implemented yet)")
	flags.Bool("force", false, "install the pinned build again even when it is installed")
	flags.Bool("json", false, "print the result as JSON")
	return cmd
}

func (a *app) runInstall(cmd *cobra.Command) error {
	flags := cmd.Flags()
	withBrowser, err := flags.GetBool("browser")
	if err != nil {
		return fmt.Errorf("read --browser: %w", err)
	}
	model, err := flags.GetString("model")
	if err != nil {
		return fmt.Errorf("read --model: %w", err)
	}
	force, err := flags.GetBool("force")
	if err != nil {
		return fmt.Errorf("read --force: %w", err)
	}
	asJSON, err := flags.GetBool("json")
	if err != nil {
		return fmt.Errorf("read --json: %w", err)
	}
	if model != "" {
		return infrastructure(errors.New("install --model: not implemented yet"))
	}
	if !withBrowser {
		return infrastructure(errors.New("nothing to install: pass --browser"))
	}
	return a.installBrowser(cmd, force, asJSON)
}

func (a *app) installBrowser(cmd *cobra.Command, force, asJSON bool) error {
	setup, err := a.installSetup(force)
	if err != nil {
		return err
	}
	out, err := a.printer(cmd.OutOrStdout())
	if err != nil {
		return err
	}
	if !asJSON {
		setup.opts.Progress = downloadProgress(out, setup.pin)
	}
	installed, err := browser.Install(commandContext(cmd), setup.opts)
	if err != nil {
		var exit *ExitError
		if errors.As(err, &exit) {
			return err
		}
		return infrastructure(err)
	}
	report := installReport{
		Version: installed.Version, Platform: installed.Platform,
		Executable: installed.Executable, AlreadyInstalled: installed.AlreadyInstalled,
	}
	if asJSON {
		return writeJSON(cmd, report)
	}
	if installed.AlreadyInstalled {
		out.Status(ui.OK, "Chrome for Testing %s is already installed at %s", installed.Version, installed.Executable)
	} else {
		out.Status(ui.OK, "installed Chrome for Testing %s at %s", installed.Version, installed.Executable)
	}
	return out.Err()
}

type installPlan struct {
	opts browser.InstallOptions
	pin  browser.Pin
}

func (a *app) installSetup(force bool) (installPlan, error) {
	cacheDir, err := service[CacheDir](a)
	if err != nil {
		return installPlan{}, err
	}
	dir, err := config.BrowserDir(cacheDir)
	if err != nil {
		return installPlan{}, infrastructure(err)
	}
	goos, err := service[GOOS](a)
	if err != nil {
		return installPlan{}, err
	}
	arch, err := service[GOARCH](a)
	if err != nil {
		return installPlan{}, err
	}
	client, err := service[*http.Client](a)
	if err != nil {
		return installPlan{}, err
	}
	baseURL, err := service[BrowserBaseURL](a)
	if err != nil {
		return installPlan{}, err
	}
	override, err := service[browserPinOverride](a)
	if err != nil {
		return installPlan{}, err
	}
	clock, err := service[Clock](a)
	if err != nil {
		return installPlan{}, err
	}
	guard, err := a.browserRunningGuard()
	if err != nil {
		return installPlan{}, err
	}
	pin := override.pin
	if pin == nil {
		found, err := browser.PinFor(string(goos), string(arch))
		if err != nil {
			return installPlan{}, infrastructure(err)
		}
		pin = &found
	}
	return installPlan{pin: *pin, opts: browser.InstallOptions{
		BrowserDir: dir, GOOS: string(goos), GOARCH: string(arch), BaseURL: string(baseURL), Client: client,
		Force: force, Guard: guard, Now: clock, Pin: pin,
	}}, nil
}

func (a *app) browserRunningGuard() (func(context.Context) error, error) {
	procs, err := service[Processes](a)
	if err != nil {
		return nil, err
	}
	return func(context.Context) error {
		records, err := procs.List()
		if err != nil {
			return infrastructure(fmt.Errorf("list the pagevow processes: %w", err))
		}
		for _, rec := range records {
			if rec.Kind == server.KindBrowser && procs.Alive(rec) {
				return infrastructure(fmt.Errorf("the browser that pagevow start keeps running (pid %d, port %d) would use files that the install replaces; run pagevow stop first", rec.PID, rec.Port))
			}
		}
		return nil
	}, nil
}

func downloadProgress(out *ui.Printer, pin browser.Pin) func(done, total int64) {
	last := 0
	return func(done, total int64) {
		if done == 0 {
			out.Status(ui.Info, "downloading Chrome for Testing %s for %s (%d MB)", browser.PinnedVersion, pin.Platform, (total+bytesPerMB/2)/bytesPerMB)
			return
		}
		if !out.Styled() || total <= 0 {
			return
		}
		if tenth := int(done * 10 / total); tenth > last {
			last = tenth
			out.Status(ui.Info, "downloaded %d%%", tenth*10)
		}
	}
}
