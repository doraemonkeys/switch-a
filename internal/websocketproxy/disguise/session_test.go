package wsdisguise

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/doraemonkeys/switch-a/internal/codex/clientdisguise"
	"github.com/doraemonkeys/switch-a/internal/codex/clientdisguise/wire"
	"github.com/doraemonkeys/switch-a/internal/codex/credentialsession"
	"github.com/doraemonkeys/switch-a/internal/model"
	"github.com/doraemonkeys/switch-a/internal/upstreamtransport"
)

type repository struct {
	target clientdisguise.TargetSnapshot
	err    error
}

func (r repository) EvaluateCandidate(_ context.Context, id string, basis clientdisguise.AccountBasis, policy clientdisguise.Policy, facts clientdisguise.PlatformFacts) (clientdisguise.Candidate, error) {
	return clientdisguise.Candidate{CredentialSessionID: id, AccountBasis: basis, Policy: policy, Facts: facts, Decision: clientdisguise.PlatformDecision{Allowed: true}}, r.err
}
func (r repository) CommitTarget(context.Context, clientdisguise.Candidate) (clientdisguise.TargetSnapshot, error) {
	return r.target, r.err
}
func (repository) MapIdentity(_ context.Context, key clientdisguise.MappingKey) (string, error) {
	return "mapped-" + key.Original, nil
}
func (repository) RestoreIdentity(context.Context, string, string, string, string) (string, bool, error) {
	return "", false, nil
}
func route() model.Provider {
	return model.Provider{ID: "route", CredentialSessions: []credentialsession.RouteSnapshot{{APIType: "codex", Credential: credentialsession.Snapshot{SessionID: "login"}}}}
}
func TestConnectionTargetAndTransportSnapshot(t *testing.T) {
	provider := route()
	pool := upstreamtransport.NewPool()
	defer pool.CloseIdleConnections()
	repo := repository{target: clientdisguise.TargetSnapshot{Policy: clientdisguise.Policy{Enabled: true},
		Login:     clientdisguise.LoginIdentity{GenerationID: "generation", DeviceID: "device"},
		Profile:   clientdisguise.ProfileRevision{Features: clientdisguise.Features{UserAgent: "observed"}},
		Transport: &clientdisguise.TransportSample{Config: []byte(`{"http_protocol":"http1","alpn":["http/1.1"]}`)}}}
	session, err := New(context.Background(), repo, []model.Provider{provider}, nil, "client", "operation", pool)
	if err != nil {
		t.Fatal(err)
	}
	if session.Current() != nil {
		t.Fatal("target installed before selection")
	}
	if _, err = session.HTTPClient(); err != nil {
		t.Fatal(err)
	}
	if err = session.Select(&provider); err == nil {
		t.Fatal("uncommitted target selected")
	}
	if _, err = session.Operation().Commit(context.Background(), &provider); err != nil {
		t.Fatal(err)
	}
	if err = session.Select(&provider); err != nil {
		t.Fatal(err)
	}
	first := session.Current()
	if err = session.Select(&provider); err != nil || session.Current() != first {
		t.Fatal("same target replaced its session snapshot")
	}
	headers, err := first.Headers(context.Background(), http.Header{})
	if err != nil || headers.Get("User-Agent") != "observed" {
		t.Fatalf("header snapshot: %v %v", headers, err)
	}
	client, err := session.HTTPClient()
	if err != nil || client == nil {
		t.Fatalf("sample client: %v %v", client, err)
	}
	again, err := session.HTTPClient()
	if err != nil || client.Transport != again.Transport {
		t.Fatal("identical sampled transport did not reuse pool")
	}
}
func TestUnsupportedWebSocketTransportTerminatesBeforeDial(t *testing.T) {
	for _, config := range []string{`{"http_protocol":"http2"}`, `{"alpn":["h2","http/1.1"]}`, `{"unsupported":true}`} {
		t.Run(config, func(t *testing.T) {
			provider := route()
			repo := repository{target: clientdisguise.TargetSnapshot{Policy: clientdisguise.Policy{Enabled: true},
				Transport: &clientdisguise.TransportSample{Config: []byte(config)}}}
			session, err := New(context.Background(), repo, []model.Provider{provider}, nil, "client", "operation", nil)
			if err != nil {
				t.Fatal(err)
			}
			if _, err = session.Operation().Commit(context.Background(), &provider); err != nil {
				t.Fatal(err)
			}
			if err = session.Select(&provider); err != nil {
				t.Fatal(err)
			}
			client, err := session.HTTPClient()
			var failure *wire.Failure
			if client != nil || !errors.As(err, &failure) || failure.DiagnosticID == "" {
				t.Fatalf("invalid transport was not diagnosed before dialing: %v %v", client, err)
			}
		})
	}
}
func TestOptionalSessionAndDependencyBoundaries(t *testing.T) {
	var absent *Session
	if absent.Operation() != nil || absent.Current() != nil || absent.Select(nil) != nil {
		t.Fatal("absent feature changed pass-through")
	}
	if client, err := absent.HTTPClient(); client != nil || err != nil {
		t.Fatal("absent feature changed transport")
	}
	if _, err := New(context.Background(), nil, nil, nil, "client", "operation", nil); err == nil {
		t.Fatal("nil repository accepted")
	}
	provider := route()
	if _, err := New(context.Background(), repository{err: errors.New("store")}, []model.Provider{provider}, nil, "client", "operation", nil); err == nil {
		t.Fatal("repository error ignored")
	}
	session, err := New(context.Background(), repository{}, []model.Provider{provider}, nil, "client", "operation", nil)
	if err != nil {
		t.Fatal(err)
	}
	if err = session.Select(nil); err == nil {
		t.Fatal("nil provider accepted")
	}
	if err = session.Select(&model.Provider{ID: "missing"}); err == nil {
		t.Fatal("missing credential accepted")
	}
	if _, err = session.Operation().Commit(context.Background(), &provider); err != nil {
		t.Fatal(err)
	}
	if err = session.Select(&provider); err != nil {
		t.Fatal(err)
	}
	if client, err := session.HTTPClient(); client != nil || err != nil {
		t.Fatal("disabled profile changed default client")
	}
}
