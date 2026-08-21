package responseanalysis

import (
	"bytes"
	"compress/gzip"
	"encoding/base64"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/doraemonkeys/switch-a/internal/apicontract"
	"github.com/doraemonkeys/switch-a/internal/responseanalysis/allocation"
	"github.com/doraemonkeys/switch-a/internal/responseanalysis/framing"
)

type protocolFixtureFile struct {
	SchemaVersion int               `json:"schema_version"`
	Cases         []protocolFixture `json:"cases"`
}

type protocolFixture struct {
	Name            string `json:"name"`
	APIType         string `json:"api_type"`
	ContentType     string `json:"content_type"`
	ContentEncoding string `json:"content_encoding"`
	Body            struct {
		Encoding string `json:"encoding"`
		Value    string `json:"value"`
	} `json:"body"`
	Expected struct {
		ProtocolID       *apicontract.ResponseProtocolID `json:"response_protocol_id"`
		EventClass       EventClass                      `json:"event_class"`
		AnalysisReason   AnalysisFailureReason           `json:"analysis_reason"`
		Fields           *SemanticFields                 `json:"fields"`
		RawBodyPreserved bool                            `json:"raw_body_preserved"`
	} `json:"expected"`
}

func TestFrozenProtocolFixtures(t *testing.T) {
	t.Parallel()
	registry := NewRegistry()
	for _, fileName := range []string{"protocol-envelopes-positive.json", "protocol-envelopes-negative.json"} {
		fixtures := loadProtocolFixtures(t, fileName)
		for _, fixture := range fixtures.Cases {
			fixture := fixture
			t.Run(fixture.Name, func(t *testing.T) {
				t.Parallel()
				body := decodeFixtureBody(t, fixture)
				protocol, resolveFailure := registry.Resolve(fixture.APIType, fixture.ContentType, fixture.ContentEncoding)
				observations := registry.Analyze(fixture.APIType, fixture.ContentType, fixture.ContentEncoding, bytes.NewReader(body), allocation.NoopReserver{})

				if fixture.Expected.ProtocolID == nil {
					if resolveFailure != fixture.Expected.AnalysisReason {
						t.Fatalf("resolve failure = %q, want %q", resolveFailure, fixture.Expected.AnalysisReason)
					}
					assertSingleFailure(t, observations, fixture.Expected.AnalysisReason)
					return
				}
				if resolveFailure != "" || protocol.ID() != *fixture.Expected.ProtocolID {
					t.Fatalf("resolution = %q, %q; want %q", protocol.ID(), resolveFailure, *fixture.Expected.ProtocolID)
				}
				if strings.EqualFold(fixture.ContentEncoding, "br") {
					assertSingleFailure(t, observations, fixture.Expected.AnalysisReason)
					if !fixture.Expected.RawBodyPreserved || !bytes.Equal(body, decodeFixtureBody(t, fixture)) {
						t.Fatal("Brotli fail-open did not preserve the frozen raw body")
					}
					return
				}
				if len(observations) != 1 {
					t.Fatalf("observations = %#v", observations)
				}
				got := observations[0]
				if got.ProtocolID != *fixture.Expected.ProtocolID || got.Class != fixture.Expected.EventClass || !reflect.DeepEqual(got.Fields, fixture.Expected.Fields) {
					t.Fatalf("observation = %#v, expected = %#v", got, fixture.Expected)
				}
			})
		}
	}
}

