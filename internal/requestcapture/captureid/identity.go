package captureid

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"strconv"
	"strings"

	"github.com/google/uuid"
)

const (
	TruncatedOpaqueID               = "[TRUNCATED_OPAQUE_ID]"
	sessionIDPrefix                 = "cs_"
	sessionIDRandomHexBytes         = 24
	GeneratedOpaqueIDPrefix         = "id_"
	GeneratedOpaqueIDBytes          = len(GeneratedOpaqueIDPrefix) + 2*16
	TraceIDPrefix                   = "gt_"
	MessageIDPrefix                 = "wm_"
	MaxBase36Uint64Bytes            = 13
	MaxSessionIDBytes               = len(sessionIDPrefix) + MaxBase36Uint64Bytes + len("_") + sessionIDRandomHexBytes
	MaxCanonicalMessageIDBytes      = len(MessageIDPrefix) + 3*MaxBase36Uint64Bytes + 2
	RecordIDPrefix                  = "rc1_"
	RecordIDTagBytes                = 12
	recordIDVersion            byte = 1
	recordIDPayloadBytes            = 1 + 8 + 8 + RecordIDTagBytes
	RecordIDEncodedBytes            = (recordIDPayloadBytes*8 + 5) / 6
	CanonicalRecordIDBytes          = len(RecordIDPrefix) + RecordIDEncodedBytes
)

var strictRecordIDEncoding = base64.RawURLEncoding.Strict()

type RecordIDValue struct {
	generation uint64
	sequence   uint64
	tag        [RecordIDTagBytes]byte
}

type ParsedRecordID struct {
	Generation uint64
	Sequence   uint64
	Tag        [RecordIDTagBytes]byte
}

func NewUUID() ([16]byte, error) {
	value, err := uuid.NewRandom()
	return [16]byte(value), err
}

func MakeSessionID(generation uint64, generated [16]byte) string {
	return sessionIDPrefix + strconv.FormatUint(generation, 36) + "_" + hex.EncodeToString(generated[:12])
}

func IsCanonicalSessionID(value string) bool {
	if len(value) < len(sessionIDPrefix)+1+1+sessionIDRandomHexBytes ||
		len(value) > MaxSessionIDBytes ||
		!strings.HasPrefix(value, sessionIDPrefix) {
		return false
	}
	_, separator, ok := parseBase36Component(value, len(sessionIDPrefix))
	if !ok || separator+1+sessionIDRandomHexBytes != len(value) || value[separator] != '_' {
		return false
	}
	for index := separator + 1; index < len(value); index++ {
		character := value[index]
		if character >= '0' && character <= '9' ||
			character >= 'a' && character <= 'f' {
			continue
		}
		return false
	}
	return true
}

func FormatGeneratedOpaqueID(generated [16]byte) string {
	return MaterializeGeneratedOpaqueID(generated)
}

func MakeTraceID(generation, sequence uint64) string {
	return MaterializeTraceID(generation, sequence)
}

func MakeTraceEntryID(generation, traceSequence, entrySequence uint64) string {
	return "te_" + strconv.FormatUint(generation, 36) + "_" +
		strconv.FormatUint(traceSequence, 36) + "_" + strconv.FormatUint(entrySequence, 36)
}

func MakeMessageID(generation, traceSequence, lineage uint64) string {
	return MaterializeMessageID(generation, traceSequence, lineage)
}

func MaterializeGeneratedOpaqueID(generated [16]byte) string {
	var buffer [GeneratedOpaqueIDBytes]byte
	copy(buffer[:], GeneratedOpaqueIDPrefix)
	hex.Encode(buffer[len(GeneratedOpaqueIDPrefix):], generated[:])
	return string(buffer[:])
}

func MaterializeTraceID(generation, sequence uint64) string {
	var buffer [len(TraceIDPrefix) + 2*MaxBase36Uint64Bytes + 1]byte
	encoded := append(buffer[:0], TraceIDPrefix...)
	encoded = strconv.AppendUint(encoded, generation, 36)
	encoded = append(encoded, '_')
	encoded = strconv.AppendUint(encoded, sequence, 36)
	return string(encoded)
}

func MaterializeMessageID(generation, traceSequence, lineage uint64) string {
	var buffer [MaxCanonicalMessageIDBytes]byte
	encoded := append(buffer[:0], MessageIDPrefix...)
	encoded = strconv.AppendUint(encoded, generation, 36)
	encoded = append(encoded, '_')
	encoded = strconv.AppendUint(encoded, traceSequence, 36)
	encoded = append(encoded, '_')
	encoded = strconv.AppendUint(encoded, lineage, 36)
	return string(encoded)
}

func TraceIDEncodedBytes(generation, sequence uint64) int {
	return len(TraceIDPrefix) + Base36Uint64Bytes(generation) + 1 + Base36Uint64Bytes(sequence)
}

func MessageIDEncodedBytes(generation, traceSequence, lineage uint64) int {
	return len(MessageIDPrefix) + Base36Uint64Bytes(generation) + 1 +
		Base36Uint64Bytes(traceSequence) + 1 + Base36Uint64Bytes(lineage)
}

