package capturevalue

import "time"

type SessionState uint8

const (
	SessionStateStopped SessionState = iota
	SessionStateActive
)

type LifecycleState string

const (
	LifecycleStateActive    LifecycleState = "active"
	LifecycleStateCompleted LifecycleState = "completed"
)

type SourceCompletion string

const (
	SourceCompletionUnknown  SourceCompletion = "unknown"
	SourceCompletionComplete SourceCompletion = "complete"
	SourceCompletionPartial  SourceCompletion = "partial"
)

type CaptureCompletion string

const (
	CaptureCompletionComplete   CaptureCompletion = "complete"
	CaptureCompletionOverflowed CaptureCompletion = "overflowed"
)

type SnapshotState string

const (
	SnapshotStateFinal         SnapshotState = "final"
	SnapshotStateActivePartial SnapshotState = "active_partial"
)

type Protocol string

const (
	ProtocolHTTP      Protocol = "http"
	ProtocolWebSocket Protocol = "websocket"
)

type SelectionMode string

const (
	SelectionModeUnknown     SelectionMode = "unknown"
	SelectionModeInitial     SelectionMode = "initial"
	SelectionModeReplacement SelectionMode = "replacement"
	SelectionModeFailover    SelectionMode = "failover"
)

type SelectionSource string

const (
	SelectionSourceUnknown          SelectionSource = "unknown"
	SelectionSourceStrategy         SelectionSource = "strategy"
	SelectionSourceStickyContinuity SelectionSource = "sticky_continuity"
	SelectionSourceActiveContinuity SelectionSource = "active_continuity"
)

type CredentialPhase string

const (
	CredentialPhaseUnknown   CredentialPhase = "unknown"
	CredentialPhaseInitial   CredentialPhase = "initial"
	CredentialPhaseRefreshed CredentialPhase = "refreshed"
)

type TerminationReason string

const (
	TerminationReasonUnknown                TerminationReason = "unknown"
	TerminationReasonEOF                    TerminationReason = "eof"
	TerminationReasonStatusFailoverDrain    TerminationReason = "status_failover_drain"
	TerminationReasonCredentialRefreshDrain TerminationReason = "credential_refresh_drain"
	TerminationReasonInternalErrorAbsorbed  TerminationReason = "internal_error_absorbed"
	TerminationReasonInternalErrorCommitted TerminationReason = "internal_error_committed"
	TerminationReasonClientDisconnect       TerminationReason = "client_disconnect"
	TerminationReasonTimeout                TerminationReason = "timeout"
	TerminationReasonCanceled               TerminationReason = "canceled"
	TerminationReasonPreparationError       TerminationReason = "preparation_error"
	TerminationReasonGatewayFinished        TerminationReason = "gateway_finished"
	TerminationReasonCaptureFault           TerminationReason = "capture_fault"
	TerminationReasonTransportError         TerminationReason = "transport_error"
	TerminationReasonReadError              TerminationReason = "read_error"
	TerminationReasonWriteError             TerminationReason = "write_error"
	TerminationReasonWebSocketClose         TerminationReason = "websocket_close"
	TerminationReasonWebSocketRelayError    TerminationReason = "websocket_relay_error"
)

type FailureSite string

const (
	FailureSiteUnknown            FailureSite = "unknown"
	FailureSiteGateway            FailureSite = "gateway"
	FailureSitePreparation        FailureSite = "preparation"
	FailureSiteTransport          FailureSite = "transport"
	FailureSiteResponseStatus     FailureSite = "response_status"
	FailureSiteResponseDrain      FailureSite = "response_drain"
	FailureSiteResponseRead       FailureSite = "response_read"
	FailureSiteResponseWrite      FailureSite = "response_write"
	FailureSiteWebSocketHandshake FailureSite = "websocket_handshake"
	FailureSiteWebSocketUpgrade   FailureSite = "websocket_upgrade"
	FailureSiteWebSocketReplay    FailureSite = "websocket_replay"
	FailureSiteWebSocketRelay     FailureSite = "websocket_relay"
	FailureSiteWebSocketMessage   FailureSite = "websocket_message"
	FailureSiteWebSocketClose     FailureSite = "websocket_close"
)

type FailurePeer string

