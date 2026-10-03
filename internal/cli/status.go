package cli

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/aymaneallaoui/pagevow/internal/browser"
	"github.com/aymaneallaoui/pagevow/internal/config"
	"github.com/aymaneallaoui/pagevow/internal/keys"
	"github.com/aymaneallaoui/pagevow/internal/ui"
)

type field struct {
	name  string
	value any
}

type statusReport struct {
	ConfigFile       string            `json:"config_file"`
	ConfigFileExists bool              `json:"config_file_exists"`
	PaidAPI          bool              `json:"paid_api"`
	Backend          map[string]any    `json:"backend"`
	URLs             map[string]any    `json:"urls"`
	TextHelper       map[string]any    `json:"text_helper"`
	Browser          map[string]any    `json:"browser"`
	Processes        []processState    `json:"processes"`
	Health           []healthState     `json:"health"`
	GPU              *gpuState         `json:"gpu,omitempty"`
	Versions         map[string]string `json:"versions"`
	Tripped          []trippedState    `json:"tripped"`
	StaleRemoved     []string          `json:"stale_removed"`
}

type processState struct {
	Name          string `json:"name"`
	Kind          string `json:"kind"`
	PID           int    `json:"pid"`
	ChildPID      int    `json:"child_pid,omitempty"`
	Port          int    `json:"port"`
	State         string `json:"state"`
	Alive         bool   `json:"alive"`
	Ready         bool   `json:"ready"`
	UptimeSeconds int64  `json:"uptime_seconds"`
	Log           string `json:"log"`
}

type healthState struct {
	Name       string `json:"name"`
	URL        string `json:"url"`
	Reachable  bool   `json:"reachable"`
	HTTPStatus int    `json:"http_status,omitempty"`
	Error      string `json:"error,omitempty"`
}

type gpuState struct {
	TotalMiB     int  `json:"total_mib"`
	UsedMiB      int  `json:"used_mib"`
	FreeMiB      int  `json:"free_mib"`
	TemperatureC int  `json:"temperature_c"`
	Unified      bool `json:"unified,omitempty"`

	Components *memoryComponents `json:"components,omitempty"`
}

type memoryComponents struct {
	FreeMiB        int `json:"free_mib"`
	SpeculativeMiB int `json:"speculative_mib"`
	PurgeableMiB   int `json:"purgeable_mib"`
	FileBackedMiB  int `json:"file_backed_mib"`
}

type trippedState struct {
	Name    string `json:"name"`
	Message string `json:"message"`
}

func (a *app) newStatusCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "status",
		Short: "Show the active backend, configured URLs and browser settings",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			asJSON, err := cmd.Flags().GetBool("json")
			if err != nil {
				return fmt.Errorf("read --json: %w", err)
			}
			cfg, path, err := a.loadConfig()
			if err != nil {
				return err
			}
			resolver, err := a.resolver()
			if err != nil {
				return err
			}
			_, statErr := os.Stat(path)
			report := buildStatus(cfg, path, statErr == nil, resolver)
			if err := a.addBrowserInstall(&report); err != nil {
				return err
			}
			if err := a.collectRuntime(commandContext(cmd), cfg, &report); err != nil {
				return err
			}
			if asJSON {
				return writeJSON(cmd, report)
			}
			platform, err := a.platformOf()
			if err != nil {
				return err
			}
			out, err := a.printer(cmd.OutOrStdout())
			if err != nil {
				return err
			}
			renderStatus(out, cfg, report, resolver)
			renderRuntime(out, report, platform)
			return out.Err()
		},
	}
	cmd.Flags().Bool("json", false, "print the status as JSON")
	return cmd
}

func writeJSON(cmd *cobra.Command, value any) error {
	encoder := json.NewEncoder(cmd.OutOrStdout())
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(value); err != nil {
		return fmt.Errorf("write JSON: %w", err)
	}
	return nil
}

