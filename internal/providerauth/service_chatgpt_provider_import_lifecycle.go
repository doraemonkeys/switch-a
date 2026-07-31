package providerauth

import (
	"fmt"
	"strings"
	"time"

	"go.uber.org/zap"
)

const (
	// One admin process should never retain an unbounded collection of tiny drafts,
	// while 32 slots still leave ample room for overlapping review tabs. Documents
	// are already capped at 5 MiB; a 32 MiB aggregate keeps worst-case credential
	// retention predictable without forcing ordinary operators to serialize work.
	maxActiveChatGPTProviderImportDrafts   = 32
	maxChatGPTProviderImportDocumentBytes  = 5 << 20
	maxAggregateChatGPTProviderImportBytes = 32 << 20
)

func (s *Service) reserveChatGPTProviderImportCapacity(rawBytes int64) error {
	if rawBytes > maxChatGPTProviderImportDocumentBytes {
		return fmt.Errorf(
			"%w: document exceeds %d bytes",
			ErrChatGPTProviderImportCapacityExceeded,
			maxChatGPTProviderImportDocumentBytes,
		)
	}

	now := s.clock.Now()
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.shutdown {
		return errProviderAuthServiceShutdown
	}
	s.pruneExpiredProviderImportsLocked(now)
	if s.providerImportSlots >= maxActiveChatGPTProviderImportDrafts {
		return fmt.Errorf(
			"%w: active draft limit %d reached",
			ErrChatGPTProviderImportCapacityExceeded,
			maxActiveChatGPTProviderImportDrafts,
		)
	}
	if rawBytes > maxAggregateChatGPTProviderImportBytes-s.providerImportBytes {
		return fmt.Errorf(
			"%w: aggregate draft memory limit reached",
			ErrChatGPTProviderImportCapacityExceeded,
		)
	}
	s.providerImportSlots++
	s.providerImportBytes += rawBytes
	s.syncSessionExpiryTaskLocked(now)
	return nil
}

func (s *Service) releaseChatGPTProviderImportReservation(rawBytes int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.providerImportSlots > 0 {
		s.providerImportSlots--
	}
	if rawBytes >= s.providerImportBytes {
		s.providerImportBytes = 0
	} else {
		s.providerImportBytes -= rawBytes
	}
}

func (s *Service) replaceChatGPTProviderImportReservationLocked(rawBytes, retainedBytes int64) error {
	baseBytes := s.providerImportBytes - rawBytes
	if baseBytes < 0 {
		return fmt.Errorf("provider import reservation accounting is inconsistent")
	}
	if retainedBytes > maxAggregateChatGPTProviderImportBytes-baseBytes {
		return fmt.Errorf(
			"%w: aggregate draft memory limit reached",
			ErrChatGPTProviderImportCapacityExceeded,
		)
	}
	s.providerImportBytes = baseBytes + retainedBytes
	return nil
}

func chatGPTProviderImportSecretBytes(candidates []ChatGPTProviderImportCandidate) int64 {
	var total int64
	for _, candidate := range candidates {
		if candidate.Credential != nil {
			total += int64(len(candidate.Credential.SecretData))
		}
	}
	return total
}

// ClaimChatGPTProviderImport atomically authorizes one commit attempt. Once a
// claim exists, cancellation and expiry cannot invalidate credentials already
// handed to that attempt; only release or successful finalization may transition it.
func (s *Service) ClaimChatGPTProviderImport(
	importID string,
) ([]ChatGPTProviderImportCandidate, error) {
	trimmedImportID := strings.TrimSpace(importID)
	if trimmedImportID == "" {
		return nil, ErrChatGPTProviderImportNotFound
	}

	now := s.clock.Now()
	s.mu.Lock()
	staged, ok := s.providerImports[trimmedImportID]
	if ok && !staged.claimed && !staged.expiresAt.After(now) {
		s.deleteChatGPTProviderImportLocked(trimmedImportID)
		s.syncSessionExpiryTaskLocked(now)
		s.mu.Unlock()
		return nil, fmt.Errorf("%w: %s", ErrChatGPTProviderImportExpired, trimmedImportID)
	}
	if !ok {
		s.mu.Unlock()
		return nil, fmt.Errorf("%w: %s", ErrChatGPTProviderImportNotFound, trimmedImportID)
	}
	if !staged.sealed {
		s.mu.Unlock()
		return nil, fmt.Errorf("%w: %s", ErrChatGPTProviderImportPreviewNotSealed, trimmedImportID)
	}
	if staged.claimed {
		s.mu.Unlock()
		return nil, fmt.Errorf("%w: %s", ErrChatGPTProviderImportInProgress, trimmedImportID)
	}

	result := make([]ChatGPTProviderImportCandidate, 0, len(staged.order))
	for _, candidateID := range staged.order {
		candidate, exists := staged.candidates[candidateID]
		if !exists {
			s.mu.Unlock()
			return nil, fmt.Errorf("%w: staged candidate %s is missing", ErrChatGPTProviderImportInvalidCandidate, candidateID)
		}
		result = append(result, cloneChatGPTProviderImportCandidate(candidate))
	}
	staged.claimed = true
	s.providerImports[trimmedImportID] = staged
	s.syncSessionExpiryTaskLocked(now)
	s.mu.Unlock()

	s.logger.Info("claimed chatgpt provider import", zap.String("import_id", trimmedImportID))
	return result, nil
}

