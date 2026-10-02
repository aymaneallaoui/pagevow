package cli

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"path/filepath"
	"slices"

	"github.com/spf13/cobra"

	"github.com/aymaneallaoui/pagevow/internal/browser"
	"github.com/aymaneallaoui/pagevow/internal/config"
	"github.com/aymaneallaoui/pagevow/internal/mode"
	"github.com/aymaneallaoui/pagevow/internal/model"
	"github.com/aymaneallaoui/pagevow/internal/server"
	"github.com/aymaneallaoui/pagevow/internal/ui"
)

const (
	bytesPerMB        = 1 << 20
	huggingFaceKeyRef = "keychain:huggingface"
	runsDirName       = "runs"
	shortCommitLen    = 7
	progressMinBytes  = bytesPerMB
)

type modelReport struct {
	Name             string `json:"name"`
	Dir              string `json:"dir"`
	Source           string `json:"source"`
	Revision         string `json:"revision"`
	BaseModel        string `json:"base_model"`
	Files            int    `json:"files"`
	AlreadyInstalled bool   `json:"already_installed"`
}

type bothReport struct {
	Browser installReport `json:"browser"`
	Model   modelReport   `json:"model"`
}

type modelRequest struct {
	arg    string
	name   string
	force  bool
	link   bool
	asJSON bool
}

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
		Long: "--browser downloads the pinned Chrome for Testing build for this operating system and architecture into the user cache directory,\n" +
			"verifies its size and SHA-256, unpacks it and records the version. Run again, the command does nothing while the pinned build\n" +
			"is installed; --force installs it again. pagevow uses the installed build before any browser found on PATH.\n" +
			"The install refuses while the browser that pagevow start keeps running exists: run pagevow stop first.\n\n" +
			"--model puts a run directory under <server.kev_dir>/runs/NAME, where pagevow use local --model NAME finds it. The source is a\n" +
			"directory (copied; --link makes a symbolic link instead and keeps its install record beside it in the runs directory), or a\n" +
			"Hugging Face repository as OWNER/NAME[@REVISION], downloaded at the commit that the revision names. A run directory needs\n" +
			"adapter_config.json, adapter_model.safetensors and head.pt. NAME is the directory or repository name unless --name is given.\n" +
			"A copy follows a symbolic link only to a file inside the source directory. A large file is checked against the SHA-256 that\n" +
			"the Hub lists, a small one against its git object id, every file against its size. A private repository needs\n" +
			"HF_TOKEN, HUGGING_FACE_HUB_TOKEN or 'pagevow keys set huggingface'; the token is never printed and only goes to the Hub.\n" +
			"An existing NAME is replaced only with --force and only when pagevow installed it, never while a model server runs it.\n" +
			"A copy whose source changed since the install (file names, sizes or modification times) is replaced only with --force.\n" +
			"head.pt is a PyTorch file that can run code when it is loaded: install models only from sources you trust.\n\n" +
			"With --browser and --model the model is installed first, then the browser.\n\n" +
			"Exit codes: 0 installed or already installed, 2 nothing was installed, or with --browser and --model the model was\n" +
			"installed and the browser was not (the message says so, and --json still prints the model report).",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error { return a.runInstall(cmd) },
	}
	flags := cmd.Flags()
	flags.Bool("browser", false, "download the pinned Chrome for Testing build")
	flags.String("model", "", "local model to install: a run directory, or a Hugging Face repository as OWNER/NAME[@REVISION]")
	flags.String("name", "", "name of the installed model under the runs directory (default: the directory or repository name)")
	flags.Bool("link", false, "link the model directory instead of copying it (with --model and a directory)")
	flags.Bool("force", false, "install again even when the browser or the model is installed")
	flags.Bool("json", false, "print the result as JSON")
	return cmd
}

func (a *app) runInstall(cmd *cobra.Command) error {
	flags := cmd.Flags()
	withBrowser, err := flags.GetBool("browser")
	if err != nil {
		return fmt.Errorf("read --browser: %w", err)
	}
	req := modelRequest{}
	if req.arg, err = flags.GetString("model"); err != nil {
		return fmt.Errorf("read --model: %w", err)
	}
	if req.name, err = flags.GetString("name"); err != nil {
		return fmt.Errorf("read --name: %w", err)
	}
	if req.link, err = flags.GetBool("link"); err != nil {
		return fmt.Errorf("read --link: %w", err)
	}
	if req.force, err = flags.GetBool("force"); err != nil {
		return fmt.Errorf("read --force: %w", err)
	}
	if req.asJSON, err = flags.GetBool("json"); err != nil {
		return fmt.Errorf("read --json: %w", err)
	}
	switch {
	case !withBrowser && req.arg == "":
		return infrastructure(errors.New("nothing to install: pass --browser or --model"))
	case req.arg == "" && (req.name != "" || req.link):
		return infrastructure(errors.New("--name and --link need --model"))
	}
	out, err := a.printer(cmd.OutOrStdout())
	if err != nil {
		return err
	}
	var (
		browserReport installReport
		modelRep      modelReport
	)
	if req.arg != "" {
		if modelRep, err = a.installModel(cmd, out, req); err != nil {
			return err
		}
	}
	if withBrowser {
		if browserReport, err = a.installBrowser(cmd, out, req.force, req.asJSON); err != nil {
			if req.arg == "" {
				return err
			}
			return a.browserFailedAfterModel(cmd, modelRep, req.asJSON, err)
		}
	}
	if req.asJSON {
		switch {
		case withBrowser && req.arg != "":
			return writeJSON(cmd, bothReport{Browser: browserReport, Model: modelRep})
		case withBrowser:
			return writeJSON(cmd, browserReport)
		}
		return writeJSON(cmd, modelRep)
	}
	return out.Err()
}

