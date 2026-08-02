package requestcapture

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"math"
	"sync"
	"sync/atomic"
	"time"

	"github.com/doraemonkeys/switch-a/internal/requestcapture/querywire"
	"go.uber.org/zap"
)

const (
	queryLeaseBaseChargeBytes int64 = 4096
	queryRecordCopyBaseBytes  int64 = 512
	queryTraceCopyBaseBytes   int64 = 256
	queryMessageCopyBaseBytes int64 = 384
	queryWriteChunkBytes            = querywire.ChunkBytes
	queryLockPollInterval           = time.Millisecond
)

type queryLease struct {
	session    *sessionState
	id         uint64
	charge     int64
	done       chan struct{}
	before     *queryLease
	after      *queryLease
	canceled   atomic.Bool
	registered bool
	cancelOnce sync.Once
	closeOnce  sync.Once
}

// RecordPageLease owns both the copied page and its process-memory charge until
// Close or WriteJSON completes. The value is intentionally not exposed so HTTP
// callers cannot accidentally outlive the cancellation guard.
type RecordPageLease struct {
	lifetimeMu sync.Mutex
	owner      *queryLease
	done       <-chan struct{}
	value      RecordPage
}

// RecordDetailLease is the detail counterpart to RecordPageLease.
type RecordDetailLease struct {
	lifetimeMu sync.Mutex
	owner      *queryLease
	done       <-chan struct{}
	value      RecordDetail
}

func (m *Manager) OpenRecordPage(ctx context.Context, sessionID string, query ListQuery) (*RecordPageLease, error) {
	done := contextDone(ctx)
	if queryContextCanceled(done) {
		return nil, ErrQueryCanceled
	}
	s := m.retainActive(0)
	if s == nil {
		return nil, ErrNoActiveSession
	}
	defer func() { _ = s.releaseOwner() }()
	if s.id != sessionID {
		return nil, ErrSessionMismatch
	}
	query, err := normalizeListQuery(query)
	if err != nil {
		return nil, err
	}
	return s.openRecordPage(done, query)
}

func (m *Manager) OpenRecordDetail(
	ctx context.Context,
	sessionID, recordID string,
	previewBytes int,
) (*RecordDetailLease, error) {
	done := contextDone(ctx)
	if queryContextCanceled(done) {
		return nil, ErrQueryCanceled
	}
	s := m.retainActive(0)
	if s == nil {
		return nil, ErrNoActiveSession
	}
	defer func() { _ = s.releaseOwner() }()
	if s.id != sessionID {
		return nil, ErrSessionMismatch
	}
	if previewBytes == 0 {
		previewBytes = m.cfg.previewBytes
	}
	if previewBytes < 0 || previewBytes > m.cfg.previewBytes {
		return nil, &ValidationError{Field: "preview_bytes", Reason: "is outside the configured range"}
	}
	return s.openRecordDetail(done, recordID, previewBytes, m.cfg.detailEventLimit)
}

func (s *sessionState) openRecordPage(done <-chan struct{}, query ListQuery) (*RecordPageLease, error) {
	owner, err := s.beginQuery(done)
	if err != nil {
		return nil, err
	}
	if err = s.lockQueryState(done, owner); err != nil {
		owner.close()
		return nil, err
	}
	charge, shape, err := s.estimateRecordPageQueryChargeLocked(query)
	if err == nil && !s.growQueryLeaseLocked(owner, charge) {
		err = ErrCapacityExceeded
	}
	var value RecordPage
	if err == nil {
		value = s.listRecordsLocked(shape)
	}
	s.unlockQueryState()
	if err != nil {
		owner.close()
		return nil, err
	}
	if err = owner.queryError(done); err != nil {
		owner.close()
		return nil, err
	}
	return &RecordPageLease{owner: owner, done: owner.done, value: value}, nil
}

