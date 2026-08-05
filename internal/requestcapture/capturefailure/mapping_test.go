package capturefailure

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"reflect"
	"strings"
	"syscall"
	"testing"

	"github.com/doraemonkeys/switch-a/internal/requestcapture"

	"github.com/coder/websocket"
)

func TestHTTPFetchMapsStableTransportFacts(t *testing.T) {
	t.Parallel()

	plainFailure := errors.New("opaque")
	tests := []struct {
		name        string
		contextErr  error
		err         error
		wantReason  requestcapture.TerminationReason
		wantClass   requestcapture.FailureClass
		wantCode    requestcapture.FailureCode
		wantPresent bool
	}{
		{name: "success", wantReason: requestcapture.TerminationReasonEOF},
		{
			name:        "opaque round trip",
			err:         plainFailure,
			wantReason:  requestcapture.TerminationReasonTransportError,
			wantClass:   requestcapture.FailureClassTransport,
			wantCode:    requestcapture.FailureCodeRoundTrip,
			wantPresent: true,
		},
		{
			name:        "connection wrapper",
			err:         &net.OpError{Err: plainFailure},
			wantReason:  requestcapture.TerminationReasonTransportError,
			wantClass:   requestcapture.FailureClassTransport,
			wantCode:    requestcapture.FailureCodeConnection,
			wantPresent: true,
		},
		{
			name:        "DNS timeout",
			err:         &net.DNSError{IsTimeout: true},
			wantReason:  requestcapture.TerminationReasonTimeout,
			wantClass:   requestcapture.FailureClassTimeout,
			wantCode:    requestcapture.FailureCodeDNS,
			wantPresent: true,
		},
		{
			name:        "request context canceled",
			contextErr:  context.Canceled,
			err:         plainFailure,
			wantReason:  requestcapture.TerminationReasonClientDisconnect,
			wantClass:   requestcapture.FailureClassCanceled,
			wantCode:    requestcapture.FailureCodeRoundTrip,
			wantPresent: true,
		},
		{
			name:        "direct cancellation",
			err:         context.Canceled,
			wantReason:  requestcapture.TerminationReasonCanceled,
			wantClass:   requestcapture.FailureClassCanceled,
			wantCode:    requestcapture.FailureCodeRoundTrip,
			wantPresent: true,
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			reason, observation := HTTPFetch(test.contextErr, test.err)
			if reason != test.wantReason {
				t.Fatalf("reason = %q, want %q", reason, test.wantReason)
			}
			if !test.wantPresent {
				if observation != (requestcapture.FailureObservation{}) {
					t.Fatalf("success observation = %#v", observation)
				}
				return
			}
			fact := observation.Primary
			if fact.Site != requestcapture.FailureSiteTransport ||
				fact.Peer != requestcapture.FailurePeerUpstream ||
				fact.Class != test.wantClass || fact.Code != test.wantCode ||
				fact.Message != "" {
				t.Fatalf("fact = %#v", fact)
			}
		})
	}
}

