package semantic

import (
	"bytes"
	"compress/gzip"
	"fmt"
	"strings"
	"testing"
)

func compressedProjectionFixture(tb testing.TB, size int) ([]byte, []byte) {
	tb.Helper()
	plain := []byte(`{"model":"chosen","input":"` + strings.Repeat("x", size) + `"}`)
	var encoded bytes.Buffer
	compressor := gzip.NewWriter(&encoded)
	if _, err := compressor.Write(plain); err != nil {
		tb.Fatal(err)
	}
	if err := compressor.Close(); err != nil {
		tb.Fatal(err)
	}
	return plain, encoded.Bytes()
}

func TestCompressedProjectionDecodedSizeBoundary(t *testing.T) {
	const unrelatedBytes = 1 << 20
	plain, encoded := compressedProjectionFixture(t, unrelatedBytes)
	for _, difference := range []int64{-1, 0, 1} {
		t.Run(fmt.Sprintf("limit_delta_%d", difference), func(t *testing.T) {
			limit := int64(len(plain)) + difference
			result := Project(t.Context(), bytes.NewReader(encoded), Options{
				ContentEncodingValues: []string{"gzip"}, MaxDecodedBytes: limit,
			})
			if result.DecodedBytes != int64(len(plain)) {
				t.Fatalf("decoded bytes=%d; want %d", result.DecodedBytes, len(plain))
			}
			// A model parsed before the oversized field cannot survive decoding failure.
			if difference < 0 {
				if result.Model.State != Unavailable || result.Model.Reason != ReasonDecodedBodyTooLarge || result.Codex.State != Unavailable {
					t.Fatalf("oversized compressed result=%+v", result)
				}
				return
			}
			if result.Model.State != Known || result.Model.Value != "chosen" {
				t.Fatalf("valid compressed result=%+v", result)
			}
		})
	}
}

func BenchmarkProjectCompressedUnrelatedString(b *testing.B) {
	const (
		smallUnrelatedBytes = 1 << 10
		largeUnrelatedBytes = 8 << 20
	)
	for _, size := range []int{smallUnrelatedBytes, largeUnrelatedBytes} {
		plain, encoded := compressedProjectionFixture(b, size)
		for _, coding := range []string{"identity", "gzip"} {
			wire := plain
			if coding == "gzip" {
				wire = encoded
			}
			b.Run(fmt.Sprintf("%s/%d", coding, size), func(b *testing.B) {
				options := Options{ContentEncodingValues: []string{coding}, MaxDecodedBytes: int64(len(plain))}
				b.ReportAllocs()
				// The throughput denominator is decoded semantic work, not network bytes.
				b.SetBytes(int64(len(plain)))
				for b.Loop() {
					result := Project(b.Context(), bytes.NewReader(wire), options)
					if result.Model.State != Known || result.Model.Value != "chosen" || result.DecodedBytes != int64(len(plain)) {
						b.Fatalf("projection result=%+v", result)
					}
				}
				b.ReportMetric(float64(len(wire)), "wire-B/op")
				b.ReportMetric(float64(len(plain)), "decoded-B/op")
			})
		}
	}
}
