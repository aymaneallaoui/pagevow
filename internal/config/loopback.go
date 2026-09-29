package config

import (
	"net/netip"
	"net/url"
	"strings"
)

// IsLoopbackURL reports whether a URL points at this machine: the host localhost or a literal loopback IP address.
// Other names, including subdomains of localhost, are not loopback because a resolver may send them elsewhere.
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
