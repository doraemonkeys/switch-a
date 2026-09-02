package providerauth

import (
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestPreviewSub2APIChatGPTImport_BoundsOversizedInvalidNameRetention(t *testing.T) {
	now := time.Date(2026, time.July, 30, 12, 0, 0, 0, time.UTC)
	accountID := "acct-bounded-name"
	valid := sub2APIImportAccount(
		"Importable",
		chatgptAccessJWT(t, accountID, "bounded@example.com", "plus", now.Add(time.Hour)),
		"refresh-bounded",
		chatgptAuthJWT(t, accountID, "bounded@example.com", "plus", now.Add(time.Hour)),
		accountID,
		4,
		1,
	)
	oversizedName := strings.Repeat("n", 4_900_000)
	raw := marshalSub2APIImportDocument(t, []any{valid, map[string]any{"name": oversizedName}}, nil)
	if len(raw) >= maxChatGPTProviderImportDocumentBytes {
		t.Fatalf("regression document = %d bytes, want below %d", len(raw), maxChatGPTProviderImportDocumentBytes)
	}
	service, _ := newSub2APIImportTestService(t, now, "candidate-valid", "candidate-name", "import-name")

	preview, err := service.PreviewSub2APIChatGPTImport(raw)
	if err != nil {
		t.Fatalf("PreviewSub2APIChatGPTImport returned error: %v", err)
	}
	if preview.Summary != (ChatGPTProviderImportSummary{Total: 2, Ready: 1, Invalid: 1}) {
		t.Fatalf("Summary = %#v, want one ready and one invalid", preview.Summary)
	}
	if len(preview.Items[1].Name) > maxSub2APIAccountNameCharacters || strings.Contains(preview.Items[1].Name, oversizedName) {
		t.Fatalf("invalid preview retained oversized name (%d bytes)", len(preview.Items[1].Name))
	}
	previewJSON, err := json.Marshal(preview)
	if err != nil {
		t.Fatalf("json.Marshal preview returned error: %v", err)
	}
	if len(previewJSON) > 32<<10 {
		t.Fatalf("preview JSON retained %d bytes from oversized source name", len(previewJSON))
	}
	sealSub2APIImportWithSourceDispositions(t, service, preview)
	candidates, err := service.ClaimChatGPTProviderImport(preview.ImportID)
	if err != nil {
		t.Fatalf("ClaimChatGPTProviderImport returned error: %v", err)
	}
	if len(candidates[1].Name) > maxSub2APIAccountNameCharacters {
		t.Fatalf("staged invalid candidate retained %d name bytes", len(candidates[1].Name))
	}
}

func TestPreviewSub2APIChatGPTImport_RejectsOversizedCredentialSourcesBeforeEncoding(t *testing.T) {
	now := time.Date(2026, time.July, 30, 12, 0, 0, 0, time.UTC)
	accountID := "acct-sized-token"
	validAccess := chatgptAccessJWT(t, accountID, "sized@example.com", "plus", now.Add(time.Hour))
	validID := chatgptAuthJWT(t, accountID, "sized@example.com", "plus", now.Add(time.Hour))
	oversizedRefresh := strings.Repeat("<", maxChatGPTProviderImportRefreshTokenBytes+1)
	oversizedAccess := strings.Repeat("a", maxChatGPTProviderImportCompactJWSBytes+1) + ".b.c"
	accounts := []any{
		sub2APIImportAccount("Refresh amplification", validAccess, oversizedRefresh, validID, accountID, 1, 0),
		sub2APIImportAccount("Oversized access", oversizedAccess, "refresh-small", validID, accountID, 1, 0),
	}
	service, _ := newSub2APIImportTestService(t, now, "candidate-refresh", "candidate-access", "import-sized-token")
	preview, err := service.PreviewSub2APIChatGPTImport(marshalSub2APIImportDocument(t, accounts, nil))
	if err != nil {
		t.Fatalf("PreviewSub2APIChatGPTImport returned error: %v", err)
	}
	if preview.Summary != (ChatGPTProviderImportSummary{Total: 2, Invalid: 2}) {
		t.Fatalf("Summary = %#v, want two invalid oversized credentials", preview.Summary)
	}
	encoded, err := json.Marshal(preview)
	if err != nil {
		t.Fatalf("json.Marshal preview returned error: %v", err)
	}
	if strings.Contains(string(encoded), oversizedRefresh) || strings.Contains(string(encoded), oversizedAccess) {
		t.Fatal("preview echoed an oversized credential source")
	}
	sealSub2APIImportWithSourceDispositions(t, service, preview)
	candidates, err := service.ClaimChatGPTProviderImport(preview.ImportID)
	if err != nil {
		t.Fatalf("ClaimChatGPTProviderImport returned error: %v", err)
	}
	for _, candidate := range candidates {
		if candidate.Credential.Kind != "" || candidate.Credential.SecretData != "" {
			t.Fatalf("oversized candidate retained credential session: %#v", candidate)
		}
	}
}

func TestPreviewSub2APIChatGPTImport_StopsAtAccountLimitWithoutMaterializingRemainder(t *testing.T) {
	var document strings.Builder
	document.Grow(5_100_000)
	document.WriteString(`{"accounts":[`)
	for index := range 1_000_000 {
		if index > 0 {
			document.WriteByte(',')
		}
		document.WriteString("null")
	}
	document.WriteString("]}")
	raw := []byte(document.String())
	if len(raw) >= maxChatGPTProviderImportDocumentBytes {
		t.Fatalf("regression document = %d bytes, want below %d", len(raw), maxChatGPTProviderImportDocumentBytes)
	}
	service := NewService(Config{Clock: fixedClock{now: time.Date(2026, time.July, 30, 12, 0, 0, 0, time.UTC)}})

	_, err := service.PreviewSub2APIChatGPTImport(raw)
	if !errors.Is(err, ErrChatGPTProviderImportInvalidDocument) || !strings.Contains(err.Error(), "accounts exceeds limit") {
		t.Fatalf("error = %v, want bounded account-limit rejection", err)
	}
	service.mu.Lock()
	slots, retainedBytes := service.providerImportSlots, service.providerImportBytes
	service.mu.Unlock()
	if slots != 0 || retainedBytes != 0 {
		t.Fatalf("failed parse retained capacity (%d slots, %d bytes)", slots, retainedBytes)
	}
}

func TestDecodeSub2APIExportDocument_PreservesDecoderCause(t *testing.T) {
	_, err := decodeSub2APIExportDocument([]byte(`{"accounts":[{"name":}]}`))
	if !errors.Is(err, ErrChatGPTProviderImportInvalidDocument) {
		t.Fatalf("error = %v, want invalid-document classification", err)
	}
	var syntaxError *json.SyntaxError
	if !errors.As(err, &syntaxError) {
		t.Fatalf("error = %v, want wrapped *json.SyntaxError", err)
	}
}

func TestChatGPTProviderImportLifecycle_ClaimSurvivesExpiryUntilReleased(t *testing.T) {
	now := time.Date(2026, time.July, 30, 12, 0, 0, 0, time.UTC)
	clock := &mutableClock{now: now}
	scheduler := &manualScheduler{}
	service := newCallbackLifecycleTestService(Config{
		Clock:       clock,
		IDGenerator: &importTestIDGenerator{ids: []string{"candidate-claimed", "import-claimed"}},
	}, &recordingCallbackEndpoint{}, scheduler)
	account := sub2APIImportAccount(
		"Claimed",
		chatgptAccessJWT(t, "acct-claimed", "claimed@example.com", "plus", now.Add(time.Hour)),
		"refresh-claimed",
		chatgptAuthJWT(t, "acct-claimed", "claimed@example.com", "plus", now.Add(time.Hour)),
		"acct-claimed",
		1,
		0,
	)
	preview, err := service.PreviewSub2APIChatGPTImport(marshalSub2APIImportDocument(t, []any{account}, nil))
	if err != nil {
		t.Fatalf("PreviewSub2APIChatGPTImport returned error: %v", err)
	}
	expiry := scheduler.latest(t)
	sealSub2APIImportWithSourceDispositions(t, service, preview)
	if _, err := service.ClaimChatGPTProviderImport(preview.ImportID); err != nil {
		t.Fatalf("ClaimChatGPTProviderImport returned error: %v", err)
	}
	if !expiry.isStopped() {
		t.Fatal("claim left a zero-delay expiry task active")
	}
	clock.Advance(chatGPTProviderImportSessionTTL)
	expiry.RunAfterStopRace()
	service.mu.Lock()
	_, retained := service.providerImports[preview.ImportID]
	service.mu.Unlock()
	if !retained {
		t.Fatal("expiry race destroyed a claimed draft")
	}
	if err := service.CancelChatGPTProviderImport(preview.ImportID); !errors.Is(err, ErrChatGPTProviderImportInProgress) {
		t.Fatalf("cancel error = %v, want ErrChatGPTProviderImportInProgress", err)
	}
	if err := service.ReleaseChatGPTProviderImportClaim(preview.ImportID); err != nil {
		t.Fatalf("ReleaseChatGPTProviderImportClaim returned error: %v", err)
	}
	service.mu.Lock()
	slots, retainedBytes := service.providerImportSlots, service.providerImportBytes
	service.mu.Unlock()
	if slots != 0 || retainedBytes != 0 {
		t.Fatalf("expired release retained capacity (%d slots, %d bytes)", slots, retainedBytes)
	}
}

func TestChatGPTProviderImportLifecycle_ClaimAndCancelRaceIsAtomic(t *testing.T) {
	service := NewService(Config{Clock: fixedClock{now: time.Date(2026, time.July, 30, 12, 0, 0, 0, time.UTC)}})
	for iteration := range 64 {
		service.mu.Lock()
		service.providerImports["race-import"] = stagedChatGPTProviderImport{
			expiresAt: time.Date(2026, time.July, 30, 13, 0, 0, 0, time.UTC),
			order:     []string{"race-candidate"},
			candidates: map[string]ChatGPTProviderImportCandidate{
				"race-candidate": {CandidateID: "race-candidate"},
			},
			sealed: true, sizeBytes: 1,
		}
		service.providerImportSlots, service.providerImportBytes = 1, 1
		service.mu.Unlock()

		start := make(chan struct{})
		var claimErr, cancelErr error
		var wait sync.WaitGroup
		wait.Add(2)
		go func() { defer wait.Done(); <-start; _, claimErr = service.ClaimChatGPTProviderImport("race-import") }()
		go func() { defer wait.Done(); <-start; cancelErr = service.CancelChatGPTProviderImport("race-import") }()
		close(start)
		wait.Wait()
		if claimErr == nil {
			if !errors.Is(cancelErr, ErrChatGPTProviderImportInProgress) {
				t.Fatalf("iteration %d cancel error = %v, want in-progress conflict", iteration, cancelErr)
			}
			if err := service.FinalizeChatGPTProviderImport("race-import"); err != nil {
				t.Fatalf("iteration %d finalize returned error: %v", iteration, err)
			}
		} else if !errors.Is(claimErr, ErrChatGPTProviderImportNotFound) || cancelErr != nil {
			t.Fatalf("iteration %d unexpected race results claim=%v cancel=%v", iteration, claimErr, cancelErr)
		}
		service.mu.Lock()
		slots, retainedBytes := service.providerImportSlots, service.providerImportBytes
		service.mu.Unlock()
		if slots != 0 || retainedBytes != 0 {
			t.Fatalf("iteration %d retained capacity (%d slots, %d bytes)", iteration, slots, retainedBytes)
		}
	}
}

func TestChatGPTProviderImportCapacity_BoundsSlotsAndAggregateBytes(t *testing.T) {
	service := NewService(Config{Clock: fixedClock{now: time.Date(2026, time.July, 30, 12, 0, 0, 0, time.UTC)}})
	if err := service.reserveChatGPTProviderImportCapacity(maxChatGPTProviderImportDocumentBytes + 1); !errors.Is(err, ErrChatGPTProviderImportCapacityExceeded) {
		t.Fatalf("oversized document error = %v, want capacity exceeded", err)
	}
	for index := range maxActiveChatGPTProviderImportDrafts {
		if err := service.reserveChatGPTProviderImportCapacity(1); err != nil {
			t.Fatalf("slot reservation %d returned error: %v", index, err)
		}
	}
	if err := service.reserveChatGPTProviderImportCapacity(1); !errors.Is(err, ErrChatGPTProviderImportCapacityExceeded) {
		t.Fatalf("slot overflow error = %v, want capacity exceeded", err)
	}
	for range maxActiveChatGPTProviderImportDrafts {
		service.releaseChatGPTProviderImportReservation(1)
	}
	const reservationBytes = 2 << 20
	reservations := maxAggregateChatGPTProviderImportBytes / reservationBytes
	for index := range reservations {
		if err := service.reserveChatGPTProviderImportCapacity(reservationBytes); err != nil {
			t.Fatalf("aggregate reservation %d returned error: %v", index, err)
		}
	}
	if err := service.reserveChatGPTProviderImportCapacity(1); !errors.Is(err, ErrChatGPTProviderImportCapacityExceeded) {
		t.Fatalf("aggregate overflow error = %v, want capacity exceeded", err)
	}
	for range reservations {
		service.releaseChatGPTProviderImportReservation(reservationBytes)
	}
	service.mu.Lock()
	slots, retainedBytes := service.providerImportSlots, service.providerImportBytes
	service.mu.Unlock()
	if slots != 0 || retainedBytes != 0 {
		t.Fatalf("released reservations retained capacity (%d slots, %d bytes)", slots, retainedBytes)
	}
}