func Base36Uint64Bytes(value uint64) int {
	if value == 0 {
		return 1
	}
	bytes := 0
	for value > 0 {
		bytes++
		value /= 36
	}
	return bytes
}

func ParseMessageID(value string) (generation, traceSequence, lineage uint64, ok bool) {
	if len(value) < len(MessageIDPrefix)+5 ||
		len(value) > MaxCanonicalMessageIDBytes ||
		!strings.HasPrefix(value, MessageIDPrefix) {
		return 0, 0, 0, false
	}

	generation, next, ok := parseBase36Component(value, len(MessageIDPrefix))
	if !ok || next >= len(value) || value[next] != '_' {
		return 0, 0, 0, false
	}
	traceSequence, next, ok = parseBase36Component(value, next+1)
	if !ok || next >= len(value) || value[next] != '_' {
		return 0, 0, 0, false
	}
	lineage, next, ok = parseBase36Component(value, next+1)
	if !ok || next != len(value) {
		return 0, 0, 0, false
	}
	return generation, traceSequence, lineage, true
}

func MakeRecordIDValue(sessionID string, generation, sequence uint64) RecordIDValue {
	fullTag := recordIDTag(sessionID, generation, sequence)
	value := RecordIDValue{generation: generation, sequence: sequence}
	copy(value.tag[:], fullTag[:RecordIDTagBytes])
	return value
}

func (value RecordIDValue) Valid() bool {
	return value.generation != 0 && value.sequence != 0
}

func (value RecordIDValue) String() string {
	if !value.Valid() {
		return ""
	}
	var payload [recordIDPayloadBytes]byte
	payload[0] = recordIDVersion
	binary.BigEndian.PutUint64(payload[1:9], value.generation)
	binary.BigEndian.PutUint64(payload[9:17], value.sequence)
	copy(payload[17:], value.tag[:])
	return RecordIDPrefix + base64.RawURLEncoding.EncodeToString(payload[:])
}

func ParseRecordID(recordID string) (ParsedRecordID, bool) {
	if len(recordID) != CanonicalRecordIDBytes || !strings.HasPrefix(recordID, RecordIDPrefix) {
		return ParsedRecordID{}, false
	}
	encoded := recordID[len(RecordIDPrefix):]
	if strings.ContainsAny(encoded, "=\r\n") {
		return ParsedRecordID{}, false
	}
	var raw [recordIDPayloadBytes]byte
	decodedBytes, err := strictRecordIDEncoding.Decode(raw[:], []byte(encoded))
	if err != nil || decodedBytes != len(raw) || raw[0] != recordIDVersion {
		return ParsedRecordID{}, false
	}
	parsed := ParsedRecordID{
		Generation: binary.BigEndian.Uint64(raw[1:9]),
		Sequence:   binary.BigEndian.Uint64(raw[9:17]),
	}
	copy(parsed.Tag[:], raw[17:])
	return parsed, parsed.Generation != 0 && parsed.Sequence != 0
}

func OwnsRecordID(sessionID string, generation uint64, recordID string) bool {
	parsed, ok := ParseRecordID(recordID)
	if !ok || parsed.Generation != generation {
		return false
	}
	expected := recordIDTag(sessionID, parsed.Generation, parsed.Sequence)
	return subtle.ConstantTimeCompare(parsed.Tag[:], expected[:RecordIDTagBytes]) == 1
}

func recordIDTag(sessionID string, generation, sequence uint64) [sha256.Size]byte {
	var material [MaxSessionIDBytes + 16]byte
	if len(sessionID) == 0 || len(sessionID) > MaxSessionIDBytes {
		return [sha256.Size]byte{}
	}
	length := copy(material[:], sessionID)
	binary.BigEndian.PutUint64(material[length:length+8], generation)
	binary.BigEndian.PutUint64(material[length+8:length+16], sequence)
	return sha256.Sum256(material[:length+16])
}

func parseBase36Component(value string, start int) (uint64, int, bool) {
	if start >= len(value) || value[start] == '0' {
		return 0, start, false
	}
	number := uint64(0)
	index := start
	for ; index < len(value) && value[index] != '_'; index++ {
		character := value[index]
		var digit uint64
		switch {
		case character >= '0' && character <= '9':
			digit = uint64(character - '0')
		case character >= 'a' && character <= 'z':
			digit = uint64(character-'a') + 10
		default:
			return 0, index, false
		}
		if number > (^uint64(0)-digit)/36 {
			return 0, index, false
		}
		number = number*36 + digit
	}
	if index == start || number == 0 || index-start > MaxBase36Uint64Bytes {
		return 0, index, false
	}
	return number, index, true
}

func BoundedOpaqueID(value string, maximumBytes int) (string, bool) {
	if maximumBytes < 0 || len(value) > maximumBytes {
		return TruncatedOpaqueID, true
	}
	return strings.Clone(strings.TrimSpace(value)), false
}
