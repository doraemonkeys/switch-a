package wire

import (
	"bufio"
	"compress/gzip"
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/doraemonkeys/switch-a/internal/upstreamtransport"
)

func TestCompressedSSEFlushesCompletedEventBeforeUpstreamEOF(t *testing.T) {
	s := testSession()
	ctx := context.Background()
	originalReader, originalWriter := io.Pipe()
	release := make(chan struct{})
	go func() {
		encoder := gzip.NewWriter(originalWriter)
		_, _ = io.WriteString(encoder, "event: response.created\ndata: {}\n\n")
		_ = encoder.Flush()
		<-release
		_ = encoder.Close()
		_ = originalWriter.Close()
	}()
	head := upstreamtransport.ResponseHead{Header: http.Header{"Content-Type": {"text/event-stream"}, "Content-Encoding": {"gzip"}}}
	_, restored, err := s.RestoreResponse(ctx, head, originalReader)
	if err != nil {
		t.Fatal(err)
	}
	defer restored.Close()
	event := make(chan string, 1)
	go func() {
		decoder, err := gzip.NewReader(restored)
		if err != nil {
			event <- err.Error()
			return
		}
		reader := bufio.NewReader(decoder)
		var output strings.Builder
		for range 3 {
			line, err := reader.ReadString('\n')
			output.WriteString(line)
			if err != nil {
				event <- err.Error()
				return
			}
		}
		event <- output.String()
		_, _ = io.Copy(io.Discard, decoder)
		_ = decoder.Close()
	}()
	select {
	case got := <-event:
		if got != "event: response.created\ndata: {}\n\n" {
			close(release)
			t.Fatal(got)
		}
	case <-time.After(2 * time.Second):
		close(release)
		t.Fatal("compressed event waited for upstream EOF")
	}
	close(release)
}