func TestRegistrySelectionMatrix(t *testing.T) {
	t.Parallel()
	tests := []struct {
		apiType     string
		contentType string
		encoding    string
		wantID      apicontract.ResponseProtocolID
		wantFailure AnalysisFailureReason
	}{
		{"claude", "application/json", "", apicontract.ProtocolAnthropicMessagesJSON, ""},
		{"deepseek-claude", "text/event-stream; charset=utf-8", "GZIP", apicontract.ProtocolAnthropicMessagesSSE, ""},
		{"codex", "application/problem+json", "identity", apicontract.ProtocolOpenAIResponsesJSON, ""},
		{"grok", "TEXT/EVENT-STREAM", "br", apicontract.ProtocolOpenAIChatCompletionsSSE, ""},
		{"deepseek-openai", "APPLICATION/JSON; Charset=UTF-8", "identity", apicontract.ProtocolOpenAIChatCompletionsJSON, ""},
		{"gemini", "text/event-stream", "identity", apicontract.ProtocolGoogleGenerateContentSSE, ""},
		{"custom:tool", "application/json", "identity", "", FailureUnsupportedProtocol},
		{"codex", "application/websocket", "identity", "", FailureUnsupportedProtocol},
		{"codex", "application/json", "gzip, br", "", FailureUnsupportedEncoding},
		{"codex", "application/json", "zstd", "", FailureUnsupportedEncoding},
		{"codex", "bad media", "identity", "", FailureUnsupportedProtocol},
	}
	registry := NewRegistry()
	for _, test := range tests {
		protocol, failure := registry.Resolve(test.apiType, test.contentType, test.encoding)
		if protocol.ID() != test.wantID || failure != test.wantFailure {
			t.Errorf("Resolve(%q,%q,%q) = %q,%q; want %q,%q", test.apiType, test.contentType, test.encoding, protocol.ID(), failure, test.wantID, test.wantFailure)
		}
	}
}

func TestIncrementalStreamAndAnalysisFailures(t *testing.T) {
	t.Parallel()
	registry := NewRegistry()
	protocol, failure := registry.Resolve("claude", "text/event-stream", "identity")
	if failure != "" {
		t.Fatal(failure)
	}
	wire := []byte("event: error\ndata: {\"type\":\"error\",\ndata: \"error\":{\"type\":\"busy\",\"message\":\"retry\"}}\n\n")
	for split := 0; split <= len(wire); split++ {
		stream := mustNewStream(t, protocol)
		var got []Observation
		feedCollect(stream, wire[:split], false, &got)
		feedCollect(stream, wire[split:], true, &got)
		if len(got) != 1 || got[0].Class != EventError || got[0].Fields == nil || got[0].Fields.Message != "retry" {
			t.Fatalf("split %d: %#v", split, got)
		}
		var extra []Observation
		feedCollect(stream, nil, true, &extra)
		if len(extra) != 0 {
			t.Fatalf("terminal stream emitted %#v", extra)
		}
	}

	jsonProtocol, _ := registry.Resolve("codex", "application/json", "identity")
	assertSingleFailure(t, analyze(jsonProtocol, strings.NewReader("{} {}")), FailureMalformedFrame)
	assertSingleFailure(t, analyze(jsonProtocol, strings.NewReader(strings.Repeat("x", MaxDecodedEventBytes+1))), FailureDecodedEventTooLarge)

	gzipProtocol, _ := registry.Resolve("codex", "application/json", "gzip")
	assertSingleFailure(t, analyze(gzipProtocol, bytes.NewReader([]byte("broken"))), FailureContentDecoding)

	brotliProtocol, _ := registry.Resolve("codex", "application/json", "br")
	assertSingleFailure(t, analyze(brotliProtocol, bytes.NewReader(nil)), FailureUnsupportedEncoding)

	stream := mustNewStream(t, protocol)
	firstEvent := "data: {\"type\":\"ping\"}\n\n"
	oversized := strings.Repeat("x", MaxDecodedEventBytes+1)
	var ordered []Observation
	feedCollect(stream, []byte(firstEvent+oversized), false, &ordered)
	if len(ordered) != 2 || ordered[0].Class != EventControl || ordered[1].AnalysisReason != FailureDecodedEventTooLarge {
		t.Fatalf("ordered limit observations = %#v", ordered)
	}
}

func TestGzipAnalysisAndUsageObservation(t *testing.T) {
	t.Parallel()
	payload := []byte(`{"usage":{"prompt_tokens":3,"completion_tokens":4}}`)
	var wire bytes.Buffer
	writer := gzip.NewWriter(&wire)
	if _, err := writer.Write(payload); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	protocol, failure := NewRegistry().Resolve("grok", "application/json", "gzip")
	if failure != "" {
		t.Fatal(failure)
	}
	observations := analyze(protocol, bytes.NewReader(wire.Bytes()))
	if len(observations) != 1 || observations[0].Class != EventClientVisible || observations[0].Usage == nil || observations[0].Usage.TotalTokens.Present {
		t.Fatalf("observations = %#v", observations)
	}
}

