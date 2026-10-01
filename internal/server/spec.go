package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"
)

const (
	defaultStopGrace     = 10 * time.Second
	defaultGuardInterval = time.Second
)

// Guard sets the limits the supervisor enforces on the GPU.
type Guard struct {
	Enabled    bool `json:"enabled"`
	MaxTempC   int  `json:"max_temp_c"`
	MinFreeMiB int  `json:"min_free_mib"`
	IntervalMS int  `json:"interval_ms,omitempty"`
}

// Spec tells a supervisor what to run; it is read from the spec file of the process.
type Spec struct {
	Name        string   `json:"name"`
	Kind        Kind     `json:"kind"`
	Argv        []string `json:"argv"`
	Dir         string   `json:"dir"`
	Env         []string `json:"env,omitempty"`
	Port        int      `json:"port"`
	ReadyURL    string   `json:"ready_url"`
	Log         string   `json:"log"`
	Guard       Guard    `json:"guard"`
	StopGraceMS int      `json:"stop_grace_ms,omitempty"`
}

// StopGrace returns how long the child may take to end after SIGTERM before it is killed.
func (s Spec) StopGrace() time.Duration {
	if s.StopGraceMS <= 0 {
		return defaultStopGrace
	}
	return time.Duration(s.StopGraceMS) * time.Millisecond
}

// Validate reports the first problem that would keep a supervisor from running the spec.
func (s Spec) Validate() error {
	switch {
	case !validName(s.Name):
		return fmt.Errorf("spec: invalid name %q", s.Name)
	case !s.Kind.supervised():
		return fmt.Errorf("spec %s: kind %q is not supervised", s.Name, s.Kind)
	case len(s.Argv) == 0 || s.Argv[0] == "":
		return fmt.Errorf("spec %s: no command", s.Name)
	case s.Log == "":
		return fmt.Errorf("spec %s: no log path", s.Name)
	case s.Port < 0 || s.Port > 65535:
		return fmt.Errorf("spec %s: invalid port %d", s.Name, s.Port)
	}
	for _, entry := range s.Env {
		if name := secretLikeName(entry); name != "" {
			return fmt.Errorf("spec %s: environment entry %s looks like a secret and never belongs in a spec file", s.Name, name)
		}
	}
	return nil
}

func secretLikeName(entry string) string {
	name, _, _ := strings.Cut(entry, "=")
	upper := strings.ToUpper(name)
	for _, suffix := range []string{"_KEY", "_TOKEN", "_SECRET"} {
		if strings.HasSuffix(upper, suffix) {
			return name
		}
	}
	return ""
}

// WriteSpec stores spec in the state directory and returns the file path.
func (s *Store) WriteSpec(spec Spec) (string, error) {
	if err := spec.Validate(); err != nil {
		return "", err
	}
	data, err := json.MarshalIndent(spec, "", "  ")
	if err != nil {
		return "", fmt.Errorf("encode spec %s: %w", spec.Name, err)
	}
	path := s.SpecPath(spec.Name)
	if err := writeFileAtomic(path, append(data, '\n')); err != nil {
		return "", fmt.Errorf("write spec %s: %w", spec.Name, err)
	}
	return path, nil
}

// RemoveSpec deletes the spec file of name; a missing file is not an error.
func (s *Store) RemoveSpec(name string) error {
	if !validName(name) {
		return fmt.Errorf("remove spec: invalid name %q", name)
	}
	if err := os.Remove(s.SpecPath(name)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("remove spec %s: %w", name, err)
	}
	return nil
}

// ReadSpec loads and validates the spec file at path.
func ReadSpec(path string) (Spec, error) {
	data, err := os.ReadFile(path) //nolint:gosec // the path is given on the command line of the supervisor
	if err != nil {
		return Spec{}, fmt.Errorf("read spec: %w", err)
	}
	var spec Spec
	if err := json.Unmarshal(data, &spec); err != nil {
		return Spec{}, fmt.Errorf("decode spec %s: %w", path, err)
	}
	if err := spec.Validate(); err != nil {
		return Spec{}, err
	}
	return spec, nil
}

func (g Guard) interval() time.Duration {
	if g.IntervalMS <= 0 {
		return defaultGuardInterval
	}
	return time.Duration(g.IntervalMS) * time.Millisecond
}

// breach returns why sample breaks the limits of g, or an empty string; a temperature of 0 is no reading.
// The free memory limit does not apply to unified memory, whose reclaimable share is not measured yet.
func (g Guard) breach(sample GPU) string {
	if g.MaxTempC > 0 && sample.TempC > 0 && sample.TempC >= g.MaxTempC {
		return fmt.Sprintf("temperature %d C reached the limit of %d C", sample.TempC, g.MaxTempC)
	}
	if !sample.Unified && sample.FreeMiB <= g.MinFreeMiB {
		return fmt.Sprintf("free GPU memory %d MiB fell to the limit of %d MiB", sample.FreeMiB, g.MinFreeMiB)
	}
	return ""
}

func (g Guard) breachMessage(name string, sample GPU) string {
	reason := g.breach(sample)
	switch {
	case reason == "":
		return ""
	case sample.TempC == 0:
		return fmt.Sprintf("guard: stopped %s: %s (free %d MiB)", name, reason, sample.FreeMiB)
	}
	return fmt.Sprintf("guard: stopped %s: %s (temp %d C, free %d MiB)", name, reason, sample.TempC, sample.FreeMiB)
}

func noReaderMessage(goos, name string) string {
	if goos == "darwin" {
		return fmt.Sprintf("guard: memory reader not available; the memory watch for %s is off", name)
	}
	return fmt.Sprintf("guard: nvidia-smi not found; the GPU watch for %s is off", name)
}

const unifiedGuardNote = "guard: free memory guard is off on unified memory until measured"
