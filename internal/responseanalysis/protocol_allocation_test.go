package responseanalysis

import (
	"bytes"
	"compress/gzip"
	"fmt"
	"io"
	"strings"
	"sync"
	"testing"

	"github.com/doraemonkeys/switch-a/internal/responseanalysis/allocation"
)

type protocolTrackingReserver struct {
	mu        sync.Mutex
	active    int
	calls     int
	denyClass allocation.Class
	deny      allocation.DenialReason
	denied    bool
}

func (r *protocolTrackingReserver) Reserve(class allocation.Class, capacity int) (allocation.Grant, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls++
	if !r.denied && class == r.denyClass && r.deny != "" {
		r.denied = true
		return nil, &allocation.Denial{
			Reason:            r.deny,
			Class:             class,
			RequestedCapacity: capacity,
		}
	}
	r.active++
	return &protocolTrackingGrant{owner: r}, nil
}

func (r *protocolTrackingReserver) activeGrants() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.active
}

type protocolTrackingGrant struct {
	once  sync.Once
	owner *protocolTrackingReserver
}

func (g *protocolTrackingGrant) Release() {
	if g == nil || g.owner == nil {
		return
	}
	g.once.Do(func() {
		g.owner.mu.Lock()
		defer g.owner.mu.Unlock()
		g.owner.active--
	})
}

func TestStreamTransfersSemanticResourcesToObservation(t *testing.T) {
	protocol, failure := NewRegistry().Resolve("claude", "text/event-stream", "identity")
	if failure != "" {
		t.Fatal(failure)
	}
	reserver := &protocolTrackingReserver{}
	stream, err := protocol.NewStream(reserver)
	if err != nil {
		t.Fatal(err)
	}
	var observed Observation
	stream.Feed(
		[]byte("event: error\ndata: {\"type\":\"error\",\"error\":{\"type\":\"BUSY\",\"message\":\"RETRY\"}}\n\n"),
		true,
		func(observation Observation) bool {
			observed = observation
			return true
		},
	)
	if observed.Class != EventError || observed.Fields == nil || observed.Fields.Message != "RETRY" {
		t.Fatalf("observation = %#v", observed)
	}
	if reserver.activeGrants() == 0 {
		t.Fatal("semantic grants were released before the observation")
	}
	stream.Release()
	if reserver.activeGrants() == 0 {
		t.Fatal("stream release freed observation-owned semantic grants")
	}
	observed.Release()
	observed.Release()
	if active := reserver.activeGrants(); active != 0 {
		t.Fatalf("active grants = %d", active)
	}
}

func TestStreamMapsRequestAndProcessReservationDenials(t *testing.T) {
	protocol, failure := NewRegistry().Resolve("claude", "text/event-stream", "identity")
	if failure != "" {
		t.Fatal(failure)
	}
	tests := []struct {
		name       string
		denyClass  allocation.Class
		deny       allocation.DenialReason
		wantReason AnalysisFailureReason
	}{
		{
			name:       "framing request denial",
			denyClass:  allocation.ClassFramingBuffer,
			deny:       allocation.DenialRequestMemoryExhausted,
			wantReason: FailureRequestMemoryExhausted,
		},
		{
			name:       "semantic process denial",
			denyClass:  allocation.ClassSemanticFields,
			deny:       allocation.DenialProcessMemoryExhausted,
			wantReason: FailureProcessMemoryExhausted,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			reserver := &protocolTrackingReserver{denyClass: test.denyClass, deny: test.deny}
			stream, err := protocol.NewStream(reserver)
			if err != nil {
				t.Fatal(err)
			}
			var observations []Observation
			stream.Feed(
				[]byte("event: error\ndata: {\"type\":\"error\",\"error\":{\"type\":\"BUSY\",\"message\":\"RETRY\"}}\n\n"),
				true,
				func(observation Observation) bool {
					observations = append(observations, observation)
					return true
				},
			)
			if len(observations) != 1 || observations[0].Class != EventFailOpen ||
				observations[0].AnalysisReason != test.wantReason {
				t.Fatalf("observations = %#v", observations)
			}
			ReleaseObservations(observations)
			stream.Release()
			if active := reserver.activeGrants(); active != 0 {
				t.Fatalf("active grants = %d", active)
			}
		})
	}
}

func TestAnalyzeScratchReservationDenialIsStableAndLeakFree(t *testing.T) {
	protocol, failure := NewRegistry().Resolve("codex", "application/json", "identity")
	if failure != "" {
		t.Fatal(failure)
	}
	reserver := &protocolTrackingReserver{
		denyClass: allocation.ClassDecodedBuffer,
		deny:      allocation.DenialRequestMemoryExhausted,
	}
	observations := protocol.Analyze(strings.NewReader("{}"), reserver)
	assertSingleFailure(t, observations, FailureRequestMemoryExhausted)
	ReleaseObservations(observations)
	if active := reserver.activeGrants(); active != 0 {
		t.Fatalf("active grants = %d", active)
	}
}

