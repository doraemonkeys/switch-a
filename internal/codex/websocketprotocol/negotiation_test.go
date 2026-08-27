package websocketprotocol

import (
	"errors"
	"reflect"
	"testing"
)

func TestParseClientOffer(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		values  []string
		want    []string
		wantErr error
	}{
		{name: "absent"},
		{name: "ordered across lines", values: []string{"realtime.v2, realtime.v1", "fallback"}, want: []string{"realtime.v2", "realtime.v1", "fallback"}},
		{name: "valid token punctuation", values: []string{"a!#$%&'*+-.^_`|~z"}, want: []string{"a!#$%&'*+-.^_`|~z"}},
		{name: "empty header", values: []string{""}, wantErr: ErrMalformedClientOffer},
		{name: "empty list member", values: []string{"one,,two"}, wantErr: ErrMalformedClientOffer},
		{name: "quoted protocol", values: []string{"\"one\""}, wantErr: ErrMalformedClientOffer},
		{name: "separator", values: []string{"one/two"}, wantErr: ErrMalformedClientOffer},
		{name: "non ASCII", values: []string{"协议"}, wantErr: ErrMalformedClientOffer},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			offer, err := ParseClientOffer(test.values)
			if !errors.Is(err, test.wantErr) {
				t.Fatalf("ParseClientOffer() error = %v, want %v", err, test.wantErr)
			}
			if got := offer.Protocols(); !reflect.DeepEqual(got, test.want) {
				t.Fatalf("Protocols() = %#v, want %#v", got, test.want)
			}
		})
	}
}

func TestNegotiationNonProbeBindsUpstreamSelection(t *testing.T) {
	t.Parallel()
	offer, err := ParseClientOffer([]string{"realtime.v2, realtime.v1"})
	if err != nil {
		t.Fatal(err)
	}
	negotiation := New(offer)
	if got := negotiation.DialOffer(); !reflect.DeepEqual(got, offer.Protocols()) {
		t.Fatalf("DialOffer() = %#v, want %#v", got, offer.Protocols())
	}

	negotiation, err = negotiation.BindUpstream("realtime.v1")
	if err != nil {
		t.Fatal(err)
	}
	if !negotiation.Fixed() || negotiation.Selected() != "realtime.v1" {
		t.Fatalf("negotiation = %#v, want fixed realtime.v1", negotiation)
	}
	if got := negotiation.DialOffer(); !reflect.DeepEqual(got, []string{"realtime.v1"}) {
		t.Fatalf("replacement DialOffer() = %#v", got)
	}
	if got, err := negotiation.DownstreamOffer(); err != nil || !reflect.DeepEqual(got, []string{"realtime.v1"}) {
		t.Fatalf("DownstreamOffer() = %#v, %v", got, err)
	}
	if err := negotiation.ValidateDownstream("realtime.v1"); err != nil {
		t.Fatalf("ValidateDownstream() error = %v", err)
	}
}

func TestNegotiationNonProbeAllowsNoSelection(t *testing.T) {
	t.Parallel()
	offer, err := ParseClientOffer([]string{"realtime.v2"})
	if err != nil {
		t.Fatal(err)
	}
	negotiation, err := New(offer).BindUpstream("")
	if err != nil {
		t.Fatal(err)
	}
	if got, err := negotiation.DownstreamOffer(); err != nil || got != nil {
		t.Fatalf("DownstreamOffer() = %#v, %v, want nil", got, err)
	}
	if err := negotiation.ValidateDownstream(""); err != nil {
		t.Fatal(err)
	}
}

func TestNegotiationProbeFixesFirstClientPreference(t *testing.T) {
	t.Parallel()
	offer, err := ParseClientOffer([]string{"realtime.v2, realtime.v1"})
	if err != nil {
		t.Fatal(err)
	}
	negotiation := New(offer).FixForProbe()
	if got := negotiation.DialOffer(); !reflect.DeepEqual(got, []string{"realtime.v2"}) {
		t.Fatalf("DialOffer() = %#v, want fixed first preference", got)
	}
	if _, err := negotiation.BindUpstream("realtime.v2"); err != nil {
		t.Fatalf("BindUpstream() error = %v", err)
	}
}

func TestNegotiationRejectsEndpointMismatch(t *testing.T) {
	t.Parallel()
	offer, err := ParseClientOffer([]string{"realtime.v2"})
	if err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name   string
		run    func() error
		peer   Peer
		reason MismatchReason
	}{
		{name: "non probe upstream unoffered", run: func() error { _, err := New(offer).BindUpstream("other"); return err }, peer: PeerUpstream, reason: MismatchReasonUnexpectedSelection},
		{name: "probe upstream empty", run: func() error { _, err := New(offer).FixForProbe().BindUpstream(""); return err }, peer: PeerUpstream, reason: MismatchReasonMissingSelection},
		{name: "probe upstream different casing", run: func() error { _, err := New(offer).FixForProbe().BindUpstream("REALTIME.V2"); return err }, peer: PeerUpstream, reason: MismatchReasonSelectionChanged},
		{name: "downstream mismatch", run: func() error { return New(offer).FixForProbe().ValidateDownstream("") }, peer: PeerDownstream, reason: MismatchReasonMissingSelection},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			err := test.run()
			if !errors.Is(err, ErrSubprotocolMismatch) {
				t.Fatalf("error = %v, want mismatch", err)
			}
			var mismatch *MismatchError
			if !errors.As(err, &mismatch) || mismatch.Peer != test.peer || mismatch.Reason != test.reason {
				t.Fatalf("mismatch = %#v, want peer %q reason %q", mismatch, test.peer, test.reason)
			}
		})
	}
}

func TestNegotiationRequiresSelectionBeforeDownstreamAccept(t *testing.T) {
	t.Parallel()
	negotiation := New(Offer{})
	if _, err := negotiation.DownstreamOffer(); !errors.Is(err, ErrSelectionNotFixed) {
		t.Fatalf("DownstreamOffer() error = %v, want %v", err, ErrSelectionNotFixed)
	}
	if err := negotiation.ValidateDownstream(""); !errors.Is(err, ErrSelectionNotFixed) {
		t.Fatalf("ValidateDownstream() error = %v, want %v", err, ErrSelectionNotFixed)
	}
}
