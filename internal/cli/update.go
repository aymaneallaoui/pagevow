package cli

import (
	"errors"
	"fmt"
	"net/http"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/aymaneallaoui/pagevow/internal/plugin"
	"github.com/aymaneallaoui/pagevow/internal/ui"
	"github.com/aymaneallaoui/pagevow/internal/update"
	"github.com/aymaneallaoui/pagevow/internal/version"
)

const (
	updateRepo      = "aymaneallaoui/pagevow"
	githubKeyRef    = "keychain:github"
	updateCacheName = "update"
)

type updateReport struct {
	Current         string `json:"current"`
	Latest          string `json:"latest"`
	UpdateAvailable bool   `json:"update_available"`
	Updated         bool   `json:"updated"`
	Executable      string `json:"executable"`
	Staged          string `json:"staged,omitempty"`
}

func (a *app) newUpdateCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "update",
		Short: "Replace the binary with the latest release",
		Long: "Download the latest GitHub release for this operating system and architecture, check the SHA-256 of the archive against\n" +
			"the release's checksums.txt and replace the running binary. A release that is not newer leaves the binary alone;\n" +
			"--force installs it again. A build that is not a release, such as dev, counts as older than every release.\n" +
			"The repository is private: give pagevow a token that can read it with GITHUB_TOKEN, GH_TOKEN or 'pagevow keys set github'.\n" +
			"The token is never printed. The checksum file comes from the same release, so it detects damage, not a tampered release.\n" +
			"After an update run 'pagevow plugin install' again: the Stop hook stores the path of the binary.\n\n" +
			"Exit codes: 0 up to date or updated, 1 with --check when a newer release exists, 2 nothing was replaced.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error { return a.runUpdate(cmd) },
	}
	flags := cmd.Flags()
	flags.Bool("check", false, "only report whether a newer release exists, download nothing")
	flags.Bool("force", false, "install the latest release even when it is not newer")
	flags.Bool("json", false, "print the result as JSON")
	return cmd
}

func (a *app) runUpdate(cmd *cobra.Command) error {
	flags := cmd.Flags()
	check, err := flags.GetBool("check")
	if err != nil {
		return fmt.Errorf("read --check: %w", err)
	}
	force, err := flags.GetBool("force")
	if err != nil {
		return fmt.Errorf("read --force: %w", err)
	}
	asJSON, err := flags.GetBool("json")
	if err != nil {
		return fmt.Errorf("read --json: %w", err)
	}
	opts, err := a.updateOptions(force)
	if err != nil {
		return err
	}
	out, err := a.printer(cmd.OutOrStdout())
	if err != nil {
		return err
	}
	ctx := commandContext(cmd)
	rel, err := update.Latest(ctx, opts)
	if err != nil {
		return infrastructure(err)
	}
	newer, err := update.NewerThan(rel.Version, opts.Current)
	if err != nil {
		return infrastructure(err)
	}
	report := updateReport{
		Current: displayVersion(opts.Current), Latest: displayVersion(rel.Version),
		UpdateAvailable: newer, Executable: opts.Executable,
	}
	if !asJSON {
		out.Status(ui.Info, "current %s, latest %s", report.Current, report.Latest)
		if !update.Comparable(opts.Current) {
			out.Status(ui.Info, "%s is not a release build, so the latest release counts as newer", report.Current)
		}
	}
	if check {
		return finishCheck(cmd, out, report, asJSON)
	}
	if !newer && !force {
		return finishCurrent(cmd, out, report, asJSON)
	}
	if !asJSON {
		opts.Progress = updateProgress(out, report.Latest)
	}
	result, err := update.Apply(ctx, opts, rel)
	if errors.Is(err, update.ErrNotReplaced) {
		report.Staged = result.Staged
		if asJSON {
			if jsonErr := writeJSON(cmd, report); jsonErr != nil {
				return jsonErr
			}
		}
		return infrastructure(fmt.Errorf("%w; the verified new binary is at %s, copy it over %s", err, result.Staged, result.Executable))
	}
	if err != nil {
		return infrastructure(err)
	}
	report.Executable = result.Executable
	report.Updated = !result.Skipped
	if asJSON {
		return writeJSON(cmd, report)
	}
	out.Status(ui.OK, "updated to %s at %s", report.Latest, result.Executable)
	a.remindPlugin(out)
	return out.Err()
}

