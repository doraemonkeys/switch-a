package h2ingress

import (
	"bytes"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"go.uber.org/zap"
	"golang.org/x/net/http2"
	"golang.org/x/net/http2/hpack"
)

func TestSocketMalformedStreamKeepsHPACKTableSynchronized(t *testing.T) {
	result := make(chan string, 1)
	server := startServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		result <- r.Header.Get("X-Dynamic")
		w.WriteHeader(http.StatusNoContent)
	}), nil)
	_, writer := rawClient(t, server)
	var encoded bytes.Buffer
	encoder := hpack.NewEncoder(&encoded)
	for _, field := range append(initialFields(0), hpack.HeaderField{Name: "x-dynamic", Value: "shared-table-value"}, hpack.HeaderField{Name: "X-Invalid", Value: "uppercase"}) {
		_ = encoder.WriteField(field)
	}
	if err := writer.WriteHeaders(http2.HeadersFrameParam{StreamID: 1, EndHeaders: true, EndStream: true, BlockFragment: encoded.Bytes()}); err != nil {
		t.Fatal(err)
	}
	encoded.Reset()
	for _, field := range append(initialFields(0), hpack.HeaderField{Name: "x-dynamic", Value: "shared-table-value"}) {
		_ = encoder.WriteField(field)
	}
	if err := writer.WriteHeaders(http2.HeadersFrameParam{StreamID: 3, EndHeaders: true, EndStream: true, BlockFragment: encoded.Bytes()}); err != nil {
		t.Fatal(err)
	}
	if got := await(t, result); got != "shared-table-value" {
		t.Fatalf("dynamic header: %q", got)
	}
}

func TestSocketInvalidActualTrailersFailBody(t *testing.T) {
	for _, test := range []struct {
		name  string
		field hpack.HeaderField
		want  error
	}{
		{"forbidden", hpack.HeaderField{Name: "content-length", Value: "4"}, errInvalidTrailers},
		{"oversize", hpack.HeaderField{Name: "x-late", Value: strings.Repeat("a", 900)}, errIngressBudget},
	} {
		t.Run(test.name, func(t *testing.T) {
			result := make(chan error, 1)
			server := startServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, err := io.ReadAll(r.Body)
				result <- err
			}), func(s *http.Server) { s.MaxHeaderBytes = 512 })
			_, writer := rawClient(t, server)
			if err := writer.WriteHeaders(http2.HeadersFrameParam{StreamID: 1, EndHeaders: true, BlockFragment: block(initialFields(4)...)}); err != nil {
				t.Fatal(err)
			}
			if err := writer.WriteData(1, false, []byte("wire")); err != nil {
				t.Fatal(err)
			}
			if err := writer.WriteHeaders(http2.HeadersFrameParam{StreamID: 1, EndHeaders: true, EndStream: true, BlockFragment: block(test.field)}); err != nil {
				t.Fatal(err)
			}
			if err := await(t, result); !errors.Is(err, test.want) {
				t.Fatalf("body error %v, want %v", err, test.want)
			}
		})
	}
}

func TestAssociationLifecycleAndRejectedHeaders(t *testing.T) {
	c := newConnection(nil, 512, zap.NewNop())
	c.streams[1] = &trailers{}
	c.streams[3] = &trailers{attached: true}
	c.releaseUnattached(1)
	c.releaseUnattached(3)
	if c.streams[1] != nil || c.streams[3] == nil {
		t.Fatal("unattached cleanup crossed active handler lifetime")
	}
	c.release(3)
	if len(c.streams) != 0 {
		t.Fatal("active handler retained after release")
	}
	for id := uint32(1); id <= maxPendingStreams; id++ {
		c.streams[id] = &trailers{}
	}
	if err := c.observeHeaders(&http2.MetaHeadersFrame{HeadersFrame: &http2.HeadersFrame{FrameHeader: http2.FrameHeader{StreamID: maxPendingStreams + 1}}}); !errors.Is(err, errIngressBudget) {
		t.Fatal(err)
	}
	c.streams = map[uint32]*trailers{7: {headTooLarge: true}}
	request := httptest.NewRequest(http.MethodGet, "https://example.test", nil)
	request.Header.Set(associationHeader, "7")
	response := httptest.NewRecorder()
	c.wrap(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Fatal("oversize request reached handler") })).ServeHTTP(response, request)
	if response.Code != http.StatusRequestHeaderFieldsTooLarge || len(c.streams) != 0 {
		t.Fatal("rejected headers did not release association")
	}
	for _, marker := range []string{"", "invalid", "999"} {
		request := httptest.NewRequest(http.MethodGet, "https://example.test", nil)
		if marker != "" {
			request.Header.Set(associationHeader, marker)
		}
		response := httptest.NewRecorder()
		c.wrap(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Fatal("invalid marker reached handler") })).ServeHTTP(response, request)
		if response.Code != http.StatusInternalServerError {
			t.Fatal(response.Code)
		}
	}
}

func TestFrameCompletionFragmentationAndMetadataBound(t *testing.T) {
	var wire bytes.Buffer
	writer := http2.NewFramer(&wire, nil)
	_ = writer.WriteData(1, false, []byte("body"))
	_ = writer.WriteHeaders(http2.HeadersFrameParam{StreamID: 3, EndHeaders: true, EndStream: true, BlockFragment: []byte{0x88}})
	_ = writer.WriteRSTStream(5, http2.ErrCodeCancel)
	var parser frameCompletion
	var released []uint32
	for _, value := range wire.Bytes() {
		parser.observe([]byte{value}, func(id uint32) { released = append(released, id) })
	}
	if len(released) != 2 || released[0] != 3 || released[1] != 5 || parser.remaining != 0 || parser.headerBytes != 0 {
		t.Fatalf("completed %v, parser %+v", released, parser)
	}
	capture := boundedCapture{limit: 4}
	_, _ = capture.Write([]byte("1234"))
	if n, err := capture.Write([]byte("5")); n != 0 || !errors.Is(err, errIngressBudget) || capture.Len() != 4 {
		t.Fatal("capture exceeded bound")
	}
	c := newConnection(nil, 512, zap.NewNop())
	if n, err := c.Read(nil); n != 0 || err != nil {
		t.Fatal("empty read failed")
	}
	if state := c.ConnectionState(); state.Version != 0 {
		t.Fatal("unexpected synthetic TLS state")
	}
}
