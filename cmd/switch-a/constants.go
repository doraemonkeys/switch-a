// Package main is the entry point for switch-a.
package main

import (
	"time"

	"switch-a/internal/defaults"
)

// Exit codes.
const ExitCodeError = 1

// ShutdownTimeout allows in-flight requests to complete during graceful shutdown.
// 30 seconds is sufficient for most LLM inference requests to finish.
const ShutdownTimeout = 30 * time.Second

// LogCleanupInterval runs daily since log retention is measured in days.
const LogCleanupInterval = 24 * time.Hour

const DefaultLogRetentionDays = defaults.LogRetentionDays

// HealthCleanupInterval balances memory usage against cleanup overhead.
// 5 minutes keeps stale records bounded while avoiding frequent cleanup cycles.
const HealthCleanupInterval = 5 * time.Minute

// HealthCleanupMaxAge removes health failure records older than 10 minutes
// to ensure stale failures don't permanently mark healthy providers as unhealthy.
const HealthCleanupMaxAge = 10 * time.Minute

// StickyCacheCleanupInterval removes expired sticky sessions periodically.
// 5 minutes balances memory efficiency against cleanup overhead.
const StickyCacheCleanupInterval = 5 * time.Minute
