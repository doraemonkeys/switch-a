package wire

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"io"
	"mime"
	"strings"

	"github.com/doraemonkeys/switch-a/internal/upstreamtransport"
)

func (s *Session) RequestBody(ctx context.Context, original upstreamtransport.BodySource, encodings []string) (upstreamtransport.BodySource, error) {
	if !s.target.Policy.Enabled {
		return original, nil
	}
	return upstreamtransport.TransformSource(ctx, original, encodings, func(ctx context.Context, source io.Reader, destination io.Writer) error {
		return s.streamJSON(ctx, source, destination, false, "http_request", "$", false)
	}, s.streamFailure("http_request"))
}
func (s *Session) RestoreResponse(ctx context.Context, head upstreamtransport.ResponseHead, body io.ReadCloser) (upstreamtransport.ResponseHead, io.ReadCloser, error) {
	if !s.target.Policy.Enabled {
		return head, body, nil
	}
	restored, err := s.RestoreHeaders(ctx, head.Header)
	if err != nil {
		return head, nil, err
	}
	contentType := head.Header.Get("Content-Type")
	if !head.AllowsBody() || !transformableMedia(contentType) {
		head.Header = restored
		return head, body, nil
	}
	result, err := upstreamtransport.TransformReader(ctx, body, head.Header.Values("Content-Encoding"), func(ctx context.Context, source io.Reader, destination io.Writer) error {
		return s.RestoreStream(ctx, source, destination, contentType)
	}, s.streamFailure("http_response"))
	if err != nil {
		return head, nil, err
	}
	head.Header = restored
	return upstreamtransport.DerivedResponseHead(head), result, nil
}
func (s *Session) streamFailure(carrier string) upstreamtransport.TransformError {
	return func(stage string, err error) error {
		var failure *Failure
		if errors.As(err, &failure) {
			return failure
		}
		var transformed *upstreamtransport.TransformationError
		if errors.As(err, &transformed) {
			return s.failure(transformed.Stage, carrier, "$", transformed.OriginalSnippet, transformed.DerivedSnippet, err)
		}
		return s.failure(stage, carrier, "$", "", "", err)
	}
}
func mediaType(value string) string {
	parsed, _, err := mime.ParseMediaType(value)
	if err != nil {
		return ""
	}
	return strings.ToLower(parsed)
}
func transformableMedia(value string) bool {
	kind := mediaType(value)
	return kind == "text/event-stream" || kind == "application/json" || strings.HasSuffix(kind, "+json")
}
func (s *Session) RestoreStream(ctx context.Context, source io.Reader, destination io.Writer, contentType string) error {
	if !s.target.Policy.Enabled {
		_, err := io.Copy(destination, source)
		return err
	}
	switch kind := mediaType(contentType); {
	case kind == "text/event-stream":
		return s.streamSSE(ctx, source, destination)
	case kind == "application/json" || strings.HasSuffix(kind, "+json"):
		return s.streamJSON(ctx, source, destination, true, "http_response", "$", false)
	default:
		_, err := io.Copy(destination, source)
		return err
	}
}

type sseLine struct {
	raw       []byte
	dataStart int
	dataEnd   int
}

func (s *Session) ServerSSE(ctx context.Context, original []byte) ([]byte, error) {
	if !s.target.Policy.Enabled {
		return original, nil
	}
	lines, data, eventType := parseSSEEvent(original)
	if len(data) == 0 {
		return original, nil
	}
	payload := bytes.Join(data, []byte("\n"))
	kind := frameType(payload)
	if kind == "" {
		kind = eventType
	}
	if !recognizedFrame(kind, true) {
		return original, nil
	}
	transformed, err := s.json(ctx, payload, true, "sse", "$", false)
	if err != nil {
		return nil, err
	}
	if bytes.Equal(payload, transformed) {
		return original, nil
	}
	parts := bytes.Split(transformed, []byte("\n"))
	if len(parts) != len(data) {
		return nil, s.failure("encode", "sse", "$", string(payload), string(transformed), errors.New("JSON transformation changed SSE data line boundaries"))
	}
	var output bytes.Buffer
	index := 0
	for _, line := range lines {
		if line.dataStart < 0 {
			output.Write(line.raw)
			continue
		}
		output.Write(line.raw[:line.dataStart])
		output.Write(parts[index])
		output.Write(line.raw[line.dataEnd:])
		index++
	}
	return output.Bytes(), nil
}
func parseSSEEvent(original []byte) ([]sseLine, [][]byte, string) {
	var lines []sseLine
	var data [][]byte
	eventType := ""
	for offset := 0; offset < len(original); {
		end := offset
		for end < len(original) && original[end] != '\n' && original[end] != '\r' {
			end++
		}
		next := end
		if next < len(original) {
			next++
			if original[end] == '\r' && next < len(original) && original[next] == '\n' {
				next++
			}
		}
		line := sseLine{raw: original[offset:next], dataStart: -1}
		field := original[offset:end]
		name, value, found := bytes.Cut(field, []byte(":"))
		if !found {
			value = nil
		}
		start := len(name) + 1
		if len(value) > 0 && value[0] == ' ' {
			value = value[1:]
			start++
		}
		if string(name) == "event" {
			eventType = string(value)
		}
		if string(name) == "data" {
			if !found {
				start = len(field)
			}
			line.dataStart = start
			line.dataEnd = end - offset
			data = append(data, value)
		}
		lines = append(lines, line)
		offset = next
	}

	return lines, data, eventType
}
func (s *Session) streamSSE(ctx context.Context, source io.Reader, destination io.Writer) error {
	reader := bufio.NewReader(source)
	var event bytes.Buffer
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		line, err := readSSELine(reader)
		if err != nil && !errors.Is(err, io.EOF) {
			return err
		}
		event.Write(line)
		if len(bytes.TrimRight(line, "\r\n")) == 0 || err != nil {
			if flushErr := s.flushSSE(ctx, event.Bytes(), destination); flushErr != nil {
				return flushErr
			}
			event.Reset()
		}
		if errors.Is(err, io.EOF) {
			return nil
		}
	}
}
func (s *Session) flushSSE(ctx context.Context, event []byte, destination io.Writer) error {
	if len(event) == 0 {
		return nil
	}
	derived, err := s.ServerSSE(ctx, event)
	if err != nil {
		return err
	}
	n, err := destination.Write(derived)
	if err != nil {
		return err
	}
	if n != len(derived) {
		return io.ErrShortWrite
	}
	if flusher, ok := destination.(interface{ Flush() error }); ok {
		return flusher.Flush()
	}
	return nil
}
func readSSELine(reader *bufio.Reader) ([]byte, error) {
	var line []byte
	for {
		b, err := reader.ReadByte()
		if err != nil {
			return line, err
		}
		line = append(line, b)
		if b == '\n' {
			return line, nil
		}
		if b == '\r' {
			if next, err := reader.Peek(1); err == nil && next[0] == '\n' {
				_, _ = reader.ReadByte()
				line = append(line, '\n')
			}
			return line, nil
		}
	}
}
