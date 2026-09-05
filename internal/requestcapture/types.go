package requestcapture

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"

	"github.com/doraemonkeys/switch-a/internal/requestcapture/capturevalue"
	"github.com/doraemonkeys/switch-a/internal/requestcapture/redaction"
)

const RawPayloadRiskNotice = "Debug capture contains raw request and response payloads that may include sensitive data."

var (
	ErrSessionActive        = errors.New("request capture session is already active")
	ErrNoActiveSession      = errors.New("request capture session is not active")
	ErrSessionMismatch      = errors.New("request capture session does not match the active session")
	ErrGenerationExhausted  = errors.New("request capture generation exhausted")
	ErrRecordNotFound       = errors.New("request capture record not found")
	ErrRecordEvicted        = errors.New("request capture record was evicted")
	ErrInvalidCursor        = errors.New("invalid request capture cursor")
	ErrSnapshotChanged      = errors.New("request capture snapshot changed")
	ErrExportLimitReached   = errors.New("request capture export limit reached")
	ErrDownloadLimitReached = errors.New("request capture download limit reached")
	ErrDownloadUnavailable  = errors.New("request capture download unavailable")
	ErrExportCanceled       = errors.New("request capture export canceled")
	ErrCapacityExceeded     = errors.New("request capture capacity exceeded")
	ErrQueryCanceled        = errors.New("request capture query canceled")
	ErrManagerClosed        = errors.New("request capture manager is closed")
	ErrInternalFailure      = errors.New("request capture internal failure")
)

// ValidationError identifies a caller-correctable field without mixing transport
// status decisions into the core capture domain.
type ValidationError struct {
	Field  string
	Reason string
}

func (e *ValidationError) Error() string {
	if e == nil {
		return "request capture validation failed"
	}
	if e.Field == "" {
		return "request capture validation failed: " + e.Reason
	}
	return fmt.Sprintf("request capture validation failed for %s: %s", e.Field, e.Reason)
}

// SessionState remains scalar because Status is an uncharged diagnostic value
// that callers may retain after the session account has been released.
// Root aliases preserve the public capture vocabulary while redaction and wire
// encoders share an acyclic, immutable value schema.
type (
	SessionState               = capturevalue.SessionState
	LifecycleState             = capturevalue.LifecycleState
	SourceCompletion           = capturevalue.SourceCompletion
	CaptureCompletion          = capturevalue.CaptureCompletion
	SnapshotState              = capturevalue.SnapshotState
	Protocol                   = capturevalue.Protocol
	SelectionMode              = capturevalue.SelectionMode
	SelectionSource            = capturevalue.SelectionSource
	CredentialPhase            = capturevalue.CredentialPhase
	TerminationReason          = capturevalue.TerminationReason
	FailureSite                = capturevalue.FailureSite
	FailurePeer                = capturevalue.FailurePeer
	FailureClass               = capturevalue.FailureClass
	FailureCode                = capturevalue.FailureCode
	FailureFact                = capturevalue.FailureFact
	FailureObservation         = capturevalue.FailureObservation
	MessageDirection           = capturevalue.MessageDirection
	MessageType                = capturevalue.MessageType
	MessageSource              = capturevalue.MessageSource
	MessageDisposition         = capturevalue.MessageDisposition
	ProviderIdentity           = capturevalue.ProviderIdentity
	AttemptMetadata            = capturevalue.AttemptMetadata
	ProviderSnapshot           = capturevalue.ProviderSnapshot
	RequestSnapshot            = capturevalue.RequestSnapshot
	HTTPResponseSnapshot       = capturevalue.HTTPResponseSnapshot
	WebSocketHandshakeSnapshot = capturevalue.WebSocketHandshakeSnapshot
	BlobPreview                = capturevalue.BlobPreview
	MessageSnapshot            = capturevalue.MessageSnapshot
	RecordSummary              = capturevalue.RecordSummary
	HTTPExchangeDetail         = capturevalue.HTTPExchangeDetail
	WebSocketCloseSnapshot     = capturevalue.WebSocketCloseSnapshot
	WebSocketExchangeDetail    = capturevalue.WebSocketExchangeDetail
	RecordDetail               = capturevalue.RecordDetail
	TraceEntryKind             = capturevalue.TraceEntryKind
	TraceEntry                 = capturevalue.TraceEntry
	GatewayTraceSummary        = capturevalue.GatewayTraceSummary
	EvictionGap                = capturevalue.EvictionGap
	ListQuery                  = capturevalue.ListQuery
	RecordPage                 = capturevalue.RecordPage
)

