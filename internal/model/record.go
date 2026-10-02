package model

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"time"
)

const (
	recordFile       = ".pagevow-model.json"
	linkRecordPrefix = ".pagevow-link-"
	maxRecordBytes   = 8 << 20
)

// ErrBadRecord reports an install record that lacks what proves pagevow installed the model.
var ErrBadRecord = errors.New("incomplete model record")

// FileRecord is one file of an installed model; SHA256 is empty for a linked model, which is not read, and ModTime is set for a copy.
type FileRecord struct {
	Name    string    `json:"name"`
	Size    int64     `json:"size"`
	SHA256  string    `json:"sha256,omitempty"`
	ModTime time.Time `json:"mtime,omitzero"`
}

// Installed is the record of a model that pagevow installed; Dir, Link and AlreadyInstalled are not stored.
type Installed struct {
	Name             string       `json:"name"`
	Dir              string       `json:"-"`
	Source           string       `json:"source"`
	Revision         string       `json:"revision,omitempty"`
	BaseModel        string       `json:"base_model"`
	Files            []FileRecord `json:"files"`
	InstalledAt      time.Time    `json:"installed_at"`
	Link             bool         `json:"-"`
	AlreadyInstalled bool         `json:"-"`
}

// recordPath returns where the install record of dir lives: inside a copied model, or beside a linked one in the runs directory.
func recordPath(dir string) (path string, link bool, err error) {
	info, err := os.Lstat(dir)
	if err != nil {
		return "", false, fmt.Errorf("read the model record: %w", err)
	}
	if info.Mode()&fs.ModeSymlink != 0 {
		return linkRecordPath(filepath.Dir(dir), filepath.Base(dir)), true, nil
	}
	return filepath.Join(dir, recordFile), false, nil
}

func linkRecordPath(runsDir, name string) string {
	return filepath.Join(runsDir, linkRecordPrefix+name+".json")
}

// ReadRecord returns the install record of dir; a directory pagevow did not install gives an error that wraps fs.ErrNotExist.
func ReadRecord(dir string) (Installed, error) {
	path, link, err := recordPath(dir)
	if err != nil {
		return Installed{}, err
	}
	file, err := os.Open(path) //nolint:gosec // the caller names its own run directory
	if err != nil {
		return Installed{}, fmt.Errorf("read the model record: %w", err)
	}
	defer func() { _ = file.Close() }()
	var rec Installed
	if err := json.NewDecoder(io.LimitReader(file, maxRecordBytes)).Decode(&rec); err != nil {
		return Installed{}, fmt.Errorf("decode the model record of %s: %w", dir, err)
	}
	if rec.Name == "" || rec.Source == "" || rec.BaseModel == "" || rec.InstalledAt.IsZero() || !link && len(rec.Files) == 0 {
		return Installed{}, fmt.Errorf("%w: the record of %s lacks the name, source, base model, install time or files", ErrBadRecord, dir)
	}
	if link {
		if target, err := os.Readlink(dir); err != nil || target != rec.Source {
			return Installed{}, fmt.Errorf("%w: the link %s does not point at the source its record names", ErrBadRecord, dir)
		}
	}
	rec.Dir, rec.Link = dir, link
	return rec, nil
}

// writeRecord writes rec to path through a temporary file in the same directory.
func writeRecord(path string, rec Installed) error {
	tmp, err := stageRecord(filepath.Dir(path), rec)
	if err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("write the model record: %w", err)
	}
	return nil
}

// stageRecord writes rec to a new temporary file in dir and returns its path.
func stageRecord(dir string, rec Installed) (string, error) {
	data, err := json.MarshalIndent(rec, "", "  ")
	if err != nil {
		return "", fmt.Errorf("encode the model record: %w", err)
	}
	tmp, err := os.CreateTemp(dir, stagePrefix+"record-*.tmp")
	if err != nil {
		return "", fmt.Errorf("write the model record: %w", err)
	}
	_, writeErr := tmp.Write(append(data, '\n'))
	closeErr := tmp.Close()
	if err := errors.Join(writeErr, closeErr); err != nil {
		_ = os.Remove(tmp.Name())
		return "", fmt.Errorf("write the model record: %w", err)
	}
	return tmp.Name(), nil
}
