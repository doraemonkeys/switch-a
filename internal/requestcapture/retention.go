package requestcapture

import (
	"math"
	"unsafe"

	"github.com/doraemonkeys/switch-a/internal/requestcapture/captureid"
	"go.uber.org/zap"
)

const (
	recordIDPrefix         = captureid.RecordIDPrefix
	recordIDTagBytes       = captureid.RecordIDTagBytes
	recordIDEncodedBytes   = captureid.RecordIDEncodedBytes
	canonicalRecordIDBytes = captureid.CanonicalRecordIDBytes
)

type parsedRecordID struct {
	generation uint64
	sequence   uint64
	tag        [recordIDTagBytes]byte
}

type evictionRange struct {
	first  uint64
	last   uint64
	before *evictionRange
	after  *evictionRange
}

type recordIDValue = captureid.RecordIDValue

func scanHandleSlotShape(
	providerCount, recordsPerProvider, maxActiveTraces, maxActiveRecords int,
) (handleSlotShape, bool) {
	if providerCount <= 0 || recordsPerProvider <= 0 || maxActiveTraces <= 0 || maxActiveRecords <= 0 {
		return handleSlotShape{}, false
	}
	providerCount64 := int64(providerCount)
	recordsPerProvider64 := int64(recordsPerProvider)
	if providerCount64 > math.MaxInt64/recordsPerProvider64 {
		return handleSlotShape{}, false
	}
	retainedRecords := providerCount64 * recordsPerProvider64
	if retainedRecords <= 0 || retainedRecords > math.MaxUint32 {
		return handleSlotShape{}, false
	}
	if int64(maxActiveTraces) > math.MaxInt64-retainedRecords ||
		int64(maxActiveRecords) > math.MaxInt64-retainedRecords {
		return handleSlotShape{}, false
	}
	gatewayCount := retainedRecords + int64(maxActiveTraces)
	recordCount := retainedRecords + int64(maxActiveRecords)
	maxPlatformInt := uint64(^uint(0) >> 1)
	if gatewayCount <= 0 || gatewayCount > math.MaxUint32 ||
		recordCount <= 0 || recordCount > math.MaxUint32 ||
		uint64(gatewayCount) > maxPlatformInt || uint64(recordCount) > maxPlatformInt {
		return handleSlotShape{}, false
	}
	gatewayBytes := int64(unsafe.Sizeof(gatewayHandleSlot{}))
	recordBytes := int64(unsafe.Sizeof(recordHandleSlot{}))
	if gatewayCount > math.MaxInt64/gatewayBytes || recordCount > math.MaxInt64/recordBytes {
		return handleSlotShape{}, false
	}
	charge := gatewayCount * gatewayBytes
	recordCharge := recordCount * recordBytes
	if recordCharge > math.MaxInt64-charge {
		return handleSlotShape{}, false
	}
	return handleSlotShape{
		gatewayCount: int(gatewayCount),
		recordCount:  int(recordCount),
		charge:       charge + recordCharge,
	}, true
}

func initializeHandleSlots(session *sessionState, shape handleSlotShape) bool {
	if session == nil || shape.gatewayCount <= 0 || shape.recordCount <= 0 || shape.charge <= 0 {
		return false
	}
	session.gatewayHandleSlots = make([]gatewayHandleSlot, shape.gatewayCount)
	session.recordHandleSlots = make([]recordHandleSlot, shape.recordCount)
	initializeGatewayHandleFreeList(session.gatewayHandleSlots)
	initializeRecordHandleFreeList(session.recordHandleSlots)
	session.freeGatewayHandleSlot = 1
	session.freeRecordHandleSlot = 1
	return true
}

func initializeGatewayHandleFreeList(slots []gatewayHandleSlot) {
	for index := range slots {
		if index+1 < len(slots) {
			slots[index].nextFree = uint32(index + 2)
		}
	}
}

func initializeRecordHandleFreeList(slots []recordHandleSlot) {
	for index := range slots {
		if index+1 < len(slots) {
			slots[index].nextFree = uint32(index + 2)
		}
	}
}

func (s *sessionState) claimGatewayHandleSlotLocked(gateway *gatewayState) (uint32, bool) {
	slotID := s.freeGatewayHandleSlot
	if gateway == nil || gateway.handleSlot != 0 || slotID == 0 || int(slotID) > len(s.gatewayHandleSlots) {
		return 0, false
	}
	slot := &s.gatewayHandleSlots[slotID-1]
	if slot.gateway != nil || slot.sequence != 0 {
		return 0, false
	}
	s.freeGatewayHandleSlot = slot.nextFree
	slot.gateway = gateway
	slot.sequence = gateway.traceSequence
	slot.nextFree = 0
	gateway.handleSlot = slotID
	return slotID, true
}

