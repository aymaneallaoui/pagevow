package cli

import (
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"

	"github.com/spf13/cobra"

	"github.com/aymaneallaoui/pagevow/internal/config"
	"github.com/aymaneallaoui/pagevow/internal/keys"
	"github.com/aymaneallaoui/pagevow/internal/ui"
)

const maxKeyBytes = 64 << 10

func (a *app) newKeysCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "keys",
		Short: "Manage keychain entries",
		Long:  "Manage the keychain entries pagevow reads through keychain:NAME references.\nValues are never printed and are never accepted as flags or arguments.",
	}
	cmd.AddCommand(a.newKeysSetCmd(), a.newKeysUnsetCmd(), a.newKeysListCmd())
	return cmd
}

func (a *app) store() (keys.Store, error) { return service[keys.Store](a) }

func (a *app) keyIndex() (*keys.Index, error) {
	path, err := a.configPath()
	if err != nil {
		return nil, err
	}
	return keys.NewIndex(keys.IndexPath(path)), nil
}

func (a *app) newKeysSetCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "set NAME",
		Short: "Store a secret read from stdin or a hidden prompt",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]
			if err := keys.ValidateName(name); err != nil {
				return err
			}
			value, err := a.readSecret(cmd, name)
			if err != nil {
				return err
			}
			store, err := a.store()
			if err != nil {
				return err
			}
			if err := store.Set(name, value); err != nil {
				return err
			}
			index, err := a.keyIndex()
			if err != nil {
				return err
			}
			if err := index.Add(name); err != nil {
				return fmt.Errorf("stored keychain:%s but could not update the key index: %w", name, err)
			}
			out, err := a.printer(cmd.OutOrStdout())
			if err != nil {
				return err
			}
			out.Status(ui.OK, "stored keychain:%s", name)
			return out.Err()
		},
	}
}

func (a *app) readSecret(cmd *cobra.Command, name string) (string, error) {
	interactive, err := service[StdinInteractive](a)
	if err != nil {
		return "", err
	}
	var value string
	if interactive(cmd.InOrStdin()) {
		prompter, err := service[Prompter](a)
		if err != nil {
			return "", err
		}
		value, err = prompter.Secret("Value for " + name)
		if err != nil {
			return "", err
		}
	} else {
		raw, err := io.ReadAll(io.LimitReader(cmd.InOrStdin(), maxKeyBytes+1))
		if err != nil {
			return "", fmt.Errorf("read value from stdin: %w", err)
		}
		if len(raw) > maxKeyBytes {
			return "", fmt.Errorf("value on stdin is longer than %d bytes", maxKeyBytes)
		}
		value = strings.TrimSuffix(strings.TrimSuffix(string(raw), "\n"), "\r")
	}
	if value == "" {
		return "", errors.New("no value given: pipe it on stdin or run in a terminal to be prompted")
	}
	return value, nil
}

func (a *app) newKeysUnsetCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "unset NAME",
		Short: "Remove a keychain entry",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]
			if err := keys.ValidateName(name); err != nil {
				return err
			}
			store, err := a.store()
			if err != nil {
				return err
			}
			out, err := a.printer(cmd.OutOrStdout())
			if err != nil {
				return err
			}
			index, err := a.keyIndex()
			if err != nil {
				return err
			}
			switch err := store.Delete(name); {
			case errors.Is(err, keys.ErrNotFound):
				out.Status(ui.Info, "keychain:%s was not set", name)
			case err != nil:
				return err
			default:
				out.Status(ui.OK, "removed keychain:%s", name)
			}
			if err := index.Remove(name); err != nil {
				return fmt.Errorf("update the key index: %w", err)
			}
			return out.Err()
		},
	}
}

type keyRow struct {
	ref     keys.Ref
	indexed bool
	fields  []string
}

func (r keyRow) source() string {
	var parts []string
	if r.indexed {
		parts = append(parts, "index")
	}
	parts = append(parts, r.fields...)
	return strings.Join(parts, ", ")
}

func (a *app) newKeysListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "Show stored key names and the key references in the config, each with its state",
		Long: "Show every name in the key index and every reference in the config, each with a state:\n" +
			"stored (in the keychain), env (resolved from the environment) or missing.\nValues are never printed.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, _, err := a.loadConfig()
			if err != nil {
				return err
			}
			resolver, err := a.resolver()
			if err != nil {
				return err
			}
			index, err := a.keyIndex()
			if err != nil {
				return err
			}
			names, err := index.Names()
			if err != nil {
				return err
			}
			rows, err := collectKeyRows(cfg, names)
			if err != nil {
				return err
			}
			out, err := a.printer(cmd.OutOrStdout())
			if err != nil {
				return err
			}
			if len(rows) == 0 {
				out.Line("no keys are stored or referenced by the config")
				return out.Err()
			}
			var table [][]string
			var fixes []string
			for _, row := range rows {
				state := keyState(resolver, row.ref)
				table = append(table, []string{row.ref.String(), row.source(), state})
				if state == keyMissing {
					fixes = append(fixes, missingHint(row))
				}
			}
			out.Table([]string{"REFERENCE", "SOURCE", "STATE"}, table)
			if len(fixes) > 0 {
				out.Blank()
				for _, fix := range fixes {
					out.Status(ui.Warn, "%s", fix)
				}
			}
			return out.Err()
		},
	}
}

const (
	keyStored  = "stored"
	keyEnv     = "env"
	keyMissing = "missing"
)

func keyState(resolver *keys.Resolver, ref keys.Ref) string {
	switch {
	case !resolver.Available(ref.String()):
		return keyMissing
	case ref.Kind == keys.KindEnv:
		return keyEnv
	default:
		return keyStored
	}
}

func missingHint(row keyRow) string {
	name := row.ref.Name
	if row.ref.Kind == keys.KindEnv {
		return fmt.Sprintf("%s is not set: export the environment variable, or point %s at another reference", row.ref, strings.Join(row.fields, ", "))
	}
	if row.indexed {
		return fmt.Sprintf("%s is in the key index but not in the keychain: run 'pagevow keys set %s' to store it again, or 'pagevow keys unset %s' to forget it", row.ref, name, name)
	}
	return fmt.Sprintf("%s is referenced by the config but not stored: run 'pagevow keys set %s'", row.ref, name)
}

func collectKeyRows(cfg config.Config, indexed []string) ([]keyRow, error) {
	byRef := map[keys.Ref]*keyRow{}
	row := func(ref keys.Ref) *keyRow {
		if byRef[ref] == nil {
			byRef[ref] = &keyRow{ref: ref}
		}
		return byRef[ref]
	}
	for _, name := range indexed {
		row(keys.Ref{Kind: keys.KindKeychain, Name: name}).indexed = true
	}
	for _, entry := range []struct{ field, reference string }{
		{"backends.jev.key", cfg.Backends.Jev.Key},
		{"backends.custom.key", cfg.Backends.Custom.Key},
		{"backends.cascade.primary_key", cfg.Backends.Cascade.PrimaryKey},
		{"backends.cascade.verifier_key", cfg.Backends.Cascade.VerifierKey},
		{"text_helper.key", cfg.TextHelper.Key},
	} {
		ref, err := keys.ParseRef(entry.reference)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", entry.field, err)
		}
		if ref.IsZero() {
			continue
		}
		r := row(ref)
		r.fields = append(r.fields, entry.field)
	}
	rows := make([]keyRow, 0, len(byRef))
	for _, r := range byRef {
		rows = append(rows, *r)
	}
	slices.SortFunc(rows, func(x, y keyRow) int { return strings.Compare(x.ref.String(), y.ref.String()) })
	return rows, nil
}
