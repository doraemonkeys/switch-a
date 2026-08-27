package sqlite

import (
	"fmt"
	"time"

	"github.com/doraemonkeys/switch-a/internal/codex/continuity"
	"github.com/doraemonkeys/switch-a/internal/codex/identity"
)

type bindingRow struct {
	Kind                      string  `gorm:"column:kind;primaryKey"`
	OpaqueKeyVersion          string  `gorm:"column:opaque_key_version;primaryKey"`
	OpaqueDigest              []byte  `gorm:"column:opaque_digest;primaryKey"`
	ClientKeyVersion          string  `gorm:"column:client_key_version"`
	ClientDigest              []byte  `gorm:"column:client_digest"`
	ProtocolVendor            string  `gorm:"column:protocol_vendor"`
	ProtocolOrigin            string  `gorm:"column:protocol_origin"`
	ProtocolSubjectKind       string  `gorm:"column:protocol_subject_kind"`
	ProtocolSubjectAccount    *string `gorm:"column:protocol_subject_account"`
	ProtocolSubjectKeyVersion *string `gorm:"column:protocol_subject_key_version"`
	ProtocolSubjectDigest     []byte  `gorm:"column:protocol_subject_digest"`
	ProtocolAPIType           string  `gorm:"column:protocol_api_type"`
	RouteTargetHint           string  `gorm:"column:route_target_hint"`
	ClaimOperationID          string  `gorm:"column:claim_operation_id"`
	Lifecycle                 string  `gorm:"column:lifecycle"`
	CreatedAtNS               int64   `gorm:"column:created_at_ns"`
	UpdatedAtNS               int64   `gorm:"column:updated_at_ns"`
	CommittedAtNS             *int64  `gorm:"column:committed_at_ns"`
	ExpiresAtNS               int64   `gorm:"column:expires_at_ns"`
	TombstoneUntilNS          *int64  `gorm:"column:tombstone_until_ns"`
}

func (bindingRow) TableName() string { return bindingsTable }

func encodeBinding(binding codexcontinuity.Binding) (bindingRow, error) {
	digest := binding.Digest.Digest()
	clientDigest := binding.Owner.ClientScope.Digest()
	authority := binding.Owner.ProtocolScope.Authority()
	subject := authority.Subject()
	row := bindingRow{
		Kind:                string(binding.Kind),
		OpaqueKeyVersion:    binding.Digest.KeyVersion(),
		OpaqueDigest:        append([]byte(nil), digest[:]...),
		ClientKeyVersion:    binding.Owner.ClientScope.KeyVersion(),
		ClientDigest:        append([]byte(nil), clientDigest[:]...),
		ProtocolVendor:      authority.Vendor(),
		ProtocolOrigin:      authority.Origin().String(),
		ProtocolSubjectKind: string(subject.Kind()),
		ProtocolAPIType:     binding.Owner.ProtocolScope.APIType(),
		RouteTargetHint:     binding.Owner.RouteTargetHint,
		ClaimOperationID:    binding.ClaimOperationID,
		Lifecycle:           string(binding.Lifecycle),
		CreatedAtNS:         binding.CreatedAt.UnixNano(),
		UpdatedAtNS:         binding.UpdatedAt.UnixNano(),
		ExpiresAtNS:         binding.ExpiresAt.UnixNano(),
	}
	if account, ok := subject.AccountID(); ok {
		row.ProtocolSubjectAccount = &account
	} else if version, sum, ok := subject.KeyedDigest(); ok {
		row.ProtocolSubjectKeyVersion = &version
		row.ProtocolSubjectDigest = append([]byte(nil), sum[:]...)
	} else {
		return bindingRow{}, fmt.Errorf("encode continuity binding: credential subject is invalid")
	}
	if binding.CommittedAt != nil {
		value := binding.CommittedAt.UnixNano()
		row.CommittedAtNS = &value
	}
	if binding.TombstoneUntil != nil {
		value := binding.TombstoneUntil.UnixNano()
		row.TombstoneUntilNS = &value
	}
	return row, nil
}

