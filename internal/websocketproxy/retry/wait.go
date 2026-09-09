package retry

import (
	"context"
	"time"
)

// TimerWaiter shares the request deadline; a retry must not outlive its client.
type TimerWaiter struct{}

func (TimerWaiter) Wait(ctx context.Context, delay time.Duration) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if delay <= 0 {
		return nil
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-timer.C:
		return ctx.Err()
	case <-ctx.Done():
		return ctx.Err()
	}
}