func TestHTTPPreparationAndForwardMapControlFlowOrigin(t *testing.T) {
	t.Parallel()

	opaque := errors.New("opaque")
	preparationTests := []struct {
		name       string
		contextErr error
		code       requestcapture.FailureCode
		wantReason requestcapture.TerminationReason
		wantPeer   requestcapture.FailurePeer
		wantClass  requestcapture.FailureClass
	}{
		{
			name:       "provider configuration",
			code:       requestcapture.FailureCodeMissingAPIKey,
			wantReason: requestcapture.TerminationReasonPreparationError,
			wantPeer:   requestcapture.FailurePeerProvider,
			wantClass:  requestcapture.FailureClassConfiguration,
		},
		{
			name:       "gateway request construction",
			code:       requestcapture.FailureCodeRequestBuild,
			wantReason: requestcapture.TerminationReasonPreparationError,
			wantPeer:   requestcapture.FailurePeerGateway,
			wantClass:  requestcapture.FailureClassConfiguration,
		},
		{
			name:       "deadline overrides class",
			contextErr: context.DeadlineExceeded,
			code:       requestcapture.FailureCodeCredentialApply,
			wantReason: requestcapture.TerminationReasonTimeout,
			wantPeer:   requestcapture.FailurePeerProvider,
			wantClass:  requestcapture.FailureClassTimeout,
		},
	}
	for _, test := range preparationTests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			reason, observation := HTTPPreparation(test.contextErr, opaque, test.code)
			fact := observation.Primary
			if reason != test.wantReason || fact.Site != requestcapture.FailureSitePreparation ||
				fact.Peer != test.wantPeer || fact.Class != test.wantClass || fact.Code != test.code {
				t.Fatalf("reason/fact = %q %#v", reason, fact)
			}
		})
	}

	forwardTests := []struct {
		name       string
		contextErr error
		err        error
		origin     HTTPForwardOrigin
		wantReason requestcapture.TerminationReason
		wantSite   requestcapture.FailureSite
		wantPeer   requestcapture.FailurePeer
		wantClass  requestcapture.FailureClass
		wantCode   requestcapture.FailureCode
	}{
		{
			name:       "upstream read",
			err:        opaque,
			origin:     HTTPForwardOriginUpstreamRead,
			wantReason: requestcapture.TerminationReasonReadError,
			wantSite:   requestcapture.FailureSiteResponseRead,
			wantPeer:   requestcapture.FailurePeerUpstream,
			wantClass:  requestcapture.FailureClassRead,
			wantCode:   requestcapture.FailureCodeUpstreamRead,
		},
		{
			name:       "bounded read timeout",
			err:        opaque,
			origin:     HTTPForwardOriginReadTimeout,
			wantReason: requestcapture.TerminationReasonTimeout,
			wantSite:   requestcapture.FailureSiteResponseRead,
			wantPeer:   requestcapture.FailurePeerUpstream,
			wantClass:  requestcapture.FailureClassTimeout,
			wantCode:   requestcapture.FailureCodeUpstreamRead,
		},
		{
			name:       "client write",
			err:        opaque,
			origin:     HTTPForwardOriginClientWrite,
			wantReason: requestcapture.TerminationReasonWriteError,
			wantSite:   requestcapture.FailureSiteResponseWrite,
			wantPeer:   requestcapture.FailurePeerClient,
			wantClass:  requestcapture.FailureClassWrite,
			wantCode:   requestcapture.FailureCodeClientWrite,
		},
		{
			name:       "request cancellation wins",
			contextErr: context.Canceled,
			err:        opaque,
			origin:     HTTPForwardOriginClientWrite,
			wantReason: requestcapture.TerminationReasonClientDisconnect,
			wantSite:   requestcapture.FailureSiteResponseWrite,
			wantPeer:   requestcapture.FailurePeerClient,
			wantClass:  requestcapture.FailureClassCanceled,
			wantCode:   requestcapture.FailureCodeClientWrite,
		},
		{
			name:       "client cancel with request context",
			contextErr: context.Canceled,
			err:        opaque,
			origin:     HTTPForwardOriginClientCancel,
			wantReason: requestcapture.TerminationReasonClientDisconnect,
			wantSite:   requestcapture.FailureSiteResponseRead,
			wantPeer:   requestcapture.FailurePeerClient,
			wantClass:  requestcapture.FailureClassCanceled,
			wantCode:   requestcapture.FailureCodeClientCancel,
		},
		{
			name:       "client cancel without context error",
			err:        opaque,
			origin:     HTTPForwardOriginClientCancel,
			wantReason: requestcapture.TerminationReasonCanceled,
			wantSite:   requestcapture.FailureSiteResponseRead,
			wantPeer:   requestcapture.FailurePeerClient,
			wantClass:  requestcapture.FailureClassCanceled,
			wantCode:   requestcapture.FailureCodeClientCancel,
		},
	}
	for _, test := range forwardTests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			reason, observation := HTTPForward(test.contextErr, test.err, test.origin)
			fact := observation.Primary
			if reason != test.wantReason || fact.Site != test.wantSite ||
				fact.Peer != test.wantPeer || fact.Class != test.wantClass || fact.Code != test.wantCode {
				t.Fatalf("reason/fact = %q %#v", reason, fact)
			}
		})
	}
	if reason, observation := HTTPForward(nil, nil, HTTPForwardOriginClientWrite); reason != requestcapture.TerminationReasonEOF || observation != (requestcapture.FailureObservation{}) {
		t.Fatalf("nil forward = %q %#v", reason, observation)
	}
}

