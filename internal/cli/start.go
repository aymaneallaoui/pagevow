package cli

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/aymaneallaoui/pagevow/internal/browser"
	"github.com/aymaneallaoui/pagevow/internal/config"
	"github.com/aymaneallaoui/pagevow/internal/server"
	"github.com/aymaneallaoui/pagevow/internal/ui"
)

const (
	readyInterval  = time.Second
	logTailLines   = 20
	stopFailBudget = 30 * time.Second

	actionStarted        = "started"
	actionAlreadyRunning = "already running"
	actionFailed         = "failed"
)

type startedProcess struct {
	Name         string  `json:"name"`
	Kind         string  `json:"kind"`
	PID          int     `json:"pid"`
	Port         int     `json:"port"`
	Action       string  `json:"action"`
	Ready        bool    `json:"ready"`
	ReadySeconds float64 `json:"ready_seconds"`
	Log          string  `json:"log"`
	Error        string  `json:"error,omitempty"`
	LogTail      string  `json:"log_tail,omitempty"`
}

type startReport struct {
	OK           bool             `json:"ok"`
	Processes    []startedProcess `json:"processes"`
	Warnings     []string         `json:"warnings"`
	StaleRemoved []string         `json:"stale_removed"`
	Problems     []string         `json:"problems"`
}

type startTarget struct {
	name     string
	kind     server.Kind
	port     int
	readyURL string
	timeout  time.Duration
	command  server.Command
	peakGiB  float64
	large    bool
}

type starter struct {
	ctx      context.Context
	cfg      config.Config
	procs    Processes
	gpu      GPUReader
	browsers ManagedBrowsers
	launcher BrowserLauncher
	home     HomeDir
	lookPath LookPath
	now      Clock
	platform server.Platform
	out      *ui.Printer
	logDir   string
	profile  string
	execPath string
	noBrowse bool
	report   startReport
}

func (a *app) newStartCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "start",
		Short: "Start what the active backend needs, and the browser",
		Long: "Start the local model server or servers of the active backend, the local text helper when it is enabled, and a browser that stays running.\n" +
			"A process that already runs is left alone. Logs are written to <user cache directory>/pagevow/logs/.\n\n" +
			"Exit codes: 0 everything requested runs, 2 something did not start.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error { return a.runStart(cmd) },
	}
	cmd.Flags().Bool("no-browser", false, "do not start the browser")
	cmd.Flags().Bool("json", false, "print the result as JSON")
	return cmd
}

func (a *app) runStart(cmd *cobra.Command) error {
	noBrowser, err := cmd.Flags().GetBool("no-browser")
	if err != nil {
		return fmt.Errorf("read --no-browser: %w", err)
	}
	asJSON, err := cmd.Flags().GetBool("json")
	if err != nil {
		return fmt.Errorf("read --json: %w", err)
	}
	cfg, _, err := a.loadConfig()
	if err != nil {
		if asJSON {
			failed := startReport{Processes: []startedProcess{}, Warnings: []string{}, StaleRemoved: []string{}, Problems: []string{err.Error()}}
			if writeErr := writeJSON(cmd, failed); writeErr != nil {
				return writeErr
			}
		}
		return infrastructure(err)
	}
	s, err := a.newStarter(commandContext(cmd), cfg, noBrowser)
	if err != nil {
		return err
	}
	if !asJSON {
		if s.out, err = a.printer(cmd.OutOrStdout()); err != nil {
			return err
		}
	}
	failed := s.run()
	if asJSON {
		if err := writeJSON(cmd, s.report); err != nil {
			return err
		}
	} else if err := s.out.Err(); err != nil {
		return err
	}
	if failed {
		return infrastructure(errors.New(s.failureSummary()))
	}
	return nil
}

