package providerimport

import (
	"context"

	"github.com/doraemonkeys/switch-a/internal/providerauth"
)

type fakeProviderImportService struct {
	preview        *providerauth.ChatGPTProviderImportPreview
	previewErr     error
	sealErr        error
	sealCandidates bool
	candidates     []providerauth.ChatGPTProviderImportCandidate
	claimErr       error
	releaseErr     error
	verifyErr      error
	finalizeErr    error
	cancelErr      error

	previewCalls     int
	previewRaw       []byte
	sealCalls        []string
	sealDispositions [][]providerauth.ChatGPTProviderImportCandidateDisposition
	claimCalls       int
	claimImportIDs   []string
	releaseCalls     []string
	verifyCalls      [][]string
	invalidateCalls  [][]string
	finalizeCalls    []string
	cancelCalls      []string
	events           *[]string
}

func (f *fakeProviderImportService) PreviewSub2APIChatGPTImport(raw []byte) (*providerauth.ChatGPTProviderImportPreview, error) {
	f.previewCalls++
	f.previewRaw = append([]byte(nil), raw...)
	f.recordEvent("preview")
	return f.preview, f.previewErr
}

func (f *fakeProviderImportService) SealChatGPTProviderImportPreview(
	importID string,
	dispositions []providerauth.ChatGPTProviderImportCandidateDisposition,
) error {
	f.sealCalls = append(f.sealCalls, importID)
	f.sealDispositions = append(
		f.sealDispositions,
		append([]providerauth.ChatGPTProviderImportCandidateDisposition(nil), dispositions...),
	)
	f.recordEvent("seal")
	if f.sealCandidates {
		byCandidateID := make(map[string]providerauth.ChatGPTProviderImportCandidateDisposition, len(dispositions))
		for i := range dispositions {
			byCandidateID[dispositions[i].CandidateID] = dispositions[i]
		}
		for i := range f.candidates {
			disposition, ok := byCandidateID[f.candidates[i].CandidateID]
			if ok {
				f.candidates[i].Disposition = &disposition
			}
		}
	}
	return f.sealErr
}

func (f *fakeProviderImportService) ClaimChatGPTProviderImport(
	importID string,
) ([]providerauth.ChatGPTProviderImportCandidate, error) {
	f.claimCalls++
	f.claimImportIDs = append(f.claimImportIDs, importID)
	f.recordEvent("claim")
	return append([]providerauth.ChatGPTProviderImportCandidate(nil), f.candidates...), f.claimErr
}

func (f *fakeProviderImportService) ReleaseChatGPTProviderImportClaim(importID string) error {
	f.releaseCalls = append(f.releaseCalls, importID)
	f.recordEvent("release")
	return f.releaseErr
}

func (f *fakeProviderImportService) VerifyChatGPTProviderImportCandidates(
	_ context.Context,
	candidates []providerauth.ChatGPTProviderImportCandidate,
) error {
	candidateIDs := make([]string, 0, len(candidates))
	for i := range candidates {
		candidateIDs = append(candidateIDs, candidates[i].CandidateID)
	}
	f.verifyCalls = append(f.verifyCalls, candidateIDs)
	f.recordEvent("verify")
	return f.verifyErr
}

func (f *fakeProviderImportService) InvalidateProviderCredentialSessions(providerIDs []string) {
	f.invalidateCalls = append(f.invalidateCalls, append([]string(nil), providerIDs...))
	f.recordEvent("invalidate")
}

func (f *fakeProviderImportService) FinalizeChatGPTProviderImport(importID string) error {
	f.finalizeCalls = append(f.finalizeCalls, importID)
	f.recordEvent("finalize")
	return f.finalizeErr
}

func (f *fakeProviderImportService) CancelChatGPTProviderImport(importID string) error {
	f.cancelCalls = append(f.cancelCalls, importID)
	f.recordEvent("cancel")
	return f.cancelErr
}

func (f *fakeProviderImportService) recordEvent(event string) {
	if f.events != nil {
		*f.events = append(*f.events, event)
	}
}
