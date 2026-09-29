package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const (
	recordSuffix  = ".json"
	specSuffix    = ".spec.json"
	trippedSuffix = ".tripped"
	dirMode       = 0o700
	fileMode      = 0o600
)

// Record describes one managed process; PID is the supervisor for model and text helper, the browser itself otherwise.
type Record struct {
	Name            string    `json:"name"`
	Kind            Kind      `json:"kind"`
	PID             int       `json:"pid"`
	ChildPID        int       `json:"child_pid"`
	ChildPGID       int       `json:"child_pgid"`
	Port            int       `json:"port"`
	Command         []string  `json:"command"`
	Dir             string    `json:"dir"`
	StartedAt       time.Time `json:"started_at"`
	StartTicks      uint64    `json:"start_ticks"`
	ChildStartTicks uint64    `json:"child_start_ticks"`
	Log             string    `json:"log"`
	ReadyURL        string    `json:"ready_url"`
	ProfileDir      string    `json:"profile_dir,omitempty"`
}

// Tripped is a guard message left behind by a supervisor that stopped its process.
type Tripped struct {
	Name    string
	Message string
}

// Store keeps records, spec files and guard messages in one state directory.
type Store struct {
	dir string
}

// NewStore returns a store for dir; the directory is created on the first write.
func NewStore(dir string) *Store {
	if abs, err := filepath.Abs(dir); err == nil {
		dir = abs
	}
	return &Store{dir: dir}
}

// Dir returns the state directory.
func (s *Store) Dir() string { return s.dir }

// SpecPath returns where the spec file of name lives.
func (s *Store) SpecPath(name string) string {
	return filepath.Join(s.dir, name+specSuffix)
}

func (s *Store) recordPath(name string) string {
	return filepath.Join(s.dir, name+recordSuffix)
}

func (s *Store) trippedPath(name string) string {
	return filepath.Join(s.dir, name+trippedSuffix)
}

// Write stores rec atomically with mode 0600.
func (s *Store) Write(rec Record) error {
	if !validName(rec.Name) {
		return fmt.Errorf("write record: invalid name %q", rec.Name)
	}
	if !rec.Kind.valid() {
		return fmt.Errorf("write record %s: invalid kind %q", rec.Name, rec.Kind)
	}
	rec.StartedAt = rec.StartedAt.UTC()
	data, err := json.MarshalIndent(rec, "", "  ")
	if err != nil {
		return fmt.Errorf("write record %s: %w", rec.Name, err)
	}
	if err := writeFileAtomic(s.recordPath(rec.Name), append(data, '\n')); err != nil {
		return fmt.Errorf("write record %s: %w", rec.Name, err)
	}
	return nil
}

// Read returns the record called name, or an error that wraps ErrNotFound.
func (s *Store) Read(name string) (Record, error) {
	if !validName(name) {
		return Record{}, fmt.Errorf("read record: invalid name %q", name)
	}
	rec, err := readRecordFile(s.recordPath(name))
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return Record{}, fmt.Errorf("record %s: %w", name, ErrNotFound)
		}
		return Record{}, err
	}
	if rec.Name != name {
		return Record{}, fmt.Errorf("record %s: file holds name %q", name, rec.Name)
	}
	return rec, nil
}

// List returns every readable record sorted by name; an unreadable file is skipped and named in the returned error.
func (s *Store) List() ([]Record, error) {
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("list records: %w", err)
	}
	var records []Record
	var problems []error
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, recordSuffix) || strings.HasSuffix(name, specSuffix) || strings.HasPrefix(name, ".") {
			continue
		}
		rec, err := readRecordFile(filepath.Join(s.dir, name))
		if err != nil {
			problems = append(problems, err)
			continue
		}
		records = append(records, rec)
	}
	sort.Slice(records, func(i, j int) bool { return records[i].Name < records[j].Name })
	return records, errors.Join(problems...)
}

// Remove deletes the record called name; a missing record is not an error.
func (s *Store) Remove(name string) error {
	if !validName(name) {
		return fmt.Errorf("remove record: invalid name %q", name)
	}
	if err := os.Remove(s.recordPath(name)); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("remove record %s: %w", name, err)
	}
	return nil
}

// WriteTripped stores the guard message for name.
func (s *Store) WriteTripped(name, message string) error {
	if !validName(name) {
		return fmt.Errorf("write tripped: invalid name %q", name)
	}
	if err := writeFileAtomic(s.trippedPath(name), []byte(message+"\n")); err != nil {
		return fmt.Errorf("write tripped %s: %w", name, err)
	}
	return nil
}

// Tripped returns every guard message in the state directory sorted by name.
func (s *Store) Tripped() ([]Tripped, error) {
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("list tripped: %w", err)
	}
	var found []Tripped
	for _, entry := range entries {
		name, ok := strings.CutSuffix(entry.Name(), trippedSuffix)
		if !ok || entry.IsDir() || !validName(name) {
			continue
		}
		data, err := os.ReadFile(s.trippedPath(name))
		if err != nil {
			return nil, fmt.Errorf("read tripped %s: %w", name, err)
		}
		found = append(found, Tripped{Name: name, Message: strings.TrimSpace(string(data))})
	}
	sort.Slice(found, func(i, j int) bool { return found[i].Name < found[j].Name })
	return found, nil
}

// ClearTripped deletes the guard message for name; a missing one is not an error.
func (s *Store) ClearTripped(name string) error {
	if !validName(name) {
		return fmt.Errorf("clear tripped: invalid name %q", name)
	}
	if err := os.Remove(s.trippedPath(name)); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("clear tripped %s: %w", name, err)
	}
	return nil
}

func readRecordFile(path string) (Record, error) {
	data, err := os.ReadFile(path) //nolint:gosec // path is built from the state directory and a validated name
	if err != nil {
		return Record{}, fmt.Errorf("read %s: %w", path, err)
	}
	var rec Record
	if err := json.Unmarshal(data, &rec); err != nil {
		return Record{}, fmt.Errorf("decode %s: %w", path, err)
	}
	return rec, nil
}

func writeFileAtomic(path string, data []byte) (err error) {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, dirMode); err != nil {
		return fmt.Errorf("create directory: %w", err)
	}
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(path)+".*.tmp")
	if err != nil {
		return fmt.Errorf("create temporary file: %w", err)
	}
	defer func() {
		if err != nil {
			_ = os.Remove(tmp.Name())
		}
	}()
	if err = tmp.Chmod(fileMode); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("set mode: %w", err)
	}
	if _, err = tmp.Write(data); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write: %w", err)
	}
	if err = tmp.Close(); err != nil {
		return fmt.Errorf("close: %w", err)
	}
	if err = os.Rename(tmp.Name(), path); err != nil {
		return fmt.Errorf("rename: %w", err)
	}
	return nil
}
