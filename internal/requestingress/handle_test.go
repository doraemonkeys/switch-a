package requestingress

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func testContext(t testing.TB) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	t.Cleanup(cancel)
	return ctx
}

func startTest(t testing.TB, input io.ReadCloser, length int64, options Options) *Handle {
	t.Helper()
	h, err := Start(testContext(t), &http.Request{Body: input, ContentLength: length, Proto: "HTTP/1.1", ProtoMajor: 1, ProtoMinor: 1}, options)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = h.Close() })
	return h
}
func openTest(t testing.TB, h *Handle) *Reader {
	t.Helper()
	r, err := h.OpenReader("attempt-1")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = r.Close() })
	return r
}
func readAllTest(t testing.TB, r io.Reader) string {
	t.Helper()
	data, err := io.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}
func waitTest(t testing.TB, h *Handle) {
	t.Helper()
	if err := h.Wait(testContext(t)); err != nil {
		t.Fatal(err)
	}
}

func TestSmallBodyStaysInMemoryAndReplays(t *testing.T) {
	budget := NewBudget(5)
	h := startTest(t, io.NopCloser(strings.NewReader("hello")), -1, Options{SharedBudget: budget})
	waitTest(t, h)
	for range 3 {
		r := openTest(t, h)
		if got := readAllTest(t, r); got != "hello" {
			t.Fatal(got)
		}
		if r.BytesRead() != 5 {
			t.Fatal(r.BytesRead())
		}
		_ = r.Close()
	}
	s := h.Snapshot()
	if s.State != Complete || s.ReceivedBytes != 5 || s.MemoryBytes != 5 || s.DiskBytes != 0 {
		t.Fatal(s)
	}
	if got := string(h.Prefix(3)); got != "hel" {
		t.Fatal(got)
	}
	if h.Prefix(0) != nil {
		t.Fatal("zero prefix")
	}
	if budget.Used() != 5 {
		t.Fatal(budget.Used())
	}
	_ = h.Close()
	if budget.Used() != 0 || h.Prefix(9) != nil {
		t.Fatal("store not released")
	}
	if _, err := h.Open(); !errors.Is(err, ErrClosed) {
		t.Fatal(err)
	}
	if _, err := h.Retain(); !errors.Is(err, ErrClosed) {
		t.Fatal(err)
	}
}

func TestRetryReplaysPrefixAndFollowsLiveTail(t *testing.T) {
	input, writer := io.Pipe()
	t.Cleanup(func() { _ = writer.Close() })
	h := startTest(t, input, -1, Options{})
	first := openTest(t, h)
	if _, err := writer.Write([]byte("prefix")); err != nil {
		t.Fatal(err)
	}
	firstBytes := make([]byte, 6)
	if _, err := io.ReadFull(first, firstBytes); err != nil {
		t.Fatal(err)
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	if h.Snapshot().State != Receiving {
		t.Fatal(h.Snapshot())
	}
	second := openTest(t, h)
	if _, err := io.ReadFull(second, firstBytes); err != nil {
		t.Fatal(err)
	}
	if string(firstBytes) != "prefix" {
		t.Fatal(string(firstBytes))
	}
	tail := make(chan string, 1)
	go func() {
		data, err := io.ReadAll(second)
		if err != nil {
			tail <- err.Error()
		} else {
			tail <- string(data)
		}
	}()
	if _, err := writer.Write([]byte("-tail")); err != nil {
		t.Fatal(err)
	}
	_ = writer.Close()
	select {
	case got := <-tail:
		if got != "-tail" {
			t.Fatal(got)
		}
	case <-testContext(t).Done():
		t.Fatal("tail blocked")
	}
	waitTest(t, h)
}

func TestReaderCloseJoinsWaitersWithoutAbortingInput(t *testing.T) {
	input, writer := io.Pipe()
	defer writer.Close()
	h := startTest(t, input, -1, Options{})
	reader := openTest(t, h)
	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := reader.Read(make([]byte, 1))
			if !errors.Is(err, ErrClosed) {
				t.Errorf("read error %v", err)
			}
		}()
	}
	if err := reader.Close(); err != nil {
		t.Fatal(err)
	}
	wg.Wait()
	if h.Snapshot().State != Receiving {
		t.Fatal(h.Snapshot())
	}
	if n, err := reader.Read(nil); n != 0 || !errors.Is(err, ErrClosed) {
		t.Fatal(n, err)
	}
	second := openTest(t, h)
	if n, err := second.Read(nil); n != 0 || err != nil {
		t.Fatal(n, err)
	}
	_ = writer.Close()
	waitTest(t, h)
}

