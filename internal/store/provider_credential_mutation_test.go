package store

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/doraemonkeys/switch-a/internal"
	"github.com/doraemonkeys/switch-a/internal/model"
)

const (
	credentialMutationTestTimeout      = 2 * time.Second
	credentialMutationTestPollInterval = time.Millisecond
)

type credentialMutationAcquisition struct {
	ownedCtx context.Context
	release  func()
	err      error
}

func TestNormalizeProviderCredentialMutationIDs(t *testing.T) {
	got, err := normalizeProviderCredentialMutationIDs([]string{
		"provider-z",
		" provider-a ",
		"provider-z",
		"provider-b",
		"provider-a",
	})
	if err != nil {
		t.Fatalf("normalizeProviderCredentialMutationIDs() error = %v", err)
	}
	want := []string{"provider-a", "provider-b", "provider-z"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("normalizeProviderCredentialMutationIDs() = %#v, want %#v", got, want)
	}

	if _, err := normalizeProviderCredentialMutationIDs([]string{"provider-a", "  "}); err == nil {
		t.Fatal("normalizeProviderCredentialMutationIDs(blank) error = nil")
	}
}

func TestProviderCredentialMutationCoordinatorSerializesAndReclaims(t *testing.T) {
	coordinator := newProviderCredentialMutationCoordinator()
	_, firstRelease, err := coordinator.with(context.Background(), []string{"provider-a"})
	if err != nil {
		t.Fatalf("first acquire error = %v", err)
	}

	secondResult := make(chan credentialMutationAcquisition, 1)
	go func() {
		ownedCtx, release, acquireErr := coordinator.with(context.Background(), []string{"provider-a"})
		secondResult <- credentialMutationAcquisition{ownedCtx: ownedCtx, release: release, err: acquireErr}
	}()
	waitForProviderMutationReferences(t, coordinator, "provider-a", 2)
	select {
	case result := <-secondResult:
		if result.release != nil {
			result.release()
		}
		t.Fatalf("second acquisition completed while first held the provider: %v", result.err)
	default:
	}

	firstRelease()
	result := receiveCredentialMutationAcquisition(t, secondResult)
	if result.err != nil {
		t.Fatalf("second acquire error = %v", result.err)
	}
	result.release()

	// Release is intentionally safe to call from stacked cleanup paths.
	idempotentDone := make(chan struct{})
	go func() {
		result.release()
		close(idempotentDone)
	}()
	select {
	case <-idempotentDone:
	case <-time.After(credentialMutationTestTimeout):
		t.Fatal("second release call blocked")
	}
	assertProviderMutationRegistrySize(t, coordinator, 0)
}

func TestProviderCredentialMutationCoordinatorAllowsDisjointProviders(t *testing.T) {
	coordinator := newProviderCredentialMutationCoordinator()
	_, releaseA, err := coordinator.with(context.Background(), []string{"provider-a"})
	if err != nil {
		t.Fatalf("acquire provider-a error = %v", err)
	}
	defer releaseA()

	resultChannel := make(chan credentialMutationAcquisition, 1)
	go func() {
		ownedCtx, release, acquireErr := coordinator.with(context.Background(), []string{"provider-b"})
		resultChannel <- credentialMutationAcquisition{ownedCtx: ownedCtx, release: release, err: acquireErr}
	}()
	result := receiveCredentialMutationAcquisition(t, resultChannel)
	if result.err != nil {
		t.Fatalf("acquire provider-b error = %v", result.err)
	}
	result.release()
}

func TestProviderCredentialMutationCoordinatorCancellationDropsWaiterReference(t *testing.T) {
	coordinator := newProviderCredentialMutationCoordinator()
	_, holderRelease, err := coordinator.with(context.Background(), []string{"provider-a"})
	if err != nil {
		t.Fatalf("holder acquire error = %v", err)
	}

	waiterContext, cancelWaiter := context.WithCancel(context.Background())
	waiterResult := make(chan credentialMutationAcquisition, 1)
	go func() {
		ownedCtx, release, acquireErr := coordinator.with(waiterContext, []string{"provider-a"})
		waiterResult <- credentialMutationAcquisition{ownedCtx: ownedCtx, release: release, err: acquireErr}
	}()
	waitForProviderMutationReferences(t, coordinator, "provider-a", 2)
	cancelWaiter()

	result := receiveCredentialMutationAcquisition(t, waiterResult)
	if !errors.Is(result.err, context.Canceled) {
		t.Fatalf("canceled waiter error = %v, want context.Canceled", result.err)
	}
	if result.release != nil {
		t.Fatal("canceled waiter returned a release function")
	}
	waitForProviderMutationReferences(t, coordinator, "provider-a", 1)

	holderRelease()
	assertProviderMutationRegistrySize(t, coordinator, 0)
}

