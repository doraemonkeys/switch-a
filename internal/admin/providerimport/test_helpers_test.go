package providerimport

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/doraemonkeys/switch-a/internal/codex/credentialsession"
	"github.com/doraemonkeys/switch-a/internal/model"
	"github.com/doraemonkeys/switch-a/internal/providerauth"
	"github.com/doraemonkeys/switch-a/internal/store"
	"go.uber.org/zap"
)

type providerImportTestClock struct{ now time.Time }

func (c providerImportTestClock) Now() time.Time { return c.now }

type providerImportTestCatalog struct {
	mu        sync.Mutex
	providers []model.Provider
	err       error
	calls     int
}

func (c *providerImportTestCatalog) ListProviders(context.Context) ([]model.Provider, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.calls++
	return append([]model.Provider(nil), c.providers...), c.err
}

type providerImportTestDrafts struct {
	mu sync.Mutex

	preview    *providerauth.ChatGPTProviderImportPreview
	previewErr error
	previewRaw []byte

	sealErr          error
	sealedImportID   string
	sealed           []providerauth.ChatGPTProviderImportCandidateDisposition
	candidates       []providerauth.ChatGPTProviderImportCandidate
	claimErr         error
	claimCalls       int
	releaseErr       error
	releaseCalls     int
	verifyErr        error
	verified         []providerauth.ChatGPTProviderImportCandidate
	invalidated      [][]string
	finalizeErr      error
	finalizeCalls    int
	cancelErr        error
	cancelCalls      int
	cancelledImport  string
	claimStarted     chan struct{}
	claimContinue    chan struct{}
	finalizeContinue chan struct{}
}

func (d *providerImportTestDrafts) PreviewSub2APIChatGPTImport(raw []byte) (*providerauth.ChatGPTProviderImportPreview, error) {
	d.mu.Lock()
	d.previewRaw = append([]byte(nil), raw...)
	preview, err := d.preview, d.previewErr
	d.mu.Unlock()
	return preview, err
}

func (d *providerImportTestDrafts) SealChatGPTProviderImportPreview(importID string, dispositions []providerauth.ChatGPTProviderImportCandidateDisposition) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.sealedImportID = importID
	d.sealed = append([]providerauth.ChatGPTProviderImportCandidateDisposition(nil), dispositions...)
	return d.sealErr
}

func (d *providerImportTestDrafts) ClaimChatGPTProviderImport(string) ([]providerauth.ChatGPTProviderImportCandidate, error) {
	d.mu.Lock()
	d.claimCalls++
	started, proceed := d.claimStarted, d.claimContinue
	candidates := append([]providerauth.ChatGPTProviderImportCandidate(nil), d.candidates...)
	err := d.claimErr
	d.mu.Unlock()
	if started != nil {
		select {
		case <-started:
		default:
			close(started)
		}
	}
	if proceed != nil {
		<-proceed
	}
	return candidates, err
}

func (d *providerImportTestDrafts) ReleaseChatGPTProviderImportClaim(string) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.releaseCalls++
	return d.releaseErr
}

func (d *providerImportTestDrafts) VerifyChatGPTProviderImportCandidates(_ context.Context, candidates []providerauth.ChatGPTProviderImportCandidate) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.verified = append([]providerauth.ChatGPTProviderImportCandidate(nil), candidates...)
	return d.verifyErr
}

func (d *providerImportTestDrafts) InvalidateCredentialSessions(sessionIDs []string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.invalidated = append(d.invalidated, append([]string(nil), sessionIDs...))
}

func (d *providerImportTestDrafts) FinalizeChatGPTProviderImport(string) error {
	d.mu.Lock()
	d.finalizeCalls++
	proceed, err := d.finalizeContinue, d.finalizeErr
	d.mu.Unlock()
	if proceed != nil {
		<-proceed
	}
	return err
}

func (d *providerImportTestDrafts) CancelChatGPTProviderImport(importID string) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.cancelCalls++
	d.cancelledImport = importID
	return d.cancelErr
}

