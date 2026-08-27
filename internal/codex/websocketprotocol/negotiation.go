package websocketprotocol

import (
	"errors"
	"fmt"
	"strings"
)

const HeaderName = "Sec-WebSocket-Protocol"

var (
	ErrMalformedClientOffer = errors.New("malformed websocket subprotocol offer")
	ErrSelectionNotFixed    = errors.New("websocket subprotocol selection is not fixed")
	ErrSubprotocolMismatch  = errors.New("websocket subprotocol mismatch")
)

type Peer string

const (
	PeerUpstream   Peer = "upstream"
	PeerDownstream Peer = "downstream"
)

// Offer preserves client preference order because a probe must make the same
// deterministic choice before any upstream provider has been selected.
type Offer struct {
	protocols []string
}

func ParseClientOffer(headerValues []string) (Offer, error) {
	if len(headerValues) == 0 {
		return Offer{}, nil
	}

	protocols := make([]string, 0, len(headerValues))
	for lineIndex, value := range headerValues {
		parts := strings.Split(value, ",")
		for partIndex, part := range parts {
			protocol := strings.TrimSpace(part)
			if !isToken(protocol) {
				return Offer{}, fmt.Errorf(
					"%w at header value %d, item %d",
					ErrMalformedClientOffer,
					lineIndex,
					partIndex,
				)
			}
			protocols = append(protocols, protocol)
		}
	}
	return Offer{protocols: protocols}, nil
}

func (o Offer) Protocols() []string {
	return append([]string(nil), o.protocols...)
}

func isToken(value string) bool {
	if value == "" {
		return false
	}
	for index := range len(value) {
		character := value[index]
		if character >= 'a' && character <= 'z' ||
			character >= 'A' && character <= 'Z' ||
			character >= '0' && character <= '9' {
			continue
		}
		switch character {
		case '!', '#', '$', '%', '&', '\'', '*', '+', '-', '.', '^', '_', '`', '|', '~':
			continue
		default:
			return false
		}
	}
	return true
}

// Negotiation is an immutable session-level protocol decision. Before it is
// fixed, a non-probe dial offers the full client list. Once fixed, every
// physical connection is constrained to exactly one protocol (or no protocol).
type Negotiation struct {
	offer    Offer
	selected string
	fixed    bool
}

func New(offer Offer) Negotiation {
	return Negotiation{offer: offer}
}

func (n Negotiation) FixForProbe() Negotiation {
	n.fixed = true
	protocols := n.offer.protocols
	if len(protocols) > 0 {
		n.selected = protocols[0]
	}
	return n
}

func (n Negotiation) Fixed() bool {
	return n.fixed
}

func (n Negotiation) Selected() string {
	return n.selected
}

func (n Negotiation) ClientOffer() []string {
	return n.offer.Protocols()
}

func (n Negotiation) DialOffer() []string {
	if !n.fixed {
		return n.offer.Protocols()
	}
	if n.selected == "" {
		return nil
	}
	return []string{n.selected}
}

func (n Negotiation) DownstreamOffer() ([]string, error) {
	if !n.fixed {
		return nil, ErrSelectionNotFixed
	}
	if n.selected == "" {
		return nil, nil
	}
	return []string{n.selected}, nil
}

func (n Negotiation) BindUpstream(actual string) (Negotiation, error) {
	if n.fixed {
		if actual != n.selected {
			return n, newMismatch(PeerUpstream, n.selected, actual)
		}
		return n, nil
	}

	if actual != "" && !containsExact(n.offer.protocols, actual) {
		return n, newMismatch(PeerUpstream, "a client-offered protocol", actual)
	}
	n.selected = actual
	n.fixed = true
	return n, nil
}

func (n Negotiation) ValidateDownstream(actual string) error {
	if !n.fixed {
		return ErrSelectionNotFixed
	}
	if actual != n.selected {
		return newMismatch(PeerDownstream, n.selected, actual)
	}
	return nil
}

func containsExact(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

type MismatchError struct {
	Peer     Peer
	Expected string
	Actual   string
}

func newMismatch(peer Peer, expected, actual string) *MismatchError {
	return &MismatchError{Peer: peer, Expected: expected, Actual: actual}
}

func (e *MismatchError) Error() string {
	return fmt.Sprintf(
		"%s: %s selected %q, expected %q",
		ErrSubprotocolMismatch,
		e.Peer,
		e.Actual,
		e.Expected,
	)
}

func (e *MismatchError) Unwrap() error {
	return ErrSubprotocolMismatch
}