func (a *app) newStarter(ctx context.Context, cfg config.Config, noBrowser bool) (*starter, error) {
	s := &starter{ctx: ctx, cfg: cfg, noBrowse: noBrowser}
	s.report = startReport{Processes: []startedProcess{}, Warnings: []string{}, StaleRemoved: []string{}, Problems: []string{}}
	var err error
	if s.procs, err = service[Processes](a); err != nil {
		return nil, err
	}
	if s.gpu, err = service[GPUReader](a); err != nil {
		return nil, err
	}
	if s.browsers, err = service[ManagedBrowsers](a); err != nil {
		return nil, err
	}
	if s.launcher, err = service[BrowserLauncher](a); err != nil {
		return nil, err
	}
	if s.home, err = service[HomeDir](a); err != nil {
		return nil, err
	}
	if s.lookPath, err = service[LookPath](a); err != nil {
		return nil, err
	}
	if s.now, err = service[Clock](a); err != nil {
		return nil, err
	}
	if s.platform, err = a.platformOf(); err != nil {
		return nil, err
	}
	cache, err := service[CacheDir](a)
	if err != nil {
		return nil, err
	}
	if s.logDir, err = config.LogDir(cache); err != nil {
		return nil, infrastructure(err)
	}
	if s.profile, err = config.ManagedProfileDir(cache); err != nil {
		return nil, infrastructure(err)
	}
	return s, nil
}

func (s *starter) info(format string, args ...any) {
	if s.out != nil {
		s.out.Status(ui.Info, format, args...)
	}
}

func (s *starter) warn(format string, args ...any) {
	message := fmt.Sprintf(format, args...)
	s.report.Warnings = append(s.report.Warnings, message)
	if s.out != nil {
		s.out.Status(ui.Warn, "%s", message)
	}
}

func (s *starter) failureSummary() string {
	if len(s.report.Problems) > 0 {
		return fmt.Sprintf("nothing was started: %d problem(s) to fix", len(s.report.Problems))
	}
	failed := 0
	for _, p := range s.report.Processes {
		if p.Action == actionFailed {
			failed++
		}
	}
	return fmt.Sprintf("%d process(es) did not start", failed)
}

// run starts what is missing and reports whether anything requested is not running.
func (s *starter) run() bool {
	live, orphaned := s.removeStale()
	targets, unsupported := s.plan()
	for _, entry := range unsupported {
		s.finish(entry)
	}
	fresh := make([]startTarget, 0, len(targets))
	for _, t := range targets {
		if _, running := live[t.name]; !running {
			fresh = append(fresh, t)
		}
	}
	if problems := s.preflight(fresh, orphaned); len(problems) > 0 {
		s.report.Problems = problems
		for _, problem := range problems {
			if s.out != nil {
				s.out.Status(ui.Fail, "%s", problem)
			}
		}
		return true
	}
	supervisedFailed := false
	for _, t := range targets {
		entry := s.startOne(t, live, supervisedFailed)
		supervisedFailed = supervisedFailed || (entry.Action == actionFailed && supervised(t.kind))
		s.finish(entry)
	}
	s.report.OK = !slices.ContainsFunc(s.report.Processes, func(p startedProcess) bool { return p.Action == actionFailed })
	if len(s.report.Processes) == 0 {
		s.info("nothing to start: the active backend needs no local process and --no-browser was given")
	}
	return !s.report.OK
}

func (s *starter) removeStale() (live map[string]server.Record, orphaned []server.Record) {
	swept := sweepRecords(s.ctx, s.procs)
	if swept.ListErr != nil {
		s.warn("some process records could not be read: %v", swept.ListErr)
	}
	for _, failure := range swept.Failed {
		s.warn("could not remove the stale record %s: %v", failure.Name, failure.Err)
	}
	for _, rec := range swept.Gone {
		s.report.StaleRemoved = append(s.report.StaleRemoved, rec.Name)
		if s.out != nil {
			s.out.Status(ui.Info, "removed stale record %s: its process is gone", rec.Name)
		}
	}
	live = map[string]server.Record{}
	for _, rec := range swept.Live {
		live[rec.Name] = rec
	}
	return live, swept.Orphaned
}

