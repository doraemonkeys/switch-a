package requestcapture

import (
	"strconv"
	"strings"
	"testing"
	"unsafe"
)

func TestParseMessageIDRequiresCanonicalBoundedForm(t *testing.T) {
	maximum := ^uint64(0)
	valid := makeMessageID(maximum, maximum, maximum)
	generation, traceSequence, lineage, ok := parseMessageID(valid)
	if !ok || generation != maximum || traceSequence != maximum || lineage != maximum {
		t.Fatalf("parseMessageID(%q) = (%d, %d, %d, %t)", valid, generation, traceSequence, lineage, ok)
	}

	for _, value := range []string{
		"wm_0_1_1",
		"wm_01_1_1",
		"wm_1_01_1",
		"wm_1_1_01",
		"WM_1_1_1",
		"wm_1_1_1_extra",
		"wm_" + strings.Repeat("z", maxBase36Uint64Bytes+1) + "_1_1",
		"wm_" + strconv.FormatUint(maximum, 36) + "0_1_1",
		strings.Repeat("x", 1<<20),
	} {
		if _, _, _, valid := parseMessageID(value); valid {
			t.Fatalf("parseMessageID(%q) unexpectedly succeeded", value)
		}
	}
}

func TestBoundedOpaqueIDRejectsBeforeScanningAndTightClones(t *testing.T) {
	oversized := strings.Repeat("x", maxRetainedIdentifierBytes+1)
	if got, truncated := boundedOpaqueID("ignored", oversized); !truncated || got != truncatedOpaqueID {
		t.Fatalf("boundedOpaqueID(oversized) = (%q, %t)", got, truncated)
	}

	backing := "  id  " + strings.Repeat("x", 1<<20)
	source := backing[:6]
	got, truncated := boundedOpaqueID("ignored", source)
	if truncated || got != "id" {
		t.Fatalf("boundedOpaqueID(%q) = (%q, %t)", source, got, truncated)
	}
	if unsafe.StringData(got) == unsafe.StringData(strings.TrimSpace(source)) {
		t.Fatal("boundedOpaqueID retained the caller's backing allocation")
	}
}

func TestParseRecordIDRejectsBeforeFixedBufferDecode(t *testing.T) {
	session := &sessionState{id: "session", generation: 7, nextRecordSequence: 9}
	recordID := session.makeRecordID(9)
	parsed, ok := parseRecordID(recordID)
	if !ok || parsed.generation != 7 || parsed.sequence != 9 || !session.ownsRecordID(recordID) {
		t.Fatalf("parseRecordID(%q) = (%+v, %t)", recordID, parsed, ok)
	}

	for _, value := range []string{
		recordID + "A",
		recordID[:len(recordID)-1],
		recordID[:len(recordIDPrefix)] + strings.Repeat("!", recordIDEncodedBytes),
		strings.Repeat("A", 1<<20),
	} {
		if _, valid := parseRecordID(value); valid {
			t.Fatalf("parseRecordID(len=%d) unexpectedly succeeded", len(value))
		}
	}
	huge := strings.Repeat("A", 1<<20)
	if allocations := testing.AllocsPerRun(100, func() {
		_, _ = parseRecordID(huge)
	}); allocations != 0 {
		t.Fatalf("oversized record ID rejection allocated %v times/run", allocations)
	}
}

func TestCanonicalEnumsDoNotRetainCallerBacking(t *testing.T) {
	backing := "initial" + strings.Repeat("x", 1<<20)
	input := SelectionMode(backing[:len(SelectionModeInitial)])
	canonical, ok := canonicalSelectionMode(input)
	if !ok || canonical != SelectionModeInitial {
		t.Fatalf("canonicalSelectionMode(%q) = (%q, %t)", input, canonical, ok)
	}
	if unsafe.StringData(string(canonical)) == unsafe.StringData(string(input)) {
		t.Fatal("canonical enum retained the caller's oversized backing allocation")
	}
}
