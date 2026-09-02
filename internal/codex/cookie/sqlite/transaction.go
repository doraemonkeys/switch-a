package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/doraemonkeys/switch-a/internal/codex/cookie"
)

const DefaultBusyTimeout = 5 * time.Second

type immediateTransaction struct {
	connection *sql.Conn
	finished   bool
}

func beginImmediate(ctx context.Context, database *sql.DB, busyTimeout time.Duration) (*immediateTransaction, error) {
	connection, err := database.Conn(ctx)
	if err != nil {
		return nil, classifyDatabaseError("acquire_connection", err)
	}
	milliseconds := max(busyTimeout.Milliseconds(), 1)
	if _, err := connection.ExecContext(ctx, fmt.Sprintf("PRAGMA busy_timeout = %d", milliseconds)); err != nil {
		_ = connection.Close()
		return nil, classifyDatabaseError("configure_busy_timeout", err)
	}
	if _, err := connection.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
		_ = connection.Close()
		return nil, classifyDatabaseError("begin_transaction", err)
	}
	return &immediateTransaction{connection: connection}, nil
}

func (tx *immediateTransaction) commit(ctx context.Context) error {
	if tx.finished {
		return corruptError("commit_transaction", errors.New("transaction is already finished"))
	}
	tx.finished = true
	if _, err := tx.connection.ExecContext(ctx, "COMMIT"); err != nil {
		_ = tx.connection.Close()
		return classifyDatabaseError("commit_transaction", err)
	}
	return tx.connection.Close()
}

func (tx *immediateTransaction) rollback() {
	if tx == nil || tx.finished {
		return
	}
	tx.finished = true
	_, _ = tx.connection.ExecContext(context.Background(), "ROLLBACK")
	_ = tx.connection.Close()
}

func withImmediateTransaction(
	ctx context.Context,
	database *sql.DB,
	busyTimeout time.Duration,
	work func(*sql.Conn) error,
) error {
	tx, err := beginImmediate(ctx, database, busyTimeout)
	if err != nil {
		return err
	}
	defer tx.rollback()
	if err := work(tx.connection); err != nil {
		return err
	}
	return tx.commit(ctx)
}

func classifyDatabaseError(operation string, cause error) error {
	if cause == nil {
		return nil
	}
	var persistence *providercookie.PersistenceError
	if errors.As(cause, &persistence) || errors.Is(cause, providercookie.ErrLimitExceeded) || errors.Is(cause, providercookie.ErrIdentifierClash) {
		return cause
	}
	message := strings.ToLower(cause.Error())
	for _, marker := range []string{
		"database disk image is malformed",
		"database malformed",
		"file is not a database",
		"malformed database schema",
		"database corruption",
		"datatype mismatch",
		"check constraint failed",
		"foreign key constraint failed",
	} {
		if strings.Contains(message, marker) {
			return corruptError(operation, cause)
		}
	}
	return storageError(operation, cause)
}
