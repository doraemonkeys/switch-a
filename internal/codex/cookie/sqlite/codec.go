package sqlite

import (
	"errors"

	"github.com/doraemonkeys/switch-a/internal/codex/cookie"
	"github.com/doraemonkeys/switch-a/internal/codex/keyring"
)

const entryColumns = `cookie_name, cookie_domain, cookie_path, value_key_version, value_nonce, value_ciphertext,
	host_only, secure, http_only, quoted, session, same_site, expires_at_ms, created_at_ms, last_access_at_ms`

type entryRow struct {
	name       string
	domain     string
	path       string
	keyVersion string
	nonce      []byte
	ciphertext []byte
	hostOnly   int
	secure     int
	httpOnly   int
	quoted     int
	session    int
	sameSite   int
	expiresAt  int64
	createdAt  int64
	accessedAt int64
}

type rowScanner interface {
	Scan(...any) error
}

func scanEntry(scanner rowScanner) (entryRow, error) {
	var row entryRow
	if err := scanner.Scan(
		&row.name,
		&row.domain,
		&row.path,
		&row.keyVersion,
		&row.nonce,
		&row.ciphertext,
		&row.hostOnly,
		&row.secure,
		&row.httpOnly,
		&row.quoted,
		&row.session,
		&row.sameSite,
		&row.expiresAt,
		&row.createdAt,
		&row.accessedAt,
	); err != nil {
		return entryRow{}, classifyDatabaseError("scan_cookie", err)
	}
	if row.keyVersion == "" || len(row.nonce) != 12 || len(row.ciphertext) < 16 ||
		!validFlag(row.hostOnly) || !validFlag(row.secure) || !validFlag(row.httpOnly) ||
		!validFlag(row.quoted) || !validFlag(row.session) || row.sameSite < 0 || row.sameSite > int(providercookie.SameSiteNone) ||
		row.expiresAt <= 0 || row.createdAt <= 0 || row.accessedAt < row.createdAt {
		return entryRow{}, corruptError("scan_cookie", errors.New("persisted Cookie metadata is invalid"))
	}
	return row, nil
}

func (r *Repository) decryptEntry(scope providercookie.CookieScope, row entryRow) (providercookie.StoredCookie, error) {
	key, err := providercookie.NewCookieKey(row.name, row.domain, row.path)
	if err != nil {
		return providercookie.StoredCookie{}, corruptError("decode_cookie_key", err)
	}
	aad, err := providercookie.EncodeValueAssociatedData(scope, key)
	if err != nil {
		return providercookie.StoredCookie{}, corruptError("encode_cookie_aad", err)
	}
	plaintext, err := r.cipher.Open(codexkeyring.AEADCookieValue, aad, codexkeyring.SealedValue{
		Version:    row.keyVersion,
		Nonce:      append([]byte(nil), row.nonce...),
		Ciphertext: append([]byte(nil), row.ciphertext...),
	})
	if err != nil {
		return providercookie.StoredCookie{}, cryptoReadError("decrypt_cookie", err)
	}
	value := string(plaintext)
	clear(plaintext)
	cookie, err := providercookie.NewStoredCookie(key, value, providercookie.CookieOptions{
		HostOnly:  row.hostOnly == 1,
		Secure:    row.secure == 1,
		HTTPOnly:  row.httpOnly == 1,
		Quoted:    row.quoted == 1,
		Session:   row.session == 1,
		SameSite:  providercookie.SameSite(row.sameSite),
		ExpiresAt: fromMillis(row.expiresAt),
		CreatedAt: fromMillis(row.createdAt),
	})
	if err != nil {
		return providercookie.StoredCookie{}, corruptError("decode_cookie", err)
	}
	return cookie, nil
}

func (r *Repository) sealCookie(scope providercookie.CookieScope, cookie providercookie.StoredCookie) (codexkeyring.SealedValue, error) {
	aad, err := providercookie.EncodeValueAssociatedData(scope, cookie.Key())
	if err != nil {
		return codexkeyring.SealedValue{}, err
	}
	plaintext := []byte(cookie.Value())
	sealed, err := r.cipher.Seal(codexkeyring.AEADCookieValue, aad, plaintext)
	clear(plaintext)
	if err != nil {
		return codexkeyring.SealedValue{}, cryptoWriteError("encrypt_cookie", err)
	}
	if sealed.Version == "" || len(sealed.Nonce) != 12 || len(sealed.Ciphertext) < 16 {
		return codexkeyring.SealedValue{}, &providercookie.PersistenceError{
			Kind:      providercookie.PersistenceCrypto,
			Operation: "encrypt_cookie",
			Cause:     errors.New("cipher returned invalid sealed metadata"),
		}
	}
	return sealed, nil
}

func encodedAuthority(scope providercookie.CookieScope) ([]byte, error) {
	if _, err := scope.MarshalBinary(); err != nil {
		return nil, err
	}
	authority, err := scope.Authority().MarshalBinary()
	if err != nil {
		return nil, &providercookie.StateError{Reason: "cookie authority is uninitialized", Cause: err}
	}
	return authority, nil
}

func validFlag(value int) bool { return value == 0 || value == 1 }

func boolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}

func cryptoReadError(operation string, cause error) error {
	kind := providercookie.PersistenceCrypto
	if codexkeyring.IsError(cause, codexkeyring.ErrorAuthenticationFailed) {
		kind = providercookie.PersistenceDecrypt
	}
	return &providercookie.PersistenceError{Kind: kind, Operation: operation, Cause: cause}
}

func cryptoWriteError(operation string, cause error) error {
	return &providercookie.PersistenceError{
		Kind:      providercookie.PersistenceCrypto,
		Operation: operation,
		Cause:     cause,
	}
}
