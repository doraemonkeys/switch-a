package proxy

import (
	"context"
	"errors"
	"testing"

	"switch-a/internal/model"

	"go.uber.org/zap"
)

type stickyModeErrorStore struct {
	*mockStore
}

func (s *stickyModeErrorStore) GetConfig(_ context.Context, key string) (string, error) {
	if key == ConfigKeyStickyMode {
		return "", errors.New("sticky_mode read failed")
	}
	return s.mockStore.configs[key], nil
}

func TestHandlerLoadConfig_StickyModeValidValue(t *testing.T) {
	store := newMockStore()
	store.configs[ConfigKeyStickyMode] = string(model.StickyModeAPIType)
	registry := NewActiveRequestRegistry()

	handler := NewHandler(Config{
		Store:          store,
		Logger:         zap.NewNop(),
		ActiveRegistry: registry,
	})

	cfg, err := handler.loadConfig(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.stickyMode != model.StickyModeAPIType {
		t.Fatalf("expected sticky mode %q, got %q", model.StickyModeAPIType, cfg.stickyMode)
	}
}

func TestHandlerLoadConfig_StickyModeInvalidFallsBack(t *testing.T) {
	store := newMockStore()
	store.configs[ConfigKeyStickyMode] = "not-a-mode"

	handler := NewHandler(Config{
		Store:  store,
		Logger: zap.NewNop(),
	})

	cfg, err := handler.loadConfig(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.stickyMode != DefaultStickyMode {
		t.Fatalf("expected fallback sticky mode %q, got %q", DefaultStickyMode, cfg.stickyMode)
	}
}

func TestHandlerLoadConfig_StickyModeReadErrorFallsBack(t *testing.T) {
	store := &stickyModeErrorStore{mockStore: newMockStore()}
	registry := NewActiveRequestRegistry()

	handler := NewHandler(Config{
		Store:          store,
		Logger:         zap.NewNop(),
		ActiveRegistry: registry,
	})

	cfg, err := handler.loadConfig(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.stickyMode != DefaultStickyMode {
		t.Fatalf("expected fallback sticky mode %q, got %q", DefaultStickyMode, cfg.stickyMode)
	}
}
