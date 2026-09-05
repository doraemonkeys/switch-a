// Package clientidentity owns the persistent downstream identity shared by
// disguise mappings, continuity ownership, and routing affinity.
package clientidentity

import (
	"context"
	"errors"
	"fmt"
	"time"

	codexidentity "github.com/doraemonkeys/switch-a/internal/codex/identity"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

var (
	ErrConflict = errors.New("client key already belongs to another identity")
	ErrNotFound = errors.New("client identity not found")
)

type ScopeDigester interface {
	ClientScopeCandidates([]byte) ([]codexidentity.ClientScope, error)
}
type Resolution struct {
	ID      string
	Primary codexidentity.ClientScope
	Aliases []codexidentity.ClientScope
}
type Client struct {
	ID             string    `json:"id" gorm:"primaryKey"`
	CreatedAt      time.Time `json:"created_at"`
	PrimaryVersion string    `json:"primary_version"`
	PrimaryDigest  []byte    `json:"primary_digest"`
}

func (Client) TableName() string { return "codex_client_identities" }

type KeyAlias struct {
	Version  string `json:"version" gorm:"primaryKey"`
	Digest   []byte `json:"digest" gorm:"primaryKey"`
	ClientID string `json:"client_id" gorm:"index;not null"`
}

func (KeyAlias) TableName() string { return "codex_client_key_aliases" }

type Trace struct {
	ClientID   string
	Decision   string
	AliasCount int
	Err        error
}
type Config struct {
	DB       *gorm.DB
	Digester ScopeDigester
	Now      func() time.Time
	NewID    func() string
	Observe  func(Trace)
}
type Resolver struct {
	db       *gorm.DB
	digester ScopeDigester
	now      func() time.Time
	newID    func() string
	observe  func(Trace)
}

func Migrate(db *gorm.DB) error { return db.AutoMigrate(&Client{}, &KeyAlias{}) }
func New(db *gorm.DB, digester ScopeDigester) (*Resolver, error) {
	return NewWithConfig(Config{DB: db, Digester: digester})
}
func NewWithConfig(config Config) (*Resolver, error) {
	if config.DB == nil || config.Digester == nil {
		return nil, fmt.Errorf("client identity requires database and digester")
	}
	if config.Now == nil {
		config.Now = time.Now
	}
	if config.NewID == nil {
		config.NewID = uuid.NewString
	}
	return &Resolver{db: config.DB, digester: config.Digester, now: config.Now, newID: config.NewID, observe: config.Observe}, nil
}
func (r *Resolver) Resolve(ctx context.Context, rawKey []byte) (Resolution, error) {
	return r.resolve(ctx, rawKey, "")
}
func (r *Resolver) BindKey(ctx context.Context, rawKey []byte, clientID string) (Resolution, error) {
	if clientID == "" {
		return Resolution{}, ErrNotFound
	}
	return r.resolve(ctx, rawKey, clientID)
}
func (r *Resolver) ListClients(ctx context.Context) ([]Client, error) {
	clients := []Client{}
	err := r.db.WithContext(ctx).Order("created_at, id").Find(&clients).Error
	return clients, err
}
func (r *Resolver) resolve(ctx context.Context, rawKey []byte, target string) (Resolution, error) {
	scopes, err := r.digester.ClientScopeCandidates(rawKey)
	if err != nil {
		return Resolution{}, err
	}
	if len(scopes) == 0 {
		return Resolution{}, fmt.Errorf("client scope candidates are empty")
	}
	var result Resolution
	err = r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// Reserve the writer before reading aliases so independent connections cannot
		// both observe absence and allocate different identities for the same key.
		if err := tx.Exec("UPDATE codex_client_identities SET id = id WHERE 0").Error; err != nil {
			return err
		}
		clientID, err := findClientID(tx, scopes, target)
		if err != nil {
			return err
		}
		client, err := r.loadOrCreateClient(tx, scopes[0], clientID)
		if err != nil {
			return err
		}
		for _, scope := range scopes {
			if err := attachScope(tx, scope, client.ID); err != nil {
				return err
			}
		}
		var aliases []KeyAlias
		if err := tx.Where("client_id = ?", client.ID).Order("version, digest").Find(&aliases).Error; err != nil {
			return err
		}
		result, err = resolution(client, aliases)
		return err
	})
	if r.observe != nil {
		decision := "resolved"
		if target != "" {
			decision = "key_bound"
		}
		if err != nil {
			decision = "failed"
		}
		r.observe(Trace{ClientID: result.ID, Decision: decision, AliasCount: len(result.Aliases), Err: err})
	}
	return result, err
}
func findClientID(tx *gorm.DB, scopes []codexidentity.ClientScope, target string) (string, error) {
	for _, scope := range scopes {
		digest := scope.Digest()
		var alias KeyAlias
		err := tx.Where("version = ? AND digest = ?", scope.KeyVersion(), digest[:]).Take(&alias).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			continue
		}
		if err != nil {
			return "", err
		}
		if target != "" && target != alias.ClientID {
			return "", ErrConflict
		}
		target = alias.ClientID
	}
	return target, nil
}
func (r *Resolver) loadOrCreateClient(tx *gorm.DB, scope codexidentity.ClientScope, id string) (Client, error) {
	if id != "" {
		var client Client
		err := tx.First(&client, "id = ?", id).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			err = ErrNotFound
		}
		return client, err
	}
	digest := scope.Digest()
	client := Client{ID: r.newID(), CreatedAt: r.now().UTC(), PrimaryVersion: scope.KeyVersion(), PrimaryDigest: append([]byte(nil), digest[:]...)}
	return client, tx.Create(&client).Error
}
func attachScope(tx *gorm.DB, scope codexidentity.ClientScope, clientID string) error {
	digest := scope.Digest()
	alias := KeyAlias{Version: scope.KeyVersion(), Digest: append([]byte(nil), digest[:]...), ClientID: clientID}
	var existing KeyAlias
	err := tx.Where("version = ? AND digest = ?", alias.Version, alias.Digest).Take(&existing).Error
	switch {
	case errors.Is(err, gorm.ErrRecordNotFound):
		return tx.Create(&alias).Error
	case err != nil:
		return err
	case existing.ClientID != clientID:
		return ErrConflict
	default:
		return nil
	}
}
func resolution(client Client, aliases []KeyAlias) (Resolution, error) {
	primary, err := decodeScope(client.PrimaryVersion, client.PrimaryDigest)
	if err != nil {
		return Resolution{}, err
	}
	result := Resolution{ID: client.ID, Primary: primary, Aliases: []codexidentity.ClientScope{primary}}
	for _, alias := range aliases {
		scope, err := decodeScope(alias.Version, alias.Digest)
		if err != nil {
			return Resolution{}, err
		}
		if !scope.Equal(primary) {
			result.Aliases = append(result.Aliases, scope)
		}
	}
	return result, nil
}
func decodeScope(version string, digest []byte) (codexidentity.ClientScope, error) {
	if len(digest) != codexidentity.DigestSize {
		return codexidentity.ClientScope{}, fmt.Errorf("invalid persisted client scope digest")
	}
	var sum [codexidentity.DigestSize]byte
	copy(sum[:], digest)
	return codexidentity.ClientScopeFromDigest(version, sum)
}

// SetObserver is a composition-time hook and must run before serving requests.
func (r *Resolver) SetObserver(observe func(Trace)) { r.observe = observe }
