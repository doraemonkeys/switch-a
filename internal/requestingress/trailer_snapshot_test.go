package requestingress

import (
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

func TestTrailerSnapshotIsResolvedOnlyAfterEOF(t *testing.T) {
	input, output := io.Pipe()
	request := httptest.NewRequest(http.MethodPost, "http://gateway", input)
	request.Trailer = http.Header{"X-Declared": nil}
	var calls atomic.Int64
	final := http.Header{"X-Declared": {"declared"}, "X-Late": {"late"}}
	handle, err := Start(t.Context(), request, Options{TrailerSnapshot: func() http.Header {
		calls.Add(1)
		return final.Clone()
	}})
	if err != nil {
		t.Fatal(err)
	}
	defer handle.Close()
	if calls.Load() != 0 {
		t.Fatal("final metadata read before EOF")
	}
	if got := handle.Head().TrailerKeys; len(got) != 1 || got[0] != "X-Declared" {
		t.Fatalf("declaration=%v", got)
	}
	_ = output.Close()
	if err := handle.Wait(t.Context()); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 1 {
		t.Fatalf("snapshot calls=%d", calls.Load())
	}
	if got := handle.Trailers().Get("X-Late"); got != "late" {
		t.Fatalf("trailer=%q", got)
	}
	final.Set("X-Late", "mutated")
	if got := handle.Trailers().Get("X-Late"); got != "late" {
		t.Fatalf("metadata aliases source=%q", got)
	}
}