func (s *sessionState) releaseGatewayHandleSlotLocked(gateway *gatewayState) bool {
	if gateway == nil || gateway.handleSlot == 0 || int(gateway.handleSlot) > len(s.gatewayHandleSlots) {
		return false
	}
	slotID := gateway.handleSlot
	slot := &s.gatewayHandleSlots[slotID-1]
	if slot.gateway != gateway || slot.sequence != gateway.traceSequence {
		return false
	}
	slot.gateway = nil
	slot.sequence = 0
	slot.nextFree = s.freeGatewayHandleSlot
	s.freeGatewayHandleSlot = slotID
	gateway.handleSlot = 0
	return true
}

func (s *sessionState) gatewayHandleLocked(slotID uint32, sequence uint64) *gatewayState {
	if slotID == 0 || sequence == 0 || int(slotID) > len(s.gatewayHandleSlots) {
		return nil
	}
	slot := &s.gatewayHandleSlots[slotID-1]
	if slot.sequence != sequence || slot.gateway == nil {
		return nil
	}
	return slot.gateway
}

func (s *sessionState) claimRecordHandleSlotLocked(record *recordState) (uint32, bool) {
	slotID := s.freeRecordHandleSlot
	if record == nil || record.handleSlot != 0 || slotID == 0 || int(slotID) > len(s.recordHandleSlots) {
		return 0, false
	}
	slot := &s.recordHandleSlots[slotID-1]
	if slot.record != nil || slot.sequence != 0 {
		return 0, false
	}
	s.freeRecordHandleSlot = slot.nextFree
	slot.record = record
	slot.sequence = record.summary.RecordSequence
	slot.nextFree = 0
	record.handleSlot = slotID
	return slotID, true
}

func (s *sessionState) releaseRecordHandleSlotLocked(record *recordState) bool {
	if record == nil || record.handleSlot == 0 || int(record.handleSlot) > len(s.recordHandleSlots) {
		return false
	}
	slotID := record.handleSlot
	slot := &s.recordHandleSlots[slotID-1]
	if slot.record != record || slot.sequence != record.summary.RecordSequence {
		return false
	}
	slot.record = nil
	slot.sequence = 0
	slot.nextFree = s.freeRecordHandleSlot
	s.freeRecordHandleSlot = slotID
	record.handleSlot = 0
	return true
}

func (s *sessionState) recordHandleLocked(slotID uint32, sequence uint64) *recordState {
	if slotID == 0 || sequence == 0 || int(slotID) > len(s.recordHandleSlots) {
		return nil
	}
	slot := &s.recordHandleSlots[slotID-1]
	if slot.sequence != sequence || slot.record == nil {
		return nil
	}
	return slot.record
}

func (s *sessionState) severHandleSlotsLocked() {
	for index := range s.gatewayHandleSlots {
		s.gatewayHandleSlots[index] = gatewayHandleSlot{}
	}
	for index := range s.recordHandleSlots {
		s.recordHandleSlots[index] = recordHandleSlot{}
	}
	s.gatewayHandleSlots = nil
	s.recordHandleSlots = nil
	s.freeGatewayHandleSlot = 0
	s.freeRecordHandleSlot = 0
}

func (s *sessionState) makeRecordID(sequence uint64) string {
	return makeRecordIDValue(s.id, s.generation, sequence).String()
}

func makeRecordIDValue(sessionID string, generation, sequence uint64) recordIDValue {
	return captureid.MakeRecordIDValue(sessionID, generation, sequence)
}

func IsCanonicalRecordID(recordID string) bool {
	_, ok := parseRecordID(recordID)
	return ok
}

func parseRecordID(recordID string) (parsedRecordID, bool) {
	parsed, ok := captureid.ParseRecordID(recordID)
	if !ok {
		return parsedRecordID{}, false
	}
	return parsedRecordID{
		generation: parsed.Generation,
		sequence:   parsed.Sequence,
		tag:        parsed.Tag,
	}, true
}

func (s *sessionState) ownsRecordID(recordID string) bool {
	parsed, ok := parseRecordID(recordID)
	return ok && parsed.generation == s.generation && parsed.sequence <= s.nextRecordSequence &&
		captureid.OwnsRecordID(s.id, s.generation, recordID)
}