const (
	FailurePeerUnknown  FailurePeer = "unknown"
	FailurePeerGateway  FailurePeer = "gateway"
	FailurePeerClient   FailurePeer = "client"
	FailurePeerUpstream FailurePeer = "upstream"
	FailurePeerProvider FailurePeer = "provider"
)

type FailureClass string

const (
	FailureClassUnknown          FailureClass = "unknown"
	FailureClassTimeout          FailureClass = "timeout"
	FailureClassCanceled         FailureClass = "canceled"
	FailureClassConfiguration    FailureClass = "configuration"
	FailureClassTransport        FailureClass = "transport"
	FailureClassHTTPStatus       FailureClass = "http_status"
	FailureClassRead             FailureClass = "read"
	FailureClassWrite            FailureClass = "write"
	FailureClassProtocol         FailureClass = "protocol"
	FailureClassWebSocketClose   FailureClass = "websocket_close"
	FailureClassUpstreamSemantic FailureClass = "upstream_semantic"
)

type FailureCode string

const (
	FailureCodeUnknown            FailureCode = "unknown"
	FailureCodeMissingBaseURL     FailureCode = "missing_base_url"
	FailureCodeMissingAPIKey      FailureCode = "missing_api_key"
	FailureCodeMissingCredentials FailureCode = "missing_credentials"
	FailureCodeRequestBuild       FailureCode = "request_build"
	FailureCodeCredentialApply    FailureCode = "credential_apply"
	FailureCodeGatewayContext     FailureCode = "gateway_context"
	FailureCodeDNS                FailureCode = "dns"
	FailureCodeConnection         FailureCode = "connection"
	FailureCodeRoundTrip          FailureCode = "round_trip"
	FailureCodeUnexpectedStatus   FailureCode = "unexpected_status"
	FailureCodeFailureBodyRead    FailureCode = "failure_body_read"
	FailureCodeDrainRead          FailureCode = "drain_read"
	FailureCodeUpstreamRead       FailureCode = "upstream_read"
	FailureCodeClientWrite        FailureCode = "client_write"
	FailureCodeClientAccept       FailureCode = "client_accept"
	FailureCodeWebSocketDial      FailureCode = "websocket_dial"
	FailureCodeHandshakeRejected  FailureCode = "handshake_rejected"
	FailureCodeWebSocketUpgrade   FailureCode = "websocket_upgrade"
	FailureCodeReplayWrite        FailureCode = "replay_write"
	FailureCodeRelayRead          FailureCode = "relay_read"
	FailureCodeRelayWrite         FailureCode = "relay_write"
	FailureCodeMessageRead        FailureCode = "message_read"
	FailureCodeMessageWrite       FailureCode = "message_write"
	FailureCodeProtocolViolation  FailureCode = "protocol_violation"
	FailureCodeWebSocketClose     FailureCode = "websocket_close"
	FailureCodeProviderSemantic   FailureCode = "provider_semantic"
)

type FailureFact struct {
	Site               FailureSite  `json:"site"`
	Peer               FailurePeer  `json:"peer"`
	Class              FailureClass `json:"class"`
	Code               FailureCode  `json:"code"`
	HTTPStatusCode     int          `json:"http_status_code,omitempty"`
	WebSocketCloseCode int          `json:"websocket_close_code,omitempty"`
	SystemErrorCode    int64        `json:"system_error_code,omitempty"`
	ProviderErrorType  string       `json:"provider_error_type,omitempty"`
	ProviderErrorCode  string       `json:"provider_error_code,omitempty"`
	Message            string       `json:"message,omitempty"`
}

type FailureObservation struct {
	Primary      FailureFact `json:"primary"`
	Secondary    FailureFact `json:"secondary"`
	HasSecondary bool        `json:"has_secondary"`
	Truncated    bool        `json:"truncated"`
}

type MessageDirection string

const (
	MessageDirectionClientToUpstream MessageDirection = "client_to_upstream"
	MessageDirectionUpstreamToClient MessageDirection = "upstream_to_client"
)

type MessageType string

const (
	MessageTypeText   MessageType = "text"
	MessageTypeBinary MessageType = "binary"
)

type MessageSource string

const (
	MessageSourceLive   MessageSource = "live"
	MessageSourceReplay MessageSource = "replay"
)

type MessageDisposition string