func TestObserveUsageDelegatesToAdapterOwnedParser(t *testing.T) {
	t.Parallel()
	usage := ObserveUsage([]byte(`{"usage":{"input_tokens":2,"output_tokens":3}}`), nil)
	if usage == nil || usage.PromptTokens.Value != 2 || usage.CompletionTokens.Value != 3 || usage.TotalTokens.Present {
		t.Fatalf("usage = %#v", usage)
	}
}

func TestGzipExpansionRespectsDecodedEventCap(t *testing.T) {
	t.Parallel()
	protocol, failure := NewRegistry().Resolve("codex", "application/json", "gzip")
	if failure != "" {
		t.Fatal(failure)
	}
	prefix, suffix := `{"value":"`, `"}`
	for _, test := range []struct {
		name       string
		size       int
		wantClass  EventClass
		wantReason AnalysisFailureReason
	}{
		{"exact cap is classified", MaxDecodedEventBytes, EventClientVisible, ""},
		{"one byte over fails open", MaxDecodedEventBytes + 1, EventFailOpen, FailureDecodedEventTooLarge},
	} {
		t.Run(test.name, func(t *testing.T) {
			payload := []byte(prefix + strings.Repeat("x", test.size-len(prefix)-len(suffix)) + suffix)
			var wire bytes.Buffer
			writer := gzip.NewWriter(&wire)
			if _, err := writer.Write(payload); err != nil {
				t.Fatal(err)
			}
			if err := writer.Close(); err != nil {
				t.Fatal(err)
			}
			observations := analyze(protocol, bytes.NewReader(wire.Bytes()))
			if len(observations) != 1 || observations[0].Class != test.wantClass || observations[0].AnalysisReason != test.wantReason {
				t.Fatalf("observations = %#v", observations)
			}
		})
	}
}

func TestBrotliFailsBeforeReadingOrMutatingRawBody(t *testing.T) {
	t.Parallel()
	protocol, failure := NewRegistry().Resolve("grok", "text/event-stream", "br")
	if failure != "" {
		t.Fatal(failure)
	}
	raw := []byte{0x1b, 0x58, 0x00, 0x28, 0x2c}
	source := &countingReader{reader: bytes.NewReader(raw)}
	decoder, err := protocol.NewDecoder(source, allocation.NoopReserver{})
	if decoder != nil || framing.ReasonOf(err) != framing.FailureUnsupportedEncoding {
		t.Fatalf("decoder=%#v err=%v", decoder, err)
	}
	if source.reads != 0 || !bytes.Equal(raw, []byte{0x1b, 0x58, 0x00, 0x28, 0x2c}) {
		t.Fatalf("Brotli fail-open read %d times or mutated raw bytes: %x", source.reads, raw)
	}
}

func TestProtocolDefensivePathsAndFailureMapping(t *testing.T) {
	t.Parallel()
	var unresolved Protocol
	if unresolved.ID() != "" {
		t.Fatal("zero protocol unexpectedly resolved")
	}
	if _, err := unresolved.NewDecoder(strings.NewReader(""), allocation.NoopReserver{}); framing.ReasonOf(err) != framing.FailureInternal {
		t.Fatalf("zero decoder error = %v", err)
	}
	if stream, err := unresolved.NewStream(allocation.NoopReserver{}); stream != nil || framing.ReasonOf(err) != framing.FailureInternal {
		t.Fatalf("zero stream = %#v, %v", stream, err)
	}

	for input, want := range map[framing.FailureReason]AnalysisFailureReason{
		framing.FailureUnsupportedEncoding:   FailureUnsupportedEncoding,
		framing.FailureContentDecoding:       FailureContentDecoding,
		framing.FailureMalformedFrame:        FailureMalformedFrame,
		framing.FailureDecodedEventTooLarge:  FailureDecodedEventTooLarge,
		framing.FailureSemanticFieldTooLarge: FailureSemanticFieldTooLarge,
		framing.FailureReason("foreign"):     FailureAnalysisInternal,
	} {
		if got := failureFromFraming(input); got != want {
			t.Errorf("failureFromFraming(%q) = %q, want %q", input, got, want)
		}
	}
	if _, ok := selectProtocolID(nil, framing.KindJSON); ok {
		t.Fatal("empty protocol list resolved")
	}
	if !lastIsFailOpen([]Observation{{Class: EventError}, {Class: EventFailOpen}}) || lastIsFailOpen(nil) {
		t.Fatal("fail-open tail detection changed")
	}
}