func TestProviderCredentialMutationCoordinatorUsesCanonicalOrder(t *testing.T) {
	coordinator := newProviderCredentialMutationCoordinator()
	_, releaseA, err := coordinator.with(context.Background(), []string{"provider-a"})
	if err != nil {
		t.Fatalf("hold provider-a error = %v", err)
	}

	overlapResult := make(chan credentialMutationAcquisition, 1)
	go func() {
		ownedCtx, release, acquireErr := coordinator.with(
			context.Background(),
			[]string{"provider-b", "provider-a", "provider-b"},
		)
		overlapResult <- credentialMutationAcquisition{ownedCtx: ownedCtx, release: release, err: acquireErr}
	}()
	waitForProviderMutationReferences(t, coordinator, "provider-a", 2)

	// If input order leaked into acquisition, the overlapping waiter would hold B
	// while blocked on A. Acquiring B here proves it waits on sorted A first.
	probeContext, cancelProbe := context.WithTimeout(context.Background(), credentialMutationTestTimeout)
	defer cancelProbe()
	_, releaseB, err := coordinator.with(probeContext, []string{"provider-b"})
	if err != nil {
		t.Fatalf("probe provider-b acquire error = %v", err)
	}
	releaseB()
	releaseA()

	result := receiveCredentialMutationAcquisition(t, overlapResult)
	if result.err != nil {
		t.Fatalf("overlap acquire error = %v", result.err)
	}
	result.release()
	assertProviderMutationRegistrySize(t, coordinator, 0)
}

func TestProviderCredentialMutationCoordinatorReleasesInReverseOrder(t *testing.T) {
	coordinator := newProviderCredentialMutationCoordinator()
	lockA := &providerCredentialMutationLock{permit: make(chan struct{}), references: 1}
	lockB := &providerCredentialMutationLock{permit: make(chan struct{}), references: 1}
	coordinator.providers["provider-a"] = lockA
	coordinator.providers["provider-b"] = lockB

	released := make(chan struct{})
	go func() {
		coordinator.release([]providerCredentialMutationLease{
			{providerID: "provider-a", lock: lockA},
			{providerID: "provider-b", lock: lockB},
		})
		close(released)
	}()

	select {
	case <-lockA.permit:
		t.Fatal("provider-a was released before provider-b")
	case <-lockB.permit:
	case <-time.After(credentialMutationTestTimeout):
		t.Fatal("reverse release did not offer provider-b first")
	}
	select {
	case <-lockA.permit:
	case <-time.After(credentialMutationTestTimeout):
		t.Fatal("reverse release did not proceed to provider-a")
	}
	select {
	case <-released:
	case <-time.After(credentialMutationTestTimeout):
		t.Fatal("reverse release did not complete")
	}
	assertProviderMutationRegistrySize(t, coordinator, 0)
}

func TestProviderCredentialMutationCoordinatorRejectsInvalidInputs(t *testing.T) {
	coordinator := newProviderCredentialMutationCoordinator()
	if ownedCtx, release, err := coordinator.with(nil, []string{"provider-a"}); err == nil || ownedCtx != nil || release != nil {
		t.Fatalf("nil-context acquire = (ctx %v, release present %t, %v), want nil context/release and error", ownedCtx, release != nil, err)
	}

	canceledContext, cancel := context.WithCancel(context.Background())
	cancel()
	if ownedCtx, release, err := coordinator.with(canceledContext, []string{"provider-a"}); !errors.Is(err, context.Canceled) || ownedCtx != nil || release != nil {
		t.Fatalf("pre-canceled acquire = (ctx %v, release present %t, %v), want nil context/release and context.Canceled", ownedCtx, release != nil, err)
	}

	ownedCtx, release, err := coordinator.with(context.Background(), nil)
	if err != nil || ownedCtx == nil || release == nil {
		t.Fatalf("empty acquire = (ctx %v, release present %t, %v), want owned context/release and nil error", ownedCtx, release != nil, err)
	}
	release()
	assertProviderMutationRegistrySize(t, coordinator, 0)

	var uninitialized SQLiteStore
	if ownedCtx, release, err := uninitialized.WithProviderCredentialMutations(
		context.Background(),
		[]string{"provider-a"},
	); err == nil || ownedCtx != nil || release != nil {
		t.Fatalf("uninitialized store acquire = (ctx %v, release present %t, %v), want nil context/release and error", ownedCtx, release != nil, err)
	}
}

