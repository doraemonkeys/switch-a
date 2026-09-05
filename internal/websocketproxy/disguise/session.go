package wsdisguise

import (
	"context"
	"fmt"
	"net/http"
	"sync"

	"github.com/doraemonkeys/switch-a/internal/codex/clientdisguise"
	"github.com/doraemonkeys/switch-a/internal/codex/clientdisguise/wire"
	"github.com/doraemonkeys/switch-a/internal/codex/disguiseruntime"
	"github.com/doraemonkeys/switch-a/internal/model"
	"github.com/doraemonkeys/switch-a/internal/upstreamtransport"
)

type Repository interface {
	disguiseruntime.Repository
	wire.Mapper
}

// Session owns the downstream connection boundary. Upstream redials may select
// another target, but cannot replace any target snapshot already observed here.
type Session struct {
	operation   *disguiseruntime.Operation
	mapper      wire.Mapper
	clientID    string
	operationID string
	pool        *upstreamtransport.Pool
	mu          sync.RWMutex
	sessions    map[string]*wire.Session
	current     *wire.Session
	target      clientdisguise.TargetSnapshot
}

func New(ctx context.Context, repository Repository, providers []model.Provider, headers http.Header, clientID, operationID string, pool *upstreamtransport.Pool) (*Session, error) {
	operation, err := disguiseruntime.New(ctx, repository, providers, headers, operationID)
	if err != nil {
		return nil, err
	}
	if pool == nil {
		pool = upstreamtransport.NewPool()
	}
	return &Session{operation: operation, mapper: repository, clientID: clientID, operationID: operationID, pool: pool, sessions: make(map[string]*wire.Session)}, nil
}
func (s *Session) Operation() *disguiseruntime.Operation {
	if s == nil {
		return nil
	}
	return s.operation
}

func (s *Session) Select(provider *model.Provider) error {
	if s == nil {
		return nil
	}
	if provider == nil {
		return fmt.Errorf("WebSocket disguise provider is required")
	}
	credential, exists := provider.CredentialSessionForAPIType("codex")
	if !exists {
		return fmt.Errorf("WebSocket provider %s has no Codex credential", provider.ID)
	}
	target, ok := s.operation.Target(provider.ID, credential.SessionID)
	if !ok {
		return fmt.Errorf("WebSocket disguise target was not committed for provider %s", provider.ID)
	}
	key := provider.ID + "\x00" + credential.SessionID
	s.mu.Lock()
	defer s.mu.Unlock()
	session := s.sessions[key]
	if session == nil {
		session = wire.NewSession(s.mapper, target, s.clientID, s.operationID)
		s.sessions[key] = session
	}
	s.current = session
	s.target = target
	return nil
}
func (s *Session) Current() *wire.Session {
	if s == nil {
		return nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.current
}
func (s *Session) HTTPClient() (*http.Client, error) {
	if s == nil {
		return nil, nil
	}
	s.mu.RLock()
	custom := s.target.Policy.Enabled && s.target.Transport != nil
	current := s.current
	s.mu.RUnlock()
	if !custom || current == nil {
		return nil, nil
	}
	config, err := current.WebSocketTransportConfig()
	if err != nil {
		return nil, err
	}
	transport, err := s.pool.Get(upstreamtransport.Config{}, config)
	if err != nil {
		return nil, err
	}
	return transport.WebSocketClient(), nil
}