func TestFrozenHeadAndFinalTrailers(t *testing.T) {
	input, writer := io.Pipe()
	trailer := http.Header{"X-Declared": nil}
	req := &http.Request{Body: input, ContentLength: -1, Proto: "HTTP/2.0", ProtoMajor: 2,
		TransferEncoding: []string{"chunked"}, Trailer: trailer}
	observed := make(chan Snapshot, 1)
	h, err := Start(testContext(t), req, Options{OnHead: func(head Head) {
		if len(head.TrailerKeys) != 1 || head.TrailerKeys[0] != "X-Declared" {
			t.Errorf("head %+v", head)
		}
		head.TrailerKeys[0] = "changed"
	}, OnFinish: func(s Snapshot) { observed <- s }})
	if err != nil {
		t.Fatal(err)
	}
	defer h.Close()
	a, b := openTest(t, h), openTest(t, h)
	head := h.Head()
	head.TransferEncoding[0] = "changed"
	if h.Head().TransferEncoding[0] != "chunked" || h.Head().TrailerKeys[0] != "X-Declared" {
		t.Fatal(h.Head())
	}
	trailer["X-Declared"] = []string{"value"}
	trailer["X-Late"] = []string{"late"}
	_ = writer.Close()
	if readAllTest(t, a) != "" || readAllTest(t, b) != "" {
		t.Fatal("unexpected body")
	}
	snapshot := <-observed
	if snapshot.Trailers.Get("X-Late") != "late" {
		t.Fatal(snapshot)
	}
	snapshot.Trailers.Set("X-Late", "mutation")
	final := h.Trailers()
	final.Set("X-Declared", "mutation")
	if h.Trailers().Get("X-Declared") != "value" || h.Trailers().Get("X-Late") != "late" {
		t.Fatal(h.Trailers())
	}
}

func TestDeclaredLimitRejectedBeforeRead(t *testing.T) {
	body := &countBody{Reader: strings.NewReader("too big")}
	_, err := Start(testContext(t), &http.Request{Body: body, ContentLength: 7}, Options{MaxBodyBytes: 3})
	if !errors.Is(err, ErrBodyTooLarge) || body.reads.Load() != 0 {
		t.Fatal(err, body.reads.Load())
	}
}

type countBody struct {
	io.Reader
	reads  atomic.Int64
	closes atomic.Int64
}

func (b *countBody) Read(p []byte) (int, error) { b.reads.Add(1); return b.Reader.Read(p) }
func (b *countBody) Close() error               { b.closes.Add(1); return nil }

func TestSourceFailuresAndActualReceivedBytes(t *testing.T) {
	failure := errors.New("client disconnected")
	for _, tc := range []struct {
		name        string
		body        io.Reader
		length, max int64
		want        error
		received    int64
	}{
		{"limit", strings.NewReader("abcdef"), -1, 3, ErrBodyTooLarge, 6},
		{"short", strings.NewReader("abc"), 5, 0, ErrLengthMismatch, 3},
		{"long", strings.NewReader("abcdef"), 3, 0, ErrLengthMismatch, 6},
		{"zero", strings.NewReader("a"), 0, 0, ErrLengthMismatch, 1},
		{"read", errorReader{failure}, -1, 0, failure, 0},
		{"empty-read", emptyReader{}, -1, 0, io.ErrNoProgress, 0},
		{"invalid-read", invalidReader{}, -1, 0, nil, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body := &countBody{Reader: tc.body}
			h := startTest(t, body, tc.length, Options{MaxBodyBytes: tc.max})
			err := h.Wait(testContext(t))
			if err == nil || (tc.want != nil && !errors.Is(err, tc.want)) {
				t.Fatal(err)
			}
			s := h.Snapshot()
			if s.State != Failed || s.ReceivedBytes != tc.received || body.closes.Load() != 1 {
				t.Fatal(s, body.closes.Load())
			}
			if _, err = h.Open(); err == nil {
				t.Fatal("failed source reopened")
			}
			_ = h.Close()
			if body.closes.Load() != 1 {
				t.Fatal(body.closes.Load())
			}
		})
	}
}

type errorReader struct{ err error }

func (r errorReader) Read([]byte) (int, error) { return 0, r.err }

type emptyReader struct{}

func (emptyReader) Read([]byte) (int, error) { return 0, nil }

type invalidReader struct{}

func (invalidReader) Read([]byte) (int, error) { return -1, nil }

func TestNoBodyAndKnownLength(t *testing.T) {
	for _, input := range []io.ReadCloser{nil, http.NoBody, io.NopCloser(strings.NewReader("abc"))} {
		size := int64(0)
		if input != nil && input != http.NoBody {
			size = 3
		}
		h := startTest(t, input, size, Options{})
		waitTest(t, h)
		if h.Head().HasBody != (size > 0) {
			t.Fatal(h.Head())
		}
		if int64(len(readAllTest(t, openTest(t, h)))) != size {
			t.Fatal(h.Snapshot())
		}
	}
}

