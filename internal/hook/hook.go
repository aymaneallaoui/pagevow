// Package hook implements the Claude Code Stop hook: it runs the project's browser tests and blocks Claude while they fail.
package hook

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/aymaneallaoui/pagevow/internal/runner"
	"github.com/aymaneallaoui/pagevow/internal/testsfile"
)

const (
	// EnvDisable turns the hook off when set to "0".
	EnvDisable = "PAGEVOW_HOOK"
	// EnvMaxBlocks caps how many times in a row the hook blocks one session.
	EnvMaxBlocks = "PAGEVOW_HOOK_MAX_BLOCKS"
	// DefaultMaxBlocks is the cap used when EnvMaxBlocks is unset or invalid.
	DefaultMaxBlocks = 2
	// OutDirName is the run output and state directory inside the project.
	OutDirName = ".pagevow"

	envProjectDir    = "CLAUDE_PROJECT_DIR"
	lastPassName     = ".last-pass"
	blocksPrefix     = ".blocks-"
	unknownSession   = "unknown"
	maxInputBytes    = 1 << 20
	skippedTailLines = 12
	fileMode         = 0o600

	exitAllow = 0
	exitFail  = 1
	exitBlock = 2
)

// Input is the subset of the Claude Code Stop hook JSON that pagevow uses.
type Input struct {
	SessionID string `json:"session_id"`
	CWD       string `json:"cwd"`
}

// ReadInput decodes the hook JSON; invalid or empty input gives a zero Input.
func ReadInput(r io.Reader) Input {
	if r == nil {
		return Input{}
	}
	data, err := io.ReadAll(io.LimitReader(r, maxInputBytes))
	if err != nil {
		return Input{}
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return Input{}
	}
	return Input{SessionID: stringField(fields, "session_id"), CWD: stringField(fields, "cwd")}
}

func stringField(fields map[string]json.RawMessage, name string) string {
	var value string
	if err := json.Unmarshal(fields[name], &value); err != nil {
		return ""
	}
	return value
}

// RunSpec describes one run of the pagevow suite.
type RunSpec struct {
	TestsFile string
	OutDir    string
	Dir       string
}

// RunResult is the outcome of one run of the suite.
type RunResult struct {
	ExitCode int
	Report   *runner.Report
	Stdout   string
	Stderr   string
}

// Runner runs the pagevow suite; an error means it could not be started at all.
type Runner interface {
	Run(ctx context.Context, spec RunSpec) (RunResult, error)
}

// Git runs one git command in dir and returns its stdout; a nil Git means git is unavailable.
type Git interface {
	Output(ctx context.Context, dir string, args ...string) ([]byte, error)
}

// Config carries what Stop needs from its environment.
type Config struct {
	LookupEnv func(string) (string, bool)
	Getwd     func() (string, error)
	Runner    Runner
	Git       Git
	Stderr    io.Writer
}

func (c Config) withDefaults() Config {
	if c.LookupEnv == nil {
		c.LookupEnv = os.LookupEnv
	}
	if c.Getwd == nil {
		c.Getwd = os.Getwd
	}
	if c.Stderr == nil {
		c.Stderr = io.Discard
	}
	return c
}

