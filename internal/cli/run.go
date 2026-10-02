package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"github.com/aymaneallaoui/pagevow/internal/browser"
	"github.com/aymaneallaoui/pagevow/internal/config"
	"github.com/aymaneallaoui/pagevow/internal/runner"
	"github.com/aymaneallaoui/pagevow/internal/server"
	"github.com/aymaneallaoui/pagevow/internal/testsfile"
	"github.com/aymaneallaoui/pagevow/internal/ui"
)

const (
	stopBrowserBudget = 20 * time.Second
	exitInterrupted   = 130
	outDirName        = ".pagevow"
)

const browserMissing = "No Chromium or Google Chrome was found.\n  Install one with: pagevow install --browser, or put Chromium or Google Chrome on your PATH."

func (a *app) newRunCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "run",
		Short: "Run the browser tests",
		Long: "Run the tests of a tests file: a decision model drives a private browser step by step (headless unless browser.headless is false or --headed is given),\n" +
			"the verifier of each test checks the final page, and every test leaves screenshots in\n" +
			"<out>/<timestamp>/.\n\nExit codes: 0 every test passed, 1 a test failed or has no verifier, 2 the run could not start.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error { return a.runTests(cmd) },
	}
	flags := cmd.Flags()
	flags.String("tests", "", "tests file (default: pagevow.yaml in the current directory)")
	flags.StringSlice("ids", nil, "run only these test ids, comma separated")
	flags.String("out", "", "output directory (default: .pagevow next to the tests file)")
	flags.String("screenshots", "", "screenshot policy: final, failed or all (default: run.screenshots)")
	flags.Int("retries", 0, "fresh reruns of a failed test (default: run.retries)")
	flags.Int("timeout", 0, "seconds per test attempt (default: run.timeout_seconds)")
	flags.Bool("headed", false, "show the browser window (default: browser.headless)")
	flags.Bool("full-page", false, "capture final.png beyond the viewport")
	flags.Bool("json", false, "print the report as JSON on stdout")
	return cmd
}

type runPlan struct {
	cfg       config.Config
	testsPath string
	outDir    string
	tests     []testsfile.Test
	warnings  []string
	asJSON    bool
	fullPage  bool
	headed    bool
}

func infrastructure(err error) error {
	return &ExitError{Code: ExitInfrastructure, Err: err}
}

func (a *app) runTests(cmd *cobra.Command) error {
	ctx, cancel := context.WithCancel(commandContext(cmd))
	defer cancel()
	stderr := cmd.ErrOrStderr()

	plan, err := a.planRun(cmd)
	if err != nil {
		return infrastructure(err)
	}
	notes, err := a.printer(stderr)
	if err != nil {
		return err
	}
	for _, warning := range plan.warnings {
		notes.Status(ui.Warn, "%s", warning)
	}
	resolver, err := a.resolver()
	if err != nil {
		return err
	}
	if services := paidServices(plan.cfg, resolver); len(services) > 0 {
		notes.Status(ui.Warn, "this run uses a paid service: %s; it may bill per request", strings.Join(services, " and "))
	}
	if err := notes.Err(); err != nil {
		return err
	}

	launcher, err := service[BrowserLauncher](a)
	if err != nil {
		return err
	}
	collab := buildCollaborators(plan.cfg, resolver)
	problems := collab.preflight(ctx)
	managedURL := a.managedBrowserURL(ctx, plan.cfg.Browser.Port)
	if plan.headed && managedURL != "" {
		notes.Status(ui.Info, "--headed: starting a private browser with a window instead of using the browser that pagevow start keeps running")
		managedURL = ""
	}
	var execPath string
	if managedURL == "" {
		var findErr error
		if execPath, findErr = launcher.Find(); findErr != nil {
			problems = append(problems, browserMissing)
		}
	}
	if ctx.Err() != nil {
		return &ExitError{Code: ExitFailure, Err: errors.New("interrupted before the run started")}
	}
	if len(problems) > 0 {
		_, _ = fmt.Fprintf(stderr, "Preflight failed; no test was run.\n\n%s\n", joinProblems(problems))
		return infrastructure(fmt.Errorf("preflight failed: %d problem(s) to fix", len(problems)))
	}

	kill := &killSwitch{}
	stopWatching, err := a.watchInterrupts(cancel, kill)
	if err != nil {
		return err
	}
	defer stopWatching()

	handle, err := a.openBrowser(ctx, notes, launcher, managedURL, execPath, plan.cfg)
	if err != nil {
		return infrastructure(fmt.Errorf("start browser: %w", err))
	}
	kill.set(handle)
	defer stopBrowser(stderr, handle)

	return a.execute(ctx, cmd, plan, collab, handle)
}

