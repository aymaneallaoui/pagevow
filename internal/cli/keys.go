package cli

import (
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/spf13/cobra"

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
			switch err := store.Delete(name); {
			case errors.Is(err, keys.ErrNotFound):
				out.Status(ui.Info, "keychain:%s was not set", name)
			case err != nil:
				return err
			default:
				out.Status(ui.OK, "removed keychain:%s", name)
			}
			return out.Err()
		},
	}
}

func (a *app) newKeysListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "Show the key references in the config and whether each one resolves",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, _, err := a.loadConfig()
			if err != nil {
				return err
			}
			resolver, err := a.resolver()
			if err != nil {
				return err
			}
			out, err := a.printer(cmd.OutOrStdout())
			if err != nil {
				return err
			}
			var rows [][]string
			for _, entry := range []struct{ field, reference string }{
				{"backends.jev.key", cfg.Backends.Jev.Key},
				{"backends.custom.key", cfg.Backends.Custom.Key},
				{"text_helper.key", cfg.TextHelper.Key},
			} {
				if entry.reference == "" {
					rows = append(rows, []string{entry.field, "(none)", "-"})
					continue
				}
				state := "missing"
				if resolver.Available(entry.reference) {
					state = "available"
				}
				rows = append(rows, []string{entry.field, entry.reference, state})
			}
			out.Table([]string{"FIELD", "REFERENCE", "STATE"}, rows)
			return out.Err()
		},
	}
}
