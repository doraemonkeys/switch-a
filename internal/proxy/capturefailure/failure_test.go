package capturefailure

import (
	"context"
	"net"
	"net/url"
	"os"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/doraemonkeys/switch-a/internal/requestcapture"

	"github.com/coder/websocket"
)

type hostileError struct {
	errorCalls   atomic.Int32
	asCalls      atomic.Int32
	unwrapCalls  atomic.Int32
	isCalls      atomic.Int32
	timeoutCalls atomic.Int32
	stringCalls  atomic.Int32
	block        <-chan struct{}
}

const (
	hostileMethodAllocationBytes = 1024 * 1024
	hostileMethodWait            = 250 * time.Millisecond
)

func (e *hostileError) Error() string {
	e.errorCalls.Add(1)
	_ = make([]byte, hostileMethodAllocationBytes)
	<-e.block
	panic("hostile Error invoked")
}

func (e *hostileError) As(any) bool {
	e.asCalls.Add(1)
	_ = make([]byte, hostileMethodAllocationBytes)
	<-e.block
	panic("hostile As invoked")
}

func (e *hostileError) Unwrap() error {
	e.unwrapCalls.Add(1)
	_ = make([]byte, hostileMethodAllocationBytes)
	<-e.block
	panic("hostile Unwrap invoked")
}

func (e *hostileError) Is(error) bool {
	e.isCalls.Add(1)
	_ = make([]byte, hostileMethodAllocationBytes)
	<-e.block
	panic("hostile Is invoked")
}

func (e *hostileError) Timeout() bool {
	e.timeoutCalls.Add(1)
	_ = make([]byte, hostileMethodAllocationBytes)
	<-e.block
	panic("hostile Timeout invoked")
}

func (e *hostileError) String() string {
	e.stringCalls.Add(1)
	_ = make([]byte, hostileMethodAllocationBytes)
	<-e.block
	panic("hostile String invoked")
}

func TestFromErrorTreatsHostileImplementationsAsOpaque(t *testing.T) {
	blocked := make(chan struct{})
	hostile := &hostileError{block: blocked}
	type result struct {
		facts []requestcapture.FailureFact
	}
	finished := make(chan result, 1)
	go func() {
		errors := []error{
			hostile,
			&url.Error{Err: hostile},
			&net.OpError{Err: hostile},
			&os.SyscallError{Err: hostile},
		}
		facts := make([]requestcapture.FailureFact, 0, len(errors))
		for _, err := range errors {
			facts = append(facts, FromError(
				requestcapture.FailureSiteTransport,
				requestcapture.FailurePeerUpstream,
				requestcapture.FailureClassTransport,
				requestcapture.FailureCodeRoundTrip,
				err,
			))
		}
		finished <- result{facts: facts}
	}()

	select {
	case got := <-finished:
		want := Fact(
			requestcapture.FailureSiteTransport,
			requestcapture.FailurePeerUpstream,
			requestcapture.FailureClassTransport,
			requestcapture.FailureCodeRoundTrip,
		)
		for _, fact := range got.facts {
			if fact.Site != want.Site || fact.Peer != want.Peer ||
				fact.Class != want.Class || fact.Message != "" {
				t.Fatalf("opaque fact = %#v, want stable site/peer/class without message", fact)
			}
		}
	case <-time.After(hostileMethodWait):
		close(blocked)
		t.Fatal("capture blocked on a hostile error method")
	}

	if calls := hostile.errorCalls.Load(); calls != 0 {
		t.Fatalf("Error calls = %d, want 0", calls)
	}
	if calls := hostile.asCalls.Load(); calls != 0 {
		t.Fatalf("As calls = %d, want 0", calls)
	}
	if calls := hostile.unwrapCalls.Load(); calls != 0 {
		t.Fatalf("Unwrap calls = %d, want 0", calls)
	}
	if calls := hostile.isCalls.Load(); calls != 0 {
		t.Fatalf("Is calls = %d, want 0", calls)
	}
	if calls := hostile.timeoutCalls.Load(); calls != 0 {
		t.Fatalf("Timeout calls = %d, want 0", calls)
	}
	if calls := hostile.stringCalls.Load(); calls != 0 {
		t.Fatalf("String calls = %d, want 0", calls)
	}
}

func TestFromErrorBoundsCyclicAndOverDepthWrappers(t *testing.T) {
	cycle := &url.Error{}
	cycle.Err = cycle
	cyclicFact := FromError(
		requestcapture.FailureSiteTransport,
		requestcapture.FailurePeerUpstream,
		requestcapture.FailureClassTransport,
		requestcapture.FailureCodeRoundTrip,
		cycle,
	)
	if cyclicFact.SystemErrorCode != 0 || cyclicFact.Message != "" {
		t.Fatalf("cyclic fact = %#v", cyclicFact)
	}

	var nested error = syscall.Errno(77)
	for range maxConcreteWrapperDepth + 1 {
		nested = &url.Error{Err: nested}
	}
	overDepthFact := FromError(
		requestcapture.FailureSiteTransport,
		requestcapture.FailurePeerUpstream,
		requestcapture.FailureClassTransport,
		requestcapture.FailureCodeRoundTrip,
		nested,
	)
	if overDepthFact.SystemErrorCode != 0 {
		t.Fatalf("over-depth system code = %d, want absent", overDepthFact.SystemErrorCode)
	}
}

