package server

import (
	"fmt"
	"io"
	"os"
	"strings"
)

const logTailWindow = 64 * 1024

// LogTail returns the last lines of the file at path.
func LogTail(path string, lines int) (string, error) {
	if lines <= 0 {
		return "", nil
	}
	file, err := os.Open(path) //nolint:gosec // the caller names its own log file
	if err != nil {
		return "", fmt.Errorf("read log tail: %w", err)
	}
	defer func() { _ = file.Close() }()
	info, err := file.Stat()
	if err != nil {
		return "", fmt.Errorf("read log tail: %w", err)
	}
	start := max(info.Size()-logTailWindow, 0)
	if _, err := file.Seek(start, io.SeekStart); err != nil {
		return "", fmt.Errorf("read log tail: %w", err)
	}
	data, err := io.ReadAll(file)
	if err != nil {
		return "", fmt.Errorf("read log tail: %w", err)
	}
	all := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
	if start > 0 && len(all) > 0 {
		all = all[1:]
	}
	if len(all) > lines {
		all = all[len(all)-lines:]
	}
	return strings.Join(all, "\n"), nil
}