// plan turns the configuration into the processes to run, largest model first; unsupported ones come back as failed entries.
func (s *starter) plan() ([]startTarget, []startedProcess) {
	var targets []startTarget
	var unsupported []startedProcess
	problems := &s.report.Problems

	legs, err := modelLegs(s.cfg)
	if err != nil {
		*problems = append(*problems, err.Error())
	}
	kevDir, kevErr := kevDirOf(s.cfg, s.home)
	if kevErr != nil && len(legs) > 0 && s.platform.LocalServing() {
		*problems = append(*problems, kevErr.Error())
	}
	for _, leg := range legs {
		entry := startedProcess{Name: leg.recordName(), Kind: string(server.KindModel), Port: leg.port, Action: actionFailed, Log: logPathOf(s.logDir, leg.recordName())}
		if !s.platform.LocalServing() {
			entry.Error = localServingUnsupported
			unsupported = append(unsupported, entry)
			continue
		}
		if kevErr != nil {
			continue
		}
		command, err := server.ModelCommand(s.platform, kevDir, leg.model, leg.mode, leg.port)
		if err != nil {
			*problems = append(*problems, fmt.Sprintf("%s: %v", leg.label, err))
			continue
		}
		ready, err := leg.readyURL()
		if err != nil {
			*problems = append(*problems, fmt.Sprintf("%s: %v", leg.label, err))
			continue
		}
		peak, large := leg.memory(s.platform, kevDir)
		targets = append(targets, startTarget{
			name: leg.recordName(), kind: server.KindModel, port: leg.port, readyURL: ready, command: command,
			timeout: time.Duration(s.cfg.Server.StartTimeoutSeconds) * time.Second, peakGiB: peak, large: large,
		})
	}
	// Largest peak first, so the memory check fails on the biggest model before anything starts.
	slices.SortStableFunc(targets, func(a, b startTarget) int { return cmp.Compare(b.peakGiB, a.peakGiB) })

	helper, err := localTextHelperOf(s.cfg)
	if err != nil {
		*problems = append(*problems, err.Error())
	}
	if helper != nil {
		targets, unsupported = s.planHelper(helper, targets, unsupported)
	}
	if !s.noBrowse {
		port := s.cfg.Browser.Port
		targets = append(targets, startTarget{
			name: server.RecordName(server.KindBrowser, port), kind: server.KindBrowser, port: port, readyURL: browser.DebugURL(port),
		})
	}
	return targets, unsupported
}

func (s *starter) planHelper(helper *localTextHelper, targets []startTarget, unsupported []startedProcess) ([]startTarget, []startedProcess) {
	if s.platform.OS == "windows" {
		return targets, append(unsupported, startedProcess{
			Name: helper.recordName(), Kind: string(server.KindTextHelper), Port: helper.port, Action: actionFailed,
			Error: "the local text helper cannot be started on Windows; run llama-server yourself and set text_helper.url",
		})
	}
	local := s.cfg.TextHelper.Local
	command, err := server.TextHelperCommand(local.Repo, local.File, local.Alias, helper.port, local.GPULayers)
	if err != nil {
		s.report.Problems = append(s.report.Problems, err.Error())
		return targets, unsupported
	}
	target := startTarget{
		name: helper.recordName(), kind: server.KindTextHelper, port: helper.port, readyURL: helper.readyURL(), command: command,
		timeout: time.Duration(local.StartTimeoutSeconds) * time.Second, peakGiB: server.TextHelperPeak(local.GPULayers),
	}
	return append(targets, target), unsupported
}

// preflight collects every reason not to launch; a target whose name or port an orphan holds is refused, since only stop signals an orphan.
func (s *starter) preflight(fresh []startTarget, orphaned []server.Record) []string {
	problems := slices.Clone(s.report.Problems)
	for _, rec := range orphaned {
		if !slices.ContainsFunc(fresh, func(t startTarget) bool { return t.name == rec.Name || t.port == rec.Port }) {
			s.warn("%s; it holds port %d and memory until pagevow stop ends it", orphanText(rec.Name, rec.PID, rec.ChildPID), rec.Port)
		}
	}
	var peaks []float64
	large := false
	for _, t := range fresh {
		if supervised(t.kind) {
			if _, err := s.lookPath(t.command.Argv[0]); err != nil {
				problems = append(problems, fmt.Sprintf("%s was not found on PATH: %s", t.command.Argv[0], installHint(t.command.Argv[0])))
			}
		}
		orphan := slices.IndexFunc(orphaned, func(rec server.Record) bool { return rec.Name == t.name || rec.Port == t.port })
		switch {
		case orphan >= 0:
			rec := orphaned[orphan]
			problems = append(problems, fmt.Sprintf("%s; run pagevow stop first (%s, port %d)", orphanText(rec.Name, rec.PID, rec.ChildPID), t.name, t.port))
		case s.procs.PortInUse(s.ctx, t.port):
			problems = append(problems, fmt.Sprintf("port %d is in use by a process that pagevow did not start; stop it or change the port in the config (%s)", t.port, t.name))
		}
		if t.peakGiB > 0 {
			peaks = append(peaks, t.peakGiB)
		}
		large = large || t.large
		if t.kind == server.KindBrowser && s.execPath == "" {
			path, err := s.launcher.Find()
			if err != nil {
				problems = append(problems, browserMissing)
				continue
			}
			s.execPath = path
		}
	}
	return append(problems, s.gpuProblems(peaks, large)...)
}