const (
	MessageDispositionForwarded   MessageDisposition = "forwarded"
	MessageDispositionSuppressed  MessageDisposition = "suppressed"
	MessageDispositionWriteFailed MessageDisposition = "write_failed"
)

type ProviderIdentity struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type AttemptMetadata struct {
	Provider             ProviderIdentity
	APIType              string
	SelectionMode        SelectionMode
	SelectionSource      SelectionSource
	ProviderAttemptIndex int
	CredentialPhase      CredentialPhase
}

type ProviderSnapshot struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	APIType   string `json:"api_type"`
	TargetURL string `json:"target_url"`
}

type RequestSnapshot struct {
	Method        string              `json:"method"`
	URL           string              `json:"url"`
	Host          string              `json:"host"`
	Headers       map[string][]string `json:"headers"`
	ContentLength int64               `json:"content_length"`
	Trailers      map[string][]string `json:"trailers,omitempty"`
}

type HTTPResponseSnapshot struct {
	StatusCode          int                 `json:"status_code"`
	Protocol            string              `json:"protocol"`
	Headers             map[string][]string `json:"headers"`
	ContentLength       int64               `json:"content_length"`
	DeclaredTrailerKeys []string            `json:"declared_trailer_keys,omitempty"`
	Trailers            map[string][]string `json:"trailers,omitempty"`
}

type WebSocketHandshakeSnapshot struct {
	StatusCode int                 `json:"status_code"`
	Protocol   string              `json:"protocol"`
	Headers    map[string][]string `json:"headers"`
}

type BlobPreview struct {
	DataBase64     string `json:"data_base64"`
	PreviewBytes   int64  `json:"preview_bytes"`
	CapturedBytes  int64  `json:"captured_bytes"`
	Truncated      bool   `json:"truncated"`
	ChecksumSHA256 string `json:"checksum_sha256"`
}

type MessageSnapshot struct {
	MessageID       string             `json:"message_id"`
	Sequence        uint64             `json:"sequence"`
	RelativeMillis  int64              `json:"relative_millis"`
	Direction       MessageDirection   `json:"direction"`
	Type            MessageType        `json:"message_type"`
	Source          MessageSource      `json:"source"`
	SourceMessageID string             `json:"source_message_id,omitempty"`
	Disposition     MessageDisposition `json:"disposition,omitempty"`
	ClientVisible   bool               `json:"client_visible"`
	Failure         FailureObservation `json:"failure"`
	HasFailure      bool               `json:"has_failure"`
	Payload         BlobPreview        `json:"payload"`
}

type RecordSummary struct {
	SessionID                      string             `json:"session_id"`
	RecordID                       string             `json:"record_id"`
	GatewayTraceID                 string             `json:"gateway_trace_id"`
	GatewayRequestID               string             `json:"gateway_request_id"`
	ExchangeIndex                  uint64             `json:"exchange_index"`
	RecordSequence                 uint64             `json:"record_sequence"`
	Provider                       ProviderSnapshot   `json:"provider"`
	Protocol                       Protocol           `json:"protocol"`
	SelectionMode                  SelectionMode      `json:"selection_mode"`
	SelectionSource                SelectionSource    `json:"selection_source"`
	ProviderAttemptIndex           int                `json:"provider_attempt_index"`
	CredentialPhase                CredentialPhase    `json:"credential_phase"`
	LifecycleState                 LifecycleState     `json:"lifecycle_state"`
	SourceCompletion               SourceCompletion   `json:"source_completion,omitempty"`
	CaptureCompletion              CaptureCompletion  `json:"capture_completion"`
	StartedAt                      time.Time          `json:"started_at"`
	CompletedAt                    *time.Time         `json:"completed_at,omitempty"`
	TerminationReason              TerminationReason  `json:"termination_reason,omitempty"`
	Failure                        FailureObservation `json:"failure"`
	HasFailure                     bool               `json:"has_failure"`
	UpstreamObservedBytes          int64              `json:"upstream_observed_bytes"`
	ApplicationWriteConfirmedBytes int64              `json:"application_write_confirmed_bytes"`
}

type HTTPExchangeDetail struct {
	Request      RequestSnapshot       `json:"request"`
	RequestBody  BlobPreview           `json:"request_body"`
	Response     *HTTPResponseSnapshot `json:"response,omitempty"`
	ResponseBody BlobPreview           `json:"response_body"`
}

