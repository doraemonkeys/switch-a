package requestcapture

import (
	"context"
	"math"
	"runtime"
	"strings"
	"time"

	"go.uber.org/zap"
)

func (m *Manager) Enabled() bool {
	return m != nil && m.active.Load() != nil
}

func (m *Manager) BeginGateway(input GatewayStart) GatewayRecorder {
	s := m.retainActive(0)
	if s == nil {
		return GatewayRecorder{}
	}
	recorder := s.beginGateway(input)
	_ = s.releaseOwner()
	return recorder
}

// retainActive transfers a temporary session owner while publication is
// protected against detachment. The first atomic read preserves the allocation-
// free disabled fast path; only the second read under accessGate may be used.
func (m *Manager) retainActive(generation uint64) *sessionState {
	if m == nil || m.active.Load() == nil {
		return nil
	}
	m.accessGate.RLock()
	session := m.active.Load()
	if session == nil || (generation != 0 && session.generation != generation) ||
		!session.retainOwner() {
		m.accessGate.RUnlock()
		return nil
	}
	m.accessGate.RUnlock()
	return session
}

func (m *Manager) Start(request StartRequest) (SessionInfo, error) {
	if m == nil {
		return SessionInfo{}, ErrManagerClosed
	}
	shape, err := m.scanStart(request)
	if err != nil {
		return SessionInfo{}, err
	}

	generation, admissionErr := m.claimStartAdmission()
	if admissionErr != nil {
		return SessionInfo{}, admissionErr
	}

	statusCharge := statusSlotBaseChargeBytes + int64(shape.statusJSONBytes)
	baseCharge := sessionRootChargeBytes + shape.providerBytes +
		int64(len(request.Providers))*(2*mapEntryChargeBytes+sliceEntryChargeBytes) + statusCharge
	baseCharge = addRetainedCharge64(
		baseCharge,
		int64(len(request.Providers))*providerRecordIndexChargeBytes,
	)
	baseCharge = addRetainedCharge64(baseCharge, shape.handleSlots.charge)
	var allocation startAllocation
	if !m.beginStartAllocation(shape.quotaBytes, baseCharge, &allocation) {
		m.releaseStartAdmission()
		return SessionInfo{}, ErrCapacityExceeded
	}

	generatedID, generateErr := m.cfg.idGenerator.NewID()
	if generateErr != nil {
		_ = allocation.rollback()
		m.releaseStartAdmission()
		m.cfg.logger.Error("request capture session ID generation failed",
			zap.Uint64("generation", generation),
		)
		return SessionInfo{}, ErrInternalFailure
	}
	startedAt := m.cfg.clock.WallNow()
	s := m.materializeStartCandidate(request, shape, generation, generatedID, startedAt, statusCharge)
	if s == nil {
		_ = allocation.rollback()
		m.releaseStartAdmission()
		return SessionInfo{}, ErrCapacityExceeded
	}
	// The response owns a detached graph before publication, so an immediate Stop
	// cannot race its construction or erase provider metadata under the caller.
	info := s.info()
	var statusMutation statusEpochMutation
	if !m.beginStatusEpochMutation(&statusMutation) {
		discardUnpublishedSessionCandidate(s)
		_ = allocation.rollback()
		m.releaseStartAdmission()
		return SessionInfo{}, ErrInternalFailure
	}

	m.lifecycleMu.Lock()
	switch {
	case m.closed:
		m.starting = false
		m.lifecycleMu.Unlock()
		_ = statusMutation.finish()
		discardUnpublishedSessionCandidate(s)
		_ = allocation.rollback()
		return SessionInfo{}, ErrManagerClosed
	case m.active.Load() != nil || !m.starting || m.generation+1 != generation:
		m.starting = false
		m.lifecycleMu.Unlock()
		_ = statusMutation.finish()
		discardUnpublishedSessionCandidate(s)
		_ = allocation.rollback()
		return SessionInfo{}, ErrSessionActive
	}
	if !allocation.commit(s) {
		m.starting = false
		m.lifecycleMu.Unlock()
		_ = statusMutation.finish()
		discardUnpublishedSessionCandidate(s)
		_ = allocation.rollback()
		return SessionInfo{}, ErrCapacityExceeded
	}
	m.accessGate.Lock()
	m.active.Store(s)
	m.accessGate.Unlock()
	m.starting = false
	m.lifecycleMu.Unlock()
	_ = statusMutation.finish()

	m.cfg.logger.Info("request capture session started",
		zap.String("session_id", info.SessionID),
		zap.Uint64("generation", info.Generation),
		zap.Int("provider_count", len(info.ProviderIDs)),
		zap.Int("completed_records_per_provider", info.CompletedRecordsPerProvider),
		zap.Int64("retained_bytes_limit", info.RetainedBytesLimit),
	)
	return info, nil
}

