package model

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"
)

const (
	recordFile     = ".pagevow-model.json"
	maxRecordBytes = 8 << 20
)

// FileRecord is one file of an installed model; SHA256 is empty for a linked model, which is not read.
type FileRecord struct {
	Name   string `json:"name"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256,omitempty"`
}

// Installed is the record of a model that pagevow installed; Dir and AlreadyInstalled are not stored.
type Installed struct {
	Name             string       `json:"name"`
	Dir              string       `json:"-"`
	Source           string       `json:"source"`
	Revision         string       `json:"revision,omitempty"`
	BaseModel        string       `json:"base_model"`
	Files            []FileRecord `json:"files"`
	InstalledAt      time.Time    `json:"installed_at"`
	AlreadyInstalled bool         `json:"-"`
}

// ReadRecord returns the install record inside dir; a directory pagevow did not install gives an error that wraps fs.ErrNotExist.
func ReadRecord(dir string) (Installed, error) {
	file, err := os.Open(filepath.Join(dir, recordFile)) //nolint:gosec // the caller names its own run directory
	if err != nil {
		return Installed{}, fmt.Errorf("read the model record: %w", err)
	}
	defer func() { _ = file.Close() }()
	var rec Installed
	if err := json.NewDecoder(io.LimitReader(file, maxRecordBytes)).Decode(&rec); err != nil {
		return Installed{}, fmt.Errorf("decode the model record in %s: %w", dir, err)
	}
	rec.Dir = dir
	return rec, nil
}

func writeRecord(dir string, rec Installed) error {
	data, err := json.MarshalIndent(rec, "", "  ")
	if err != nil {
		return fmt.Errorf("encode the model record: %w", err)
	}
	tmp, err := os.CreateTemp(dir, ".pagevow-model-*.tmp")
	if err != nil {
		return fmt.Errorf("write the model record: %w", err)
	}
	_, writeErr := tmp.Write(append(data, '\n'))
	closeErr := tmp.Close()
	if err := errors.Join(writeErr, closeErr); err != nil {
		_ = os.Remove(tmp.Name())
		return fmt.Errorf("write the model record: %w", err)
	}
	if err := os.Rename(tmp.Name(), filepath.Join(dir, recordFile)); err != nil {
		_ = os.Remove(tmp.Name())
		return fmt.Errorf("write the model record: %w", err)
	}
	return nil
}
