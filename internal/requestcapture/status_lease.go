package requestcapture

import (
	"context"
	"errors"
	"io"
	"math"
	"runtime"

	"github.com/doraemonkeys/switch-a/internal/requestcapture/statuswire"
)

const (
	// The dynamic part contains only field names and maximum-width scalar values.
	// Provider strings are charged separately from their exact escaped lengths.
	statusFixedJSONCapacityBytes = 2048
	statusStoppedJSONBytes       = 1024
	statusSlotBaseChargeBytes    = 512
)

var (
	ErrStatusBusy        = errors.New("request capture status slot is busy")
	ErrStatusLeaseClosed = errors.New("request capture status lease is closed")
	errStatusChanged     = errors.New("request capture status generation changed")
)

// StatusSessionID copies the active identity into the snapshot itself. Unlike a
// string descriptor, a retained Status value therefore cannot keep session-owned
// bytes alive after Stop releases their account token.
type StatusSessionID struct {
	storage [maxSessionIDBytes]byte
	length  uint8
}

func makeStatusSessionID(value string) StatusSessionID {
	var result StatusSessionID
	if len(value) == 0 || len(value) > len(result.storage) {
		return result
	}
	copy(result.storage[:], value)
	result.length = uint8(len(value))
	return result
}

func (id StatusSessionID) Valid() bool {
	return id.length > 0 && int(id.length) <= len(id.storage)
}

func (id StatusSessionID) Equal(value string) bool {
	if len(value) != int(id.length) {
		return false
	}
	for index := range value {
		if id.storage[index] != value[index] {
			return false
		}
	}
	return true
}

func (id StatusSessionID) String() string {
	return string(id.storage[:id.length])
}

// statusEpochMutation marks a writer whose registry and account effects cross
// separate lock domains. Multiple writers may overlap; the epoch stays odd until
// the final writer completes, so Status never accepts an intermediate view.
type statusEpochMutation struct {
	manager *Manager
	active  bool
}

func (m *Manager) beginStatusEpochMutation(mutation *statusEpochMutation) bool {
	if m == nil || mutation == nil || mutation.active {
		return false
	}
	m.statusEpochMu.Lock()
	defer m.statusEpochMu.Unlock()
	if m.statusEpochWriters == math.MaxUint64 {
		return false
	}
	if m.statusEpochWriters == 0 {
		epoch := m.statusEpoch.Load()
		if epoch > math.MaxUint64-2 {
			return false
		}
		m.statusEpoch.Store(epoch + 1)
	}
	m.statusEpochWriters++
	*mutation = statusEpochMutation{manager: m, active: true}
	return true
}

func (mutation *statusEpochMutation) finish() bool {
	if mutation == nil || !mutation.active || mutation.manager == nil {
		return mutation != nil && !mutation.active
	}
	m := mutation.manager
	m.statusEpochMu.Lock()
	if m.statusEpochWriters == 0 {
		m.statusEpochMu.Unlock()
		return false
	}
	m.statusEpochWriters--
	if m.statusEpochWriters == 0 {
		m.statusEpoch.Store(m.statusEpoch.Load() + 1)
	}
	m.statusEpochMu.Unlock()
	*mutation = statusEpochMutation{}
	return true
}

type statusJSONSlot struct {
	storage []byte
	length  int
	charge  int64
	session *sessionState
	retired bool
}

type statusLeaseClaim struct {
	sequence       uint64
	session        *sessionState
	slot           *statusJSONSlot
	writing        bool
	closeRequested bool
}

// StatusLease is a numeric capability into a single manager-owned checkout.
// Keeping session storage behind the manager registry ensures copied or stale
// handles cannot retain a refunded generation's object graph.
type StatusLease struct {
	manager       *Manager
	claimSequence uint64
}

func (lease StatusLease) Valid() bool {
	return lease.manager != nil && lease.claimSequence != 0
}