const (
	SessionStateStopped                     = capturevalue.SessionStateStopped
	SessionStateActive                      = capturevalue.SessionStateActive
	LifecycleStateActive                    = capturevalue.LifecycleStateActive
	LifecycleStateCompleted                 = capturevalue.LifecycleStateCompleted
	SourceCompletionUnknown                 = capturevalue.SourceCompletionUnknown
	SourceCompletionComplete                = capturevalue.SourceCompletionComplete
	SourceCompletionPartial                 = capturevalue.SourceCompletionPartial
	CaptureCompletionComplete               = capturevalue.CaptureCompletionComplete
	CaptureCompletionOverflowed             = capturevalue.CaptureCompletionOverflowed
	SnapshotStateFinal                      = capturevalue.SnapshotStateFinal
	SnapshotStateActivePartial              = capturevalue.SnapshotStateActivePartial
	ProtocolHTTP                            = capturevalue.ProtocolHTTP
	ProtocolWebSocket                       = capturevalue.ProtocolWebSocket
	SelectionModeUnknown                    = capturevalue.SelectionModeUnknown
	SelectionModeInitial                    = capturevalue.SelectionModeInitial
	SelectionModeReplacement                = capturevalue.SelectionModeReplacement
	SelectionModeFailover                   = capturevalue.SelectionModeFailover
	SelectionSourceUnknown                  = capturevalue.SelectionSourceUnknown
	SelectionSourceStrategy                 = capturevalue.SelectionSourceStrategy
	SelectionSourceStickyContinuity         = capturevalue.SelectionSourceStickyContinuity
	SelectionSourceActiveContinuity         = capturevalue.SelectionSourceActiveContinuity
	CredentialPhaseUnknown                  = capturevalue.CredentialPhaseUnknown
	CredentialPhaseInitial                  = capturevalue.CredentialPhaseInitial
	CredentialPhaseRefreshed                = capturevalue.CredentialPhaseRefreshed
	TerminationReasonUnknown                = capturevalue.TerminationReasonUnknown
	TerminationReasonEOF                    = capturevalue.TerminationReasonEOF
	TerminationReasonStatusFailoverDrain    = capturevalue.TerminationReasonStatusFailoverDrain
	TerminationReasonCredentialRefreshDrain = capturevalue.TerminationReasonCredentialRefreshDrain
	TerminationReasonInternalErrorAbsorbed  = capturevalue.TerminationReasonInternalErrorAbsorbed
	TerminationReasonInternalErrorCommitted = capturevalue.TerminationReasonInternalErrorCommitted
	TerminationReasonClientDisconnect       = capturevalue.TerminationReasonClientDisconnect
	TerminationReasonTimeout                = capturevalue.TerminationReasonTimeout
	TerminationReasonCanceled               = capturevalue.TerminationReasonCanceled
	TerminationReasonPreparationError       = capturevalue.TerminationReasonPreparationError
	TerminationReasonGatewayFinished        = capturevalue.TerminationReasonGatewayFinished
	TerminationReasonCaptureFault           = capturevalue.TerminationReasonCaptureFault
	TerminationReasonTransportError         = capturevalue.TerminationReasonTransportError
	TerminationReasonReadError              = capturevalue.TerminationReasonReadError
	TerminationReasonWriteError             = capturevalue.TerminationReasonWriteError
	TerminationReasonWebSocketClose         = capturevalue.TerminationReasonWebSocketClose
	TerminationReasonWebSocketRelayError    = capturevalue.TerminationReasonWebSocketRelayError
	FailureSiteUnknown                      = capturevalue.FailureSiteUnknown
	FailureSiteGateway                      = capturevalue.FailureSiteGateway
	FailureSitePreparation                  = capturevalue.FailureSitePreparation
	FailureSiteTransport                    = capturevalue.FailureSiteTransport
	FailureSiteResponseStatus               = capturevalue.FailureSiteResponseStatus
	FailureSiteResponseDrain                = capturevalue.FailureSiteResponseDrain
	FailureSiteResponseRead                 = capturevalue.FailureSiteResponseRead
	FailureSiteResponseWrite                = capturevalue.FailureSiteResponseWrite
	FailureSiteWebSocketHandshake           = capturevalue.FailureSiteWebSocketHandshake
	FailureSiteWebSocketUpgrade             = capturevalue.FailureSiteWebSocketUpgrade
	FailureSiteWebSocketReplay              = capturevalue.FailureSiteWebSocketReplay
	FailureSiteWebSocketRelay               = capturevalue.FailureSiteWebSocketRelay
	FailureSiteWebSocketMessage             = capturevalue.FailureSiteWebSocketMessage
	FailureSiteWebSocketClose               = capturevalue.FailureSiteWebSocketClose
	FailurePeerUnknown                      = capturevalue.FailurePeerUnknown
	FailurePeerGateway                      = capturevalue.FailurePeerGateway
	FailurePeerClient                       = capturevalue.FailurePeerClient
	FailurePeerUpstream                     = capturevalue.FailurePeerUpstream
	FailurePeerProvider                     = capturevalue.FailurePeerProvider
	FailureClassUnknown                     = capturevalue.FailureClassUnknown
	FailureClassTimeout                     = capturevalue.FailureClassTimeout
	FailureClassCanceled                    = capturevalue.FailureClassCanceled
	FailureClassConfiguration               = capturevalue.FailureClassConfiguration
	FailureClassTransport                   = capturevalue.FailureClassTransport
	FailureClassHTTPStatus                  = capturevalue.FailureClassHTTPStatus
	FailureClassRead                        = capturevalue.FailureClassRead
	FailureClassWrite                       = capturevalue.FailureClassWrite
	FailureClassProtocol                    = capturevalue.FailureClassProtocol
	FailureClassWebSocketClose              = capturevalue.FailureClassWebSocketClose
	FailureClassUpstreamSemantic            = capturevalue.FailureClassUpstreamSemantic
	FailureCodeUnknown                      = capturevalue.FailureCodeUnknown
	FailureCodeMissingBaseURL               = capturevalue.FailureCodeMissingBaseURL
	FailureCodeMissingAPIKey                = capturevalue.FailureCodeMissingAPIKey
	FailureCodeMissingCredentials           = capturevalue.FailureCodeMissingCredentials
	FailureCodeRequestBuild                 = capturevalue.FailureCodeRequestBuild
	FailureCodeCredentialApply              = capturevalue.FailureCodeCredentialApply
	FailureCodeGatewayContext               = capturevalue.FailureCodeGatewayContext
	FailureCodeGatewayIngress               = capturevalue.FailureCodeGatewayIngress
	FailureCodeDNS                          = capturevalue.FailureCodeDNS
	FailureCodeConnection                   = capturevalue.FailureCodeConnection
	FailureCodeRoundTrip                    = capturevalue.FailureCodeRoundTrip
	FailureCodeUnexpectedStatus             = capturevalue.FailureCodeUnexpectedStatus
	FailureCodeFailureBodyRead              = capturevalue.FailureCodeFailureBodyRead
	FailureCodeDrainRead                    = capturevalue.FailureCodeDrainRead
	FailureCodeUpstreamRead                 = capturevalue.FailureCodeUpstreamRead
	FailureCodeClientCancel                 = capturevalue.FailureCodeClientCancel
	FailureCodeClientWrite                  = capturevalue.FailureCodeClientWrite
	FailureCodeClientAccept                 = capturevalue.FailureCodeClientAccept
	FailureCodeWebSocketDial                = capturevalue.FailureCodeWebSocketDial
	FailureCodeHandshakeRejected            = capturevalue.FailureCodeHandshakeRejected
	FailureCodeWebSocketUpgrade             = capturevalue.FailureCodeWebSocketUpgrade
	FailureCodeReplayWrite                  = capturevalue.FailureCodeReplayWrite
	FailureCodeRelayRead                    = capturevalue.FailureCodeRelayRead
	FailureCodeRelayWrite                   = capturevalue.FailureCodeRelayWrite
	FailureCodeMessageRead                  = capturevalue.FailureCodeMessageRead
	FailureCodeMessageWrite                 = capturevalue.FailureCodeMessageWrite
	FailureCodeProtocolViolation            = capturevalue.FailureCodeProtocolViolation
	FailureCodeWebSocketClose               = capturevalue.FailureCodeWebSocketClose
	FailureCodeProviderSemantic             = capturevalue.FailureCodeProviderSemantic
	MessageDirectionClientToUpstream        = capturevalue.MessageDirectionClientToUpstream
	MessageDirectionUpstreamToClient        = capturevalue.MessageDirectionUpstreamToClient
	MessageTypeText                         = capturevalue.MessageTypeText
	MessageTypeBinary                       = capturevalue.MessageTypeBinary
	MessageSourceLive                       = capturevalue.MessageSourceLive
	MessageSourceReplay                     = capturevalue.MessageSourceReplay
	MessageDispositionForwarded             = capturevalue.MessageDispositionForwarded
	MessageDispositionSuppressed            = capturevalue.MessageDispositionSuppressed
	MessageDispositionWriteFailed           = capturevalue.MessageDispositionWriteFailed
	MessageDispositionIdentityRejected      = capturevalue.MessageDispositionIdentityRejected
	MessageDispositionProtocolRejected      = capturevalue.MessageDispositionProtocolRejected
	MessageDispositionStorageRejected       = capturevalue.MessageDispositionStorageRejected
	TraceEntryRecord                        = capturevalue.TraceEntryRecord
	TraceEntryTransition                    = capturevalue.TraceEntryTransition
)

