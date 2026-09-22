package cli

import (
	_ "embed"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/aymaneallaoui/pagevow/internal/ui"
)

//go:embed starter.yaml
var starterTests string

const testsFileName = "pagevow.yaml"

func (a *app) newInitCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "init [DIR]",
		Short: "Write a starter pagevow.yaml tests file",
		Long:  "Write a starter pagevow.yaml with three example tests into DIR (default: the current directory).\nAn existing file is never overwritten.",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			dir := "."
			if len(args) == 1 {
				dir = args[0]
			}
			path := filepath.Join(dir, testsFileName)
			if err := writeStarter(path); err != nil {
				return err
			}
			out, err := a.printer(cmd.OutOrStdout())
			if err != nil {
				return err
			}
			out.Status(ui.OK, "wrote %s", path)
			out.Line("Edit the urls and goals, then run: pagevow run")
			return out.Err()
		},
	}
}

func writeStarter(path string) (err error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return fmt.Errorf("create directory for %s: %w", path, err)
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644) //nolint:gosec // a tests file is a project file that gets committed
	if errors.Is(err, fs.ErrExist) {
		return fmt.Errorf("%s already exists: edit it, or remove it first to start over", path)
	}
	if err != nil {
		return fmt.Errorf("create %s: %w", path, err)
	}
	defer func() {
		if closeErr := file.Close(); err == nil && closeErr != nil {
			err = fmt.Errorf("close %s: %w", path, closeErr)
		}
	}()
	if _, err := file.WriteString(starterTests); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	return nil
}