type sliceError []byte

func (sliceError) Error() string { panic("slice Error invoked") }

func TestFromErrorAcceptsUncomparableDynamicError(t *testing.T) {
	var err error = sliceError("opaque")
	want := Fact(
		requestcapture.FailureSitePreparation,
		requestcapture.FailurePeerProvider,
		requestcapture.FailureClassConfiguration,
		requestcapture.FailureCodeCredentialApply,
	)
	if got := FromError(want.Site, want.Peer, want.Class, want.Code, err); got != want {
		t.Fatalf("fact = %#v, want %#v", got, want)
	}
}

func TestFromErrorExtractsOnlyAllowlistedConcreteFields(t *testing.T) {
	const systemCode = syscall.Errno(123)
	err := &url.Error{Err: &net.OpError{Err: &os.SyscallError{Err: systemCode}}}

	got := FromError(
		requestcapture.FailureSiteTransport,
		requestcapture.FailurePeerUpstream,
		requestcapture.FailureClassTransport,
		requestcapture.FailureCodeRoundTrip,
		err,
	)

	if got.SystemErrorCode != int64(systemCode) {
		t.Fatalf("system error code = %d, want %d", got.SystemErrorCode, systemCode)
	}
	if got.Message != "" {
		t.Fatalf("message = %q, want empty", got.Message)
	}
}

func TestFromErrorUsesConcreteDNSAndContextFactsWithoutMethods(t *testing.T) {
	dnsFact := FromError(
		requestcapture.FailureSiteTransport,
		requestcapture.FailurePeerUpstream,
		requestcapture.FailureClassTransport,
		requestcapture.FailureCodeRoundTrip,
		&net.DNSError{IsTimeout: true},
	)
	if dnsFact.Class != requestcapture.FailureClassTimeout {
		t.Fatalf("DNS class = %q, want timeout", dnsFact.Class)
	}

	contextFact := FromError(
		requestcapture.FailureSiteGateway,
		requestcapture.FailurePeerGateway,
		requestcapture.FailureClassUnknown,
		requestcapture.FailureCodeGatewayContext,
		context.Canceled,
	)
	if contextFact.Class != requestcapture.FailureClassCanceled {
		t.Fatalf("context class = %q, want canceled", contextFact.Class)
	}
}

func TestWebSocketCloseBoundsProtocolMessageAtUTF8Boundary(t *testing.T) {
	reason := strings.Repeat("a", maxWebSocketCloseReasonBytes-1) + "界secret-tail"
	fact, truncated := WebSocketClose(
		requestcapture.FailureSiteWebSocketClose,
		requestcapture.FailurePeerUpstream,
		&websocket.CloseError{Code: websocket.StatusPolicyViolation, Reason: reason},
	)

	if fact.Class != requestcapture.FailureClassWebSocketClose ||
		fact.WebSocketCloseCode != int(websocket.StatusPolicyViolation) {
		t.Fatalf("close fact = %#v", fact)
	}
	if !truncated {
		t.Fatal("bounded close reason did not report truncation")
	}
	if len(fact.Message) > maxWebSocketCloseReasonBytes {
		t.Fatalf("bounded reason bytes = %d, want <= %d", len(fact.Message), maxWebSocketCloseReasonBytes)
	}
	if strings.Contains(fact.Message, "secret-tail") {
		t.Fatalf("bounded reason retained tail: %q", fact.Message)
	}
}

func TestObservationPromotesOnlyPhysicalFailure(t *testing.T) {
	secondary := FromError(
		requestcapture.FailureSiteResponseDrain,
		requestcapture.FailurePeerUpstream,
		requestcapture.FailureClassRead,
		requestcapture.FailureCodeDrainRead,
		syscall.Errno(9),
	)
	observation := Observation(requestcapture.FailureFact{}, secondary)

	if observation.Primary != secondary {
		t.Fatalf("primary = %#v, want %#v", observation.Primary, secondary)
	}
	if observation.HasSecondary || observation.Secondary != (requestcapture.FailureFact{}) {
		t.Fatalf("unexpected secondary = %#v (present=%t)", observation.Secondary, observation.HasSecondary)
	}
}

func TestFactRejectsNonCanonicalEnumAliases(t *testing.T) {
	hostileAlias := strings.Repeat("x", hostileMethodAllocationBytes)
	fact := Fact(
		requestcapture.FailureSite(hostileAlias),
		requestcapture.FailurePeer(hostileAlias),
		requestcapture.FailureClass(hostileAlias),
		requestcapture.FailureCode(hostileAlias),
	)

	if fact.Site != requestcapture.FailureSiteUnknown ||
		fact.Peer != requestcapture.FailurePeerUnknown ||
		fact.Class != requestcapture.FailureClassUnknown ||
		fact.Code != requestcapture.FailureCodeUnknown {
		t.Fatalf("canonical fact = %#v", fact)
	}
}
