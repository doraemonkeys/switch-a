package adapters

import (
	"strings"
	"testing"

	"github.com/doraemonkeys/switch-a/internal/responseanalysis/allocation"
	"github.com/doraemonkeys/switch-a/internal/responseanalysis/framing"
)

func TestCanonicalNumber(t *testing.T) {
	t.Parallel()
	tests := map[string]string{
		"0":         "0",
		"-0.000":    "0",
		"503":       "503",
		"5.03e2":    "503",
		"1.2300":    "1.23",
		"0.00120":   "0.0012",
		"12e-3":     "0.012",
		"1e3":       "1000",
		"-1.25E+2":  "-125",
		"1000.0000": "1000",
	}
	for input, want := range tests {
		got, status := canonicalForTest(input, 256)
		if status != fieldValid || got != want {
			t.Errorf("canonicalNumber(%q) = %q, %v; want %q", input, got, status, want)
		}
	}
	for _, input := range []string{"", `"5"`, "true", "null", "{}"} {
		if _, status := canonicalForTest(input, 256); status != fieldInvalid {
			t.Errorf("canonicalNumber(%q) status = %v", input, status)
		}
	}
	if got, status := canonicalForTest("0e999999999999999999999", 1); status != fieldValid || got != "0" {
		t.Fatalf("zero exponent normalization = %q,%v", got, status)
	}
	if _, status := canonicalForTest("1e999999999999999999999", 4); status != fieldTooLarge {
		t.Fatalf("overflow exponent status = %v", status)
	}
	if _, status := canonicalForTest("1e10", 4); status != fieldTooLarge {
		t.Fatalf("expanded number status = %v", status)
	}
}

func canonicalForTest(input string, maxBytes int) (string, fieldStatus) {
	document, ok := decodeDocument([]byte(input))
	if !ok {
		return "", fieldInvalid
	}
	resources := testResourceContext()
	defer resources.release()
	return canonicalNumber(document.data, document.root, maxBytes, &resources)
}

func TestFieldHelpersDistinguishAbsentInvalidAndOversized(t *testing.T) {
	t.Parallel()
	document := mustDocument(t, `{"string":"value","number":1.2e1,"invalid":true,"object":{"child":"yes"}}`)
	resources := testResourceContext()
	defer resources.release()
	if value, status := stringField(document, document.root, "string", 5, &resources); value != "value" || status != fieldValid {
		t.Fatalf("string = %q, %v", value, status)
	}
	if _, status := stringField(document, document.root, "missing", 5, &resources); status != fieldAbsent {
		t.Fatalf("missing = %v", status)
	}
	if _, status := stringField(document, document.root, "invalid", 5, &resources); status != fieldInvalid {
		t.Fatalf("invalid = %v", status)
	}
	if _, status := stringField(document, document.root, "string", 4, &resources); status != fieldTooLarge {
		t.Fatalf("oversized = %v", status)
	}
	if value, status := scalarField(document, document.root, "number", 5, &resources); value != "12" || status != fieldValid {
		t.Fatalf("number = %q, %v", value, status)
	}
	child, ok := objectField(document, document.root, "object")
	if !ok {
		t.Fatal("object field absent")
	}
	if matched, status := exactStringFieldEquals(document, child, "child", "yes", 3); !matched || status != fieldValid {
		t.Fatalf("child match = %v,%v", matched, status)
	}
	if matched, status := exactStringFieldEquals(document, child, "child", "yes", 2); matched || status != fieldTooLarge {
		t.Fatalf("oversized exact match = %v,%v", matched, status)
	}
	if matched, status := exactStringFieldEquals(document, document.root, "invalid", "yes", 3); matched || status != fieldInvalid {
		t.Fatalf("invalid exact match = %v,%v", matched, status)
	}
	if matched, status := exactStringFieldEquals(document, document.root, "missing", "yes", 3); matched || status != fieldAbsent {
		t.Fatalf("absent exact match = %v,%v", matched, status)
	}
	if _, ok := objectField(document, document.root, "invalid"); ok {
		t.Fatal("scalar became object")
	}
}

