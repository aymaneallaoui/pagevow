package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/spf13/cobra"

	"github.com/aymaneallaoui/pagevow/internal/browser"
	"github.com/aymaneallaoui/pagevow/internal/config"
	"github.com/aymaneallaoui/pagevow/internal/keys"
	"github.com/aymaneallaoui/pagevow/internal/server"
	"github.com/aymaneallaoui/pagevow/internal/ui"
)

const (
	levelOK   = "ok"
	levelWarn = "warn"
	levelFail = "fail"
)

const browserInstallFix = "install one with: pagevow install --browser, or put Chromium or Google Chrome on your PATH"

type doctorCheck struct {
	ID      string `json:"id"`
	Level   string `json:"level"`
	Finding string `json:"finding"`
	Fix     string `json:"fix,omitempty"`
}

type doctorReport struct {
	OK     bool          `json:"ok"`
	Checks []doctorCheck `json:"checks"`
}

type doctor struct {
	ctx      context.Context
	procs    Processes
	gpu      GPUReader
	launcher BrowserLauncher
	resolver *keys.Resolver
	lookPath LookPath
	home     HomeDir
	goos     GOOS
	goarch   GOARCH
	platform server.Platform
	cache    CacheDir
	cfg      config.Config
	checks   []doctorCheck
	live     map[string]server.Record
}

func (a *app) newDoctorCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "doctor",
		Short: "Check the setup and say how to fix each problem",
		Long: "Check the config file, the keys of the active backend, the local model setup, the browser, the text helper and the process records.\n" +
			"Every check is ok, warn or fail and comes with the way to fix it. Stale records are removed.\n\n" +
			"Exit codes: 0 no check failed (warnings do not count), 1 at least one check failed.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error { return a.runDoctor(cmd) },
	}
	cmd.Flags().Bool("json", false, "print the checks as JSON")
	return cmd
}

func (a *app) runDoctor(cmd *cobra.Command) error {
	asJSON, err := cmd.Flags().GetBool("json")
	if err != nil {
		return fmt.Errorf("read --json: %w", err)
	}
	d, err := a.newDoctor(commandContext(cmd))
	if err != nil {
		return err
	}
	cfg, path, loadErr := a.loadConfig()
	d.run(cfg, path, loadErr)

	report := doctorReport{OK: true, Checks: d.checks}
	failed := 0
	for _, c := range d.checks {
		if c.Level == levelFail {
			failed++
			report.OK = false
		}
	}
	if asJSON {
		if err := writeJSON(cmd, report); err != nil {
			return err
		}
	} else if err := a.renderDoctor(cmd, report, failed); err != nil {
		return err
	}
	if failed > 0 {
		return &ExitError{Code: ExitFailure, Err: fmt.Errorf("%d check(s) failed", failed)}
	}
	return nil
}

func (a *app) renderDoctor(cmd *cobra.Command, report doctorReport, failed int) error {
	out, err := a.printer(cmd.OutOrStdout())
	if err != nil {
		return err
	}
	for _, c := range report.Checks {
		out.Status(levelOf(c.Level), "%s", c.Finding)
		if c.Fix != "" {
			out.Line("    fix: %s", c.Fix)
		}
	}
	out.Blank()
	if failed == 0 {
		out.Status(ui.OK, "no check failed")
	} else {
		out.Status(ui.Fail, "%d check(s) failed", failed)
	}
	return out.Err()
}

func levelOf(level string) ui.Level {
	switch level {
	case levelOK:
		return ui.OK
	case levelWarn:
		return ui.Warn
	}
	return ui.Fail
}

func (a *app) newDoctor(ctx context.Context) (*doctor, error) {
	d := &doctor{ctx: ctx, live: map[string]server.Record{}}
	var err error
	if d.procs, err = service[Processes](a); err != nil {
		return nil, err
	}
	if d.gpu, err = service[GPUReader](a); err != nil {
		return nil, err
	}
	if d.launcher, err = service[BrowserLauncher](a); err != nil {
		return nil, err
	}
	if d.resolver, err = a.resolver(); err != nil {
		return nil, err
	}
	if d.lookPath, err = service[LookPath](a); err != nil {
		return nil, err
	}
	if d.home, err = service[HomeDir](a); err != nil {
		return nil, err
	}
	if d.goos, err = service[GOOS](a); err != nil {
		return nil, err
	}
	if d.goarch, err = service[GOARCH](a); err != nil {
		return nil, err
	}
	d.platform = server.Platform{OS: string(d.goos), Arch: string(d.goarch)}
	if d.cache, err = service[CacheDir](a); err != nil {
		return nil, err
	}
	return d, nil
}

