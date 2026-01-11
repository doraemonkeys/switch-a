// Package config handles configuration loading and management.
package config

// Environment variable names.
const (
	EnvPort       = "SWITCHA_PORT"
	EnvAdminPort  = "SWITCHA_ADMIN_PORT"
	EnvDBPath     = "SWITCHA_DB_PATH"
	EnvAdminToken = "SWITCHA_ADMIN_TOKEN"
)

// Default configuration values.
const (
	DefaultPort      = "28080"
	DefaultAdminPort = "28081"
	DefaultDBPath    = "./data.db"
)
