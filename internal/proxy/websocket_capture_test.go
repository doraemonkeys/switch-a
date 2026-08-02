package proxy

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/doraemonkeys/switch-a/internal/model"
	"github.com/doraemonkeys/switch-a/internal/proxy/capturebridge"
	"github.com/doraemonkeys/switch-a/internal/proxy/capturefailure"
	"github.com/doraemonkeys/switch-a/internal/requestcapture"

	"github.com/coder/websocket"
	"go.uber.org/zap/zaptest"
)

func TestWebSocketDialCaptureFailureBodyCompletionTracksExistingDrain(t *testing.T) {
	t.Parallel()

	readFailure := errors.New("body read failed")
	var exactBoundaryBody *noProbeBoundaryReadCloser
	tests := []struct {
		name             string
		newBody          func() io.ReadCloser
		contentLength    int64
		wantBytes        int64
		wantCompletion   requestcapture.SourceCompletion
		wantReachedEOF   bool
		wantLimitReached bool
		wantReadErr      error
		assertBody       func(*testing.T)
	}{
		{
			name:           "status with declared empty body is complete",
			newBody:        func() io.ReadCloser { return nil },
			contentLength:  0,
			wantCompletion: requestcapture.SourceCompletionComplete,
		},
		{
			name: "body smaller than drain limit is complete",
			newBody: func() io.ReadCloser {
				return io.NopCloser(strings.NewReader("small failure"))
			},
			contentLength:  int64(len("small failure")),
			wantBytes:      int64(len("small failure")),
			wantCompletion: requestcapture.SourceCompletionComplete,
			wantReachedEOF: true,
		},
		{
			name: "body exactly at drain limit remains partial without a probe read",
			newBody: func() io.ReadCloser {
				exactBoundaryBody = &noProbeBoundaryReadCloser{remaining: maxDrainBytes}
				return exactBoundaryBody
			},
			contentLength:    -1,
			wantBytes:        maxDrainBytes,
			wantCompletion:   requestcapture.SourceCompletionPartial,
			wantLimitReached: true,
			assertBody: func(t *testing.T) {
				t.Helper()
				if exactBoundaryBody.readsPastBoundary != 0 {
					t.Fatalf("reads past exact boundary = %d, want 0", exactBoundaryBody.readsPastBoundary)
				}
				if !exactBoundaryBody.closed {
					t.Fatal("exact-boundary body was not closed")
				}
			},
		},
		{
			name: "declared body exactly at drain limit proves completion",
			newBody: func() io.ReadCloser {
				return io.NopCloser(bytes.NewReader(bytes.Repeat([]byte("d"), maxDrainBytes)))
			},
			contentLength:    maxDrainBytes,
			wantBytes:        maxDrainBytes,
			wantCompletion:   requestcapture.SourceCompletionComplete,
			wantLimitReached: true,
		},
		{
			name: "body beyond drain limit is partial",
			newBody: func() io.ReadCloser {
				return io.NopCloser(bytes.NewReader(bytes.Repeat([]byte("x"), maxDrainBytes+1)))
			},
			contentLength:    -1,
			wantBytes:        maxDrainBytes,
			wantCompletion:   requestcapture.SourceCompletionPartial,
			wantLimitReached: true,
		},
		{
			name: "body read error is partial",
			newBody: func() io.ReadCloser {
				return &captureReadErrorBody{payload: []byte("partial"), err: readFailure}
			},
			contentLength:  -1,
			wantBytes:      int64(len("partial")),
			wantCompletion: requestcapture.SourceCompletionPartial,
			wantReadErr:    readFailure,
		},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			manager, session := startCaptureTestManager(t, []requestcapture.ProviderIdentity{{
				ID:   "provider",
				Name: "Provider",
			}})
			defer manager.Close()
			gateway := manager.BeginGateway(requestcapture.GatewayStart{GatewayRequestID: test.name})
			body := test.newBody()
			forwarder := NewWebSocketForwarder(WebSocketForwarderConfig{
				Logger: zaptest.NewLogger(t),
				Dialer: &mockDialer{dialFunc: func(
					context.Context,
					string,
					*websocket.DialOptions,
				) (*websocket.Conn, *http.Response, error) {
					return nil, &http.Response{
						StatusCode:    http.StatusBadGateway,
						Proto:         "HTTP/1.1",
						ContentLength: test.contentLength,
						Header:        http.Header{"X-Upstream": {"rejected"}},
						Body:          body,
					}, errors.New("handshake rejected")
				}},
			})

			exchange := forwarder.dialUpstream(context.Background(), WebSocketDialRequest{
				URL:                 "wss://upstream.example/responses",
				Headers:             http.Header{"Authorization": {"Bearer capture-secret"}},
				Capture:             gateway,
				CaptureParticipates: true,
				Attempt:             captureWebSocketTestAttempt("provider", requestcapture.CredentialPhaseInitial),
			})
			if exchange.Accepted() {
				t.Fatal("dial exchange unexpectedly accepted")
			}
			if exchange.HandshakeStatusCode != http.StatusBadGateway || exchange.HandshakeProtocol != "HTTP/1.1" {
				t.Fatalf("handshake = (%d, %q)", exchange.HandshakeStatusCode, exchange.HandshakeProtocol)
			}
			if exchange.StartedAt.IsZero() || exchange.CompletedAt.Before(exchange.StartedAt) ||
				!exchange.HandshakeObservedAt.Equal(exchange.CompletedAt) {
				t.Fatalf("dial timestamps = start:%v observed:%v completed:%v", exchange.StartedAt, exchange.HandshakeObservedAt, exchange.CompletedAt)
			}
			if exchange.ObservedFailureBodyBytes != test.wantBytes {
				t.Fatalf("observed body bytes = %d, want %d", exchange.ObservedFailureBodyBytes, test.wantBytes)
			}
			if exchange.FailureBodyReachedEOF != test.wantReachedEOF ||
				exchange.FailureBodyLimitReached != test.wantLimitReached ||
				!errors.Is(exchange.FailureBodyReadErr, test.wantReadErr) {
				t.Fatalf(
					"body observation = eof:%t limit:%t err:%v",
					exchange.FailureBodyReachedEOF,
					exchange.FailureBodyLimitReached,
					exchange.FailureBodyReadErr,
				)
			}
			outcome := webSocketDialFailureCaptureOutcome(
				context.Background(),
				exchange,
				webSocketDialFailureReason(exchange, true),
			)
			if outcome.SourceCompletion != test.wantCompletion {
				t.Fatalf("source completion = %q, want %q", outcome.SourceCompletion, test.wantCompletion)
			}
			if outcome.TerminationReason != requestcapture.TerminationReasonStatusFailoverDrain {
				t.Fatalf("termination = %q, want status failover drain", outcome.TerminationReason)
			}
			if outcome.Failure.Primary.Code != requestcapture.FailureCodeHandshakeRejected ||
				outcome.Failure.Primary.HTTPStatusCode != http.StatusBadGateway ||
				outcome.Failure.HasSecondary != (test.wantReadErr != nil) {
				t.Fatalf("handshake failure = %#v", outcome.Failure)
			}
			if test.wantReadErr != nil &&
				outcome.Failure.Secondary.Code != requestcapture.FailureCodeFailureBodyRead {
				t.Fatalf("failure-body observation = %#v", outcome.Failure.Secondary)
			}
			finishWebSocketDialCapture(exchange, outcome)
			gateway.Finish(requestcapture.GatewayOutcome{})

			page, err := readCaptureTestPage(manager, session, requestcapture.ListQuery{Limit: 10})
			if err != nil {
				t.Fatalf("ListRecords() error = %v", err)
			}
			if len(page.Records) != 1 {
				t.Fatalf("records = %d, want 1", len(page.Records))
			}
			if page.Records[0].SourceCompletion != test.wantCompletion ||
				page.Records[0].UpstreamObservedBytes != test.wantBytes {
				t.Fatalf("record summary = %#v", page.Records[0])
			}
			if test.assertBody != nil {
				test.assertBody(t)
			}
		})
	}
}