func (d *doctor) add(id, level, fix, format string, args ...any) {
	d.checks = append(d.checks, doctorCheck{ID: id, Level: level, Finding: fmt.Sprintf(format, args...), Fix: fix})
}

func (d *doctor) run(cfg config.Config, path string, loadErr error) {
	d.records()
	recordChecks := d.checks
	d.checks = nil
	if loadErr != nil {
		d.add("config", levelFail, "edit "+path+" or remove the wrong entries, then run pagevow doctor again", "the config file does not load: %v", loadErr)
	} else {
		d.cfg = cfg
		d.add("config", levelOK, "", "config file %s loads and validates", path)
		d.keys()
		d.backend()
		d.localServing()
	}
	d.browserChecks(loadErr == nil)
	if loadErr == nil {
		d.textHelper()
	}
	d.checks = append(d.checks, recordChecks...)
	d.tripped()
	d.directories()
}

func (d *doctor) records() {
	swept := sweepRecords(d.ctx, d.procs)
	if swept.ListErr != nil {
		d.add("records", levelWarn, "remove the unreadable file from "+d.procs.StateDir(), "some process records could not be read: %v", swept.ListErr)
	}
	for _, rec := range swept.Live {
		d.live[rec.Name] = rec
	}
	for _, failure := range swept.Failed {
		d.add("records", levelFail, "remove "+filepath.Join(d.procs.StateDir(), failure.Name+".json")+" by hand", "the stale record %s could not be removed: %v", failure.Name, failure.Err)
	}
	for _, rec := range swept.Gone {
		d.add("records", levelWarn, "", "removed the stale record %s: its process is gone", rec.Name)
	}
	if len(swept.Failed) == 0 && len(swept.Gone) == 0 {
		d.add("records", levelOK, "", "no stale process records (%d running)", len(d.live))
	}
}

func (d *doctor) keys() {
	c := d.cfg
	type ref struct {
		field    string
		value    string
		required bool
	}
	var refs []ref
	switch c.Backend {
	case config.BackendJev:
		refs = append(refs, ref{"backends.jev.key", c.Backends.Jev.Key, true})
	case config.BackendCustom:
		refs = append(refs, ref{"backends.custom.key", c.Backends.Custom.Key, false})
	case config.BackendCascade:
		refs = append(refs, ref{"backends.cascade.primary_key", c.Backends.Cascade.PrimaryKey, false}, ref{"backends.cascade.verifier_key", c.Backends.Cascade.VerifierKey, false})
	default:
		d.add("keys", levelOK, "", "the local backend needs no key")
	}
	if c.TextHelper.URL != "" && !config.IsLoopbackURL(c.TextHelper.URL) {
		refs = append(refs, ref{"text_helper.key", c.TextHelper.Key, true})
	}
	for _, r := range refs {
		d.checkKey(r.field, r.value, r.required)
	}
}

func (d *doctor) checkKey(field, reference string, required bool) {
	id := "key:" + field
	if reference == "" {
		if required {
			d.add(id, levelFail, "store a key with: pagevow keys set NAME, then set "+field+" to keychain:NAME", "%s is empty", field)
			return
		}
		d.add(id, levelOK, "", "%s is empty: no key is sent", field)
		return
	}
	if _, err := d.resolver.Resolve(reference); err != nil {
		d.add(id, levelFail, keyFix(reference), "the key %s (%s) could not be read: %s", reference, field, keyProblem(err))
		return
	}
	d.add(id, levelOK, "", "the key %s (%s) resolves", reference, field)
}

