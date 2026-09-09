package framegate

import (
	"context"
	"errors"
	"net"
	"testing"
	"time"
)

func awaitControls(t *testing.T, g *Gate, n int) {
	t.Helper()
	deadline := time.NewTimer(time.Second)
	defer deadline.Stop()
	tick := time.NewTicker(time.Millisecond)
	defer tick.Stop()
	for {
		g.mu.Lock()
		got := g.controls
		g.mu.Unlock()
		if got == n {
			return
		}
		select {
		case <-tick.C:
		case <-deadline.C:
			t.Fatalf("queued controls = %d, want %d", got, n)
		}
	}
}

func TestControlOwnsNextBoundaryBeforeQueuedData(t *testing.T) {
	var g Gate
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := g.Lock(ctx, nil, false); err != nil {
		t.Fatal(err)
	}
	order := make(chan string, 2)
	dataDone := make(chan error, 1)
	go func() {
		err := g.Lock(ctx, nil, false)
		if err == nil {
			order <- "data"
			g.Unlock()
		}
		dataDone <- err
	}()
	controlDone := make(chan error, 1)
	go func() {
		err := g.Lock(ctx, nil, true)
		if err == nil {
			order <- "control"
			g.Unlock()
		}
		controlDone <- err
	}()
	awaitControls(t, &g, 1)
	g.Unlock()
	if err := <-controlDone; err != nil {
		t.Fatal(err)
	}
	if err := <-dataDone; err != nil {
		t.Fatal(err)
	}
	if first := <-order; first != "control" {
		t.Fatalf("next frame = %s", first)
	}
}

func TestCanceledControlDoesNotStrandData(t *testing.T) {
	var g Gate
	if err := g.Lock(context.Background(), nil, false); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- g.Lock(ctx, nil, true) }()
	awaitControls(t, &g, 1)
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	g.Unlock()
	ctx2, stop := context.WithTimeout(context.Background(), time.Second)
	defer stop()
	if err := g.Lock(ctx2, nil, false); err != nil {
		t.Fatal(err)
	}
	g.Unlock()
}

func TestClosedConnectionWakesQueuedFrames(t *testing.T) {
	var g Gate
	closed := make(chan struct{})
	if err := g.Lock(context.Background(), closed, false); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- g.Lock(context.Background(), closed, true) }()
	awaitControls(t, &g, 1)
	close(closed)
	if err := <-done; !errors.Is(err, net.ErrClosed) {
		t.Fatal(err)
	}
	g.Unlock()
	if err := g.Lock(context.Background(), closed, false); !errors.Is(err, net.ErrClosed) {
		t.Fatal(err)
	}
	// Teardown can reclaim buffers after connection closure without admitting writes.
	if err := g.Lock(context.Background(), nil, false); err != nil {
		t.Fatal(err)
	}
	g.Unlock()
}