func TestWebSocketMappingsPreservePrimaryAndSecondaryFacts(t *testing.T) {
	t.Parallel()

	opaque := errors.New("opaque")
	reason, preparation := WebSocketPreparation(nil, opaque, requestcapture.FailureCodeMissingCredentials)
	if reason != requestcapture.TerminationReasonPreparationError ||
		preparation.Primary.Site != requestcapture.FailureSitePreparation ||
		preparation.Primary.Peer != requestcapture.FailurePeerProvider ||
		preparation.Primary.Code != requestcapture.FailureCodeMissingCredentials {
		t.Fatalf("preparation = %q %#v", reason, preparation)
	}

	rejected := WebSocketHandshake(http.StatusForbidden, opaque, syscall.Errno(5))
	if rejected.Primary.Site != requestcapture.FailureSiteWebSocketHandshake ||
		rejected.Primary.Class != requestcapture.FailureClassHTTPStatus ||
		rejected.Primary.Code != requestcapture.FailureCodeHandshakeRejected ||
		rejected.Primary.HTTPStatusCode != http.StatusForbidden ||
		!rejected.HasSecondary ||
		rejected.Secondary.Code != requestcapture.FailureCodeFailureBodyRead ||
		rejected.Secondary.SystemErrorCode != 5 {
		t.Fatalf("rejected handshake = %#v", rejected)
	}

	upgrade := WebSocketHandshake(http.StatusSwitchingProtocols, opaque, nil)
	if upgrade.Primary.Class != requestcapture.FailureClassProtocol ||
		upgrade.Primary.Code != requestcapture.FailureCodeWebSocketUpgrade {
		t.Fatalf("upgrade failure = %#v", upgrade)
	}

	dial := WebSocketHandshake(0, &net.OpError{Err: opaque}, nil)
	if dial.Primary.Class != requestcapture.FailureClassTransport ||
		dial.Primary.Code != requestcapture.FailureCodeConnection {
		t.Fatalf("dial failure = %#v", dial)
	}

	bodyOnly := WebSocketHandshake(http.StatusSwitchingProtocols, nil, opaque)
	if bodyOnly.Primary.Code != requestcapture.FailureCodeFailureBodyRead || bodyOnly.HasSecondary {
		t.Fatalf("body-only handshake = %#v", bodyOnly)
	}
	if none := WebSocketHandshake(http.StatusSwitchingProtocols, nil, nil); none != (requestcapture.FailureObservation{}) {
		t.Fatalf("empty handshake = %#v", none)
	}
}

func TestWebSocketTerminalMappingsUseDestinationPeer(t *testing.T) {
	t.Parallel()

	opaque := errors.New("opaque")
	reason, accepted := WebSocketClientAccept(context.Canceled, opaque)
	if reason != requestcapture.TerminationReasonClientDisconnect ||
		accepted.Primary.Site != requestcapture.FailureSiteWebSocketUpgrade ||
		accepted.Primary.Peer != requestcapture.FailurePeerClient ||
		accepted.Primary.Class != requestcapture.FailureClassCanceled ||
		accepted.Primary.Code != requestcapture.FailureCodeClientAccept {
		t.Fatalf("client accept = %q %#v", reason, accepted)
	}

	reason, replay := WebSocketReplayWrite(nil, opaque)
	if reason != requestcapture.TerminationReasonWriteError ||
		replay.Primary.Site != requestcapture.FailureSiteWebSocketReplay ||
		replay.Primary.Peer != requestcapture.FailurePeerUpstream ||
		replay.Primary.Code != requestcapture.FailureCodeReplayWrite {
		t.Fatalf("replay = %q %#v", reason, replay)
	}

	for _, peer := range []requestcapture.FailurePeer{
		requestcapture.FailurePeerClient,
		requestcapture.FailurePeerUpstream,
	} {
		observation := WebSocketMessageWrite(peer, opaque)
		if observation.Primary.Site != requestcapture.FailureSiteWebSocketMessage ||
			observation.Primary.Peer != peer ||
			observation.Primary.Class != requestcapture.FailureClassWrite ||
			observation.Primary.Code != requestcapture.FailureCodeMessageWrite {
			t.Fatalf("message write to %q = %#v", peer, observation)
		}
	}
}

