package logger

import (
	"testing"
)

func TestNew_Development(t *testing.T) {
	logger := New(true)
	if logger == nil {
		t.Fatal("expected non-nil logger")
	}
	logger.Info("test message")
}

func TestNew_Production(t *testing.T) {
	logger := New(false)
	if logger == nil {
		t.Fatal("expected non-nil logger")
	}
	logger.Info("test message")
}