func TestWebSocketDialCaptureTransportFailureDoesNotMasqueradeAsStatusDrain(t *testing.T) {
	t.Parallel()

	dialErr := errors.New("dial tcp: refused")
	exchange := DialExchange{Err: dialErr}
	if got := webSocketDialFailureReason(exchange, true); got != requestcapture.TerminationReasonTransportError {
		t.Fatalf("failure reason = %q, want transport_error", got)
	}
	outcome := webSocketDialFailureCaptureOutcome(context.Background(), exchange, webSocketDialFailureReason(exchange, true))
	if outcome.SourceCompletion != requestcapture.SourceCompletionPartial ||
		outcome.TerminationReason != requestcapture.TerminationReasonTransportError ||
		outcome.Failure.Primary.Code != requestcapture.FailureCodeWebSocketDial {
		t.Fatalf("outcome = %#v", outcome)
	}
}

func TestWebSocketReplayLineageFollowsPhysicalWriteSuccess(t *testing.T) {
	t.Run("capture disabled still marks a successful delivery", func(t *testing.T) {
		buffer := newPreVisibleClientMessageBuffer(preVisibleClientReplayBufferLimitBytes)
		buffer.RecordWithLineage(
			websocket.MessageText,
			[]byte("payload"),
			false,
			requestcapture.MessageLineage{},
		)
		orchestrator := &WebSocketSessionOrchestrator{replayBuffer: buffer}
		server := newRecordingWSServer(t, make(chan webSocketReplayMessage, 1))
		defer server.Close()

		if err := deliverBufferedMessages(t, orchestrator, context.Background(), server, webSocketRelayOptions{}); err != nil {
			t.Fatalf("deliverBufferedMessages() error = %v", err)
		}
		snapshot := buffer.Snapshot()
		if len(snapshot.Messages) != 1 || !snapshot.Messages[0].Delivered {
			t.Fatalf("buffer snapshot = %#v, want physically delivered", snapshot)
		}
	})

	t.Run("unselected success makes selected delivery a replay", func(t *testing.T) {
		manager, session := startCaptureTestManager(t, []requestcapture.ProviderIdentity{{
			ID:   "selected",
			Name: "Selected",
		}})
		defer manager.Close()
		gateway := manager.BeginGateway(requestcapture.GatewayStart{GatewayRequestID: "unselected-selected"})
		sourceLineage := gateway.NewMessageID()
		if !sourceLineage.Valid() {
			t.Fatal("source lineage is invalid")
		}
		buffer := newPreVisibleClientMessageBuffer(preVisibleClientReplayBufferLimitBytes)
		buffer.RecordWithLineage(websocket.MessageText, []byte("payload"), false, sourceLineage)
		orchestrator := &WebSocketSessionOrchestrator{replayBuffer: buffer}
		server := newRecordingWSServer(t, make(chan webSocketReplayMessage, 2))
		defer server.Close()

		unselected := beginWebSocketCaptureTestRecord(gateway, "unselected", requestcapture.SelectionModeInitial)
		if err := deliverBufferedMessages(t, orchestrator, context.Background(), server, webSocketRelayOptions{
			GatewayCapture: gateway,
			Capture:        unselected,
			CaptureMode:    capturebridge.ModeTransition,
		}); err != nil {
			t.Fatalf("unselected delivery error = %v", err)
		}
		unselected.Finish(requestcapture.Outcome{
			SourceCompletion:  requestcapture.SourceCompletionPartial,
			TerminationReason: requestcapture.TerminationReasonStatusFailoverDrain,
		})

		selected := beginWebSocketCaptureTestRecord(gateway, "selected", requestcapture.SelectionModeReplacement)
		if err := deliverBufferedMessages(t, orchestrator, context.Background(), server, webSocketRelayOptions{
			GatewayCapture: gateway,
			Capture:        selected,
			CaptureMode:    capturebridge.ModePayload,
		}); err != nil {
			t.Fatalf("selected delivery error = %v", err)
		}
		selected.Finish(requestcapture.Outcome{
			SourceCompletion:  requestcapture.SourceCompletionComplete,
			TerminationReason: requestcapture.TerminationReasonWebSocketClose,
		})
		gateway.Finish(requestcapture.GatewayOutcome{})

		detail := getWebSocketCaptureTestDetail(t, manager, session, selected.ID())
		assertCapturedMessage(
			t,
			detail.WebSocket.Messages,
			requestcapture.MessageSourceReplay,
			requestcapture.MessageDispositionForwarded,
			detail.WebSocket.Messages[0].SourceMessageID,
		)
		if detail.WebSocket.Messages[0].SourceMessageID == "" ||
			detail.WebSocket.Messages[0].SourceMessageID == detail.WebSocket.Messages[0].MessageID {
			t.Fatalf("replay lineage IDs = %#v", detail.WebSocket.Messages[0])
		}
	})

	t.Run("write failure keeps the next successful delivery live", func(t *testing.T) {
		manager, session := startCaptureTestManager(t, []requestcapture.ProviderIdentity{
			{ID: "first", Name: "First"},
			{ID: "second", Name: "Second"},
		})
		defer manager.Close()
		gateway := manager.BeginGateway(requestcapture.GatewayStart{GatewayRequestID: "write-retry"})
		sourceLineage := gateway.NewMessageID()
		if !sourceLineage.Valid() {
			t.Fatal("source lineage is invalid")
		}
		buffer := newPreVisibleClientMessageBuffer(preVisibleClientReplayBufferLimitBytes)
		buffer.RecordWithLineage(websocket.MessageText, []byte("payload"), false, sourceLineage)
		orchestrator := &WebSocketSessionOrchestrator{replayBuffer: buffer}
		server := newRecordingWSServer(t, make(chan webSocketReplayMessage, 2))
		defer server.Close()

		first := beginWebSocketCaptureTestRecord(gateway, "first", requestcapture.SelectionModeInitial)
		connectCtx, cancelConnect := context.WithTimeout(context.Background(), 5*time.Second)
		failedConn, _, err := websocket.Dial(connectCtx, wsURL(server), nil)
		cancelConnect()
		if err != nil {
			t.Fatalf("dial connection for failed write: %v", err)
		}
		_ = failedConn.CloseNow()
		_, replayed, writeErr := orchestrator.replayBufferedMessages(
			context.Background(),
			failedConn,
			nil,
			webSocketRelayOptions{
				GatewayCapture: gateway,
				Capture:        first,
				CaptureMode:    capturebridge.ModePayload,
			},
		)
		if !replayed || writeErr == nil {
			t.Fatalf("closed-connection replay = attempted:%t error:%v, want attempted failure", replayed, writeErr)
		}
		if buffer.Snapshot().Messages[0].Delivered {
			t.Fatal("failed physical write marked the message delivered")
		}
		_, failure := capturefailure.WebSocketReplayWrite(nil, writeErr)
		first.Finish(requestcapture.Outcome{
			SourceCompletion:  requestcapture.SourceCompletionPartial,
			TerminationReason: requestcapture.TerminationReasonWriteError,
			Failure:           failure,
		})

		second := beginWebSocketCaptureTestRecord(gateway, "second", requestcapture.SelectionModeReplacement)
		if err := deliverBufferedMessages(t, orchestrator, context.Background(), server, webSocketRelayOptions{
			GatewayCapture: gateway,
			Capture:        second,
			CaptureMode:    capturebridge.ModePayload,
		}); err != nil {
			t.Fatalf("retry delivery error = %v", err)
		}
		second.Finish(requestcapture.Outcome{
			SourceCompletion:  requestcapture.SourceCompletionComplete,
			TerminationReason: requestcapture.TerminationReasonWebSocketClose,
		})
		gateway.Finish(requestcapture.GatewayOutcome{})

		firstDetail := getWebSocketCaptureTestDetail(t, manager, session, first.ID())
		assertCapturedMessage(
			t,
			firstDetail.WebSocket.Messages,
			requestcapture.MessageSourceLive,
			requestcapture.MessageDispositionWriteFailed,
			"",
		)
		stableMessageID := firstDetail.WebSocket.Messages[0].MessageID
		if stableMessageID == "" {
			t.Fatal("failed live message ID is empty")
		}
		if !firstDetail.WebSocket.Messages[0].HasFailure ||
			firstDetail.WebSocket.Messages[0].Failure.Primary.Code != requestcapture.FailureCodeMessageWrite ||
			firstDetail.WebSocket.Messages[0].Failure.Primary.Peer != requestcapture.FailurePeerUpstream {
			t.Fatalf("failed message observation = %#v", firstDetail.WebSocket.Messages[0])
		}
		if !firstDetail.Summary.HasFailure ||
			firstDetail.Summary.Failure.Primary.Code != requestcapture.FailureCodeReplayWrite {
			t.Fatalf("failed replay summary = %#v", firstDetail.Summary)
		}
		secondDetail := getWebSocketCaptureTestDetail(t, manager, session, second.ID())
		assertCapturedMessage(
			t,
			secondDetail.WebSocket.Messages,
			requestcapture.MessageSourceLive,
			requestcapture.MessageDispositionForwarded,
			"",
		)
		if secondDetail.WebSocket.Messages[0].MessageID != stableMessageID {
			t.Fatalf("successful live message ID = %q, want %q", secondDetail.WebSocket.Messages[0].MessageID, stableMessageID)
		}
		if firstDetail.WebSocket.Messages[0].Sequence >= secondDetail.WebSocket.Messages[0].Sequence {
			t.Fatalf(
				"gateway sequences = failed:%d successful:%d, want globally increasing",
				firstDetail.WebSocket.Messages[0].Sequence,
				secondDetail.WebSocket.Messages[0].Sequence,
			)
		}
		exportedFailure := exportWebSocketCaptureMetadata(t, manager, session, []string{first.ID()})
		exportedSuccess := exportWebSocketCaptureMetadata(t, manager, session, []string{second.ID()})
		if exportedFailure.WebSocket == nil || len(exportedFailure.WebSocket.Messages) != 1 ||
			exportedFailure.WebSocket.Messages[0].Disposition != requestcapture.MessageDispositionWriteFailed ||
			!exportedFailure.WebSocket.Messages[0].HasFailure ||
			exportedFailure.WebSocket.Messages[0].Failure.Primary.Code != requestcapture.FailureCodeMessageWrite {
			t.Fatalf("exported write failure = %#v", exportedFailure.WebSocket)
		}
		if exportedSuccess.WebSocket == nil || len(exportedSuccess.WebSocket.Messages) != 1 ||
			exportedSuccess.WebSocket.Messages[0].Source != requestcapture.MessageSourceLive ||
			exportedSuccess.WebSocket.Messages[0].MessageID != stableMessageID {
			t.Fatalf("exported successful retry = %#v", exportedSuccess.WebSocket)
		}
	})

	t.Run("selected success links the next provider replay", func(t *testing.T) {
		manager, session := startCaptureTestManager(t, []requestcapture.ProviderIdentity{
			{ID: "first", Name: "First"},
			{ID: "second", Name: "Second"},
		})
		defer manager.Close()
		gateway := manager.BeginGateway(requestcapture.GatewayStart{GatewayRequestID: "selected-failover"})
		sourceLineage := gateway.NewMessageID()
		if !sourceLineage.Valid() {
			t.Fatal("source lineage is invalid")
		}
		buffer := newPreVisibleClientMessageBuffer(preVisibleClientReplayBufferLimitBytes)
		buffer.RecordWithLineage(websocket.MessageBinary, []byte{1, 2, 3}, false, sourceLineage)
		orchestrator := &WebSocketSessionOrchestrator{replayBuffer: buffer}
		server := newRecordingWSServer(t, make(chan webSocketReplayMessage, 2))
		defer server.Close()

		first := beginWebSocketCaptureTestRecord(gateway, "first", requestcapture.SelectionModeInitial)
		if err := deliverBufferedMessages(t, orchestrator, context.Background(), server, webSocketRelayOptions{
			GatewayCapture: gateway,
			Capture:        first,
			CaptureMode:    capturebridge.ModePayload,
		}); err != nil {
			t.Fatalf("first delivery error = %v", err)
		}
		first.Finish(requestcapture.Outcome{
			SourceCompletion:  requestcapture.SourceCompletionPartial,
			TerminationReason: requestcapture.TerminationReasonWebSocketRelayError,
		})

		second := beginWebSocketCaptureTestRecord(gateway, "second", requestcapture.SelectionModeFailover)
		if err := deliverBufferedMessages(t, orchestrator, context.Background(), server, webSocketRelayOptions{
			GatewayCapture: gateway,
			Capture:        second,
			CaptureMode:    capturebridge.ModePayload,
		}); err != nil {
			t.Fatalf("replay delivery error = %v", err)
		}
		second.Finish(requestcapture.Outcome{
			SourceCompletion:  requestcapture.SourceCompletionComplete,
			TerminationReason: requestcapture.TerminationReasonWebSocketClose,
		})
		gateway.Finish(requestcapture.GatewayOutcome{})

		firstDetail := getWebSocketCaptureTestDetail(t, manager, session, first.ID())
		assertCapturedMessage(
			t,
			firstDetail.WebSocket.Messages,
			requestcapture.MessageSourceLive,
			requestcapture.MessageDispositionForwarded,
			"",
		)
		secondDetail := getWebSocketCaptureTestDetail(t, manager, session, second.ID())
		assertCapturedMessage(
			t,
			secondDetail.WebSocket.Messages,
			requestcapture.MessageSourceReplay,
			requestcapture.MessageDispositionForwarded,
			firstDetail.WebSocket.Messages[0].MessageID,
		)
		if secondDetail.WebSocket.Messages[0].MessageID == firstDetail.WebSocket.Messages[0].MessageID {
			t.Fatal("replay reused the source message ID instead of allocating its own event ID")
		}
		if firstDetail.WebSocket.Messages[0].Sequence >= secondDetail.WebSocket.Messages[0].Sequence {
			t.Fatalf(
				"gateway sequences = live:%d replay:%d, want globally increasing",
				firstDetail.WebSocket.Messages[0].Sequence,
				secondDetail.WebSocket.Messages[0].Sequence,
			)
		}
	})
}

