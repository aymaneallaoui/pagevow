package config_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/aymaneallaoui/pagevow/internal/config"
)

func TestIsLoopbackURL(t *testing.T) {
	cases := map[string]bool{
		"http://127.0.0.1:8009":         true,
		"http://127.1.2.3/v1":           true,
		"http://localhost:8010":         true,
		"http://LOCALHOST":              true,
		"http://api.localhost:3000":     false,
		"http://localhost.":             false,
		"http://a.b.localhost":          false,
		"http://127.0.0.1.":             false,
		"http://[::ffff:7f00:1]:80":     true,
		"http://[::ffff:10.0.0.1]:80":   false,
		"http://127.255.255.254":        true,
		"http://128.0.0.1":              false,
		"http://[::1]:8009":             true,
		"http://[::ffff:127.0.0.1]:80":  true,
		"http://192.168.1.20:8009":      false,
		"http://10.0.0.5":               false,
		"http://[fe80::1]:8009":         false,
		"http://0.0.0.0:8009":           false,
		"https://api.typesafe.ai":       false,
		"https://localhost.example.com": false,
		"http://127.0.0.1.example.com":  false,
		"http://%zz":                    false,
		"":                              false,
	}
	for raw, want := range cases {
		assert.Equal(t, want, config.IsLoopbackURL(raw), raw)
	}
}
