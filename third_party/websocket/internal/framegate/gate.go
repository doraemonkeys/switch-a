// Package framegate arbitrates complete frames; bytes within a frame cannot be interleaved.
package framegate

import (
	"context"
	"net"
	"sync"
)

type Gate struct {
	mu       sync.Mutex
	held     bool
	controls int
	changed  chan struct{}
}

func (g *Gate) notify() {
	if g.changed != nil {
		close(g.changed)
	}
	g.changed = make(chan struct{})
}

// Lock gives waiting control frames the next frame boundary, before more data.
// closed is nil only when the owner is reclaiming buffers after transport closure.
func (g *Gate) Lock(ctx context.Context, closed <-chan struct{}, control bool) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.changed == nil {
		g.changed = make(chan struct{})
	}
	if control {
		g.controls++
		defer func() { g.controls--; g.notify() }()
	}
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		select {
		case <-closed:
			return net.ErrClosed
		default:
		}
		if !g.held && (control || g.controls == 0) {
			g.held = true
			return nil
		}
		changed := g.changed
		g.mu.Unlock()
		select {
		case <-changed:
		case <-ctx.Done():
		case <-closed:
		}
		g.mu.Lock()
	}
}

func (g *Gate) Unlock() {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.held = false
	g.notify()
}