type WebSocketCloseSnapshot struct {
	Direction       MessageDirection `json:"direction"`
	Code            int              `json:"code"`
	Reason          string           `json:"reason,omitempty"`
	ReasonTruncated bool             `json:"reason_truncated"`
	Clean           bool             `json:"clean"`
}

type WebSocketExchangeDetail struct {
	Request         RequestSnapshot             `json:"request"`
	RequestBody     BlobPreview                 `json:"request_body"`
	Handshake       *WebSocketHandshakeSnapshot `json:"handshake,omitempty"`
	HandshakeBody   BlobPreview                 `json:"handshake_body"`
	Messages        []MessageSnapshot           `json:"messages"`
	EventsTruncated bool                        `json:"events_truncated"`
	Close           *WebSocketCloseSnapshot     `json:"close,omitempty"`
}

type RecordDetail struct {
	Summary       RecordSummary            `json:"summary"`
	SnapshotState SnapshotState            `json:"snapshot_state"`
	GatewayTrace  *GatewayTraceSummary     `json:"gateway_trace"`
	HTTP          *HTTPExchangeDetail      `json:"http,omitempty"`
	WebSocket     *WebSocketExchangeDetail `json:"websocket,omitempty"`
}

type TraceEntryKind string

const (
	TraceEntryRecord     TraceEntryKind = "record"
	TraceEntryTransition TraceEntryKind = "transition"
)

type TraceEntry struct {
	Kind                 TraceEntryKind     `json:"kind"`
	EntryID              string             `json:"entry_id"`
	Sequence             uint64             `json:"sequence"`
	RecordID             string             `json:"record_id,omitempty"`
	Provider             ProviderSnapshot   `json:"provider"`
	ProviderAttemptIndex int                `json:"provider_attempt_index"`
	SelectionMode        SelectionMode      `json:"selection_mode"`
	SelectionSource      SelectionSource    `json:"selection_source"`
	CredentialPhase      CredentialPhase    `json:"credential_phase"`
	TerminationReason    TerminationReason  `json:"termination_reason,omitempty"`
	Failure              FailureObservation `json:"failure"`
	HasFailure           bool               `json:"has_failure"`
	MetadataTruncated    bool               `json:"metadata_truncated"`
}

type GatewayTraceSummary struct {
	GatewayTraceID         string       `json:"gateway_trace_id"`
	GatewayRequestID       string       `json:"gateway_request_id"`
	Entries                []TraceEntry `json:"entries"`
	HistoryTruncatedBefore bool         `json:"history_truncated_before"`
	HistoryTruncatedAfter  bool         `json:"history_truncated_after"`
}

type EvictionGap struct {
	Detected    bool `json:"detected"`
	RecordCount int  `json:"record_count"`
}

type ListQuery struct {
	Limit             int
	Cursor            string
	SnapshotWatermark string
}

type RecordPage struct {
	SessionID         string                `json:"session_id"`
	SnapshotWatermark string                `json:"snapshot_watermark"`
	Records           []RecordSummary       `json:"records"`
	GatewayTraces     []GatewayTraceSummary `json:"gateway_traces"`
	NextCursor        string                `json:"next_cursor,omitempty"`
	EvictionGap       EvictionGap           `json:"eviction_gap"`
}

func CanonicalSelectionMode(value SelectionMode) (SelectionMode, bool) {
	switch value {
	case SelectionModeInitial:
		return SelectionModeInitial, true
	case SelectionModeReplacement:
		return SelectionModeReplacement, true
	case SelectionModeFailover:
		return SelectionModeFailover, true
	default:
		return "", false
	}
}

func CanonicalSelectionSource(value SelectionSource) (SelectionSource, bool) {
	switch value {
	case SelectionSourceStrategy:
		return SelectionSourceStrategy, true
	case SelectionSourceStickyContinuity:
		return SelectionSourceStickyContinuity, true
	case SelectionSourceActiveContinuity:
		return SelectionSourceActiveContinuity, true
	default:
		return "", false
	}
}