// browserFailedAfterModel reports a browser install that failed after the model was installed: the model report still goes out.
func (a *app) browserFailedAfterModel(cmd *cobra.Command, modelRep modelReport, asJSON bool, err error) error {
	if asJSON {
		if jsonErr := writeJSON(cmd, modelRep); jsonErr != nil {
			return errors.Join(err, jsonErr)
		}
	}
	done := "installed model %s at %s"
	if modelRep.AlreadyInstalled {
		done = "model %s was already installed at %s"
	}
	return infrastructure(fmt.Errorf(done+", but the browser install failed: %w", modelRep.Name, modelRep.Dir, err))
}

func (a *app) installBrowser(cmd *cobra.Command, out *ui.Printer, force, asJSON bool) (installReport, error) {
	setup, err := a.installSetup(force)
	if err != nil {
		return installReport{}, err
	}
	if !asJSON {
		setup.opts.Progress = downloadProgress(out, setup.pin)
	}
	installed, err := browser.Install(commandContext(cmd), setup.opts)
	if err != nil {
		var exit *ExitError
		if errors.As(err, &exit) {
			return installReport{}, err
		}
		return installReport{}, infrastructure(err)
	}
	report := installReport{
		Version: installed.Version, Platform: installed.Platform,
		Executable: installed.Executable, AlreadyInstalled: installed.AlreadyInstalled,
	}
	if asJSON {
		return report, nil
	}
	if installed.AlreadyInstalled {
		out.Status(ui.OK, "Chrome for Testing %s is already installed at %s", installed.Version, installed.Executable)
	} else {
		out.Status(ui.OK, "installed Chrome for Testing %s at %s", installed.Version, installed.Executable)
	}
	return report, nil
}

func (a *app) installModel(cmd *cobra.Command, out *ui.Printer, req modelRequest) (modelReport, error) {
	src, err := model.ParseSource(req.arg)
	if err != nil {
		return modelReport{}, infrastructure(err)
	}
	opts, err := a.modelOptions(src, req)
	if err != nil {
		return modelReport{}, err
	}
	if !req.asJSON {
		opts.Begin = modelBegin(out)
		opts.Progress = modelProgress(out)
	}
	installed, err := model.Install(commandContext(cmd), src, opts)
	if err != nil {
		var exit *ExitError
		if errors.As(err, &exit) {
			return modelReport{}, err
		}
		return modelReport{}, infrastructure(err)
	}
	report := modelReport{
		Name: installed.Name, Dir: installed.Dir, Source: installed.Source, Revision: installed.Revision,
		BaseModel: installed.BaseModel, Files: len(installed.Files), AlreadyInstalled: installed.AlreadyInstalled,
	}
	if req.asJSON {
		return report, nil
	}
	if installed.AlreadyInstalled {
		out.Status(ui.OK, "model %s is already installed at %s (base %s)", installed.Name, installed.Dir, installed.BaseModel)
	} else {
		out.Status(ui.OK, "installed model %s at %s (base %s)", installed.Name, installed.Dir, installed.BaseModel)
	}
	if err := a.modelHint(out, installed.Name); err != nil {
		return modelReport{}, err
	}
	return report, nil
}

