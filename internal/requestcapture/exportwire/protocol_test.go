package exportwire

import (
	"bytes"
	"crypto/sha256"
	"errors"
	"hash"
	"io"
	"math"
	"strings"
	"testing"
)

type errorWriter struct {
	err error
}

func (writer errorWriter) Write([]byte) (int, error) {
	return 0, writer.err
}

type shortWriter struct{}

func (shortWriter) Write(value []byte) (int, error) {
	if len(value) == 0 {
		return 0, nil
	}
	return len(value) - 1, nil
}

func TestWorkspaceSizingAndFragmentBounds(t *testing.T) {
	minimum := MinimumLineBytes()
	if minimum <= 0 || BlobRawFragmentBytes(minimum) <= 0 ||
		MetadataRawFragmentBytes(minimum) <= 0 {
		t.Fatalf("minimum line size %d does not admit fragments", minimum)
	}
	if BlobRawFragmentBytes(0) != 0 || MetadataRawFragmentBytes(0) != 0 {
		t.Fatal("non-positive line size admitted a fragment")
	}
	workspace, charge, ok := WorkspaceSizing(minimum)
	if !ok || workspace <= minimum || charge <= int64(workspace) {
		t.Fatalf("WorkspaceSizing(%d) = (%d, %d, %t)", minimum, workspace, charge, ok)
	}
	if _, _, ok := WorkspaceSizing(0); ok {
		t.Fatal("WorkspaceSizing(0) unexpectedly succeeded")
	}
	maxInt := int(^uint(0) >> 1)
	if _, _, ok := WorkspaceSizing(maxInt); ok {
		t.Fatal("WorkspaceSizing(maxInt) unexpectedly succeeded")
	}
}

func TestWriterEmitsEveryProtocolEvent(t *testing.T) {
	var output bytes.Buffer
	lineStorage := make([]byte, 4096)
	metadataBuffer := make([]byte, 7)
	writer := NewWriter(&output, lineStorage, metadataBuffer, "raw risk", func() error { return nil })
	if writer.LineBytes() != len(lineStorage) {
		t.Fatalf("LineBytes() = %d", writer.LineBytes())
	}

	digest := Digest{
		RawSize:    3,
		ChunkCount: 1,
		Checksum:   sha256.Sum256([]byte("raw")),
	}
	calls := []func() error{
		func() error { return writer.WriteManifestBegin(1) },
		func() error { return writer.WriteRecordBegin(0, 1) },
		func() error {
			return writer.WriteMetadataChunk(true, 0, 0, 0, []byte("raw"), digest.Checksum)
		},
		func() error { return writer.WriteMetadataEnd(true, 0, digest) },
		func() error {
			return writer.WriteMetadataChunk(false, 0, 0, 0, []byte("raw"), digest.Checksum)
		},
		func() error { return writer.WriteMetadataEnd(false, 0, digest) },
		func() error {
			return writer.WriteBlobData(0, 0, 0, 0, 3, []byte("raw"), digest.Checksum)
		},
		func() error { return writer.WriteBlobEnd(0, 0, digest) },
		func() error { return writer.WriteRecordEnd(0, 1, 3, digest) },
		func() error { return writer.WriteExportEnd(1) },
	}
	for index, call := range calls {
		if err := call(); err != nil {
			t.Fatalf("protocol call %d error = %v", index, err)
		}
	}
	lines := strings.Split(strings.TrimSpace(output.String()), "\n")
	if len(lines) != len(calls) {
		t.Fatalf("line count = %d, want %d", len(lines), len(calls))
	}
	for _, fragment := range []string{
		"\"event\":\"manifest\"",
		"\"event\":\"record\"",
		"\"event\":\"blob_chunk\"",
		"\"event\":\"record_end\"",
		"\"event\":\"export_end\"",
		"\"raw_payload_risk\":\"raw risk\"",
	} {
		if !strings.Contains(output.String(), fragment) {
			t.Fatalf("stream omitted %s", fragment)
		}
	}
}

