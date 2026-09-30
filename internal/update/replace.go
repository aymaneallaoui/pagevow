package update

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
)

const oldSuffix = ".old"

func replaceExecutable(goos, exe, fresh string) error {
	if goos == windows {
		return replaceAside(exe, fresh)
	}
	if err := os.Rename(fresh, exe); err != nil {
		return fmt.Errorf("replace %s: %w", exe, err)
	}
	return nil
}

func replaceAside(exe, fresh string) error {
	old := exe + oldSuffix
	if err := os.Remove(old); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("remove the previous %s: %w", old, err)
	}
	if err := os.Rename(exe, old); err != nil {
		return fmt.Errorf("move %s aside: %w", exe, err)
	}
	if err := os.Rename(fresh, exe); err != nil {
		moveErr := fmt.Errorf("replace %s: %w", exe, err)
		if restoreErr := os.Rename(old, exe); restoreErr != nil {
			return errors.Join(moveErr, fmt.Errorf("restore %s from %s: %w", exe, old, restoreErr))
		}
		return moveErr
	}
	return nil
}

func removeOld(exe string) { _ = os.Remove(exe + oldSuffix) }
