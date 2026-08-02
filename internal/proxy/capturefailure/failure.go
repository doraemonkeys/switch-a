package capturefailure

import (
	"context"
	"io"
	"net"
	"net/url"
	"os"
	"reflect"
	"syscall"
	"unicode/utf8"

	"github.com/doraemonkeys/switch-a/internal/requestcapture"

	"github.com/coder/websocket"
)

const (
	// Standard transport wrappers are shallow. A fixed traversal budget keeps the
	// capture boundary bounded even if an allowlisted wrapper graph is cyclic.
	maxConcreteWrapperDepth = 4
	// A WebSocket close control frame has at most 123 payload bytes after its
	// two-byte status code. Treating that protocol limit as the capture limit means
	// an injected CloseError cannot smuggle an arbitrarily large retained string.
	maxWebSocketCloseReasonBytes = 123
	// Provider error envelopes are application protocol metadata rather than an
	// arbitrary Go error string. Bound them before ownership crosses into capture.
	maxProviderDiagnosticIdentifierBytes = 128
	maxProviderProtocolMessageBytes      = 512
)

// IsEOF recognizes only the standard sentinel without invoking custom Is or
// Unwrap hooks. Upstream readers are allowed to return an uncomparable dynamic
// error, so reflective comparability is checked before exact equality.
func IsEOF(err error) bool {
	return isExactError(err, io.EOF)
}

// IsUnexpectedEOF is the bounded counterpart used when observing io.ReadFull.
// It intentionally recognizes only the standard sentinel and never custom hooks.
func IsUnexpectedEOF(err error) bool {
	return isExactError(err, io.ErrUnexpectedEOF)
}

func isExactError(err error, target error) bool {
	if err == nil {
		return false
	}
	errValue := reflect.ValueOf(err)
	return errValue.IsValid() && errValue.Comparable() && errValue.Equal(reflect.ValueOf(target))
}

// Fact records a failure already established by control flow. The capture domain
// requires explicit unknown values so stored observations never depend on Go zero
// values acquiring accidental meaning.
func Fact(
	site requestcapture.FailureSite,
	peer requestcapture.FailurePeer,
	class requestcapture.FailureClass,
	code requestcapture.FailureCode,
) requestcapture.FailureFact {
	return requestcapture.FailureFact{
		Site:  canonicalSite(site),
		Peer:  canonicalPeer(peer),
		Class: canonicalClass(class),
		Code:  canonicalCode(code),
	}
}

// FromError enriches a caller-owned control-flow fact with fields from a small
// concrete-type allowlist. It deliberately never calls Error, String, Is, As,
// Unwrap, Timeout, or Temporary: capture must remain bounded when an upstream or
// downstream implementation returns a hostile error value.
func FromError(
	site requestcapture.FailureSite,
	peer requestcapture.FailurePeer,
	class requestcapture.FailureClass,
	code requestcapture.FailureCode,
	err error,
) requestcapture.FailureFact {
	if err == nil {
		return requestcapture.FailureFact{}
	}

	fact := Fact(site, peer, class, code)
	current := err
	for depth := 0; depth < maxConcreteWrapperDepth && current != nil; depth++ {
		if contextClass, ok := ContextClass(current); ok {
			fact.Class = contextClass
			return fact
		}

		// The any conversion is deliberate: this is an exact concrete allowlist,
		// not error-chain matching. errors.As would invoke attacker-controlled
		// As/Unwrap hooks at the capture boundary.
		switch concrete := any(current).(type) {
		case syscall.Errno:
			fact.SystemErrorCode = int64(concrete)
			return fact
		case *url.Error:
			if concrete == nil {
				return fact
			}
			current = concrete.Err
		case *net.OpError:
			if concrete == nil {
				return fact
			}
			if fact.Code == requestcapture.FailureCodeRoundTrip ||
				fact.Code == requestcapture.FailureCodeWebSocketDial ||
				fact.Code == requestcapture.FailureCodeUnknown {
				fact.Code = requestcapture.FailureCodeConnection
			}
			current = concrete.Err
		case *net.DNSError:
			if concrete == nil {
				return fact
			}
			if concrete.IsTimeout {
				fact.Class = requestcapture.FailureClassTimeout
			}
			fact.Code = requestcapture.FailureCodeDNS
			return fact
		case *os.SyscallError:
			if concrete == nil {
				return fact
			}
			current = concrete.Err
		case *os.PathError:
			if concrete == nil {
				return fact
			}
			current = concrete.Err
		default:
			return fact
		}
	}
	return fact
}