func TestMediaAndCodingParsers(t *testing.T) {
	t.Parallel()
	for _, contentType := range []string{"application/json", "application/problem+json", "APPLICATION/VND.API+JSON; charset=UTF-8"} {
		if kind, ok := parseMediaKind(contentType); !ok || kind != framing.KindJSON {
			t.Errorf("JSON media %q = %v,%v", contentType, kind, ok)
		}
	}
	for _, contentType := range []string{"", "text/plain", "application/+json", "image/problem+json", "application/json; bad"} {
		if _, ok := parseMediaKind(contentType); ok {
			t.Errorf("invalid media %q accepted", contentType)
		}
	}
	if kind, ok := parseMediaKind("text/event-stream; charset=utf-8"); !ok || kind != framing.KindSSE {
		t.Fatalf("SSE media = %v,%v", kind, ok)
	}

	for input, want := range map[string]framing.ContentCoding{"": framing.CodingIdentity, " identity ": framing.CodingIdentity, "GZIP": framing.CodingGzip, "Br": framing.CodingBrotli} {
		if got, ok := ParseContentCoding(input); !ok || got != want {
			t.Errorf("coding %q = %v,%v", input, got, ok)
		}
	}
	for _, input := range []string{"gzip,br", "zstd"} {
		if _, ok := ParseContentCoding(input); ok {
			t.Errorf("coding %q accepted", input)
		}
	}
	if _, ok := selectProtocolID([]apicontract.ResponseProtocolID{"future.json.transport.v1"}, framing.KindJSON); ok {
		t.Fatal("protocol selection inferred semantics from an unknown ID spelling")
	}
}

func TestResolveResponseMediaUsesOnlyAuthoritativeOrUnambiguousEvidence(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name        string
		contentType string
		accept      []string
		wantType    string
		wantSource  ResponseMediaSource
		wantSSE     bool
		wantSupport bool
	}{
		{
			name: "declared response type is authoritative", contentType: "application/problem+json",
			accept: []string{mediaTypeEventStream}, wantType: "application/problem+json",
			wantSource: ResponseMediaFromContentType, wantSupport: true,
		},
		{
			name: "missing type recovers one accepted SSE representation", accept: []string{mediaTypeEventStream},
			wantType: mediaTypeEventStream, wantSource: ResponseMediaFromRequestAccept, wantSSE: true, wantSupport: true,
		},
		{
			name:     "equivalent JSON ranges remain unambiguous",
			accept:   []string{`application/json; q=0.8, application/problem+json; profile="a,b"`},
			wantType: mediaTypeJSON, wantSource: ResponseMediaFromRequestAccept, wantSupport: true,
		},
		{
			name:     "zero quality alternatives are not acceptable",
			accept:   []string{"text/plain; q=0", mediaTypeEventStream},
			wantType: mediaTypeEventStream, wantSource: ResponseMediaFromRequestAccept, wantSSE: true, wantSupport: true,
		},
		{name: "JSON and SSE are ambiguous", accept: []string{mediaTypeJSON + ", " + mediaTypeEventStream}, wantSource: ResponseMediaUnknown},
		{name: "wildcard is ambiguous", accept: []string{"*/*"}, wantSource: ResponseMediaUnknown},
		{name: "invalid quality is rejected", accept: []string{mediaTypeEventStream + "; q=2"}, wantSource: ResponseMediaUnknown},
		{
			name: "unsupported declared type does not defer to Accept", contentType: "text/plain",
			accept: []string{mediaTypeEventStream}, wantType: "text/plain", wantSource: ResponseMediaFromContentType,
		},
		{name: "missing evidence remains unknown", wantSource: ResponseMediaUnknown},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			media := ResolveResponseMedia(test.contentType, test.accept)
			if media.ContentType() != test.wantType || media.Source() != test.wantSource ||
				media.IsEventStream() != test.wantSSE || media.Supported() != test.wantSupport {
				t.Fatalf("media = %#v", media)
			}
		})
	}
}