func (s *sessionState) openRecordDetail(
	done <-chan struct{},
	recordID string,
	previewBytes, eventLimit int,
) (*RecordDetailLease, error) {
	owner, err := s.beginQuery(done)
	if err != nil {
		return nil, err
	}
	if err = s.lockQueryState(done, owner); err != nil {
		owner.close()
		return nil, err
	}
	record, err := s.lookupRecordLocked(recordID)
	if err != nil {
		s.unlockQueryState()
		owner.close()
		return nil, err
	}
	charge := estimateRecordDetailQueryChargeLocked(record, previewBytes, eventLimit)
	if !s.growQueryLeaseLocked(owner, charge) {
		s.unlockQueryState()
		owner.close()
		return nil, ErrCapacityExceeded
	}
	snapshot := snapshotRecordDetailQueryLocked(record, previewBytes, eventLimit)
	s.unlockQueryState()

	value, err := snapshot.materialize(done, owner, previewBytes)
	snapshot.release()
	if err != nil {
		owner.close()
		return nil, err
	}
	if err = owner.queryError(done); err != nil {
		owner.close()
		return nil, err
	}
	return &RecordDetailLease{owner: owner, done: owner.done, value: value}, nil
}

func (l *RecordPageLease) Done() <-chan struct{} {
	if l == nil {
		return closedQueryDone()
	}
	return l.done
}

func (l *RecordDetailLease) Done() <-chan struct{} {
	if l == nil {
		return closedQueryDone()
	}
	return l.done
}

func (l *RecordPageLease) WriteJSON(ctx context.Context, dst io.Writer) error {
	if l == nil {
		return ErrQueryCanceled
	}
	done := contextDone(ctx)
	l.lifetimeMu.Lock()
	defer l.lifetimeMu.Unlock()
	if l.owner == nil {
		return ErrQueryCanceled
	}
	owner := l.owner
	value := l.value
	l.owner = nil
	l.value = RecordPage{}
	defer owner.close()
	if dst == nil {
		return ErrQueryCanceled
	}
	return querywire.WriteRecordPage(dst, value, func() error { return owner.queryError(done) })
}

func (l *RecordDetailLease) WriteJSON(ctx context.Context, dst io.Writer) error {
	if l == nil {
		return ErrQueryCanceled
	}
	done := contextDone(ctx)
	l.lifetimeMu.Lock()
	defer l.lifetimeMu.Unlock()
	if l.owner == nil {
		return ErrQueryCanceled
	}
	owner := l.owner
	value := l.value
	l.owner = nil
	l.value = RecordDetail{}
	defer owner.close()
	if dst == nil {
		return ErrQueryCanceled
	}
	return querywire.WriteRecordDetail(dst, value, func() error { return owner.queryError(done) })
}

func (l *RecordPageLease) Close() {
	if l == nil {
		return
	}
	l.lifetimeMu.Lock()
	defer l.lifetimeMu.Unlock()
	owner := l.owner
	l.owner = nil
	l.value = RecordPage{}
	if owner != nil {
		owner.close()
	}
}

func (l *RecordDetailLease) Close() {
	if l == nil {
		return
	}
	l.lifetimeMu.Lock()
	defer l.lifetimeMu.Unlock()
	owner := l.owner
	l.owner = nil
	l.value = RecordDetail{}
	if owner != nil {
		owner.close()
	}
}

func (s *sessionState) beginQuery(done <-chan struct{}) (*queryLease, error) {
	if queryContextCanceled(done) {
		return nil, ErrQueryCanceled
	}
	if s.manager.active.Load() != s {
		return nil, ErrNoActiveSession
	}

	// Admission owns its full streaming buffer charge before it can wait on the
	// session gate or mutex. The session quota therefore bounds both active
	// queries and lock-contending goroutines without burdening the proxy hot path.
	charge := queryLeaseBaseChargeBytes + queryWriteChunkBytes
	s.queryMu.Lock()
	if s.queryCanceledLocked() || s.manager.active.Load() != s {
		s.queryMu.Unlock()
		return nil, ErrNoActiveSession
	}
	if s.nextQuerySequence == math.MaxUint64 {
		s.queryMu.Unlock()
		return nil, ErrCapacityExceeded
	}
	if !s.retainOwner() {
		s.queryMu.Unlock()
		return nil, ErrNoActiveSession
	}
	m := s.manager
	m.mu.Lock()
	if s.releasing || charge > s.quotaBytes-s.chargedBytes ||
		charge > m.cfg.processCeilingBytes-m.processCharged {
		m.mu.Unlock()
		s.queryMu.Unlock()
		_ = s.releaseOwner()
		return nil, ErrCapacityExceeded
	}
	s.chargedBytes += charge
	s.temporaryBytes += charge
	m.processCharged += charge
	m.processTemporary += charge
	m.mu.Unlock()

	s.nextQuerySequence++
	lease := &queryLease{
		session:    s,
		id:         s.nextQuerySequence,
		charge:     charge,
		done:       make(chan struct{}),
		registered: true,
	}
	s.appendQueryLeaseLocked(lease)
	s.queryMu.Unlock()

	if err := lease.queryError(done); err != nil {
		lease.close()
		return nil, err
	}
	return lease, nil
}