func TestWebSocketCaptureRecordsSuppressionBeforeClientVisibility(t *testing.T) {
	t.Parallel()

	manager, session := startCaptureTestManager(t, []requestcapture.ProviderIdentity{{
		ID:   "provider",
		Name: "Provider",
	}})
	defer manager.Close()
	gateway := manager.BeginGateway(requestcapture.GatewayStart{GatewayRequestID: "suppression"})
	recorder := beginWebSocketCaptureTestRecord(gateway, "provider", requestcapture.SelectionModeInitial)
	lifecycle := newWebSocketLifecycleState()
	lifecycle.MarkClientAccepted()
	payload := []byte(`{"type":"error","error":{"code":"model_not_allowed"}}`)
	forwarder := NewWebSocketForwarder(WebSocketForwarderConfig{Logger: zaptest.NewLogger(t)})

	progress := forwarder.relayPreVisibleUpstreamMessage(
		context.Background(),
		nil,
		nil,
		webSocketRelayOptions{
			GatewayCapture: gateway,
			Capture:        recorder,
			CaptureMode:    capturebridge.ModePayload,
			PreWriteToClient: func(webSocketPreWriteContext) webSocketPreWriteDecision {
				return webSocketPreWriteDecision{Action: webSocketPreWriteActionSuppress}
			},
		}.withCaptureHooks(),
		lifecycle,
		webSocketInitialReadResult{messageType: websocket.MessageText, data: payload},
		nil,
		func(websocket.MessageType, []byte) {
			t.Fatal("suppressed message became client-visible")
		},
		newWebSocketCommitState(),
		0,
	)
	if progress.Result == nil || progress.Result.Disposition != webSocketRelayDispositionSuppressedUpstreamError {
		t.Fatalf("relay progress = %#v", progress)
	}
	recorder.Finish(requestcapture.Outcome{
		SourceCompletion:  requestcapture.SourceCompletionPartial,
		TerminationReason: requestcapture.TerminationReasonWebSocketRelayError,
	})
	gateway.Finish(requestcapture.GatewayOutcome{})

	detail := getWebSocketCaptureTestDetail(t, manager, session, recorder.ID())
	assertCapturedMessage(
		t,
		detail.WebSocket.Messages,
		requestcapture.MessageSourceLive,
		requestcapture.MessageDispositionSuppressed,
		"",
	)
	if detail.WebSocket.Messages[0].ClientVisible {
		t.Fatal("suppressed upstream message was marked client-visible")
	}
}

