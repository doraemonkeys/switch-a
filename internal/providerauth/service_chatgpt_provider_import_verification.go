package providerauth

import (
	"bytes"
	"context"
	"crypto"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"math/big"
	"net/http"
	"strings"
	"time"

	"go.uber.org/zap"
)

const (
	chatGPTProviderImportJWKSURL              = "https://auth.openai.com/.well-known/jwks.json"
	maxChatGPTProviderImportJWKSBytes         = 1 << 20
	maxChatGPTProviderImportCompactJWSBytes   = 64 << 10
	maxChatGPTProviderImportRefreshTokenBytes = 16 << 10
	maxChatGPTProviderImportJWTHeaderBytes    = 4 << 10
	maxChatGPTProviderImportJWTPayloadBytes   = 48 << 10
	maxChatGPTProviderImportJWTSignatureBytes = 1 << 10
	chatGPTProviderImportClockSkew            = 2 * time.Minute
	maxChatGPTProviderImportTokenLifetime     = 30 * 24 * time.Hour
	minimumChatGPTProviderImportRSAKeyBits    = 2048
	// Identity strings feed preview, logs, and persisted routing state. Tight
	// protocol-shaped limits prevent a signed but pathological claim retaining the
	// surrounding multi-megabyte import after parsing.
	maxChatGPTProviderImportAccountIDBytes = 256
	maxChatGPTProviderImportEmailBytes     = 320
	maxChatGPTProviderImportPlanTypeBytes  = 128
)

var (
	ErrChatGPTProviderImportJWKSUnavailable    = errors.New("chatgpt provider import signing keys unavailable")
	ErrChatGPTProviderImportVerificationFailed = errors.New("chatgpt provider import token verification failed")
)

// ChatGPTProviderImportVerificationError identifies the opaque preview row that
// failed without retaining or formatting attacker-controlled token or claim data.
type ChatGPTProviderImportVerificationError struct {
	CandidateID string
}

func (e *ChatGPTProviderImportVerificationError) Error() string {
	return ErrChatGPTProviderImportVerificationFailed.Error()
}

func (e *ChatGPTProviderImportVerificationError) Unwrap() error {
	return ErrChatGPTProviderImportVerificationFailed
}

type chatGPTProviderImportJWKSet struct {
	Keys []chatGPTProviderImportJWK `json:"keys"`
}

type chatGPTProviderImportJWK struct {
	KeyType  string `json:"kty"`
	Use      string `json:"use"`
	Alg      string `json:"alg"`
	KeyID    string `json:"kid"`
	Modulus  string `json:"n"`
	Exponent string `json:"e"`
}

type chatGPTProviderImportJOSEHeader struct {
	Algorithm string
	KeyID     string
}

type verifiedChatGPTProviderImportClaims struct {
	Issuer    string
	Audience  []string
	ExpiresAt time.Time
	IssuedAt  *time.Time
	NotBefore *time.Time
	AccountID string
	Email     string
	PlanType  string
}

// VerifyChatGPTProviderImportCandidates cryptographically validates the whole
// selected batch before the caller acquires a store lease. Refresh tokens remain
// opaque: signed access and ID tokens prove the exported account identity, not
// ownership or future validity of the refresh grant.
func (s *Service) VerifyChatGPTProviderImportCandidates(
	ctx context.Context,
	candidates []ChatGPTProviderImportCandidate,
) error {
	if len(candidates) == 0 {
		return nil
	}
	keys, err := s.fetchChatGPTProviderImportJWKSet(ctx)
	if err != nil {
		s.logger.Warn("chatgpt provider import signing keys unavailable",
			zap.Int("candidate_count", len(candidates)),
		)
		return ErrChatGPTProviderImportJWKSUnavailable
	}

	now := s.clock.Now()
	for index := range candidates {
		candidate := &candidates[index]
		if err := verifyChatGPTProviderImportCandidate(candidate, keys, now); err != nil {
			s.logger.Warn("chatgpt provider import candidate verification failed",
				zap.String("candidate_id", candidate.CandidateID),
				zap.Int("source_index", candidate.SourceIndex),
			)
			return &ChatGPTProviderImportVerificationError{CandidateID: candidate.CandidateID}
		}
	}

	s.logger.Info("verified chatgpt provider import candidates",
		zap.Int("candidate_count", len(candidates)),
	)
	return nil
}