func TestMetadataStreamChunksAndDigestsInput(t *testing.T) {
	var output bytes.Buffer
	writer := NewWriter(
		&output,
		make([]byte, 1024),
		make([]byte, 3),
		"risk",
		func() error { return nil },
	)
	stream := NewMetadataStream(&writer, false, 4)
	if err := stream.WriteByte('a'); err != nil {
		t.Fatalf("WriteByte() error = %v", err)
	}
	if err := stream.WriteString("bcdef"); err != nil {
		t.Fatalf("WriteString() error = %v", err)
	}
	if err := stream.WriteBytes([]byte("ghi")); err != nil {
		t.Fatalf("WriteBytes() error = %v", err)
	}
	digest, err := stream.Finish()
	if err != nil {
		t.Fatalf("Finish() error = %v", err)
	}
	raw := []byte("abcdefghi")
	if digest.RawSize != int64(len(raw)) || digest.ChunkCount != 3 ||
		digest.Checksum != sha256.Sum256(raw) {
		t.Fatalf("digest = %+v", digest)
	}
	if got := CurrentSHA256(sha256.New()); got != sha256.Sum256(nil) {
		t.Fatalf("CurrentSHA256(empty) = %x", got)
	}

	empty := NewMetadataStream(&writer, true, 0)
	emptyDigest, err := empty.Finish()
	if err != nil || emptyDigest.RawSize != 0 || emptyDigest.ChunkCount != 0 {
		t.Fatalf("empty Finish() = (%+v, %v)", emptyDigest, err)
	}
}

func TestFixedLineAndWriterFailuresAreFailClosed(t *testing.T) {
	line := fixedJSONLine{}
	if !errors.Is(line.WriteByte('x'), ErrLineTooLarge) ||
		!errors.Is(line.WriteString("x"), ErrLineTooLarge) ||
		!errors.Is(line.WriteBytes([]byte("x")), ErrLineTooLarge) ||
		!errors.Is(line.writeBase64([]byte("x")), ErrLineTooLarge) ||
		!errors.Is(line.writeChecksum(sha256.Sum256(nil)), ErrLineTooLarge) {
		t.Fatal("zero-sized line did not reject writes")
	}

	storage := make([]byte, 256)
	line = fixedJSONLine{storage: storage}
	if err := line.writeInt64(math.MinInt64); err != nil {
		t.Fatalf("writeInt64() error = %v", err)
	}
	if err := line.writeUint64(math.MaxUint64); err != nil {
		t.Fatalf("writeUint64() error = %v", err)
	}
	if err := line.writeInt(42); err != nil {
		t.Fatalf("writeInt() error = %v", err)
	}
	if err := line.writeBase64([]byte("raw")); err != nil {
		t.Fatalf("writeBase64() error = %v", err)
	}
	if err := line.writeChecksum(sha256.Sum256(nil)); err != nil {
		t.Fatalf("writeChecksum() error = %v", err)
	}
	if len(line.bytes()) == 0 {
		t.Fatal("fixed line remained empty")
	}
	line.reset()
	if len(line.bytes()) != 0 {
		t.Fatal("reset did not clear the logical line")
	}

	sentinel := errors.New("not ready")
	checked := NewWriter(io.Discard, make([]byte, 256), nil, "risk", func() error { return sentinel })
	for index, call := range []func() error{
		func() error { return checked.WriteManifestBegin(0) },
		func() error { return checked.WriteRecordBegin(0, 0) },
		func() error { return checked.WriteMetadataChunk(true, 0, 0, 0, nil, [sha256.Size]byte{}) },
		func() error { return checked.WriteMetadataEnd(true, 0, Digest{}) },
		func() error { return checked.WriteBlobData(0, 0, 0, 0, 0, nil, [sha256.Size]byte{}) },
		func() error { return checked.WriteBlobEnd(0, 0, Digest{}) },
		func() error { return checked.WriteRecordEnd(0, 0, 0, Digest{}) },
		func() error { return checked.WriteExportEnd(0) },
	} {
		if err := call(); !errors.Is(err, sentinel) {
			t.Fatalf("checked call %d error = %v", index, err)
		}
	}

	writeErr := errors.New("destination failed")
	for _, destination := range []io.Writer{errorWriter{err: writeErr}, shortWriter{}} {
		writer := NewWriter(destination, make([]byte, 1024), nil, "risk", func() error { return nil })
		if err := writer.WriteExportEnd(0); err == nil {
			t.Fatalf("WriteExportEnd() with %T unexpectedly succeeded", destination)
		}
	}
	tiny := NewWriter(io.Discard, make([]byte, 1), nil, "risk", func() error { return nil })
	if err := tiny.WriteManifestBegin(0); !errors.Is(err, ErrLineTooLarge) {
		t.Fatalf("tiny WriteManifestBegin() error = %v", err)
	}
}