func installHint(program string) string {
	if program == "uv" {
		return "install uv from https://docs.astral.sh/uv/"
	}
	return "install " + program + " and make sure it is on your PATH"
}

func (s *starter) gpuProblems(peaks []float64, large bool) []string {
	if len(peaks) == 0 {
		return nil
	}
	reading, err := s.gpu.Read(s.ctx)
	switch {
	case err != nil && s.platform.MLX():
		return []string{fmt.Sprintf("the memory reader (sysctl) failed: %v; the model was not started because memory could not be checked; run: sysctl hw.memsize vm.page_free_count", err)}
	case errors.Is(err, server.ErrNoGPUTool):
		s.warn("nvidia-smi was not found, so free GPU memory was not checked")
		return nil
	case err != nil:
		s.warn("free GPU memory could not be read (%v), so it was not checked", err)
		return nil
	}
	if large {
		if err := server.FitsFloor(reading); err != nil {
			return []string{err.Error() + "; " + floorFix}
		}
	}
	if err := server.Fits(reading, peaks...); err != nil {
		return []string{err.Error() + "; " + fitsFix(s.platform)}
	}
	return nil
}

const floorFix = "choose a model of 1B or less, or use backend jev or custom"

func fitsFix(p server.Platform) string {
	if p.MLX() {
		return "quit other programs to free memory, or choose a model of 1B or less"
	}
	return "stop other GPU programs or choose a smaller mode with: pagevow use local --mode nf4"
}

func (s *starter) startOne(t startTarget, live map[string]server.Record, supervisedFailed bool) startedProcess {
	if rec, running := live[t.name]; running {
		return s.reuse(t, rec)
	}
	if s.ctx.Err() != nil {
		return startedProcess{Name: t.name, Kind: string(t.kind), Port: t.port, Action: actionFailed, Log: logPathOf(s.logDir, t.name), Error: "interrupted before it was started"}
	}
	if supervised(t.kind) && supervisedFailed {
		return startedProcess{Name: t.name, Kind: string(t.kind), Port: t.port, Action: actionFailed, Log: logPathOf(s.logDir, t.name),
			Error: "not started because an earlier process failed to start"}
	}
	if t.kind == server.KindBrowser {
		return s.launchBrowser(t)
	}
	return s.launchSupervised(t)
}

func (s *starter) reuse(t startTarget, rec server.Record) startedProcess {
	entry := startedProcess{Name: t.name, Kind: string(t.kind), PID: rec.PID, Port: t.port, Log: rec.Log}
	if s.answers(t) {
		entry.Action, entry.Ready = actionAlreadyRunning, true
		return entry
	}
	entry.Action = actionFailed
	if t.kind == server.KindBrowser {
		entry.Error = fmt.Sprintf("the browser (pid %d) is running but its debugging endpoint does not answer; run pagevow stop, then pagevow start", rec.PID)
		return entry
	}
	s.info("%s is running but does not answer yet; waiting up to %d seconds", t.name, int(t.timeout.Seconds()))
	if err := s.procs.WaitReady(s.ctx, rec, readyInterval, t.timeout); err != nil {
		entry.Error = readyFailure(t, err)
		entry.LogTail = s.logTail(rec.Log)
		return entry
	}
	entry.Action, entry.Ready = actionAlreadyRunning, true
	return entry
}

func (s *starter) answers(t startTarget) bool {
	if t.kind == server.KindBrowser {
		_, err := s.browsers.Version(s.ctx, t.readyURL)
		return err == nil
	}
	return answered(s.procs.Probe(s.ctx, t.readyURL))
}

