package keys

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
)

// IndexFileName is the name of the key index file that sits next to the config file.
const IndexFileName = "keys.json"

// IndexPath returns the key index location for a config file path.
func IndexPath(configPath string) string {
	return filepath.Join(filepath.Dir(configPath), IndexFileName)
}

// Index records the names of stored keychain entries, never their values.
type Index struct {
	path string
}

// NewIndex returns an Index backed by the file at path.
func NewIndex(path string) *Index { return &Index{path: path} }

type indexFile struct {
	Names []string `json:"names"`
}

// Names returns the sorted indexed names; a missing file yields none.
func (i *Index) Names() ([]string, error) {
	raw, err := os.ReadFile(i.path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read key index %s: %w", i.path, err)
	}
	var file indexFile
	if err := json.Unmarshal(raw, &file); err != nil {
		return nil, fmt.Errorf("parse key index %s (delete it and run keys set again): %w", i.path, err)
	}
	slices.Sort(file.Names)
	return slices.Compact(file.Names), nil
}

// Add records name in the index.
func (i *Index) Add(name string) error {
	names, err := i.Names()
	if err != nil {
		return err
	}
	if slices.Contains(names, name) {
		return nil
	}
	return i.write(append(names, name))
}

// Remove drops name from the index; an absent name is not an error.
func (i *Index) Remove(name string) error {
	names, err := i.Names()
	if err != nil {
		return err
	}
	kept := slices.DeleteFunc(slices.Clone(names), func(n string) bool { return n == name })
	if len(kept) == len(names) {
		return nil
	}
	return i.write(kept)
}

func (i *Index) write(names []string) (err error) {
	slices.Sort(names)
	raw, err := json.MarshalIndent(indexFile{Names: names}, "", "  ")
	if err != nil {
		return fmt.Errorf("encode key index: %w", err)
	}
	dir := filepath.Dir(i.path)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return fmt.Errorf("create directory for key index: %w", err)
	}
	tmp, err := os.CreateTemp(dir, IndexFileName+".*")
	if err != nil {
		return fmt.Errorf("create key index: %w", err)
	}
	defer func() {
		if err != nil {
			_ = os.Remove(tmp.Name())
		}
	}()
	if _, err = tmp.Write(append(raw, '\n')); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write key index: %w", err)
	}
	if err = tmp.Close(); err != nil {
		return fmt.Errorf("close key index: %w", err)
	}
	if err = os.Rename(tmp.Name(), i.path); err != nil {
		return fmt.Errorf("replace key index %s: %w", i.path, err)
	}
	return nil
}