func TestProtocolEventsPropagateEveryLineBoundary(t *testing.T) {
	digest := Digest{
		RawSize:    3,
		ChunkCount: 1,
		Checksum:   sha256.Sum256([]byte("raw")),
	}
	cases := []struct {
		name string
		call func(*Writer) error
	}{
		{name: "manifest", call: func(writer *Writer) error { return writer.WriteManifestBegin(1) }},
		{name: "record begin", call: func(writer *Writer) error { return writer.WriteRecordBegin(2, 3) }},
		{name: "manifest metadata chunk", call: func(writer *Writer) error {
			return writer.WriteMetadataChunk(true, 0, 1, 2, []byte("raw"), digest.Checksum)
		}},
		{name: "record metadata chunk", call: func(writer *Writer) error {
			return writer.WriteMetadataChunk(false, 2, 1, 2, []byte("raw"), digest.Checksum)
		}},
		{name: "manifest metadata end", call: func(writer *Writer) error {
			return writer.WriteMetadataEnd(true, 0, digest)
		}},
		{name: "record metadata end", call: func(writer *Writer) error {
			return writer.WriteMetadataEnd(false, 2, digest)
		}},
		{name: "blob data", call: func(writer *Writer) error {
			return writer.WriteBlobData(1, 2, 3, 4, 5, []byte("raw"), digest.Checksum)
		}},
		{name: "blob end", call: func(writer *Writer) error { return writer.WriteBlobEnd(1, 2, digest) }},
		{name: "record end", call: func(writer *Writer) error {
			return writer.WriteRecordEnd(1, 2, 3, digest)
		}},
		{name: "export end", call: func(writer *Writer) error { return writer.WriteExportEnd(2) }},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			succeeded := false
			for capacity := 0; capacity <= 2048; capacity++ {
				writer := NewWriter(
					io.Discard,
					make([]byte, capacity),
					nil,
					"risk",
					func() error { return nil },
				)
				err := test.call(&writer)
				if err == nil {
					succeeded = true
					break
				}
				if !errors.Is(err, ErrLineTooLarge) {
					t.Fatalf("capacity %d error = %v", capacity, err)
				}
			}
			if !succeeded {
				t.Fatal("event never fit within maximum test capacity")
			}
		})
	}
}

type failingHash struct {
	hash.Hash
}

func (failingHash) Write([]byte) (int, error) {
	return 0, errors.New("hash failed")
}

func TestMetadataStreamPropagatesHashAndWriterFailures(t *testing.T) {
	writer := NewWriter(io.Discard, make([]byte, 1024), make([]byte, 1), "risk", func() error { return nil })
	stream := NewMetadataStream(&writer, false, 0)
	stream.hasher = failingHash{Hash: sha256.New()}
	if err := stream.WriteByte('x'); err != nil {
		t.Fatalf("WriteByte() error = %v", err)
	}
	if _, err := stream.Finish(); err == nil {
		t.Fatal("Finish() ignored hash failure")
	}

	sentinel := errors.New("canceled")
	blocked := NewWriter(io.Discard, make([]byte, 1024), make([]byte, 1), "risk", func() error { return sentinel })
	stream = NewMetadataStream(&blocked, false, 0)
	if err := stream.WriteString("ab"); !errors.Is(err, sentinel) {
		t.Fatalf("WriteString() error = %v", err)
	}
}
