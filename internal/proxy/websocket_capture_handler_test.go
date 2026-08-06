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
	"github.com/doraemonkeys/switch-a/internal/requestcapture"

	"github.com/coder/websocket"
	"go.uber.org/zap/zaptest"
)

const (
	webSocketCaptureExportManifestEvent  = "manifest"
	webSocketCaptureExportRecordEvent    = "record"
	webSocketCaptureExportMetadataPart   = "metadata_chunk"
	webSocketCaptureHandshakeBodyBlobID  = "handshake_body"
	webSocketCaptureProviderAttemptIndex = 0
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

type webSocketCaptureFailingAuthenticator struct {
	providerID string
	secret     string
}

func (auth webSocketCaptureFailingAuthenticator) ApplyProviderCredentials(
	_ context.Context,
	headers http.Header,
	provider *model.Provider,
	_, _ string,
	_ *http.Request,
) error {
	if provider.ID == auth.providerID {
		headers.Set("Authorization", "Bearer "+auth.secret)
		return errors.New("credential preparation failed for " + auth.secret)
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

func TestHandlerWebSocketCaptureEndToEndLiveCloseAndExport(t *testing.T) {
	const (
		providerID    = "capture-live-provider"
		clientFrame   = `{"type":"response.create","response":{"instructions":"capture me"}}`
		upstreamFrame = `{"type":"response.created","response":{"id":"capture-response"}}`
		closeReason   = "upstream complete"
	)

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			t.Errorf("accept upstream websocket: %v", err)
			return
		}
		defer conn.CloseNow()

		messageType, payload, err := conn.Read(r.Context())
		if err != nil {
			t.Errorf("read client frame: %v", err)
			return
		}
		if messageType != websocket.MessageText || string(payload) != clientFrame {
			t.Errorf("client frame = (%v, %q), want text/%q", messageType, payload, clientFrame)
			return
		}
		if err := conn.Write(r.Context(), websocket.MessageText, []byte(upstreamFrame)); err != nil {
			t.Errorf("write upstream frame: %v", err)
			return
		}
		_ = conn.Close(websocket.StatusNormalClosure, closeReason)
	}))
	defer upstream.Close()

	provider := model.Provider{
		ID:       providerID,
		Name:     "Capture Live Provider",
		APIKey:   "capture-live-secret",
		AuthMode: AuthModeBearer,
		Enabled:  true,
		APITypes: []model.ProviderAPIType{{
			ProviderID: providerID,
			APIType:    APITypeCodex,
			BaseURL:    upstream.URL,
		}},
	}
	manager, session := startCaptureTestManager(t, []requestcapture.ProviderIdentity{{
		ID:   provider.ID,
		Name: provider.Name,
	}})
	defer manager.Close()
	store := newMockStore()
	store.providers = []model.Provider{provider}
	handler := NewHandler(Config{
		Store:   store,
		Capture: manager,
		Logger:  zaptest.NewLogger(t),
	})
	proxyServer := httptest.NewServer(handler)
	defer proxyServer.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn, _, err := websocket.Dial(ctx, wsURL(proxyServer)+"/responses?model=capture-model", nil)
	if err != nil {
		t.Fatalf("dial websocket through proxy: %v", err)
	}
	if err := conn.Write(ctx, websocket.MessageText, []byte(clientFrame)); err != nil {
		t.Fatalf("write client frame: %v", err)
	}
	messageType, payload, err := conn.Read(ctx)
	if err != nil {
		t.Fatalf("read upstream frame: %v", err)
	}
	if messageType != websocket.MessageText || string(payload) != upstreamFrame {
		t.Fatalf("upstream frame = (%v, %q), want text/%q", messageType, payload, upstreamFrame)
	}
	if _, _, err := conn.Read(ctx); err == nil {
		t.Fatal("terminal upstream close unexpectedly produced another application frame")
	}
	_ = conn.CloseNow()

	record := waitForCompletedWebSocketCaptureRecord(t, manager, session, providerID)
	detail := getWebSocketCaptureTestDetail(t, manager, session, record.RecordID)
	if detail.WebSocket.Handshake == nil || detail.WebSocket.Handshake.StatusCode != http.StatusSwitchingProtocols {
		t.Fatalf("handshake = %#v, want 101", detail.WebSocket.Handshake)
	}
	if len(detail.WebSocket.Messages) != 2 {
		t.Fatalf("captured messages = %#v, want two physical frames", detail.WebSocket.Messages)
	}
	clientMessage := findCapturedWebSocketMessage(detail.WebSocket.Messages, requestcapture.MessageDirectionClientToUpstream)
	if clientMessage == nil || clientMessage.Source != requestcapture.MessageSourceLive ||
		clientMessage.Disposition != requestcapture.MessageDispositionForwarded || clientMessage.ClientVisible {
		t.Fatalf("client message = %#v", clientMessage)
	}
	upstreamMessage := findCapturedWebSocketMessage(detail.WebSocket.Messages, requestcapture.MessageDirectionUpstreamToClient)
	if upstreamMessage == nil || upstreamMessage.Source != requestcapture.MessageSourceLive ||
		upstreamMessage.Disposition != requestcapture.MessageDispositionForwarded || !upstreamMessage.ClientVisible {
		t.Fatalf("upstream message = %#v", upstreamMessage)
	}
	if detail.WebSocket.Close == nil ||
		detail.WebSocket.Close.Direction != requestcapture.MessageDirectionUpstreamToClient ||
		detail.WebSocket.Close.Code != int(websocket.StatusNormalClosure) ||
		detail.WebSocket.Close.Reason != closeReason || !detail.WebSocket.Close.Clean {
		t.Fatalf("close = %#v", detail.WebSocket.Close)
	}
	if detail.Summary.SourceCompletion != requestcapture.SourceCompletionComplete ||
		detail.Summary.TerminationReason != requestcapture.TerminationReasonWebSocketClose {
		t.Fatalf("summary = %#v", detail.Summary)
	}

	exported := exportWebSocketCaptureMetadata(t, manager, session, []string{record.RecordID})
	if exported.RecordID != record.RecordID || exported.WebSocket == nil ||
		exported.WebSocket.Handshake == nil || exported.WebSocket.Handshake.StatusCode != http.StatusSwitchingProtocols ||
		len(exported.WebSocket.Messages) != 2 || exported.WebSocket.Close == nil ||
		exported.WebSocket.Close.Reason != closeReason {
		t.Fatalf("exported websocket metadata = %#v", exported)
	}
	if authorization := exported.Request.Headers["Authorization"]; len(authorization) != 1 || authorization[0] != "Bearer [REDACTED]" {
		t.Fatalf("exported Authorization = %#v, want redacted", authorization)
	}
}

