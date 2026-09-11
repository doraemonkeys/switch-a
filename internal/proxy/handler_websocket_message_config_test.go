package proxy

import (
	"context"
	"errors"
	"strconv"
	"testing"

	"github.com/doraemonkeys/switch-a/internal/defaults"
	"go.uber.org/zap"
)

type websocketMessageConfigErrorStore struct{ *mockStore }

func (s websocketMessageConfigErrorStore) GetConfig(ctx context.Context, key string) (string, error) {
	if key == defaults.ConfigKeyWebSocketMaxMessageSizeMiB {
		return "", errors.New("config read failed")
	}
	return s.mockStore.GetConfig(ctx, key)
}

func TestLoadConfigWebSocketMaxMessageBytes(t *testing.T) {
	for _, tt := range []struct {
		value   string
		wantMiB int64
	}{
		{"", 128}, {"128", 128}, {"1", 1}, {"256", 256},
		{"0", 128}, {"-1", 128}, {"1.5", 128}, {"bad", 128},
		{strconv.FormatInt(defaults.MaxWebSocketMessageSizeMiB, 10), defaults.MaxWebSocketMessageSizeMiB},
		{strconv.FormatInt(defaults.MaxWebSocketMessageSizeMiB+1, 10), 128},
	} {
		t.Run(tt.value, func(t *testing.T) {
			st := newMockStore()
			st.configs[defaults.ConfigKeyWebSocketMaxMessageSizeMiB] = tt.value
			h := &Handler{store: st, logger: zap.NewNop()}
			cfg, err := h.loadConfig(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if want := tt.wantMiB * defaults.BytesPerMiB; cfg.websocketMaxMessageBytes != want {
				t.Fatalf("bytes = %d, want %d", cfg.websocketMaxMessageBytes, want)
			}
		})
	}
	st := websocketMessageConfigErrorStore{newMockStore()}
	h := &Handler{store: st, logger: zap.NewNop()}
	cfg, err := h.loadConfig(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if cfg.websocketMaxMessageBytes != defaults.WebSocketMaxMessageSizeMiB*defaults.BytesPerMiB {
		t.Fatalf("read failure fallback = %d", cfg.websocketMaxMessageBytes)
	}
}