func TestWebSocketCapturePersistsOnlyObservedCloseFrames(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name          string
		peer          webSocketPeer
		direction     requestcapture.MessageDirection
		code          websocket.StatusCode
		clean         bool
		terminalCause requestcapture.TerminationReason
	}{
		{
			name:          "upstream close",
			peer:          webSocketPeerUpstream,
			direction:     requestcapture.MessageDirectionUpstreamToClient,
			code:          websocket.StatusNormalClosure,
			clean:         true,
			terminalCause: requestcapture.TerminationReasonWebSocketClose,
		},
		{
			name:          "client close",
			peer:          webSocketPeerClient,
			direction:     requestcapture.MessageDirectionClientToUpstream,
			code:          websocket.StatusPolicyViolation,
			clean:         false,
			terminalCause: requestcapture.TerminationReasonClientDisconnect,
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			manager, session := startCaptureTestManager(t, []requestcapture.ProviderIdentity{{
				ID:   "provider",
				Name: "Provider",
			}})
			defer manager.Close()
			gateway := manager.BeginGateway(requestcapture.GatewayStart{GatewayRequestID: test.name})
			recorder := beginWebSocketCaptureTestRecord(gateway, "provider", requestcapture.SelectionModeInitial)
			closeErr := &websocket.CloseError{
				Code:   test.code,
				Reason: "closed for close-secret",
			}
			modelCause := modelTerminalCauseForCaptureClose(test.peer, test.clean)
			relay := &webSocketRelaySessionResult{
				TerminalCause:      modelCause,
				ObservedCloseError: closeErr,
				FailurePeer:        test.peer,
				FailureOperation:   webSocketRelayFailureOperationRead,
			}
			result := relay.toWebSocketResult()
			outcome := webSocketRelayCaptureOutcome(context.Background(), relay, result)
			if outcome.TerminationReason != test.terminalCause {
				t.Fatalf("termination = %q, want %q", outcome.TerminationReason, test.terminalCause)
			}
			var credentialEvidence requestcapture.CredentialEvidence
			credentialEvidence.Add("close-secret")
			credentialEvidence.Seal()
			finishWebSocketDialCapture(DialExchange{
				capture:            recorder,
				captureMode:        capturebridge.ModePayload,
				credentialEvidence: credentialEvidence,
			}, outcome)
			gateway.Finish(requestcapture.GatewayOutcome{})

			detail := getWebSocketCaptureTestDetail(t, manager, session, recorder.ID())
			if detail.WebSocket.Close == nil {
				t.Fatal("observed close frame was not persisted")
			}
			closeSnapshot := detail.WebSocket.Close
			if closeSnapshot.Direction != test.direction ||
				closeSnapshot.Code != int(test.code) ||
				closeSnapshot.Clean != test.clean {
				t.Fatalf("close snapshot = %#v", closeSnapshot)
			}
			if strings.Contains(closeSnapshot.Reason, "close-secret") {
				t.Fatalf("close reason leaked credential: %q", closeSnapshot.Reason)
			}
			if test.clean {
				if detail.Summary.HasFailure {
					t.Fatalf("clean close recorded failure = %#v", detail.Summary.Failure)
				}
				return
			}
			if !detail.Summary.HasFailure ||
				detail.Summary.Failure.Primary.Code != requestcapture.FailureCodeWebSocketClose ||
				detail.Summary.Failure.Primary.WebSocketCloseCode != int(test.code) ||
				detail.Summary.Failure.Primary.Peer != captureFailurePeer(test.peer) ||
				strings.Contains(detail.Summary.Failure.Primary.Message, "close-secret") ||
				!strings.Contains(detail.Summary.Failure.Primary.Message, "[REDACTED]") {
				t.Fatalf("abnormal close failure = present:%t observation:%#v", detail.Summary.HasFailure, detail.Summary.Failure)
			}
		})
	}

	synthesized := &webSocketRelaySessionResult{
		CloseCode:   websocket.StatusNormalClosure,
		FailurePeer: webSocketPeerUpstream,
	}
	if observed := webSocketCaptureCloseObservation(synthesized); observed != nil {
		t.Fatalf("synthesized close produced observation = %#v", observed)
	}
}