func TestHandlerWebSocketCaptureEndToEndUnselectedSuppressionAndSelectedReplayLineage(t *testing.T) {
	const (
		primaryID    = "capture-primary"
		fallbackID   = "capture-fallback"
		clientFrame  = `{"type":"response.create","response":{"model":"capture-model"}}`
		semanticErr  = `{"type":"error","status":403,"error":{"type":"auth_error","code":"model_not_allowed","message":"model access denied"}}`
		successFrame = `{"type":"response.created","response":{"model":"capture-model"}}`
	)

	primary := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			t.Errorf("accept primary websocket: %v", err)
			return
		}
		defer conn.CloseNow()
		_, payload, err := conn.Read(r.Context())
		if err != nil {
			t.Errorf("read primary client frame: %v", err)
			return
		}
		if string(payload) != clientFrame {
			t.Errorf("primary client frame = %q, want %q", payload, clientFrame)
			return
		}
		if err := conn.Write(r.Context(), websocket.MessageText, []byte(semanticErr)); err != nil {
			t.Errorf("write primary semantic error: %v", err)
		}
	}))
	defer primary.Close()

	fallback := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			t.Errorf("accept fallback websocket: %v", err)
			return
		}
		defer conn.CloseNow()
		_, payload, err := conn.Read(r.Context())
		if err != nil {
			t.Errorf("read replayed client frame: %v", err)
			return
		}
		if string(payload) != clientFrame {
			t.Errorf("replayed client frame = %q, want %q", payload, clientFrame)
			return
		}
		if err := conn.Write(r.Context(), websocket.MessageText, []byte(successFrame)); err != nil {
			t.Errorf("write fallback success: %v", err)
			return
		}
		_ = conn.Close(websocket.StatusNormalClosure, "fallback complete")
	}))
	defer fallback.Close()

	primaryProvider := &model.Provider{
		ID:       primaryID,
		Name:     "Capture Primary",
		APIKey:   "primary-secret",
		AuthMode: AuthModeBearer,
		Enabled:  true,
		APITypes: []model.ProviderAPIType{{ProviderID: primaryID, APIType: APITypeCodex, BaseURL: primary.URL}},
	}
	fallbackProvider := &model.Provider{
		ID:       fallbackID,
		Name:     "Capture Fallback",
		APIKey:   "fallback-secret",
		AuthMode: AuthModeBearer,
		Enabled:  true,
		APITypes: []model.ProviderAPIType{{ProviderID: fallbackID, APIType: APITypeCodex, BaseURL: fallback.URL}},
	}
	// Only the replacement is selected for payload retention. The primary still
	// participates as a transition so its live client read can publish lineage
	// without retaining either physical frame.
	manager, session := startCaptureTestManager(t, []requestcapture.ProviderIdentity{{
		ID: fallbackID, Name: fallbackProvider.Name,
	}})
	defer manager.Close()
	store := newMockStore()
	store.providers = []model.Provider{*primaryProvider, *fallbackProvider}
	selector := &mockSelector{
		selectWithMetadataFunc: func(context.Context, *model.SelectRequest) (*selectResult, error) {
			return &selectResult{Provider: primaryProvider}, nil
		},
		selectExcludingFunc: func(context.Context, *model.SelectRequest, map[string]bool) (*model.Provider, error) {
			return fallbackProvider, nil
		},
	}
	handler := NewHandler(Config{
		Store:    store,
		Selector: selector,
		Capture:  manager,
		Logger:   zaptest.NewLogger(t),
	})
	proxyServer := httptest.NewServer(handler)
	defer proxyServer.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn, _, err := websocket.Dial(ctx, wsURL(proxyServer)+"/responses?model=capture-model", nil)
	if err != nil {
		t.Fatalf("dial websocket through proxy: %v", err)
	}
	if err := conn.Write(ctx, websocket.MessageText, []byte(clientFrame)); err != nil {
		t.Fatalf("write client frame: %v", err)
	}
	_, payload, err := conn.Read(ctx)
	if err != nil {
		t.Fatalf("read fallback success: %v", err)
	}
	if string(payload) != successFrame {
		t.Fatalf("client-visible frame = %q, want only fallback success %q", payload, successFrame)
	}
	_, _, _ = conn.Read(ctx)
	_ = conn.CloseNow()

	var page requestcapture.RecordPage
	waitFor(t, func() bool {
		var listErr error
		page, listErr = readCaptureTestPage(manager, session, requestcapture.ListQuery{Limit: 10})
		if listErr != nil || len(page.Records) != 1 || len(page.GatewayTraces) != 1 || len(page.GatewayTraces[0].Entries) != 2 {
			return false
		}
		return page.Records[0].LifecycleState == requestcapture.LifecycleStateCompleted
	}, testPollTimeout)
	fallbackRecord := findWebSocketCaptureRecord(page.Records, fallbackID)
	if fallbackRecord == nil {
		t.Fatalf("records = %#v, want only selected fallback", page.Records)
	}
	primaryTransition := page.GatewayTraces[0].Entries[0]
	if primaryTransition.Kind != requestcapture.TraceEntryTransition ||
		primaryTransition.Provider.ID != primaryID ||
		primaryTransition.TerminationReason != requestcapture.TerminationReasonWebSocketRelayError ||
		!primaryTransition.HasFailure {
		t.Fatalf("unselected primary transition = %#v", primaryTransition)
	}
	fallbackDetail := getWebSocketCaptureTestDetail(t, manager, session, fallbackRecord.RecordID)
	fallbackClient := findCapturedWebSocketMessage(fallbackDetail.WebSocket.Messages, requestcapture.MessageDirectionClientToUpstream)
	fallbackSuccess := findCapturedWebSocketMessage(fallbackDetail.WebSocket.Messages, requestcapture.MessageDirectionUpstreamToClient)
	expectedPrimaryLiveID := firstWebSocketCaptureMessageID(page.GatewayTraces[0].GatewayTraceID)
	if fallbackClient == nil || fallbackClient.Source != requestcapture.MessageSourceReplay ||
		fallbackClient.SourceMessageID != expectedPrimaryLiveID ||
		fallbackClient.SourceMessageID == fallbackClient.MessageID ||
		fallbackClient.Disposition != requestcapture.MessageDispositionForwarded {
		t.Fatalf(
			"fallback replay = %#v, want primary live lineage %q",
			fallbackClient,
			expectedPrimaryLiveID,
		)
	}
	if fallbackSuccess == nil || fallbackSuccess.Disposition != requestcapture.MessageDispositionForwarded || !fallbackSuccess.ClientVisible {
		t.Fatalf("fallback success = %#v", fallbackSuccess)
	}
	if !(fallbackClient.Sequence < fallbackSuccess.Sequence) {
		t.Fatalf(
			"gateway message sequences = fallback replay:%d visible:%d",
			fallbackClient.Sequence,
			fallbackSuccess.Sequence,
		)
	}
	if len(page.GatewayTraces) != 1 || len(page.GatewayTraces[0].Entries) != 2 ||
		page.GatewayTraces[0].Entries[0].Provider.ID != primaryID ||
		page.GatewayTraces[0].Entries[1].Provider.ID != fallbackID ||
		page.GatewayTraces[0].Entries[1].SelectionMode != requestcapture.SelectionModeReplacement {
		t.Fatalf("provider trace = %#v", page.GatewayTraces)
	}

	exportedFallback := exportWebSocketCaptureMetadata(t, manager, session, []string{fallbackRecord.RecordID})
	if exportedFallback.WebSocket == nil {
		t.Fatal("exported fallback websocket metadata is missing")
	}
	exportedFallbackReplay := findCapturedWebSocketMessage(
		exportedFallback.WebSocket.Messages,
		requestcapture.MessageDirectionClientToUpstream,
	)
	exportedFallbackVisible := findCapturedWebSocketMessage(
		exportedFallback.WebSocket.Messages,
		requestcapture.MessageDirectionUpstreamToClient,
	)
	if exportedFallbackReplay == nil || exportedFallbackReplay.Source != requestcapture.MessageSourceReplay ||
		exportedFallbackReplay.SourceMessageID != fallbackClient.SourceMessageID ||
		exportedFallbackVisible == nil || !exportedFallbackVisible.ClientVisible {
		t.Fatalf(
			"exported fallback replay/visible = replay:%#v visible:%#v",
			exportedFallbackReplay,
			exportedFallbackVisible,
		)
	}
}

