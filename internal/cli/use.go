package cli

import (
	"errors"
	"fmt"
	"net/url"
	"slices"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"github.com/aymaneallaoui/pagevow/internal/config"
	"github.com/aymaneallaoui/pagevow/internal/keys"
	"github.com/aymaneallaoui/pagevow/internal/ui"
)

type backendSpec struct {
	flagKeys map[string]string
	required []requiredField
}

type requiredField struct {
	flag, key, title, example string
	current                   func(config.Config) string
}

var backendSpecs = map[string]backendSpec{
	config.BackendLocal: {flagKeys: map[string]string{
		"url": "backends.local.url", "model": "backends.local.model", "mode": "backends.local.mode",
	}},
	config.BackendJev: {flagKeys: map[string]string{
		"url": "backends.jev.url", "key": "backends.jev.key",
	}},
	config.BackendCustom: {
		flagKeys: map[string]string{"url": "backends.custom.url", "key": "backends.custom.key"},
		required: []requiredField{{flag: "url", key: "backends.custom.url", title: "URL of the custom backend", example: "http://127.0.0.1:8080",
			current: func(c config.Config) string { return c.Backends.Custom.URL }}},
	},
	config.BackendCascade: {flagKeys: map[string]string{
		"primary": "backends.cascade.primary", "verifier": "backends.cascade.verifier",
		"target-conf": "backends.cascade.target_conf", "veto-cache": "backends.cascade.veto_cache",
	}},
}

func (a *app) newUseCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:       "use local|jev|custom|cascade",
		Short:     "Choose the decision backend",
		Long:      "Choose the decision backend and store it in the config file.\n\nFlags by backend:\n  local    --url --model --mode\n  jev      --url --key\n  custom   --url --key\n  cascade  --primary --verifier --target-conf --veto-cache\n\n--key takes a reference (keychain:NAME or env:NAME), never the secret itself.",
		Args:      cobra.ExactArgs(1),
		ValidArgs: config.BackendNames(),
		RunE: func(cmd *cobra.Command, args []string) error {
			return a.runUse(cmd, args[0])
		},
	}
	flags := cmd.Flags()
	flags.String("url", "", "backend URL (local, jev, custom)")
	flags.String("model", "", "local model name")
	flags.String("mode", "", "local model precision mode, for example nf4, int8, bf16")
	flags.String("key", "", "API key reference: keychain:NAME or env:NAME (jev, custom)")
	flags.String("primary", "", "primary model URL (cascade)")
	flags.String("verifier", "", "verifier model URL (cascade)")
	flags.Float64("target-conf", 0, "confidence below which the verifier is asked, 0 to 1 (cascade)")
	flags.Bool("veto-cache", true, "reuse a verifier override on the same page (cascade)")
	return cmd
}

func (a *app) runUse(cmd *cobra.Command, name string) error {
	spec, ok := backendSpecs[name]
	if !ok {
		return fmt.Errorf("unknown backend %q: choose one of %s", name, strings.Join(config.BackendNames(), ", "))
	}
	if err := rejectForeignFlags(cmd, name, spec); err != nil {
		return err
	}
	updates := map[string]any{"backend": name}
	if err := collectFlagUpdates(cmd, spec, updates); err != nil {
		return err
	}

	cfg, path, err := a.loadConfig()
	if err != nil {
		return err
	}
	if err := a.fillRequired(cmd, name, spec, cfg, updates); err != nil {
		return err
	}
	if err := config.Save(path, updates); err != nil {
		return err
	}

	cfg, _, err = a.loadConfig()
	if err != nil {
		return err
	}
	out, err := a.printer(cmd.OutOrStdout())
	if err != nil {
		return err
	}
	out.Status(ui.OK, "backend set to %s (%s)", name, path)
	renderBackend(out, cfg)
	a.warnMissingKey(out, cfg)
	return out.Err()
}

