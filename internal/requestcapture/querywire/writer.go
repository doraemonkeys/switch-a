package querywire

import (
	"strconv"
	"time"

	"github.com/doraemonkeys/switch-a/internal/requestcapture/capturevalue"
	"github.com/doraemonkeys/switch-a/internal/requestcapture/jsonstream"
)

type textSink interface {
	writeByte(byte) error
	writeString(string) error
	writeBytes([]byte) error
}

type stringSink struct {
	sink textSink
}

func (adapter stringSink) WriteByte(value byte) error {
	return adapter.sink.writeByte(value)
}

func (adapter stringSink) WriteString(value string) error {
	return adapter.sink.writeString(value)
}

func (adapter stringSink) WriteBytes(value []byte) error {
	return adapter.sink.writeBytes(value)
}

type documentWriter struct {
	sink textSink
	err  error
}

type jsonDocumentWriter = documentWriter

func (writer *documentWriter) raw(value string) {
	if writer.err == nil {
		writer.err = writer.sink.writeString(value)
	}
}

func (writer *documentWriter) string(value string) {
	if writer.err == nil {
		writer.err = jsonstream.WriteString(stringSink{sink: writer.sink}, value)
	}
}

func (writer *documentWriter) int(value int) {
	writer.int64(int64(value))
}

func (writer *documentWriter) int64(value int64) {
	if writer.err != nil {
		return
	}
	var encoded [32]byte
	writer.err = writer.sink.writeBytes(strconv.AppendInt(encoded[:0], value, 10))
}

func (writer *documentWriter) uint64(value uint64) {
	if writer.err != nil {
		return
	}
	var encoded [32]byte
	writer.err = writer.sink.writeBytes(strconv.AppendUint(encoded[:0], value, 10))
}

func (writer *documentWriter) boolean(value bool) {
	if value {
		writer.raw("true")
		return
	}
	writer.raw("false")
}

func (writer *documentWriter) time(value time.Time) {
	if writer.err != nil {
		return
	}
	if err := writer.sink.writeByte('"'); err != nil {
		writer.err = err
		return
	}
	var encoded [64]byte
	rendered := value.AppendFormat(encoded[:0], time.RFC3339Nano)
	if err := writer.sink.writeBytes(rendered); err != nil {
		writer.err = err
		return
	}
	writer.err = writer.sink.writeByte('"')
}

func (writer *documentWriter) beginObject() {
	writer.raw("{")
}

func (writer *documentWriter) endObject() {
	writer.raw("}")
}

func (writer *documentWriter) beginArray() {
	writer.raw("[")
}

func (writer *documentWriter) endArray() {
	writer.raw("]")
}

func (writer *documentWriter) field(name string, first *bool) {
	if !*first {
		writer.raw(",")
	}
	*first = false
	writer.string(name)
	writer.raw(":")
}

func writeProviderSnapshotJSON(writer *documentWriter, provider capturevalue.ProviderSnapshot) {
	first := true
	writer.beginObject()
	writer.field("id", &first)
	writer.string(provider.ID)
	writer.field("name", &first)
	writer.string(provider.Name)
	writer.field("api_type", &first)
	writer.string(provider.APIType)
	writer.field("target_url", &first)
	writer.string(provider.TargetURL)
	writer.endObject()
}

func writeWebSocketHandshakeJSON(writer *documentWriter, handshake *capturevalue.WebSocketHandshakeSnapshot) {
	if handshake == nil {
		writer.raw("null")
		return
	}
	first := true
	writer.beginObject()
	writer.field("status_code", &first)
	writer.int(handshake.StatusCode)
	writer.field("protocol", &first)
	writer.string(handshake.Protocol)
	writer.field("headers", &first)
	writeHeadersJSON(writer, handshake.Headers)
	writer.endObject()
}

func writeHeadersJSON(writer *documentWriter, headers map[string][]string) {
	if headers == nil {
		writer.raw("null")
		return
	}
	first := true
	writer.beginObject()
	for name, values := range headers {
		writer.field(name, &first)
		writeStringsJSON(writer, values)
	}
	writer.endObject()
}

func writeStringsJSON(writer *documentWriter, values []string) {
	if values == nil {
		writer.raw("null")
		return
	}
	writer.beginArray()
	for index, value := range values {
		if index > 0 {
			writer.raw(",")
		}
		writer.string(value)
	}
	writer.endArray()
}
