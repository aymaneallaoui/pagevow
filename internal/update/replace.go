package update

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
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
	removeErr := os.Remove(old)
	if errors.Is(removeErr, fs.ErrNotExist) {
		removeErr = nil
	}
	if removeErr != nil {
		unique, err := uniqueOldName(exe)
		if err != nil {
			return fmt.Errorf("remove the previous %s: %w", old, removeErr)
		}
		old = unique
	}
	if err := os.Rename(exe, old); err != nil {
		if removeErr != nil {
			return fmt.Errorf("move %s aside: %w; the previous %s cannot be removed (%w), run 'pagevow stop' to end a pagevow process still running from it",
				exe, err, exe+oldSuffix, removeErr)
		}
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

func uniqueOldName(exe string) (string, error) {
	var suffix [6]byte
	if _, err := rand.Read(suffix[:]); err != nil {
		return "", fmt.Errorf("name the backup of %s: %w", exe, err)
	}
	return exe + oldSuffix + "-" + hex.EncodeToString(suffix[:]), nil
}

// RemoveLeftover deletes the backups of an earlier Windows update next to exe, skipping any that a running process still holds.
func RemoveLeftover(exe, goos string) {
	if goos != windows {
		return
	}
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		exe = resolved
	}
	_ = os.Remove(exe + oldSuffix)
	dir := filepath.Dir(exe)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	prefix := filepath.Base(exe) + oldSuffix + "-"
	for _, entry := range entries {
		if entry.Type().IsRegular() && strings.HasPrefix(entry.Name(), prefix) {
			_ = os.Remove(filepath.Join(dir, entry.Name()))
		}
	}
}