// ContextClass recognizes only the two standard context sentinels. The
// shared exact matcher safely rejects slice-backed dynamic errors without
// invoking custom matching hooks.
func ContextClass(err error) (requestcapture.FailureClass, bool) {
	if err == nil {
		return "", false
	}
	if isExactError(err, context.DeadlineExceeded) {
		return requestcapture.FailureClassTimeout, true
	}
	if isExactError(err, context.Canceled) {
		return requestcapture.FailureClassCanceled, true
	}
	return "", false
}

func HTTPStatus(
	site requestcapture.FailureSite,
	peer requestcapture.FailurePeer,
	statusCode int,
) requestcapture.FailureFact {
	fact := Fact(
		site,
		peer,
		requestcapture.FailureClassHTTPStatus,
		requestcapture.FailureCodeUnexpectedStatus,
	)
	if statusCode > 0 {
		fact.HTTPStatusCode = statusCode
	}
	return fact
}

func WithHTTPStatus(fact requestcapture.FailureFact, statusCode int) requestcapture.FailureFact {
	if statusCode > 0 {
		fact.HTTPStatusCode = statusCode
	}
	return fact
}

func WebSocketClose(
	site requestcapture.FailureSite,
	peer requestcapture.FailurePeer,
	closeError *websocket.CloseError,
) (requestcapture.FailureFact, bool) {
	if closeError == nil {
		return requestcapture.FailureFact{}, false
	}
	fact := Fact(
		site,
		peer,
		requestcapture.FailureClassWebSocketClose,
		requestcapture.FailureCodeWebSocketClose,
	)
	applyWebSocketCloseCode(&fact, closeError.Code)
	message, truncated := boundedUTF8Prefix(closeError.Reason, maxWebSocketCloseReasonBytes)
	fact.Message = message
	return fact, truncated
}

func ProviderSemantic(
	site requestcapture.FailureSite,
	peer requestcapture.FailurePeer,
	statusCode int,
	providerErrorType string,
	providerErrorCode string,
	message string,
) (requestcapture.FailureFact, bool) {
	fact := Fact(
		site,
		peer,
		requestcapture.FailureClassUpstreamSemantic,
		requestcapture.FailureCodeProviderSemantic,
	)
	if statusCode > 0 {
		fact.HTTPStatusCode = statusCode
	}
	var truncated bool
	fact.ProviderErrorType, truncated = boundedUTF8Prefix(
		providerErrorType,
		maxProviderDiagnosticIdentifierBytes,
	)
	var codeTruncated bool
	fact.ProviderErrorCode, codeTruncated = boundedUTF8Prefix(
		providerErrorCode,
		maxProviderDiagnosticIdentifierBytes,
	)
	var messageTruncated bool
	boundedMessage, messageTruncated := boundedUTF8Prefix(message, maxProviderProtocolMessageBytes)
	fact.Message = boundedMessage
	return fact, truncated || codeTruncated || messageTruncated
}

// Observation promotes a lone secondary fact so callers can independently
// construct physical failure facts without manufacturing an empty primary.
func Observation(
	primary requestcapture.FailureFact,
	secondary requestcapture.FailureFact,
) requestcapture.FailureObservation {
	if empty(primary) {
		primary, secondary = secondary, requestcapture.FailureFact{}
	}
	observation := requestcapture.FailureObservation{Primary: primary}
	if !empty(secondary) {
		observation.Secondary = secondary
		observation.HasSecondary = true
	}
	return observation
}

func applyWebSocketCloseCode(fact *requestcapture.FailureFact, code websocket.StatusCode) {
	fact.Class = requestcapture.FailureClassWebSocketClose
	fact.Code = requestcapture.FailureCodeWebSocketClose
	fact.WebSocketCloseCode = int(code)
}

func boundedUTF8Prefix(value string, limit int) (string, bool) {
	if len(value) <= limit {
		return value, false
	}
	end := limit
	for end > 0 && !utf8.ValidString(value[:end]) {
		end--
	}
	return value[:end], true
}