// ReleaseChatGPTProviderImportClaim makes a failed pre-commit attempt retryable.
// If its original review window elapsed while claimed, release destroys it instead.
func (s *Service) ReleaseChatGPTProviderImportClaim(importID string) error {
	trimmedImportID := strings.TrimSpace(importID)
	if trimmedImportID == "" {
		return ErrChatGPTProviderImportNotFound
	}

	now := s.clock.Now()
	s.mu.Lock()
	staged, ok := s.providerImports[trimmedImportID]
	if !ok {
		s.mu.Unlock()
		return fmt.Errorf("%w: %s", ErrChatGPTProviderImportNotFound, trimmedImportID)
	}
	if !staged.claimed {
		s.mu.Unlock()
		return nil
	}
	staged.claimed = false
	if !staged.expiresAt.After(now) {
		s.deleteChatGPTProviderImportLocked(trimmedImportID)
	} else {
		s.providerImports[trimmedImportID] = staged
	}
	s.syncSessionExpiryTaskLocked(now)
	s.mu.Unlock()

	s.logger.Debug("released chatgpt provider import claim", zap.String("import_id", trimmedImportID))
	return nil
}

// FinalizeChatGPTProviderImport consumes a claimed draft only after its durable
// transaction succeeds. The claim remains valid even if the review TTL elapsed
// while the transaction was in progress.
func (s *Service) FinalizeChatGPTProviderImport(importID string) error {
	trimmedImportID := strings.TrimSpace(importID)
	if trimmedImportID == "" {
		return ErrChatGPTProviderImportNotFound
	}

	now := s.clock.Now()
	s.mu.Lock()
	staged, ok := s.providerImports[trimmedImportID]
	if ok && !staged.claimed && !staged.expiresAt.After(now) {
		s.deleteChatGPTProviderImportLocked(trimmedImportID)
		s.syncSessionExpiryTaskLocked(now)
		s.mu.Unlock()
		return fmt.Errorf("%w: %s", ErrChatGPTProviderImportExpired, trimmedImportID)
	}
	if !ok {
		s.mu.Unlock()
		return fmt.Errorf("%w: %s", ErrChatGPTProviderImportNotFound, trimmedImportID)
	}
	if !staged.claimed {
		s.mu.Unlock()
		return fmt.Errorf("%w: %s", ErrChatGPTProviderImportNotClaimed, trimmedImportID)
	}
	s.deleteChatGPTProviderImportLocked(trimmedImportID)
	s.syncSessionExpiryTaskLocked(now)
	s.mu.Unlock()

	s.logger.Info("finalized chatgpt provider import", zap.String("import_id", trimmedImportID))
	return nil
}

// CancelChatGPTProviderImport is idempotent for idle drafts, but a claimed draft
// returns a conflict so the UI never reports cancellation while a commit may write.
func (s *Service) CancelChatGPTProviderImport(importID string) error {
	trimmedImportID := strings.TrimSpace(importID)
	if trimmedImportID == "" {
		return nil
	}

	s.mu.Lock()
	staged, ok := s.providerImports[trimmedImportID]
	if ok && staged.claimed {
		s.mu.Unlock()
		return fmt.Errorf("%w: %s", ErrChatGPTProviderImportInProgress, trimmedImportID)
	}
	s.deleteChatGPTProviderImportLocked(trimmedImportID)
	s.syncSessionExpiryTaskLocked(s.clock.Now())
	s.mu.Unlock()

	s.logger.Debug("cancelled chatgpt provider import", zap.String("import_id", trimmedImportID))
	return nil
}

func (s *Service) deleteChatGPTProviderImportLocked(importID string) bool {
	staged, ok := s.providerImports[importID]
	if !ok {
		return false
	}
	delete(s.providerImports, importID)
	if s.providerImportSlots > 0 {
		s.providerImportSlots--
	}
	if staged.sizeBytes >= s.providerImportBytes {
		s.providerImportBytes = 0
	} else {
		s.providerImportBytes -= staged.sizeBytes
	}
	return true
}

func (s *Service) pruneExpiredProviderImportsLocked(now time.Time) {
	for importID, staged := range s.providerImports {
		if !staged.claimed && !staged.expiresAt.After(now) {
			s.deleteChatGPTProviderImportLocked(importID)
		}
	}
}