func (s *sessionState) lockQueryState(done <-chan struct{}, lease *queryLease) error {
	timer := time.NewTimer(0)
	if !timer.Stop() {
		<-timer.C
	}
	defer timer.Stop()

	for {
		if err := lease.queryError(done); err != nil {
			return err
		}
		s.gate.RLock()
		if err := lease.queryError(done); err != nil {
			s.gate.RUnlock()
			return err
		}
		if s.mu.TryLock() {
			if !s.accepting {
				s.mu.Unlock()
				s.gate.RUnlock()
				return ErrQueryCanceled
			}
			if err := lease.queryError(done); err != nil {
				s.mu.Unlock()
				s.gate.RUnlock()
				return err
			}
			return nil
		}
		s.gate.RUnlock()

		timer.Reset(queryLockPollInterval)
		select {
		case <-timer.C:
		case <-done:
			return ErrQueryCanceled
		case <-lease.done:
			return ErrQueryCanceled
		}
	}
}

func (s *sessionState) unlockQueryState() {
	s.mu.Unlock()
	s.gate.RUnlock()
}

func (s *sessionState) growQueryLeaseLocked(lease *queryLease, total int64) bool {
	if lease == nil || lease.session != s || lease.canceled.Load() ||
		s.manager.active.Load() != s {
		return false
	}
	if total <= lease.charge {
		return true
	}
	additional := total - lease.charge
	m := s.manager
	m.mu.Lock()
	defer m.mu.Unlock()
	if s.releasing || additional > s.quotaBytes-s.chargedBytes ||
		additional > m.cfg.processCeilingBytes-m.processCharged {
		return false
	}
	s.chargedBytes += additional
	s.temporaryBytes += additional
	m.processCharged += additional
	m.processTemporary += additional
	lease.charge = total
	return true
}

func (s *sessionState) appendQueryLeaseLocked(lease *queryLease) {
	lease.before = s.queryLeaseLast
	if s.queryLeaseLast != nil {
		s.queryLeaseLast.after = lease
	} else {
		s.queryLeaseFirst = lease
	}
	s.queryLeaseLast = lease
	s.queryLeaseCount++
}

func (s *sessionState) queryCanceledLocked() bool {
	select {
	case <-s.queryDone:
		return true
	default:
		return false
	}
}

func (s *sessionState) cancelQueries() {
	if s == nil {
		return
	}
	s.queryCancelOnce.Do(func() {
		s.queryMu.Lock()
		close(s.queryDone)
		for lease := s.queryLeaseFirst; lease != nil; lease = lease.after {
			lease.cancel()
		}
		s.queryMu.Unlock()
	})
}

func (q *queryLease) queryError(done <-chan struct{}) error {
	if q == nil || queryContextCanceled(done) || q.canceled.Load() {
		return ErrQueryCanceled
	}
	s := q.session
	if s == nil || s.manager.active.Load() != s {
		return ErrQueryCanceled
	}
	return nil
}

func (q *queryLease) cancel() {
	if q == nil {
		return
	}
	q.cancelOnce.Do(func() {
		q.canceled.Store(true)
		close(q.done)
	})
}

func (q *queryLease) close() {
	if q == nil {
		return
	}
	q.closeOnce.Do(func() {
		s := q.session
		if s != nil {
			s.releaseQueryLease(q)
		}
	})
}