func TestWebSocketCaptureCleanCloseWinsContextRace(t *testing.T) {
	t.Parallel()

	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	deadline, deadlineCancel := context.WithDeadline(context.Background(), time.Unix(1, 0))
	defer deadlineCancel()

	tests := []struct {
		name          string
		ctx           context.Context
		terminalCause model.TerminalCause
		observedClose bool
	}{
		{name: "observed close then cancel", ctx: canceled, terminalCause: model.TerminalInternalError, observedClose: true},
		{name: "observed close then deadline", ctx: deadline, terminalCause: model.TerminalInternalError, observedClose: true},
		{name: "reduced clean close then cancel", ctx: canceled, terminalCause: model.TerminalCleanClose},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var relay *webSocketRelaySessionResult
			if test.observedClose {
				relay = &webSocketRelaySessionResult{
					ObservedCloseError: &websocket.CloseError{Code: websocket.StatusNormalClosure, Reason: "done"},
					FailurePeer:        webSocketPeerUpstream,
					FailureOperation:   webSocketRelayFailureOperationRead,
				}
			}
			outcome := webSocketRelayCaptureOutcome(test.ctx, relay, &WebSocketResult{
				Err:           test.ctx.Err(),
				TerminalCause: test.terminalCause,
			})
			if outcome.SourceCompletion != requestcapture.SourceCompletionComplete ||
				outcome.TerminationReason != requestcapture.TerminationReasonWebSocketClose ||
				outcome.Failure.Primary.Code != "" {
				t.Fatalf("clean-close race outcome = %#v", outcome)
			}
		})
	}
}

