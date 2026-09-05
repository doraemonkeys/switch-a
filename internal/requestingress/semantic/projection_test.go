package semantic

import (
	"bytes"
	"compress/gzip"
	"compress/zlib"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"reflect"
	"strings"
	"testing"

	codexheaders "github.com/doraemonkeys/switch-a/internal/codex/headers"
	"github.com/doraemonkeys/switch-a/internal/model"
	"github.com/klauspost/compress/zstd"
)

type chunkReader struct {
	source io.Reader
	size   int
}

func (r chunkReader) Read(p []byte) (int, error) {
	if len(p) > r.size {
		p = p[:r.size]
	}
	return r.source.Read(p)
}
func TestConsumerEquivalence(t *testing.T) {
	fixtures := []string{
		"", "null", "[]", "{}", " true ",
		"{\"model\":\"before\",\"MODEL\":\"after\",\"model\":null}",
		"{\"model\":7,\"model\":\"after\"}",
		"{\"model\":\"before\",\"model\":\"\"}",
		"{\"\\u006dodel\":\"escaped\\n\\ud800\",\"unknown\":\"\\udfff\"}",
		"{\"model\":\"m\",\"input\":{\"model\":\"nested\"}}",
		"{\"model\":\"m\"}{}", "{\"model\":\"m\"} {", "{\"model\":\"m\",\"x\":[}",
		"{\"reasoning\":{\"effort\":\"low\"},\"reasoning\":{\"effort\":\"high\"}}",
		"{\"reasoning\":{\"effort\":\"low\"},\"reasoning\":{}}",
		"{\"reasoning\":{\"effort\":false,\"effort\":\"high\"}}",
		"{\"reasoning\":{\"effort\":\"low\",\"effort\":false}}",
		"{\"reasoning\":{\"effort\":\"high\"},\"input\":[}",
		"{\"reasoning\":{\"effort\":\"high\",\"input\":[}}",
		"{\"reasoning\":{\"effort\":\"high\"}}{}",
		"{\"reasoning\":{\"effort\":\"high\"},\"reasoning\":{\"effort\":\"low\",\"bad\":[}}",
		"{\"Reasoning\":{\"effort\":7},\"reasoning\":{\"Effort\":7,\"effort\":\"\"}}",
		"{\"reasoning_effort\":null}", "{\"reasoning_effort\":\"low\",\"reasoning_effort\":\"high\"}",
		"{\"reasoning_effort\":false,\"reasoning_effort\":\"high\"}",
		"{\"output_config\":{\"effort\":\"high\"},\"thinking\":{\"type\":\"enabled\",\"budget_tokens\":1024}}",
		"{\"thinking\":{\"type\":\"enabled\",\"type\":7,\"budget_tokens\":1e3}}",
		"{\"thinking\":{\"type\":null,\"type\":\"enabled\",\"budget_tokens\":-1}}",
		"{\"thinking\":{\"budget_tokens\":9223372036854775808}}",
		"{\"thinking\":{\"budget_tokens\":-9223372036854775808}}",
		"{\"thinking\":{\"budget_tokens\":0.0}}",
		"{\"thinking\":{\"budget_tokens\":01}}",
		"{\"reasoning\":{\"effort\":\"" + strings.Repeat("界", 33) + "\"}}",
		"{\"reasoning\":{\"effort\":\"" + strings.Repeat("\\u0061", 32) + "\"}}",
		"{\"type\":\"response.create\",\"client_metadata\":{\"thread_id\":\"thread\",\"session_id\":\"session\",\"x-codex-window-id\":\"window\",\"x-codex-turn-metadata\":\"turn\"},\"previous_response_id\":\"response\"}",
		"{\"type\":\"response.create\",\"client_metadata\":null}",
		"{\"type\":\"response.create\",\"client_metadata\":{\"session_id\":\"one\",\"session\\u005fid\":\"two\"}}",
		"{\"type\":\"response.create\",\"client_metadata\":{\"Thread_Id\":\"ignored\",\"thread_id\":\" \"}}",
		"{\"type\":\"response.create\",\"client_metadata\":{},\"client_metadata\":{}}",
		"{\"type\":\"response.create\",\"previous_response_id\":\"one\",\"previous_response_id\":\"two\"}",
		"{\"type\":\"response.create\",\"previous_response_id\":null}",
		"{\"type\":\"response.create\",\"type\":\"response.create\"}",
		"{\"type\":\"response.create\",\"\\u0074ype\":\"response.create\"}",
		"{\"TYPE\":\"response.create\",\"previous_response_id\":null}",
		"{\"type\":\"response.inject\",\"client_metadata\":null}",
		"{\"type\":\"response.append\"}",
		"{\"type\":\"future.event\",\"client_metadata\":null}",
		"{\"client_metadata\":{\"thread_id\":\"ignored\"},\"previous_response_id\":\"ignored\"}",
		"{\"type\":\"response.create\",\"client_metadata\":{\"thread_id\":\"ok\"}}{}",
		"{\"type\":\"response.create\",\"client_metadata\":{\"thread_id\":\"ok\"},\"bad\":[}",
		"{\"model\":\"\xff\",\"reasoning\":{\"effort\":\"\xff\"}}",
		"{\"irrelevant\":\"" + strings.Repeat("x", 2<<20) + "\",\"model\":\"after\",\"reasoning\":{\"effort\":\"high\"}}",
		"{\"irrelevant\":\"bad\\q\"}",
		"{\"irrelevant\":\"bad\n\"}",
	}
	for i, body := range fixtures {
		for _, entry := range []struct {
			contract ReasoningContract
			api      string
		}{{ReasoningCodex, "codex"}, {ReasoningClaude, "claude"}, {ReasoningChat, "chat"}} {
			for _, chunk := range []int{1, 17, 4096} {
				t.Run(fmt.Sprintf("%d/%s/%d", i, entry.api, chunk), func(t *testing.T) {
					got := Project(context.Background(), chunkReader{source: strings.NewReader(body), size: chunk}, Options{ReasoningContract: entry.contract, MaxDecodedBytes: 4 << 20})
					var legacyModel struct{ Model string }
					wantModel := "unknown"
					if json.Unmarshal([]byte(body), &legacyModel) == nil && legacyModel.Model != "" {
						wantModel = legacyModel.Model
					}
					if got.Model.Value != wantModel {
						t.Fatalf("model=%q want %q", got.Model.Value, wantModel)
					}
					wantReasoning := legacyRequestedReasoning(entry.api, []byte(body))
					if !reflect.DeepEqual(got.Reasoning.Value, wantReasoning) {
						t.Fatalf("reasoning=%#v want %#v (values %v/%v)", got.Reasoning.Value, wantReasoning, reasoningValues(got.Reasoning.Value), reasoningValues(wantReasoning))
					}
					var legacy codexheaders.MessageView
					if body != "" {
						legacy = codexheaders.InspectClientPayload(nil, []byte(body))
					}
					if got.Codex.Value.Recognized() != legacy.Recognized() || got.Codex.Value.EventType() != legacy.EventType() {
						t.Fatal("event observation changed")
					}
					for _, owner := range []codexheaders.OwnerStatus{codexheaders.OwnerUnknown, codexheaders.OwnerCurrent, codexheaders.OwnerConflict} {
						lookup := func(codexheaders.BindingCandidate) codexheaders.OwnerStatus { return owner }
						headers := http.Header{"Thread-Id": {"header-thread"}}
						want := codexheaders.DecideClient(codexheaders.ClientInput{Headers: headers, Message: legacy, Owners: lookup})
						actual := codexheaders.DecideClientEvidence(codexheaders.ClientEvidenceInput{Headers: headers, Evidence: got.Codex.Value, Owners: lookup})
						if !reflect.DeepEqual(actual.Decisions(), want.Decisions()) {
							t.Fatalf("Codex decisions changed: %#v != %#v", actual.Decisions(), want.Decisions())
						}
					}
				})
			}
		}
	}
}
func reasoningValues(v model.RequestedReasoningObservation) []any {
	var values []any
	if v.Effort != nil {
		values = append(values, *v.Effort)
	}
	if v.Mode != nil {
		values = append(values, *v.Mode)
	}
	if v.BudgetTokens != nil {
		values = append(values, *v.BudgetTokens)
	}
	return values
}
func encoded(t testing.TB, coding string, body []byte) []byte {
	t.Helper()
	var buffer bytes.Buffer
	var writer io.WriteCloser
	switch coding {
	case "gzip":
		writer = gzip.NewWriter(&buffer)
	case "deflate":
		writer = zlib.NewWriter(&buffer)
	case "zstd":
		var err error
		writer, err = zstd.NewWriter(&buffer)
		if err != nil {
			t.Fatal(err)
		}
	default:
		t.Fatalf("unknown test coding %s", coding)
	}
	if _, err := writer.Write(body); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes()
}
func TestCodingAndLateFailures(t *testing.T) {
	body := []byte("{\"model\":\"chosen\",\"reasoning\":{\"effort\":\"high\"}}")
	for _, coding := range []string{"gzip", "deflate", "zstd"} {
		t.Run(coding, func(t *testing.T) {
			wire := encoded(t, coding, body)
			got := Project(context.Background(), bytes.NewReader(wire), Options{ContentEncodingValues: []string{coding}, MaxDecodedBytes: int64(len(body)), ReasoningContract: ReasoningCodex})
			if got.Model.Value != "chosen" || got.DecodedBytes != int64(len(body)) {
				t.Fatalf("decoded=%#v", got)
			}
			got = Project(context.Background(), bytes.NewReader(wire), Options{ContentEncodingValues: []string{coding}, MaxDecodedBytes: int64(len(body) - 1), ReasoningContract: ReasoningCodex})
			if got.Model.Reason != "decoded_body_too_large" || got.Reasoning.Value.Effort != nil {
				t.Fatalf("limit result=%#v", got)
			}
			got = Project(context.Background(), bytes.NewReader(wire[:len(wire)-1]), Options{ContentEncodingValues: []string{coding}, MaxDecodedBytes: 1024, ReasoningContract: ReasoningCodex})
			if got.Model.State != Unavailable || got.Reasoning.Value.Effort != nil {
				t.Fatalf("truncated coding=%#v", got)
			}
		})
	}
	stack := encoded(t, "gzip", encoded(t, "deflate", body))
	got := Project(context.Background(), bytes.NewReader(stack), Options{ContentEncodingValues: []string{"DEFLATE", " identity, GZip "}, MaxDecodedBytes: 1024})
	if got.Model.Value != "chosen" {
		t.Fatalf("stack=%#v", got)
	}
	gzipBad := encoded(t, "gzip", body)
	gzipBad[len(gzipBad)-5] ^= 1
	got = Project(context.Background(), bytes.NewReader(gzipBad), Options{ContentEncodingValues: []string{"gzip"}, MaxDecodedBytes: 1024, ReasoningContract: ReasoningCodex})
	if got.Model.Reason != "content_decoding" || got.Reasoning.Value.Effort != nil {
		t.Fatalf("checksum=%#v", got)
	}
	for _, fixture := range []struct {
		body   string
		coding []string
		limit  int64
		reason string
	}{
		{"x", []string{"br"}, 10, "unsupported_content_encoding"},
		{"x", []string{"gzip,"}, 10, "invalid_content_encoding"},
		{"x", []string{"identity,identity,identity,identity,identity"}, 10, "invalid_content_encoding"},
		{"x", []string{"gzip"}, 0, "invalid_limit"},
		{"x", []string{"gzip"}, 10, "content_decoding"},
		{"x", []string{"deflate"}, 10, "content_decoding"},
		{"x", []string{"zstd"}, 10, "content_decoding"},
	} {
		got := Project(context.Background(), strings.NewReader(fixture.body), Options{ContentEncodingValues: fixture.coding, MaxDecodedBytes: fixture.limit})
		if got.Model.Reason != fixture.reason {
			t.Errorf("reason %q want %q", got.Model.Reason, fixture.reason)
		}
		if got.Reasoning.State != Known || *got.Reasoning.Value.State != model.ReasoningObservationUnsupported {
			t.Fatal("unsupported observation blocked by coding")
		}
	}
	got = Project(context.Background(), strings.NewReader(""), Options{ContentEncodingValues: []string{"br"}})
	if got.Model.Reason != "invalid_json" {
		t.Fatalf("empty-wire legacy bypass lost: %#v", got)
	}
}

type failingReader struct{}

func (failingReader) Read([]byte) (int, error) { return 0, errors.New("injected read failure") }
func TestSourceFailureAndCancellation(t *testing.T) {
	got := Project(context.Background(), failingReader{}, Options{})
	if got.Model.State != Unavailable {
		t.Fatal("failed source became known")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	got = Project(ctx, strings.NewReader("{}"), Options{})
	if got.Model.State != Unavailable {
		t.Fatal("cancellation became known")
	}
}
func BenchmarkProjectUnrelatedString(b *testing.B) {
	for _, size := range []int{1 << 10, 8 << 20} {
		body := "{\"input\":\"" + strings.Repeat("x", size) + "\",\"model\":\"chosen\"}"
		b.Run(fmt.Sprint(size), func(b *testing.B) {
			b.ReportAllocs()
			b.SetBytes(int64(len(body)))
			for b.Loop() {
				Project(context.Background(), strings.NewReader(body), Options{})
			}
		})
	}
}