func (m *Manager) claimStartAdmission() (uint64, error) {
	m.lifecycleMu.Lock()
	defer m.lifecycleMu.Unlock()
	switch {
	case m.closed:
		return 0, ErrManagerClosed
	case m.active.Load() != nil || m.starting:
		return 0, ErrSessionActive
	case m.generation == math.MaxUint64:
		return 0, ErrGenerationExhausted
	}
	m.starting = true
	return m.generation + 1, nil
}

func (m *Manager) releaseStartAdmission() {
	m.lifecycleMu.Lock()
	m.starting = false
	m.lifecycleMu.Unlock()
}

func (m *Manager) materializeStartCandidate(
	request StartRequest,
	shape startShape,
	generation uint64,
	generatedID [16]byte,
	startedAt time.Time,
	statusCharge int64,
) *sessionState {
	providers := make([]ProviderIdentity, len(request.Providers))
	for index, provider := range request.Providers {
		providers[index] = ProviderIdentity{
			ID:   strings.Clone(provider.ID),
			Name: strings.Clone(provider.Name),
		}
	}
	session := &sessionState{
		manager:            m,
		queryDone:          make(chan struct{}),
		id:                 makeSessionID(generation, generatedID),
		generation:         generation,
		startedAt:          startedAt.UnixNano(),
		accepting:          true,
		exportAdmission:    make(chan struct{}, 1),
		recordsPerProvider: shape.recordsPerProvider,
		quotaBytes:         shape.quotaBytes,
		statusCharge:       statusCharge,
	}
	session.statusSlot = &statusJSONSlot{
		storage: make([]byte, shape.statusJSONBytes),
		charge:  statusCharge,
		session: session,
	}
	if !initializeHandleSlots(session, shape.handleSlots) {
		discardUnpublishedSessionCandidate(session)
		return nil
	}
	session.providers = make(map[string]ProviderIdentity, len(providers))
	session.providerOrder = providers
	session.providerRecordIndex = make([]providerRecordIndex, len(providers))
	session.providerRecords = make(map[string]*providerRecordIndex, len(providers))
	for index, provider := range providers {
		session.providers[provider.ID] = provider
		session.providerRecords[provider.ID] = &session.providerRecordIndex[index]
	}
	return session
}

