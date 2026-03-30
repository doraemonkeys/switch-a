package store

import "testing"

func TestMigrateWebSocketColumn_CopiesData(t *testing.T) {
	t.Parallel()

	db := setupWebSocketMigrationDB(t)
	if err := db.Exec(`INSERT INTO request_logs (is_web_socket, is_websocket, provider_id) VALUES (1, 0, 'p1')`).Error; err != nil {
		t.Fatalf("seed ws log: %v", err)
	}
	if err := db.Exec(`INSERT INTO request_logs (is_web_socket, is_websocket, provider_id) VALUES (0, 0, 'p2')`).Error; err != nil {
		t.Fatalf("seed regular log: %v", err)
	}

	if err := migrateWebSocketColumn(db); err != nil {
		t.Fatalf("migrateWebSocketColumn error: %v", err)
	}

	var wsCount int64
	if err := db.Raw(`SELECT COUNT(*) FROM request_logs WHERE is_websocket = 1`).Scan(&wsCount).Error; err != nil {
		t.Fatalf("count ws: %v", err)
	}
	if wsCount != 1 {
		t.Errorf("is_websocket=1 count = %d, want 1", wsCount)
	}

	var colCount int64
	if err := db.Raw(`SELECT COUNT(*) FROM pragma_table_info('request_logs') WHERE name = 'is_web_socket'`).Scan(&colCount).Error; err != nil {
		t.Fatalf("check column: %v", err)
	}
	if colCount != 0 {
		t.Error("is_web_socket column should have been dropped")
	}
}

func TestMigrateWebSocketColumn_NoLegacyColumn(t *testing.T) {
	t.Parallel()

	db := openMigrationSQLiteDB(t, "ws_no_legacy.db")
	if err := db.Exec(`CREATE TABLE IF NOT EXISTS request_logs (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		is_websocket BOOLEAN DEFAULT 0
	)`).Error; err != nil {
		t.Fatalf("create table: %v", err)
	}

	if err := migrateWebSocketColumn(db); err != nil {
		t.Fatalf("migrateWebSocketColumn error: %v", err)
	}
}
