package jsonstream

import (
	"errors"
	"testing"
)

type stringSink struct {
	bytes  []byte
	calls  int
	failAt int
}

func (sink *stringSink) ready() error {
	sink.calls++
	if sink.failAt > 0 && sink.calls == sink.failAt {
		return errors.New("sink failed")
	}
	return nil
}

func (sink *stringSink) WriteByte(value byte) error {
	if err := sink.ready(); err != nil {
		return err
	}
	sink.bytes = append(sink.bytes, value)
	return nil
}

func (sink *stringSink) WriteString(value string) error {
	if err := sink.ready(); err != nil {
		return err
	}
	sink.bytes = append(sink.bytes, value...)
	return nil
}

func (sink *stringSink) WriteBytes(value []byte) error {
	if err := sink.ready(); err != nil {
		return err
	}
	sink.bytes = append(sink.bytes, value...)
	return nil
}

func TestWriteStringEncodesEveryEscapeClass(t *testing.T) {
	tests := []struct {
		name  string
		value string
		want  string
	}{
		{name: "plain", value: "plain 世界", want: "\"plain 世界\""},
		{name: "named escapes", value: "\"\\\b\f\n\r\t", want: "\"\\\"\\\\\\b\\f\\n\\r\\t\""},
		{name: "control", value: string([]byte{0x00, 0x1f}), want: "\"\\u0000\\u001f\""},
		{name: "line separators", value: "\u2028\u2029", want: "\"\\u2028\\u2029\""},
		{name: "invalid UTF-8", value: string([]byte{0xff}), want: "\"\\ufffd\""},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			sink := &stringSink{}
			if err := WriteString(sink, test.value); err != nil {
				t.Fatalf("WriteString() error = %v", err)
			}
			if got := string(sink.bytes); got != test.want {
				t.Fatalf("WriteString() = %q, want %q", got, test.want)
			}
		})
	}
}

func TestWriteStringPropagatesSinkFailures(t *testing.T) {
	for failAt := 1; failAt <= 4; failAt++ {
		sink := &stringSink{failAt: failAt}
		if err := WriteString(sink, string([]byte{0x00})+"value"); err == nil {
			t.Fatalf("WriteString() with failure at call %d unexpectedly succeeded", failAt)
		}
	}
	for _, value := range []string{
		string([]byte{0xff}),
		" ",
		" ",
		"世界",
		"\"",
	} {
		sink := &stringSink{failAt: 2}
		if err := WriteString(sink, value); err == nil {
			t.Fatalf("WriteString(%q) unexpectedly ignored sink failure", value)
		}
	}
}

func TestWriteEscapedPrefixWritesValidMultibyteRune(t *testing.T) {
	t.Parallel()

	sink := &stringSink{}
	consumed, err := writeEscapedPrefix(sink, "界")
	if err != nil {
		t.Fatalf("writeEscapedPrefix() error = %v", err)
	}
	if consumed != len("界") || string(sink.bytes) != "界" {
		t.Fatalf("writeEscapedPrefix() = (%d, %q)", consumed, sink.bytes)
	}
}
