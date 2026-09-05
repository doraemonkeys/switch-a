package semantic

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"reflect"
	"strings"
	"testing"

	codexheaders "github.com/doraemonkeys/switch-a/internal/codex/headers"
)

func TestEveryCodingCompletesBeforeFacts(t *testing.T) {
	// The streaming zstd fixture advertises a larger window than its tiny inner JSON.
	const decodedLimit = 16 << 20
	jsonBody := []byte("{\"model\":\"verified\",\"reasoning\":{\"effort\":\"high\"}}")
	padded := append(encoded(t, "deflate", jsonBody), bytes.Repeat([]byte("p"), 128<<10)...)
	for _, outer := range []string{"gzip", "deflate", "zstd"} {
		for _, corrupt := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/corrupt=%t", outer, corrupt), func(t *testing.T) {
				wire := encoded(t, outer, padded)
				if corrupt {
					wire[len(wire)-1] ^= 1
				}
				result := Project(context.Background(), bytes.NewReader(wire), Options{
					ContentEncodingValues: []string{"deflate,identity," + outer},
					MaxDecodedBytes:       decodedLimit, ReasoningContract: ReasoningCodex,
				})
				if corrupt {
					if result.Model.State != Unavailable || result.Model.Reason != ReasonContentDecoding || result.Reasoning.Value.Effort != nil {
						t.Fatalf("outer failure retained semantic values: %#v", result)
					}
				} else if result.Model.Value != "verified" || result.DecodedBytes != int64(len(jsonBody)) {
					t.Fatalf("outer unused output changed final JSON semantics: %#v", result)
				}
			})
		}
	}
}

type gatedTail struct {
	entered chan struct{}
	release chan struct{}
	failure error
}

func (r *gatedTail) Read([]byte) (int, error) {
	select {
	case <-r.entered:
	default:
		close(r.entered)
	}
	<-r.release
	return 0, r.failure
}

func TestSuccessfulCodingWaitsForSourceOutcome(t *testing.T) {
	for _, failure := range []error{io.EOF, errors.New("injected source tail failure")} {
		tail := &gatedTail{entered: make(chan struct{}), release: make(chan struct{}), failure: failure}
		wire := encoded(t, "deflate", []byte("{\"model\":\"verified\"}"))
		done := make(chan Result, 1)
		go func() {
			done <- Project(context.Background(), io.MultiReader(bytes.NewReader(wire), tail), Options{ContentEncodingValues: []string{"deflate"}, MaxDecodedBytes: 1024})
		}()
		<-tail.entered
		select {
		case result := <-done:
			close(tail.release)
			t.Fatalf("published before source outcome: %#v", result)
		default:
		}
		close(tail.release)
		result := <-done
		if failure == io.EOF {
			if result.Model.Value != "verified" {
				t.Fatalf("healthy tail: %#v", result)
			}
		} else if result.Model.State != Unavailable || result.Model.Reason != ReasonContentDecoding {
			t.Fatalf("failed wire tail published facts: %#v", result)
		}
	}
}

func nestedWrongType(depth int) string {
	value := "\"leaf\""
	for range depth {
		value = "{\"effort\":" + value + ",\"type\":" + value + "}"
	}
	return value
}
func TestWrongTypeReasoningRetainsLegacyObservation(t *testing.T) {
	nested := nestedWrongType(12)
	for _, contract := range []struct {
		kind       ReasoningContract
		api, field string
	}{
		{ReasoningCodex, "codex", "reasoning"}, {ReasoningClaude, "claude", "thinking"}, {ReasoningChat, "chat", "reasoning_effort"},
	} {
		body := "{\"" + contract.field + "\":" + nested + "}"
		got := Project(context.Background(), strings.NewReader(body), Options{ReasoningContract: contract.kind})
		want := legacyRequestedReasoning(contract.api, []byte(body))
		if !reflect.DeepEqual(got.Reasoning.Value, want) {
			t.Fatalf("%s changed wrong-type observation", contract.api)
		}
	}
}
func TestEventTypeRecognitionNeedsOnlyBoundedText(t *testing.T) {
	escaped := strings.Builder{}
	for _, c := range "response.create" {
		fmt.Fprintf(&escaped, "\\u%04x", c)
	}
	for _, value := range []string{escaped.String(), strings.Repeat("x", 8<<20)} {
		body := "{\"type\":\"" + value + "\",\"client_metadata\":{\"thread_id\":\"thread\"}}"
		got := Project(context.Background(), strings.NewReader(body), Options{})
		want := codexheaders.InspectClientFrame([]byte(body))
		if got.Codex.Value.Recognized() != want.Recognized() || got.Codex.Value.EventType() != want.EventType() {
			t.Fatal("bounded type changed recognition")
		}
	}
}
func BenchmarkProjectWrongTypeReasoning(b *testing.B) {
	for _, depth := range []int{8, 18} {
		body := "{\"reasoning\":" + nestedWrongType(depth) + "}"
		b.Run(fmt.Sprint(len(body)), func(b *testing.B) {
			b.ReportAllocs()
			b.SetBytes(int64(len(body)))
			for b.Loop() {
				Project(context.Background(), strings.NewReader(body), Options{ReasoningContract: ReasoningCodex})
			}
		})
	}
}
func BenchmarkProjectUnknownEventType(b *testing.B) {
	for _, size := range []int{1 << 10, 8 << 20} {
		body := "{\"type\":\"" + strings.Repeat("x", size) + "\"}"
		b.Run(fmt.Sprint(size), func(b *testing.B) {
			b.ReportAllocs()
			b.SetBytes(int64(len(body)))
			for b.Loop() {
				Project(context.Background(), strings.NewReader(body), Options{})
			}
		})
	}
}