func TestProviderCredentialMutationOwnedContextSupportsNestedWritesOnlyWithinLease(t *testing.T) {
	coordinator := newProviderCredentialMutationCoordinator()
	ownedCtx, outerRelease, err := coordinator.with(
		context.Background(),
		[]string{"provider-b", "provider-a"},
	)
	if err != nil {
		t.Fatalf("outer acquire error = %v", err)
	}

	nestedCtx, nestedRelease, err := coordinator.with(ownedCtx, []string{"provider-a"})
	if err != nil {
		t.Fatalf("nested subset acquire error = %v", err)
	}
	if nestedCtx != ownedCtx {
		t.Fatal("nested subset did not reuse the lease-owned context")
	}
	nestedRelease()
	if _, release, err := coordinator.with(ownedCtx, []string{"provider-c"}); err == nil || release != nil {
		t.Fatalf("nested expansion = (release present %t, %v), want nil release and error", release != nil, err)
	}

	outerRelease()
	refreshedCtx, refreshedRelease, err := coordinator.with(ownedCtx, []string{"provider-a"})
	if err != nil {
		t.Fatalf("acquire through released context error = %v", err)
	}
	if refreshedCtx == ownedCtx {
		t.Fatal("released ownership token was accepted as active")
	}
	refreshedRelease()
	assertProviderMutationRegistrySize(t, coordinator, 0)
}

func TestSQLiteCredentialStateWriteReusesOwnedMutationContext(t *testing.T) {
	sqliteStore := setupTestStore(t)
	provider := importTestProvider(t, "provider-a", "account-a", nil)
	if err := sqliteStore.CreateProvider(context.Background(), &provider); err != nil {
		t.Fatalf("CreateProvider() error = %v", err)
	}
	current, err := sqliteStore.GetProvider(context.Background(), provider.ID)
	if err != nil {
		t.Fatalf("GetProvider() error = %v", err)
	}
	credential := current.Credential.Clone()
	credential.SecretData = mustMarshalChatGPTCredentialDataWithTokens(
		t,
		"account-a",
		"next-access",
		"next-refresh",
	)

	ownedCtx, release, err := sqliteStore.WithProviderCredentialMutations(
		context.Background(),
		[]string{provider.ID},
	)
	if err != nil {
		t.Fatalf("WithProviderCredentialMutations() error = %v", err)
	}
	defer release()

	writeResult := make(chan error, 1)
	go func() {
		writeResult <- sqliteStore.UpdateProviderCredentialState(
			ownedCtx,
			provider.ID,
			credential,
			&model.ProviderAuthState{
				Status:    model.ProviderAuthStatusActive,
				AccountID: "account-a",
			},
		)
	}()
	select {
	case err := <-writeResult:
		if err != nil {
			t.Fatalf("nested UpdateProviderCredentialState() error = %v", err)
		}
	case <-time.After(credentialMutationTestTimeout):
		t.Fatal("nested credential state write deadlocked on its owning lease")
	}
}

func TestSQLiteAuthStateWriteWaitsForProviderMutationLease(t *testing.T) {
	sqliteStore := setupTestStore(t)
	provider := importTestProvider(t, "provider-a", "account-a", nil)
	if err := sqliteStore.CreateProvider(context.Background(), &provider); err != nil {
		t.Fatalf("CreateProvider() error = %v", err)
	}
	_, release, err := sqliteStore.WithProviderCredentialMutations(
		context.Background(),
		[]string{provider.ID},
	)
	if err != nil {
		t.Fatalf("WithProviderCredentialMutations() error = %v", err)
	}

	writeResult := make(chan error, 1)
	go func() {
		writeResult <- sqliteStore.UpdateProviderAuthState(
			context.Background(),
			provider.ID,
			&model.ProviderAuthState{Status: model.ProviderAuthStatusActive, AccountID: "account-a"},
		)
	}()
	waitForProviderMutationReferences(t, sqliteStore.credentialMutations, provider.ID, 2)
	select {
	case err := <-writeResult:
		t.Fatalf("auth-state write completed while lease was held: %v", err)
	default:
	}

	release()
	select {
	case err := <-writeResult:
		if err != nil {
			t.Fatalf("UpdateProviderAuthState() error = %v", err)
		}
	case <-time.After(credentialMutationTestTimeout):
		t.Fatal("auth-state write did not resume after mutation lease release")
	}
	assertProviderMutationRegistrySize(t, sqliteStore.credentialMutations, 0)
}

