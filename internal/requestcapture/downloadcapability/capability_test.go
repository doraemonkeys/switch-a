package downloadcapability

import (
	"bytes"
	"errors"
	"strings"
	"testing"
)

type failingReader struct{}

func (failingReader) Read([]byte) (int, error) {
	return 0, errors.New("sensitive dependency failure")
}

func TestExportIDIsCanonicalAndRejectsMalformedValues(t *testing.T) {
	if _, err := MakeExportID([16]byte{}); err == nil {
		t.Fatal("MakeExportID(zero) unexpectedly succeeded")
	}
	exportID, err := MakeExportID([16]byte{1})
	if err != nil {
		t.Fatalf("MakeExportID() error = %v", err)
	}
	if !IsCanonicalExportID(exportID) || len(exportID) != CanonicalExportIDBytes {
		t.Fatalf("generated export ID %q is not canonical", exportID)
	}

	for _, value := range []string{
		"",
		exportID[:len(exportID)-1],
		"xx_" + exportID[3:],
		exportID[:3] + strings.Repeat("!", len(exportID)-3),
		exportID + "=",
	} {
		if IsCanonicalExportID(value) {
			t.Fatalf("IsCanonicalExportID(%q) unexpectedly succeeded", value)
		}
	}
}

func TestTokenGenerationHashingAndMatching(t *testing.T) {
	entropy := bytes.Repeat([]byte{0xa5}, EntropyBytes)
	raw, stored, err := NewToken(bytes.NewReader(entropy))
	if err != nil {
		t.Fatalf("NewToken() error = %v", err)
	}
	if stored != HashToken(raw) || !Matches(stored, raw) {
		t.Fatal("generated token does not match stored hash")
	}
	if Matches(stored, strings.Repeat("x", len(raw))) {
		t.Fatal("different token matched")
	}
	if Matches(stored, raw[:len(raw)-1]) {
		t.Fatal("wrong-length token matched")
	}
}

func TestTokenGenerationFailsWithOpaqueDependencyErrors(t *testing.T) {
	if _, _, err := NewToken(nil); err == nil {
		t.Fatal("NewToken(nil) unexpectedly succeeded")
	}
	if _, _, err := NewToken(failingReader{}); err == nil || strings.Contains(err.Error(), "sensitive") {
		t.Fatalf("NewToken(failingReader) error = %v", err)
	}
}