// Evidence aliases keep producer-facing capabilities opaque while the redaction
// module owns their fixed-capacity storage and mutation rules.
type (
	CredentialEvidence      = redaction.CredentialEvidence
	SensitiveHeaderEvidence = redaction.SensitiveHeaderEvidence
)

type ExportScope string

const (
	ExportScopeAll     ExportScope = "all"
	ExportScopeRecords ExportScope = "records"
)

type StartRequest struct {
	Providers                   []ProviderIdentity `json:"providers"`
	CompletedRecordsPerProvider int                `json:"completed_records_per_provider"`
	RetainedBytesLimit          int64              `json:"retained_bytes_limit"`
	AcknowledgeRawPayloadRisk   bool               `json:"acknowledge_raw_payload_risk"`
}

type SessionInfo struct {
	SessionID                   string             `json:"session_id"`
	Generation                  uint64             `json:"generation"`
	StartedAt                   time.Time          `json:"started_at"`
	Providers                   []ProviderIdentity `json:"providers"`
	ProviderIDs                 []string           `json:"provider_ids"`
	CompletedRecordsPerProvider int                `json:"completed_records_per_provider"`
	RetainedBytesLimit          int64              `json:"retained_bytes_limit"`
}

type ProcessMemoryStatus struct {
	CeilingBytes   int64 `json:"ceiling_bytes"`
	ChargedBytes   int64 `json:"charged_bytes"`
	RetainedBytes  int64 `json:"retained_bytes"`
	PinnedBytes    int64 `json:"pinned_bytes"`
	ReleasingBytes int64 `json:"releasing_bytes"`
	TemporaryBytes int64 `json:"temporary_bytes"`
}