func TestCredentialBindingReplacementLeasesThePreviousOwner(t *testing.T) {
	sqliteStore := setupTestStore(t)
	previous := importTestProvider(t, "previous-provider", "shared-account", nil)
	if err := sqliteStore.CreateProvider(context.Background(), &previous); err != nil {
		t.Fatalf("CreateProvider(previous) error = %v", err)
	}
	_, releasePrevious, err := sqliteStore.WithProviderCredentialMutations(
		context.Background(),
		[]string{previous.ID},
	)
	if err != nil {
		t.Fatalf("hold previous provider error = %v", err)
	}
	t.Cleanup(releasePrevious)

	replacement := importTestProvider(t, "replacement-provider", "shared-account", nil)
	replaceResult := make(chan error, 1)
	go func() {
		replaceResult <- sqliteStore.CreateProvider(
			context.Background(),
			&replacement,
			ProviderWriteOptions{
				CredentialBindingResolution: model.CredentialBindingResolutionReplace,
			},
		)
	}()
	waitForProviderMutationReferences(t, sqliteStore.credentialMutations, previous.ID, 2)
	select {
	case err := <-replaceResult:
		t.Fatalf("binding replacement completed while previous owner lease was held: %v", err)
	default:
	}
	stillOwned, err := sqliteStore.GetProvider(context.Background(), previous.ID)
	if err != nil {
		t.Fatalf("GetProvider(previous while held) error = %v", err)
	}
	if stillOwned.Credential == nil {
		t.Fatal("previous owner credential was cleared outside its mutation lease")
	}

	releasePrevious()
	select {
	case err := <-replaceResult:
		if err != nil {
			t.Fatalf("CreateProvider(replacement) error = %v", err)
		}
	case <-time.After(credentialMutationTestTimeout):
		t.Fatal("binding replacement did not resume after previous owner release")
	}
	previousAfter, err := sqliteStore.GetProvider(context.Background(), previous.ID)
	if err != nil {
		t.Fatalf("GetProvider(previous after replacement) error = %v", err)
	}
	if previousAfter.Credential != nil || previousAfter.AuthState == nil ||
		previousAfter.AuthState.Status != model.ProviderAuthStatusNotConnected {
		t.Fatalf("previous owner state = %#v, want cleared credential and not-connected auth", previousAfter)
	}
	replacementAfter, err := sqliteStore.GetProvider(context.Background(), replacement.ID)
	if err != nil {
		t.Fatalf("GetProvider(replacement) error = %v", err)
	}
	if replacementAfter.Credential == nil || replacementAfter.Credential.BindingAccountID == nil ||
		*replacementAfter.Credential.BindingAccountID != "shared-account" {
		t.Fatalf("replacement credential = %#v, want shared-account binding", replacementAfter.Credential)
	}
	assertProviderMutationRegistrySize(t, sqliteStore.credentialMutations, 0)
}

type credentialMutationForwardingStore struct {
	internal.Store

	mu             sync.Mutex
	receivedCtx    context.Context
	receivedIDs    []string
	releaseInvoked bool
	err            error
}

func (s *credentialMutationForwardingStore) WithProviderCredentialMutations(
	ctx context.Context,
	providerIDs []string,
) (context.Context, func(), error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.receivedCtx = ctx
	s.receivedIDs = append([]string(nil), providerIDs...)
	if s.err != nil {
		return nil, nil, s.err
	}
	ownedCtx := context.WithValue(ctx, forwardedCredentialMutationContextKey{}, "owned")
	return ownedCtx, func() {
		s.mu.Lock()
		s.releaseInvoked = true
		s.mu.Unlock()
	}, nil
}

type credentialMutationUnsupportedStore struct {
	internal.Store
}

