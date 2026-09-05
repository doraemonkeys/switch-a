package requestingress

import (
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
)

func TestFailureKindsPreserveCauses(t *testing.T) {
	readFailure := errors.New("client disconnected")
	storageFailure := errors.New("disk unavailable")
	for _, tc := range []struct {
		name    string
		input   io.Reader
		length  int64
		options Options
		kind    FailureKind
		cause   error
	}{
		{"read", errorReader{readFailure}, -1, Options{}, FailureRead, readFailure},
		{"limit", strings.NewReader("body"), -1, Options{MaxBodyBytes: 2}, FailureLimit, ErrBodyTooLarge},
		{"length", strings.NewReader("body"), 8, Options{}, FailureLength, ErrLengthMismatch},
		{"storage", strings.NewReader("body"), -1, Options{MemoryBytes: -1, CreateStorage: func() (Storage, error) { return nil, storageFailure }}, FailureStorage, storageFailure},
	} {
		t.Run(tc.name, func(t *testing.T) {
			failed := make(chan Snapshot, 1)
			tc.options.OnFailure = func(snapshot Snapshot) { failed <- snapshot }
			h := startTest(t, io.NopCloser(tc.input), tc.length, tc.options)
			err := h.Wait(testContext(t))
			if !errors.Is(err, tc.cause) {
				t.Fatal(err)
			}
			var failure *Failure
			if !errors.As(err, &failure) || failure.Kind != tc.kind {
				t.Fatal(err)
			}
			if failure.Error() == "" {
				t.Fatal("missing failure diagnostic")
			}
			if h.Snapshot().FailureKind != tc.kind {
				t.Fatal(h.Snapshot())
			}
			select {
			case s := <-failed:
				if s.State != Failed || s.FailureKind != tc.kind || !errors.Is(s.Err, tc.cause) {
					t.Fatal(s)
				}
			default:
				t.Fatal("Wait did not join failure notification")
			}
			h.reportFailure()
			select {
			case <-failed:
				t.Fatal("duplicate failure notification")
			default:
			}
		})
	}
}

func TestLateStorageFailureIsSeparateFromInputCompletion(t *testing.T) {
	storage := &testStorage{readErr: io.ErrUnexpectedEOF}
	var mu sync.Mutex
	var completions, failures []Snapshot
	var failureEvents int
	h := startTest(t, io.NopCloser(strings.NewReader("body")), -1, Options{
		MemoryBytes: -1, CreateStorage: storage.create,
		OnFinish:  func(s Snapshot) { mu.Lock(); completions = append(completions, s); mu.Unlock() },
		OnFailure: func(s Snapshot) { mu.Lock(); failures = append(failures, s); mu.Unlock() },
		Trace: func(e Event) {
			mu.Lock()
			if e.Name == "failed" {
				failureEvents++
			}
			mu.Unlock()
		},
	})
	waitTest(t, h)
	const readerCount = 8
	readers := make([]*Reader, readerCount)
	for i := range readers {
		readers[i] = openTest(t, h)
	}
	var group sync.WaitGroup
	for _, reader := range readers {
		group.Add(1)
		go func() {
			defer group.Done()
			_, err := reader.Read(make([]byte, 4))
			if !errors.Is(err, io.ErrUnexpectedEOF) {
				t.Errorf("storage error %v", err)
			}
			_ = reader.Close()
		}()
	}
	group.Wait()
	_ = h.Close()
	mu.Lock()
	defer mu.Unlock()
	if len(completions) != 1 || completions[0].State != Complete {
		t.Fatal(completions)
	}
	if len(failures) != 1 || failures[0].State != Failed || failures[0].FailureKind != FailureStorage {
		t.Fatal(failures)
	}
	if failureEvents != 1 {
		t.Fatal(failureEvents)
	}
	if storage.closes.Load() != 1 || storage.removes.Load() != 1 {
		t.Fatal("cleanup lost after late failure")
	}
}

func TestStorageFailureWhileReceivingStopsInputAndNotifiesOnce(t *testing.T) {
	input, writer := io.Pipe()
	defer writer.Close()
	storage := &testStorage{readErr: io.ErrUnexpectedEOF}
	failed := make(chan Snapshot, 2)
	h := startTest(t, input, -1, Options{MemoryBytes: -1, CreateStorage: storage.create,
		OnFailure: func(s Snapshot) { failed <- s },
	})
	reader := openTest(t, h)
	if _, err := writer.Write([]byte("prefix")); err != nil {
		t.Fatal(err)
	}
	if _, err := reader.Read(make([]byte, 1)); !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatal(err)
	}
	if err := h.Wait(testContext(t)); !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatal(err)
	}
	if h.Snapshot().FailureKind != FailureStorage {
		t.Fatal(h.Snapshot())
	}
	select {
	case <-failed:
	default:
		t.Fatal("missing failure callback")
	}
	select {
	case <-failed:
		t.Fatal("duplicate callback from pump and reader")
	default:
	}
}

func TestTrailerMapReplacementAtEOF(t *testing.T) {
	input, writer := io.Pipe()
	request := &http.Request{Body: input, ContentLength: -1, Proto: "HTTP/2.0", ProtoMajor: 2}
	h, err := Start(testContext(t), request, Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer h.Close()
	reader := openTest(t, h)
	if _, err = writer.Write([]byte("body")); err != nil {
		t.Fatal(err)
	}
	request.Trailer = http.Header{"X-Undeclared": []string{"late"}}
	_ = writer.Close()
	if got := readAllTest(t, reader); got != "body" {
		t.Fatal(got)
	}
	waitTest(t, h)
	if h.Trailers().Get("X-Undeclared") != "late" {
		t.Fatal(h.Trailers())
	}
	if len(h.Head().TrailerKeys) != 0 {
		t.Fatal("original declaration changed", h.Head())
	}
}
