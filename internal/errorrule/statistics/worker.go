package statistics

import (
	"context"
	"time"
)

const (
	StatsFlushInterval   = 5 * time.Second
	StatsShutdownTimeout = 5 * time.Second
)

// Run owns no caller context in the accumulator. Cancellation stops periodic
// work and triggers one independently bounded best-effort shutdown flush.
func (a *Accumulator) Run(ctx context.Context) error {
	ticker := time.NewTicker(StatsFlushInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			shutdownCtx, cancel := context.WithTimeout(context.Background(), StatsShutdownTimeout)
			defer cancel()
			return a.Flush(shutdownCtx)
		case <-ticker.C:
			if err := a.Flush(ctx); err != nil {
				// Exact deltas were restored; the next tick retries without making
				// transient persistence availability part of the request path.
				continue
			}
		}
	}
}