func firstWebSocketCaptureMessageID(gatewayTraceID string) string {
	const (
		gatewayTracePrefix = "gt_"
		messagePrefix      = "wm_"
	)
	if !strings.HasPrefix(gatewayTraceID, gatewayTracePrefix) {
		return ""
	}
	// This E2E sends exactly one live client frame before replacement, so the
	// gateway's first admitted lineage is the physical primary delivery.
	return messagePrefix + strings.TrimPrefix(gatewayTraceID, gatewayTracePrefix) + "_1"
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

func findWebSocketCaptureRecord(records []requestcapture.RecordSummary, providerID string) *requestcapture.RecordSummary {
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
		Scope: requestcapture.ExportScopeRecords, RecordIDs: recordIDs,
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
	if len(metadataBytes) == 0 || len(manifestBytes) == 0 {
		t.Fatal("export did not contain websocket record and manifest metadata")
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
		t.Fatalf("gateway_trace_index = %d, trace count %d", metadata.GatewayTraceIndex, len(manifest.GatewayTraces))
	}
	trace := manifest.GatewayTraces[metadata.GatewayTraceIndex]
	if trace.TraceIndex != metadata.GatewayTraceIndex || trace.Trace.GatewayTraceID != metadata.Summary.GatewayTraceID {
		t.Fatalf("gateway trace reference = %#v, summary trace ID %q", trace, metadata.Summary.GatewayTraceID)
	}
	metadata.GatewayTrace = trace.Trace
	return metadata
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

func TestHandlerWebSocketCaptureCredentialRefreshCreatesTwoPhysicalExchanges(t *testing.T) {
	initialBody := "stale credential"
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Header.Get("Authorization") {
		case "Bearer initial-token":
			w.Header().Set("X-Handshake", "initial")
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = io.WriteString(w, initialBody)
			return
		case "Bearer refreshed-token":
		default:
			t.Errorf("unexpected Authorization = %q", r.Header.Get("Authorization"))
			w.WriteHeader(http.StatusUnauthorized)
			return
		}

		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			t.Errorf("accept refreshed websocket: %v", err)
			return
		}
		if err := conn.Write(r.Context(), websocket.MessageText, []byte(`{"type":"response.created"}`)); err != nil {
			t.Errorf("write refreshed event: %v", err)
			_ = conn.CloseNow()
			return
		}
		_ = conn.Close(websocket.StatusNormalClosure, "refresh complete")
	}))
	defer upstream.Close()

	provider := model.Provider{
		ID:       "provider",
		Name:     "Provider",
		APIKey:   "readiness-only",
		Enabled:  true,
		AuthMode: AuthModeBearer,
		APITypes: []model.ProviderAPIType{{
			ProviderID: "provider",
			APIType:    APITypeCodex,
			BaseURL:    upstream.URL,
		}},
	}
	manager, session := startCaptureTestManager(t, []requestcapture.ProviderIdentity{{
		ID:   provider.ID,
		Name: provider.Name,
	}})
	defer manager.Close()
	store := newMockStore()
	store.providers = []model.Provider{provider}
	auth := &refreshingCaptureAuthenticator{}
	handler := NewHandler(Config{
		Store:   store,
		Auth:    auth,
		Capture: manager,
		Logger:  zaptest.NewLogger(t),
	})
	proxyServer := httptest.NewServer(handler)
	defer proxyServer.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn, _, err := websocket.Dial(ctx, wsURL(proxyServer)+"/responses", nil)
	if err != nil {
		t.Fatalf("dial websocket through proxy: %v", err)
	}
	if _, payload, readErr := conn.Read(ctx); readErr != nil || string(payload) != `{"type":"response.created"}` {
		t.Fatalf("refreshed event = %q, error = %v", payload, readErr)
	}
	_, _, _ = conn.Read(ctx)
	_ = conn.CloseNow()

	var page requestcapture.RecordPage
	waitFor(t, func() bool {
		var listErr error
		page, listErr = readCaptureTestPage(manager, session, requestcapture.ListQuery{Limit: 10})
		if listErr != nil || len(page.Records) != 2 {
			return false
		}
		return page.Records[0].LifecycleState == requestcapture.LifecycleStateCompleted &&
			page.Records[1].LifecycleState == requestcapture.LifecycleStateCompleted
	}, testPollTimeout)
	if auth.applyCalls != 2 || auth.refreshCalls != 1 {
		t.Fatalf("auth calls = apply:%d refresh:%d, want 2/1", auth.applyCalls, auth.refreshCalls)
	}

	var initial, refreshed *requestcapture.RecordSummary
	for index := range page.Records {
		record := &page.Records[index]
		switch record.CredentialPhase {
		case requestcapture.CredentialPhaseInitial:
			initial = record
		case requestcapture.CredentialPhaseRefreshed:
			refreshed = record
		}
	}
	if initial == nil || refreshed == nil {
		t.Fatalf("physical exchanges = %#v", page.Records)
	}
	if initial.ProviderAttemptIndex != webSocketCaptureProviderAttemptIndex ||
		refreshed.ProviderAttemptIndex != webSocketCaptureProviderAttemptIndex {
		t.Fatalf("provider attempt indices = %d/%d, want 0/0", initial.ProviderAttemptIndex, refreshed.ProviderAttemptIndex)
	}
	if initial.TerminationReason != requestcapture.TerminationReasonCredentialRefreshDrain ||
		initial.SourceCompletion != requestcapture.SourceCompletionComplete ||
		initial.UpstreamObservedBytes != int64(len(initialBody)) {
		t.Fatalf("initial exchange = %#v", initial)
	}
	initialDetail := getWebSocketCaptureTestDetail(t, manager, session, initial.RecordID)
	if initialDetail.WebSocket.Handshake == nil ||
		initialDetail.WebSocket.Handshake.StatusCode != http.StatusUnauthorized ||
		initialDetail.WebSocket.HandshakeBody.CapturedBytes != int64(len(initialBody)) {
		t.Fatalf("initial handshake detail = %#v", initialDetail.WebSocket)
	}
	if got := initialDetail.WebSocket.Request.Headers["Authorization"]; len(got) != 1 || got[0] != "Bearer initial-token" {
		t.Fatalf("initial Authorization = %#v, want access token visible", got)
	}
	exportedInitial := exportWebSocketCaptureMetadata(t, manager, session, []string{initial.RecordID})
	var exportedHandshakeBody *webSocketCaptureExportBlob
	for index := range exportedInitial.Blobs {
		if exportedInitial.Blobs[index].BlobID == webSocketCaptureHandshakeBodyBlobID {
			exportedHandshakeBody = &exportedInitial.Blobs[index]
			break
		}
	}
	if exportedInitial.WebSocket == nil || exportedInitial.WebSocket.Handshake == nil ||
		exportedInitial.WebSocket.Handshake.StatusCode != http.StatusUnauthorized ||
		exportedHandshakeBody == nil || exportedHandshakeBody.RawSize != int64(len(initialBody)) {
		t.Fatalf(
			"exported initial handshake = websocket:%#v body:%#v",
			exportedInitial.WebSocket,
			exportedHandshakeBody,
		)
	}
	refreshedDetail := getWebSocketCaptureTestDetail(t, manager, session, refreshed.RecordID)
	if refreshedDetail.WebSocket.Handshake == nil ||
		refreshedDetail.WebSocket.Handshake.StatusCode != http.StatusSwitchingProtocols {
		t.Fatalf("refreshed handshake detail = %#v", refreshedDetail.WebSocket)
	}
}