type providerImportTestStore struct {
	mu sync.Mutex

	receipts      map[string]*store.ProviderImportReceipt
	receiptErr    error
	receiptFunc   func(string, int) (*store.ProviderImportReceipt, error)
	receiptCalls  int
	applyErr      error
	applyFunc     func(context.Context, *store.ProviderImportBundle) error
	applied       []*store.ProviderImportBundle
	mutationErr   error
	mutationIDs   [][]string
	releaseCalls  int
	mutationValue any
}

func (s *providerImportTestStore) WithCredentialSessionMutations(
	ctx context.Context,
	sessionIDs []string,
) (context.Context, func(), error) {
	s.mu.Lock()
	s.mutationIDs = append(s.mutationIDs, append([]string(nil), sessionIDs...))
	err := s.mutationErr
	value := s.mutationValue
	s.mu.Unlock()
	if err != nil {
		return nil, nil, err
	}
	if value != nil {
		ctx = context.WithValue(ctx, providerImportTestContextKey{}, value)
	}
	return ctx, func() {
		s.mu.Lock()
		s.releaseCalls++
		s.mu.Unlock()
	}, nil
}

func (s *providerImportTestStore) GetProviderImportReceipt(_ context.Context, importID string) (*store.ProviderImportReceipt, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.receiptCalls++
	if s.receiptFunc != nil {
		return s.receiptFunc(importID, s.receiptCalls)
	}
	if s.receiptErr != nil {
		return nil, s.receiptErr
	}
	receipt := s.receipts[importID]
	if receipt == nil {
		return nil, store.ErrProviderImportReceiptNotFound
	}
	clone := *receipt
	clone.ResponsePayload = append([]byte(nil), receipt.ResponsePayload...)
	return &clone, nil
}

func (s *providerImportTestStore) ApplyProviderImport(ctx context.Context, bundle *store.ProviderImportBundle) error {
	s.mu.Lock()
	s.applied = append(s.applied, bundle)
	apply := s.applyFunc
	err := s.applyErr
	s.mu.Unlock()
	if apply != nil {
		return apply(ctx, bundle)
	}
	if err == nil && bundle != nil && bundle.Receipt != nil {
		s.mu.Lock()
		if s.receipts == nil {
			s.receipts = make(map[string]*store.ProviderImportReceipt)
		}
		clone := *bundle.Receipt
		clone.ResponsePayload = append([]byte(nil), bundle.Receipt.ResponsePayload...)
		s.receipts[clone.ImportID] = &clone
		s.mu.Unlock()
	}
	return err
}

type providerImportTestContextKey struct{}

type providerImportTestLifecycles struct {
	mu       sync.Mutex
	calls    int
	err      error
	skip     bool
	callback func()
}

func (l *providerImportTestLifecycles) RetireAllProviderGenerations(mutation func() error) error {
	l.mu.Lock()
	l.calls++
	err, skip, callback := l.err, l.skip, l.callback
	l.mu.Unlock()
	if callback != nil {
		callback()
	}
	if err != nil || skip {
		return err
	}
	return mutation()
}

type providerImportTestBody struct{ err error }

func (b providerImportTestBody) Read([]byte) (int, error) { return 0, b.err }
func (providerImportTestBody) Close() error               { return nil }

func newProviderImportTestHandler(
	catalog *providerImportTestCatalog,
	drafts *providerImportTestDrafts,
	importStore *providerImportTestStore,
	lifecycles *providerImportTestLifecycles,
	logger *zap.Logger,
) *Handler {
	if catalog == nil {
		catalog = &providerImportTestCatalog{}
	}
	if drafts == nil {
		drafts = &providerImportTestDrafts{}
	}
	if importStore == nil {
		importStore = &providerImportTestStore{}
	}
	config := Config{
		ProviderCatalog: catalog,
		Drafts:          drafts,
		Store:           importStore,
		Logger:          logger,
	}
	if lifecycles != nil {
		config.Lifecycles = lifecycles
	}
	handler := NewHandler(config)
	handler.providerImportSetReadDeadline = func(http.ResponseWriter, time.Time) error { return nil }
	handler.providerImportReceipts = newProviderImportCommitReceiptRegistry(
		providerImportTestClock{now: time.Date(2026, 8, 27, 1, 2, 3, 0, time.UTC)},
		time.Minute,
	)
	return handler
}