type SessionStatus struct {
	SessionID                   StatusSessionID
	Generation                  uint64
	StartedAtUnixNano           int64
	ProviderCount               int
	CompletedRecordsPerProvider int
	RetainedBytesLimit          int64
	RetainedBytes               int64
	ActiveRecordCount           int
	CompletedRecordCount        int
	GatewayTraceCount           int
	EvictedRecordCount          uint64
	OverflowedRecordCount       uint64
	HistoryTruncatedTraceCount  uint64
	DroppedTraceCount           uint64
	DroppedExchangeCount        uint64
	DroppedTransitionCount      uint64
}

type Status struct {
	State               SessionState
	ProcessMemory       ProcessMemoryStatus
	PendingExportCount  int
	ActiveDownloadCount int
	HasSession          bool
	Session             SessionStatus
}

// GatewayRecorder is a lightweight value bound to one immutable session
// generation. Its zero value is the disabled-path recorder and is always safe.
type GatewayRecorder struct {
	manager       *Manager
	generation    uint64
	traceSequence uint64
	handleSlot    uint32
}

// Recorder captures one real HTTP request or WebSocket dial. It never surfaces
// failures to the proxy; capture degradation is represented in record metadata.
type recorderKind uint8

const (
	recorderKindRecord recorderKind = iota + 1
	recorderKindTransition
)