// Stop applies the Stop hook rules and returns the process exit code: 0, 1 or 2.
func Stop(ctx context.Context, cfg Config, in Input) int {
	cfg = cfg.withDefaults()
	if value, ok := cfg.LookupEnv(EnvDisable); ok && value == "0" {
		return exitAllow
	}
	dir := ProjectDir(cfg.LookupEnv, in, cfg.Getwd)
	testsFile, err := testsfile.Find(dir)
	switch {
	case errors.Is(err, testsfile.ErrNotFound):
		return exitAllow
	case err != nil:
		say(cfg.Stderr, "Browser tests skipped: could not look for a tests file: %v\n", err)
		return exitFail
	}
	outDir := filepath.Join(dir, OutDirName)
	if err := runner.EnsureRealDir(outDir); err != nil {
		say(cfg.Stderr, "Browser tests skipped: could not create %s: %v\n", outDir, err)
		return exitFail
	}
	st := state{
		lastPass: filepath.Join(outDir, lastPassName),
		blocks:   filepath.Join(outDir, blocksPrefix+SanitizeSessionID(in.SessionID)),
	}
	st.fingerprint, err = Fingerprint(ctx, cfg.Git, dir, testsFile)
	if err != nil {
		say(cfg.Stderr, "Browser tests skipped: could not fingerprint the project: %v\n", err)
		return exitFail
	}
	if st.readLastPass() == st.fingerprint {
		return exitAllow
	}
	if cfg.Runner == nil {
		say(cfg.Stderr, "Browser tests skipped: could not run pagevow: no runner configured\n")
		return exitFail
	}
	res, err := cfg.Runner.Run(ctx, RunSpec{TestsFile: testsFile, OutDir: outDir, Dir: dir})
	if err != nil {
		say(cfg.Stderr, "Browser tests skipped: could not run pagevow: %v\n", err)
		return exitFail
	}
	switch res.ExitCode {
	case runner.ExitPassed:
		warnPaid(cfg.Stderr, res)
		return st.passed(cfg.Stderr)
	case runner.ExitFailed:
		warnPaid(cfg.Stderr, res)
		return st.failed(cfg, res)
	default:
		say(cfg.Stderr, "%s", skippedText(res))
		return exitFail
	}
}

type state struct {
	lastPass    string
	blocks      string
	fingerprint string
}

func (s state) readLastPass() string {
	data, ok := readState(s.lastPass)
	if !ok {
		return ""
	}
	return strings.TrimSpace(data)
}

func (s state) readBlocks() int {
	data, ok := readState(s.blocks)
	if !ok {
		return 0
	}
	count, ok := parseCount(strings.TrimSpace(data))
	if !ok {
		return 0
	}
	return count
}

func (s state) passed(stderr io.Writer) int {
	if err := removeState(s.blocks); err != nil {
		warnState(stderr, err)
	}
	if err := writeState(s.lastPass, s.fingerprint+"\n"); err != nil {
		warnState(stderr, err)
	}
	return exitAllow
}

func (s state) failed(cfg Config, res RunResult) int {
	limit := DefaultMaxBlocks
	if value, ok := cfg.LookupEnv(EnvMaxBlocks); ok {
		limit = ParseMaxBlocks(value)
	}
	blocks := s.readBlocks()
	if blocks >= limit {
		if err := writeState(s.blocks, "0\n"); err != nil {
			warnState(cfg.Stderr, err)
		}
		say(cfg.Stderr, "Browser tests still fail after %d blocked attempts (limit %d). The hook lets Claude stop now. "+
			"Claude must tell the user plainly that the browser tests are failing.\n%s", blocks, limit, runOutput(res))
		return exitFail
	}
	blocks++
	if err := writeState(s.blocks, strconv.Itoa(blocks)+"\n"); err != nil {
		say(cfg.Stderr, "Browser tests still fail, but the block counter could not be saved (%v), so the hook lets Claude stop now. "+
			"Claude must tell the user plainly that the browser tests are failing.\n%s", err, runOutput(res))
		return exitFail
	}
	say(cfg.Stderr, "Browser tests failed. Inspect each final.png listed below with the Read tool, "+
		"decide whether the app or the test is wrong, and fix it.\n%sBrowser test block %d of %d.\n", runOutput(res), blocks, limit)
	return exitBlock
}

func readState(path string) (string, bool) {
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() {
		return "", false
	}
	data, err := os.ReadFile(path) //nolint:gosec // the path is inside the project's own state directory
	if err != nil {
		return "", false
	}
	return string(data), true
}

// removeState deletes a state file; a symlink is removed without being followed.
func removeState(path string) error {
	info, err := os.Lstat(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("remove hook state: %w", err)
	}
	if !info.Mode().IsRegular() && info.Mode()&fs.ModeSymlink == 0 {
		return fmt.Errorf("remove hook state: %s is not a regular file", path)
	}
	if err := os.Remove(path); err != nil {
		return fmt.Errorf("remove hook state: %w", err)
	}
	return nil
}

