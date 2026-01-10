// Package main is the entry point for switch-a.
package main

import (
	"fmt"
	"os"

	"github.com/e-idea/switch-a/internal/config"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(ExitCodeError)
	}
}

func run() error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}

	fmt.Printf("Switch-A starting on port %s\n", cfg.Port)
	return nil
}