func TestWebSocketCaptureDropsOpaqueFailureMessageWithoutSealedCredentialEvidence(t *testing.T) {
	t.Parallel()

	manager, session := startCaptureTestManager(t, []requestcapture.ProviderIdentity{{
		ID:   "provider",
		Name: "Provider",
	}})
	defer manager.Close()
	gateway := manager.BeginGateway(requestcapture.GatewayStart{GatewayRequestID: "missing-evidence"})
	recorder := beginWebSocketCaptureTestRecord(gateway, "provider", requestcapture.SelectionModeInitial)
	fact, truncated := capturefailure.ProviderSemantic(
		requestcapture.FailureSiteWebSocketMessage,
		requestcapture.FailurePeerProvider,
		http.StatusBadRequest,
		"opaque-provider-type",
		"opaque-provider-code",
		"opaque-provider-diagnostic",
	)
	failure := capturefailure.Observation(fact, requestcapture.FailureFact{})
	failure.Truncated = truncated
	recorder.Finish(requestcapture.Outcome{
		SourceCompletion:  requestcapture.SourceCompletionPartial,
		TerminationReason: requestcapture.TerminationReasonWebSocketRelayError,
		Failure:           failure,
		// Zero evidence intentionally means the credential surface was not
		// inspected. Diagnostic text must therefore fail closed.
		CredentialEvidence: requestcapture.CredentialEvidence{},
	})
	gateway.Finish(requestcapture.GatewayOutcome{})

	detail := getWebSocketCaptureTestDetail(t, manager, session, recorder.ID())
	if !detail.Summary.HasFailure ||
		detail.Summary.Failure.Primary.ProviderErrorType != "" ||
		detail.Summary.Failure.Primary.ProviderErrorCode != "" ||
		detail.Summary.Failure.Primary.Message != "" ||
		!detail.Summary.Failure.Truncated {
		t.Fatalf("query failure = present:%t observation:%#v", detail.Summary.HasFailure, detail.Summary.Failure)
	}
	exported := exportWebSocketCaptureMetadata(t, manager, session, []string{recorder.ID()})
	if !exported.Summary.HasFailure ||
		exported.Summary.Failure.Primary.ProviderErrorType != "" ||
		exported.Summary.Failure.Primary.ProviderErrorCode != "" ||
		exported.Summary.Failure.Primary.Message != "" ||
		!exported.Summary.Failure.Truncated {
		t.Fatalf("export failure = present:%t observation:%#v", exported.Summary.HasFailure, exported.Summary.Failure)
	}
}

func TestWebSocketCaptureRetainsRedactedProviderSemanticIdentity(t *testing.T) {
	t.Parallel()

	const credential = "provider-semantic-secret"
	manager, session := startCaptureTestManager(t, []requestcapture.ProviderIdentity{{
		ID:   "provider",
		Name: "Provider",
	}})
	defer manager.Close()
	gateway := manager.BeginGateway(requestcapture.GatewayStart{GatewayRequestID: "provider-semantic"})
	recorder := beginWebSocketCaptureTestRecord(gateway, "provider", requestcapture.SelectionModeInitial)
	result := &WebSocketResult{UpstreamError: &WebSocketUpstreamError{
		ProviderErrorType: "type-" + credential,
		Code:              "code-" + credential,
		StatusCode:        http.StatusTooManyRequests,
		Message:           "message-" + credential,
	}}
	outcome := webSocketRelayCaptureOutcome(context.Background(), nil, result)
	var credentialEvidence requestcapture.CredentialEvidence
	credentialEvidence.Add(credential)
	credentialEvidence.Seal()
	finishWebSocketDialCapture(DialExchange{
		capture:            recorder,
		captureMode:        capturebridge.ModePayload,
		credentialEvidence: credentialEvidence,
	}, outcome)
	gateway.Finish(requestcapture.GatewayOutcome{})

	assertFailure := func(t *testing.T, failure requestcapture.FailureObservation, present bool) {
		t.Helper()
		fact := failure.Primary
		if !present || fact.Code != requestcapture.FailureCodeProviderSemantic ||
			fact.HTTPStatusCode != http.StatusTooManyRequests ||
			!strings.Contains(fact.ProviderErrorType, "[REDACTED]") ||
			!strings.Contains(fact.ProviderErrorCode, "[REDACTED]") ||
			!strings.Contains(fact.Message, "[REDACTED]") ||
			strings.Contains(fact.ProviderErrorType, credential) ||
			strings.Contains(fact.ProviderErrorCode, credential) ||
			strings.Contains(fact.Message, credential) {
			t.Fatalf("provider semantic failure = present:%t observation:%#v", present, failure)
		}
	}
	detail := getWebSocketCaptureTestDetail(t, manager, session, recorder.ID())
	assertFailure(t, detail.Summary.Failure, detail.Summary.HasFailure)
	exported := exportWebSocketCaptureMetadata(t, manager, session, []string{recorder.ID()})
	assertFailure(t, exported.Summary.Failure, exported.Summary.HasFailure)
}

const (
	webSocketCaptureExportManifestEvent = "manifest"
	webSocketCaptureExportRecordEvent   = "record"
	webSocketCaptureExportMetadataPart  = "metadata_chunk"
	webSocketCaptureHandshakeBodyBlobID = "handshake_body"
)

