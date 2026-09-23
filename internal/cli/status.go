package cli

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/aymaneallaoui/pagevow/internal/config"
	"github.com/aymaneallaoui/pagevow/internal/ui"
)

type field struct {
	name  string
	value any
}

type statusReport struct {
	ConfigFile       string         `json:"config_file"`
	ConfigFileExists bool           `json:"config_file_exists"`
	PaidAPI          bool           `json:"paid_api"`
	Backend          map[string]any `json:"backend"`
	URLs             map[string]any `json:"urls"`
	TextHelper       map[string]any `json:"text_helper"`
	Browser          map[string]any `json:"browser"`
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
			_, statErr := os.Stat(path)
			report := buildStatus(cfg, path, statErr == nil)
			if asJSON {
				return writeJSON(cmd, report)
			}
			out, err := a.printer(cmd.OutOrStdout())
			if err != nil {
				return err
			}
			renderStatus(out, cfg, report)
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

func buildStatus(cfg config.Config, path string, exists bool) statusReport {
	return statusReport{
		ConfigFile:       path,
		ConfigFileExists: exists,
		PaidAPI:          len(paidServices(cfg)) > 0,
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

func paidServices(cfg config.Config) []string {
	var services []string
	if cfg.Backend == config.BackendJev {
		services = append(services, fmt.Sprintf("the jev decision backend at %s", cfg.Backends.Jev.URL))
	}
	if helper := cfg.TextHelper.URL; helper != "" && !config.IsLoopbackURL(helper) {
		services = append(services, fmt.Sprintf("the text helper at %s", helper))
	}
	return services
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
		return []field{{"primary", c.Primary}, {"verifier", c.Verifier}, {"target_conf", c.TargetConf}, {"veto_cache", c.VetoCache}}
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

func renderStatus(out *ui.Printer, cfg config.Config, report statusReport) {
	renderBackend(out, cfg)
	out.Blank()
	renderPaid(out, cfg, report.PaidAPI)
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

func renderPaid(out *ui.Printer, cfg config.Config, paid bool) {
	out.Heading("Paid services")
	out.Pairs([]ui.Pair{{Key: "paid_api", Value: fmt.Sprint(paid)}})
	if !paid {
		out.Status(ui.OK, "run would use no paid service: the backend and the text helper are local or not set")
		return
	}
	for _, service := range paidServices(cfg) {
		out.Status(ui.Warn, "run would call %s, which may bill per request", service)
	}
}
