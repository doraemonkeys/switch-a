package proxy

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/doraemonkeys/switch-a/internal/requestcapture"
	"github.com/doraemonkeys/switch-a/internal/requestingress"
)

type captureFailedReplayStorage struct{ bytes.Buffer }

func (*captureFailedReplayStorage) ReadAt([]byte, int64) (int, error) {
	return 0, errors.New("replay disk read failed")
}
func (*captureFailedReplayStorage) Close() error  { return nil }
func (*captureFailedReplayStorage) Remove() error { return nil }

func TestCaptureIngressReceivesLateReplayFailureWithoutReplacingEOF(t *testing.T) {
	provider := requestcapture.ProviderIdentity{ID: "late-replay", Name: "late replay"}
	manager, session := startCaptureTestManager(t, []requestcapture.ProviderIdentity{provider})
	defer manager.Close()
	pctx := &proxyContext{capture: manager.BeginGateway(requestcapture.GatewayStart{GatewayRequestID: "late-replay"})}
	source := httptest.NewRequest(http.MethodPost, "http://gateway.test/upload", bytes.NewBufferString("payload"))
	handle, err := requestingress.Start(context.Background(), source, requestingress.Options{
		MemoryBytes: -1, CreateStorage: func() (requestingress.Storage, error) { return &captureFailedReplayStorage{}, nil },
		OnHead: pctx.beginCaptureIngress, OnChunk: pctx.observeCaptureIngressChunk, OnFinish: pctx.finishCaptureIngress, OnFailure: pctx.failCaptureIngress,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer handle.Close()
	if err := handle.Wait(context.Background()); err != nil {
		t.Fatal(err)
	}
	recorder := pctx.capture.BeginHTTP(requestcapture.RawHTTPStart{URL: source.URL, Attempt: requestcapture.AttemptMetadata{Provider: provider}})
	reader, err := handle.Open()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := reader.Read(make([]byte, 1)); err == nil {
		t.Fatal("storage failure was not injected")
	}
	_ = reader.Close()
	detail, err := readCaptureTestDetail(manager, session, recorder.ID(), 64)
	if err != nil {
		t.Fatal(err)
	}
	input := detail.HTTP.Request.Ingress
	if input.State != "complete" || input.ReceivedBytes != 7 || input.SourceFailure == nil || input.SourceFailure.Kind != requestcapture.IngressFailureStorage || input.SourceFailure.Reason != handle.Snapshot().Err.Error() {
		t.Fatalf("late source failure=%+v", input)
	}
	pctx.capture.Finish(requestcapture.GatewayOutcome{})
}

func TestCaptureIngressFailureMapping(t *testing.T) {
	for _, kind := range []requestingress.FailureKind{requestingress.FailureRead, requestingress.FailureLength, requestingress.FailureLimit, ""} {
		t.Run(string(kind), func(t *testing.T) {
			provider := requestcapture.ProviderIdentity{ID: "mapping"}
			manager, session := startCaptureTestManager(t, []requestcapture.ProviderIdentity{provider})
			defer manager.Close()
			pctx := &proxyContext{capture: manager.BeginGateway(requestcapture.GatewayStart{GatewayRequestID: "mapping"})}
			pctx.beginCaptureIngress(requestingress.Head{})
			pctx.failCaptureIngress(requestingress.Snapshot{FailureKind: kind})
			recorder := pctx.capture.BeginHTTP(requestcapture.RawHTTPStart{Attempt: requestcapture.AttemptMetadata{Provider: provider}})
			detail, err := readCaptureTestDetail(manager, session, recorder.ID(), 64)
			if err != nil {
				t.Fatal(err)
			}
			want := string(kind)
			if want == "" {
				want = "unknown"
			}
			if string(detail.HTTP.Request.Ingress.SourceFailure.Kind) != want {
				t.Fatalf("kind=%+v want=%s", detail.HTTP.Request.Ingress.SourceFailure, want)
			}
			pctx.capture.Finish(requestcapture.GatewayOutcome{})
		})
	}
}