type webSocketCaptureExportEnvelope struct {
	Event       string `json:"event"`
	Part        string `json:"part"`
	RecordIndex int    `json:"record_index"`
	DataBase64  []byte `json:"data_base64"`
}

type webSocketCaptureExportMetadata struct {
	RecordID          string                             `json:"record_id"`
	Summary           requestcapture.RecordSummary       `json:"summary"`
	GatewayTraceIndex int                                `json:"gateway_trace_index"`
	GatewayTrace      requestcapture.GatewayTraceSummary `json:"-"`
	Request           requestcapture.RequestSnapshot     `json:"request"`
	WebSocket         *struct {
		Handshake *requestcapture.WebSocketHandshakeSnapshot `json:"handshake"`
		Messages  []requestcapture.MessageSnapshot           `json:"messages"`
		Close     *requestcapture.WebSocketCloseSnapshot     `json:"close"`
	} `json:"websocket"`
	Blobs []webSocketCaptureExportBlob `json:"blobs"`
}

type webSocketCaptureExportManifest struct {
	GatewayTraces []struct {
		TraceIndex int                                `json:"trace_index"`
		Trace      requestcapture.GatewayTraceSummary `json:"trace"`
	} `json:"gateway_traces"`
}

type webSocketCaptureExportBlob struct {
	BlobID  string `json:"blob_id"`
	RawSize int64  `json:"raw_size"`
}

func waitForCompletedWebSocketCaptureRecord(
	t *testing.T,
	manager *requestcapture.Manager,
	session requestcapture.SessionInfo,
	providerID string,
) requestcapture.RecordSummary {
	t.Helper()
	var record requestcapture.RecordSummary
	waitFor(t, func() bool {
		page, err := readCaptureTestPage(manager, session, requestcapture.ListQuery{Limit: 10})
		if err != nil {
			return false
		}
		found := findWebSocketCaptureRecord(page.Records, providerID)
		if found == nil || found.LifecycleState != requestcapture.LifecycleStateCompleted {
			return false
		}
		record = *found
		return true
	}, testPollTimeout)
	return record
}

func findWebSocketCaptureRecord(
	records []requestcapture.RecordSummary,
	providerID string,
) *requestcapture.RecordSummary {
	for index := range records {
		if records[index].Protocol == requestcapture.ProtocolWebSocket && records[index].Provider.ID == providerID {
			return &records[index]
		}
	}
	return nil
}

func findCapturedWebSocketMessage(
	messages []requestcapture.MessageSnapshot,
	direction requestcapture.MessageDirection,
) *requestcapture.MessageSnapshot {
	for index := range messages {
		if messages[index].Direction == direction {
			return &messages[index]
		}
	}
	return nil
}

func exportWebSocketCaptureMetadata(
	t *testing.T,
	manager *requestcapture.Manager,
	session requestcapture.SessionInfo,
	recordIDs []string,
) webSocketCaptureExportMetadata {
	t.Helper()
	ticket, err := manager.CreateExport(context.Background(), session.SessionID, requestcapture.ExportRequest{
		Scope:     requestcapture.ExportScopeRecords,
		RecordIDs: recordIDs,
	})
	if err != nil {
		t.Fatalf("CreateExport() error = %v", err)
	}
	download, err := manager.AcceptDownload(ticket.ExportID, ticket.DownloadToken)
	if err != nil {
		t.Fatalf("AcceptDownload() error = %v", err)
	}
	var destination bytes.Buffer
	if err := download.WriteTo(context.Background(), &destination); err != nil {
		t.Fatalf("export WriteTo() error = %v", err)
	}

	var manifestBytes, metadataBytes []byte
	scanner := bufio.NewScanner(bytes.NewReader(destination.Bytes()))
	scanner.Buffer(nil, requestcapture.DefaultExportLineBytes)
	for scanner.Scan() {
		var envelope webSocketCaptureExportEnvelope
		if err := json.Unmarshal(scanner.Bytes(), &envelope); err != nil {
			t.Fatalf("decode export line: %v", err)
		}
		if envelope.Part != webSocketCaptureExportMetadataPart {
			continue
		}
		switch {
		case envelope.Event == webSocketCaptureExportManifestEvent:
			manifestBytes = append(manifestBytes, envelope.DataBase64...)
		case envelope.Event == webSocketCaptureExportRecordEvent && envelope.RecordIndex == 0:
			metadataBytes = append(metadataBytes, envelope.DataBase64...)
		}
	}
	if err := scanner.Err(); err != nil {
		t.Fatalf("scan export: %v", err)
	}
	if len(metadataBytes) == 0 {
		t.Fatal("export did not contain websocket record metadata")
	}
	if len(manifestBytes) == 0 {
		t.Fatal("export did not contain manifest metadata")
	}
	var metadata webSocketCaptureExportMetadata
	if err := json.Unmarshal(metadataBytes, &metadata); err != nil {
		t.Fatalf("decode websocket export metadata: %v", err)
	}
	var manifest webSocketCaptureExportManifest
	if err := json.Unmarshal(manifestBytes, &manifest); err != nil {
		t.Fatalf("decode websocket export manifest: %v", err)
	}
	if metadata.GatewayTraceIndex < 0 || metadata.GatewayTraceIndex >= len(manifest.GatewayTraces) {
		t.Fatalf(
			"gateway_trace_index = %d, trace count %d",
			metadata.GatewayTraceIndex,
			len(manifest.GatewayTraces),
		)
	}
	trace := manifest.GatewayTraces[metadata.GatewayTraceIndex]
	if trace.TraceIndex != metadata.GatewayTraceIndex ||
		trace.Trace.GatewayTraceID != metadata.Summary.GatewayTraceID {
		t.Fatalf("gateway trace reference = %#v, summary trace ID %q", trace, metadata.Summary.GatewayTraceID)
	}
	metadata.GatewayTrace = trace.Trace
	return metadata
}

type webSocketCaptureFailingAuthenticator struct {
	providerID string
	secret     string
}

