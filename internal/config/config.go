// Package config loads and validates the pagevow configuration: file, then environment, then flags.
package config

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"

	"github.com/aymaneallaoui/pagevow/internal/mode"
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

// Model server modes accepted in backends.local.mode and the cascade mode fields.
const (
	ModeNF4     = mode.NF4
	ModeInt8    = mode.Int8
	ModeDefault = mode.Default
)

// EnvPrefix prefixes every environment variable that overrides a config key.
const EnvPrefix = "PAGEVOW"

// Config is the typed form of the user config file.
type Config struct {
	Backend    string     `mapstructure:"backend"`
	Backends   Backends   `mapstructure:"backends"`
	Server     Server     `mapstructure:"server"`
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
	Primary       string  `mapstructure:"primary"`
	Verifier      string  `mapstructure:"verifier"`
	PrimaryKey    string  `mapstructure:"primary_key"`
	VerifierKey   string  `mapstructure:"verifier_key"`
	PrimaryModel  string  `mapstructure:"primary_model"`
	PrimaryMode   string  `mapstructure:"primary_mode"`
	VerifierModel string  `mapstructure:"verifier_model"`
	VerifierMode  string  `mapstructure:"verifier_mode"`
	TargetConf    float64 `mapstructure:"target_conf"`
	VetoCache     bool    `mapstructure:"veto_cache"`
}

// Server holds the settings of the local model servers that pagevow starts.
type Server struct {
	KevDir              string `mapstructure:"kev_dir"`
	StartTimeoutSeconds int    `mapstructure:"start_timeout_seconds"`
	GPUWatch            bool   `mapstructure:"gpu_watch"`
	GPUMaxTempC         int    `mapstructure:"gpu_max_temp_c"`
	GPUMinFreeMiB       int    `mapstructure:"gpu_min_free_mib"`
}

// TextHelper describes the optional text model that produces field values.
type TextHelper struct {
	URL            string `mapstructure:"url"`
	Model          string `mapstructure:"model"`
	Key            string `mapstructure:"key"`
	TimeoutSeconds int    `mapstructure:"timeout_seconds"`
	// Reasoning is "none" to switch the helper's reasoning off; empty keeps the provider default.
	Reasoning string          `mapstructure:"reasoning"`
	Local     LocalTextHelper `mapstructure:"local"`
}

// LocalTextHelper describes a llama-server that pagevow starts for the text helper.
type LocalTextHelper struct {
	Enabled             bool   `mapstructure:"enabled"`
	Repo                string `mapstructure:"repo"`
	File                string `mapstructure:"file"`
	Alias               string `mapstructure:"alias"`
	GPULayers           int    `mapstructure:"gpu_layers"`
	StartTimeoutSeconds int    `mapstructure:"start_timeout_seconds"`
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

// ModeNames lists every valid model server mode.
func ModeNames() []string {
	return mode.Names()
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
			Local:  Local{URL: "http://127.0.0.1:8009", Model: "jev-4b", Mode: "nf4"},
			Jev:    Remote{URL: "https://api.typesafe.ai", Key: "keychain:typesafe"},
			Custom: Remote{},
			Cascade: Cascade{
				Primary: "http://127.0.0.1:8009", Verifier: "http://127.0.0.1:8010",
				PrimaryModel: "jev-08b-d1a", PrimaryMode: ModeDefault, VerifierModel: "jev-4b", VerifierMode: ModeNF4,
				TargetConf: 0.5, VetoCache: true,
			},
		},
		Server: Server{KevDir: "~/kev", StartTimeoutSeconds: 600, GPUWatch: true, GPUMaxTempC: 87, GPUMinFreeMiB: 1500},
		TextHelper: TextHelper{
			Key: "keychain:text-helper", TimeoutSeconds: 20,
			Local: LocalTextHelper{
				Repo: "unsloth/Qwen3-1.7B-GGUF", File: "Qwen3-1.7B-Q4_K_M.gguf", Alias: "qwen3-1.7b",
				GPULayers: 99, StartTimeoutSeconds: 900,
			},
		},
		Browser: Browser{Port: 9333, Headless: true, Viewport: Viewport{Width: 1480, Height: 780}, Channel: "chrome-for-testing"},
		Run:     Run{Retries: 1, TimeoutSeconds: 120, Screenshots: ScreenshotsFailed, MaxSteps: 60},
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

// StateDir returns the directory of the process records under the user cache directory.
func StateDir(userCacheDir func() (string, error)) (string, error) {
	return cacheSubdir(userCacheDir, "run")
}

// LogDir returns the directory of the process logs under the user cache directory.
func LogDir(userCacheDir func() (string, error)) (string, error) {
	return cacheSubdir(userCacheDir, "logs")
}

// ProfilesDir returns the directory of the browser profiles under the user cache directory.
func ProfilesDir(userCacheDir func() (string, error)) (string, error) {
	return cacheSubdir(userCacheDir, "profiles")
}

// BrowserDir returns the directory of the Chrome for Testing installs under the user cache directory.
func BrowserDir(userCacheDir func() (string, error)) (string, error) {
	return cacheSubdir(userCacheDir, "browser")
}

// ManagedProfileDir returns the profile directory of the browser that pagevow start manages.
func ManagedProfileDir(userCacheDir func() (string, error)) (string, error) {
	dir, err := ProfilesDir(userCacheDir)
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "managed"), nil
}

func cacheSubdir(userCacheDir func() (string, error), name string) (string, error) {
	dir, err := userCacheDir()
	if err != nil {
		return "", fmt.Errorf("locate user cache directory: %w", err)
	}
	return filepath.Join(dir, "pagevow", name), nil
}

// ExpandHome replaces a leading "~" or "~/" with the home directory and returns any other path unchanged.
func ExpandHome(path string, userHomeDir func() (string, error)) (string, error) {
	if path != "~" && !strings.HasPrefix(path, "~/") && !strings.HasPrefix(path, "~"+string(filepath.Separator)) {
		return path, nil
	}
	home, err := userHomeDir()
	if err != nil {
		return "", fmt.Errorf("expand %q: locate home directory: %w", path, err)
	}
	return filepath.Join(home, path[1:]), nil
}

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
