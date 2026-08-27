package store

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/doraemonkeys/switch-a/internal/codex/credentialsession"
	codexmaintenance "github.com/doraemonkeys/switch-a/internal/codex/maintenance"

	"gorm.io/gorm"
)

const codexMaintenanceAPIType = "codex"

type codexMaintenanceCatalogRow struct {
	RouteTargetID       string         `gorm:"column:route_target_id"`
	ProviderID          sql.NullString `gorm:"column:provider_id"`
	Vendor              sql.NullString `gorm:"column:vendor"`
	FinalURL            sql.NullString `gorm:"column:final_url"`
	BindingSessionID    sql.NullString `gorm:"column:binding_session_id"`
	CredentialSessionID sql.NullString `gorm:"column:credential_session_id"`
	CredentialVendor    sql.NullString `gorm:"column:credential_vendor"`
	CredentialKind      sql.NullString `gorm:"column:credential_kind"`
	SubjectKind         sql.NullString `gorm:"column:subject_kind"`
	SubjectValue        []byte         `gorm:"column:subject_value"`
	SubjectKeyVersion   sql.NullString `gorm:"column:subject_key_version"`
}

// LoadCodexMaintenanceCatalog reads every Codex route, credential subject, and
// configured final URL through one SQLite snapshot. LEFT JOINs are deliberate:
// silently omitting a damaged parent would turn a partial catalog into false
// evidence that an Authority is unreachable.
func (s *SQLiteStore) LoadCodexMaintenanceCatalog(ctx context.Context) (codexmaintenance.CatalogSnapshot, error) {
	if s == nil || s.db == nil {
		return codexmaintenance.CatalogSnapshot{}, fmt.Errorf("load Codex maintenance catalog: SQLite store is unavailable")
	}
	if ctx == nil {
		return codexmaintenance.CatalogSnapshot{}, fmt.Errorf("load Codex maintenance catalog: context is required")
	}
	var rows []codexMaintenanceCatalogRow
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		return tx.Table("provider_api_types AS api_types").
			Select(`api_types.provider_id AS route_target_id,
				providers.id AS provider_id,
				providers.vendor AS vendor,
				api_types.base_url AS final_url,
				bindings.session_id AS binding_session_id,
				sessions.id AS credential_session_id,
				sessions.vendor AS credential_vendor,
				sessions.kind AS credential_kind,
				sessions.subject_kind AS subject_kind,
				sessions.subject_value AS subject_value,
				sessions.subject_key_version AS subject_key_version`).
			Joins("LEFT JOIN providers ON providers.id = api_types.provider_id").
			Joins("LEFT JOIN route_target_credentials AS bindings ON bindings.route_target_id = api_types.provider_id AND bindings.api_type = api_types.api_type").
			Joins("LEFT JOIN credential_sessions AS sessions ON sessions.id = bindings.session_id").
			Where("api_types.api_type = ?", codexMaintenanceAPIType).
			Order("api_types.provider_id ASC").
			Scan(&rows).Error
	}, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return codexmaintenance.CatalogSnapshot{}, fmt.Errorf("load Codex maintenance catalog: %w", err)
	}
	routes := make([]codexmaintenance.CatalogRoute, 0, len(rows))
	for _, row := range rows {
		if !row.ProviderID.Valid || !row.Vendor.Valid || !row.FinalURL.Valid ||
			!row.BindingSessionID.Valid || !row.CredentialSessionID.Valid || !row.CredentialVendor.Valid ||
			!row.CredentialKind.Valid || !row.SubjectKind.Valid ||
			!row.SubjectKeyVersion.Valid {
			return codexmaintenance.CatalogSnapshot{}, fmt.Errorf("load Codex maintenance catalog: route target %q has an incomplete provider, binding, or credential session", row.RouteTargetID)
		}
		if row.BindingSessionID.String != row.CredentialSessionID.String {
			return codexmaintenance.CatalogSnapshot{}, fmt.Errorf("load Codex maintenance catalog: route target %q resolved a mismatched credential session", row.RouteTargetID)
		}
		if row.Vendor.String != row.CredentialVendor.String {
			return codexmaintenance.CatalogSnapshot{}, fmt.Errorf("load Codex maintenance catalog: route target %q and credential session vendors do not match", row.RouteTargetID)
		}
		kind := credentialsession.Kind(row.CredentialKind.String)
		subjectKind := credentialsession.SubjectKind(row.SubjectKind.String)
		if (kind != credentialsession.KindAPIKey && kind != credentialsession.KindChatGPT) ||
			(kind == credentialsession.KindAPIKey && subjectKind != credentialsession.SubjectKeyedDigest && subjectKind != credentialsession.SubjectPending) ||
			(kind == credentialsession.KindChatGPT && subjectKind != credentialsession.SubjectAccount && subjectKind != credentialsession.SubjectPending) {
			return codexmaintenance.CatalogSnapshot{}, fmt.Errorf("load Codex maintenance catalog: route target %q has an incompatible credential kind and subject", row.RouteTargetID)
		}
		routes = append(routes, codexmaintenance.CatalogRoute{
			RouteTargetID: row.RouteTargetID,
			Vendor:        row.Vendor.String,
			FinalURL:      row.FinalURL.String,
			Subject: credentialsession.Subject{
				Kind:       subjectKind,
				Value:      append([]byte(nil), row.SubjectValue...),
				KeyVersion: row.SubjectKeyVersion.String,
			},
		})
	}
	return codexmaintenance.NewCatalogSnapshot(routes), nil
}
