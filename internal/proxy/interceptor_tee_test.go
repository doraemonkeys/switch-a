package proxy

import (
	"bytes"
	"io"
	"strings"
	"testing"
)

// testReadCloser wraps a Reader and tracks Close calls.
// Named to avoid conflict with testReadCloser in transport_test.go.
type testReadCloser struct {
	io.Reader
	closed bool
}

func (m *testReadCloser) Close() error {
	m.closed = true
	return nil
}

// ============================================================
// interceptTeeReadCloser tests
// ============================================================

func TestInterceptTeeReadCloser_Read(t *testing.T) {
	original := &testReadCloser{Reader: strings.NewReader("hello world")}
	var buf bytes.Buffer
	eofCalled := false

	trc := &interceptTeeReadCloser{
		original: original,
		tee:      io.TeeReader(original, &buf),
		onEOF:    func() { eofCalled = true },
	}

	// Read all data
	data := make([]byte, 20)
	n, err := trc.Read(data)
	if n != 11 {
		t.Errorf("expected n=11, got %d", n)
	}
	if err != nil {
		t.Errorf("expected nil error, got %v", err)
	}
	if string(data[:n]) != "hello world" {
		t.Errorf("expected 'hello world', got %q", string(data[:n]))
	}
	if eofCalled {
		t.Error("onEOF should not be called yet")
	}

	// Next read should return EOF
	n, err = trc.Read(data)
	if n != 0 {
		t.Errorf("expected n=0, got %d", n)
	}
	if err != io.EOF {
		t.Errorf("expected io.EOF, got %v", err)
	}
	if !eofCalled {
		t.Error("onEOF should be called on EOF")
	}

	// Verify tee captured the data
	if buf.String() != "hello world" {
		t.Errorf("expected buffer 'hello world', got %q", buf.String())
	}
}

func TestInterceptTeeReadCloser_Close(t *testing.T) {
	original := &testReadCloser{Reader: strings.NewReader("test")}
	trc := &interceptTeeReadCloser{
		original: original,
		tee:      io.TeeReader(original, &bytes.Buffer{}),
	}

	if err := trc.Close(); err != nil {
		t.Errorf("expected nil error, got %v", err)
	}
	if !original.closed {
		t.Error("expected original to be closed")
	}
}

func TestInterceptTeeReadCloser_NilOnEOF(t *testing.T) {
	original := &testReadCloser{Reader: strings.NewReader("")}
	trc := &interceptTeeReadCloser{
		original: original,
		tee:      io.TeeReader(original, &bytes.Buffer{}),
		onEOF:    nil, // nil callback should not panic
	}

	data := make([]byte, 10)
	_, err := trc.Read(data)
	if err != io.EOF {
		t.Errorf("expected io.EOF, got %v", err)
	}
	// Should not panic with nil onEOF
}