func buildStatus(cfg config.Config, path string, exists bool, resolver *keys.Resolver) statusReport {
	return statusReport{
		ConfigFile:       path,
		ConfigFileExists: exists,
		PaidAPI:          len(paidServices(cfg, resolver)) > 0,
		Backend:          toMap(append([]field{{"name", cfg.Backend}}, activeFields(cfg)...)),
		URLs: toMap([]field{
			{"local", cfg.Backends.Local.URL},
			{"jev", cfg.Backends.Jev.URL},
			{"custom", cfg.Backends.Custom.URL},
			{"cascade_primary", cfg.Backends.Cascade.Primary},
			{"cascade_verifier", cfg.Backends.Cascade.Verifier},
			{"text_helper", cfg.TextHelper.URL},
		}),
		TextHelper: toMap(textHelperFields(cfg)),
		Browser: toMap([]field{
			{"port", cfg.Browser.Port},
			{"headless", cfg.Browser.Headless},
			{"viewport_width", cfg.Browser.Viewport.Width},
			{"viewport_height", cfg.Browser.Viewport.Height},
			{"channel", cfg.Browser.Channel},
		}),
	}
}

func (a *app) addBrowserInstall(report *statusReport) error {
	cacheDir, err := service[CacheDir](a)
	if err != nil {
		return err
	}
	dir, err := config.BrowserDir(cacheDir)
	if err != nil {
		report.Browser["installed"] = false
		return nil
	}
	goos, err := service[GOOS](a)
	if err != nil {
		return err
	}
	goarch, err := service[GOARCH](a)
	if err != nil {
		return err
	}
	rec, err := browser.LookupInstalled(dir, string(goos), string(goarch))
	report.Browser["installed"] = err == nil
	if err == nil {
		report.Browser["installed_version"] = rec.Version
		report.Browser["installed_path"] = rec.Executable
	}
	return nil
}

func browserInstallLine(report statusReport) string {
	if installed, _ := report.Browser["installed"].(bool); installed {
		return fmt.Sprintf("%v at %v", report.Browser["installed_version"], report.Browser["installed_path"])
	}
	return "no (pagevow install --browser)"
}

type destination struct {
	label  string
	url    string
	keyRef string
}

// destinations lists where a run would send page data: the active backend and the text helper.
func destinations(cfg config.Config) []destination {
	var out []destination
	switch cfg.Backend {
	case config.BackendLocal:
		out = append(out, destination{"the local decision backend", cfg.Backends.Local.URL, ""})
	case config.BackendJev:
		out = append(out, destination{"the jev decision backend", cfg.Backends.Jev.URL, cfg.Backends.Jev.Key})
	case config.BackendCustom:
		out = append(out, destination{"the custom decision backend", cfg.Backends.Custom.URL, cfg.Backends.Custom.Key})
	case config.BackendCascade:
		out = append(out,
			destination{"the cascade primary model", cfg.Backends.Cascade.Primary, cfg.Backends.Cascade.PrimaryKey},
			destination{"the cascade verifier model", cfg.Backends.Cascade.Verifier, cfg.Backends.Cascade.VerifierKey})
	}
	return append(out, destination{"the text helper", cfg.TextHelper.URL, cfg.TextHelper.Key})
}

// paidServices names the destinations that are not on this machine and would be called with a real API key.
func paidServices(cfg config.Config, resolver *keys.Resolver) []string {
	var services []string
	for _, d := range destinations(cfg) {
		if d.url == "" || config.IsLoopbackURL(d.url) || !hasRealKey(resolver, d.keyRef) {
			continue
		}
		services = append(services, fmt.Sprintf("%s at %s", d.label, d.url))
	}
	return services
}

func hasRealKey(resolver *keys.Resolver, ref string) bool {
	if resolver == nil || ref == "" {
		return false
	}
	value, err := resolver.Resolve(ref)
	return err == nil && value != "" && value != placeholderKey
}