func CanonicalCredentialPhase(value CredentialPhase) (CredentialPhase, bool) {
	switch value {
	case CredentialPhaseInitial:
		return CredentialPhaseInitial, true
	case CredentialPhaseRefreshed:
		return CredentialPhaseRefreshed, true
	default:
		return "", false
	}
}

func CanonicalMessageDirection(value MessageDirection) (MessageDirection, bool) {
	switch value {
	case MessageDirectionClientToUpstream:
		return MessageDirectionClientToUpstream, true
	case MessageDirectionUpstreamToClient:
		return MessageDirectionUpstreamToClient, true
	default:
		return "", false
	}
}

func CanonicalMessageType(value MessageType) (MessageType, bool) {
	switch value {
	case MessageTypeText:
		return MessageTypeText, true
	case MessageTypeBinary:
		return MessageTypeBinary, true
	default:
		return "", false
	}
}

func CanonicalMessageSource(value MessageSource) (MessageSource, bool) {
	switch value {
	case MessageSourceLive:
		return MessageSourceLive, true
	case MessageSourceReplay:
		return MessageSourceReplay, true
	default:
		return "", false
	}
}

func CanonicalMessageDisposition(value MessageDisposition) (MessageDisposition, bool) {
	switch value {
	case MessageDispositionForwarded:
		return MessageDispositionForwarded, true
	case MessageDispositionSuppressed:
		return MessageDispositionSuppressed, true
	case MessageDispositionWriteFailed:
		return MessageDispositionWriteFailed, true
	default:
		return "", false
	}
}

func CanonicalTerminationReason(value TerminationReason) (TerminationReason, bool) {
	switch value {
	case TerminationReasonEOF:
		return TerminationReasonEOF, true
	case TerminationReasonStatusFailoverDrain:
		return TerminationReasonStatusFailoverDrain, true
	case TerminationReasonCredentialRefreshDrain:
		return TerminationReasonCredentialRefreshDrain, true
	case TerminationReasonInternalErrorAbsorbed:
		return TerminationReasonInternalErrorAbsorbed, true
	case TerminationReasonInternalErrorCommitted:
		return TerminationReasonInternalErrorCommitted, true
	case TerminationReasonClientDisconnect:
		return TerminationReasonClientDisconnect, true
	case TerminationReasonTimeout:
		return TerminationReasonTimeout, true
	case TerminationReasonCanceled:
		return TerminationReasonCanceled, true
	case TerminationReasonPreparationError:
		return TerminationReasonPreparationError, true
	case TerminationReasonGatewayFinished:
		return TerminationReasonGatewayFinished, true
	case TerminationReasonCaptureFault:
		return TerminationReasonCaptureFault, true
	case TerminationReasonTransportError:
		return TerminationReasonTransportError, true
	case TerminationReasonReadError:
		return TerminationReasonReadError, true
	case TerminationReasonWriteError:
		return TerminationReasonWriteError, true
	case TerminationReasonWebSocketClose:
		return TerminationReasonWebSocketClose, true
	case TerminationReasonWebSocketRelayError:
		return TerminationReasonWebSocketRelayError, true
	default:
		return "", false
	}
}

func CanonicalSourceCompletion(value SourceCompletion) (SourceCompletion, bool) {
	switch value {
	case SourceCompletionComplete:
		return SourceCompletionComplete, true
	case SourceCompletionPartial:
		return SourceCompletionPartial, true
	default:
		return "", false
	}
}

func CanonicalFailureSite(value FailureSite) (FailureSite, bool) {
	switch value {
	case FailureSiteUnknown:
		return FailureSiteUnknown, true
	case FailureSiteGateway:
		return FailureSiteGateway, true
	case FailureSitePreparation:
		return FailureSitePreparation, true
	case FailureSiteTransport:
		return FailureSiteTransport, true
	case FailureSiteResponseStatus:
		return FailureSiteResponseStatus, true
	case FailureSiteResponseDrain:
		return FailureSiteResponseDrain, true
	case FailureSiteResponseRead:
		return FailureSiteResponseRead, true
	case FailureSiteResponseWrite:
		return FailureSiteResponseWrite, true
	case FailureSiteWebSocketHandshake:
		return FailureSiteWebSocketHandshake, true
	case FailureSiteWebSocketUpgrade:
		return FailureSiteWebSocketUpgrade, true
	case FailureSiteWebSocketReplay:
		return FailureSiteWebSocketReplay, true
	case FailureSiteWebSocketRelay:
		return FailureSiteWebSocketRelay, true
	case FailureSiteWebSocketMessage:
		return FailureSiteWebSocketMessage, true
	case FailureSiteWebSocketClose:
		return FailureSiteWebSocketClose, true
	default:
		return FailureSiteUnknown, false
	}
}

