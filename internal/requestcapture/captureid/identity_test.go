package captureid

import (
	"encoding/base64"
	"encoding/binary"
	"math"
	"strings"
	"testing"
)

func TestIdentityFormatsAndValidatesCanonicalValues(t *testing.T) {
	generated := [16]byte{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13}
	sessionID := MakeSessionID(math.MaxUint64, generated)
	if !IsCanonicalSessionID(sessionID) {
		t.Fatalf("generated session ID %q is not canonical", sessionID)
	}
	if got := FormatGeneratedOpaqueID(generated); got != "id_0102030405060708090a0b0c0d000000" {
		t.Fatalf("FormatGeneratedOpaqueID() = %q", got)
	}
	if got := MakeTraceID(35, 36); got != "gt_z_10" {
		t.Fatalf("MakeTraceID() = %q", got)
	}
	if got := MakeTraceEntryID(1, 2, 3); got != "te_1_2_3" {
		t.Fatalf("MakeTraceEntryID() = %q", got)
	}

	messageID := MakeMessageID(math.MaxUint64, math.MaxUint64, math.MaxUint64)
	generation, trace, lineage, ok := ParseMessageID(messageID)
	if !ok || generation != math.MaxUint64 || trace != math.MaxUint64 || lineage != math.MaxUint64 {
		t.Fatalf("ParseMessageID(%q) = (%d, %d, %d, %t)", messageID, generation, trace, lineage, ok)
	}
}

func TestIdentityRejectsNonCanonicalValues(t *testing.T) {
	validRandom := strings.Repeat("a", sessionIDRandomHexBytes)
	for _, value := range []string{
		"",
		"cs_0_" + validRandom,
		"cs_01_" + validRandom,
		"cs_A_" + validRandom,
		"cs_1_" + strings.Repeat("a", sessionIDRandomHexBytes-1),
		"cs_1_" + strings.Repeat("A", sessionIDRandomHexBytes),
		"cs_1_" + strings.Repeat("g", sessionIDRandomHexBytes),
		"cs_" + strings.Repeat("z", MaxBase36Uint64Bytes+1) + "_" + validRandom,
	} {
		if IsCanonicalSessionID(value) {
			t.Fatalf("IsCanonicalSessionID(%q) unexpectedly succeeded", value)
		}
	}

	for _, value := range []string{
		"",
		"wm_0_1_1",
		"wm_01_1_1",
		"wm_A_1_1",
		"wm_1_0_1",
		"wm_1_1_0",
		"wm_1_1",
		"wm_1_1_1_extra",
		"wm_" + strings.Repeat("z", MaxBase36Uint64Bytes+1) + "_1_1",
	} {
		if _, _, _, ok := ParseMessageID(value); ok {
			t.Fatalf("ParseMessageID(%q) unexpectedly succeeded", value)
		}
	}
}

func TestBoundedOpaqueIDOwnsOrTruncatesInput(t *testing.T) {
	backing := []byte("  value  ")
	value, truncated := BoundedOpaqueID(string(backing), len(backing))
	if truncated || value != "value" {
		t.Fatalf("BoundedOpaqueID() = (%q, %t)", value, truncated)
	}
	backing[2] = 'X'
	if value != "value" {
		t.Fatal("bounded value retained caller storage")
	}

	if value, truncated = BoundedOpaqueID("oversized", 3); !truncated || value != TruncatedOpaqueID {
		t.Fatalf("oversized BoundedOpaqueID() = (%q, %t)", value, truncated)
	}
	if value, truncated = BoundedOpaqueID("", -1); !truncated || value != TruncatedOpaqueID {
		t.Fatalf("negative-bound BoundedOpaqueID() = (%q, %t)", value, truncated)
	}
}

func TestNewUUIDReturnsNonZeroIdentity(t *testing.T) {
	value, err := NewUUID()
	if err != nil {
		t.Fatalf("NewUUID() error = %v", err)
	}
	if value == ([16]byte{}) {
		t.Fatal("NewUUID() returned zero")
	}
}

func TestIdentityMaterializersReportExactEncodedLengths(t *testing.T) {
	generated := [16]byte{0xff, 0x10, 0x20}
	if got, want := MaterializeGeneratedOpaqueID(generated), FormatGeneratedOpaqueID(generated); got != want {
		t.Fatalf("MaterializeGeneratedOpaqueID() = %q, want %q", got, want)
	}

	for _, values := range [][3]uint64{
		{0, 0, 0},
		{1, 35, 36},
		{math.MaxUint64, math.MaxUint64, math.MaxUint64},
	} {
		generation, traceSequence, lineage := values[0], values[1], values[2]
		traceID := MaterializeTraceID(generation, traceSequence)
		if got := TraceIDEncodedBytes(generation, traceSequence); got != len(traceID) {
			t.Fatalf("TraceIDEncodedBytes(%d, %d) = %d, want %d", generation, traceSequence, got, len(traceID))
		}
		messageID := MaterializeMessageID(generation, traceSequence, lineage)
		if got := MessageIDEncodedBytes(generation, traceSequence, lineage); got != len(messageID) {
			t.Fatalf(
				"MessageIDEncodedBytes(%d, %d, %d) = %d, want %d",
				generation,
				traceSequence,
				lineage,
				got,
				len(messageID),
			)
		}
	}

	for value, want := range map[uint64]int{
		0:              1,
		1:              1,
		35:             1,
		36:             2,
		math.MaxUint64: MaxBase36Uint64Bytes,
	} {
		if got := Base36Uint64Bytes(value); got != want {
			t.Fatalf("Base36Uint64Bytes(%d) = %d, want %d", value, got, want)
		}
	}
}