type Recorder struct {
	manager        *Manager
	generation     uint64
	traceSequence  uint64
	recordSequence uint64
	entrySequence  uint64
	gatewaySlot    uint32
	recordSlot     uint32
	recordID       recordIDValue
	kind           recorderKind
}

type GatewayStart struct {
	GatewayRequestID string
	StartedAt        time.Time
}

type RawRequest struct {
	Method             string
	Headers            http.Header
	ContentLength      int64
	Trailers           http.Header
	Body               []byte
	SensitiveHeaders   SensitiveHeaderEvidence
	CredentialEvidence CredentialEvidence
}

type IngressHead = redaction.IngressHead
type IngressFailure = capturevalue.IngressFailureSnapshot

const (
	IngressFailureUnknown = capturevalue.IngressFailureUnknown
	IngressFailureRead    = capturevalue.IngressFailureRead
	IngressFailureLimit   = capturevalue.IngressFailureLimit
	IngressFailureLength  = capturevalue.IngressFailureLength
	IngressFailureStorage = capturevalue.IngressFailureStorage
)

type IngressFinish struct {
	State         string
	ReceivedBytes int64
	Trailers      http.Header
	Reason        string
}

type RawHTTPStart struct {
	Attempt AttemptMetadata
	// URL is borrowed for the duration of BeginHTTP; requestcapture owns every
	// retained representation it derives from it.
	URL     *url.URL
	Request RawRequest
}

type RawWebSocketStart struct {
	Attempt AttemptMetadata
	// TargetURL is borrowed for the duration of BeginWebSocket and parsed inside
	// requestcapture so the proxy performs no capture-only materialization.
	TargetURL string
	Request   RawRequest
}

// TransitionTargetInput is a closed borrowed URL union. It prevents HTTP
// callers from materializing URL.String and WebSocket callers from parsing a URL
// solely to feed capture.
type TransitionTargetInput struct {
	protocol     Protocol
	httpURL      *url.URL
	webSocketURL string
}

func HTTPTransitionTarget(raw *url.URL) TransitionTargetInput {
	return TransitionTargetInput{protocol: ProtocolHTTP, httpURL: raw}
}

func WebSocketTransitionTarget(raw string) TransitionTargetInput {
	return TransitionTargetInput{protocol: ProtocolWebSocket, webSocketURL: raw}
}

type TransitionStart struct {
	Attempt            AttemptMetadata
	Target             TransitionTargetInput
	TerminationReason  TerminationReason
	Failure            FailureObservation
	CredentialEvidence CredentialEvidence
}

type GatewayOutcome struct {
	TerminationReason  TerminationReason
	Failure            FailureObservation
	CredentialEvidence CredentialEvidence
}

type HTTPResponseHead struct {
	StatusCode         int
	Protocol           string
	Headers            http.Header
	ContentLength      int64
	DeclaredTrailers   http.Header
	SensitiveHeaders   SensitiveHeaderEvidence
	CredentialEvidence CredentialEvidence
}

type WebSocketHandshake struct {
	StatusCode         int
	Protocol           string
	Headers            http.Header
	SensitiveHeaders   SensitiveHeaderEvidence
	CredentialEvidence CredentialEvidence
}

type WebSocketCloseObservation struct {
	Direction MessageDirection
	Code      int
	Reason    string
	Clean     bool
}

type Outcome struct {
	SourceCompletion   SourceCompletion
	TerminationReason  TerminationReason
	Failure            FailureObservation
	ResponseTrailers   http.Header
	WebSocketClose     *WebSocketCloseObservation
	CredentialEvidence CredentialEvidence
	// CompletedAt freezes the physical exchange boundary while publication waits
	// for proxy-owned health, sticky, and retry commits. Zero preserves the
	// recorder's clock-based fallback for direct and compatibility callers.
	CompletedAt time.Time `json:"-"`
}