func (s *Service) fetchChatGPTProviderImportJWKSet(ctx context.Context) ([]chatGPTProviderImportJWK, error) {
	if ctx == nil {
		return nil, ErrChatGPTProviderImportJWKSUnavailable
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, chatGPTProviderImportJWKSURL, nil)
	if err != nil {
		return nil, ErrChatGPTProviderImportJWKSUnavailable
	}
	response, err := s.httpClient.Do(request)
	if err != nil || response == nil || response.Body == nil {
		return nil, ErrChatGPTProviderImportJWKSUnavailable
	}
	defer response.Body.Close()
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return nil, ErrChatGPTProviderImportJWKSUnavailable
	}

	payload, err := io.ReadAll(io.LimitReader(response.Body, maxChatGPTProviderImportJWKSBytes+1))
	if err != nil || len(payload) > maxChatGPTProviderImportJWKSBytes {
		return nil, ErrChatGPTProviderImportJWKSUnavailable
	}
	var keySet chatGPTProviderImportJWKSet
	decoder := json.NewDecoder(bytes.NewReader(payload))
	if err := decoder.Decode(&keySet); err != nil || len(keySet.Keys) == 0 {
		return nil, ErrChatGPTProviderImportJWKSUnavailable
	}
	if err := requireJSONEOF(decoder); err != nil {
		return nil, ErrChatGPTProviderImportJWKSUnavailable
	}
	return keySet.Keys, nil
}

func verifyChatGPTProviderImportCandidate(
	candidate *ChatGPTProviderImportCandidate,
	keys []chatGPTProviderImportJWK,
	now time.Time,
) error {
	if candidate == nil || candidate.Credential == nil || candidate.AuthState == nil {
		return ErrChatGPTProviderImportVerificationFailed
	}
	secret, err := decodeChatGPTCredentialSecret(candidate.Credential.SecretData)
	if err != nil || secret == nil {
		return ErrChatGPTProviderImportVerificationFailed
	}
	if strings.TrimSpace(secret.AccessToken) == "" {
		return ErrChatGPTProviderImportVerificationFailed
	}
	refreshToken := strings.TrimSpace(secret.RefreshToken)
	if refreshToken == "" || len(refreshToken) > maxChatGPTProviderImportRefreshTokenBytes {
		return ErrChatGPTProviderImportVerificationFailed
	}
	if strings.TrimSpace(secret.OAuthIssuer) != defaultOAuthIssuer ||
		strings.TrimSpace(secret.OAuthClientID) != defaultOAuthClientID {
		return ErrChatGPTProviderImportVerificationFailed
	}

	accessClaims, err := verifyChatGPTProviderImportToken(
		secret.AccessToken,
		chatGPTAPIAudience,
		keys,
		now,
	)
	if err != nil {
		return ErrChatGPTProviderImportVerificationFailed
	}
	if strings.TrimSpace(secret.IDToken) != "" {
		idClaims, err := verifyChatGPTProviderImportToken(
			secret.IDToken,
			defaultOAuthClientID,
			keys,
			now,
		)
		if err != nil || accessClaims.AccountID != idClaims.AccountID {
			return ErrChatGPTProviderImportVerificationFailed
		}
	}

	accountID := accessClaims.AccountID
	if normalizedBindingAccountID(candidate.Credential.BindingAccountID) != accountID ||
		strings.TrimSpace(candidate.AuthState.AccountID) != accountID {
		return ErrChatGPTProviderImportVerificationFailed
	}
	return nil
}

