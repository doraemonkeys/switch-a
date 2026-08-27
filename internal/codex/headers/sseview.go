package codexheaders

import "bytes"

var utf8ByteOrderMark = []byte{0xef, 0xbb, 0xbf}

// SSEScan is a read-only view over complete SSE events at the start of a
// caller-owned buffer. The unconsumed suffix is deliberately left to the
// transport because an event can span arbitrary network reads.
type SSEScan struct {
	messages []MessageView
	consumed int
}

func (s SSEScan) Messages() []MessageView { return append([]MessageView(nil), s.messages...) }
func (s SSEScan) ConsumedBytes() int      { return s.consumed }

// ScanServerSSE observes every complete event prefix without rewriting its
// bytes. final permits an unterminated final event only after transport EOF.
func ScanServerSSE(raw []byte, final bool) SSEScan {
	var scan SSEScan
	for scan.consumed < len(raw) {
		end, complete := nextSSEEventEnd(raw, scan.consumed)
		if !complete {
			if !final {
				break
			}
			end = len(raw)
		}
		eventWire := raw[scan.consumed:end]
		scan.messages = append(scan.messages, inspectServerSSEEvent(eventWire))
		scan.consumed = end
	}
	return scan
}

func inspectServerSSEEvent(wire []byte) MessageView {
	semantic, hasData := serverSSEData(wire)
	if !hasData {
		return MessageView{present: true, wire: wire, direction: directionServer}
	}
	return inspectMessage(wire, semantic, directionServer, true)
}

func nextSSEEventEnd(raw []byte, start int) (int, bool) {
	position := start
	for position < len(raw) {
		lineStart := position
		for position < len(raw) && raw[position] != '\r' && raw[position] != '\n' {
			position++
		}
		if position == len(raw) {
			return 0, false
		}
		emptyLine := position == lineStart
		if raw[position] == '\r' && position+1 < len(raw) && raw[position+1] == '\n' {
			position += 2
		} else {
			position++
		}
		if emptyLine {
			return position, true
		}
	}
	return 0, false
}

func serverSSEData(wire []byte) ([]byte, bool) {
	var dataLines [][]byte
	for position := 0; position < len(wire); {
		lineStart := position
		for position < len(wire) && wire[position] != '\r' && wire[position] != '\n' {
			position++
		}
		line := wire[lineStart:position]
		if lineStart == 0 {
			line = bytes.TrimPrefix(line, utf8ByteOrderMark)
		}
		if position < len(wire) {
			if wire[position] == '\r' && position+1 < len(wire) && wire[position+1] == '\n' {
				position += 2
			} else {
				position++
			}
		}
		if len(line) == 0 {
			break
		}
		if line[0] == ':' {
			continue
		}
		field, value, found := bytes.Cut(line, []byte{':'})
		if !found {
			value = nil
		}
		if !bytes.Equal(field, []byte("data")) {
			continue
		}
		if len(value) > 0 && value[0] == ' ' {
			value = value[1:]
		}
		dataLines = append(dataLines, value)
	}
	if len(dataLines) == 0 {
		return nil, false
	}
	if len(dataLines) == 1 {
		return dataLines[0], true
	}
	return bytes.Join(dataLines, []byte{'\n'}), true
}
