package framing

import (
	"bytes"
	"compress/gzip"
	"errors"
	"io"
	"testing"

	"github.com/doraemonkeys/switch-a/internal/responseanalysis/allocation"
)

func TestDecoderWorkingSetGrantLifecycle(t *testing.T) {
	t.Parallel()
	payload := []byte("bounded payload")
	compressed := gzipBytes(t, payload)
	reserver := &trackingReserver{}

	decoder, err := NewDecoderWithReserver(CodingGzip, bytes.NewReader(compressed), reserver)
	if err != nil {
		t.Fatal(err)
	}
	active, _, requests := reserver.snapshot()
	if active != GzipDecoderWorkingMemoryBytes || len(requests) != 1 || requests[0].class != allocation.ClassDecoderWorkingSet || requests[0].capacity != GzipDecoderWorkingMemoryBytes {
		t.Fatalf("active=%d requests=%#v", active, requests)
	}
	decoded, err := io.ReadAll(decoder)
	if err != nil || !bytes.Equal(decoded, payload) {
		t.Fatalf("decoded=%q err=%v", decoded, err)
	}
	if active, _, _ := reserver.snapshot(); active != GzipDecoderWorkingMemoryBytes {
		t.Fatalf("working grant released before close: %d", active)
	}
	if err := decoder.Close(); err != nil {
		t.Fatal(err)
	}
	if err := decoder.Close(); err != nil {
		t.Fatal(err)
	}
	if active, _, _ := reserver.snapshot(); active != 0 {
		t.Fatalf("active after close=%d", active)
	}
}

func TestDecoderReservesBeforeReadingAndReleasesOnConstructorFailure(t *testing.T) {
	t.Parallel()
	t.Run("denial prevents source read", func(t *testing.T) {
		reserver := &trackingReserver{denyAt: 1}
		source := &countingSource{reader: bytes.NewReader(gzipBytes(t, []byte("payload")))}
		decoder, err := NewDecoderWithReserver(CodingGzip, source, reserver)
		if decoder != nil || source.reads != 0 {
			t.Fatalf("decoder=%#v reads=%d", decoder, source.reads)
		}
		if reason, ok := allocation.DenialReasonOf(err); !ok || reason != allocation.DenialRequestMemoryExhausted {
			t.Fatalf("error=%v reason=%q ok=%v", err, reason, ok)
		}
	})

	t.Run("invalid gzip releases grant", func(t *testing.T) {
		reserver := &trackingReserver{}
		decoder, err := NewDecoderWithReserver(CodingGzip, bytes.NewReader([]byte("not gzip")), reserver)
		if decoder != nil {
			t.Fatalf("decoder=%#v", decoder)
		}
		assertReason(t, err, FailureContentDecoding)
		if active, _, _ := reserver.snapshot(); active != 0 {
			t.Fatalf("active after constructor failure=%d", active)
		}
	})
}

func TestIdentityAndBrotliNeverReserveWorkingMemory(t *testing.T) {
	t.Parallel()
	reserver := &trackingReserver{}
	identity, err := NewDecoderWithReserver(CodingIdentity, bytes.NewReader([]byte("plain")), reserver)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.ReadAll(identity); err != nil {
		t.Fatal(err)
	}
	if err := identity.Close(); err != nil {
		t.Fatal(err)
	}

	source := &countingSource{reader: bytes.NewReader([]byte("encoded"))}
	brotli, err := NewDecoderWithReserver(CodingBrotli, source, reserver)
	if brotli != nil || source.reads != 0 {
		t.Fatalf("decoder=%#v reads=%d", brotli, source.reads)
	}
	assertReason(t, err, FailureUnsupportedEncoding)
	if active, peak, requests := reserver.snapshot(); active != 0 || peak != 0 || len(requests) != 0 {
		t.Fatalf("active=%d peak=%d requests=%#v", active, peak, requests)
	}
}

func TestDecoderRejectsMissingAllocationDependency(t *testing.T) {
	t.Parallel()
	if decoder, err := NewDecoderWithReserver(CodingIdentity, bytes.NewReader(nil), nil); decoder != nil || !errors.Is(err, allocation.ErrNilReserver) {
		t.Fatalf("decoder=%#v err=%v", decoder, err)
	}
	source := &countingSource{reader: bytes.NewReader(gzipBytes(t, []byte("payload")))}
	if decoder, err := NewDecoderWithReserver(CodingGzip, source, &trackingReserver{nilAt: 1}); decoder != nil || !errors.Is(err, allocation.ErrNilGrant) || source.reads != 0 {
		t.Fatalf("decoder=%#v err=%v reads=%d", decoder, err, source.reads)
	}
}

func gzipBytes(t *testing.T, payload []byte) []byte {
	t.Helper()
	var compressed bytes.Buffer
	writer := gzip.NewWriter(&compressed)
	if _, err := writer.Write(payload); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return compressed.Bytes()
}

type countingSource struct {
	reader io.Reader
	reads  int
}

func (r *countingSource) Read(buffer []byte) (int, error) {
	r.reads++
	return r.reader.Read(buffer)
}