type MessageRead struct {
	Lineage       MessageLineage
	Direction     MessageDirection
	Type          MessageType
	Payload       []byte
	Source        MessageSource
	SourceLineage MessageLineage
}

type MessageResult struct {
	Disposition        MessageDisposition
	WriteConfirmed     bool
	Failure            FailureObservation
	CredentialEvidence CredentialEvidence
}

type MessageRef struct {
	manager        *Manager
	generation     uint64
	traceSequence  uint64
	recordSequence uint64
	sequence       uint64
	lineage        uint64
}

func (r MessageRef) issued() bool {
	return r.manager != nil && r.generation != 0 && r.traceSequence != 0 &&
		r.recordSequence != 0 && r.sequence != 0 && r.lineage != 0
}

func (r MessageRef) active() bool {
	if !r.issued() {
		return false
	}
	session := r.manager.retainActive(r.generation)
	if session == nil {
		return false
	}
	_ = session.releaseOwner()
	return true
}

// ID is derived entirely from the issued value capability. Eviction and Stop may
// revoke mutation access, but they cannot change an identity already returned to
// proxy code.
func (r MessageRef) ID() string {
	if !r.issued() {
		return ""
	}
	return materializeMessageID(r.generation, r.traceSequence, r.lineage)
}

func (r MessageRef) Sequence() uint64 {
	if !r.issued() {
		return 0
	}
	return r.sequence
}

func (r MessageRef) Valid() bool {
	return r.active()
}

// Lineage returns the lineage actually published with the message. It may differ
// from MessageRead.Lineage when capture had to assign a fallback capability.
func (r MessageRef) Lineage() MessageLineage {
	if !r.issued() {
		return MessageLineage{}
	}
	return MessageLineage{
		generation:    r.generation,
		traceSequence: r.traceSequence,
		lineage:       r.lineage,
	}
}

func (r MessageRef) belongsTo(recorder Recorder) bool {
	return r.manager != nil &&
		r.manager == recorder.manager &&
		r.generation == recorder.generation &&
		r.traceSequence == recorder.traceSequence &&
		r.recordSequence == recorder.recordSequence &&
		r.sequence != 0 &&
		r.lineage != 0
}

// MessageLineage is a value capability, not a retained ID string. Proxy replay
// buffers can safely carry it across provider attempts without extending the
// capture session graph or creating memory outside capture admission.
type MessageLineage struct {
	generation    uint64
	traceSequence uint64
	lineage       uint64
}

// Valid reports structural validity only. MessageRead still revalidates the
// generation and trace so a value surviving Stop cannot target a new session.
func (lineage MessageLineage) Valid() bool {
	return lineage.generation != 0 && lineage.traceSequence != 0 && lineage.lineage != 0
}

type ExportRequest struct {
	Scope     ExportScope `json:"scope"`
	RecordIDs []string    `json:"record_ids,omitempty"`
}

type ExportTicket struct {
	ExportID      string    `json:"export_id"`
	SessionID     string    `json:"session_id"`
	RecordCount   int       `json:"record_count"`
	ExpiresAt     time.Time `json:"expires_at"`
	DownloadToken string    `json:"download_token"`
}

// Download is one claimed streaming attempt against a short-lived export. Its
// zero value is invalid. WriteTo owns the active-download slot until it returns;
// the underlying capability can be accepted again until expiry. Close abandons
// an unstarted attempt or cancels an in-flight stream; both are idempotent.
type Download struct {
	manager *Manager
	slot    int
	epoch   uint64
}

func (d Download) Valid() bool {
	return d.manager != nil && d.slot >= 0 && d.epoch != 0
}

func (d Download) WriteTo(ctx context.Context, dst io.Writer) error {
	if !d.Valid() {
		return ErrDownloadUnavailable
	}
	return d.manager.writeDownload(d.slot, d.epoch, ctx, dst)
}

func (d Download) Close() error {
	if !d.Valid() {
		return nil
	}
	d.manager.closeDownload(d.slot, d.epoch)
	return nil
}