func (m *Manager) scanStart(request StartRequest) (startShape, error) {
	if !request.AcknowledgeRawPayloadRisk {
		return startShape{}, &ValidationError{Field: "acknowledge_raw_payload_risk", Reason: "must be true"}
	}
	if len(request.Providers) == 0 {
		return startShape{}, &ValidationError{Field: "providers", Reason: "must contain at least one provider"}
	}
	if len(request.Providers) > maxRetainedProviders {
		return startShape{}, &ValidationError{Field: "providers", Reason: "exceeds the supported provider count"}
	}
	recordsPerProvider := request.CompletedRecordsPerProvider
	if recordsPerProvider == 0 {
		recordsPerProvider = m.cfg.defaultRecordsPerProvider
	}
	if recordsPerProvider < 1 || recordsPerProvider > m.cfg.maxRecordsPerProvider {
		return startShape{}, &ValidationError{Field: "completed_records_per_provider", Reason: "is outside the configured range"}
	}
	quotaBytes := request.RetainedBytesLimit
	if quotaBytes == 0 {
		quotaBytes = m.cfg.defaultSessionQuotaBytes
	}
	if quotaBytes <= 0 || quotaBytes > m.cfg.processCeilingBytes {
		return startShape{}, &ValidationError{Field: "retained_bytes_limit", Reason: "must be positive and not exceed the process ceiling"}
	}
	providerBytes := int64(0)
	for index, provider := range request.Providers {
		if len(provider.ID) > maxRetainedProviderIDBytes || len(provider.Name) > maxRetainedProviderNameBytes {
			return startShape{}, &ValidationError{Field: "providers", Reason: "provider identity exceeds retained metadata limits"}
		}
		if provider.ID != strings.TrimSpace(provider.ID) ||
			provider.Name != strings.TrimSpace(provider.Name) {
			return startShape{}, &ValidationError{
				Field:  "providers",
				Reason: "provider identities must use canonical whitespace",
			}
		}
		if provider.ID == "" {
			return startShape{}, &ValidationError{Field: "providers", Reason: "provider ID at index is empty"}
		}
		for previous := 0; previous < index; previous++ {
			if provider.ID == request.Providers[previous].ID {
				return startShape{}, &ValidationError{Field: "providers", Reason: "provider IDs must be unique"}
			}
		}
		addition := int64(len(provider.ID) + len(provider.Name))
		if addition > math.MaxInt64-providerBytes {
			return startShape{}, ErrCapacityExceeded
		}
		providerBytes += addition
	}
	statusJSONBytes, statusSizeValid := statusJSONCapacity(request.Providers)
	if !statusSizeValid {
		return startShape{}, ErrCapacityExceeded
	}
	handleSlots, handleSlotsValid := scanHandleSlotShape(
		len(request.Providers), recordsPerProvider, m.cfg.maxActiveTraces, m.cfg.maxActiveRecords,
	)
	if !handleSlotsValid {
		return startShape{}, ErrCapacityExceeded
	}
	return startShape{
		recordsPerProvider: recordsPerProvider,
		quotaBytes:         quotaBytes,
		providerBytes:      providerBytes,
		statusJSONBytes:    statusJSONBytes,
		handleSlots:        handleSlots,
	}, nil
}

func (m *Manager) Stop(sessionID string) error {
	if m == nil {
		return ErrNoActiveSession
	}
	var statusMutation statusEpochMutation
	if !m.beginStatusEpochMutation(&statusMutation) {
		return ErrInternalFailure
	}
	m.lifecycleMu.Lock()
	m.accessGate.Lock()
	s := m.active.Load()
	if s == nil {
		m.accessGate.Unlock()
		m.lifecycleMu.Unlock()
		_ = statusMutation.finish()
		return ErrNoActiveSession
	}
	if sessionID != s.id {
		m.accessGate.Unlock()
		m.lifecycleMu.Unlock()
		_ = statusMutation.finish()
		return ErrSessionMismatch
	}
	m.active.Store(nil)
	m.accessGate.Unlock()
	m.lifecycleMu.Unlock()
	_ = statusMutation.finish()

	// Query and export cancellation become visible immediately after logical
	// detachment, before teardown can wait on either session lock.
	s.cancelQueries()
	m.cancelSessionExports(s.id)
	stoppedID := s.id
	stoppedGeneration := s.generation
	s.stop()
	m.cfg.logger.Info("request capture session stopped",
		zap.String("session_id", stoppedID),
		zap.Uint64("generation", stoppedGeneration),
	)
	return nil
}