func (d *doctor) backend() {
	for _, target := range healthTargets(d.cfg) {
		if target.name == "text_helper" {
			continue
		}
		id := "backend:" + target.name
		if target.url == "" {
			d.add(id, levelFail, "set it with: pagevow use "+d.cfg.Backend+" --url URL", "the URL of the %s is not set", target.label)
			continue
		}
		status, err := d.procs.Probe(d.ctx, target.probeURL)
		switch {
		case answered(status, err):
			d.add(id, levelOK, "", "the %s answers at %s (HTTP %d)", target.label, target.url, status)
		case target.startable:
			d.add(id, levelWarn, "start it with: pagevow start", "the %s does not answer at %s yet", target.label, target.url)
		default:
			d.add(id, levelFail, "check the URL and your network connection; pagevow status shows what is configured", "the %s does not answer at %s: %s", target.label, target.url, failureText(status, err))
		}
	}
}

func (d *doctor) localServing() {
	legs, err := modelLegs(d.cfg)
	if err != nil {
		d.add("local:config", levelFail, "give each model server its own URL with a port", "%v", err)
		return
	}
	if len(legs) == 0 {
		return
	}
	if !d.platform.LocalServing() {
		d.add("local:platform", levelFail, "run: pagevow use jev, or pagevow use custom --url URL", "%s", localServingUnsupported)
		return
	}
	if _, err := d.lookPath("uv"); err != nil {
		d.add("local:uv", levelFail, "install uv from https://docs.astral.sh/uv/", "uv was not found on PATH")
	} else {
		d.add("local:uv", levelOK, "", "uv is on PATH")
	}
	kevDir, err := kevDirOf(d.cfg, d.home)
	if err != nil {
		d.add("local:kev", levelFail, "set server.kev_dir to your kev checkout", "%v", err)
		return
	}
	if !d.kevCheckout(kevDir) {
		return
	}
	var peaks []float64
	large := false
	for _, leg := range legs {
		d.checkLeg(kevDir, leg, d.bf16Fix(legs, leg))
		if _, running := d.live[leg.recordName()]; !running {
			peak, legLarge := leg.memory(d.platform, kevDir)
			peaks = append(peaks, peak)
			large = large || legLarge
		}
	}
	if helper, _ := localTextHelperOf(d.cfg); helper != nil {
		if _, running := d.live[helper.recordName()]; !running {
			if peak := server.TextHelperPeak(d.cfg.TextHelper.Local.GPULayers); peak > 0 {
				peaks = append(peaks, peak)
			}
		}
	}
	d.gpuChecks(peaks, large)
}

func (d *doctor) kevCheckout(kevDir string) bool {
	if !fileExists(filepath.Join(kevDir, "kev", "serve.py")) {
		d.add("local:kev", levelFail, "set server.kev_dir to your kev checkout (it must contain kev/serve.py)", "server.kev_dir %s does not contain kev/serve.py", kevDir)
		return false
	}
	d.add("local:kev", levelOK, "", "kev checkout found at %s", kevDir)
	return true
}

// bf16Fix is the command that moves leg to bf16, or every leg at once when each of them has a mode that this platform cannot serve.
func (d *doctor) bf16Fix(legs []modelLeg, leg modelLeg) string {
	if len(legs) > 1 && !slices.ContainsFunc(legs, func(l modelLeg) bool { return server.CheckMode(d.platform, l.mode) == nil }) {
		return bf16FixAll(d.cfg.Backend, legs)
	}
	return leg.bf16Fix(d.cfg.Backend)
}

