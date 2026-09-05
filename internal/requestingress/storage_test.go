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
)

type testStorage struct {
	data       bytes.Buffer
	createErr  error
	writeErr   error
	readErr    error
	shortWrite bool
	shortRead  bool
	closeErr   error
	removeErr  error
	closes     atomic.Int64
	removes    atomic.Int64
}

func (s *testStorage) Write(p []byte) (int, error) {
	if s.writeErr != nil {
		return 0, s.writeErr
	}
	if s.shortWrite {
		return 0, nil
	}
	return s.data.Write(p)
}
func (s *testStorage) ReadAt(p []byte, offset int64) (int, error) {
	if s.readErr != nil {
		return 0, s.readErr
	}
	if s.shortRead {
		return 0, nil
	}
	return bytes.NewReader(s.data.Bytes()).ReadAt(p, offset)
}
func (s *testStorage) Close() error  { s.closes.Add(1); return s.closeErr }
func (s *testStorage) Remove() error { s.removes.Add(1); return s.removeErr }
func (s *testStorage) create() (Storage, error) {
	if s.createErr != nil {
		return nil, s.createErr
	}
	return s, nil
}

func TestStorageFailures(t *testing.T) {
	failure := errors.New("disk failure")
	for _, tc := range []struct {
		name    string
		storage *testStorage
		want    error
	}{
		{"create", &testStorage{createErr: failure}, failure},
		{"write", &testStorage{writeErr: failure}, failure},
		{"short-write", &testStorage{shortWrite: true}, io.ErrShortWrite},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := startTest(t, io.NopCloser(strings.NewReader("body")), -1, Options{MemoryBytes: -1, CreateStorage: tc.storage.create})
			if err := h.Wait(testContext(t)); !errors.Is(err, tc.want) {
				t.Fatal(err)
			}
			if h.Snapshot().State != Failed {
				t.Fatal(h.Snapshot())
			}
			_ = h.Close()
			want := int64(1)
			if tc.storage.createErr != nil {
				want = 0
			}
			if tc.storage.closes.Load() != want || tc.storage.removes.Load() != want {
				t.Fatal(tc.storage.closes.Load(), tc.storage.removes.Load())
			}
		})
	}
}
func TestStorageReadFailureFailsSource(t *testing.T) {
	for _, short := range []bool{false, true} {
		t.Run(map[bool]string{false: "error", true: "short"}[short], func(t *testing.T) {
			storage := &testStorage{shortRead: short}
			if !short {
				storage.readErr = io.ErrUnexpectedEOF
			}
			h := startTest(t, io.NopCloser(strings.NewReader("body")), -1, Options{MemoryBytes: -1, CreateStorage: storage.create})
			waitTest(t, h)
			reader := openTest(t, h)
			if n, err := reader.Read(make([]byte, 4)); n != 0 || !errors.Is(err, io.ErrUnexpectedEOF) {
				t.Fatal(n, err)
			}
			if h.Snapshot().State != Failed {
				t.Fatal(h.Snapshot())
			}
			if _, err := reader.Read(make([]byte, 4)); !errors.Is(err, io.ErrUnexpectedEOF) {
				t.Fatal(err)
			}
			if prefix := h.Prefix(4); len(prefix) != 0 {
				t.Fatal(prefix)
			}
			_ = reader.Close()
			_ = h.Close()
			if storage.closes.Load() != 1 || storage.removes.Load() != 1 {
				t.Fatal("storage not released exactly once")
			}
		})
	}
}
func TestStorageCleanupErrorsAreJoined(t *testing.T) {
	closeFailure, removeFailure := errors.New("close failed"), errors.New("remove failed")
	storage := &testStorage{closeErr: closeFailure, removeErr: removeFailure}
	h := startTest(t, io.NopCloser(strings.NewReader("body")), -1, Options{MemoryBytes: -1, CreateStorage: storage.create})
	waitTest(t, h)
	if err := h.Close(); !errors.Is(err, closeFailure) || !errors.Is(err, removeFailure) {
		t.Fatal(err)
	}
	if err := h.Close(); err != nil {
		t.Fatal(err)
	}
	if storage.closes.Load() != 1 || storage.removes.Load() != 1 {
		t.Fatal("duplicate cleanup")
	}
}
func TestTemporaryFileCreationFailure(t *testing.T) {
	missing := t.TempDir() + "/missing"
	t.Setenv("TMP", missing)
	t.Setenv("TEMP", missing)
	t.Setenv("TMPDIR", missing)
	storage, err := createTemporaryStorage()
	if err == nil {
		_ = storage.Close()
		_ = storage.Remove()
		t.Fatal("expected missing temp directory")
	}
	if !errors.Is(err, os.ErrNotExist) {
		t.Fatal(err)
	}
}
func TestTinyReadsDoNotAccumulateDescriptors(t *testing.T) {
	payload := strings.Repeat("x", minimumMemorySegmentBytes*3)
	for _, memory := range []int64{minimumMemorySegmentBytes * 2, -1} {
		h := startTest(t, io.NopCloser(oneByteReader{strings.NewReader(payload)}), -1, Options{MemoryBytes: memory})
		waitTest(t, h)
		if got := readAllTest(t, openTest(t, h)); got != payload {
			t.Fatal("body changed")
		}
		h.mu.Lock()
		segments := len(h.segments)
		h.mu.Unlock()
		if segments > 3 {
			t.Fatal("tiny reads retained per-read descriptors", segments)
		}
	}
}

