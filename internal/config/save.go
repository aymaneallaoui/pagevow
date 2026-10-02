package config

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/viper"
)

// Save merges updates into the config file at path and writes back only the keys already in the file plus the updates.
func Save(path string, updates map[string]any) error {
	file := viper.New()
	file.SetConfigFile(path)
	if err := file.ReadInConfig(); err != nil && !isMissing(err) {
		return fmt.Errorf("read config file %s: %w", path, err)
	}
	for key, value := range updates {
		file.Set(key, value)
	}

	check := viper.New()
	setDefaults(check)
	if err := check.MergeConfigMap(file.AllSettings()); err != nil {
		return fmt.Errorf("merge configuration: %w", err)
	}
	cfg, err := decode(check)
	if err != nil {
		return err
	}
	if err := Validate(cfg); err != nil {
		return err
	}

	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return fmt.Errorf("create config directory: %w", err)
	}
	if err := file.WriteConfigAs(path); err != nil {
		return fmt.Errorf("write config file %s: %w", path, err)
	}
	return nil
}