func TestFailureValueHelpersBoundAndCanonicalizeMetadata(t *testing.T) {
	t.Parallel()

	if got := FromError("", "", "", "", nil); got != (requestcapture.FailureFact{}) {
		t.Fatalf("nil error fact = %#v", got)
	}
	if class, ok := ContextClass(nil); ok || class != "" {
		t.Fatalf("nil context class = %q, %t", class, ok)
	}
	if class, ok := ContextClass(errors.New("opaque")); ok || class != "" {
		t.Fatalf("opaque context class = %q, %t", class, ok)
	}
	if !IsEOF(io.EOF) || IsEOF(nil) || IsEOF(errors.New("EOF")) || IsEOF(sliceError("opaque")) {
		t.Fatal("exact EOF recognition accepted a non-sentinel or rejected io.EOF")
	}
	if !IsUnexpectedEOF(io.ErrUnexpectedEOF) || IsUnexpectedEOF(nil) ||
		IsUnexpectedEOF(errors.New("unexpected EOF")) || IsUnexpectedEOF(sliceError("opaque")) {
		t.Fatal("exact unexpected EOF recognition accepted a non-sentinel or rejected io.ErrUnexpectedEOF")
	}
	if fact, truncated := WebSocketClose("", "", nil); fact != (requestcapture.FailureFact{}) || truncated {
		t.Fatalf("nil close = %#v, %t", fact, truncated)
	}

	status := HTTPStatus(
		requestcapture.FailureSiteResponseStatus,
		requestcapture.FailurePeerUpstream,
		http.StatusTooManyRequests,
	)
	if status.Class != requestcapture.FailureClassHTTPStatus ||
		status.Code != requestcapture.FailureCodeUnexpectedStatus ||
		status.HTTPStatusCode != http.StatusTooManyRequests {
		t.Fatalf("status fact = %#v", status)
	}
	if got := HTTPStatus("invalid", "invalid", -1); got.Site != requestcapture.FailureSiteUnknown ||
		got.Peer != requestcapture.FailurePeerUnknown || got.HTTPStatusCode != 0 {
		t.Fatalf("canonical status = %#v", got)
	}
	if got := WithHTTPStatus(status, -1); got.HTTPStatusCode != status.HTTPStatusCode {
		t.Fatalf("negative status changed fact = %#v", got)
	}
	if got := WithHTTPStatus(requestcapture.FailureFact{}, http.StatusBadGateway); got.HTTPStatusCode != http.StatusBadGateway {
		t.Fatalf("status enrichment = %#v", got)
	}

	message := strings.Repeat("界", maxProviderProtocolMessageBytes)
	semantic, truncated := ProviderSemantic(
		requestcapture.FailureSiteWebSocketMessage,
		requestcapture.FailurePeerProvider,
		http.StatusBadRequest,
		strings.Repeat("provider-type-", maxProviderDiagnosticIdentifierBytes),
		strings.Repeat("provider-code-", maxProviderDiagnosticIdentifierBytes),
		message,
	)
	if !truncated || len(semantic.Message) > maxProviderProtocolMessageBytes ||
		!strings.HasPrefix(message, semantic.Message) || !strings.HasSuffix(semantic.Message, "界") ||
		len(semantic.ProviderErrorType) > maxProviderDiagnosticIdentifierBytes ||
		len(semantic.ProviderErrorCode) > maxProviderDiagnosticIdentifierBytes ||
		semantic.HTTPStatusCode != http.StatusBadRequest {
		t.Fatalf("semantic fact = %#v, truncated=%t", semantic, truncated)
	}
	exactSemantic, truncated := ProviderSemantic(
		requestcapture.FailureSiteWebSocketMessage,
		requestcapture.FailurePeerProvider,
		0,
		"model_error",
		"model_not_allowed",
		"safe",
	)
	if truncated || exactSemantic.ProviderErrorType != "model_error" ||
		exactSemantic.ProviderErrorCode != "model_not_allowed" ||
		exactSemantic.Message != "safe" || exactSemantic.HTTPStatusCode != 0 {
		t.Fatalf("exact semantic = %#v, truncated=%t", exactSemantic, truncated)
	}

	closeFact, truncated := WebSocketClose(
		requestcapture.FailureSiteWebSocketClose,
		requestcapture.FailurePeerClient,
		&websocket.CloseError{Code: websocket.StatusPolicyViolation, Reason: "safe"},
	)
	if truncated || closeFact.Message != "safe" ||
		closeFact.WebSocketCloseCode != int(websocket.StatusPolicyViolation) {
		t.Fatalf("exact close = %#v, truncated=%t", closeFact, truncated)
	}
}