func (s *sessionState) appendRecordLocked(record *recordState) {
	record.older = s.newestRecord
	if s.newestRecord == nil {
		s.oldestRecord = record
	} else {
		s.newestRecord.newer = record
	}
	s.newestRecord = record
	s.retainedRecordCount++
}

func (s *sessionState) removeRecordLocked(record *recordState) {
	if record == nil {
		return
	}
	if record.older == nil {
		s.oldestRecord = record.newer
	} else {
		record.older.newer = record.newer
	}
	if record.newer == nil {
		s.newestRecord = record.older
	} else {
		record.newer.older = record.older
	}
	record.older = nil
	record.newer = nil
	if s.retainedRecordCount > 0 {
		s.retainedRecordCount--
	}
}

func (s *sessionState) lookupRecordLocked(recordID string) (*recordState, error) {
	for record := s.oldestRecord; record != nil; record = record.newer {
		if record.id == recordID && !record.evicted {
			return record, nil
		}
	}
	if s.ownsRecordID(recordID) {
		return nil, ErrRecordEvicted
	}
	return nil, ErrRecordNotFound
}

// noteEvictionLocked retains exact gap semantics in interval form. At most one
// range can exist between adjacent retained records, so the index is bounded by
// live retention rather than session duration.
func (s *sessionState) noteEvictionLocked(record *recordState) {
	sequence := record.summary.RecordSequence
	var next *evictionRange
	for next = s.evictionRangeFirst; next != nil && next.last < sequence; next = next.after {
	}
	var previous *evictionRange
	if next != nil {
		previous = next.before
	} else {
		previous = s.evictionRangeLast
	}

	joinsPrevious := previous != nil && (previous.last == ^uint64(0) || previous.last+1 >= sequence)
	joinsNext := next != nil && (sequence == ^uint64(0) || sequence+1 >= next.first)
	switch {
	case joinsPrevious && joinsNext:
		previous.last = next.last
		s.removeEvictionRangeLocked(next)
	case joinsPrevious:
		if sequence > previous.last {
			previous.last = sequence
		}
	case joinsNext:
		if sequence < next.first {
			next.first = sequence
		}
	default:
		indexCharge := evictionRangeChargeBytes
		if indexCharge > record.charge {
			indexCharge = record.charge
		}
		record.charge -= indexCharge
		s.evictionIndexCharge += indexCharge
		node := &evictionRange{
			first:  sequence,
			last:   sequence,
			before: previous,
			after:  next,
		}
		if previous == nil {
			s.evictionRangeFirst = node
		} else {
			previous.after = node
		}
		if next == nil {
			s.evictionRangeLast = node
		} else {
			next.before = node
		}
		s.evictionRangeCount++
	}
}

func (s *sessionState) removeEvictionRangeLocked(target *evictionRange) {
	if target.before == nil {
		s.evictionRangeFirst = target.after
	} else {
		target.before.after = target.after
	}
	if target.after == nil {
		s.evictionRangeLast = target.before
	} else {
		target.after.before = target.before
	}
	target.before = nil
	target.after = nil
	if s.evictionRangeCount > 0 {
		s.evictionRangeCount--
	}
	if s.evictionIndexCharge >= evictionRangeChargeBytes {
		s.evictionIndexCharge -= evictionRangeChargeBytes
		s.releaseLocked(evictionRangeChargeBytes)
	}
}

func (s *sessionState) evictionCountBetweenLocked(lower, upper uint64) uint64 {
	if lower > upper {
		return 0
	}
	var count uint64
	for interval := s.evictionRangeFirst; interval != nil; interval = interval.after {
		if interval.last < lower {
			continue
		}
		if interval.first > upper {
			break
		}
		first := interval.first
		if first < lower {
			first = lower
		}
		last := interval.last
		if last > upper {
			last = upper
		}
		width := last - first + 1
		if ^uint64(0)-count < width {
			return ^uint64(0)
		}
		count += width
	}
	return count
}

func (s *sessionState) releaseEvictionIndexLocked() {
	s.evictionRangeFirst = nil
	s.evictionRangeLast = nil
	s.evictionRangeCount = 0
	if s.evictionIndexCharge > 0 {
		s.releaseLocked(s.evictionIndexCharge)
		s.evictionIndexCharge = 0
	}
}

func (s *sessionState) logMetadataTruncationLocked(
	gateway *gatewayState,
	record *recordState,
	field string,
	limit int,
) {
	fields := []zap.Field{
		zap.String("session_id", s.id),
		zap.Uint64("generation", s.generation),
		zap.String("field", field),
		zap.Int("retained_limit_bytes", limit),
	}
	if gateway != nil {
		fields = append(fields,
			zap.String("gateway_trace_id", gateway.id),
			zap.String("gateway_request_id", gateway.requestID),
		)
	}
	if record != nil {
		fields = append(fields, zap.String("record_id", record.id))
	}
	s.manager.cfg.logger.Warn("request capture metadata truncated", fields...)
}

