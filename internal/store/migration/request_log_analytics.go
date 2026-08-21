package migration

import (
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/doraemonkeys/switch-a/internal/instant"

	"gorm.io/gorm"
)

const (
	requestLogsTableName                     = "request_logs"
	requestLogCreatedAtUnixNanoColumn        = "created_at_unix_nano"
	requestLogCreatedAtUnixNanoIndex         = "idx_request_logs_created_at_unix_nano"
	requestLogProviderCreatedAtUnixNanoIndex = "idx_request_logs_provider_created_at_unix_nano"
	requestLogModelCreatedAtUnixNanoIndex    = "idx_request_logs_model_created_at_unix_nano"
	requestLogAPITypeCreatedAtUnixNanoIndex  = "idx_request_logs_api_type_created_at_unix_nano"
	legacyRequestLogProviderCreatedAtIndex   = "idx_request_logs_provider_created_at"
	legacyRequestLogModelCreatedAtIndex      = "idx_request_logs_model_created_at"
	legacyRequestLogAPITypeCreatedAtIndex    = "idx_request_logs_api_type_created_at"
	requestLogTimestampBackfillSize          = 500
	requestLogInvalidIDSampleSize            = 16
)

var legacyRequestLogAnalyticsIndexes = []string{
	legacyRequestLogProviderCreatedAtIndex,
	legacyRequestLogModelCreatedAtIndex,
	legacyRequestLogAPITypeCreatedAtIndex,
}

var requestLogAnalyticsIndexes = []struct {
	name       string
	columns    []string
	legacyName string
}{
	{name: requestLogCreatedAtUnixNanoIndex, columns: []string{requestLogCreatedAtUnixNanoColumn}},
	{name: requestLogProviderCreatedAtUnixNanoIndex, columns: []string{"provider_id", requestLogCreatedAtUnixNanoColumn}, legacyName: legacyRequestLogProviderCreatedAtIndex},
	{name: requestLogModelCreatedAtUnixNanoIndex, columns: []string{"model", requestLogCreatedAtUnixNanoColumn}, legacyName: legacyRequestLogModelCreatedAtIndex},
	{name: requestLogAPITypeCreatedAtUnixNanoIndex, columns: []string{"api_type", requestLogCreatedAtUnixNanoColumn}, legacyName: legacyRequestLogAPITypeCreatedAtIndex},
}

type requestLogTimestampBackfill struct {
	ID            uint
	StorageClass  string
	CreatedAtText string
}

var requestLogTimestampLayouts = []string{
	"2006-01-02 15:04:05.999999999-07:00",
	"2006-01-02T15:04:05.999999999-07:00",
	"2006-01-02 15:04:05.999999999",
	"2006-01-02T15:04:05.999999999",
	"2006-01-02 15:04:05",
	"2006-01-02T15:04:05",
	"2006-01-02 15:04",
	"2006-01-02T15:04",
	"2006-01-02",
	"2006-01-02 15:04:05.999999999 -0700 MST",
}

// RequestLogTimestampMigrationReport exposes bounded startup diagnostics while
// keeping malformed source values private and out of logs.
type RequestLogTimestampMigrationReport struct {
	BackfilledCount int64
	InvalidCount    int64
	InvalidIDs      []uint
}

type requestLogTimestampBatchReport struct {
	backfilledCount int64
	invalidIDs      []uint
}

// MigrateRequestLogCreatedAtInstants gives historical offset-bearing text the
// same exact instant key as new writes. Bounded transactions keep a large log
// history from becoming one oversized WAL transaction, and NULL legacy dates
// retain their prior exclusion semantics.
func MigrateRequestLogCreatedAtInstants(db *gorm.DB) (RequestLogTimestampMigrationReport, error) {
	var report RequestLogTimestampMigrationReport
	var lastID uint
	for {
		batch, err := readRequestLogTimestampBackfillBatch(db, lastID)
		if err != nil {
			return report, err
		}
		if len(batch) == 0 {
			return report, nil
		}

		batchReport, err := migrateRequestLogTimestampBatch(db, batch)
		if err != nil {
			return report, err
		}
		report.BackfilledCount += batchReport.backfilledCount
		report.InvalidCount += int64(len(batchReport.invalidIDs))
		for _, id := range batchReport.invalidIDs {
			if len(report.InvalidIDs) == requestLogInvalidIDSampleSize {
				break
			}
			report.InvalidIDs = append(report.InvalidIDs, id)
		}
		lastID = batch[len(batch)-1].ID
	}
}

func migrateRequestLogTimestampBatch(db *gorm.DB, batch []requestLogTimestampBackfill) (requestLogTimestampBatchReport, error) {
	var report requestLogTimestampBatchReport
	err := db.Transaction(func(tx *gorm.DB) error {
		for _, row := range batch {
			backfilled, invalid, err := migrateRequestLogTimestampRow(tx, row)
			if err != nil {
				return err
			}
			if backfilled {
				report.backfilledCount++
			}
			if invalid {
				report.invalidIDs = append(report.invalidIDs, row.ID)
			}
		}
		return nil
	})
	if err != nil {
		return requestLogTimestampBatchReport{}, err
	}
	return report, nil
}