func (a *app) openBrowser(ctx context.Context, notes *ui.Printer, launcher BrowserLauncher, managedURL, execPath string, cfg config.Config) (RunBrowser, error) {
	if managedURL == "" {
		return launcher.Launch(ctx, BrowserSpec{ExecPath: execPath, Viewport: cfg.Browser.Viewport, Headless: cfg.Browser.Headless})
	}
	browsers, err := service[ManagedBrowsers](a)
	if err != nil {
		return nil, err
	}
	handle, err := browsers.Attach(ctx, managedURL, cfg.Browser.Viewport)
	if err != nil {
		return nil, err
	}
	notes.Status(ui.Info, "using the browser that pagevow start keeps running (%s)", managedURL)
	return handle, notes.Err()
}

// managedBrowserURL returns the debugging URL of the browser pagevow start keeps running, or "" when there is none that answers.
func (a *app) managedBrowserURL(ctx context.Context, port int) string {
	procs, err := service[Processes](a)
	if err != nil {
		return ""
	}
	browsers, err := service[ManagedBrowsers](a)
	if err != nil {
		return ""
	}
	records, _ := procs.List()
	for _, rec := range records {
		if rec.Name != server.RecordName(server.KindBrowser, port) || procs.State(rec) != server.StateRunning {
			continue
		}
		debugURL := browser.DebugURL(rec.Port)
		if _, err := browsers.Version(ctx, debugURL); err == nil {
			return debugURL
		}
	}
	return ""
}

func commandContext(cmd *cobra.Command) context.Context {
	if ctx := cmd.Context(); ctx != nil {
		return ctx
	}
	return context.Background()
}

func (a *app) execute(ctx context.Context, cmd *cobra.Command, plan runPlan, collab *collaborators, handle RunBrowser) error {
	now, err := service[Clock](a)
	if err != nil {
		return err
	}
	run, err := runner.New(runner.Options{
		TestsFile:   plan.testsPath,
		OutDir:      plan.outDir,
		Screenshots: runner.ScreenshotPolicy(plan.cfg.Run.Screenshots),
		Retries:     plan.cfg.Run.Retries,
		Timeout:     time.Duration(plan.cfg.Run.TimeoutSeconds) * time.Second,
		FullPage:    plan.fullPage,
		MaxSteps:    plan.cfg.Run.MaxSteps,
		Guards: runner.Guards{
			LoopGuard:      plan.cfg.Guards.LoopGuard,
			DoneMinConf:    plan.cfg.Guards.DoneMinConf,
			BlockedMinConf: plan.cfg.Guards.BlockedMinConf,
		},
		VetoCache: collab.vetoCache,
		Secrets:   collab.secrets,
	}, runner.Deps{Sessions: handle, Decider: collab.decider, Text: collab.text, Clock: now})
	if err != nil {
		return infrastructure(err)
	}
	report, runErr := run.Run(ctx, plan.tests)
	if report.RunDir != "" {
		if err := a.renderReport(cmd, report, plan.asJSON); err != nil {
			return err
		}
	}
	if runErr != nil {
		return infrastructure(runErr)
	}
	if runner.ExitCode(report, nil) == runner.ExitPassed {
		return nil
	}
	if failures := report.Failures(); failures != "" {
		_, _ = fmt.Fprintln(cmd.ErrOrStderr(), failures)
	}
	return &ExitError{Code: ExitFailure, Err: failureSummary(report)}
}

func failureSummary(report runner.Report) error {
	if report.Interrupted {
		return fmt.Errorf("interrupted after %d of the tests; the report so far is in %s", report.Totals.Tests, report.RunDir)
	}
	return fmt.Errorf("%d of %d tests did not pass", report.Totals.Tests-report.Totals.Passed, report.Totals.Tests)
}

func (a *app) renderReport(cmd *cobra.Command, report runner.Report, asJSON bool) error {
	if asJSON {
		data, err := report.JSON()
		if err != nil {
			return fmt.Errorf("render report: %w", err)
		}
		_, err = fmt.Fprintln(cmd.OutOrStdout(), string(data))
		return err
	}
	out, err := a.printer(cmd.OutOrStdout())
	if err != nil {
		return err
	}
	if !out.Styled() {
		out.Line("%s", report.Text())
		return out.Err()
	}
	for _, test := range report.Tests {
		out.Status(outcomeLevel(test.Outcome), "%s", test.SummaryLine())
		if missing := test.MissingLine(); missing != "" {
			out.Line("%s", missing)
		}
		for _, warning := range test.WarningLines() {
			out.Line("%s", warning)
		}
	}
	out.Blank()
	out.Line("%s", report.TotalsText())
	if failures := report.Failures(); failures != "" {
		out.Blank()
		out.Line("%s", failures)
	}
	return out.Err()
}