type oneByteReader struct{ io.Reader }

func (r oneByteReader) Read(p []byte) (int, error) { return r.Reader.Read(p[:min(len(p), 1)]) }

func TestWaitAndCloseJoinFinishObserver(t *testing.T) {
	input, writer := io.Pipe()
	entered, release := make(chan struct{}), make(chan struct{})
	ctx, cancel := context.WithCancel(testContext(t))
	defer cancel()
	var h *Handle
	var err error
	h, err = Start(ctx, httpRequest(input), Options{OnFinish: func(Snapshot) {
		_ = h.Snapshot()
		cancel()
		close(entered)
		<-release
	}})
	if err != nil {
		t.Fatal(err)
	}
	_ = writer.Close()
	<-entered
	waited, closed := make(chan struct{}), make(chan struct{})
	go func() { _ = h.Wait(context.Background()); close(waited) }()
	go func() { _ = h.Close(); close(closed) }()
	select {
	case <-waited:
		t.Fatal("Wait overtook observer")
	default:
	}
	select {
	case <-closed:
		t.Fatal("Close overtook observer")
	default:
	}
	close(release)
	select {
	case <-waited:
	case <-testContext(t).Done():
		t.Fatal("Wait blocked")
	}
	select {
	case <-closed:
	case <-testContext(t).Done():
		t.Fatal("Close blocked")
	}
}

func TestSimultaneousReadersSeeSameEOFAndTrailerSnapshot(t *testing.T) {
	input, writer := io.Pipe()
	h := startTest(t, input, -1, Options{})
	var wg sync.WaitGroup
	readers := make([]*Reader, 20)
	for i := range readers {
		readers[i] = openTest(t, h)
	}
	for _, reader := range readers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			data, err := io.ReadAll(reader)
			if err != nil || string(data) != "complete" {
				t.Errorf("read %q: %v", data, err)
			}
		}()
	}
	_, _ = writer.Write([]byte("complete"))
	_ = writer.Close()
	wg.Wait()
	waitTest(t, h)
}

func httpRequest(input io.ReadCloser) *http.Request {
	return &http.Request{Body: input, ContentLength: -1}
}

func BenchmarkIngress(b *testing.B) {
	for _, size := range []int{1 << 10, 4 << 20} {
		for _, memory := range []int64{DefaultMemoryBytes, -1} {
			name := "memory"
			if memory < 0 {
				name = "disk"
			}
			if size > 1<<10 {
				name += "/large"
			} else {
				name += "/small"
			}
			payload := bytes.Repeat([]byte("x"), size)
			b.Run(name, func(b *testing.B) {
				b.ReportAllocs()
				b.SetBytes(int64(size))
				for range b.N {
					h, err := Start(context.Background(), httpRequest(io.NopCloser(bytes.NewReader(payload))), Options{MemoryBytes: memory})
					if err != nil {
						b.Fatal(err)
					}
					r, err := h.Open()
					if err != nil {
						b.Fatal(err)
					}
					if _, err = io.Copy(io.Discard, r); err != nil {
						b.Fatal(err)
					}
					_ = r.Close()
					_ = h.Close()
				}
			})
		}
	}
}