func migrateRequestLogTimestampRow(tx *gorm.DB, row requestLogTimestampBackfill) (backfilled, invalid bool, err error) {
	unixNano, valid := requestLogTimestampUnixNano(row.StorageClass, row.CreatedAtText)
	if !valid {
		result := tx.Table(requestLogsTableName).
			Where("id = ? AND "+requestLogCreatedAtUnixNanoColumn+" IS NULL", row.ID).
			UpdateColumn("created_at", nil)
		if result.Error != nil {
			return false, false, fmt.Errorf("quarantine invalid request-log timestamp %d: %w", row.ID, result.Error)
		}
		return false, result.RowsAffected > 0, nil
	}

	result := tx.Table(requestLogsTableName).
		Where("id = ? AND "+requestLogCreatedAtUnixNanoColumn+" IS NULL", row.ID).
		UpdateColumn(requestLogCreatedAtUnixNanoColumn, unixNano)
	if result.Error != nil {
		return false, false, fmt.Errorf("backfill request-log timestamp %d: %w", row.ID, result.Error)
	}
	return result.RowsAffected > 0, false, nil
}

func readRequestLogTimestampBackfillBatch(db *gorm.DB, afterID uint) ([]requestLogTimestampBackfill, error) {
	rows, err := db.Raw(
		"SELECT id, typeof(created_at), CASE WHEN typeof(created_at) = 'text' THEN CAST(created_at AS TEXT) ELSE '' END FROM "+requestLogsTableName+
			" WHERE id > ? AND created_at IS NOT NULL AND "+requestLogCreatedAtUnixNanoColumn+" IS NULL"+
			" ORDER BY id ASC LIMIT ?",
		afterID,
		requestLogTimestampBackfillSize,
	).Rows()
	if err != nil {
		return nil, fmt.Errorf("read request-log timestamp backfill: %w", err)
	}
	defer rows.Close()

	batch := make([]requestLogTimestampBackfill, 0, requestLogTimestampBackfillSize)
	for rows.Next() {
		var row requestLogTimestampBackfill
		if err := rows.Scan(&row.ID, &row.StorageClass, &row.CreatedAtText); err != nil {
			return nil, fmt.Errorf("scan request-log timestamp backfill: %w", err)
		}
		batch = append(batch, row)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate request-log timestamp backfill: %w", err)
	}
	return batch, nil
}

func requestLogTimestampUnixNano(storageClass, value string) (int64, bool) {
	if storageClass != "text" {
		return 0, false
	}
	value = strings.TrimSuffix(value, "Z")
	for _, layout := range requestLogTimestampLayouts {
		createdAt, err := time.ParseInLocation(layout, value, time.UTC)
		if err != nil {
			continue
		}
		unixNano, err := instant.UnixNano(createdAt)
		return unixNano, err == nil
	}
	return 0, false
}

// MigrateRequestLogAnalyticsIndexes commits one verified replacement per phase.
// The schema itself is the durable progress record, so a restart retains every
// completed full-table build and resumes at the first absent or invalid index.
func MigrateRequestLogAnalyticsIndexes(db *gorm.DB) error {
	for _, index := range requestLogAnalyticsIndexes {
		if err := db.Transaction(func(tx *gorm.DB) error {
			statement := fmt.Sprintf(
				"CREATE INDEX IF NOT EXISTS %s ON %s (%s)",
				index.name,
				requestLogsTableName,
				strings.Join(index.columns, ", "),
			)
			if err := tx.Exec(statement).Error; err != nil {
				return fmt.Errorf("create request-log analytics index %s: %w", index.name, err)
			}
			actualColumns, err := requestLogIndexColumns(tx, index.name)
			if err != nil {
				return err
			}
			if !slices.Equal(actualColumns, index.columns) {
				return fmt.Errorf("verify request-log analytics index %s: columns %v, want %v", index.name, actualColumns, index.columns)
			}
			if index.legacyName != "" {
				if err := tx.Exec("DROP INDEX IF EXISTS " + index.legacyName).Error; err != nil {
					return fmt.Errorf("drop legacy request-log analytics index %s: %w", index.legacyName, err)
				}
			}
			return nil
		}); err != nil {
			return err
		}
	}
	return nil
}

func requestLogIndexColumns(db *gorm.DB, indexName string) ([]string, error) {
	var columns []string
	if err := db.Raw("SELECT name FROM pragma_index_info(?) ORDER BY seqno", indexName).Scan(&columns).Error; err != nil {
		return nil, fmt.Errorf("inspect request-log analytics index %s: %w", indexName, err)
	}
	return columns, nil
}
