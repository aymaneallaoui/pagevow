package config

import (
	"net/netip"
	"net/url"
	"strings"
)

// IsLoopbackURL reports whether a URL points at this machine: localhost or a loopback IP address.
// An unparseable URL is not loopback.
func IsLoopbackURL(raw string) bool {
	parsed, err := url.Parse(raw)
	if err != nil {
		return false
	}
	host := strings.ToLower(parsed.Hostname())
	if host == "localhost" || strings.HasSuffix(host, ".localhost") {
		return true
	}
	addr, err := netip.ParseAddr(host)
	if err != nil {
		return false
	}
	return addr.Unmap().IsLoopback()
}