func (a webSocketCaptureFailingAuthenticator) ApplyProviderCredentials(
	_ context.Context,
	headers http.Header,
	provider *model.Provider,
	_, _ string,
	_ *http.Request,
) error {
	if provider.ID == a.providerID {
		headers.Set("Authorization", "Bearer "+a.secret)
		return errors.New("credential preparation failed for " + a.secret)
	}
	headers.Set("Authorization", "Bearer fallback-token")
	return nil
}

func (webSocketCaptureFailingAuthenticator) RefreshProviderCredentials(
	context.Context,
	*model.Provider,
) (bool, error) {
	return false, nil
}

func captureWebSocketTestAttempt(
	providerID string,
	phase requestcapture.CredentialPhase,
) requestcapture.AttemptMetadata {
	return requestcapture.AttemptMetadata{
		Provider: requestcapture.ProviderIdentity{
			ID:   providerID,
			Name: providerID,
		},
		APIType:              APITypeCodex,
		SelectionMode:        requestcapture.SelectionModeInitial,
		SelectionSource:      requestcapture.SelectionSourceStrategy,
		ProviderAttemptIndex: webSocketCaptureProviderAttemptIndex,
		CredentialPhase:      phase,
	}
}

func beginWebSocketCaptureTestRecord(
	gateway requestcapture.GatewayRecorder,
	providerID string,
	mode requestcapture.SelectionMode,
) requestcapture.Recorder {
	attempt := captureWebSocketTestAttempt(providerID, requestcapture.CredentialPhaseInitial)
	attempt.SelectionMode = mode
	var sensitiveHeaders requestcapture.SensitiveHeaderEvidence
	sensitiveHeaders.Seal()
	var credentialEvidence requestcapture.CredentialEvidence
	credentialEvidence.Seal()
	recorder := gateway.BeginWebSocket(requestcapture.RawWebSocketStart{
		Attempt:   attempt,
		TargetURL: "wss://upstream.example/responses",
		Request: requestcapture.RawRequest{
			Method:             http.MethodGet,
			SensitiveHeaders:   sensitiveHeaders,
			CredentialEvidence: credentialEvidence,
		},
	})
	recorder.ObserveWebSocketHandshake(requestcapture.WebSocketHandshake{
		StatusCode:         http.StatusSwitchingProtocols,
		Protocol:           "HTTP/1.1",
		SensitiveHeaders:   sensitiveHeaders,
		CredentialEvidence: credentialEvidence,
	})
	return recorder
}

func deliverBufferedMessages(
	t *testing.T,
	orchestrator *WebSocketSessionOrchestrator,
	writeCtx context.Context,
	server *httptest.Server,
	options webSocketRelayOptions,
) error {
	t.Helper()
	connectCtx, cancelConnect := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancelConnect()
	conn, _, err := websocket.Dial(connectCtx, wsURL(server), nil)
	if err != nil {
		t.Fatalf("dial recording websocket: %v", err)
	}
	defer conn.CloseNow()

	_, replayed, err := orchestrator.replayBufferedMessages(writeCtx, conn, nil, options)
	if !replayed {
		t.Fatal("buffered delivery did not report a replay attempt")
	}
	return err
}

func getWebSocketCaptureTestDetail(
	t *testing.T,
	manager *requestcapture.Manager,
	session requestcapture.SessionInfo,
	recordID string,
) requestcapture.RecordDetail {
	t.Helper()
	detail, err := readCaptureTestDetail(manager, session, recordID, 1024)
	if err != nil {
		t.Fatalf("read record detail: %v", err)
	}
	if detail.WebSocket == nil {
		t.Fatal("WebSocket detail is missing")
	}
	return detail
}

func readCaptureTestPage(
	manager *requestcapture.Manager,
	session requestcapture.SessionInfo,
	query requestcapture.ListQuery,
) (requestcapture.RecordPage, error) {
	lease, err := manager.OpenRecordPage(context.Background(), session.SessionID, query)
	if err != nil {
		return requestcapture.RecordPage{}, err
	}
	defer lease.Close()

	var encoded bytes.Buffer
	if err := lease.WriteJSON(context.Background(), &encoded); err != nil {
		return requestcapture.RecordPage{}, err
	}
	var page requestcapture.RecordPage
	if err := json.Unmarshal(encoded.Bytes(), &page); err != nil {
		return requestcapture.RecordPage{}, err
	}
	return page, nil
}

func readCaptureTestDetail(
	manager *requestcapture.Manager,
	session requestcapture.SessionInfo,
	recordID string,
	previewBytes int,
) (requestcapture.RecordDetail, error) {
	lease, err := manager.OpenRecordDetail(context.Background(), session.SessionID, recordID, previewBytes)
	if err != nil {
		return requestcapture.RecordDetail{}, err
	}
	defer lease.Close()

	var encoded bytes.Buffer
	if err := lease.WriteJSON(context.Background(), &encoded); err != nil {
		return requestcapture.RecordDetail{}, err
	}
	var detail requestcapture.RecordDetail
	if err := json.Unmarshal(encoded.Bytes(), &detail); err != nil {
		return requestcapture.RecordDetail{}, err
	}
	return detail, nil
}

func assertCapturedMessage(
	t *testing.T,
	messages []requestcapture.MessageSnapshot,
	source requestcapture.MessageSource,
	disposition requestcapture.MessageDisposition,
	sourceMessageID string,
) {
	t.Helper()
	if len(messages) != 1 {
		t.Fatalf("messages = %d, want 1: %#v", len(messages), messages)
	}
	message := messages[0]
	if message.Source != source ||
		message.Disposition != disposition ||
		message.SourceMessageID != sourceMessageID {
		t.Fatalf("message = %#v", message)
	}
}

func modelTerminalCauseForCaptureClose(peer webSocketPeer, clean bool) model.TerminalCause {
	if peer == webSocketPeerClient {
		return model.TerminalClientDisconnect
	}
	if clean {
		return model.TerminalCleanClose
	}
	return model.TerminalUpstreamTransportError
}
