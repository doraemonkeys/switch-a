// Package config handles configuration loading and management.
package config

import (
	"errors"
	"fmt"
	"runtime"
	"strings"

	"github.com/spf13/viper"
)

// configureViperPaths sets up config file paths for a viper instance.
// If configPath is provided, it uses that exact path.
// Otherwise, it searches in standard locations.
func configureViperPaths(v *viper.Viper, configPath string) {
	if configPath != "" {
		v.SetConfigFile(configPath)
	} else {
		v.SetConfigName(ConfigFileName)
		v.SetConfigType(ConfigFileType)
		v.AddConfigPath(".")
		v.AddConfigPath("./config")
		// Unix-only system config path
		if runtime.GOOS != "windows" {
			v.AddConfigPath("/etc/switch-a")
		}
	}
}

// Config holds the startup configuration loaded from environment variables or config file.
type Config struct {
	Port           string `mapstructure:"port"`
	AdminPort      string `mapstructure:"admin_port"`
	DBPath         string `mapstructure:"db_path"`
	AdminToken     string `mapstructure:"admin_token"`
	LogPath        string `mapstructure:"log_path"`
	LogMaxSizeMB   int    `mapstructure:"log_max_size_mb"`
	LogMaxKeepDays int    `mapstructure:"log_max_keep_days"`
	ConfigFileUsed string `mapstructure:"-"` // Path to config file that was loaded (not from config)
}

// Load loads configuration from config file and/or environment variables.
// Priority (highest to lowest):
//  1. Environment variables (SWITCHA_*)
//  2. Config file (config.yaml, config.json, etc.)
//  3. Default values
func Load() (*Config, error) {
	return LoadWithPath("")
}

// LoadWithPath loads configuration from the specified config file path.
// If configPath is empty, it searches for config file in default locations.
func LoadWithPath(configPath string) (*Config, error) {
	v := viper.New()

	// Set default values
	v.SetDefault(KeyPort, DefaultPort)
	v.SetDefault(KeyAdminPort, DefaultAdminPort)
	v.SetDefault(KeyDBPath, DefaultDBPath)
	v.SetDefault(KeyLogPath, DefaultLogPath)
	v.SetDefault(KeyLogMaxSizeMB, DefaultLogMaxSizeMB)
	v.SetDefault(KeyLogMaxKeepDays, DefaultLogMaxKeepDays)

	// Configure environment variables
	v.SetEnvPrefix(EnvPrefix)
	v.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))
	v.AutomaticEnv()

	// Bind environment variables explicitly for proper mapping
	_ = v.BindEnv(KeyPort, EnvPort)
	_ = v.BindEnv(KeyAdminPort, EnvAdminPort)
	_ = v.BindEnv(KeyDBPath, EnvDBPath)
	_ = v.BindEnv(KeyAdminToken, EnvAdminToken)
	_ = v.BindEnv(KeyLogPath, EnvLogPath)
	_ = v.BindEnv(KeyLogMaxSizeMB, EnvLogMaxSizeMB)
	_ = v.BindEnv(KeyLogMaxKeepDays, EnvLogMaxKeepDays)

	// Configure config file paths
	configureViperPaths(v, configPath)

	// Read config file (optional unless explicit path was provided)
	var configFileUsed string
	if err := v.ReadInConfig(); err != nil {
		if configPath != "" {
			// If explicit path was provided, any error is fatal
			return nil, fmt.Errorf("failed to read config file: %w", err)
		}
		// Only ignore "not found" errors for default search paths
		// Surface parse errors so users know their config file is malformed
		var configFileNotFoundError viper.ConfigFileNotFoundError
		if !errors.As(err, &configFileNotFoundError) {
			return nil, fmt.Errorf("failed to parse config file: %w", err)
		}
	} else {
		configFileUsed = v.ConfigFileUsed()
	}

	cfg := &Config{}
	if err := v.Unmarshal(cfg); err != nil {
		return nil, fmt.Errorf("failed to unmarshal config: %w", err)
	}

	// Validate required fields
	if cfg.AdminToken == "" {
		return nil, fmt.Errorf("%s is required (set via environment variable or config file)", EnvAdminToken)
	}

	cfg.ConfigFileUsed = configFileUsed
	return cfg, nil
}