// writeState replaces the state file atomically and refuses a target that is not a regular file, so a planted symlink is never written through.
func writeState(path, content string) error {
	if info, err := os.Lstat(path); err == nil {
		if !info.Mode().IsRegular() {
			return fmt.Errorf("write hook state: %s is not a regular file", path)
		}
	} else if !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("write hook state: %w", err)
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".tmp-*")
	if err != nil {
		return fmt.Errorf("write hook state: %w", err)
	}
	tmpName := tmp.Name()
	_, werr := tmp.WriteString(content)
	cerr := tmp.Close()
	if err := errors.Join(werr, cerr); err != nil {
		_ = os.Remove(tmpName)
		return fmt.Errorf("write hook state: %w", err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		_ = os.Remove(tmpName)
		return fmt.Errorf("write hook state: %w", err)
	}
	return nil
}

func warnPaid(w io.Writer, res RunResult) {
	if res.Report == nil || len(res.Report.PaidServices) == 0 {
		return
	}
	say(w, "Browser tests used a paid service: %s; it may bill per request.\n", strings.Join(res.Report.PaidServices, " and "))
}

func warnState(w io.Writer, err error) {
	say(w, "pagevow could not save hook state: %v\n", err)
}

func say(w io.Writer, format string, args ...any) {
	_, _ = fmt.Fprintf(w, format, args...)
}

func runOutput(res RunResult) string {
	var b strings.Builder
	if res.Report != nil {
		writeLine(&b, res.Report.Failures())
		if res.Report.RunDir != "" {
			writeLine(&b, "Full report: "+filepath.Join(res.Report.RunDir, "report.json"))
		}
		return b.String()
	}
	raw := res.Stderr
	if strings.TrimSpace(raw) == "" {
		raw = res.Stdout
	}
	writeLine(&b, raw)
	return b.String()
}

func writeLine(b *strings.Builder, text string) {
	text = strings.TrimRight(text, "\n")
	if text == "" {
		return
	}
	b.WriteString(text)
	b.WriteByte('\n')
}

func skippedText(res RunResult) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Browser tests skipped: pagevow run exited with code %d "+
		"(backend not reachable, browser missing, or an invalid tests file).\n", res.ExitCode)
	writeLine(&b, lastLines(res.Stderr, skippedTailLines))
	b.WriteString("Start what is missing, then ask Claude to run the browser tests again:\n")
	b.WriteString("  pagevow start      starts the local model server and the browser\n")
	b.WriteString("  pagevow doctor     shows what is wrong and how to fix it\n")
	return b.String()
}

func lastLines(text string, n int) string {
	lines := strings.Split(strings.TrimRight(text, "\n"), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n")
}

// ProjectDir returns the project directory: CLAUDE_PROJECT_DIR, else the hook's cwd, else the working directory.
func ProjectDir(lookupEnv func(string) (string, bool), in Input, getwd func() (string, error)) string {
	if lookupEnv == nil {
		lookupEnv = os.LookupEnv
	}
	if getwd == nil {
		getwd = os.Getwd
	}
	if value, ok := lookupEnv(envProjectDir); ok && value != "" {
		return value
	}
	if in.CWD != "" {
		return in.CWD
	}
	if wd, err := getwd(); err == nil && wd != "" {
		return wd
	}
	return "."
}

// SanitizeSessionID keeps only letters, digits, underscore and hyphen; an empty result becomes "unknown".
func SanitizeSessionID(id string) string {
	var b strings.Builder
	for _, r := range id {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '_', r == '-':
			b.WriteRune(r)
		}
	}
	if b.Len() == 0 {
		return unknownSession
	}
	return b.String()
}

// ParseMaxBlocks reads the block cap; anything but a plain non-negative base-10 number gives DefaultMaxBlocks.
func ParseMaxBlocks(value string) int {
	count, ok := parseCount(value)
	if !ok {
		return DefaultMaxBlocks
	}
	return count
}

func parseCount(value string) (int, bool) {
	if value == "" {
		return 0, false
	}
	for _, r := range value {
		if r < '0' || r > '9' {
			return 0, false
		}
	}
	count, err := strconv.Atoi(value)
	if err != nil {
		return 0, false
	}
	return count, true
}