func (lease StatusLease) WriteJSON(ctx context.Context, destination io.Writer) error {
	if !lease.Valid() || destination == nil {
		return ErrStatusLeaseClosed
	}
	if err := statusContextError(ctx); err != nil {
		_ = lease.Close()
		return err
	}

	m := lease.manager
	m.statusLeaseMu.Lock()
	claim := &m.statusLeaseClaim
	if claim.sequence != lease.claimSequence || claim.slot == nil || claim.writing {
		m.statusLeaseMu.Unlock()
		return ErrStatusLeaseClosed
	}
	claim.writing = true
	payload := claim.slot.storage[:claim.slot.length]
	m.statusLeaseMu.Unlock()
	// External writers may panic. The capability registry and account owner must
	// still be retired exactly once while preserving the writer's panic value.
	defer lease.finishWrite()

	written, writeErr := destination.Write(payload)
	if writeErr == nil && written != len(payload) {
		writeErr = io.ErrShortWrite
	}
	if writeErr == nil {
		writeErr = statusContextError(ctx)
	}
	return writeErr
}

func (lease StatusLease) Close() error {
	lease.release(false)
	return nil
}

func (lease StatusLease) finishWrite() {
	lease.release(true)
}

func (lease StatusLease) release(finishedWrite bool) {
	if !lease.Valid() {
		return
	}
	m := lease.manager
	m.statusLeaseMu.Lock()
	claim := &m.statusLeaseClaim
	if claim.sequence != lease.claimSequence || claim.slot == nil {
		m.statusLeaseMu.Unlock()
		return
	}
	if claim.writing && !finishedWrite {
		claim.closeRequested = true
		m.statusLeaseMu.Unlock()
		return
	}
	session := claim.session
	slot := claim.slot
	slot.length = 0
	releaseCharge := int64(0)
	if slot.retired {
		releaseCharge = slot.charge
		slot.storage = nil
		slot.charge = 0
		slot.session = nil
	}
	// Sever the registry before refunding storage or releasing the session owner.
	// A stale copied lease can retain only the manager and its obsolete sequence.
	m.statusLeaseClaim = statusLeaseClaim{}
	m.statusLeaseMu.Unlock()
	if releaseCharge > 0 {
		session.mu.Lock()
		session.releaseLocked(releaseCharge)
		session.mu.Unlock()
	}
	if session != nil {
		_ = session.releaseOwner()
	}
}

func statusJSONCapacity(providers []ProviderIdentity) (int, bool) {
	total := int64(statusFixedJSONCapacityBytes)
	for _, provider := range providers {
		addition := int64(2*quotedJSONBytes(provider.ID) + quotedJSONBytes(provider.Name))
		// Object/array punctuation is deliberately fixed per selected provider.
		addition += 32
		if addition < 0 || addition > math.MaxInt64-total {
			return 0, false
		}
		total += addition
	}
	if total > int64(int(^uint(0)>>1)) {
		return 0, false
	}
	return int(total), true
}

func quotedJSONBytes(value string) int {
	return statuswire.QuotedBytes(value)
}

type statusJSONBuilder struct {
	wire statuswire.Builder
}

func newStatusJSONBuilder(storage []byte) statusJSONBuilder {
	return statusJSONBuilder{wire: statuswire.New(storage)}
}

func (builder *statusJSONBuilder) byte(value byte) {
	builder.wire.Byte(value)
}

func (builder *statusJSONBuilder) literal(value string) {
	builder.wire.Literal(value)
}

func (builder *statusJSONBuilder) quoted(value string) {
	builder.wire.Quoted(value)
}

func (builder *statusJSONBuilder) int64(value int64) {
	builder.wire.Int64(value)
}

func (builder *statusJSONBuilder) uint64(value uint64) {
	builder.wire.Uint64(value)
}

func (builder *statusJSONBuilder) int(value int) {
	builder.wire.Int(value)
}

func (builder *statusJSONBuilder) timestamp(unixNano int64) {
	builder.wire.Timestamp(unixNano)
}

type statusProcessSnapshot struct {
	ceiling   int64
	charged   int64
	retained  int64
	pinned    int64
	releasing int64
	temporary int64
}

func (builder *statusJSONBuilder) process(snapshot statusProcessSnapshot) {
	builder.wire.Process(statuswire.Process{
		Ceiling:   snapshot.ceiling,
		Charged:   snapshot.charged,
		Retained:  snapshot.retained,
		Pinned:    snapshot.pinned,
		Releasing: snapshot.releasing,
		Temporary: snapshot.temporary,
	})
}

func (builder *statusJSONBuilder) overflowed() bool {
	return builder.wire.Overflowed()
}

func (builder *statusJSONBuilder) length() int {
	return builder.wire.Len()
}

