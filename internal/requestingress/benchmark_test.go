package requestingress

import (
	"bytes"
	"context"
	"io"
	"testing"
)

func BenchmarkConcurrentIngress(b *testing.B) {
	const bodyBytes = 256 << 10
	const captureBytes = 1 << 10
	payload := bytes.Repeat([]byte("x"), bodyBytes)
	for _, capture := range []bool{false, true} {
		name := "capture-disabled"
		if capture {
			name = "bounded-capture"
		}
		b.Run(name, func(b *testing.B) {
			budget := NewBudget(64 << 10)
			b.ReportAllocs()
			b.SetBytes(bodyBytes)
			b.RunParallel(func(pb *testing.PB) {
				for pb.Next() {
					var evidence []byte
					options := Options{SharedBudget: budget}
					if capture {
						evidence = make([]byte, 0, captureBytes)
						options.OnChunk = func(chunk []byte) {
							count := min(cap(evidence)-len(evidence), len(chunk))
							evidence = append(evidence, chunk[:count]...)
						}
					}
					h, err := Start(context.Background(), httpRequest(io.NopCloser(bytes.NewReader(payload))), options)
					if err != nil {
						b.Error(err)
						return
					}
					r, err := h.Open()
					if err != nil {
						b.Error(err)
						_ = h.Close()
						return
					}
					if _, err = io.Copy(io.Discard, r); err != nil {
						b.Error(err)
					}
					_ = r.Close()
					_ = h.Close()
					if capture && len(evidence) != captureBytes {
						b.Error("bounded capture incomplete")
					}
				}
			})
			if budget.Used() != 0 {
				b.Fatal("shared memory leaked", budget.Used())
			}
		})
	}
}