func verifyChatGPTProviderImportToken(
	token string,
	expectedAudience string,
	keys []chatGPTProviderImportJWK,
	now time.Time,
) (verifiedChatGPTProviderImportClaims, error) {
	if len(strings.TrimSpace(token)) > maxChatGPTProviderImportCompactJWSBytes {
		return verifiedChatGPTProviderImportClaims{}, ErrChatGPTProviderImportVerificationFailed
	}
	headerSegment, payloadSegment, signatureSegment, err := splitCompactJWS(token)
	if err != nil {
		return verifiedChatGPTProviderImportClaims{}, err
	}
	headerJSON, err := decodeChatGPTProviderImportJWTSegment(
		headerSegment,
		maxChatGPTProviderImportJWTHeaderBytes,
	)
	if err != nil {
		return verifiedChatGPTProviderImportClaims{}, err
	}
	header, err := decodeChatGPTProviderImportJOSEHeader(headerJSON)
	if err != nil || header.Algorithm != "RS256" || strings.TrimSpace(header.KeyID) == "" {
		return verifiedChatGPTProviderImportClaims{}, ErrChatGPTProviderImportVerificationFailed
	}
	publicKey, err := selectChatGPTProviderImportRSAKey(keys, header.KeyID)
	if err != nil {
		return verifiedChatGPTProviderImportClaims{}, err
	}
	signature, err := decodeChatGPTProviderImportJWTSegment(
		signatureSegment,
		maxChatGPTProviderImportJWTSignatureBytes,
	)
	if err != nil {
		return verifiedChatGPTProviderImportClaims{}, err
	}

	hash := sha256.New()
	_, _ = io.WriteString(hash, headerSegment)
	_, _ = io.WriteString(hash, ".")
	_, _ = io.WriteString(hash, payloadSegment)
	if err := rsa.VerifyPKCS1v15(publicKey, crypto.SHA256, hash.Sum(nil), signature); err != nil {
		return verifiedChatGPTProviderImportClaims{}, ErrChatGPTProviderImportVerificationFailed
	}

	payloadJSON, err := decodeChatGPTProviderImportJWTSegment(
		payloadSegment,
		maxChatGPTProviderImportJWTPayloadBytes,
	)
	if err != nil {
		return verifiedChatGPTProviderImportClaims{}, err
	}
	claims, err := decodeVerifiedChatGPTProviderImportClaims(payloadJSON)
	if err != nil || claims.Issuer != defaultOAuthIssuer ||
		!hasExactChatGPTProviderImportAudience(claims.Audience, expectedAudience) {
		return verifiedChatGPTProviderImportClaims{}, ErrChatGPTProviderImportVerificationFailed
	}
	latestAccepted := now.Add(chatGPTProviderImportClockSkew)
	if (claims.IssuedAt != nil && claims.IssuedAt.After(latestAccepted)) ||
		(claims.NotBefore != nil && claims.NotBefore.After(latestAccepted)) {
		return verifiedChatGPTProviderImportClaims{}, ErrChatGPTProviderImportVerificationFailed
	}
	// Expired signed tokens remain importable so the normal refresh path can heal
	// them, but chronology and lifetime still bound attacker-chosen future dates.
	if claims.ExpiresAt.After(latestAccepted.Add(maxChatGPTProviderImportTokenLifetime)) ||
		(claims.IssuedAt != nil && (!claims.ExpiresAt.After(*claims.IssuedAt) ||
			claims.ExpiresAt.Sub(*claims.IssuedAt) > maxChatGPTProviderImportTokenLifetime)) ||
		(claims.NotBefore != nil && !claims.ExpiresAt.After(*claims.NotBefore)) {
		return verifiedChatGPTProviderImportClaims{}, ErrChatGPTProviderImportVerificationFailed
	}
	return claims, nil
}

func decodeChatGPTProviderImportJWTSegment(segment string, maximumBytes int) ([]byte, error) {
	if segment == "" || base64.RawURLEncoding.DecodedLen(len(segment)) > maximumBytes {
		return nil, ErrChatGPTProviderImportVerificationFailed
	}
	decoded, err := base64.RawURLEncoding.DecodeString(segment)
	if err != nil || len(decoded) > maximumBytes {
		return nil, ErrChatGPTProviderImportVerificationFailed
	}
	return decoded, nil
}

func decodeChatGPTProviderImportJOSEHeader(raw []byte) (chatGPTProviderImportJOSEHeader, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	opening, err := decoder.Token()
	if err != nil || opening != json.Delim('{') {
		return chatGPTProviderImportJOSEHeader{}, ErrChatGPTProviderImportVerificationFailed
	}
	var header chatGPTProviderImportJOSEHeader
	var seenAlgorithm, seenKeyID bool
	for decoder.More() {
		key, err := readJSONObjectKey(decoder)
		if err != nil {
			return chatGPTProviderImportJOSEHeader{}, err
		}
		switch key {
		case "alg":
			if seenAlgorithm || decoder.Decode(&header.Algorithm) != nil {
				return chatGPTProviderImportJOSEHeader{}, ErrChatGPTProviderImportVerificationFailed
			}
			seenAlgorithm = true
		case "kid":
			if seenKeyID || decoder.Decode(&header.KeyID) != nil {
				return chatGPTProviderImportJOSEHeader{}, ErrChatGPTProviderImportVerificationFailed
			}
			seenKeyID = true
		default:
			if err := skipNextJSONValue(decoder); err != nil {
				return chatGPTProviderImportJOSEHeader{}, ErrChatGPTProviderImportVerificationFailed
			}
		}
	}
	if closing, err := decoder.Token(); err != nil || closing != json.Delim('}') {
		return chatGPTProviderImportJOSEHeader{}, ErrChatGPTProviderImportVerificationFailed
	}
	if err := requireJSONEOF(decoder); err != nil {
		return chatGPTProviderImportJOSEHeader{}, err
	}
	return header, nil
}