func (s *sessionState) releaseQueryLease(lease *queryLease) {
	s.queryMu.Lock()
	if lease == nil || !lease.registered || lease.session != s {
		s.queryMu.Unlock()
		return
	}

	m := s.manager
	m.mu.Lock()
	if lease.charge > s.chargedBytes || lease.charge > s.temporaryBytes ||
		lease.charge > m.processCharged || lease.charge > m.processTemporary ||
		(s.releasing && lease.charge > m.processReleasing) {
		m.mu.Unlock()
		s.queryMu.Unlock()
		s.logQueryReleaseRejected(lease, "account_underflow")
		return
	}
	charge := lease.charge
	s.removeQueryLeaseLocked(lease)
	lease.cancel()
	lease.charge = 0
	lease.session = nil
	s.chargedBytes -= charge
	s.temporaryBytes -= charge
	m.processCharged -= charge
	m.processTemporary -= charge
	if s.releasing {
		m.processReleasing -= charge
	}
	m.mu.Unlock()
	s.queryMu.Unlock()
	_ = s.releaseOwner()
}

func (s *sessionState) removeQueryLeaseLocked(lease *queryLease) {
	if lease.before != nil {
		lease.before.after = lease.after
	} else {
		s.queryLeaseFirst = lease.after
	}
	if lease.after != nil {
		lease.after.before = lease.before
	} else {
		s.queryLeaseLast = lease.before
	}
	lease.before = nil
	lease.after = nil
	lease.registered = false
	s.queryLeaseCount--
}

func (s *sessionState) logQueryReleaseRejected(lease *queryLease, reason string) {
	s.manager.cfg.logger.Error("request capture query temporary release rejected",
		zap.String("session_id", s.id),
		zap.Uint64("generation", s.generation),
		zap.Uint64("query_sequence", lease.id),
		zap.Int64("release_bytes", lease.charge),
		zap.String("reason", reason),
	)
}

type cursorPayload struct {
	Generation uint64 `json:"g"`
	Watermark  uint64 `json:"w"`
	Before     uint64 `json:"b"`
}

type watermarkPayload struct {
	Generation uint64 `json:"g"`
	Watermark  uint64 `json:"w"`
}

func encodeCursor(generation, watermark, before uint64) string {
	payload, _ := json.Marshal(cursorPayload{
		Generation: generation,
		Watermark:  watermark,
		Before:     before,
	})
	return base64.RawURLEncoding.EncodeToString(payload)
}

func decodeCursor(value string) (cursorPayload, error) {
	raw, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil {
		return cursorPayload{}, ErrInvalidCursor
	}
	var payload cursorPayload
	if err := json.Unmarshal(raw, &payload); err != nil || payload.Generation == 0 || payload.Before == 0 {
		return cursorPayload{}, ErrInvalidCursor
	}
	return payload, nil
}

func encodeWatermark(generation, watermark uint64) string {
	payload, _ := json.Marshal(watermarkPayload{
		Generation: generation,
		Watermark:  watermark,
	})
	return base64.RawURLEncoding.EncodeToString(payload)
}

func decodeWatermark(value string) (generation, watermark uint64, err error) {
	raw, decodeErr := base64.RawURLEncoding.DecodeString(value)
	if decodeErr != nil {
		return 0, 0, ErrInvalidCursor
	}
	var payload watermarkPayload
	if unmarshalErr := json.Unmarshal(raw, &payload); unmarshalErr != nil || payload.Generation == 0 {
		return 0, 0, ErrInvalidCursor
	}
	return payload.Generation, payload.Watermark, nil
}

func contextDone(ctx context.Context) <-chan struct{} {
	if ctx == nil {
		return nil
	}
	return ctx.Done()
}

func queryContextCanceled(done <-chan struct{}) bool {
	select {
	case <-done:
		return true
	default:
		return false
	}
}

var (
	closedQueryDoneOnce sync.Once
	closedQueryDoneChan chan struct{}
)

func closedQueryDone() <-chan struct{} {
	closedQueryDoneOnce.Do(func() {
		closedQueryDoneChan = make(chan struct{})
		close(closedQueryDoneChan)
	})
	return closedQueryDoneChan
}
