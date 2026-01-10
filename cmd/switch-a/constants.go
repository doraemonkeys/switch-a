// Package main is the entry point for switch-a.
package main

import "time"

// Exit codes.
const ExitCodeError = 1

// ShutdownTimeout is the timeout for graceful server shutdown.
const ShutdownTimeout = 30 * time.Second