func normalizeAttempt(attempt *AttemptMetadata) {
	if attempt.SelectionMode == "" {
		attempt.SelectionMode = SelectionModeInitial
	}
	if attempt.SelectionSource == "" {
		attempt.SelectionSource = SelectionSourceStrategy
	}
	if attempt.CredentialPhase == "" {
		attempt.CredentialPhase = CredentialPhaseInitial
	}
	if attempt.ProviderAttemptIndex < 0 {
		attempt.ProviderAttemptIndex = 0
	}
}

func requestMetadata(raw RawRequest) redaction.RequestMetadata {
	return redaction.RequestMetadata{
		Method:             raw.Method,
		Headers:            raw.Headers,
		ContentLength:      raw.ContentLength,
		Trailers:           raw.Trailers,
		SensitiveHeaders:   raw.SensitiveHeaders,
		CredentialEvidence: raw.CredentialEvidence,
	}
}

func responseMetadata(raw HTTPResponseHead) redaction.HTTPResponseMetadata {
	return redaction.HTTPResponseMetadata{
		StatusCode:         raw.StatusCode,
		Protocol:           raw.Protocol,
		Headers:            raw.Headers,
		ContentLength:      raw.ContentLength,
		DeclaredTrailers:   raw.DeclaredTrailers,
		SensitiveHeaders:   raw.SensitiveHeaders,
		CredentialEvidence: raw.CredentialEvidence,
	}
}

func handshakeMetadata(raw WebSocketHandshake) redaction.WebSocketHandshakeMetadata {
	return redaction.WebSocketHandshakeMetadata{
		StatusCode:         raw.StatusCode,
		Protocol:           raw.Protocol,
		Headers:            raw.Headers,
		SensitiveHeaders:   raw.SensitiveHeaders,
		CredentialEvidence: raw.CredentialEvidence,
	}
}

func borrowedTransitionTarget(input TransitionTargetInput) redaction.Target {
	switch input.protocol {
	case "":
		if input.httpURL == nil && input.webSocketURL == "" {
			return redaction.Target{}
		}
	case ProtocolHTTP:
		if input.httpURL != nil && input.webSocketURL == "" {
			return redaction.BorrowedHTTPTarget(input.httpURL)
		}
	case ProtocolWebSocket:
		if input.httpURL == nil {
			return redaction.BorrowedWebSocketTarget(input.webSocketURL)
		}
	}
	return redaction.InvalidTarget()
}

func (s *sessionState) accepts(bound *sessionState, generation uint64) bool {
	return s != nil && bound == s && s.manager.active.Load() == s && s.generation == generation
}

func canonicalSelectionMode(value SelectionMode) (SelectionMode, bool) {
	return capturevalue.CanonicalSelectionMode(value)
}

func canonicalMessageDirection(value MessageDirection) (MessageDirection, bool) {
	return capturevalue.CanonicalMessageDirection(value)
}

func canonicalMessageType(value MessageType) (MessageType, bool) {
	return capturevalue.CanonicalMessageType(value)
}

func canonicalMessageSource(value MessageSource) (MessageSource, bool) {
	return capturevalue.CanonicalMessageSource(value)
}

func canonicalMessageDisposition(value MessageDisposition) (MessageDisposition, bool) {
	return capturevalue.CanonicalMessageDisposition(value)
}

func canonicalTerminationReason(value TerminationReason) (TerminationReason, bool) {
	return capturevalue.CanonicalTerminationReason(value)
}

func canonicalSourceCompletion(value SourceCompletion) (SourceCompletion, bool) {
	return capturevalue.CanonicalSourceCompletion(value)
}

func retainedTerminationReason(value TerminationReason) redaction.TextSanitization {
	if canonical, ok := canonicalTerminationReason(value); ok {
		return redaction.TextSanitization{Value: string(canonical)}
	}
	return redaction.TextSanitization{Value: string(TerminationReasonUnknown), Truncated: true}
}

func retainedSourceCompletion(value SourceCompletion) redaction.TextSanitization {
	if canonical, ok := canonicalSourceCompletion(value); ok {
		return redaction.TextSanitization{Value: string(canonical)}
	}
	return redaction.TextSanitization{Value: string(SourceCompletionUnknown), Truncated: true}
}