func outcomeLevel(outcome string) ui.Level {
	switch outcome {
	case runner.OutcomePass:
		return ui.OK
	case runner.OutcomeUnverified:
		return ui.Warn
	}
	return ui.Fail
}

func (a *app) planRun(cmd *cobra.Command) (runPlan, error) {
	flags := cmd.Flags()
	cfg, _, err := a.loadConfigWithFlags(map[string]*pflag.Flag{
		"run.retries":         flags.Lookup("retries"),
		"run.timeout_seconds": flags.Lookup("timeout"),
		"run.screenshots":     flags.Lookup("screenshots"),
	})
	if err != nil {
		return runPlan{}, err
	}
	plan := runPlan{cfg: cfg}
	if plan.asJSON, err = flags.GetBool("json"); err != nil {
		return runPlan{}, fmt.Errorf("read --json: %w", err)
	}
	if plan.fullPage, err = flags.GetBool("full-page"); err != nil {
		return runPlan{}, fmt.Errorf("read --full-page: %w", err)
	}
	headed, err := flags.GetBool("headed")
	if err != nil {
		return runPlan{}, fmt.Errorf("read --headed: %w", err)
	}
	plan.headed = headed
	if headed {
		plan.cfg.Browser.Headless = false
	}
	if plan.testsPath, err = testsPathOf(cmd); err != nil {
		return runPlan{}, err
	}
	now, err := service[Clock](a)
	if err != nil {
		return runPlan{}, err
	}
	tests, warnings, err := testsfile.Load(plan.testsPath, now())
	if err != nil {
		return runPlan{}, err
	}
	ids, err := flags.GetStringSlice("ids")
	if err != nil {
		return runPlan{}, fmt.Errorf("read --ids: %w", err)
	}
	if plan.tests, err = testsfile.Filter(tests, testsfile.ParseIDs(strings.Join(ids, ","))); err != nil {
		return runPlan{}, fmt.Errorf("select tests from %s: %w", plan.testsPath, err)
	}
	if len(plan.tests) == 0 {
		return runPlan{}, fmt.Errorf("no tests to run in %s", plan.testsPath)
	}
	plan.warnings = warnings
	plan.outDir, err = flags.GetString("out")
	if err != nil {
		return runPlan{}, fmt.Errorf("read --out: %w", err)
	}
	if plan.outDir == "" {
		plan.outDir = filepath.Join(filepath.Dir(plan.testsPath), outDirName)
	}
	return plan, nil
}

func testsPathOf(cmd *cobra.Command) (string, error) {
	given, err := cmd.Flags().GetString("tests")
	if err != nil {
		return "", fmt.Errorf("read --tests: %w", err)
	}
	if given != "" {
		return given, nil
	}
	dir, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("find the current directory: %w", err)
	}
	path, err := testsfile.Find(dir)
	if err != nil {
		return "", fmt.Errorf("%w\n  Create one with: pagevow init", err)
	}
	return path, nil
}

func stopBrowser(stderr io.Writer, handle RunBrowser) {
	ctx, cancel := context.WithTimeout(context.Background(), stopBrowserBudget)
	defer cancel()
	if err := handle.Stop(ctx); err != nil {
		_, _ = fmt.Fprintf(stderr, "pagevow: warning: %v\n", err)
	}
}

type killSwitch struct {
	mu      sync.Mutex
	browser RunBrowser
}

func (k *killSwitch) set(b RunBrowser) {
	k.mu.Lock()
	defer k.mu.Unlock()
	k.browser = b
}

func (k *killSwitch) stop() {
	k.mu.Lock()
	b := k.browser
	k.mu.Unlock()
	if b == nil {
		return
	}
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	_ = b.Stop(cancelled)
}

// watchInterrupts cancels the run on the first interrupt and, on the second, kills the browser and exits at once.
func (a *app) watchInterrupts(cancel context.CancelFunc, kill *killSwitch) (func(), error) {
	interrupts, err := service[Interrupts](a)
	if err != nil {
		return nil, err
	}
	exit, err := service[Exit](a)
	if err != nil {
		return nil, err
	}
	signals, release := interrupts()
	done := make(chan struct{})
	finished := make(chan struct{})
	go func() {
		defer close(finished)
		for count := 0; ; {
			select {
			case <-signals:
				count++
				cancel()
				if count > 1 {
					kill.stop()
					exit(exitInterrupted)
					return
				}
			case <-done:
				return
			}
		}
	}()
	return func() {
		close(done)
		release()
		<-finished
	}, nil
}
