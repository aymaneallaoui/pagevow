package model

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"unicode"
)

const (
	adapterConfigFile  = "adapter_config.json"
	adapterWeightsFile = "adapter_model.safetensors"
	headFile           = "head.pt"

	maxConfigBytes = 1 << 20
	maxBaseLen     = 256
)

// ErrInvalid reports a directory that is not a usable run directory.
var ErrInvalid = errors.New("invalid model directory")

func requiredFiles() [3]string {
	return [3]string{adapterConfigFile, adapterWeightsFile, headFile}
}

// Validate checks that dir holds the files a model server needs and returns the base model that the adapter names.
func Validate(dir string) (string, error) {
	return validate(dir, false)
}

// validate checks the required files; contained also refuses a file that is a symbolic link out of dir, which a copy cannot take.
func validate(dir string, contained bool) (string, error) {
	for _, name := range requiredFiles() {
		full := filepath.Join(dir, name)
		info, err := os.Lstat(full)
		if err == nil && info.Mode()&fs.ModeSymlink != 0 {
			if contained && linkOutside(dir, full) {
				return "", fmt.Errorf("%w: %s is a symbolic link out of the source directory; pass --link, or download with --local-dir", ErrInvalid, name)
			}
			info, err = os.Stat(full)
		}
		switch {
		case errors.Is(err, os.ErrNotExist):
			return "", fmt.Errorf("%w: %s has no %s", ErrInvalid, dir, name)
		case err != nil:
			return "", fmt.Errorf("check %s in %s: %w", name, dir, err)
		case !info.Mode().IsRegular() || info.Size() == 0:
			return "", fmt.Errorf("%w: %s in %s is not a non-empty file", ErrInvalid, name, dir)
		}
	}
	return baseModelOf(dir)
}

// linkOutside reports whether the link at full resolves to a path outside dir.
func linkOutside(dir, full string) bool {
	target, err := filepath.EvalSymlinks(full)
	return err == nil && !sameOrInside(dir, target)
}

func baseModelOf(dir string) (string, error) {
	file, err := os.Open(filepath.Join(dir, adapterConfigFile)) //nolint:gosec // the caller names its own run directory
	if err != nil {
		return "", fmt.Errorf("read %s in %s: %w", adapterConfigFile, dir, err)
	}
	defer func() { _ = file.Close() }()
	var config struct {
		Base string `json:"base_model_name_or_path"`
	}
	if err := json.NewDecoder(io.LimitReader(file, maxConfigBytes)).Decode(&config); err != nil {
		return "", fmt.Errorf("%w: decode %s in %s: %w", ErrInvalid, adapterConfigFile, dir, err)
	}
	base := strings.TrimSpace(config.Base)
	switch {
	case base == "":
		return "", fmt.Errorf("%w: %s in %s names no base model", ErrInvalid, adapterConfigFile, dir)
	case len(base) > maxBaseLen || strings.ContainsFunc(base, unicode.IsControl):
		return "", fmt.Errorf("%w: the base model name in %s of %s is not plain text", ErrInvalid, adapterConfigFile, dir)
	}
	return base, nil
}