func (m *Manager) Status() Status {
	if m == nil {
		return Status{State: SessionStateStopped}
	}
	for {
		epoch := m.statusEpoch.Load()
		if epoch&1 != 0 {
			runtime.Gosched()
			continue
		}
		m.exportMu.Lock()
		pendingExports := m.pendingExportCountLocked()
		activeDownloads := m.activeDownloads
		m.exportMu.Unlock()

		s := m.retainActive(0)
		if s == nil {
			m.mu.Lock()
			status := Status{
				State:               SessionStateStopped,
				ProcessMemory:       m.processStatusLocked(),
				PendingExportCount:  pendingExports,
				ActiveDownloadCount: activeDownloads,
			}
			m.mu.Unlock()
			if m.statusEpoch.Load() != epoch || epoch&1 != 0 || m.active.Load() != nil {
				continue
			}
			return status
		}

		s.mu.Lock()
		if m.active.Load() != s || !s.accepting || s.releasing {
			s.mu.Unlock()
			_ = s.releaseOwner()
			continue
		}
		completedRecords := s.retainedRecordCount - s.activeRecords
		if completedRecords < 0 {
			completedRecords = 0
		}
		sessionStatus := SessionStatus{
			SessionID:                   makeStatusSessionID(s.id),
			Generation:                  s.generation,
			StartedAtUnixNano:           s.startedAt,
			ProviderCount:               len(s.providerOrder),
			CompletedRecordsPerProvider: s.recordsPerProvider,
			RetainedBytesLimit:          s.quotaBytes,
			ActiveRecordCount:           s.activeRecords,
			CompletedRecordCount:        completedRecords,
			GatewayTraceCount:           s.traceCount,
			EvictedRecordCount:          s.evictedCount,
			OverflowedRecordCount:       s.overflowedCount,
			HistoryTruncatedTraceCount:  s.truncatedTraceCount,
			DroppedTraceCount:           s.droppedTraceCount,
			DroppedExchangeCount:        s.droppedExchangeCount,
			DroppedTransitionCount:      s.droppedTransitionCount,
		}
		m.mu.Lock()
		sessionStatus.RetainedBytes = s.chargedBytes - s.temporaryBytes
		status := Status{
			State:               SessionStateActive,
			ProcessMemory:       m.processStatusLocked(),
			PendingExportCount:  pendingExports,
			ActiveDownloadCount: activeDownloads,
			HasSession:          true,
			Session:             sessionStatus,
		}
		m.mu.Unlock()
		s.mu.Unlock()
		_ = s.releaseOwner()
		if m.statusEpoch.Load() == epoch && epoch&1 == 0 && m.active.Load() == s {
			return status
		}
	}
}

func (m *Manager) processStatusLocked() ProcessMemoryStatus {
	return ProcessMemoryStatus{
		CeilingBytes:   m.cfg.processCeilingBytes,
		ChargedBytes:   m.processCharged,
		RetainedBytes:  m.processCharged - m.processTemporary,
		PinnedBytes:    m.processPinned,
		ReleasingBytes: m.processReleasing,
		TemporaryBytes: m.processTemporary,
	}
}

func (m *Manager) CreateExport(
	ctx context.Context,
	sessionID string,
	request ExportRequest,
) (ExportTicket, error) {
	s, err := m.activeSession(sessionID)
	if err != nil {
		return ExportTicket{}, err
	}
	return m.createExport(ctx, s, request)
}

func (m *Manager) AcceptDownload(exportID, rawToken string) (Download, error) {
	if m == nil {
		return Download{}, ErrDownloadUnavailable
	}
	download, err := m.acceptDownload(exportID, rawToken)
	if err == nil {
		return download, nil
	}
	m.cfg.logger.Debug("request capture export download rejected",
		zap.String("export_id", exportID),
		zap.String("cause", classifyDownloadAdmissionError(err)),
	)
	// A caller must not be able to distinguish token validity from transient
	// capacity or lifecycle state. The operator-facing cause remains observable.
	return Download{}, ErrDownloadUnavailable
}