func (m *Manager) OpenStatus(ctx context.Context) (StatusLease, error) {
	if m == nil {
		return StatusLease{}, ErrManagerClosed
	}
	for {
		if err := statusContextError(ctx); err != nil {
			return StatusLease{}, err
		}
		session := m.retainActive(0)
		var lease StatusLease
		var slot *statusJSONSlot
		var err error
		if session == nil {
			lease, slot, err = m.claimManagerStatusSlot()
		} else {
			lease, slot, err = m.claimSessionStatusSlot(session)
			if err != nil {
				_ = session.releaseOwner()
			}
		}
		if err != nil {
			return StatusLease{}, err
		}
		if err = m.populateStatusLease(ctx, slot, session); err == nil {
			return lease, nil
		}
		_ = lease.Close()
		if !errors.Is(err, errStatusChanged) {
			return StatusLease{}, err
		}
	}
}

func (m *Manager) claimManagerStatusSlot() (StatusLease, *statusJSONSlot, error) {
	slot := &m.managerStatusSlot
	lease, err := m.claimStatusSlot(nil, slot)
	return lease, slot, err
}

func (m *Manager) claimSessionStatusSlot(session *sessionState) (StatusLease, *statusJSONSlot, error) {
	session.gate.RLock()
	defer session.gate.RUnlock()
	session.mu.Lock()
	defer session.mu.Unlock()
	if m.active.Load() != session || !session.accepting || session.statusSlot == nil {
		return StatusLease{}, nil, errStatusChanged
	}
	slot := session.statusSlot
	if slot.retired {
		return StatusLease{}, nil, errStatusChanged
	}
	lease, err := m.claimStatusSlot(session, slot)
	return lease, slot, err
}

func (m *Manager) claimStatusSlot(session *sessionState, slot *statusJSONSlot) (StatusLease, error) {
	m.statusLeaseMu.Lock()
	defer m.statusLeaseMu.Unlock()
	if slot == nil || m.statusLeaseClaim.slot != nil || m.statusLeaseNext == math.MaxUint64 {
		return StatusLease{}, ErrStatusBusy
	}
	m.statusLeaseNext++
	slot.length = 0
	m.statusLeaseClaim = statusLeaseClaim{
		sequence: m.statusLeaseNext,
		session:  session,
		slot:     slot,
	}
	return StatusLease{manager: m, claimSequence: m.statusLeaseNext}, nil
}

func (m *Manager) populateStatusLease(
	ctx context.Context,
	slot *statusJSONSlot,
	expectedSession *sessionState,
) error {
	for {
		if err := statusContextError(ctx); err != nil {
			return err
		}
		epoch := m.statusEpoch.Load()
		if epoch&1 != 0 {
			runtime.Gosched()
			continue
		}
		m.exportMu.Lock()
		pendingExports := m.pendingExportCountLocked()
		activeDownloads := m.activeDownloads
		m.exportMu.Unlock()

		var err error
		if expectedSession == nil {
			err = m.populateStoppedStatusSlot(slot, pendingExports, activeDownloads)
		} else {
			err = m.populateActiveStatusSlot(slot, expectedSession, pendingExports, activeDownloads)
		}
		if err != nil {
			return err
		}
		if m.statusEpoch.Load() == epoch && epoch&1 == 0 && m.active.Load() == expectedSession {
			return nil
		}
	}
}

func (m *Manager) populateStoppedStatusSlot(
	slot *statusJSONSlot,
	pendingExports, activeDownloads int,
) error {
	if m.active.Load() != nil {
		return errStatusChanged
	}
	m.mu.Lock()
	process := statusProcessSnapshot{
		ceiling:   m.cfg.processCeilingBytes,
		charged:   m.processCharged,
		retained:  m.processCharged - m.processTemporary,
		pinned:    m.processPinned,
		releasing: m.processReleasing,
		temporary: m.processTemporary,
	}
	m.mu.Unlock()

	builder := newStatusJSONBuilder(slot.storage)
	builder.literal(`{"state":"stopped",`)
	builder.process(process)
	builder.literal(`,"pending_export_count":`)
	builder.int(pendingExports)
	builder.literal(`,"active_download_count":`)
	builder.int(activeDownloads)
	builder.literal(`,"session":null}`)
	builder.byte('\n')
	if builder.overflowed() {
		return ErrInternalFailure
	}
	slot.length = builder.length()
	return nil
}

