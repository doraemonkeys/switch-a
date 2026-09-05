package proxy

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/doraemonkeys/switch-a/internal/attemptevidence"
	"github.com/doraemonkeys/switch-a/internal/codex/clientdisguise"
	"github.com/doraemonkeys/switch-a/internal/codex/clientdisguise/wire"
	"github.com/doraemonkeys/switch-a/internal/codex/disguiseruntime"
	codexhttp "github.com/doraemonkeys/switch-a/internal/codex/http"
	"github.com/doraemonkeys/switch-a/internal/model"
	disguiseresponse "github.com/doraemonkeys/switch-a/internal/proxy/disguise"
	"github.com/doraemonkeys/switch-a/internal/responseanalysis"
	"github.com/doraemonkeys/switch-a/internal/upstreamtransport"
	"github.com/google/uuid"
	"go.uber.org/zap"
)

type httpDisguiseOperation struct {
	operation           *disguiseruntime.Operation
	sessions            map[string]*wire.Session
	current             *wire.Session
	target              clientdisguise.TargetSnapshot
	providerID          string
	credentialSessionID string
	accountID           string
	failure             *wire.Failure
}

var errClientDisguiseFailed = errors.New("client disguise conversion failed")

// Metadata on HEAD and no-content responses describes another representation;
// content type and encoding cannot imply that this exchange contains its body.
func httpResponseAllowsBody(method string, status int) bool {
	return method != http.MethodHead && status >= http.StatusOK && status != http.StatusNoContent && status != http.StatusResetContent && status != http.StatusNotModified
}

func (h *Handler) beginHTTPDisguise(ctx context.Context, pctx *proxyContext) error {
	if pctx.apiType != APITypeCodex || h.clientDisguise == nil {
		return nil
	}
	providers, err := h.store.ListProvidersByAPIType(ctx, pctx.apiType)
	if err != nil {
		return err
	}
	op, err := disguiseruntime.New(ctx, h.clientDisguise, providers, pctx.r.Header, pctx.requestID)
	if err != nil {
		return err
	}
	pctx.disguise = &httpDisguiseOperation{operation: op, sessions: make(map[string]*wire.Session)}
	pctx.selectReq.ClientDisguise = op
	return nil
}

func (h *Handler) prepareHTTPDisguise(ctx context.Context, pctx *proxyContext, provider *model.Provider, request *http.Request) error {
	d := pctx.disguise
	if d == nil {
		return nil
	}
	credential, ok := provider.CredentialSessionForAPIType(APITypeCodex)
	if !ok {
		return httpDisguiseInvariantFailure(pctx, "target", fmt.Errorf("selected provider has no Codex credential"))
	}
	target, ok := d.operation.Target(provider.ID, credential.SessionID)
	if !ok {
		return httpDisguiseInvariantFailure(pctx, "target", fmt.Errorf("selected provider has no committed disguise snapshot"))
	}
	key := provider.ID + "\x00" + credential.SessionID
	session := d.sessions[key]
	if session == nil {
		session = wire.NewSession(h.clientDisguise, target, pctx.codex.ClientIdentity().ID, pctx.requestID)
		d.sessions[key] = session
	}
	d.current, d.target, d.providerID = session, target, provider.ID
	d.credentialSessionID = credential.SessionID
	d.accountID = credential.AuthState.AccountID
	pctx.transport = h.getTransport(pctx.cfg)
	if !target.Policy.Enabled {
		return nil
	}
	// Continuity and credential policy have already examined the original
	// client request. Only this physical delivery receives the derived bytes.
	derived, err := session.Headers(ctx, request.Header)
	if err != nil {
		return err
	}
	request.Header = derived
	if codexhttp.RequestUsesJSON(pctx.r) {
		source, err := session.RequestBody(ctx, pctx.upload, pctx.r.Header.Values("Content-Encoding"))
		if err != nil {
			return err
		}
		if err := upstreamtransport.SetBodySource(request, source); err != nil {
			return err
		}
	}
	config, err := session.TransportConfig()
	if err != nil {
		return err
	}
	if h.transportOverride == nil {
		transport, err := h.disguisePool.Get(upstreamtransport.Config{ConnectTimeout: pctx.cfg.connectTimeout, FirstByteTimeout: pctx.cfg.firstByteTimeout}, config)
		if err != nil {
			return httpDisguiseInvariantFailure(pctx, "transport", err)
		}
		pctx.transport = &Transport{upstream: transport}
	}
	h.logger.Debug("client_disguise.http_target", zap.String("operation_id", pctx.requestID), zap.String("provider_id", provider.ID), zap.String("credential_session_id", credential.SessionID), zap.String("profile_revision", target.Profile.ID))
	return nil
}

func httpDisguiseInvariantFailure(pctx *proxyContext, stage string, cause error) *wire.Failure {
	return &wire.Failure{DiagnosticID: uuid.NewString(), OperationID: pctx.requestID, Stage: stage, Carrier: "http", FieldPath: "$", Cause: cause}
}