func (d *doctor) checkLeg(kevDir string, leg modelLeg, bf16Fix string) {
	prefix := "local:" + leg.recordName()
	if variable := quantisationVariable(leg.mode); variable != "" && d.platform.OS != "darwin" {
		text, err := os.ReadFile(filepath.Join(kevDir, "kev", "checkpoint.py")) //nolint:gosec // the path is inside the configured kev directory
		if err != nil || !strings.Contains(string(text), variable) {
			d.add(prefix+":checkpoint", levelFail, "update the kev checkout, or use mode bf16: pagevow use local --mode bf16", "mode %s needs %s in kev/checkpoint.py and %s does not have it", leg.mode, variable, kevDir)
		} else {
			d.add(prefix+":checkpoint", levelOK, "", "kev/checkpoint.py supports mode %s", leg.mode)
		}
	}
	runDir := leg.model
	if !filepath.IsAbs(runDir) {
		runDir = filepath.Join(kevDir, "runs", leg.model)
	}
	if !dirExists(runDir) {
		d.add(prefix+":run", levelFail, "train or copy the run into "+filepath.Join(kevDir, "runs")+", or pick another model with: pagevow use local --model NAME", "the run directory %s for the %s does not exist", runDir, leg.label)
		return
	}
	d.add(prefix+":run", levelOK, "", "run directory %s exists", runDir)
	_, err := server.ModelCommand(d.platform, kevDir, leg.model, leg.mode, leg.port)
	switch {
	case errors.Is(err, server.ErrModeUnavailable):
		d.add(prefix+":mode", levelFail, bf16Fix, "%v", err)
		return
	case errors.Is(err, server.ErrDefaultModeUnsafe) && d.platform.MLX():
		d.add(prefix+":mode", levelFail, bf16Fix, "mode default is not allowed for %s: it is only allowed for models of 1B or less", leg.model)
		return
	case errors.Is(err, server.ErrDefaultModeUnsafe):
		d.add(prefix+":mode", levelFail, "use nf4, int8 or bf16", "mode default is not allowed for %s: it keeps CUDA graphs on and needs more GPU memory than is safe for this model", leg.model)
		return
	}
	if err != nil {
		d.add(prefix+":mode", levelFail, "", "%v", err)
		return
	}
	d.add(prefix+":mode", levelOK, "", "mode %s is allowed for %s", leg.mode, leg.model)
}

func quantisationVariable(mode string) string {
	switch mode {
	case config.ModeNF4:
		return "KEV_LOAD_IN_4BIT"
	case config.ModeInt8:
		return "KEV_LOAD_IN_8BIT"
	}
	return ""
}

func (d *doctor) gpuChecks(peaks []float64, large bool) {
	reading, err := d.gpu.Read(d.ctx)
	switch {
	case errors.Is(err, server.ErrNoGPUTool):
		d.add("local:gpu", levelFail, "install the NVIDIA driver, or use backend jev or custom", "nvidia-smi was not found")
		return
	case err != nil && d.platform.MLX():
		d.add("local:gpu", levelFail, "run: sysctl hw.memsize vm.page_free_count", "the memory could not be read: %v", err)
		return
	case err != nil:
		d.add("local:gpu", levelFail, "run nvidia-smi and check the driver", "the GPU could not be read: %v", err)
		return
	}
	if reading.Unified {
		d.add("local:gpu", levelOK, "", "unified memory: %d MiB free of %d MiB", reading.FreeMiB, reading.TotalMiB)
	} else {
		d.add("local:gpu", levelOK, "", "nvidia-smi answers: %d MiB free of %d MiB, %d C", reading.FreeMiB, reading.TotalMiB, reading.TempC)
	}
	if len(peaks) == 0 {
		return
	}
	if large {
		if err := server.FitsFloor(reading); err != nil {
			d.add("local:gpu-memory", levelFail, floorFix, "%v", err)
			return
		}
	}
	if err := server.Fits(reading, peaks...); err != nil {
		d.add("local:gpu-memory", levelFail, doctorFitsFix(d.platform), "%v", err)
		return
	}
	if d.platform.MLX() {
		d.add("local:gpu-memory", levelOK, "", "free memory fits the models pagevow start would launch (the macOS peaks are estimates)")
		return
	}
	d.add("local:gpu-memory", levelOK, "", "free GPU memory fits the models pagevow start would launch")
}

func doctorFitsFix(p server.Platform) string {
	if p.MLX() {
		return fitsFix(p)
	}
	return "stop other GPU programs or choose a smaller mode: pagevow use local --mode nf4"
}

func (d *doctor) browserChecks(haveConfig bool) {
	path, err := d.launcher.Find()
	if err != nil {
		d.add("browser:executable", levelFail, browserInstallFix, "no Chromium or Google Chrome was found")
	} else {
		d.add("browser:executable", levelOK, "", "browser executable found: %s", path)
	}
	d.installedBrowserCheck(err != nil)
	if !haveConfig {
		return
	}
	port := d.cfg.Browser.Port
	name := server.RecordName(server.KindBrowser, port)
	switch rec, owned := d.live[name]; {
	case owned:
		d.add("browser:port", levelOK, "", "port %d belongs to the browser pagevow started (pid %d)", port, rec.PID)
	case d.procs.PortInUse(d.ctx, port):
		d.add("browser:port", levelFail, "stop that process, or set browser.port to a free port", "port %d is in use by a process that pagevow did not start", port)
	default:
		d.add("browser:port", levelOK, "", "browser port %d is free", port)
	}
}

