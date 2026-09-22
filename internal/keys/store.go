package keys

import (
	"errors"
	"fmt"
	"sync"

	"github.com/zalando/go-keyring"
)

// ServiceName is the keychain service under which pagevow stores its entries.
const ServiceName = "pagevow"

// Store reads and writes named secrets.
type Store interface {
	Get(name string) (string, error)
	Set(name, value string) error
	Delete(name string) error
}

// Keychain is a Store backed by the operating system keychain.
type Keychain struct{}

// NewKeychain returns a Store over the OS keychain.
func NewKeychain() Keychain { return Keychain{} }

// Get returns the secret stored under name.
func (Keychain) Get(name string) (string, error) {
	value, err := keyring.Get(ServiceName, name)
	if errors.Is(err, keyring.ErrNotFound) {
		return "", fmt.Errorf("keychain entry %q: %w", name, ErrNotFound)
	}
	if err != nil {
		return "", fmt.Errorf("read keychain entry %q: %w", name, err)
	}
	return value, nil
}

// Set stores value under name.
func (Keychain) Set(name, value string) error {
	if err := keyring.Set(ServiceName, name, value); err != nil {
		return fmt.Errorf("write keychain entry %q: %w", name, err)
	}
	return nil
}

// Delete removes the entry under name.
func (Keychain) Delete(name string) error {
	err := keyring.Delete(ServiceName, name)
	if errors.Is(err, keyring.ErrNotFound) {
		return fmt.Errorf("keychain entry %q: %w", name, ErrNotFound)
	}
	if err != nil {
		return fmt.Errorf("delete keychain entry %q: %w", name, err)
	}
	return nil
}

// Memory is an in-process Store for tests and dry runs.
type Memory struct {
	mu      sync.Mutex
	entries map[string]string
}

// NewMemory returns an empty in-memory Store.
func NewMemory() *Memory { return &Memory{entries: map[string]string{}} }

// Get returns the secret stored under name.
func (m *Memory) Get(name string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	value, ok := m.entries[name]
	if !ok {
		return "", fmt.Errorf("keychain entry %q: %w", name, ErrNotFound)
	}
	return value, nil
}

// Set stores value under name.
func (m *Memory) Set(name, value string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.entries[name] = value
	return nil
}

// Delete removes the entry under name.
func (m *Memory) Delete(name string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.entries[name]; !ok {
		return fmt.Errorf("keychain entry %q: %w", name, ErrNotFound)
	}
	delete(m.entries, name)
	return nil
}
