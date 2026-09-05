package wire

import (
	"errors"
	"fmt"
	"io"
	"strings"
)

// Keys are captured only while they could identify a protocol field. Unknown
// strings and numeric business payloads pass through with constant working memory.
func (p *jsonTransformer) stringToken(capture, suppress bool) ([]byte, error) {
	var raw []byte
	appendByte := func(b byte) error {
		if capture {
			if !suppress && len(raw) >= maxCapturedKey {
				capture = false
				raw = nil
			} else {
				raw = append(raw, b)
			}
		}
		if !suppress {
			return p.emit(b)
		}
		return nil
	}
	b, err := p.read()
	if err != nil {
		return nil, err
	}
	if b != '"' {
		return nil, fmt.Errorf("expected JSON string")
	}
	if err = appendByte(b); err != nil {
		return nil, err
	}
	var escape stringEscape
	for {
		b, err = p.read()
		if err != nil {
			return nil, err
		}
		if err = appendByte(b); err != nil {
			return nil, err
		}
		done, escapeErr := escape.advance(b)
		if escapeErr != nil || done {
			return raw, escapeErr
		}
	}
}

type stringEscape struct {
	escaped       bool
	unicodeDigits int
}

func (e *stringEscape) advance(b byte) (bool, error) {
	if e.unicodeDigits > 0 {
		if !strings.ContainsRune("0123456789abcdefABCDEF", rune(b)) {
			return false, fmt.Errorf("invalid Unicode escape")
		}
		e.unicodeDigits--
		return false, nil
	}
	if e.escaped {
		e.escaped = false
		if b == 'u' {
			e.unicodeDigits = 4
			return false, nil
		}
		if !strings.ContainsRune("\"\\/bfnrt", rune(b)) {
			return false, fmt.Errorf("invalid JSON escape")
		}
		return false, nil
	}
	if b == '"' {
		return true, nil
	}
	if b < ' ' {
		return false, fmt.Errorf("unescaped JSON control character")
	}
	e.escaped = b == '\\'
	return false, nil
}
func (p *jsonTransformer) scalar() error {
	first, err := p.peek()
	if err != nil {
		return err
	}
	switch first {
	case 'n':
		return p.literal("null")
	case 't':
		return p.literal("true")
	case 'f':
		return p.literal("false")
	}
	state := numberStart
	for {
		b, err := p.peek()
		if err != nil && !errors.Is(err, io.EOF) {
			return err
		}
		if errors.Is(err, io.EOF) || strings.ContainsRune(" \t\r\n,}]", rune(b)) {
			break
		}
		state = state.advance(b)
		if state == numberInvalid {
			return fmt.Errorf("invalid JSON number")
		}
		_, _ = p.read()
		if err = p.emit(b); err != nil {
			return err
		}
	}
	if state != numberZero && state != numberInteger && state != numberFraction && state != numberExponentDigits {
		return fmt.Errorf("incomplete JSON scalar")
	}
	return nil
}
func (p *jsonTransformer) literal(value string) error {
	for i := range len(value) {
		b, err := p.read()
		if err != nil {
			return err
		}
		if b != value[i] {
			return fmt.Errorf("invalid JSON literal")
		}
		if err = p.emit(b); err != nil {
			return err
		}
	}
	return nil
}

type numberState uint8

const (
	numberInvalid numberState = iota
	numberStart
	numberSign
	numberZero
	numberInteger
	numberDot
	numberFraction
	numberExponent
	numberExponentSign
	numberExponentDigits
)

func (state numberState) advance(b byte) numberState {
	digit := b >= '0' && b <= '9'
	switch state {
	case numberStart, numberSign:
		switch {
		case state == numberStart && b == '-':
			return numberSign
		case b == '0':
			return numberZero
		case digit:
			return numberInteger
		}
	case numberZero, numberInteger:
		switch {
		case state == numberInteger && digit:
			return numberInteger
		case b == '.':
			return numberDot
		case b == 'e' || b == 'E':
			return numberExponent
		}
	case numberDot, numberFraction:
		if digit {
			return numberFraction
		}
		if state == numberFraction && (b == 'e' || b == 'E') {
			return numberExponent
		}
	case numberExponent:
		if b == '+' || b == '-' {
			return numberExponentSign
		}
		if digit {
			return numberExponentDigits
		}
	case numberExponentSign, numberExponentDigits:
		if digit {
			return numberExponentDigits
		}
	}
	return numberInvalid
}
