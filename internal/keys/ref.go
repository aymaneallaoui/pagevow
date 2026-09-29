// Package keys stores secrets in the OS keychain and resolves references to them.
package keys

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
)

// Kind tells where a referenced secret lives.
type Kind string

// Reference kinds.
const (
	KindKeychain Kind = "keychain"
	KindEnv      Kind = "env"
)

// ErrNotFound is returned when a keychain entry does not exist.
var ErrNotFound = errors.New("key not found")

// ErrNotReference is returned when a value is neither empty nor a keychain: or env: reference.
var ErrNotReference = errors.New("value is not a keychain: or env: reference")

var (
	keychainName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)
	envName      = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
)

// Ref points at a secret without containing it.
type Ref struct {
	Kind Kind
	Name string
}

// IsZero reports whether the reference is empty.
func (r Ref) IsZero() bool { return r.Kind == "" }

// String renders the reference in its config file form.
func (r Ref) String() string {
	if r.IsZero() {
		return ""
	}
	return string(r.Kind) + ":" + r.Name
}

// ParseRef parses "keychain:NAME" or "env:NAME"; an empty string yields the zero Ref.
func ParseRef(s string) (Ref, error) {
	if s == "" {
		return Ref{}, nil
	}
	kind, name, found := strings.Cut(s, ":")
	if !found {
		return Ref{}, ErrNotReference
	}
	switch Kind(kind) {
	case KindKeychain:
		if !keychainName.MatchString(name) {
			return Ref{}, ErrNotReference
		}
	case KindEnv:
		if !envName.MatchString(name) {
			return Ref{}, ErrNotReference
		}
	default:
		return Ref{}, ErrNotReference
	}
	return Ref{Kind: Kind(kind), Name: name}, nil
}

// ValidateName checks that a keychain entry name is well formed.
func ValidateName(name string) error {
	if !keychainName.MatchString(name) {
		return fmt.Errorf("invalid key name %q: use letters, digits, dot, underscore or hyphen, starting with a letter or digit", name)
	}
	return nil
}