func (m *Manager) populateActiveStatusSlot(
	slot *statusJSONSlot,
	session *sessionState,
	pendingExports, activeDownloads int,
) error {
	session.mu.Lock()
	defer session.mu.Unlock()
	if m.active.Load() != session || !session.accepting || session.statusSlot != slot {
		return errStatusChanged
	}

	completedRecords := max(session.retainedRecordCount-session.activeRecords, 0)
	m.mu.Lock()
	process := statusProcessSnapshot{
		ceiling:   m.cfg.processCeilingBytes,
		charged:   m.processCharged,
		retained:  m.processCharged - m.processTemporary,
		pinned:    m.processPinned,
		releasing: m.processReleasing,
		temporary: m.processTemporary,
	}
	sessionRetained := session.chargedBytes - session.temporaryBytes
	m.mu.Unlock()

	builder := newStatusJSONBuilder(slot.storage)
	builder.literal(`{"state":"active",`)
	builder.process(process)
	builder.literal(`,"pending_export_count":`)
	builder.int(pendingExports)
	builder.literal(`,"active_download_count":`)
	builder.int(activeDownloads)
	builder.literal(`,"session":{"session_id":`)
	builder.quoted(session.id)
	builder.literal(`,"generation":`)
	builder.uint64(session.generation)
	builder.literal(`,"started_at":`)
	builder.timestamp(session.startedAt)
	builder.literal(`,"providers":[`)
	for index, provider := range session.providerOrder {
		if index > 0 {
			builder.byte(',')
		}
		builder.literal(`{"id":`)
		builder.quoted(provider.ID)
		builder.literal(`,"name":`)
		builder.quoted(provider.Name)
		builder.byte('}')
	}
	builder.literal(`],"provider_ids":[`)
	for index, provider := range session.providerOrder {
		if index > 0 {
			builder.byte(',')
		}
		builder.quoted(provider.ID)
	}
	builder.literal(`],"completed_records_per_provider":`)
	builder.int(session.recordsPerProvider)
	builder.literal(`,"retained_bytes_limit":`)
	builder.int64(session.quotaBytes)
	builder.literal(`,"retained_bytes":`)
	builder.int64(sessionRetained)
	builder.literal(`,"active_record_count":`)
	builder.int(session.activeRecords)
	builder.literal(`,"completed_record_count":`)
	builder.int(completedRecords)
	builder.literal(`,"gateway_trace_count":`)
	builder.int(session.traceCount)
	builder.literal(`,"evicted_record_count":`)
	builder.uint64(session.evictedCount)
	builder.literal(`,"overflowed_record_count":`)
	builder.uint64(session.overflowedCount)
	builder.literal(`,"history_truncated_trace_count":`)
	builder.uint64(session.truncatedTraceCount)
	builder.literal(`,"dropped_trace_count":`)
	builder.uint64(session.droppedTraceCount)
	builder.literal(`,"dropped_exchange_count":`)
	builder.uint64(session.droppedExchangeCount)
	builder.literal(`,"dropped_transition_count":`)
	builder.uint64(session.droppedTransitionCount)
	builder.literal(`}}`)
	builder.byte('\n')
	if builder.overflowed() {
		return ErrInternalFailure
	}
	slot.length = builder.length()
	return nil
}

func (s *sessionState) retireStatusSlotLocked() {
	if s == nil || s.statusSlot == nil {
		return
	}
	slot := s.statusSlot
	s.statusSlot = nil
	s.statusCharge = 0
	m := s.manager
	m.statusLeaseMu.Lock()
	slot.retired = true
	releaseCharge := int64(0)
	if m.statusLeaseClaim.slot != slot {
		releaseCharge = slot.charge
		slot.storage = nil
		slot.charge = 0
		slot.session = nil
	}
	m.statusLeaseMu.Unlock()
	if releaseCharge > 0 {
		s.releaseLocked(releaseCharge)
	}
}

func discardStatusSlot(slot *statusJSONSlot) {
	if slot == nil {
		return
	}
	slot.storage = nil
	slot.session = nil
	slot.charge = 0
}

func statusContextError(ctx context.Context) error {
	if ctx == nil {
		return nil
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
		return nil
	}
}
