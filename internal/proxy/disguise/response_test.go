package disguise

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/doraemonkeys/switch-a/internal/codex/clientdisguise"
	"github.com/doraemonkeys/switch-a/internal/codex/clientdisguise/wire"
	"github.com/doraemonkeys/switch-a/internal/upstreamtransport"
)

func responseSession() *wire.Session {
	return wire.NewSession(nil, clientdisguise.TargetSnapshot{Policy: clientdisguise.Policy{Enabled: true}}, "client", "operation")
}
func jsonHead() upstreamtransport.ResponseHead {
	return upstreamtransport.ResponseHead{Header: http.Header{"Content-Type": []string{"application/json"}, "Content-Length": []string{"10"}}}
}

func TestResponseStreamBoundsInputAndJoinsValidOutput(t *testing.T) {
	var output bytes.Buffer
	head, stream, err := NewResponseStream(context.Background(), responseSession(), jsonHead(), &output)
	if err != nil {
		t.Fatal(err)
	}
	if head.Header.Get("Content-Length") != "" {
		t.Fatal("derived length retained")
	}
	payload := `{"business":"` + strings.Repeat("x", 65536) + `"}`
	for offset := 0; offset < len(payload); {
		end := min(offset+997, len(payload))
		if _, err := stream.Write([]byte(payload[offset:end])); err != nil {
			t.Fatal(err)
		}
		offset = end
	}
	if err := stream.Close(); err != nil {
		t.Fatal(err)
	}
	if output.String() != payload {
		t.Fatal("opaque business value changed")
	}
}

func TestResponseStreamMalformedTailIsTerminal(t *testing.T) {
	session := responseSession()
	_, stream, err := NewResponseStream(context.Background(), session, jsonHead(), io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = stream.Write([]byte(`{"business":"unfinished`))
	err = stream.Close()
	var failure *wire.Failure
	if !errors.As(err, &failure) || session.Failure() == nil {
		t.Fatalf("failure=%v", err)
	}
}

func TestResponseStreamAbortAndSinkErrorPreserveLifecycle(t *testing.T) {
	t.Run("abort", func(t *testing.T) {
		session := responseSession()
		_, stream, err := NewResponseStream(context.Background(), session, jsonHead(), io.Discard)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = stream.Write([]byte(`{"business":"`))
		stream.Abort()
		if session.Failure() != nil {
			t.Fatal("abort became a transformation fault")
		}
	})
	t.Run("sink", func(t *testing.T) {
		session := responseSession()
		boom := errors.New("downstream closed")
		_, stream, err := NewResponseStream(context.Background(), session, jsonHead(), WriterFunc(func([]byte) (int, error) { return 0, boom }))
		if err != nil {
			t.Fatal(err)
		}
		_, _ = stream.Write([]byte(`{"business":"value"}`))
		if err := stream.Close(); !errors.Is(err, boom) {
			t.Fatalf("sink failure=%v", err)
		}
		if session.Failure() != nil {
			t.Fatal("sink became transformation fault")
		}
	})
	t.Run("coding", func(t *testing.T) {
		head := jsonHead()
		head.Header.Set("Content-Encoding", "unsupported")
		_, _, err := NewResponseStream(context.Background(), responseSession(), head, io.Discard)
		if err == nil {
			t.Fatal("invalid coding accepted")
		}
	})
}