func TestCancelAbortAndCloseUnblockInput(t *testing.T) {
	for _, action := range []string{"cancel", "abort", "close"} {
		t.Run(action, func(t *testing.T) {
			input := &interruptBody{started: make(chan struct{}), interrupted: make(chan struct{})}
			ctx, cancel := context.WithCancel(testContext(t))
			defer cancel()
			h, err := Start(ctx, &http.Request{Body: input, ContentLength: -1}, Options{Interrupt: func(error) { input.interrupt() }})
			if err != nil {
				t.Fatal(err)
			}
			defer h.Close()
			r := openTest(t, h)
			<-input.started
			switch action {
			case "cancel":
				cancel()
			case "abort":
				h.Abort(nil)
			case "close":
				_ = h.Close()
			}
			if err = h.Wait(testContext(t)); err == nil {
				t.Fatal("missing abort error")
			}
			if h.Snapshot().State != Aborted {
				t.Fatal(h.Snapshot())
			}
			if _, err = h.Open(); err == nil {
				t.Fatal("aborted source reopened")
			}
			if _, err = r.Read(make([]byte, 1)); err == nil {
				t.Fatal("reader did not abort")
			}
			h.Abort(errors.New("second"))
			_ = h.Close()
			if input.closes.Load() != 1 {
				t.Fatal(input.closes.Load())
			}
		})
	}
}

type interruptBody struct {
	started, interrupted chan struct{}
	once                 sync.Once
	closes               atomic.Int64
}

func (b *interruptBody) Read([]byte) (int, error) {
	close(b.started)
	<-b.interrupted
	return 0, io.ErrClosedPipe
}
func (b *interruptBody) interrupt()   { b.once.Do(func() { close(b.interrupted) }) }
func (b *interruptBody) Close() error { <-b.interrupted; b.closes.Add(1); return nil }

func TestWaitContextDoesNotAbortSource(t *testing.T) {
	input, writer := io.Pipe()
	defer writer.Close()
	h := startTest(t, input, -1, Options{})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := h.Wait(ctx); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if h.Snapshot().State != Receiving {
		t.Fatal(h.Snapshot())
	}
}

func TestMemorySpillSharedBudgetAndDeferredCleanup(t *testing.T) {
	budget := NewBudget(4)
	input, writer := io.Pipe()
	h := startTest(t, input, -1, Options{MemoryBytes: 4, SharedBudget: budget})
	if _, err := writer.Write([]byte("abcd")); err != nil {
		t.Fatal(err)
	}
	reader := openTest(t, h)
	prefix := make([]byte, 4)
	if _, err := io.ReadFull(reader, prefix); err != nil {
		t.Fatal(err)
	}
	if _, err := writer.Write([]byte("efgh")); err != nil {
		t.Fatal(err)
	}
	_ = writer.Close()
	waitTest(t, h)
	release, err := h.Retain()
	if err != nil {
		t.Fatal(err)
	}
	s := h.Snapshot()
	if s.MemoryBytes != 4 || s.DiskBytes != 4 || budget.Used() != 4 {
		t.Fatal(s, budget.Used())
	}
	if got := string(h.Prefix(99)); got != "abcdefgh" {
		t.Fatal(got)
	}
	h.mu.Lock()
	path := h.storage.(temporaryStorage).Name()
	h.mu.Unlock()
	second := startTest(t, io.NopCloser(strings.NewReader("ij")), -1, Options{SharedBudget: budget})
	waitTest(t, second)
	if second.Snapshot().DiskBytes != 2 {
		t.Fatal(second.Snapshot())
	}
	_ = h.Close()
	if got := readAllTest(t, reader); got != "efgh" {
		t.Fatal(got)
	}
	_ = reader.Close()
	if _, err = os.Stat(path); err != nil {
		t.Fatal("reference must retain file", err)
	}
	release()
	release()
	if _, err = os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("file not removed", err)
	}
	if budget.Used() != 0 {
		t.Fatal(budget.Used())
	}
}

func TestConcurrentOpenCloseAndObservers(t *testing.T) {
	var eventsMu sync.Mutex
	events := map[string]int{}
	finish := make(chan struct{})
	h := startTest(t, io.NopCloser(bytes.NewReader(bytes.Repeat([]byte("x"), SegmentBytes*3))), -1, Options{
		OperationID: "op-1", MemoryBytes: 1,
		Trace: func(e Event) {
			if e.OperationID != "op-1" {
				t.Errorf("operation %q", e.OperationID)
			}
			eventsMu.Lock()
			events[e.Name]++
			eventsMu.Unlock()
		},
		OnFinish: func(Snapshot) { close(finish) },
	})
	<-finish
	var wg sync.WaitGroup
	for range 32 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r, err := h.Open()
			if err == nil {
				_, _ = io.Copy(io.Discard, r)
				_ = r.Close()
			}
		}()
	}
	wg.Add(1)
	go func() { defer wg.Done(); _ = h.Close() }()
	wg.Wait()
	_ = h.Close()
	eventsMu.Lock()
	defer eventsMu.Unlock()
	if events["started"] != 1 || events["spilled"] != 1 {
		t.Fatal(events)
	}
}
