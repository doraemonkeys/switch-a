// Package requestingress owns one immutable, replayable view of a client upload.
package requestingress

import (
	"context"
	"errors"
	"io"
	"net/http"
	"os"
	"sync"
)

const (
	DefaultMemoryBytes        int64 = 1 << 20
	DefaultSharedMemoryBytes  int64 = 64 << 20
	SegmentBytes                    = 32 << 10
	minimumMemorySegmentBytes       = 4 << 10
)

var (
	ErrBodyTooLarge   = errors.New("request body exceeds maximum size")
	ErrLengthMismatch = errors.New("request body differs from declared content length")
	ErrClosed         = errors.New("request ingress closed")
	ErrAborted        = errors.New("request ingress aborted")
)

type State string

const (
	Receiving State = "receiving"
	Complete  State = "complete"
	Failed    State = "failed"
	Aborted   State = "aborted"
)

type Head struct {
	Protocol         string
	ProtocolMajor    int
	ProtocolMinor    int
	ContentLength    int64
	TransferEncoding []string
	TrailerKeys      []string
	HasBody          bool
}

type Snapshot struct {
	State         State
	ReceivedBytes int64
	MemoryBytes   int64
	DiskBytes     int64
	Err           error
	Trailers      http.Header
	CleanupErr    error
	FailureKind   FailureKind
}

type Event struct {
	Name          string
	OperationID   string
	AttemptID     string
	State         State
	ReceivedBytes int64
	MemoryBytes   int64
	DiskBytes     int64
	ReaderBytes   int64
	Err           error
}

// Storage permits deterministic disk failure tests without coupling consumers to files.
type Storage interface {
	io.ReaderAt
	io.Writer
	io.Closer
	Remove() error
}

type Options struct {
	// TrailerSnapshot supplies protocol-owned final metadata after input EOF.
	TrailerSnapshot func() http.Header
	MaxBodyBytes    int64
	OperationID     string
	// Interrupt must unblock a pending input Read; net/http Body.Close alone can wait for it.
	Interrupt func(error)
	OnHead    func(Head)
	// OnChunk borrows immutable input only until it returns; observers must bound retained evidence.
	OnChunk func([]byte)
	// Callbacks run inline outside locks. They may signal cancellation but must not join Close or Wait.
	OnFinish func(Snapshot)
	// OnFailure reports a source failure once, including replay storage faults after OnFinish.
	OnFailure func(Snapshot)
	Trace     func(Event)
	// Zero values select the named defaults. A negative memory limit forces disk storage.
	MemoryBytes   int64
	SharedBudget  *Budget
	CreateStorage func() (Storage, error)
}

type Budget struct {
	mu    sync.Mutex
	limit int64
	used  int64
}

func NewBudget(limit int64) *Budget { return &Budget{limit: limit} }
func (b *Budget) Used() int64       { b.mu.Lock(); defer b.mu.Unlock(); return b.used }
func (b *Budget) acquire(minimum, desired int64) int64 {
	b.mu.Lock()
	defer b.mu.Unlock()
	n := min(desired, b.limit-b.used)
	if n < minimum {
		return 0
	}
	b.used += n
	return n
}
func (b *Budget) release(n int64) { b.mu.Lock(); b.used -= n; b.mu.Unlock() }

var processBudget = NewBudget(DefaultSharedMemoryBytes)

type temporaryStorage struct{ *os.File }

func (s temporaryStorage) Remove() error { return os.Remove(s.Name()) }
func createTemporaryStorage() (Storage, error) {
	f, err := os.CreateTemp("", "switch-a-ingress-*")
	if err != nil {
		return nil, err
	}
	return temporaryStorage{f}, nil
}

// AwaitContext is deliberately not retained by any owner.
func awaitContext(ctx context.Context, done <-chan struct{}) error {
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
