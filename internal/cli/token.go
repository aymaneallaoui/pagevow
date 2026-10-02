package cli

import "strings"

// tokenFrom returns the first non-empty environment variable of names, then the keychain reference, then an empty token.
func (a *app) tokenFrom(names []string, keyRef string) (string, error) {
	lookup, err := service[LookupEnv](a)
	if err != nil {
		return "", err
	}
	for _, name := range names {
		if value, ok := lookup(name); ok && strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value), nil
		}
	}
	resolver, err := a.resolver()
	if err != nil {
		return "", err
	}
	// An unreadable or empty keychain means no token: the request then says the repository is missing or private.
	token, _ := resolver.Resolve(keyRef)
	return strings.TrimSpace(token), nil
}
