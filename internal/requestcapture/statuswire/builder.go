package statuswire

import (
	"strconv"
	"time"
	"unicode/utf8"
)

type Process struct {
	Ceiling   int64
	Charged   int64
	Retained  int64
	Pinned    int64
	Releasing int64
	Temporary int64
}

// Builder owns the fail-closed mechanics for encoding into caller-reserved
// storage. Once any append exceeds capacity, later writes remain inert.
type Builder struct {
	storage  []byte
	length   int
	overflow bool
}

func New(storage []byte) Builder {
	return Builder{storage: storage}
}

func (builder *Builder) Len() int {
	return builder.length
}

func (builder *Builder) Overflowed() bool {
	return builder.overflow
}

func (builder *Builder) Byte(value byte) {
	if builder.overflow || builder.length >= len(builder.storage) {
		builder.overflow = true
		return
	}
	builder.storage[builder.length] = value
	builder.length++
}

func (builder *Builder) Literal(value string) {
	if builder.overflow || len(value) > len(builder.storage)-builder.length {
		builder.overflow = true
		return
	}
	copy(builder.storage[builder.length:], value)
	builder.length += len(value)
}

func (builder *Builder) Bytes(value []byte) {
	if builder.overflow || len(value) > len(builder.storage)-builder.length {
		builder.overflow = true
		return
	}
	copy(builder.storage[builder.length:], value)
	builder.length += len(value)
}

func (builder *Builder) Quoted(value string) {
	builder.Byte('"')
	const hexDigits = "0123456789abcdef"
	for len(value) > 0 && !builder.overflow {
		if value[0] < utf8.RuneSelf {
			current := value[0]
			switch current {
			case '"', '\\':
				builder.Byte('\\')
				builder.Byte(current)
			case '\b':
				builder.Literal(`\b`)
			case '\f':
				builder.Literal(`\f`)
			case '\n':
				builder.Literal(`\n`)
			case '\r':
				builder.Literal(`\r`)
			case '\t':
				builder.Literal(`\t`)
			default:
				if current < 0x20 {
					builder.Literal(`\u00`)
					builder.Byte(hexDigits[current>>4])
					builder.Byte(hexDigits[current&0x0f])
				} else {
					builder.Byte(current)
				}
			}
			value = value[1:]
			continue
		}
		_, size := utf8.DecodeRuneInString(value)
		if size == 1 {
			builder.Literal("�")
		} else {
			builder.Literal(value[:size])
		}
		value = value[size:]
	}
	builder.Byte('"')
}

func (builder *Builder) Int64(value int64) {
	var storage [20]byte
	builder.Bytes(strconv.AppendInt(storage[:0], value, 10))
}

func (builder *Builder) Uint64(value uint64) {
	var storage [20]byte
	builder.Bytes(strconv.AppendUint(storage[:0], value, 10))
}

func (builder *Builder) Int(value int) {
	builder.Int64(int64(value))
}

func (builder *Builder) Timestamp(unixNano int64) {
	var storage [32]byte
	encoded := time.Unix(0, unixNano).UTC().AppendFormat(storage[:0], time.RFC3339Nano)
	builder.Byte('"')
	builder.Bytes(encoded)
	builder.Byte('"')
}

func (builder *Builder) Process(snapshot Process) {
	builder.Literal(`"process_memory":{"ceiling_bytes":`)
	builder.Int64(snapshot.Ceiling)
	builder.Literal(`,"charged_bytes":`)
	builder.Int64(snapshot.Charged)
	builder.Literal(`,"retained_bytes":`)
	builder.Int64(snapshot.Retained)
	builder.Literal(`,"pinned_bytes":`)
	builder.Int64(snapshot.Pinned)
	builder.Literal(`,"releasing_bytes":`)
	builder.Int64(snapshot.Releasing)
	builder.Literal(`,"temporary_bytes":`)
	builder.Int64(snapshot.Temporary)
	builder.Byte('}')
}

func QuotedBytes(value string) int {
	length := 2
	for len(value) > 0 {
		if value[0] < utf8.RuneSelf {
			switch value[0] {
			case '"', '\\', '\b', '\f', '\n', '\r', '\t':
				length += 2
			default:
				if value[0] < 0x20 {
					length += 6
				} else {
					length++
				}
			}
			value = value[1:]
			continue
		}
		_, size := utf8.DecodeRuneInString(value)
		if size == 1 {
			length += len("�")
		} else {
			length += size
		}
		value = value[size:]
	}
	return length
}
