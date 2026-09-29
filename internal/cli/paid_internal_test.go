package cli

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/aymaneallaoui/pagevow/internal/config"
	"github.com/aymaneallaoui/pagevow/internal/keys"
)

func resolverWith(store *keys.Memory, env map[string]string) *keys.Resolver {
	return keys.NewResolver(store, func(name string) (string, bool) { value, ok := env[name]; return value, ok })
}

func TestPaidServicesAreDecidedByDestinationAndKey(t *testing.T) {
	store := keys.NewMemory()
	_ = store.Set("hosted", "sk-live-value")
	_ = store.Set("placeholder", placeholderKey)
	resolver := resolverWith(store, map[string]string{"MY_KEY": "sk-env-value", "EMPTY": ""})

	withBackend := func(backend string, mutate func(*config.Config)) config.Config {
		cfg := config.Defaults()
		cfg.Backend = backend
		mutate(&cfg)
		return cfg
	}
	cases := []struct {
		name string
		cfg  config.Config
		want []string
	}{
		{"defaults", config.Defaults(), nil},
		{"jev with a stored key", withBackend(config.BackendJev, func(c *config.Config) { c.Backends.Jev.Key = "keychain:hosted" }),
			[]string{"the jev decision backend at https://api.typesafe.ai"}},
		{"custom at the hosted api", withBackend(config.BackendCustom, func(c *config.Config) {
			c.Backends.Custom = config.Remote{URL: "https://api.typesafe.ai", Key: "env:MY_KEY"}
		}), []string{"the custom decision backend at https://api.typesafe.ai"}},
		{"custom with an unset variable", withBackend(config.BackendCustom, func(c *config.Config) {
			c.Backends.Custom = config.Remote{URL: "https://api.typesafe.ai", Key: "env:EMPTY"}
		}), nil},
		{"custom with a missing keychain entry", withBackend(config.BackendCustom, func(c *config.Config) {
			c.Backends.Custom = config.Remote{URL: "https://api.typesafe.ai", Key: "keychain:absent"}
		}), nil},
		{"custom with the placeholder key", withBackend(config.BackendCustom, func(c *config.Config) {
			c.Backends.Custom = config.Remote{URL: "https://api.typesafe.ai", Key: "keychain:placeholder"}
		}), nil},
		{"custom without a key", withBackend(config.BackendCustom, func(c *config.Config) {
			c.Backends.Custom = config.Remote{URL: "https://api.typesafe.ai"}
		}), nil},
		{"custom on loopback with a key", withBackend(config.BackendCustom, func(c *config.Config) {
			c.Backends.Custom = config.Remote{URL: "http://127.0.0.1:1", Key: "env:MY_KEY"}
		}), nil},
		{"custom without a url", withBackend(config.BackendCustom, func(c *config.Config) { c.Backends.Custom.Key = "env:MY_KEY" }), nil},
		{"cascade at public urls has no key", withBackend(config.BackendCascade, func(c *config.Config) {
			c.Backends.Cascade.Primary, c.Backends.Cascade.Verifier = "https://a.example.test", "https://b.example.test"
		}), nil},
		{"only the active backend counts", withBackend(config.BackendLocal, func(c *config.Config) {
			c.Backends.Jev.Key = "keychain:hosted"
		}), nil},
		{"text helper with a key", withBackend(config.BackendLocal, func(c *config.Config) {
			c.TextHelper.URL, c.TextHelper.Key = "https://helper.example.test/v1", "env:MY_KEY"
		}), []string{"the text helper at https://helper.example.test/v1"}},
		{"text helper on a localhost subdomain", withBackend(config.BackendLocal, func(c *config.Config) {
			c.TextHelper.URL, c.TextHelper.Key = "http://helper.localhost:8081", "env:MY_KEY"
		}), []string{"the text helper at http://helper.localhost:8081"}},
		{"backend and helper together", withBackend(config.BackendJev, func(c *config.Config) {
			c.Backends.Jev.Key = "keychain:hosted"
			c.TextHelper.URL, c.TextHelper.Key = "https://helper.example.test/v1", "env:MY_KEY"
		}), []string{"the jev decision backend at https://api.typesafe.ai", "the text helper at https://helper.example.test/v1"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, paidServices(tc.cfg, resolver))
		})
	}
	assert.Nil(t, paidServices(config.Defaults(), nil))
	custom := config.Defaults()
	custom.Backend = config.BackendCustom
	custom.Backends.Custom = config.Remote{URL: "https://api.typesafe.ai", Key: "env:MY_KEY"}
	assert.Nil(t, paidServices(custom, nil), "without a resolver no key can be read")
}
