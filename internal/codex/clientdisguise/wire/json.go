package wire

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
)

const maxJSONDepth = 10000
const maxCapturedKey = 1024

func (s *Session) RequestJSON(ctx context.Context, original []byte) ([]byte, error) {
	return s.json(ctx, original, false, "http_request", "$", false)
}
func (s *Session) ResponseJSON(ctx context.Context, original []byte) ([]byte, error) {
	return s.json(ctx, original, true, "http_response", "$", false)
}
func (s *Session) ClientFrame(ctx context.Context, original []byte) ([]byte, error) {
	return s.frame(ctx, original, false)
}
func (s *Session) ServerFrame(ctx context.Context, original []byte) ([]byte, error) {
	return s.frame(ctx, original, true)
}
func (s *Session) frame(ctx context.Context, original []byte, restore bool) ([]byte, error) {
	if !s.target.Policy.Enabled {
		return original, nil
	}
	if !recognizedFrame(frameType(original), restore) {
		return original, nil
	}
	return s.json(ctx, original, restore, "websocket", "$", false)
}

// Classification must not impose a JSON contract on opaque protocol events.
// Once a known type is found, the transformer validates the entire document.
func frameType(original []byte) string {
	decoder := json.NewDecoder(bytes.NewReader(original))
	token, err := decoder.Token()
	if err != nil || token != json.Delim('{') {
		return ""
	}
	for decoder.More() {
		token, err = decoder.Token()
		if err != nil {
			return ""
		}
		name, ok := token.(string)
		if !ok {
			return ""
		}
		if name == "type" {
			var value string
			if decoder.Decode(&value) != nil {
				return ""
			}
			return value
		}
		var value json.RawMessage
		if decoder.Decode(&value) != nil {
			return ""
		}
	}
	return ""
}
func recognizedFrame(kind string, restore bool) bool {
	if !restore {
		return kind == "response.create"
	}
	switch kind {
	case "response.created", "response.in_progress", "response.completed", "response.done", "response.failed", "response.incomplete":
		return true
	}
	return false
}
func (s *Session) json(ctx context.Context, original []byte, restore bool, carrier, path string, nested bool) ([]byte, error) {
	if !s.target.Policy.Enabled || len(bytes.TrimSpace(original)) == 0 {
		return original, nil
	}
	var output bytes.Buffer
	err := s.streamJSON(ctx, bytes.NewReader(original), &output, restore, carrier, path, nested)
	if err != nil {
		return nil, err
	}
	return output.Bytes(), nil
}
func (s *Session) streamJSON(ctx context.Context, source io.Reader, destination io.Writer, restore bool, carrier, path string, nested bool) error {
	input := &observedReader{Reader: source}
	output := &observedWriter{Writer: destination}
	parser := jsonTransformer{reader: bufio.NewReader(input), writer: bufio.NewWriter(output), session: s, restore: restore, carrier: carrier, nested: nested, root: path}
	err := parser.document(ctx, path)
	if err == nil {
		err = parser.writer.Flush()
	}
	if err != nil {
		if errors.Is(err, input.failure) || errors.Is(err, output.failure) {
			return err
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		var failure *Failure
		if errors.As(err, &failure) {
			return err
		}
		return s.failure("parse", carrier, parser.path, parser.originalSnippet(), "", err)
	}
	return nil
}

type observedReader struct {
	io.Reader
	failure error
}

func (r *observedReader) Read(p []byte) (int, error) {
	n, err := r.Reader.Read(p)
	if err != nil && !errors.Is(err, io.EOF) {
		r.failure = err
	}
	return n, err
}

type observedWriter struct {
	io.Writer
	failure error
}

func (w *observedWriter) Write(p []byte) (int, error) {
	n, err := w.Writer.Write(p)
	if err == nil && n != len(p) {
		err = io.ErrShortWrite
	}
	if err != nil {
		w.failure = err
	}
	return n, err
}

type jsonTransformer struct {
	reader     *bufio.Reader
	writer     *bufio.Writer
	session    *Session
	restore    bool
	carrier    string
	nested     bool
	root       string
	path       string
	recent     []byte
	recentNext int
}

func (p *jsonTransformer) read() (byte, error) {
	b, err := p.reader.ReadByte()
	if err == nil {
		if len(p.recent) == snippetLimit {
			p.recent[p.recentNext] = b
			p.recentNext = (p.recentNext + 1) % snippetLimit
		} else {
			p.recent = append(p.recent, b)
		}
	}
	return b, err
}
func (p *jsonTransformer) originalSnippet() string {
	return string(p.recent[p.recentNext:]) + string(p.recent[:p.recentNext])
}
func (p *jsonTransformer) peek() (byte, error) {
	b, err := p.reader.Peek(1)
	if err != nil {
		return 0, err
	}
	return b[0], nil
}
func (p *jsonTransformer) emit(b byte) error { return p.writer.WriteByte(b) }
func (p *jsonTransformer) whitespace() error {
	for {
		b, err := p.peek()
		if err != nil {
			return err
		}
		if b != ' ' && b != '\t' && b != '\r' && b != '\n' {
			return nil
		}
		_, _ = p.read()
		if err = p.emit(b); err != nil {
			return err
		}
	}
}
func (p *jsonTransformer) document(ctx context.Context, path string) error {
	p.path = path
	if err := p.whitespace(); err != nil {
		if errors.Is(err, io.EOF) {
			return nil
		}
		return err
	}
	if err := p.value(ctx, path, 0, ""); err != nil {
		return err
	}
	err := p.whitespace()
	if errors.Is(err, io.EOF) {
		return nil
	}
	if err != nil {
		return err
	}
	return fmt.Errorf("unexpected data after JSON value")
}

// Only protocol-owned containers can carry identities. Business metadata and
// prompt/tool objects may contain identical key names without identity semantics.
func (p *jsonTransformer) kind(parent, name string) string {
	if name != strings.ToLower(name) {
		return ""
	}
	container := parent == "$" || parent == "$.client_metadata"
	if p.restore && (parent == "$.response" || parent == "$.response.client_metadata") {
		container = true
	}
	if p.nested {
		container = parent == p.root
	}
	if !container {
		return ""
	}
	if kind := fieldKind(name); kind != "" {
		return kind
	}
	if !p.restore && (p.nested || parent == "$.client_metadata") {
		if kind := protocolFeatureKind(name); kind != "" {
			return kind
		}
	}
	if p.nested {
		switch name {
		case "cwd", "workspace_path", "project_path":
			return "telemetry"
		}
	}
	return ""
}