func (r *recordState) stateFaultLocked(reason string) {
	r.markOverflowLocked()
	r.disabled = true
	if r.stateFaultLogged {
		return
	}
	r.stateFaultLogged = true
	r.session.manager.cfg.logger.Warn("request capture recorder state transition rejected",
		zap.String("session_id", r.session.id),
		zap.Uint64("generation", r.session.generation),
		zap.String("gateway_trace_id", r.gateway.id),
		zap.String("gateway_request_id", r.gateway.requestID),
		zap.String("record_id", r.id),
		zap.String("protocol", string(r.protocol)),
		zap.String("reason", reason),
	)
}

func (s *sessionState) logGatewayFinalizerLocked(
	gateway *gatewayState,
	record *recordState,
	reason TerminationReason,
) {
	s.manager.cfg.logger.Warn("request capture recorder finalized by gateway safety net",
		zap.String("session_id", s.id),
		zap.Uint64("generation", s.generation),
		zap.String("gateway_trace_id", gateway.id),
		zap.String("gateway_request_id", gateway.requestID),
		zap.String("record_id", record.id),
		zap.String("termination_reason", string(reason)),
	)
}

func (s *sessionState) evictOldestCompletedLocked(exclude *recordState) bool {
	for record := s.oldestRecord; record != nil; record = record.newer {
		if record == exclude || record.evicted || !record.completed {
			continue
		}
		s.evictRecordLocked(record)
		return true
	}
	return false
}

func (s *sessionState) evictRecordLocked(record *recordState) {
	if record == nil || record.evicted {
		return
	}
	s.noteEvictionLocked(record)
	s.evictedCount++
	s.detachAndReleaseRecordLocked(record, true)
}

func (s *sessionState) detachAndReleaseRecordLocked(record *recordState, truncateHistory bool) {
	if record == nil {
		return
	}
	record.evicted = true
	s.removeRecordLocked(record)
	_ = s.removeProviderRecordLocked(record)
	gateway, entryCharge := s.detachRecordTraceLocked(record, truncateHistory)
	s.releaseRecordLocked(record)
	if entryCharge > 0 {
		s.releaseLocked(entryCharge)
	}
	if truncateHistory && gateway != nil && gateway.finished && gateway.liveRecords == 0 {
		s.releaseTraceLocked(gateway)
	}
}

func (s *sessionState) detachRecordTraceLocked(
	record *recordState,
	truncateHistory bool,
) (*gatewayState, int64) {
	gateway := record.gateway
	if gateway == nil {
		return nil, 0
	}
	entryCharge := int64(0)
	entry := record.traceEntry
	if entry != nil {
		if truncateHistory {
			hasBefore, hasAfter := gateway.liveRecordsAroundLocked(entry)
			if !hasBefore && !hasAfter {
				hasBefore = true
				hasAfter = true
			}
			gateway.markHistoryTruncatedLocked(hasAfter, hasBefore)
		}
		entryCharge = gateway.severEntryLocked(entry)
		record.traceEntry = nil
	}
	if gateway.liveRecords > 0 {
		gateway.liveRecords--
	}
	return gateway, entryCharge
}

func (s *sessionState) appendProviderRecordLocked(providerID string, record *recordState) bool {
	index := s.providerRecords[providerID]
	if index == nil || record == nil || record.providerBefore != nil || record.providerAfter != nil {
		return false
	}
	record.providerBefore = index.last
	if index.last == nil {
		index.first = record
	} else {
		index.last.providerAfter = record
	}
	index.last = record
	index.count++
	return true
}

func (s *sessionState) removeProviderRecordLocked(record *recordState) bool {
	if record == nil {
		return false
	}
	index := s.providerRecords[record.summary.Provider.ID]
	if index == nil || index.count <= 0 {
		return false
	}
	if record.providerBefore == nil {
		if index.first != record {
			return false
		}
		index.first = record.providerAfter
	} else {
		record.providerBefore.providerAfter = record.providerAfter
	}
	if record.providerAfter == nil {
		if index.last != record {
			return false
		}
		index.last = record.providerBefore
	} else {
		record.providerAfter.providerBefore = record.providerBefore
	}
	record.providerBefore = nil
	record.providerAfter = nil
	index.count--
	return true
}