func TestCachedStoreWithProviderCredentialMutationsForwards(t *testing.T) {
	forwarding := &credentialMutationForwardingStore{}
	cached := NewCachedStore(CachedStoreConfig{Store: forwarding})
	ctx := context.WithValue(context.Background(), credentialMutationContextKey{}, "operation-id")
	providerIDs := []string{"provider-b", "provider-a"}

	ownedCtx, release, err := cached.WithProviderCredentialMutations(ctx, providerIDs)
	if err != nil {
		t.Fatalf("cached acquire error = %v", err)
	}
	if release == nil {
		t.Fatal("cached acquire returned nil release")
	}
	if got := ownedCtx.Value(forwardedCredentialMutationContextKey{}); got != "owned" {
		t.Fatalf("cached owned context marker = %v, want owned", got)
	}
	release()

	forwarding.mu.Lock()
	defer forwarding.mu.Unlock()
	if forwarding.receivedCtx != ctx {
		t.Fatal("cached store did not forward the original context")
	}
	if !reflect.DeepEqual(forwarding.receivedIDs, providerIDs) {
		t.Fatalf("forwarded IDs = %#v, want %#v", forwarding.receivedIDs, providerIDs)
	}
	if !forwarding.releaseInvoked {
		t.Fatal("cached store did not return the underlying release function")
	}
}

func TestCachedStoreWithProviderCredentialMutationsSharesSQLiteCoordinator(t *testing.T) {
	sqliteStore := setupTestStore(t)
	if sqliteStore.credentialMutations == nil {
		t.Fatal("NewSQLiteStore did not initialize the credential mutation coordinator")
	}
	cached := NewCachedStore(CachedStoreConfig{Store: sqliteStore})
	_, holderRelease, err := sqliteStore.WithProviderCredentialMutations(
		context.Background(),
		[]string{"provider-a"},
	)
	if err != nil {
		t.Fatalf("sqlite acquire error = %v", err)
	}

	waiterContext, cancelWaiter := context.WithCancel(context.Background())
	waiterResult := make(chan credentialMutationAcquisition, 1)
	go func() {
		ownedCtx, release, acquireErr := cached.WithProviderCredentialMutations(
			waiterContext,
			[]string{"provider-a"},
		)
		waiterResult <- credentialMutationAcquisition{ownedCtx: ownedCtx, release: release, err: acquireErr}
	}()
	waitForProviderMutationReferences(t, sqliteStore.credentialMutations, "provider-a", 2)
	cancelWaiter()
	result := receiveCredentialMutationAcquisition(t, waiterResult)
	if !errors.Is(result.err, context.Canceled) {
		t.Fatalf("cached waiter error = %v, want context.Canceled", result.err)
	}
	holderRelease()
	assertProviderMutationRegistrySize(t, sqliteStore.credentialMutations, 0)
}

func TestCachedStoreWithProviderCredentialMutationsReportsUnsupported(t *testing.T) {
	cached := NewCachedStore(CachedStoreConfig{Store: &credentialMutationUnsupportedStore{}})
	ownedCtx, release, err := cached.WithProviderCredentialMutations(
		context.Background(),
		[]string{"provider-a"},
	)
	if err == nil || !strings.Contains(err.Error(), "does not support") {
		t.Fatalf("unsupported acquire error = %v", err)
	}
	if release != nil {
		t.Fatal("unsupported acquire returned a release function")
	}
	if ownedCtx != nil {
		t.Fatal("unsupported acquire returned an owned context")
	}
}

type credentialMutationContextKey struct{}
type forwardedCredentialMutationContextKey struct{}

func waitForProviderMutationReferences(
	t *testing.T,
	coordinator *providerCredentialMutationCoordinator,
	providerID string,
	want int,
) {
	t.Helper()
	deadline := time.Now().Add(credentialMutationTestTimeout)
	for time.Now().Before(deadline) {
		coordinator.mu.Lock()
		lock := coordinator.providers[providerID]
		got := 0
		if lock != nil {
			got = lock.references
		}
		coordinator.mu.Unlock()
		if got == want {
			return
		}
		time.Sleep(credentialMutationTestPollInterval)
	}
	t.Fatalf("provider %q did not reach %d mutation references", providerID, want)
}

func receiveCredentialMutationAcquisition(
	t *testing.T,
	results <-chan credentialMutationAcquisition,
) credentialMutationAcquisition {
	t.Helper()
	select {
	case result := <-results:
		return result
	case <-time.After(credentialMutationTestTimeout):
		t.Fatal("timed out waiting for credential mutation acquisition")
		return credentialMutationAcquisition{}
	}
}

func assertProviderMutationRegistrySize(
	t *testing.T,
	coordinator *providerCredentialMutationCoordinator,
	want int,
) {
	t.Helper()
	coordinator.mu.Lock()
	defer coordinator.mu.Unlock()
	if got := len(coordinator.providers); got != want {
		t.Fatalf("credential mutation registry size = %d, want %d", got, want)
	}
}