func TestRecordIDRoundTripAndOwnership(t *testing.T) {
	const (
		sessionID  = "cs_7_0123456789abcdef01234567"
		generation = uint64(7)
		sequence   = uint64(9)
	)

	value := MakeRecordIDValue(sessionID, generation, sequence)
	if !value.Valid() {
		t.Fatal("MakeRecordIDValue() returned an invalid value")
	}
	recordID := value.String()
	if len(recordID) != CanonicalRecordIDBytes || !strings.HasPrefix(recordID, RecordIDPrefix) {
		t.Fatalf("RecordIDValue.String() = %q", recordID)
	}
	parsed, ok := ParseRecordID(recordID)
	if !ok || parsed.Generation != generation || parsed.Sequence != sequence || parsed.Tag == ([RecordIDTagBytes]byte{}) {
		t.Fatalf("ParseRecordID(%q) = (%+v, %t)", recordID, parsed, ok)
	}
	if !OwnsRecordID(sessionID, generation, recordID) {
		t.Fatal("OwnsRecordID() rejected the originating session")
	}
	if OwnsRecordID(sessionID, generation+1, recordID) {
		t.Fatal("OwnsRecordID() accepted a different generation")
	}
	if OwnsRecordID(sessionID+"x", generation, recordID) {
		t.Fatal("OwnsRecordID() accepted a different session")
	}
}

func TestRecordIDRejectsInvalidAndTamperedValues(t *testing.T) {
	if (RecordIDValue{}).Valid() || (RecordIDValue{}).String() != "" {
		t.Fatal("zero RecordIDValue must not be materialized")
	}
	if value := MakeRecordIDValue("session", 0, 1); value.Valid() || value.String() != "" {
		t.Fatal("zero generation must not be materialized")
	}
	if value := MakeRecordIDValue("session", 1, 0); value.Valid() || value.String() != "" {
		t.Fatal("zero sequence must not be materialized")
	}

	valid := MakeRecordIDValue("session", 2, 3).String()
	for _, recordID := range []string{
		"",
		valid + "A",
		valid[:len(valid)-1],
		"bad_" + valid[len(RecordIDPrefix):],
		valid[:len(RecordIDPrefix)] + "=" + valid[len(RecordIDPrefix)+1:],
		valid[:len(RecordIDPrefix)] + "!" + valid[len(RecordIDPrefix)+1:],
	} {
		if _, ok := ParseRecordID(recordID); ok {
			t.Fatalf("ParseRecordID(%q) unexpectedly succeeded", recordID)
		}
		if OwnsRecordID("session", 2, recordID) {
			t.Fatalf("OwnsRecordID(%q) unexpectedly succeeded", recordID)
		}
	}

	for _, mutation := range []func([]byte){
		func(raw []byte) { raw[0] = recordIDVersion + 1 },
		func(raw []byte) { binary.BigEndian.PutUint64(raw[1:9], 0) },
		func(raw []byte) { binary.BigEndian.PutUint64(raw[9:17], 0) },
	} {
		raw := decodeRecordIDForTest(t, valid)
		mutation(raw)
		if _, ok := ParseRecordID(RecordIDPrefix + base64.RawURLEncoding.EncodeToString(raw)); ok {
			t.Fatal("ParseRecordID() accepted a noncanonical payload")
		}
	}

	raw := decodeRecordIDForTest(t, valid)
	raw[len(raw)-1] ^= 1
	tampered := RecordIDPrefix + base64.RawURLEncoding.EncodeToString(raw)
	if _, ok := ParseRecordID(tampered); !ok {
		t.Fatal("tag-only mutation should remain structurally parseable")
	}
	if OwnsRecordID("session", 2, tampered) {
		t.Fatal("OwnsRecordID() accepted a tampered authentication tag")
	}
}

func TestRecordIDTagRejectsUnboundedSessionMaterial(t *testing.T) {
	if got := recordIDTag("", 1, 1); got != ([32]byte{}) {
		t.Fatalf("recordIDTag(empty) = %x, want zero", got)
	}
	if got := recordIDTag(strings.Repeat("x", MaxSessionIDBytes+1), 1, 1); got != ([32]byte{}) {
		t.Fatalf("recordIDTag(oversized) = %x, want zero", got)
	}
}

func decodeRecordIDForTest(t *testing.T, recordID string) []byte {
	t.Helper()
	raw, err := base64.RawURLEncoding.DecodeString(recordID[len(RecordIDPrefix):])
	if err != nil {
		t.Fatalf("decode record ID: %v", err)
	}
	return raw
}