func (d *doctor) installedBrowserCheck(noSystemBrowser bool) {
	dir, err := config.BrowserDir(d.cache)
	if err != nil {
		return
	}
	rec, err := browser.LookupInstalled(dir, string(d.goos), string(d.goarch))
	switch {
	case err == nil && rec.Version != browser.PinnedVersion:
		d.add("browser:installed", levelWarn, "pagevow install --browser", "Chrome for Testing %s is installed at %s, this pagevow pins %s", rec.Version, rec.Executable, browser.PinnedVersion)
	case err == nil:
		d.add("browser:installed", levelOK, "", "pagevow installed Chrome for Testing %s at %s", rec.Version, rec.Executable)
	case errors.Is(err, browser.ErrInstallBroken):
		d.add("browser:installed", levelWarn, "pagevow install --browser", "the browser that pagevow installed cannot be used: %v", err)
	case noSystemBrowser:
		d.add("browser:installed", levelWarn, "pagevow install --browser", "pagevow has not installed Chrome for Testing %s", browser.PinnedVersion)
	}
}

func (d *doctor) textHelper() {
	t := d.cfg.TextHelper
	if t.URL == "" {
		d.add("text-helper", levelWarn, "set text_helper.url, or enable text_helper.local and set a loopback URL", "text_helper.url is not set: TYPE_TEXT steps will fail")
	} else {
		d.add("text-helper", levelOK, "", "text helper URL is %s", t.URL)
	}
	if !t.Local.Enabled {
		return
	}
	if _, err := localTextHelperOf(d.cfg); err != nil {
		d.add("text-helper:local", levelFail, "set text_helper.url to a loopback URL such as http://127.0.0.1:8081/v1", "%v", err)
		return
	}
	if _, err := d.lookPath("llama-server"); err != nil {
		d.add("text-helper:llama-server", levelFail, "install llama.cpp so that llama-server is on your PATH", "llama-server was not found on PATH")
		return
	}
	d.add("text-helper:llama-server", levelOK, "", "llama-server is on PATH")
	if legs, _ := modelLegs(d.cfg); len(legs) == 0 && d.goos != "windows" {
		if _, running := d.live[server.RecordName(server.KindTextHelper, portOfTextHelper(d.cfg))]; !running {
			if peak := server.TextHelperPeak(t.Local.GPULayers); peak > 0 {
				d.gpuChecks([]float64{peak}, false)
			}
		}
	}
}

func (d *doctor) tripped() {
	found, err := d.procs.Tripped()
	if err != nil {
		d.add("guard", levelWarn, "", "guard messages could not be read: %v", err)
		return
	}
	if len(found) == 0 {
		d.add("guard", levelOK, "", "no process was stopped by the GPU guard")
		return
	}
	for _, t := range found {
		d.add("guard", levelWarn, "check the GPU, then run pagevow start; a new start clears the message", "%s", t.Message)
	}
}

func (d *doctor) directories() {
	stateDir := d.procs.StateDir()
	logDir, err := config.LogDir(d.cache)
	if err != nil {
		d.add("directories", levelFail, "", "%v", err)
		return
	}
	for _, dir := range []struct{ id, path string }{{"directory:state", stateDir}, {"directory:logs", logDir}} {
		if err := writable(dir.path); err != nil {
			d.add(dir.id, levelFail, "make "+dir.path+" writable for your user", "%s is not writable: %v", dir.path, err)
			continue
		}
		d.add(dir.id, levelOK, "", "%s is writable", dir.path)
	}
}

func writable(dir string) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	file, err := os.CreateTemp(dir, ".doctor-*")
	if err != nil {
		return err
	}
	name := file.Name()
	if err := file.Close(); err != nil {
		return err
	}
	return os.Remove(name)
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.Mode().IsRegular()
}

func dirExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

func failureText(status int, err error) string {
	if err != nil {
		return rootCause(err)
	}
	return fmt.Sprintf("HTTP %d", status)
}

func portOfTextHelper(cfg config.Config) int {
	port, _ := portOfURL(cfg.TextHelper.URL)
	return port
}
