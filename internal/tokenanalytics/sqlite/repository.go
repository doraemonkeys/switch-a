// Package sqlite implements token analytics against an isolated read-only
// SQLite snapshot without widening the request-path store interfaces.
package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/doraemonkeys/switch-a/internal/tokenanalytics"

	_ "github.com/glebarez/go-sqlite"
)

const (
	sqliteDriverName      = "sqlite"
	readBusyTimeout       = 2 * time.Second
	repositoryOpenTimeout = 5 * time.Second
)

var (
	errMemoryDatabase        = errors.New("token analytics requires a shareable file-backed database")
	errInvalidWindow         = errors.New("invalid token analytics window")
	errInvalidRankLimit      = errors.New("invalid token analytics rank limit")
	errEmptySemanticContract = errors.New("token analytics semantic contract is empty")
	errSnapshotClosed        = errors.New("token analytics snapshot is closed")
)

// Repository owns a pool separate from the single-connection writer. One open
// connection serializes analytics snapshots while WAL keeps writes independent.
type Repository struct {
	db *sql.DB
}

var _ tokenanalytics.SnapshotReader = (*Repository)(nil)

// Open validates a file-backed database immediately so startup cannot defer a
// bad path or read-only permission failure until the first admin request.
func Open(databasePath string) (*Repository, error) {
	dsn, err := readOnlyDSN(databasePath)
	if err != nil {
		return nil, err
	}

	db, err := sql.Open(sqliteDriverName, dsn)
	if err != nil {
		return nil, fmt.Errorf("open token analytics database: %w", err)
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	db.SetConnMaxIdleTime(5 * time.Minute)

	ctx, cancel := context.WithTimeout(context.Background(), repositoryOpenTimeout)
	defer cancel()
	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("open token analytics database: %w", err)
	}
	return &Repository{db: db}, nil
}

func readOnlyDSN(databasePath string) (string, error) {
	trimmed := strings.TrimSpace(databasePath)
	lower := strings.ToLower(trimmed)
	if trimmed == "" || lower == ":memory:" || strings.HasPrefix(lower, "file::memory:") || strings.Contains(lower, "mode=memory") {
		return "", errMemoryDatabase
	}

	absolutePath, err := filepath.Abs(trimmed)
	if err != nil {
		return "", fmt.Errorf("resolve token analytics database path: %w", err)
	}
	uriPath := filepath.ToSlash(absolutePath)
	if filepath.VolumeName(absolutePath) != "" {
		uriPath = "/" + uriPath
	}
	uri := url.URL{
		Scheme:   "file",
		Path:     uriPath,
		RawQuery: "mode=ro",
	}
	return uri.String(), nil
}

// OpenSnapshot reserves the sole analytics connection and begins one read
// transaction. The summary query is intentionally the first SELECT performed by
// the consumer because that statement pins the WAL snapshot for later reads.
func (r *Repository) OpenSnapshot(ctx context.Context) (tokenanalytics.Snapshot, error) {
	conn, err := r.db.Conn(ctx)
	if err != nil {
		return nil, fmt.Errorf("acquire token analytics connection: %w", err)
	}

	initialized := false
	defer func() {
		if !initialized {
			_ = conn.Close()
		}
	}()

	if _, err := conn.ExecContext(ctx, "PRAGMA query_only=ON"); err != nil {
		return nil, fmt.Errorf("enable token analytics query-only mode: %w", err)
	}
	busyTimeoutMilliseconds := int64(readBusyTimeout / time.Millisecond)
	if _, err := conn.ExecContext(ctx, fmt.Sprintf("PRAGMA busy_timeout=%d", busyTimeoutMilliseconds)); err != nil {
		return nil, fmt.Errorf("configure token analytics busy timeout: %w", err)
	}

	tx, err := conn.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, fmt.Errorf("begin token analytics snapshot: %w", err)
	}

	initialized = true
	return &Snapshot{tx: tx, conn: conn}, nil
}

// Close releases the independent analytics pool. Process wiring closes this
// repository before the writer so no new read snapshot can outlive shutdown.
func (r *Repository) Close() error {
	if r == nil || r.db == nil {
		return nil
	}
	return r.db.Close()
}

// Snapshot is deliberately stateful only for transaction lifecycle; request
// contexts remain method arguments and are never retained.
type Snapshot struct {
	mu     sync.Mutex
	tx     *sql.Tx
	conn   *sql.Conn
	closed bool
}

var _ tokenanalytics.Snapshot = (*Snapshot)(nil)

// Close rolls back even after cancellation or a failed read, then returns the
// dedicated connection to the analytics pool. Repeated closes are harmless.
func (s *Snapshot) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil
	}
	s.closed = true
	return combineCloseErrors(s.tx.Rollback(), s.conn.Close())
}
