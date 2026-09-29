// Package config loads and validates the pagevow configuration: file, then environment, then flags.
package config

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
)

// Backend names accepted in the backend field.
const (
	BackendLocal   = "local"
	BackendJev     = "jev"
	BackendCustom  = "custom"
	BackendCascade = "cascade"
)

// Screenshot policies accepted in run.screenshots.
const (
	ScreenshotsFinal  = "final"
	ScreenshotsFailed = "failed"
	ScreenshotsAll    = "all"
)

// EnvPrefix prefixes every environment variable that overrides a config key.
const EnvPrefix = "PAGEVOW"

// Config is the typed form of the user config file.
type Config struct {
	Backend    string     `mapstructure:"backend"`
	Backends   Backends   `mapstructure:"backends"`
	TextHelper TextHelper `mapstructure:"text_helper"`
	Browser    Browser    `mapstructure:"browser"`
	Run        Run        `mapstructure:"run"`
	Guards     Guards     `mapstructure:"guards"`
}

// Backends holds the settings of every decision backend.
type Backends struct {
	Local   Local   `mapstructure:"local"`
	Jev     Remote  `mapstructure:"jev"`
	Custom  Remote  `mapstructure:"custom"`
	Cascade Cascade `mapstructure:"cascade"`
}

// Local describes a model server that pagevow starts on this machine.
type Local struct {
	URL   string `mapstructure:"url"`
	Model string `mapstructure:"model"`
	Mode  string `mapstructure:"mode"`
}

// Remote describes an HTTP backend and the reference to its API key.
type Remote struct {
	URL string `mapstructure:"url"`
	Key string `mapstructure:"key"`
}

// Cascade describes a primary model checked by a verifier model.
type Cascade struct {
	Primary    string  `mapstructure:"primary"`
	Verifier   string  `mapstructure:"verifier"`
	TargetConf float64 `mapstructure:"target_conf"`
	VetoCache  bool    `mapstructure:"veto_cache"`
}

// TextHelper describes the optional text model that produces field values.
type TextHelper struct {
	URL            string `mapstructure:"url"`
	Model          string `mapstructure:"model"`
	Key            string `mapstructure:"key"`
	TimeoutSeconds int    `mapstructure:"timeout_seconds"`
	// Reasoning is "none" to switch the helper's reasoning off; empty keeps the provider default.
	Reasoning string `mapstructure:"reasoning"`
}

// Browser describes how the test browser is launched.
type Browser struct {
	Port     int      `mapstructure:"port"`
	Headless bool     `mapstructure:"headless"`
	Viewport Viewport `mapstructure:"viewport"`
	Channel  string   `mapstructure:"channel"`
}

// Viewport is the browser window size in CSS pixels.
type Viewport struct {
	Width  int `mapstructure:"width"`
	Height int `mapstructure:"height"`
}

// Run holds the defaults of the run command.
type Run struct {
	Retries        int    `mapstructure:"retries"`
	TimeoutSeconds int    `mapstructure:"timeout_seconds"`
	Screenshots    string `mapstructure:"screenshots"`
	MaxSteps       int    `mapstructure:"max_steps"`
}

// Guards holds the optional agent safety gates.
type Guards struct {
	LoopGuard      bool    `mapstructure:"loop_guard"`
	DoneMinConf    float64 `mapstructure:"done_min_conf"`
	BlockedMinConf float64 `mapstructure:"blocked_min_conf"`
}

// BackendNames lists every valid value of the backend field.
func BackendNames() []string {
	return []string{BackendLocal, BackendJev, BackendCustom, BackendCascade}
}

// Defaults returns the configuration used when nothing else is set.
func Defaults() Config {
	return Config{
		Backend: BackendLocal,
		Backends: Backends{
			Local:   Local{URL: "http://127.0.0.1:8009", Model: "jev-4b", Mode: "nf4"},
			Jev:     Remote{URL: "https://api.typesafe.ai", Key: "keychain:typesafe"},
			Custom:  Remote{},
			Cascade: Cascade{Primary: "http://127.0.0.1:8009", Verifier: "http://127.0.0.1:8010", TargetConf: 0.5, VetoCache: true},
		},
		TextHelper: TextHelper{Key: "keychain:text-helper", TimeoutSeconds: 20},
		Browser:    Browser{Port: 9333, Headless: true, Viewport: Viewport{Width: 1480, Height: 780}, Channel: "chrome-for-testing"},
		Run:        Run{Retries: 1, TimeoutSeconds: 120, Screenshots: ScreenshotsFailed, MaxSteps: 60},
	}
}

// DefaultPath returns the config file location under the user config directory.
func DefaultPath(userConfigDir func() (string, error)) (string, error) {
	dir, err := userConfigDir()
	if err != nil {
		return "", fmt.Errorf("locate user config directory: %w", err)
	}
	return filepath.Join(dir, "pagevow", "config.yaml"), nil
}

// SystemPath returns the config file location for the current user.
func SystemPath() (string, error) { return DefaultPath(os.UserConfigDir) }

// Keys lists every dotted config key, for example "backends.local.url".
func Keys() []string {
	var keys []string
	walk("", reflect.ValueOf(Defaults()), func(key string, _ any) { keys = append(keys, key) })
	return keys
}

// EnvName returns the environment variable that overrides a dotted config key.
func EnvName(key string) string {
	return EnvPrefix + "_" + strings.ToUpper(strings.ReplaceAll(key, ".", "_"))
}

func walk(prefix string, v reflect.Value, visit func(key string, value any)) {
	t := v.Type()
	for i := range t.NumField() {
		tag := t.Field(i).Tag.Get("mapstructure")
		if tag == "" {
			continue
		}
		key := tag
		if prefix != "" {
			key = prefix + "." + tag
		}
		field := v.Field(i)
		if field.Kind() == reflect.Struct {
			walk(key, field, visit)
			continue
		}
		visit(key, field.Interface())
	}
}