func TestFromErrorHandlesTypedNilAndAllowedWrapperFields(t *testing.T) {
	t.Parallel()

	typedNils := []error{
		(*url.Error)(nil),
		(*net.OpError)(nil),
		(*net.DNSError)(nil),
		(*os.SyscallError)(nil),
		(*os.PathError)(nil),
	}
	for _, err := range typedNils {
		fact := FromError(
			requestcapture.FailureSiteTransport,
			requestcapture.FailurePeerUpstream,
			requestcapture.FailureClassTransport,
			requestcapture.FailureCodeRoundTrip,
			err,
		)
		if fact.Site != requestcapture.FailureSiteTransport || fact.Message != "" {
			t.Fatalf("typed-nil %T fact = %#v", err, fact)
		}
	}

	pathFact := FromError(
		requestcapture.FailureSiteTransport,
		requestcapture.FailurePeerUpstream,
		requestcapture.FailureClassTransport,
		requestcapture.FailureCodeUnknown,
		&os.PathError{Err: syscall.Errno(13)},
	)
	if pathFact.SystemErrorCode != 13 || pathFact.Code != requestcapture.FailureCodeUnknown {
		t.Fatalf("path fact = %#v", pathFact)
	}
	unchangedCode := FromError(
		requestcapture.FailureSiteResponseRead,
		requestcapture.FailurePeerUpstream,
		requestcapture.FailureClassRead,
		requestcapture.FailureCodeUpstreamRead,
		&net.OpError{Err: syscall.Errno(9)},
	)
	if unchangedCode.Code != requestcapture.FailureCodeUpstreamRead || unchangedCode.SystemErrorCode != 9 {
		t.Fatalf("non-dial op fact = %#v", unchangedCode)
	}
}

func TestObservationPreservesStableOrdering(t *testing.T) {
	t.Parallel()

	primary := Fact(
		requestcapture.FailureSiteResponseStatus,
		requestcapture.FailurePeerUpstream,
		requestcapture.FailureClassHTTPStatus,
		requestcapture.FailureCodeUnexpectedStatus,
	)
	secondary := Fact(
		requestcapture.FailureSiteResponseDrain,
		requestcapture.FailurePeerUpstream,
		requestcapture.FailureClassRead,
		requestcapture.FailureCodeDrainRead,
	)
	observation := Observation(primary, secondary)
	if observation.Primary != primary || !observation.HasSecondary || observation.Secondary != secondary {
		t.Fatalf("ordered observation = %#v", observation)
	}
	if emptyObservation := Observation(requestcapture.FailureFact{}, requestcapture.FailureFact{}); emptyObservation != (requestcapture.FailureObservation{}) {
		t.Fatalf("empty observation = %#v", emptyObservation)
	}
}

func TestCaptureFailureBoundaryContainsOnlyBoundedValueFields(t *testing.T) {
	t.Parallel()

	errorType := reflect.TypeFor[error]()
	stringerType := reflect.TypeFor[fmt.Stringer]()
	for _, boundary := range []reflect.Type{
		reflect.TypeFor[requestcapture.FailureFact](),
		reflect.TypeFor[requestcapture.FailureObservation](),
		reflect.TypeFor[requestcapture.CredentialEvidence](),
		reflect.TypeFor[requestcapture.SensitiveHeaderEvidence](),
	} {
		assertBoundedValueType(t, boundary, errorType, stringerType, map[reflect.Type]bool{})
	}
}

func assertBoundedValueType(
	t *testing.T,
	typeOfValue reflect.Type,
	errorType reflect.Type,
	stringerType reflect.Type,
	visiting map[reflect.Type]bool,
) {
	t.Helper()
	if typeOfValue.Implements(errorType) || typeOfValue.Implements(stringerType) {
		t.Fatalf("capture boundary type %v exposes executable string conversion", typeOfValue)
	}
	switch typeOfValue.Kind() {
	case reflect.Interface, reflect.Map, reflect.Slice, reflect.Func, reflect.Chan,
		reflect.Pointer, reflect.UnsafePointer:
		t.Fatalf("capture boundary type %v contains unbounded or executable kind %v", typeOfValue, typeOfValue.Kind())
	case reflect.Array:
		assertBoundedValueType(t, typeOfValue.Elem(), errorType, stringerType, visiting)
	case reflect.Struct:
		if visiting[typeOfValue] {
			return
		}
		visiting[typeOfValue] = true
		defer delete(visiting, typeOfValue)
		for fieldIndex := 0; fieldIndex < typeOfValue.NumField(); fieldIndex++ {
			assertBoundedValueType(t, typeOfValue.Field(fieldIndex).Type, errorType, stringerType, visiting)
		}
	}
}