func (m *Manager) Close() error {
	if m == nil {
		return nil
	}
	var statusMutation statusEpochMutation
	if !m.beginStatusEpochMutation(&statusMutation) {
		return ErrInternalFailure
	}
	m.lifecycleMu.Lock()
	if m.closed {
		m.lifecycleMu.Unlock()
		_ = statusMutation.finish()
		return nil
	}
	m.closed = true
	m.accessGate.Lock()
	s := m.active.Load()
	m.active.Store(nil)
	m.accessGate.Unlock()
	m.lifecycleMu.Unlock()
	_ = statusMutation.finish()
	// Cancellation must be visible before teardown waits for the session gate.
	if s != nil {
		s.cancelQueries()
	}
	m.cancelAllExports()
	if s != nil {
		s.stop()
	}
	return nil
}

func (m *Manager) activeSession(sessionID string) (*sessionState, error) {
	if m == nil {
		return nil, ErrNoActiveSession
	}
	s := m.active.Load()
	if s == nil {
		return nil, ErrNoActiveSession
	}
	if s.id != sessionID {
		return nil, ErrSessionMismatch
	}
	return s, nil
}

func (s *sessionState) info() SessionInfo {
	providers := make([]ProviderIdentity, len(s.providerOrder))
	ids := make([]string, len(providers))
	for i := range providers {
		providers[i] = ProviderIdentity{
			ID:   strings.Clone(s.providerOrder[i].ID),
			Name: strings.Clone(s.providerOrder[i].Name),
		}
		ids[i] = providers[i].ID
	}
	return SessionInfo{
		SessionID:                   strings.Clone(s.id),
		Generation:                  s.generation,
		StartedAt:                   unixNanoTime(s.startedAt),
		Providers:                   providers,
		ProviderIDs:                 ids,
		CompletedRecordsPerProvider: s.recordsPerProvider,
		RetainedBytesLimit:          s.quotaBytes,
	}
}

func (s *sessionState) retainOwner() bool {
	if s == nil {
		return false
	}
	s.ownerMu.Lock()
	defer s.ownerMu.Unlock()
	if !s.ownerAccepting || s.ownerCount <= 0 || s.manager == nil ||
		s.ownerCount == int(^uint(0)>>1) {
		return false
	}
	s.ownerCount++
	return true
}

func (s *sessionState) releaseOwner() bool {
	if s == nil {
		return false
	}
	s.ownerMu.Lock()
	defer s.ownerMu.Unlock()
	return s.releaseOwnerLocked()
}

func (s *sessionState) releaseActiveOwner() bool {
	if s == nil {
		return false
	}
	s.ownerMu.Lock()
	defer s.ownerMu.Unlock()
	s.ownerAccepting = false
	return s.releaseOwnerLocked()
}

func (s *sessionState) releaseOwnerLocked() bool {
	if s.ownerCount <= 0 || s.manager == nil {
		return false
	}
	if s.ownerCount > 1 {
		s.ownerCount--
		return true
	}
	charge := s.rootCharge
	m := s.manager
	m.mu.Lock()
	if charge <= 0 || s.chargedBytes != charge || s.temporaryBytes != 0 ||
		charge > m.processCharged ||
		(s.releasing && charge > m.processReleasing) {
		m.mu.Unlock()
		return false
	}
	s.chargedBytes -= charge
	m.processCharged -= charge
	if s.releasing {
		m.processReleasing -= charge
	}
	m.mu.Unlock()

	s.ownerCount = 0
	s.rootCharge = 0
	// Identity is immutable for the lifetime of the shell. Pinned query/export
	// operations may still need it after logical detachment; the scalar string is
	// released naturally when the final owner makes the shell unreachable.
	s.queryDone = nil
	s.exportAdmission = nil
	s.queryLeaseFirst = nil
	s.queryLeaseLast = nil
	s.manager = nil
	return true
}

func (s *sessionState) stop() {
	s.gate.Lock()
	s.mu.Lock()
	if s.accepting {
		s.accepting = false
		s.markReleasingLocked()
		s.releaseAllLocked()
	}
	s.mu.Unlock()
	s.gate.Unlock()
}

func unixNanoTime(value int64) (result time.Time) {
	return time.Unix(0, value).UTC()
}