func (d *httpDisguiseOperation) prepareResponse(ctx context.Context, writer *firstWriteResponseWriter, head upstreamtransport.ResponseHead, media responseanalysis.ResponseMedia, hasBody bool) error {
	if d == nil || d.current == nil || !d.target.Policy.Enabled {
		return nil
	}
	session := d.current
	transformJSON := hasBody && codexhttp.IsJSONContentType(head.Header.Get("Content-Type"))
	transformSSE := hasBody && media.IsEventStream()
	writer.restoreHeader = func(original http.Header) (http.Header, error) {
		restored, err := session.RestoreHeaders(ctx, original)
		if err != nil {
			return nil, err
		}
		if transformJSON || transformSSE {
			return upstreamtransport.DerivedResponseHead(upstreamtransport.ResponseHead{Header: restored}).Header, nil
		}
		return restored, nil
	}
	if transformSSE {
		writer.restoreEvent = func(original []byte) ([]byte, error) { return session.ServerSSE(ctx, original) }
		return nil
	}
	if !transformJSON {
		return nil
	}
	_, stream, err := disguiseresponse.NewResponseStream(ctx, session, head, disguiseresponse.WriterFunc(func(payload []byte) (int, error) { return writer.writePhysical(payload, nil) }))
	if err != nil {
		return err
	}
	writer.responseStream = stream
	return nil
}

func (h *Handler) disguiseFailure(pctx *proxyContext, err error) (forwardResult, bool) {
	var failure *wire.Failure
	if pctx.disguise != nil && pctx.disguise.failure != nil {
		return failureResult(attemptFailureDisguise, pctx.disguise.failure), true
	}
	if !errors.As(err, &failure) && pctx.disguise != nil && pctx.disguise.current != nil {
		failure = pctx.disguise.current.Failure()
	}
	if failure == nil {
		return forwardResult{}, false
	}
	if pctx.disguise != nil {
		if pctx.disguise.failure == nil {
			pctx.disguise.failure = failure
			h.logger.Error("client_disguise.http_failed", zap.String("operation_id", pctx.requestID), zap.String("diagnostic_id", failure.DiagnosticID), zap.String("stage", failure.Stage), zap.String("carrier", failure.Carrier), zap.String("field_path", failure.FieldPath), zap.String("original_snippet", failure.OriginalSnippet), zap.String("derived_snippet", failure.DerivedSnippet), zap.Error(failure))
		}
		failure = pctx.disguise.failure
	}
	return failureResult(attemptFailureDisguise, failure), true
}

func (pctx *proxyContext) disguiseEvidence() *attemptevidence.ClientDisguise {
	d := pctx.disguise
	if d == nil {
		return nil
	}
	t := d.target
	e := &attemptevidence.ClientDisguise{DiagnosticID: pctx.requestID, RequestID: pctx.requestID, OperationID: pctx.requestID, ProviderID: d.providerID, CredentialSessionID: t.Login.CredentialSessionID, ClientIdentityID: pctx.codex.ClientIdentity().ID, GenerationID: t.Login.GenerationID, DeviceID: t.Login.DeviceID, ClientVersion: t.Profile.ClientVersion, RevisionID: t.Profile.ID, Decision: "disabled"}
	e.CredentialSessionID = d.credentialSessionID
	e.AccountID = d.accountID
	e.SourceID = t.Profile.SourceID
	if !t.Profile.CapturedAt.IsZero() {
		e.CapturedAt = t.Profile.CapturedAt.Format(time.RFC3339Nano)
	}
	e.ClientType = t.Profile.Tuple.ClientType
	e.Platform = t.Profile.Tuple.Platform
	e.Arch = t.Profile.Tuple.Arch
	facts := d.operation.Facts()
	e.PlatformFacts = map[string]string{"platform": facts.Tuple.Platform, "arch": facts.Tuple.Arch, "client_type": facts.Tuple.ClientType, "conflict": fmt.Sprint(facts.Conflict)}
	for _, basis := range facts.Evidence {
		e.PlatformFacts[basis.Field] = basis.Value
	}
	if t.Policy.Enabled {
		e.Decision = "transformed"
		e.AppliedScopes = []string{"identifiers", "application_profile"}
	}
	if t.Transport != nil {
		e.TransportSampleID = t.Transport.ID
		if t.Policy.Enabled {
			e.AppliedScopes = append(e.AppliedScopes, "transport")
		}
	}
	for _, candidate := range d.operation.Exclusions() {
		e.Candidates = append(e.Candidates, attemptevidence.DisguiseCandidate{ProviderID: candidate.ProviderID, CredentialSessionID: candidate.CredentialSessionID, Outcome: "excluded", Reason: candidate.Reason})
	}
	if d.current == nil && len(e.Candidates) > 0 {
		e.Decision = "excluded"
	}
	if d.current != nil {
		for _, difference := range d.current.Differences() {
			e.Differences = append(e.Differences, attemptevidence.DisguiseDifference{Carrier: difference.Carrier, Location: difference.FieldPath, Original: difference.Original, Derived: difference.Derived})
		}
	}
	if f := d.failure; f != nil {
		e.DiagnosticID = f.DiagnosticID
		e.Decision = "failed"
		e.Phase = f.Stage
		e.Failure = &attemptevidence.DisguiseFailure{Phase: f.Stage, Location: f.Carrier + ":" + f.FieldPath, OriginalSnippet: f.OriginalSnippet, DerivedSnippet: f.DerivedSnippet}
		for cause := error(f); cause != nil; cause = errors.Unwrap(cause) {
			e.Failure.ErrorChain = append(e.Failure.ErrorChain, cause.Error())
		}
	}
	return e
}

func (h *Handler) mergeDisguiseEvidence(pctx *proxyContext, existing *string) *string {
	if evidence := pctx.disguiseEvidence(); evidence != nil {
		merged, err := attemptevidence.EncodeClientDisguiseString(existing, evidence)
		if err == nil {
			return merged
		}
		h.logger.Error("client_disguise.evidence_failed", zap.String("operation_id", pctx.requestID), zap.Error(err))
	}
	return existing
}