func rejectForeignFlags(cmd *cobra.Command, name string, spec backendSpec) error {
	var problem error
	cmd.Flags().Visit(func(flag *pflag.Flag) {
		if problem != nil || flag.Name == "config" {
			return
		}
		if _, ok := spec.flagKeys[flag.Name]; !ok {
			problem = fmt.Errorf("--%s does not apply to backend %s; it applies to %s", flag.Name, name, backendsUsing(flag.Name))
		}
	})
	return problem
}

func backendsUsing(flag string) string {
	var names []string
	for _, name := range config.BackendNames() {
		if _, ok := backendSpecs[name].flagKeys[flag]; ok {
			names = append(names, name)
		}
	}
	slices.Sort(names)
	return strings.Join(names, ", ")
}

func collectFlagUpdates(cmd *cobra.Command, spec backendSpec, updates map[string]any) error {
	for flagName, key := range spec.flagKeys {
		flag := cmd.Flags().Lookup(flagName)
		if flag == nil || !flag.Changed {
			continue
		}
		switch flag.Value.Type() {
		case "float64":
			value, err := cmd.Flags().GetFloat64(flagName)
			if err != nil {
				return fmt.Errorf("read --%s: %w", flagName, err)
			}
			updates[key] = value
		case "bool":
			value, err := cmd.Flags().GetBool(flagName)
			if err != nil {
				return fmt.Errorf("read --%s: %w", flagName, err)
			}
			updates[key] = value
		default:
			value := flag.Value.String()
			if flagName == "url" || flagName == "primary" || flagName == "verifier" {
				if err := validateURL(flagName, value); err != nil {
					return err
				}
			}
			updates[key] = value
		}
	}
	return nil
}

func (a *app) fillRequired(cmd *cobra.Command, name string, spec backendSpec, cfg config.Config, updates map[string]any) error {
	var missing []requiredField
	for _, req := range spec.required {
		if _, given := updates[req.key]; !given && req.current(cfg) == "" {
			missing = append(missing, req)
		}
	}
	if len(missing) == 0 {
		return nil
	}
	interactive, err := service[StdinInteractive](a)
	if err != nil {
		return err
	}
	if !interactive(cmd.InOrStdin()) {
		flags := make([]string, len(missing))
		for i, req := range missing {
			flags[i] = "--" + req.flag
		}
		return fmt.Errorf("use %s: %s is required and no value is configured", name, strings.Join(flags, ", "))
	}
	prompter, err := service[Prompter](a)
	if err != nil {
		return err
	}
	for _, req := range missing {
		value, err := prompter.Input(req.title, req.example)
		if err != nil {
			return err
		}
		value = strings.TrimSpace(value)
		if value == "" {
			return fmt.Errorf("use %s: --%s is required", name, req.flag)
		}
		if err := validateURL(req.flag, value); err != nil {
			return err
		}
		updates[req.key] = value
	}
	return nil
}

func validateURL(flag, value string) error {
	parsed, err := url.Parse(value)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
		return fmt.Errorf("--%s: %q is not an http or https URL", flag, value)
	}
	return nil
}

func (a *app) warnMissingKey(out *ui.Printer, cfg config.Config) {
	var reference string
	switch cfg.Backend {
	case config.BackendJev:
		reference = cfg.Backends.Jev.Key
	case config.BackendCustom:
		reference = cfg.Backends.Custom.Key
	}
	ref, err := keys.ParseRef(reference)
	if err != nil || ref.IsZero() {
		return
	}
	resolver, err := a.resolver()
	if err != nil {
		return
	}
	_, err = resolver.Resolve(reference)
	if err == nil || (!errors.Is(err, keys.ErrNotFound) && !errors.Is(err, keys.ErrEmptyValue)) {
		return
	}
	if ref.Kind == keys.KindKeychain {
		out.Status(ui.Warn, "no key stored yet: run \"pagevow keys set %s\"", ref.Name)
		return
	}
	out.Status(ui.Warn, "environment variable %s is not set", ref.Name)
}