func CanonicalFailurePeer(value FailurePeer) (FailurePeer, bool) {
	switch value {
	case FailurePeerUnknown:
		return FailurePeerUnknown, true
	case FailurePeerGateway:
		return FailurePeerGateway, true
	case FailurePeerClient:
		return FailurePeerClient, true
	case FailurePeerUpstream:
		return FailurePeerUpstream, true
	case FailurePeerProvider:
		return FailurePeerProvider, true
	default:
		return FailurePeerUnknown, false
	}
}

func CanonicalFailureClass(value FailureClass) (FailureClass, bool) {
	switch value {
	case FailureClassUnknown:
		return FailureClassUnknown, true
	case FailureClassTimeout:
		return FailureClassTimeout, true
	case FailureClassCanceled:
		return FailureClassCanceled, true
	case FailureClassConfiguration:
		return FailureClassConfiguration, true
	case FailureClassTransport:
		return FailureClassTransport, true
	case FailureClassHTTPStatus:
		return FailureClassHTTPStatus, true
	case FailureClassRead:
		return FailureClassRead, true
	case FailureClassWrite:
		return FailureClassWrite, true
	case FailureClassProtocol:
		return FailureClassProtocol, true
	case FailureClassWebSocketClose:
		return FailureClassWebSocketClose, true
	case FailureClassUpstreamSemantic:
		return FailureClassUpstreamSemantic, true
	default:
		return FailureClassUnknown, false
	}
}

func CanonicalFailureCode(value FailureCode) (FailureCode, bool) {
	switch value {
	case FailureCodeUnknown:
		return FailureCodeUnknown, true
	case FailureCodeMissingBaseURL:
		return FailureCodeMissingBaseURL, true
	case FailureCodeMissingAPIKey:
		return FailureCodeMissingAPIKey, true
	case FailureCodeMissingCredentials:
		return FailureCodeMissingCredentials, true
	case FailureCodeRequestBuild:
		return FailureCodeRequestBuild, true
	case FailureCodeCredentialApply:
		return FailureCodeCredentialApply, true
	case FailureCodeGatewayContext:
		return FailureCodeGatewayContext, true
	case FailureCodeDNS:
		return FailureCodeDNS, true
	case FailureCodeConnection:
		return FailureCodeConnection, true
	case FailureCodeRoundTrip:
		return FailureCodeRoundTrip, true
	case FailureCodeUnexpectedStatus:
		return FailureCodeUnexpectedStatus, true
	case FailureCodeFailureBodyRead:
		return FailureCodeFailureBodyRead, true
	case FailureCodeDrainRead:
		return FailureCodeDrainRead, true
	case FailureCodeUpstreamRead:
		return FailureCodeUpstreamRead, true
	case FailureCodeClientWrite:
		return FailureCodeClientWrite, true
	case FailureCodeClientAccept:
		return FailureCodeClientAccept, true
	case FailureCodeWebSocketDial:
		return FailureCodeWebSocketDial, true
	case FailureCodeHandshakeRejected:
		return FailureCodeHandshakeRejected, true
	case FailureCodeWebSocketUpgrade:
		return FailureCodeWebSocketUpgrade, true
	case FailureCodeReplayWrite:
		return FailureCodeReplayWrite, true
	case FailureCodeRelayRead:
		return FailureCodeRelayRead, true
	case FailureCodeRelayWrite:
		return FailureCodeRelayWrite, true
	case FailureCodeMessageRead:
		return FailureCodeMessageRead, true
	case FailureCodeMessageWrite:
		return FailureCodeMessageWrite, true
	case FailureCodeProtocolViolation:
		return FailureCodeProtocolViolation, true
	case FailureCodeWebSocketClose:
		return FailureCodeWebSocketClose, true
	case FailureCodeProviderSemantic:
		return FailureCodeProviderSemantic, true
	default:
		return FailureCodeUnknown, false
	}
}
