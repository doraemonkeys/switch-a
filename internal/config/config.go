// Package config handles configuration loading and management.
package config

import (
	"errors"
	"os"
)

// Config holds the startup configuration loaded from environment variables.
type Config struct {
	Port       string
	DBPath     string
	AdminToken string
}

// Load loads configuration from environment variables.
func Load() (*Config, error) {
	cfg := &Config{
		Port:       getEnvOrDefault(EnvPort, DefaultPort),
		DBPath:     getEnvOrDefault(EnvDBPath, DefaultDBPath),
		AdminToken: os.Getenv(EnvAdminToken),
	}

	if cfg.AdminToken == "" {
		return nil, errors.New("SWITCHA_ADMIN_TOKEN is required")
	}

	return cfg, nil
}

func getEnvOrDefault(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}