func TestHandlerWebSocketCapturePreparationFailureIsSanitizedTransition(t *testing.T) {
	t.Parallel()

	const (
		primaryID     = "preparation-primary"
		fallbackID    = "preparation-fallback"
		secret        = "preparation-secret"
		fallbackFrame = `{"type":"response.created","provider":"fallback"}`
	)
	fallback := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			t.Errorf("accept fallback websocket: %v", err)
			return
		}
		defer conn.CloseNow()
		if err := conn.Write(r.Context(), websocket.MessageText, []byte(fallbackFrame)); err != nil {
			t.Errorf("write fallback frame: %v", err)
			return
		}
		_ = conn.Close(websocket.StatusNormalClosure, "fallback complete")
	}))
	defer fallback.Close()

	primaryProvider := &model.Provider{
		ID:       primaryID,
		Name:     "Preparation Primary",
		APIKey:   "readiness-only",
		Enabled:  true,
		AuthMode: AuthModeBearer,
		APITypes: []model.ProviderAPIType{{
			ProviderID: primaryID,
			APIType:    APITypeCodex,
			BaseURL:    "https://upstream.example",
		}},
	}
	fallbackProvider := &model.Provider{
		ID:       fallbackID,
		Name:     "Preparation Fallback",
		APIKey:   "fallback-readiness-only",
		Enabled:  true,
		AuthMode: AuthModeBearer,
		APITypes: []model.ProviderAPIType{{
			ProviderID: fallbackID,
			APIType:    APITypeCodex,
			BaseURL:    fallback.URL,
		}},
	}
	manager, session := startCaptureTestManager(t, []requestcapture.ProviderIdentity{
		{ID: primaryID, Name: primaryProvider.Name},
		{ID: fallbackID, Name: fallbackProvider.Name},
	})
	defer manager.Close()
	store := newMockStore()
	store.providers = []model.Provider{*primaryProvider, *fallbackProvider}
	selector := &mockSelector{
		selectWithMetadataFunc: func(context.Context, *model.SelectRequest) (*selectResult, error) {
			return &selectResult{Provider: primaryProvider}, nil
		},
		selectExcludingFunc: func(context.Context, *model.SelectRequest, map[string]bool) (*model.Provider, error) {
			return fallbackProvider, nil
		},
	}
	handler := NewHandler(Config{
		Store:    store,
		Selector: selector,
		Auth: webSocketCaptureFailingAuthenticator{
			providerID: primaryID,
			secret:     secret,
		},
		Capture: manager,
		Logger:  zaptest.NewLogger(t),
	})
	proxyServer := httptest.NewServer(handler)
	defer proxyServer.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn, _, err := websocket.Dial(ctx, wsURL(proxyServer)+"/responses?token="+secret, nil)
	if err != nil {
		t.Fatalf("dial websocket through proxy: %v", err)
	}
	if _, payload, err := conn.Read(ctx); err != nil || string(payload) != fallbackFrame {
		t.Fatalf("fallback frame = %q, error = %v", payload, err)
	}
	_, _, _ = conn.Read(ctx)
	_ = conn.CloseNow()

	var page requestcapture.RecordPage
	waitFor(t, func() bool {
		var listErr error
		page, listErr = readCaptureTestPage(manager, session, requestcapture.ListQuery{Limit: 10})
		return listErr == nil && len(page.Records) == 1 &&
			page.Records[0].LifecycleState == requestcapture.LifecycleStateCompleted &&
			len(page.GatewayTraces) == 1 && len(page.GatewayTraces[0].Entries) == 2
	}, testPollTimeout)
	if page.Records[0].Provider.ID != fallbackID {
		t.Fatalf("physical record = %#v, want fallback", page.Records[0])
	}
	entry := page.GatewayTraces[0].Entries[0]
	if entry.Kind != requestcapture.TraceEntryTransition ||
		entry.Provider.ID != primaryID ||
		entry.TerminationReason != requestcapture.TerminationReasonPreparationError ||
		entry.ProviderAttemptIndex != webSocketCaptureProviderAttemptIndex ||
		entry.CredentialPhase != requestcapture.CredentialPhaseInitial {
		t.Fatalf("transition = %#v", entry)
	}
	if !strings.Contains(entry.Provider.TargetURL, secret) ||
		strings.Contains(entry.Failure.Primary.Message, "[REDACTED]") {
		t.Fatalf("transition provider diagnostics were unexpectedly redacted: %#v", entry)
	}
	if !entry.HasFailure || entry.Failure.Primary.Code != requestcapture.FailureCodeCredentialApply {
		t.Fatalf("transition failure = present:%t observation:%#v", entry.HasFailure, entry.Failure)
	}
	if entry.Provider.TargetURL == "" {
		t.Fatal("known physical target URL was not retained")
	}
	exported := exportWebSocketCaptureMetadata(t, manager, session, []string{page.Records[0].RecordID})
	if len(exported.GatewayTrace.Entries) != 2 ||
		exported.GatewayTrace.Entries[0].Kind != requestcapture.TraceEntryTransition ||
		exported.GatewayTrace.Entries[0].TerminationReason != requestcapture.TerminationReasonPreparationError ||
		strings.Contains(exported.GatewayTrace.Entries[0].Failure.Primary.Message, "[REDACTED]") ||
		!exported.GatewayTrace.Entries[0].HasFailure {
		t.Fatalf("exported preparation transition = %#v", exported.GatewayTrace)
	}
}