func decodeBinding(row bindingRow) (codexcontinuity.Binding, error) {
	kind := codexcontinuity.Kind(row.Kind)
	if err := kind.Validate(); err != nil {
		return codexcontinuity.Binding{}, err
	}
	namespace, _ := kind.Namespace()
	opaqueSum, err := fixedDigest(row.OpaqueDigest, "opaque digest")
	if err != nil {
		return codexcontinuity.Binding{}, err
	}
	digest, err := codexidentity.OpaqueDigestFromParts(namespace, row.OpaqueKeyVersion, opaqueSum)
	if err != nil {
		return codexcontinuity.Binding{}, fmt.Errorf("decode continuity opaque digest: %w", err)
	}
	clientSum, err := fixedDigest(row.ClientDigest, "client digest")
	if err != nil {
		return codexcontinuity.Binding{}, err
	}
	client, err := codexidentity.ClientScopeFromDigest(row.ClientKeyVersion, clientSum)
	if err != nil {
		return codexcontinuity.Binding{}, fmt.Errorf("decode continuity client scope: %w", err)
	}
	protocolScope, err := decodeProtocolScope(row)
	if err != nil {
		return codexcontinuity.Binding{}, err
	}
	lifecycle := codexcontinuity.Lifecycle(row.Lifecycle)
	if lifecycle != codexcontinuity.LifecyclePending &&
		lifecycle != codexcontinuity.LifecycleCommitted &&
		lifecycle != codexcontinuity.LifecycleTombstone {
		return codexcontinuity.Binding{}, fmt.Errorf("decode continuity binding: unsupported lifecycle")
	}
	binding := codexcontinuity.Binding{
		Kind:             kind,
		Digest:           digest,
		Owner:            codexcontinuity.Owner{ClientScope: client, ProtocolScope: protocolScope, RouteTargetHint: row.RouteTargetHint},
		Lifecycle:        lifecycle,
		ClaimOperationID: row.ClaimOperationID,
		CreatedAt:        time.Unix(0, row.CreatedAtNS).UTC(),
		UpdatedAt:        time.Unix(0, row.UpdatedAtNS).UTC(),
		ExpiresAt:        time.Unix(0, row.ExpiresAtNS).UTC(),
	}
	if row.CommittedAtNS != nil {
		value := time.Unix(0, *row.CommittedAtNS).UTC()
		binding.CommittedAt = &value
	}
	if row.TombstoneUntilNS != nil {
		value := time.Unix(0, *row.TombstoneUntilNS).UTC()
		binding.TombstoneUntil = &value
	}
	return binding, nil
}

func decodeProtocolScope(row bindingRow) (codexidentity.ProtocolScope, error) {
	origin, err := codexidentity.ParseOrigin(row.ProtocolOrigin)
	if err != nil {
		return codexidentity.ProtocolScope{}, fmt.Errorf("decode continuity origin: %w", err)
	}
	var subject codexidentity.CredentialSubject
	switch codexidentity.CredentialSubjectKind(row.ProtocolSubjectKind) {
	case codexidentity.CredentialSubjectAccount:
		if row.ProtocolSubjectAccount == nil {
			return codexidentity.ProtocolScope{}, fmt.Errorf("decode continuity account subject: account is missing")
		}
		subject, err = codexidentity.NewAccountCredentialSubject(*row.ProtocolSubjectAccount)
	case codexidentity.CredentialSubjectKeyedDigest:
		if row.ProtocolSubjectKeyVersion == nil {
			return codexidentity.ProtocolScope{}, fmt.Errorf("decode continuity keyed subject: version is missing")
		}
		var sum [codexidentity.DigestSize]byte
		sum, err = fixedDigest(row.ProtocolSubjectDigest, "credential subject digest")
		if err == nil {
			subject, err = codexidentity.NewKeyedCredentialSubject(*row.ProtocolSubjectKeyVersion, sum)
		}
	default:
		return codexidentity.ProtocolScope{}, fmt.Errorf("decode continuity credential subject: kind is unsupported")
	}
	if err != nil {
		return codexidentity.ProtocolScope{}, fmt.Errorf("decode continuity credential subject: %w", err)
	}
	authority, err := codexidentity.NewUpstreamAuthority(row.ProtocolVendor, origin, subject)
	if err != nil {
		return codexidentity.ProtocolScope{}, fmt.Errorf("decode continuity authority: %w", err)
	}
	protocolScope, err := codexidentity.NewProtocolScope(authority, row.ProtocolAPIType)
	if err != nil {
		return codexidentity.ProtocolScope{}, fmt.Errorf("decode continuity protocol scope: %w", err)
	}
	return protocolScope, nil
}

func fixedDigest(value []byte, field string) ([codexidentity.DigestSize]byte, error) {
	if len(value) != codexidentity.DigestSize {
		return [codexidentity.DigestSize]byte{}, fmt.Errorf("decode continuity %s: invalid length", field)
	}
	var result [codexidentity.DigestSize]byte
	copy(result[:], value)
	return result, nil
}