func TestAnalyzeStopsGzipExpansionAtErrorObservationBound(t *testing.T) {
	protocol, failure := NewRegistry().Resolve("claude", "text/event-stream", "gzip")
	if failure != "" {
		t.Fatal(failure)
	}
	decoded := manyAnthropicErrors(500)
	compressed := gzipBytes(t, decoded)
	source := &byteCountingReader{reader: bytes.NewReader(compressed)}
	reserver := &protocolTrackingReserver{}

	observations := protocol.Analyze(source, reserver)
	if len(observations) != MaxTestMessageErrors+1 {
		t.Fatalf("observation count = %d", len(observations))
	}
	for index := range MaxTestMessageErrors {
		if observations[index].Class != EventError {
			t.Fatalf("observation %d = %#v", index, observations[index])
		}
	}
	if tail := observations[len(observations)-1]; tail.Class != EventFailOpen ||
		tail.AnalysisReason != FailureRequestMemoryExhausted {
		t.Fatalf("tail = %#v", tail)
	}
	if source.bytesRead >= len(compressed) {
		t.Fatalf("analyzer consumed all %d compressed bytes after reaching its observation bound", len(compressed))
	}
	if reserver.activeGrants() == 0 {
		t.Fatal("retained error observations lost their semantic grants")
	}
	ReleaseObservations(observations)
	if active := reserver.activeGrants(); active != 0 {
		t.Fatalf("active grants after result release = %d", active)
	}
}

func TestAnalyzeDecodedBodyCapStopsBeforeProtocolEOF(t *testing.T) {
	protocol, failure := NewRegistry().Resolve("claude", "text/event-stream", "identity")
	if failure != "" {
		t.Fatal(failure)
	}
	limits := AnalysisLimits{MaxDecodedBodyBytes: 64, MaxErrorObservations: 2}
	observations := protocol.AnalyzeBounded(
		strings.NewReader(strings.Repeat("event: ping\ndata: {\"type\":\"ping\"}\n\n", 10)),
		allocation.NoopReserver{},
		limits,
	)
	assertSingleFailure(t, observations, FailureRequestMemoryExhausted)
}

func TestFlagshipThirdSSEErrorAt72KiBIsFramedAndClassified(t *testing.T) {
	protocol, failure := NewRegistry().Resolve("claude", "text/event-stream", "identity")
	if failure != "" {
		t.Fatal(failure)
	}
	stream, err := protocol.NewStream(allocation.NoopReserver{})
	if err != nil {
		t.Fatal(err)
	}
	const targetThirdEventBytes = 72 * 1024
	prefix := "event: error\ndata: {\"type\":\"error\",\"padding\":\""
	suffix := "\",\"error\":{\"type\":\"OVERLOADED\",\"message\":\"RETRY LATER\"}}\n\n"
	third := prefix + strings.Repeat("x", targetThirdEventBytes-len(prefix)-len(suffix)) + suffix
	wire := "event: ping\ndata: {\"type\":\"ping\"}\n\n" +
		"event: message_start\ndata: {\"type\":\"message_start\"}\n\n" +
		third
	var observations []Observation
	stream.Feed([]byte(wire), true, func(observation Observation) bool {
		observations = append(observations, observation)
		return true
	})
	defer ReleaseObservations(observations)
	if len(observations) != 3 ||
		observations[0].Class != EventControl ||
		observations[1].Class != EventControl ||
		observations[2].Class != EventError ||
		observations[2].Fields == nil ||
		observations[2].Fields.Message != "RETRY LATER" {
		t.Fatalf("observations = %#v", observations)
	}
}

func TestAnalyzeLimitsRejectExpansionBeyondHardMaximum(t *testing.T) {
	var unresolved Protocol
	for _, limits := range []AnalysisLimits{
		{},
		{MaxDecodedBodyBytes: MaxTestMessageDecodedBodyBytes + 1, MaxErrorObservations: 1},
		{MaxDecodedBodyBytes: 1, MaxErrorObservations: MaxTestMessageErrors + 1},
	} {
		assertSingleFailure(
			t,
			unresolved.AnalyzeBounded(strings.NewReader(""), allocation.NoopReserver{}, limits),
			FailureAnalysisInternal,
		)
	}
}

func manyAnthropicErrors(count int) []byte {
	var builder strings.Builder
	state := uint64(0x9e3779b97f4a7c15)
	for index := range count {
		builder.WriteString("event: error\ndata: {\"type\":\"error\",\"padding\":\"")
		for range 256 {
			state ^= state << 13
			state ^= state >> 7
			state ^= state << 17
			builder.WriteByte("0123456789abcdef"[state&15])
		}
		builder.WriteString("\",\"error\":{\"type\":\"BUSY\",\"message\":\"RETRY ")
		fmt.Fprint(&builder, index)
		builder.WriteString("\"}}\n\n")
	}
	return []byte(builder.String())
}

func gzipBytes(t *testing.T, data []byte) []byte {
	t.Helper()
	var compressed bytes.Buffer
	writer := gzip.NewWriter(&compressed)
	if _, err := writer.Write(data); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return compressed.Bytes()
}

type byteCountingReader struct {
	reader    io.Reader
	bytesRead int
}

func (r *byteCountingReader) Read(buffer []byte) (int, error) {
	n, err := r.reader.Read(buffer)
	r.bytesRead += n
	return n, err
}