func selectChatGPTProviderImportRSAKey(
	keys []chatGPTProviderImportJWK,
	keyID string,
) (*rsa.PublicKey, error) {
	var match *chatGPTProviderImportJWK
	for index := range keys {
		if strings.TrimSpace(keys[index].KeyID) != keyID {
			continue
		}
		if match != nil {
			return nil, ErrChatGPTProviderImportVerificationFailed
		}
		match = &keys[index]
	}
	if match == nil || match.KeyType != "RSA" ||
		(match.Use != "" && match.Use != "sig") ||
		(match.Alg != "" && match.Alg != "RS256") {
		return nil, ErrChatGPTProviderImportVerificationFailed
	}
	modulusBytes, err := base64.RawURLEncoding.DecodeString(match.Modulus)
	if err != nil || len(modulusBytes) == 0 {
		return nil, ErrChatGPTProviderImportVerificationFailed
	}
	exponentBytes, err := base64.RawURLEncoding.DecodeString(match.Exponent)
	if err != nil || len(exponentBytes) == 0 || len(exponentBytes) > 4 {
		return nil, ErrChatGPTProviderImportVerificationFailed
	}
	exponent := 0
	for _, value := range exponentBytes {
		exponent = exponent<<8 | int(value)
	}
	modulus := new(big.Int).SetBytes(modulusBytes)
	if modulus.BitLen() < minimumChatGPTProviderImportRSAKeyBits || exponent < 3 || exponent%2 == 0 {
		return nil, ErrChatGPTProviderImportVerificationFailed
	}
	return &rsa.PublicKey{N: modulus, E: exponent}, nil
}

func decodeVerifiedChatGPTProviderImportClaims(raw []byte) (verifiedChatGPTProviderImportClaims, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	opening, err := decoder.Token()
	if err != nil || opening != json.Delim('{') {
		return verifiedChatGPTProviderImportClaims{}, ErrChatGPTProviderImportVerificationFailed
	}
	var claims verifiedChatGPTProviderImportClaims
	var expiration, issuedAt, notBefore json.RawMessage
	seen := make(map[string]struct{}, 7)
	for decoder.More() {
		key, err := readJSONObjectKey(decoder)
		if err != nil {
			return verifiedChatGPTProviderImportClaims{}, err
		}
		switch key {
		case "iss", "aud", "exp", "iat", "nbf", "email", "https://api.openai.com/auth":
			if _, duplicate := seen[key]; duplicate {
				return verifiedChatGPTProviderImportClaims{}, ErrChatGPTProviderImportVerificationFailed
			}
			seen[key] = struct{}{}
		}
		switch key {
		case "iss":
			err = decoder.Decode(&claims.Issuer)
		case "aud":
			claims.Audience, err = decodeChatGPTProviderImportAudience(decoder)
		case "exp":
			err = decoder.Decode(&expiration)
		case "iat":
			err = decoder.Decode(&issuedAt)
		case "nbf":
			err = decoder.Decode(&notBefore)
		case "email":
			err = decoder.Decode(&claims.Email)
		case "https://api.openai.com/auth":
			claims.AccountID, claims.PlanType, err = decodeChatGPTProviderImportAuthClaims(decoder)
		default:
			err = skipNextJSONValue(decoder)
		}
		if err != nil {
			return verifiedChatGPTProviderImportClaims{}, ErrChatGPTProviderImportVerificationFailed
		}
	}
	if closing, err := decoder.Token(); err != nil || closing != json.Delim('}') {
		return verifiedChatGPTProviderImportClaims{}, ErrChatGPTProviderImportVerificationFailed
	}
	if err := requireJSONEOF(decoder); err != nil {
		return verifiedChatGPTProviderImportClaims{}, err
	}
	claims.ExpiresAt, err = decodeChatGPTProviderImportNumericDate(expiration)
	claims.Email = strings.TrimSpace(claims.Email)
	claims.PlanType = strings.TrimSpace(claims.PlanType)
	if err != nil || claims.AccountID == "" ||
		len(claims.AccountID) > maxChatGPTProviderImportAccountIDBytes ||
		len(claims.Email) > maxChatGPTProviderImportEmailBytes ||
		len(claims.PlanType) > maxChatGPTProviderImportPlanTypeBytes {
		return verifiedChatGPTProviderImportClaims{}, ErrChatGPTProviderImportVerificationFailed
	}
	if len(issuedAt) > 0 {
		value, err := decodeChatGPTProviderImportNumericDate(issuedAt)
		if err != nil {
			return verifiedChatGPTProviderImportClaims{}, err
		}
		claims.IssuedAt = &value
	}
	if len(notBefore) > 0 {
		value, err := decodeChatGPTProviderImportNumericDate(notBefore)
		if err != nil {
			return verifiedChatGPTProviderImportClaims{}, err
		}
		claims.NotBefore = &value
	}
	return claims, nil
}

