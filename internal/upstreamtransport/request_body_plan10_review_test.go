package upstreamtransport

import (
	"bytes"
	"context"
	"crypto/tls"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/net/http2"
	"golang.org/x/net/http2/hpack"
)

func TestPlan10ReviewHTTP2RefusedStreamPreservesNativeRetry(t *testing.T) {
	for _, owned := range []bool{false, true} {
		name := "native"
		if owned {
			name = "source"
		}
		t.Run(name, func(t *testing.T) {
			var attempts atomic.Int32
			server := httptest.NewUnstartedServer(http.NotFoundHandler())
			server.EnableHTTP2 = true
			server.Config.TLSNextProto = map[string]func(*http.Server, *tls.Conn, http.Handler){
				"h2": func(_ *http.Server, conn *tls.Conn, _ http.Handler) {
					defer conn.Close()
					preface := make([]byte, len(http2.ClientPreface))
					if _, err := io.ReadFull(conn, preface); err != nil {
						return
					}
					framer := http2.NewFramer(conn, conn)
					if err := framer.WriteSettings(); err != nil {
						return
					}
					for {
						frame, err := framer.ReadFrame()
						if err != nil {
							return
						}
						switch f := frame.(type) {
						case *http2.SettingsFrame:
							if !f.IsAck() {
								_ = framer.WriteSettingsAck()
							}
						case *http2.HeadersFrame:
							if attempts.Add(1) == 1 {
								_ = framer.WriteRSTStream(f.StreamID, http2.ErrCodeRefusedStream)
							} else {
								var block bytes.Buffer
								encoder := hpack.NewEncoder(&block)
								_ = encoder.WriteField(hpack.HeaderField{Name: ":status", Value: "200"})
								_ = framer.WriteHeaders(http2.HeadersFrameParam{StreamID: f.StreamID, BlockFragment: block.Bytes(), EndHeaders: true, EndStream: true})
							}
						}
					}
				},
			}
			server.StartTLS()
			defer server.Close()
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			transport := New(Config{})
			defer transport.CloseIdleConnections()
			base := transport.followClient.Transport.(*http.Transport)
			base.ForceAttemptHTTP2 = true
			base.TLSClientConfig = server.Client().Transport.(*http.Transport).TLSClientConfig.Clone()
			var request *http.Request
			var err error
			if owned {
				request, err = BuildRequest(ctx, http.MethodPost, server.URL, testBodySource([]byte("wire")), httptest.NewRequest(http.MethodPost, "http://gateway.test", nil))
			} else {
				request, err = http.NewRequestWithContext(ctx, http.MethodPost, server.URL, strings.NewReader("wire"))
			}
			if err != nil {
				t.Fatal(err)
			}
			response, _, err := transport.Fetch(ctx, request, ExecutionOptions{})
			if err != nil {
				t.Fatalf("native-eligible REFUSED_STREAM retry lost: attempts=%d error=%T %v", attempts.Load(), err, err)
			}
			closeResponse(t, response)
			if attempts.Load() != 2 {
				t.Fatalf("attempts=%d", attempts.Load())
			}
		})
	}
}
