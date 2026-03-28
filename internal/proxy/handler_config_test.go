package proxy

import (
	"context"
	"errors"
	"testing"

	"switch-a/internal/defaults"
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

type websocketProbeErrorStore struct {
	*mockStore
}

func (s *websocketProbeErrorStore) GetConfig(_ context.Context, key string) (string, error) {
	if key == ConfigKeyWebSocketProbeClientModel {
		return "", errors.New("websocket_probe_client_model read failed")
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

func TestHandlerLoadConfig_WebSocketProbeClientModelValidValue(t *testing.T) {
	store := newMockStore()
	store.configs[ConfigKeyWebSocketProbeClientModel] = "false"

	handler := NewHandler(Config{
		Store:  store,
		Logger: zap.NewNop(),
	})

	cfg, err := handler.loadConfig(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.websocketProbeClientModel {
		t.Fatal("expected websocket probe client model to be false")
	}
}

func TestHandlerLoadConfig_WebSocketProbeClientModelInvalidFallsBack(t *testing.T) {
	store := newMockStore()
	store.configs[ConfigKeyWebSocketProbeClientModel] = "not-a-bool"

	handler := NewHandler(Config{
		Store:  store,
		Logger: zap.NewNop(),
	})

	cfg, err := handler.loadConfig(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.websocketProbeClientModel != defaults.WebSocketProbeClientModel {
		t.Fatalf(
			"expected fallback websocket probe client model %t, got %t",
			defaults.WebSocketProbeClientModel,
			cfg.websocketProbeClientModel,
		)
	}
}

func TestHandlerLoadConfig_WebSocketProbeClientModelReadErrorFallsBack(t *testing.T) {
	store := &websocketProbeErrorStore{mockStore: newMockStore()}
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
	if cfg.websocketProbeClientModel != defaults.WebSocketProbeClientModel {
		t.Fatalf(
			"expected fallback websocket probe client model %t, got %t",
			defaults.WebSocketProbeClientModel,
			cfg.websocketProbeClientModel,
		)
	}
}