func providerImportTestCandidate(
	t *testing.T,
	candidateID string,
	accountID string,
	secret string,
	disposition providerauth.ChatGPTProviderImportCandidateDisposition,
) providerauth.ChatGPTProviderImportCandidate {
	t.Helper()
	subject, err := credentialsession.AccountSubject(accountID)
	if err != nil {
		t.Fatal(err)
	}
	disposition.CandidateID = candidateID
	return providerauth.ChatGPTProviderImportCandidate{
		CandidateID: candidateID,
		State:       providerauth.ChatGPTProviderImportCandidateStateReady,
		Credential: credentialsession.Snapshot{
			Kind: credentialsession.KindChatGPT, SecretData: secret,
			Version: 1, Subject: subject,
			AuthState: credentialsession.AuthState{
				Status: credentialsession.AuthStatusActive, AccountID: accountID,
			},
		},
		Disposition: &disposition,
	}
}

func providerImportTestProvider(
	t *testing.T,
	providerID string,
	apiTypes []string,
	sessionID string,
	accountID string,
	version int64,
) model.Provider {
	t.Helper()
	subject, err := credentialsession.AccountSubject(accountID)
	if err != nil {
		t.Fatal(err)
	}
	provider := model.Provider{ID: providerID, Name: providerID, Vendor: "openai", AuthMode: "bearer", Enabled: true, Weight: 1}
	for _, apiType := range apiTypes {
		provider.APITypes = append(provider.APITypes, model.ProviderAPIType{
			ProviderID: providerID, APIType: apiType, BaseURL: "https://example.test/" + apiType,
		})
		provider.CredentialSessions = append(provider.CredentialSessions, credentialsession.RouteSnapshot{
			RouteTargetID: providerID,
			APIType:       apiType,
			VendorScope:   provider.Vendor,
			Credential: credentialsession.Snapshot{
				SessionID: sessionID, Kind: credentialsession.KindChatGPT,
				SecretData: `{"access_token":"old"}`, Version: version, Subject: subject,
				AuthState: credentialsession.AuthState{Status: credentialsession.AuthStatusActive, AccountID: accountID},
			},
		})
	}
	return provider
}

func providerImportTestCreateItem(candidateID, providerID, name string) ProviderImportCommitItem {
	return ProviderImportCommitItem{CandidateID: candidateID, Action: providerImportActionCreate, ProviderID: providerID, Name: name}
}

func providerImportTestCommitRequest(t *testing.T, importID, body string) *http.Request {
	t.Helper()
	r := httptest.NewRequest(http.MethodPost, "/admin/api/provider-imports/test/commit", strings.NewReader(body))
	r.SetPathValue("import_id", importID)
	return r
}

func providerImportTestCancelRequest(importID string) *http.Request {
	r := httptest.NewRequest(http.MethodDelete, "/admin/api/provider-imports/test", nil)
	r.SetPathValue("import_id", importID)
	return r
}

func requireProviderImportStatus(t *testing.T, recorder *httptest.ResponseRecorder, status int) {
	t.Helper()
	if recorder.Code != status {
		t.Fatalf("status = %d, want %d; body = %s", recorder.Code, status, recorder.Body.String())
	}
}

var _ io.ReadCloser = providerImportTestBody{}
var _ ProviderCatalog = (*providerImportTestCatalog)(nil)
var _ DraftService = (*providerImportTestDrafts)(nil)
var _ Store = (*providerImportTestStore)(nil)
var _ ProviderLifecycleCoordinator = (*providerImportTestLifecycles)(nil)
