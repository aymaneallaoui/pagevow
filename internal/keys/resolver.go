package keys

import (
	"errors"
	"fmt"
)

// ErrEmptyValue is returned when a referenced environment variable is unset or empty.
var ErrEmptyValue = errors.New("value is empty")

// Resolver turns references into secret values.
type Resolver struct {
	store     Store
	lookupEnv func(string) (string, bool)
}

// NewResolver builds a Resolver over a keychain Store and an environment lookup.
func NewResolver(store Store, lookupEnv func(string) (string, bool)) *Resolver {
	return &Resolver{store: store, lookupEnv: lookupEnv}
}

// Resolve returns the secret a reference points at; an empty reference resolves to an empty string.
func (r *Resolver) Resolve(ref string) (string, error) {
	parsed, err := ParseRef(ref)
	if err != nil {
		return "", err
	}
	switch parsed.Kind {
	case "":
		return "", nil
	case KindKeychain:
		return r.store.Get(parsed.Name)
	case KindEnv:
		value, ok := r.lookupEnv(parsed.Name)
		if !ok || value == "" {
			return "", fmt.Errorf("environment variable %s: %w", parsed.Name, ErrEmptyValue)
		}
		return value, nil
	}
	return "", ErrNotReference
}

// Available reports whether a reference currently resolves, without exposing the value.
func (r *Resolver) Available(ref string) bool {
	value, err := r.Resolve(ref)
	return err == nil && value != ""
}
