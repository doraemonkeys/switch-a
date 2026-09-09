package retry

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestTimerWaiter(t *testing.T) {
	w := TimerWaiter{}
	for _, delay := range []time.Duration{0, -1, time.Nanosecond} {
		if err := w.Wait(context.Background(), delay); err != nil {
			t.Fatal(err)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := w.Wait(ctx, 0); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled zero-delay wait: %v", err)
	}
	ctx, cancel = context.WithTimeout(context.Background(), time.Millisecond)
	defer cancel()
	if err := w.Wait(ctx, time.Hour); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("canceled pending wait: %v", err)
	}
}