func empty(fact requestcapture.FailureFact) bool {
	return fact.Site == "" &&
		fact.Peer == "" &&
		fact.Class == "" &&
		fact.Code == "" &&
		fact.HTTPStatusCode == 0 &&
		fact.WebSocketCloseCode == 0 &&
		fact.SystemErrorCode == 0 &&
		fact.ProviderErrorType == "" &&
		fact.ProviderErrorCode == "" &&
		fact.Message == ""
}

func canonicalSite(site requestcapture.FailureSite) requestcapture.FailureSite {
	switch site {
	case requestcapture.FailureSiteUnknown,
		requestcapture.FailureSiteGateway,
		requestcapture.FailureSitePreparation,
		requestcapture.FailureSiteTransport,
		requestcapture.FailureSiteResponseStatus,
		requestcapture.FailureSiteResponseDrain,
		requestcapture.FailureSiteResponseRead,
		requestcapture.FailureSiteResponseWrite,
		requestcapture.FailureSiteWebSocketHandshake,
		requestcapture.FailureSiteWebSocketUpgrade,
		requestcapture.FailureSiteWebSocketReplay,
		requestcapture.FailureSiteWebSocketRelay,
		requestcapture.FailureSiteWebSocketMessage,
		requestcapture.FailureSiteWebSocketClose:
		return site
	default:
		return requestcapture.FailureSiteUnknown
	}
}

func canonicalPeer(peer requestcapture.FailurePeer) requestcapture.FailurePeer {
	switch peer {
	case requestcapture.FailurePeerUnknown,
		requestcapture.FailurePeerGateway,
		requestcapture.FailurePeerClient,
		requestcapture.FailurePeerUpstream,
		requestcapture.FailurePeerProvider:
		return peer
	default:
		return requestcapture.FailurePeerUnknown
	}
}

func canonicalClass(class requestcapture.FailureClass) requestcapture.FailureClass {
	switch class {
	case requestcapture.FailureClassUnknown,
		requestcapture.FailureClassTimeout,
		requestcapture.FailureClassCanceled,
		requestcapture.FailureClassConfiguration,
		requestcapture.FailureClassTransport,
		requestcapture.FailureClassHTTPStatus,
		requestcapture.FailureClassRead,
		requestcapture.FailureClassWrite,
		requestcapture.FailureClassProtocol,
		requestcapture.FailureClassWebSocketClose,
		requestcapture.FailureClassUpstreamSemantic:
		return class
	default:
		return requestcapture.FailureClassUnknown
	}
}

func canonicalCode(code requestcapture.FailureCode) requestcapture.FailureCode {
	switch code {
	case requestcapture.FailureCodeUnknown,
		requestcapture.FailureCodeMissingBaseURL,
		requestcapture.FailureCodeMissingAPIKey,
		requestcapture.FailureCodeMissingCredentials,
		requestcapture.FailureCodeRequestBuild,
		requestcapture.FailureCodeCredentialApply,
		requestcapture.FailureCodeGatewayContext,
		requestcapture.FailureCodeDNS,
		requestcapture.FailureCodeConnection,
		requestcapture.FailureCodeRoundTrip,
		requestcapture.FailureCodeUnexpectedStatus,
		requestcapture.FailureCodeFailureBodyRead,
		requestcapture.FailureCodeDrainRead,
		requestcapture.FailureCodeUpstreamRead,
		requestcapture.FailureCodeClientWrite,
		requestcapture.FailureCodeClientAccept,
		requestcapture.FailureCodeWebSocketDial,
		requestcapture.FailureCodeHandshakeRejected,
		requestcapture.FailureCodeWebSocketUpgrade,
		requestcapture.FailureCodeReplayWrite,
		requestcapture.FailureCodeRelayRead,
		requestcapture.FailureCodeRelayWrite,
		requestcapture.FailureCodeMessageRead,
		requestcapture.FailureCodeMessageWrite,
		requestcapture.FailureCodeProtocolViolation,
		requestcapture.FailureCodeWebSocketClose,
		requestcapture.FailureCodeProviderSemantic:
		return code
	default:
		return requestcapture.FailureCodeUnknown
	}
}