func activeFields(cfg config.Config) []field {
	switch cfg.Backend {
	case config.BackendLocal:
		return []field{{"url", cfg.Backends.Local.URL}, {"model", cfg.Backends.Local.Model}, {"mode", cfg.Backends.Local.Mode}}
	case config.BackendJev:
		return []field{{"url", cfg.Backends.Jev.URL}, {"key", cfg.Backends.Jev.Key}}
	case config.BackendCustom:
		return []field{{"url", cfg.Backends.Custom.URL}, {"key", cfg.Backends.Custom.Key}}
	case config.BackendCascade:
		c := cfg.Backends.Cascade
		return []field{
			{"primary", c.Primary}, {"primary_model", c.PrimaryModel}, {"primary_mode", c.PrimaryMode}, {"primary_key", c.PrimaryKey},
			{"verifier", c.Verifier}, {"verifier_model", c.VerifierModel}, {"verifier_mode", c.VerifierMode}, {"verifier_key", c.VerifierKey},
			{"target_conf", c.TargetConf}, {"op_conf", c.OpConf}, {"veto_cache", c.VetoCache},
		}
	}
	return nil
}

func textHelperFields(cfg config.Config) []field {
	t := cfg.TextHelper
	return []field{{"url", t.URL}, {"model", t.Model}, {"key", t.Key}, {"timeout_seconds", t.TimeoutSeconds}}
}

func toMap(fields []field) map[string]any {
	out := make(map[string]any, len(fields))
	for _, f := range fields {
		out[f.name] = f.value
	}
	return out
}

func pairs(fields []field) []ui.Pair {
	out := make([]ui.Pair, 0, len(fields))
	for _, f := range fields {
		out = append(out, ui.Pair{Key: f.name, Value: display(f.value)})
	}
	return out
}

func display(value any) string {
	if text, ok := value.(string); ok && text == "" {
		return "(not set)"
	}
	return fmt.Sprint(value)
}

func renderStatus(out *ui.Printer, cfg config.Config, report statusReport, resolver *keys.Resolver) {
	renderBackend(out, cfg)
	out.Blank()
	renderPaid(out, cfg, report.PaidAPI, resolver)
	out.Blank()
	out.Heading("URLs")
	out.Pairs([]ui.Pair{
		{Key: "local", Value: display(cfg.Backends.Local.URL)},
		{Key: "jev", Value: display(cfg.Backends.Jev.URL)},
		{Key: "custom", Value: display(cfg.Backends.Custom.URL)},
		{Key: "cascade primary", Value: display(cfg.Backends.Cascade.Primary)},
		{Key: "cascade verifier", Value: display(cfg.Backends.Cascade.Verifier)},
		{Key: "text helper", Value: display(cfg.TextHelper.URL)},
	})
	out.Blank()
	out.Heading("Text helper")
	out.Pairs(pairs(textHelperFields(cfg)))
	out.Blank()
	out.Heading("Browser")
	out.Pairs([]ui.Pair{
		{Key: "port", Value: fmt.Sprint(cfg.Browser.Port)},
		{Key: "headless", Value: fmt.Sprint(cfg.Browser.Headless)},
		{Key: "viewport", Value: fmt.Sprintf("%dx%d", cfg.Browser.Viewport.Width, cfg.Browser.Viewport.Height)},
		{Key: "channel", Value: cfg.Browser.Channel},
		{Key: "installed", Value: browserInstallLine(report)},
	})
	out.Blank()
	out.Heading("Config")
	state := "not created yet, defaults apply"
	if report.ConfigFileExists {
		state = "found"
	}
	out.Pairs([]ui.Pair{{Key: "file", Value: report.ConfigFile}, {Key: "state", Value: state}})
}

func renderBackend(out *ui.Printer, cfg config.Config) {
	out.Heading("Backend")
	out.Pairs(append([]ui.Pair{{Key: "active", Value: cfg.Backend}}, pairs(activeFields(cfg))...))
	if cfg.Backend == config.BackendJev {
		out.Status(ui.Info, "jev sends page data to a remote service that may bill per request")
	}
}

func renderPaid(out *ui.Printer, cfg config.Config, paid bool, resolver *keys.Resolver) {
	out.Heading("Paid services")
	out.Pairs([]ui.Pair{{Key: "paid_api", Value: fmt.Sprint(paid)}})
	if !paid {
		out.Status(ui.OK, "run would use no paid service: no remote endpoint has an API key configured")
		return
	}
	for _, service := range paidServices(cfg, resolver) {
		out.Status(ui.Warn, "run would call %s, which may bill per request", service)
	}
}
