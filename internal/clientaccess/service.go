package clientaccess

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/google/uuid"
)

const (
	GeneratedKeyPrefix      = "sk-switch-a-"
	GeneratedKeyRandomBytes = 32
)

type Storage interface {
	Snapshot(context.Context) (Snapshot, error)
	CreateKey(context.Context, Key) error
	RenameKey(context.Context, string, string, time.Time) (Key, error)
	DeleteKey(context.Context, string) error
	SetMode(context.Context, Mode) error
}

type ServiceConfig struct {
	Store  Storage
	Random io.Reader
	Now    func() time.Time
	NewID  func() string
}

type Service struct {
	store  Storage
	random io.Reader
	now    func() time.Time
	newID  func() string
}

func NewService(cfg ServiceConfig) *Service {
	if cfg.Store == nil {
		panic("clientaccess: Store is required")
	}
	if cfg.Random == nil {
		cfg.Random = rand.Reader
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	if cfg.NewID == nil {
		cfg.NewID = uuid.NewString
	}
	return &Service{store: cfg.Store, random: cfg.Random, now: cfg.Now, newID: cfg.NewID}
}

func (s *Service) Snapshot(ctx context.Context) (Snapshot, error) {
	snapshot, err := s.store.Snapshot(ctx)
	if err != nil {
		return Snapshot{}, err
	}
	if err := ValidateSnapshot(snapshot); err != nil {
		return Snapshot{}, err
	}
	if snapshot.Keys == nil {
		snapshot.Keys = []Key{}
	}
	return snapshot, nil
}

func (s *Service) Create(ctx context.Context, name, value string) (Key, error) {
	name = strings.TrimSpace(name)
	if err := validateName(name); err != nil {
		return Key{}, err
	}
	if err := validateKey(value); err != nil {
		return Key{}, err
	}
	now := s.now().UTC()
	key := Key{ID: s.newID(), Name: name, Key: value, CreatedAt: now, UpdatedAt: now}
	if err := validateRecord(key); err != nil {
		return Key{}, err
	}
	if err := s.store.CreateKey(ctx, key); err != nil {
		return Key{}, err
	}
	return key, nil
}

func (s *Service) Generate(ctx context.Context, name string) (Key, error) {
	if err := validateName(name); err != nil {
		return Key{}, err
	}
	entropy := make([]byte, GeneratedKeyRandomBytes)
	if _, err := io.ReadFull(s.random, entropy); err != nil {
		return Key{}, fmt.Errorf("generate client API key: %w", err)
	}
	return s.Create(ctx, name, GeneratedKeyPrefix+base64.RawURLEncoding.EncodeToString(entropy))
}

func (s *Service) Rename(ctx context.Context, id, name string) (Key, error) {
	name = strings.TrimSpace(name)
	if err := validateName(name); err != nil {
		return Key{}, err
	}
	return s.store.RenameKey(ctx, id, name, s.now().UTC())
}

func (s *Service) Delete(ctx context.Context, id string) error {
	return s.store.DeleteKey(ctx, id)
}

func (s *Service) SetMode(ctx context.Context, mode Mode) error {
	if err := validateMode(mode); err != nil {
		return err
	}
	return s.store.SetMode(ctx, mode)
}
