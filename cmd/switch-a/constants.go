// Package main is the entry point for switch-a.
package main

import (
	"time"

	"switch-a/internal/defaults"
)

// Exit codes.
const ExitCodeError = 1

// ShutdownTimeout is the timeout for graceful server shutdown.
const ShutdownTimeout = 30 * time.Second

// LogCleanupInterval is the interval between log cleanup runs.
const LogCleanupInterval = 24 * time.Hour

// DefaultLogRetentionDays is the default number of days to retain request logs.
const DefaultLogRetentionDays = defaults.LogRetentionDays