func (a *app) modelOptions(src model.Source, req modelRequest) (model.Options, error) {
	cfg, _, err := a.loadConfig()
	if err != nil {
		return model.Options{}, infrastructure(err)
	}
	home, err := service[HomeDir](a)
	if err != nil {
		return model.Options{}, err
	}
	kevDir, err := kevDirOf(cfg, home)
	if err != nil {
		return model.Options{}, infrastructure(err)
	}
	if !dirExists(kevDir) {
		return model.Options{}, infrastructure(fmt.Errorf("server.kev_dir %s does not exist: set it to your kev checkout, or create it", kevDir))
	}
	client, err := service[*http.Client](a)
	if err != nil {
		return model.Options{}, err
	}
	hubURL, err := service[HubBaseURL](a)
	if err != nil {
		return model.Options{}, err
	}
	clock, err := service[Clock](a)
	if err != nil {
		return model.Options{}, err
	}
	guard, err := a.modelInUseGuard()
	if err != nil {
		return model.Options{}, err
	}
	token := ""
	if src.Repo != "" {
		if token, err = a.tokenFrom([]string{"HF_TOKEN", "HUGGING_FACE_HUB_TOKEN"}, huggingFaceKeyRef); err != nil {
			return model.Options{}, err
		}
	}
	return model.Options{
		RunsDir: filepath.Join(kevDir, runsDirName), Name: req.name, Force: req.force, Link: req.link,
		HubBaseURL: string(hubURL), Client: hubClient(client), Token: token, Guard: guard, Now: clock,
	}, nil
}

// hubClient keeps the transport and redirect rule of base without its overall timeout, which a large model file outlasts.
// The stall guard of model.Install takes the place of that timeout.
func hubClient(base *http.Client) *http.Client {
	return &http.Client{Transport: base.Transport, CheckRedirect: base.CheckRedirect, Jar: base.Jar}
}

func (a *app) modelInUseGuard() (func(context.Context, string) error, error) {
	procs, err := service[Processes](a)
	if err != nil {
		return nil, err
	}
	return func(ctx context.Context, dir string) error {
		records, err := procs.List()
		if err != nil {
			return infrastructure(fmt.Errorf("list the pagevow processes: %w", err))
		}
		for _, rec := range records {
			if rec.Kind != server.KindModel {
				continue
			}
			state := procs.State(ctx, rec)
			if err := ctx.Err(); err != nil {
				return infrastructure(fmt.Errorf("check the pagevow processes: %w", err))
			}
			if state == server.StateGone {
				continue
			}
			pid := rec.PID
			if state == server.StateOrphaned {
				pid = rec.ChildPID
			}
			if run := runDirOf(rec.Command); run != "" && samePath(run, dir) {
				return infrastructure(fmt.Errorf("the model server %s (pid %d) runs %s and would use files that the install replaces; run pagevow stop first", rec.Name, pid, dir))
			}
		}
		return nil
	}, nil
}

func runDirOf(argv []string) string {
	i := slices.Index(argv, "--run")
	if i < 0 || i+1 >= len(argv) {
		return ""
	}
	return argv[i+1]
}

func samePath(a, b string) bool {
	if filepath.Clean(a) == filepath.Clean(b) {
		return true
	}
	realA, errA := filepath.EvalSymlinks(a)
	realB, errB := filepath.EvalSymlinks(b)
	return errA == nil && errB == nil && realA == realB
}

func (a *app) modelHint(out *ui.Printer, name string) error {
	goos, err := service[GOOS](a)
	if err != nil {
		return err
	}
	switch goos {
	case "linux":
		out.Status(ui.Info, "use it with: pagevow use local --model %s --mode %s", name, mode.NF4)
	case "darwin":
		out.Status(ui.Info, "use it with: pagevow use local --model %s --mode %s", name, mode.BF16)
	}
	return nil
}

func modelBegin(out *ui.Printer) func(model.Plan) {
	return func(plan model.Plan) {
		switch {
		case plan.Link:
			out.Status(ui.Info, "linking %s as %s", plan.Source, plan.Dir)
		case plan.Revision != "":
			out.Status(ui.Info, "downloading %s@%s (%d files, %d MB)", plan.Source, shortCommit(plan.Revision), plan.Files, (plan.Bytes+bytesPerMB/2)/bytesPerMB)
		default:
			out.Status(ui.Info, "copying %s to %s", plan.Source, plan.Dir)
		}
	}
}

func shortCommit(commit string) string {
	if len(commit) > shortCommitLen {
		return commit[:shortCommitLen]
	}
	return commit
}

func modelProgress(out *ui.Printer) func(file string, done, total int64) {
	var (
		current string
		last    int
	)
	return func(file string, done, total int64) {
		if file != current {
			current, last = file, 0
		}
		if !out.Styled() || done == 0 || total < progressMinBytes {
			return
		}
		if tenth := int(done * 10 / total); tenth > last {
			last = tenth
			out.Status(ui.Info, "%s %d%%", file, tenth*10)
		}
	}
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
	return func(ctx context.Context) error {
		records, err := procs.List()
		if err != nil {
			return infrastructure(fmt.Errorf("list the pagevow processes: %w", err))
		}
		for _, rec := range records {
			if rec.Kind != server.KindBrowser {
				continue
			}
			running := procs.State(ctx, rec) == server.StateRunning
			if err := ctx.Err(); err != nil {
				return infrastructure(fmt.Errorf("check the pagevow processes: %w", err))
			}
			if running {
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