func TestScannerCoversEscapesAndGrammarFailureBoundaries(t *testing.T) {
	t.Parallel()
	document := mustDocument(t, `{"field":"\"\\\/\b\f\n\r\t\u0041\uD83D\uDE00\uD800\u0042\uDC00","array":[1,2],"object":{"a":1,"b":2}}`)
	raw, ok := document.objectField(document.root, "field")
	if !ok {
		t.Fatal("escaped field absent")
	}
	var decoded []rune
	if !walkJSONString(document.data, raw, func(value rune) { decoded = append(decoded, value) }) {
		t.Fatal("valid escaped string was rejected")
	}
	want := []rune{'"', '\\', '/', '\b', '\f', '\n', '\r', '\t', 'A', '😀', '�', 'B', '�'}
	if string(decoded) != string(want) {
		t.Fatalf("decoded = %q, want %q", string(decoded), string(want))
	}
	array, _ := document.objectField(document.root, "array")
	visited := 0
	document.arrayValues(array, func(jsonValue) bool {
		visited++
		return false
	})
	if visited != 1 || document.objectFieldCount(document.root) != 3 {
		t.Fatalf("visited=%d root fields=%d", visited, document.objectFieldCount(document.root))
	}
	document.arrayValues(document.root, func(jsonValue) bool {
		t.Fatal("non-array invoked visitor")
		return true
	})
	if _, ok := (jsonDocument{}).objectField(jsonValue{}, "field"); ok || (jsonDocument{}).objectFieldCount(jsonValue{}) != 0 {
		t.Fatal("invalid parent exposed fields")
	}
	if _, ok := scanJSONString([]byte("x"), 0); ok || validHex4([]byte("abc")) || bytesEqualString([]byte("a"), "ab") {
		t.Fatal("defensive scanner boundary accepted invalid input")
	}

	invalid := []string{
		`{"a" 1}`,
		`{1:2}`,
		`{"a":}`,
		`{"a":1`,
		`{"a":1 "b":2}`,
		`{"a":1,}`,
		`[`,
		`[1`,
		`[1 2]`,
		`[1,]`,
		`tru`,
		`falsx`,
		`nul`,
		`"unterminated`,
		`"bad\`,
		`"bad\q"`,
		`"bad\u12xz"`,
		"\"bad\ncontrol\"",
		`-`,
		`.1`,
		`01`,
		`1.`,
		`1e`,
		`1e+`,
	}
	for _, input := range invalid {
		if _, ok := decodeDocument([]byte(input)); ok {
			t.Errorf("invalid JSON accepted: %q", input)
		}
	}
	tooDeepObject := strings.Repeat(`{"a":`, maxJSONNestingDepth+1) + "0" + strings.Repeat("}", maxJSONNestingDepth+1)
	if _, ok := decodeDocument([]byte(tooDeepObject)); ok {
		t.Fatal("object nesting beyond the fixed scanner stack was accepted")
	}
}

func TestScannerTrimmingAndDocumentBoundaries(t *testing.T) {
	t.Parallel()
	document := mustDocument(t, `{"field":"\u2003 MiXeD İ \t","duplicate":"first","duplicate":"LAST"}`)
	resources := testResourceContext()
	defer resources.release()
	value, status := stringField(document, document.root, "field", 32, &resources)
	if status != fieldValid || value != "MiXeD \u0130" {
		t.Fatalf("trimmed field = %q,%v", value, status)
	}
	caseSensitiveBound := mustDocument(t, `{"field":"\u0130"}`)
	if _, status := stringField(caseSensitiveBound, caseSensitiveBound.root, "field", 1, &resources); status != fieldTooLarge {
		t.Fatalf("case-preserving UTF-8 bound = %v", status)
	}
	duplicate, status := stringField(document, document.root, "duplicate", 8, &resources)
	if status != fieldValid || duplicate != "LAST" {
		t.Fatalf("duplicate = %q,%v", duplicate, status)
	}
	if matched, _ := exactStringFieldEquals(document, document.root, "duplicate", "LAST", 8); !matched {
		t.Fatal("exact discriminator comparison normalized its input")
	}

	for _, valid := range []string{"null", "[]", `"text"`, "-1.2e3", `{"a":[true,false,null]}`} {
		if _, ok := decodeDocument([]byte(valid)); !ok {
			t.Errorf("valid JSON rejected: %s", valid)
		}
	}
	for _, invalid := range []string{"", "{} {}", "[1,]", `{"a":1,}`, `"\x"`, string([]byte{'"', 0xff, '"'})} {
		if _, ok := decodeDocument([]byte(invalid)); ok {
			t.Errorf("invalid JSON accepted: %q", invalid)
		}
	}
	tooDeep := strings.Repeat("[", maxJSONNestingDepth+1) + "0" + strings.Repeat("]", maxJSONNestingDepth+1)
	if _, ok := decodeDocument([]byte(tooDeep)); ok {
		t.Fatal("nesting beyond the fixed scanner stack was accepted")
	}
}

func TestGoogleDetailsAreRootRelativeAndFirstMatching(t *testing.T) {
	t.Parallel()
	adapter := googleAdapter{baseAdapter{kind: framing.KindJSON, limits: testLimits, reserver: allocation.NoopReserver{}}}
	resources := testResourceContext()
	defer resources.release()
	document := mustDocument(t, `{"details":[null,{"@type":"other","reason":"skip"},{"@type":"google.rpc.ErrorInfo","reason":"FIRST"},{"@type":"type.googleapis.com/google.rpc.ErrorInfo","reason":"second"}]}`)
	value, status := adapter.errorInfoReason(document, document.root, &resources)
	if status != fieldValid || value != "FIRST" {
		t.Fatalf("reason = %q, %v", value, status)
	}
	malformed := mustDocument(t, `{"details":{}}`)
	if _, status := adapter.errorInfoReason(malformed, malformed.root, &resources); status != fieldAbsent {
		t.Fatalf("aggregate details status = %v", status)
	}
	absent := mustDocument(t, `{}`)
	if _, status := adapter.errorInfoReason(absent, absent.root, &resources); status != fieldAbsent {
		t.Fatalf("absent details status = %v", status)
	}
}

func testResourceContext() resourceContext {
	return resourceContext{reserver: allocation.NoopReserver{}}
}

func mustDocument(t *testing.T, data string) jsonDocument {
	t.Helper()
	document, ok := decodeDocument([]byte(data))
	if !ok {
		t.Fatalf("invalid test document: %s", data)
	}
	return document
}