func (s *starter) launchSupervised(t startTarget) startedProcess {
	log := logPathOf(s.logDir, t.name)
	entry := startedProcess{Name: t.name, Kind: string(t.kind), Port: t.port, Action: actionFailed, Log: log}
	if err := s.procs.ClearTripped(t.name); err != nil {
		s.warn("could not clear the guard message of %s: %v", t.name, err)
	}
	s.info("starting %s; waiting up to %d seconds for %s (log: %s)", t.name, int(t.timeout.Seconds()), t.readyURL, log)
	begun := s.now()
	rec, err := s.procs.Spawn(s.ctx, server.Spec{
		Name: t.name, Kind: t.kind, Argv: t.command.Argv, Dir: t.command.Dir, Env: t.command.Env,
		Port: t.port, ReadyURL: t.readyURL, Log: log, Guard: guardOf(s.cfg),
	})
	if err != nil {
		entry.Error = err.Error()
		return entry
	}
	entry.PID = rec.PID
	if err := s.procs.WaitReady(s.ctx, rec, readyInterval, t.timeout); err != nil {
		entry.Error = readyFailure(t, err)
		entry.LogTail = s.logTail(log)
		s.stopQuietly(rec)
		return entry
	}
	entry.Action, entry.Ready = actionStarted, true
	entry.ReadySeconds = s.now().Sub(begun).Seconds()
	return entry
}

func (s *starter) launchBrowser(t startTarget) startedProcess {
	log := logPathOf(s.logDir, t.name)
	entry := startedProcess{Name: t.name, Kind: string(t.kind), Port: t.port, Action: actionFailed, Log: log}
	if s.execPath == "" {
		path, err := s.launcher.Find()
		if err != nil {
			entry.Error = browserMissing
			return entry
		}
		s.execPath = path
	}
	s.info("starting %s (log: %s)", t.name, log)
	process, err := s.browsers.LaunchDetached(s.ctx, ManagedBrowserSpec{
		ExecPath: s.execPath, Port: t.port, Headless: s.cfg.Browser.Headless, Viewport: s.cfg.Browser.Viewport,
		ProfileDir: s.profile, LogPath: log,
	})
	if err != nil {
		entry.Error = err.Error()
		return entry
	}
	entry.PID = process.PID
	rec, err := s.procs.Track(server.Record{
		Name: t.name, Kind: server.KindBrowser, PID: process.PID, Port: t.port, Command: []string{s.execPath},
		StartedAt: s.now().UTC(), Log: log, ReadyURL: process.DebugURL + "/json/version", ProfileDir: s.profile,
	})
	if err != nil {
		entry.Error = fmt.Sprintf("the browser started but its record could not be written: %v", err)
		s.stopQuietly(rec)
		return entry
	}
	entry.Action, entry.Ready = actionStarted, true
	return entry
}

func supervised(kind server.Kind) bool {
	return kind == server.KindModel || kind == server.KindTextHelper
}

func readyFailure(t startTarget, err error) string {
	switch {
	case errors.Is(err, server.ErrProcessEnded):
		return "the process ended before it answered"
	case errors.Is(err, context.Canceled):
		return "interrupted while waiting for it to answer"
	case errors.Is(err, context.DeadlineExceeded):
		return fmt.Sprintf("did not answer at %s within %d seconds", t.readyURL, int(t.timeout.Seconds()))
	}
	return err.Error()
}

func (s *starter) logTail(path string) string {
	tail, err := server.LogTail(path, logTailLines)
	if err != nil {
		return ""
	}
	return tail
}

func (s *starter) stopQuietly(rec server.Record) {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(s.ctx), stopFailBudget)
	defer cancel()
	if _, err := s.procs.Stop(ctx, rec); err != nil {
		s.warn("could not stop %s after it failed to start: %v", rec.Name, err)
	}
}

func (s *starter) finish(entry startedProcess) {
	s.report.Processes = append(s.report.Processes, entry)
	if s.out == nil {
		return
	}
	switch entry.Action {
	case actionStarted:
		s.out.Status(ui.OK, "%s started (pid %d, port %d, ready in %.0f s); log: %s", entry.Name, entry.PID, entry.Port, entry.ReadySeconds, entry.Log)
	case actionAlreadyRunning:
		s.out.Status(ui.OK, "%s already running (pid %d, port %d); log: %s", entry.Name, entry.PID, entry.Port, entry.Log)
	default:
		s.out.Status(ui.Fail, "%s: %s", entry.Name, entry.Error)
		if entry.LogTail != "" {
			s.out.Line("%s", indent(entry.LogTail))
		}
		if entry.Log != "" {
			s.out.Line("  log: %s", entry.Log)
		}
	}
}

func indent(text string) string {
	return "  | " + strings.ReplaceAll(text, "\n", "\n  | ")
}
