package config

import (
	"errors"
	"fmt"
	"io/fs"
	"math"
	"os"
	"reflect"
	"strconv"
	"strings"

	"github.com/spf13/pflag"
	"github.com/spf13/viper"
)

// Options controls one Load call.
type Options struct {
	// File is the config file path; a missing file is not an error.
	File string
	// Flags maps a dotted config key to the command line flag that overrides it.
	Flags map[string]*pflag.Flag
	// LookupEnv reads the legacy environment variables; it defaults to os.LookupEnv.
	LookupEnv func(string) (string, bool)
}

type legacyVar struct {
	env  string
	keys []string
	// asRef stores "env:NAME" instead of the value, so secrets never enter the config struct.
	asRef bool
	// seconds parses the value as a number of seconds.
	seconds bool
}

var legacyVars = []legacyVar{
	{env: "TYPESAFE_BASE_URL", keys: []string{"backends.custom.url"}},
	{env: "TYPESAFE_API_KEY", keys: []string{"backends.jev.key", "backends.custom.key"}, asRef: true},
	{env: "JEV_VERIFIER_BASE_URL", keys: []string{"backends.cascade.verifier"}},
	{env: "TEXT_MODEL_BASE_URL", keys: []string{"text_helper.url"}},
	{env: "TEXT_MODEL", keys: []string{"text_helper.model"}},
	{env: "TEXT_MODEL_API_KEY", keys: []string{"text_helper.key"}, asRef: true},
	{env: "TEXT_TIMEOUT_S", keys: []string{"text_helper.timeout_seconds"}, seconds: true},
}

// Load reads the configuration with precedence file, then PAGEVOW_* environment, then flags.
func Load(opts Options) (Config, error) {
	v, err := newViper(opts)
	if err != nil {
		return Config{}, err
	}
	if opts.File != "" {
		v.SetConfigFile(opts.File)
		if err := v.ReadInConfig(); err != nil && !isMissing(err) {
			return Config{}, fmt.Errorf("read config file %s: %w", opts.File, err)
		}
	}
	for key, flag := range opts.Flags {
		if err := v.BindPFlag(key, flag); err != nil {
			return Config{}, fmt.Errorf("bind flag for %s: %w", key, err)
		}
	}
	cfg, err := decode(v)
	if err != nil {
		return Config{}, err
	}
	if err := Validate(cfg); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

func newViper(opts Options) (*viper.Viper, error) {
	v := viper.New()
	v.SetEnvPrefix(EnvPrefix)
	v.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))
	v.AutomaticEnv()
	setDefaults(v)
	lookup := opts.LookupEnv
	if lookup == nil {
		lookup = os.LookupEnv
	}
	if err := applyLegacy(v, lookup); err != nil {
		return nil, err
	}
	return v, nil
}

func setDefaults(v *viper.Viper) {
	walk("", reflect.ValueOf(Defaults()), v.SetDefault)
}

func applyLegacy(v *viper.Viper, lookup func(string) (string, bool)) error {
	for _, legacy := range legacyVars {
		raw, ok := lookup(legacy.env)
		if !ok || raw == "" {
			continue
		}
		var value any = raw
		switch {
		case legacy.asRef:
			value = "env:" + legacy.env
		case legacy.seconds:
			seconds, err := strconv.ParseFloat(raw, 64)
			if err != nil || seconds <= 0 {
				return fmt.Errorf("%s: %q is not a positive number of seconds", legacy.env, raw)
			}
			value = int(math.Ceil(seconds))
		}
		for _, key := range legacy.keys {
			v.SetDefault(key, value)
		}
	}
	return nil
}

func decode(v *viper.Viper) (Config, error) {
	var cfg Config
	if err := v.Unmarshal(&cfg); err != nil {
		return Config{}, fmt.Errorf("decode configuration: %w", err)
	}
	return cfg, nil
}

func isMissing(err error) bool {
	var notFound viper.ConfigFileNotFoundError
	return errors.As(err, &notFound) || errors.Is(err, fs.ErrNotExist)
}
