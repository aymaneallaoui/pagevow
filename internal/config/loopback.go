package config

import (
	"errors"
	"net/netip"
	"net/url"
	"strings"

	"github.com/aymaneallaoui/pagevow/internal/secret"
)

// ErrKeyOverPlainHTTP is returned when a key would travel over plain http to a host that is not this machine.
var ErrKeyOverPlainHTTP = errors.New("refusing to send a key over plain http to a host that is not loopback")

// CheckKeyTransport returns ErrKeyOverPlainHTTP when key is a real secret and rawURL is plain http to a non-loopback host.
func CheckKeyTransport(rawURL, key string) error {
	if !secret.Is(key) {
		return nil
	}
	parsed, err := url.Parse(rawURL)
	if err != nil || parsed.Scheme != "http" || IsLoopbackURL(rawURL) {
		return nil
	}
	return ErrKeyOverPlainHTTP
}

// IsLoopbackURL reports whether a URL names localhost or a literal loopback IP address, never another name a resolver might send elsewhere.
func IsLoopbackURL(raw string) bool {
	parsed, err := url.Parse(raw)
	if err != nil {
		return false
	}
	host := strings.ToLower(parsed.Hostname())
	if host == "localhost" {
		return true
	}
	addr, err := netip.ParseAddr(host)
	if err != nil {
		return false
	}
	return addr.Unmap().IsLoopback()
}
