package sqlite

import (
	"context"
	"database/sql"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"weak"

	"github.com/doraemonkeys/switch-a/internal/codex/cookie"
)

// Live leases are process state. File-backed repositories share them even
// across connection pools; weak references let closed databases be collected.
var databaseActivities = struct {
	sync.Mutex
	entries map[any]weak.Pointer[jarActivity]
}{entries: make(map[any]weak.Pointer[jarActivity])}

type jarActivity struct {
	mu         sync.Mutex
	references map[providercookie.JarID]int
}

type activityCleanup struct {
	key   any
	value weak.Pointer[jarActivity]
}

func activityFor(ctx context.Context, database *sql.DB) (*jarActivity, error) {
	var sequence int
	var name, file string
	if err := database.QueryRowContext(ctx, "PRAGMA database_list").Scan(&sequence, &name, &file); err != nil {
		return nil, classifyDatabaseError("open_cookie_activity", err)
	}
	var key any = weak.Make(database)
	if file != "" {
		file = filepath.Clean(file)
		if runtime.GOOS == "windows" {
			file = strings.ToLower(file)
		}
		key = file
	}
	databaseActivities.Lock()
	defer databaseActivities.Unlock()
	if value := databaseActivities.entries[key].Value(); value != nil {
		return value, nil
	}
	value := &jarActivity{references: make(map[providercookie.JarID]int)}
	pointer := weak.Make(value)
	databaseActivities.entries[key] = pointer
	runtime.AddCleanup(value, func(entry activityCleanup) {
		databaseActivities.Lock()
		defer databaseActivities.Unlock()
		if databaseActivities.entries[entry.key] == entry.value {
			delete(databaseActivities.entries, entry.key)
		}
	}, activityCleanup{key: key, value: pointer})
	return value, nil
}

// Acquisition occurs before the writer transaction commits. Reclamation also
// runs under SQLite's writer lock, so lookup cannot race deletion. The mutex
// only guards live references and never spans I/O or waits for a database lock.
func (a *jarActivity) acquire(jar providercookie.JarID) func() {
	a.mu.Lock()
	a.references[jar]++
	a.mu.Unlock()
	return sync.OnceFunc(func() {
		a.mu.Lock()
		defer a.mu.Unlock()
		a.references[jar]--
		if a.references[jar] == 0 {
			delete(a.references, jar)
		}
	})
}

func (a *jarActivity) active(raw []byte) bool {
	jar, err := providercookie.JarIDFromBytes(raw)
	if err != nil {
		return false
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.references[jar] > 0
}