func finishCheck(cmd *cobra.Command, out *ui.Printer, report updateReport, asJSON bool) error {
	if asJSON {
		if err := writeJSON(cmd, report); err != nil {
			return err
		}
	} else if report.UpdateAvailable {
		out.Status(ui.Info, "an update is available: run pagevow update")
	} else {
		out.Status(ui.OK, "already up to date")
	}
	if err := out.Err(); err != nil {
		return err
	}
	if report.UpdateAvailable {
		return &ExitError{Code: ExitFailure, Err: errors.New("an update is available"), Silent: true}
	}
	return nil
}

func finishCurrent(cmd *cobra.Command, out *ui.Printer, report updateReport, asJSON bool) error {
	if asJSON {
		return writeJSON(cmd, report)
	}
	out.Status(ui.OK, "already up to date")
	return out.Err()
}

func (a *app) updateOptions(force bool) (update.Options, error) {
	executable, err := service[Executable](a)
	if err != nil {
		return update.Options{}, err
	}
	path, err := executable()
	if err != nil {
		return update.Options{}, infrastructure(fmt.Errorf("find the pagevow executable: %w", err))
	}
	info, err := service[version.Info](a)
	if err != nil {
		return update.Options{}, err
	}
	goos, err := service[GOOS](a)
	if err != nil {
		return update.Options{}, err
	}
	arch, err := service[GOARCH](a)
	if err != nil {
		return update.Options{}, err
	}
	client, err := service[*http.Client](a)
	if err != nil {
		return update.Options{}, err
	}
	baseURL, err := service[UpdateBaseURL](a)
	if err != nil {
		return update.Options{}, err
	}
	cacheDir, err := service[CacheDir](a)
	if err != nil {
		return update.Options{}, err
	}
	token, err := a.githubToken()
	if err != nil {
		return update.Options{}, err
	}
	fallback := ""
	if cache, err := cacheDir(); err == nil {
		fallback = filepath.Join(cache, "pagevow", updateCacheName)
	}
	return update.Options{
		Repo: updateRepo, APIBaseURL: string(baseURL), Client: client, Token: token,
		GOOS: string(goos), GOARCH: string(arch), Current: info.Version, Executable: path,
		FallbackDir: fallback, Force: force,
	}, nil
}

func (a *app) githubToken() (string, error) {
	lookup, err := service[LookupEnv](a)
	if err != nil {
		return "", err
	}
	for _, name := range []string{"GITHUB_TOKEN", "GH_TOKEN"} {
		if value, ok := lookup(name); ok && strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value), nil
		}
	}
	resolver, err := a.resolver()
	if err != nil {
		return "", err
	}
	// An unreadable or empty keychain means no token: the release request then says the repository is missing or private.
	token, _ := resolver.Resolve(githubKeyRef)
	return strings.TrimSpace(token), nil
}

func (a *app) remindPlugin(out *ui.Printer) {
	root, err := a.pluginRoot()
	if err != nil || !plugin.Installed(root) {
		return
	}
	out.Status(ui.Info, "run pagevow plugin install again so the Stop hook uses the new binary")
}

func displayVersion(text string) string {
	if update.Comparable(text) {
		return "v" + strings.TrimPrefix(text, "v")
	}
	return text
}

func updateProgress(out *ui.Printer, latest string) func(done, total int64) {
	last := 0
	return func(done, total int64) {
		if done == 0 {
			if total > 0 {
				out.Status(ui.Info, "downloading pagevow %s (%.1f MB)", latest, float64(total)/bytesPerMB)
			} else {
				out.Status(ui.Info, "downloading pagevow %s", latest)
			}
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