func TestCapturedCodexUsageUsesNegotiatedMediaWhenResponseTypeIsMissing(t *testing.T) {
	t.Parallel()
	media := ResolveResponseMedia("", []string{mediaTypeEventStream})
	protocol, failure := NewRegistry().Resolve("codex", media.ContentType(), "identity")
	if failure != "" {
		t.Fatal(failure)
	}
	stream := mustNewStream(t, protocol)
	var observations []Observation
	payload := []byte("event: response.completed\n" +
		`data: {"type":"response.completed","response":{"status":"completed","usage":{"input_tokens":51114,"input_tokens_details":{"cache_write_tokens":0,"cached_tokens":49920},"output_tokens":103,"output_tokens_details":{"reasoning_tokens":8},"total_tokens":51217}}}` + "\n\n")
	feedCollect(stream, payload, true, &observations)
	if len(observations) != 1 || observations[0].Class != EventUsage || observations[0].Usage == nil {
		t.Fatalf("observations = %#v", observations)
	}
	usage := observations[0].Usage
	if usage.PromptTokens.Value != 51114 || usage.CompletionTokens.Value != 103 || usage.TotalTokens.Value != 51217 ||
		usage.CacheReadInputTokens.Value != 49920 || usage.ReasoningTokens.Value != 8 || usage.CacheCreation == nil ||
		usage.CacheCreation.InputTokens.Value != 0 || !usage.CacheCreation.InputTokens.Present {
		t.Fatalf("usage = %#v", usage)
	}
}

func loadProtocolFixtures(t *testing.T, name string) protocolFixtureFile {
	t.Helper()
	path := filepath.Join("..", "..", "contracts", "internal-error", "v1", name)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var fixtures protocolFixtureFile
	if err := json.Unmarshal(data, &fixtures); err != nil {
		t.Fatal(err)
	}
	if fixtures.SchemaVersion != 1 {
		t.Fatalf("schema version = %d", fixtures.SchemaVersion)
	}
	return fixtures
}

func decodeFixtureBody(t *testing.T, fixture protocolFixture) []byte {
	t.Helper()
	switch fixture.Body.Encoding {
	case "utf8":
		return []byte(fixture.Body.Value)
	case "base64":
		decoded, err := base64.StdEncoding.DecodeString(fixture.Body.Value)
		if err != nil {
			t.Fatal(err)
		}
		return decoded
	default:
		t.Fatalf("unknown body encoding %q", fixture.Body.Encoding)
		return nil
	}
}

func assertSingleFailure(t *testing.T, observations []Observation, want AnalysisFailureReason) {
	t.Helper()
	if len(observations) != 1 || observations[0].Class != EventFailOpen || observations[0].AnalysisReason != want {
		t.Fatalf("observations = %#v, want fail-open %q", observations, want)
	}
}

func mustNewStream(t *testing.T, protocol Protocol) *Stream {
	t.Helper()
	stream, err := protocol.NewStream(allocation.NoopReserver{})
	if err != nil {
		t.Fatal(err)
	}
	return stream
}

func feedCollect(stream *Stream, decoded []byte, eof bool, observations *[]Observation) {
	stream.Feed(decoded, eof, func(observation Observation) bool {
		*observations = append(*observations, observation)
		return true
	})
}

func analyze(protocol Protocol, source io.Reader) []Observation {
	return protocol.Analyze(source, allocation.NoopReserver{})
}

type countingReader struct {
	reader io.Reader
	reads  int
}

func (r *countingReader) Read(buffer []byte) (int, error) {
	r.reads++
	return r.reader.Read(buffer)
}

var _ io.Reader = (*bytes.Reader)(nil)
