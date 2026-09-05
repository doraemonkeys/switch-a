package wire

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

func (p *jsonTransformer) value(ctx context.Context, path string, depth int, kind string) error {
	p.path = path
	if err := ctx.Err(); err != nil {
		return err
	}
	if depth > maxJSONDepth {
		return fmt.Errorf("JSON nesting exceeds %d", maxJSONDepth)
	}
	if err := p.whitespace(); err != nil {
		return err
	}
	b, err := p.peek()
	if err != nil {
		return err
	}
	if b == '"' {
		return p.stringValue(ctx, path, kind)
	}
	if kind != "" && !strings.HasPrefix(kind, "feature:") && b != 'n' {
		return fmt.Errorf("identity field must be a string or null")
	}
	switch b {
	case '{':
		return p.objectValue(ctx, path, depth)
	case '[':
		return p.arrayValue(ctx, path, depth)
	default:
		return p.scalar()
	}
}
func (p *jsonTransformer) stringValue(ctx context.Context, path, kind string) error {
	if kind == "" {
		_, err := p.stringToken(false, false)
		return err
	}
	raw, err := p.stringToken(true, true)
	if err != nil {
		return err
	}
	var value string
	if err = json.Unmarshal(raw, &value); err != nil {
		return err
	}
	derived, err := p.session.transformValue(ctx, kind, value, p.restore, p.carrier, path)
	if err != nil {
		return err
	}
	encoded := raw
	if derived != value {
		encoded, err = json.Marshal(derived)
		if err != nil {
			return err
		}
	}
	_, err = p.writer.Write(encoded)
	return err
}
func (p *jsonTransformer) objectValue(ctx context.Context, path string, depth int) error {
	empty, err := p.openContainer('}')
	if err != nil || empty {
		return err
	}
	for {
		if err = p.objectMember(ctx, path, depth); err != nil {
			return err
		}
		done, separatorErr := p.separator('}')
		if separatorErr != nil || done {
			return separatorErr
		}
		if err = p.whitespace(); err != nil {
			return err
		}
	}
}
func (p *jsonTransformer) objectMember(ctx context.Context, path string, depth int) error {
	raw, err := p.stringToken(true, false)
	if err != nil {
		return err
	}
	var name string
	if raw != nil {
		if err = json.Unmarshal(raw, &name); err != nil {
			return err
		}
	}
	if err = p.whitespace(); err != nil {
		return err
	}
	separator, err := p.read()
	if err != nil {
		return err
	}
	if separator != ':' {
		return fmt.Errorf("expected colon after object key")
	}
	if err = p.emit(separator); err != nil {
		return err
	}
	childPath := path + "." + name
	if strings.ContainsAny(name, ".[]") {
		quoted, _ := json.Marshal(name)
		childPath = path + "[" + string(quoted) + "]"
	}
	return p.value(ctx, childPath, depth+1, p.kind(path, name))
}
func (p *jsonTransformer) arrayValue(ctx context.Context, path string, depth int) error {
	empty, err := p.openContainer(']')
	if err != nil || empty {
		return err
	}
	for index := 0; ; index++ {
		if err = p.value(ctx, fmt.Sprintf("%s[%d]", path, index), depth+1, ""); err != nil {
			return err
		}
		done, separatorErr := p.separator(']')
		if separatorErr != nil || done {
			return separatorErr
		}
	}
}
func (p *jsonTransformer) openContainer(closing byte) (bool, error) {
	opening, err := p.read()
	if err != nil {
		return false, err
	}
	if err = p.emit(opening); err != nil {
		return false, err
	}
	if err = p.whitespace(); err != nil {
		return false, err
	}
	next, err := p.peek()
	if err != nil {
		return false, err
	}
	if next != closing {
		return false, nil
	}
	_, _ = p.read()
	return true, p.emit(next)
}
func (p *jsonTransformer) separator(closing byte) (bool, error) {
	if err := p.whitespace(); err != nil {
		return false, err
	}
	separator, err := p.read()
	if err != nil {
		return false, err
	}
	if separator != closing && separator != ',' {
		return false, fmt.Errorf("expected JSON container separator")
	}
	if err = p.emit(separator); err != nil {
		return false, err
	}
	return separator == closing, nil
}