func decodeChatGPTProviderImportAudience(decoder *json.Decoder) ([]string, error) {
	var raw json.RawMessage
	if err := decoder.Decode(&raw); err != nil {
		return nil, err
	}
	var single string
	if json.Unmarshal(raw, &single) == nil {
		return []string{single}, nil
	}
	var multiple []string
	if json.Unmarshal(raw, &multiple) != nil || len(multiple) == 0 {
		return nil, ErrChatGPTProviderImportVerificationFailed
	}
	return multiple, nil
}

func hasExactChatGPTProviderImportAudience(audience []string, expected string) bool {
	if len(audience) == 0 {
		return false
	}
	for _, value := range audience {
		if strings.TrimSpace(value) != expected {
			return false
		}
	}
	return true
}

func decodeChatGPTProviderImportAuthClaims(decoder *json.Decoder) (string, string, error) {
	opening, err := decoder.Token()
	if err != nil || opening != json.Delim('{') {
		return "", "", ErrChatGPTProviderImportVerificationFailed
	}
	var accountID, planType string
	var seenAccountID, seenPlanType bool
	for decoder.More() {
		key, err := readJSONObjectKey(decoder)
		if err != nil {
			return "", "", err
		}
		switch key {
		case "chatgpt_account_id":
			if seenAccountID || decoder.Decode(&accountID) != nil {
				return "", "", ErrChatGPTProviderImportVerificationFailed
			}
			seenAccountID = true
		case "chatgpt_plan_type":
			if seenPlanType || decoder.Decode(&planType) != nil {
				return "", "", ErrChatGPTProviderImportVerificationFailed
			}
			seenPlanType = true
		default:
			if err := skipNextJSONValue(decoder); err != nil {
				return "", "", ErrChatGPTProviderImportVerificationFailed
			}
		}
	}
	if closing, err := decoder.Token(); err != nil || closing != json.Delim('}') {
		return "", "", ErrChatGPTProviderImportVerificationFailed
	}
	return strings.TrimSpace(accountID), strings.TrimSpace(planType), nil
}

func decodeChatGPTProviderImportNumericDate(raw json.RawMessage) (time.Time, error) {
	if len(raw) == 0 {
		return time.Time{}, ErrChatGPTProviderImportVerificationFailed
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var number json.Number
	if err := decoder.Decode(&number); err != nil {
		return time.Time{}, ErrChatGPTProviderImportVerificationFailed
	}
	if err := requireJSONEOF(decoder); err != nil {
		return time.Time{}, err
	}
	seconds, err := number.Int64()
	if err != nil {
		return time.Time{}, ErrChatGPTProviderImportVerificationFailed
	}
	value, err := jsonRepresentableUnixTime(seconds)
	if err != nil {
		return time.Time{}, ErrChatGPTProviderImportVerificationFailed
	}
	return value, nil
}

func readJSONObjectKey(decoder *json.Decoder) (string, error) {
	raw, err := decoder.Token()
	if err != nil {
		return "", ErrChatGPTProviderImportVerificationFailed
	}
	key, ok := raw.(string)
	if !ok {
		return "", ErrChatGPTProviderImportVerificationFailed
	}
	return key, nil
}

func requireJSONEOF(decoder *json.Decoder) error {
	if _, err := decoder.Token(); err != io.EOF {
		return ErrChatGPTProviderImportVerificationFailed
	}
	return nil
}
