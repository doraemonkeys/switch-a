package providerauth

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/doraemonkeys/switch-a/internal/codex/credentialsession"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"
)

const providerImportVerifierTestKeyID = "test-signing-key"

func newProviderImportVerifierTestKey(t *testing.T) *rsa.PrivateKey {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, minimumChatGPTProviderImportRSAKeyBits)
	if err != nil {
		t.Fatalf("rsa.GenerateKey returned error: %v", err)
	}
	return key
}

func providerImportVerifierTestJWK(key *rsa.PrivateKey, keyID, use, algorithm string) chatGPTProviderImportJWK {
	return chatGPTProviderImportJWK{
		KeyType:  "RSA",
		Use:      use,
		Alg:      algorithm,
		KeyID:    keyID,
		Modulus:  base64.RawURLEncoding.EncodeToString(key.PublicKey.N.Bytes()),
		Exponent: base64.RawURLEncoding.EncodeToString(bigEndianInt(key.PublicKey.E)),
	}
}

func bigEndianInt(value int) []byte {
	bytes := make([]byte, 0, 4)
	for value > 0 {
		bytes = append([]byte{byte(value)}, bytes...)
		value >>= 8
	}
	return bytes
}

func marshalProviderImportVerifierJWKSet(t *testing.T, keys ...chatGPTProviderImportJWK) string {
	t.Helper()
	payload, err := json.Marshal(chatGPTProviderImportJWKSet{Keys: keys})
	if err != nil {
		t.Fatalf("json.Marshal JWK set returned error: %v", err)
	}
	return string(payload)
}

func signProviderImportVerifierJWT(
	t *testing.T,
	key *rsa.PrivateKey,
	algorithm string,
	keyID *string,
	claims map[string]any,
) string {
	t.Helper()
	header := map[string]any{"alg": algorithm, "typ": "JWT"}
	if keyID != nil {
		header["kid"] = *keyID
	}
	headerJSON, err := json.Marshal(header)
	if err != nil {
		t.Fatalf("json.Marshal header returned error: %v", err)
	}
	return signProviderImportVerifierJWTWithHeader(t, key, headerJSON, claims)
}

func signProviderImportVerifierJWTWithHeader(
	t *testing.T,
	key *rsa.PrivateKey,
	headerJSON []byte,
	claims map[string]any,
) string {
	t.Helper()
	payloadJSON, err := json.Marshal(claims)
	if err != nil {
		t.Fatalf("json.Marshal claims returned error: %v", err)
	}
	header := base64.RawURLEncoding.EncodeToString(headerJSON)
	payload := base64.RawURLEncoding.EncodeToString(payloadJSON)
	signingInput := header + "." + payload
	digest := sha256.Sum256([]byte(signingInput))
	signature, err := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, digest[:])
	if err != nil {
		t.Fatalf("rsa.SignPKCS1v15 returned error: %v", err)
	}
	return signingInput + "." + base64.RawURLEncoding.EncodeToString(signature)
}

func tamperProviderImportVerifierJWT(t *testing.T, token string) string {
	t.Helper()
	header, payload, signature, err := splitCompactJWS(token)
	if err != nil {
		t.Fatalf("splitCompactJWS returned error: %v", err)
	}
	decoded, err := base64.RawURLEncoding.DecodeString(signature)
	if err != nil || len(decoded) == 0 {
		t.Fatalf("decode signature returned (%d bytes, %v)", len(decoded), err)
	}
	decoded[0] ^= 1
	return header + "." + payload + "." + base64.RawURLEncoding.EncodeToString(decoded)
}

func providerImportVerifierClaims(now time.Time, accountID, audience string) map[string]any {
	return map[string]any{
		"iss": defaultOAuthIssuer,
		"aud": audience,
		"exp": now.Add(10 * time.Minute).Unix(),
		"iat": now.Add(-time.Minute).Unix(),
		"nbf": now.Add(-time.Minute).Unix(),
		"https://api.openai.com/auth": map[string]any{
			"chatgpt_account_id": accountID,
			"chatgpt_plan_type":  "plus",
		},
	}
}

func providerImportVerifierCandidate(
	t *testing.T,
	candidateID, accountID, accessToken, idToken string,
) ChatGPTProviderImportCandidate {
	t.Helper()
	secret, err := json.Marshal(chatGPTCredentialSecret{
		AccessToken:   accessToken,
		RefreshToken:  "refresh-secret-" + candidateID,
		IDToken:       idToken,
		OAuthIssuer:   defaultOAuthIssuer,
		OAuthClientID: defaultOAuthClientID,
	})
	if err != nil {
		t.Fatalf("json.Marshal credential returned error: %v", err)
	}
	subject, err := credentialsession.AccountSubject(accountID)
	if err != nil {
		t.Fatal(err)
	}
	return ChatGPTProviderImportCandidate{
		CandidateID: candidateID,
		State:       ChatGPTProviderImportCandidateStateReady,
		Credential: credentialsession.Snapshot{
			Kind:       credentialsession.KindChatGPT,
			SecretData: string(secret), Version: 1, Subject: subject,
			AuthState: credentialsession.AuthState{
				Status: credentialsession.AuthStatusActive, AccountID: accountID,
			},
		},
	}
}

func runProviderImportVerifier(
	t *testing.T,
	now time.Time,
	status int,
	body string,
	doError error,
	logger *zap.Logger,
	candidates []ChatGPTProviderImportCandidate,
) (error, int, *http.Request) {
	t.Helper()
	calls := 0
	var captured *http.Request
	service := NewService(Config{
		Clock:  fixedClock{now: now},
		Logger: logger,
		HTTPClient: stubHTTPDoer{do: func(request *http.Request) (*http.Response, error) {
			calls++
			captured = request
			if doError != nil {
				return nil, doError
			}
			return &http.Response{
				StatusCode: status,
				Status:     fmt.Sprintf("%d test", status),
				Body:       io.NopCloser(strings.NewReader(body)),
			}, nil
		}},
	})
	return service.VerifyChatGPTProviderImportCandidates(context.Background(), candidates), calls, captured
}

func TestVerifyChatGPTProviderImportCandidates_SignedBatchAndOptionalIDToken(t *testing.T) {
	now := time.Date(2026, time.July, 30, 12, 0, 0, 0, time.UTC)
	key := newProviderImportVerifierTestKey(t)
	keyID := providerImportVerifierTestKeyID
	accessOne := signProviderImportVerifierJWT(t, key, "RS256", &keyID, providerImportVerifierClaims(now, "acct-one", chatGPTAPIAudience))
	idOne := signProviderImportVerifierJWT(t, key, "RS256", &keyID, providerImportVerifierClaims(now, "acct-one", defaultOAuthClientID))
	accessOnly := signProviderImportVerifierJWT(t, key, "RS256", &keyID, providerImportVerifierClaims(now, "acct-two", chatGPTAPIAudience))
	candidates := []ChatGPTProviderImportCandidate{
		providerImportVerifierCandidate(t, "candidate-one", "acct-one", accessOne, idOne),
		providerImportVerifierCandidate(t, "candidate-two", "acct-two", accessOnly, ""),
	}
	jwks := marshalProviderImportVerifierJWKSet(t, providerImportVerifierTestJWK(key, keyID, "sig", "RS256"))

	err, calls, request := runProviderImportVerifier(t, now, http.StatusOK, jwks, nil, zap.NewNop(), candidates)
	if err != nil {
		t.Fatalf("VerifyChatGPTProviderImportCandidates returned error: %v", err)
	}
	if calls != 1 {
		t.Fatalf("JWKS request count = %d, want 1 for the whole batch", calls)
	}
	if request == nil || request.Method != http.MethodGet || request.URL.String() != chatGPTProviderImportJWKSURL {
		t.Fatalf("JWKS request = %#v, want GET %s", request, chatGPTProviderImportJWKSURL)
	}
}

func TestVerifyChatGPTProviderImportCandidates_AcceptsExpiredAuthenticToken(t *testing.T) {
	now := time.Date(2026, time.July, 30, 12, 0, 0, 0, time.UTC)
	key := newProviderImportVerifierTestKey(t)
	keyID := providerImportVerifierTestKeyID
	claims := providerImportVerifierClaims(now, "acct-expired", chatGPTAPIAudience)
	claims["iat"] = now.Add(-2 * time.Hour).Unix()
	claims["nbf"] = now.Add(-2 * time.Hour).Unix()
	claims["exp"] = now.Add(-time.Hour).Unix()
	accessToken := signProviderImportVerifierJWT(t, key, "RS256", &keyID, claims)
	candidate := providerImportVerifierCandidate(t, "candidate-expired", "acct-expired", accessToken, "")
	jwks := marshalProviderImportVerifierJWKSet(t, providerImportVerifierTestJWK(key, keyID, "", ""))

	err, _, _ := runProviderImportVerifier(t, now, http.StatusOK, jwks, nil, zap.NewNop(), []ChatGPTProviderImportCandidate{candidate})
	if err != nil {
		t.Fatalf("expired authentic token should remain importable: %v", err)
	}
}

func TestVerifyChatGPTProviderImportCandidates_RejectsOversizedOpaqueRefreshToken(t *testing.T) {
	now := time.Date(2026, time.July, 30, 12, 0, 0, 0, time.UTC)
	key := newProviderImportVerifierTestKey(t)
	keyID := providerImportVerifierTestKeyID
	access := signProviderImportVerifierJWT(t, key, "RS256", &keyID, providerImportVerifierClaims(now, "acct-refresh-size", chatGPTAPIAudience))
	candidate := providerImportVerifierCandidate(t, "candidate-refresh-size", "acct-refresh-size", access, "")
	secret, err := decodeChatGPTCredentialSecret(candidate.Credential.SecretData)
	if err != nil {
		t.Fatalf("decodeChatGPTCredentialSecret returned error: %v", err)
	}
	secret.RefreshToken = strings.Repeat("<", maxChatGPTProviderImportRefreshTokenBytes+1)
	payload, err := json.Marshal(secret)
	if err != nil {
		t.Fatalf("json.Marshal secret returned error: %v", err)
	}
	candidate.Credential.SecretData = string(payload)
	jwks := marshalProviderImportVerifierJWKSet(t, providerImportVerifierTestJWK(key, keyID, "sig", "RS256"))

	err, _, _ = runProviderImportVerifier(t, now, http.StatusOK, jwks, nil, zap.NewNop(), []ChatGPTProviderImportCandidate{candidate})
	if !errors.Is(err, ErrChatGPTProviderImportVerificationFailed) {
		t.Fatalf("error = %v, want oversized refresh verification failure", err)
	}
}

func TestVerifyChatGPTProviderImportCandidates_RejectsInvalidTokenBatchAtomically(t *testing.T) {
	now := time.Date(2026, time.July, 30, 12, 0, 0, 0, time.UTC)
	key := newProviderImportVerifierTestKey(t)
	keyID := providerImportVerifierTestKeyID
	validAccess := signProviderImportVerifierJWT(t, key, "RS256", &keyID, providerImportVerifierClaims(now, "acct-good", chatGPTAPIAudience))
	badAccess := signProviderImportVerifierJWT(t, key, "RS256", &keyID, providerImportVerifierClaims(now, "acct-bad", chatGPTAPIAudience))
	badAccess = tamperProviderImportVerifierJWT(t, badAccess)
	candidates := []ChatGPTProviderImportCandidate{
		providerImportVerifierCandidate(t, "candidate-good", "acct-good", validAccess, ""),
		providerImportVerifierCandidate(t, "candidate-bad", "acct-bad", badAccess, ""),
	}
	jwks := marshalProviderImportVerifierJWKSet(t, providerImportVerifierTestJWK(key, keyID, "sig", "RS256"))
	core, observed := observer.New(zap.DebugLevel)

	err, calls, _ := runProviderImportVerifier(t, now, http.StatusOK, jwks, nil, zap.New(core), candidates)
	if !errors.Is(err, ErrChatGPTProviderImportVerificationFailed) {
		t.Fatalf("error = %v, want ErrChatGPTProviderImportVerificationFailed", err)
	}
	var candidateError *ChatGPTProviderImportVerificationError
	if !errors.As(err, &candidateError) || candidateError.CandidateID != "candidate-bad" {
		t.Fatalf("typed error = %#v, want candidate-bad", candidateError)
	}
	if calls != 1 {
		t.Fatalf("JWKS request count = %d, want 1", calls)
	}
	serializedLogs := fmt.Sprint(observed.All())
	for _, secret := range []string{validAccess, badAccess, "refresh-secret-candidate-good", "refresh-secret-candidate-bad"} {
		if strings.Contains(err.Error(), secret) || strings.Contains(serializedLogs, secret) {
			t.Fatalf("error or logs disclosed imported secret %q", secret)
		}
	}
}

func TestVerifyChatGPTProviderImportCandidates_RejectsJOSEAndBindingViolations(t *testing.T) {
	now := time.Date(2026, time.July, 30, 12, 0, 0, 0, time.UTC)
	key := newProviderImportVerifierTestKey(t)
	otherKey := newProviderImportVerifierTestKey(t)
	keyID := providerImportVerifierTestKeyID
	baseClaims := providerImportVerifierClaims(now, "acct-test", chatGPTAPIAudience)
	validJWK := providerImportVerifierTestJWK(key, keyID, "sig", "RS256")

	tests := []struct {
		name      string
		access    func(*testing.T) string
		idToken   func(*testing.T) string
		accountID string
		keys      []chatGPTProviderImportJWK
	}{
		{
			name: "wrong algorithm",
			access: func(t *testing.T) string {
				return signProviderImportVerifierJWT(t, key, "PS256", &keyID, baseClaims)
			},
			keys: []chatGPTProviderImportJWK{validJWK},
		},
		{
			name: "oversized compact jws",
			access: func(*testing.T) string {
				return strings.Repeat("a", maxChatGPTProviderImportCompactJWSBytes+1) + ".b.c"
			},
			keys: []chatGPTProviderImportJWK{validJWK},
		},
		{
			name: "missing key id",
			access: func(t *testing.T) string {
				return signProviderImportVerifierJWT(t, key, "RS256", nil, baseClaims)
			},
			keys: []chatGPTProviderImportJWK{validJWK},
		},
		{
			name: "unknown key id",
			access: func(t *testing.T) string {
				unknown := "unknown-key"
				return signProviderImportVerifierJWT(t, key, "RS256", &unknown, baseClaims)
			},
			keys: []chatGPTProviderImportJWK{validJWK},
		},
		{
			name: "signature from another key",
			access: func(t *testing.T) string {
				return signProviderImportVerifierJWT(t, otherKey, "RS256", &keyID, baseClaims)
			},
			keys: []chatGPTProviderImportJWK{validJWK},
		},
		{
			name: "duplicate key id",
			access: func(t *testing.T) string {
				return signProviderImportVerifierJWT(t, key, "RS256", &keyID, baseClaims)
			},
			keys: []chatGPTProviderImportJWK{validJWK, providerImportVerifierTestJWK(otherKey, keyID, "sig", "RS256")},
		},
		{
			name: "non signing jwk",
			access: func(t *testing.T) string {
				return signProviderImportVerifierJWT(t, key, "RS256", &keyID, baseClaims)
			},
			keys: []chatGPTProviderImportJWK{providerImportVerifierTestJWK(key, keyID, "enc", "RS256")},
		},
		{
			name: "duplicate protected algorithm",
			access: func(t *testing.T) string {
				return signProviderImportVerifierJWTWithHeader(t, key, []byte(`{"alg":"RS256","alg":"RS256","kid":"test-signing-key"}`), baseClaims)
			},
			keys: []chatGPTProviderImportJWK{validJWK},
		},
		{
			name: "optional id token account mismatch",
			access: func(t *testing.T) string {
				return signProviderImportVerifierJWT(t, key, "RS256", &keyID, baseClaims)
			},
			idToken: func(t *testing.T) string {
				return signProviderImportVerifierJWT(t, key, "RS256", &keyID, providerImportVerifierClaims(now, "acct-other", defaultOAuthClientID))
			},
			keys: []chatGPTProviderImportJWK{validJWK},
		},
		{
			name: "candidate binding mismatch",
			access: func(t *testing.T) string {
				return signProviderImportVerifierJWT(t, key, "RS256", &keyID, baseClaims)
			},
			accountID: "acct-other",
			keys:      []chatGPTProviderImportJWK{validJWK},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			accountID := test.accountID
			if accountID == "" {
				accountID = "acct-test"
			}
			idToken := ""
			if test.idToken != nil {
				idToken = test.idToken(t)
			}
			candidate := providerImportVerifierCandidate(t, "candidate-violation", accountID, test.access(t), idToken)
			jwks := marshalProviderImportVerifierJWKSet(t, test.keys...)
			err, _, _ := runProviderImportVerifier(t, now, http.StatusOK, jwks, nil, zap.NewNop(), []ChatGPTProviderImportCandidate{candidate})
			if !errors.Is(err, ErrChatGPTProviderImportVerificationFailed) {
				t.Fatalf("error = %v, want verification failure", err)
			}
		})
	}
}

func TestVerifyChatGPTProviderImportCandidates_RejectsInvalidSignedTimes(t *testing.T) {
	now := time.Date(2026, time.July, 30, 12, 0, 0, 0, time.UTC)
	key := newProviderImportVerifierTestKey(t)
	keyID := providerImportVerifierTestKeyID
	jwks := marshalProviderImportVerifierJWKSet(t, providerImportVerifierTestJWK(key, keyID, "sig", "RS256"))
	tests := []struct {
		name   string
		mutate func(map[string]any)
	}{
		{name: "future issued at", mutate: func(claims map[string]any) {
			claims["iat"] = now.Add(chatGPTProviderImportClockSkew + time.Second).Unix()
		}},
		{name: "future not before", mutate: func(claims map[string]any) {
			claims["nbf"] = now.Add(chatGPTProviderImportClockSkew + time.Second).Unix()
		}},
		{name: "expiration before issuance", mutate: func(claims map[string]any) { claims["exp"] = now.Add(-2 * time.Minute).Unix() }},
		{name: "excessive lifetime", mutate: func(claims map[string]any) {
			claims["exp"] = now.Add(maxChatGPTProviderImportTokenLifetime + time.Hour).Unix()
		}},
		{name: "non integer expiration", mutate: func(claims map[string]any) { claims["exp"] = 1.5 }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			claims := providerImportVerifierClaims(now, "acct-time", chatGPTAPIAudience)
			test.mutate(claims)
			access := signProviderImportVerifierJWT(t, key, "RS256", &keyID, claims)
			candidate := providerImportVerifierCandidate(t, "candidate-time", "acct-time", access, "")
			err, _, _ := runProviderImportVerifier(t, now, http.StatusOK, jwks, nil, zap.NewNop(), []ChatGPTProviderImportCandidate{candidate})
			if !errors.Is(err, ErrChatGPTProviderImportVerificationFailed) {
				t.Fatalf("error = %v, want verification failure", err)
			}
		})
	}
}

func TestVerifyChatGPTProviderImportCandidates_JWKSUnavailableIsTypedAndBounded(t *testing.T) {
	now := time.Date(2026, time.July, 30, 12, 0, 0, 0, time.UTC)
	candidate := ChatGPTProviderImportCandidate{CandidateID: "not-inspected-on-outage"}
	tests := []struct {
		name    string
		status  int
		body    string
		doError error
	}{
		{name: "transport", status: http.StatusOK, doError: errors.New("transport-secret")},
		{name: "non success", status: http.StatusServiceUnavailable, body: "upstream-secret"},
		{name: "malformed", status: http.StatusOK, body: "{"},
		{name: "empty key set", status: http.StatusOK, body: `{"keys":[]}`},
		{name: "oversized", status: http.StatusOK, body: strings.Repeat("x", maxChatGPTProviderImportJWKSBytes+1)},
		{name: "trailing json", status: http.StatusOK, body: `{"keys":[{}]} {}`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err, calls, _ := runProviderImportVerifier(t, now, test.status, test.body, test.doError, zap.NewNop(), []ChatGPTProviderImportCandidate{candidate})
			if !errors.Is(err, ErrChatGPTProviderImportJWKSUnavailable) {
				t.Fatalf("error = %v, want ErrChatGPTProviderImportJWKSUnavailable", err)
			}
			if errors.Is(err, ErrChatGPTProviderImportVerificationFailed) {
				t.Fatalf("error = %v, should remain retryable JWKS failure", err)
			}
			if calls != 1 {
				t.Fatalf("request count = %d, want 1", calls)
			}
			for _, secret := range []string{"transport-secret", "upstream-secret"} {
				if strings.Contains(err.Error(), secret) {
					t.Fatalf("error disclosed upstream content %q", secret)
				}
			}
		})
	}
}

func TestVerifyChatGPTProviderImportCandidates_EmptyBatchDoesNotFetchKeys(t *testing.T) {
	err, calls, _ := runProviderImportVerifier(
		t,
		time.Now(),
		http.StatusOK,
		`{"keys":[]}`,
		nil,
		zap.NewNop(),
		nil,
	)
	if err != nil || calls != 0 {
		t.Fatalf("empty batch = (%v, %d requests), want (nil, 0)", err, calls)
	}
}

func TestChatGPTProviderImportVerificationParsingEdges(t *testing.T) {
	t.Run("bounded compact segments reject empty and malformed data", func(t *testing.T) {
		for _, segment := range []string{"", "%%%"} {
			if _, err := decodeChatGPTProviderImportJWTSegment(segment, 8); err == nil {
				t.Fatalf("segment %q error = nil, want rejection", segment)
			}
		}
	})

	t.Run("protected header skips inert metadata but rejects ambiguity", func(t *testing.T) {
		header, err := decodeChatGPTProviderImportJOSEHeader([]byte(`{
			"alg":"RS256","kid":"key","jku":{"ignored":[1,2]}
		}`))
		if err != nil || header.Algorithm != "RS256" || header.KeyID != "key" {
			t.Fatalf("header = %#v, error = %v", header, err)
		}
		for _, raw := range []string{
			`[]`,
			`{"alg":"RS256","kid":"one","kid":"two"}`,
			`{"alg":"RS256","kid":"key"} {}`,
		} {
			if _, err := decodeChatGPTProviderImportJOSEHeader([]byte(raw)); err == nil {
				t.Fatalf("header %s error = nil, want rejection", raw)
			}
		}
	})

	t.Run("typed claims accept exact audience arrays and skip unknown claims", func(t *testing.T) {
		claims, err := decodeVerifiedChatGPTProviderImportClaims([]byte(`{
			"iss":"https://auth.openai.com",
			"aud":["https://api.openai.com/v1"],
			"exp":1785413400,
			"email":"user@example.com",
			"unknown":{"nested":[1,2,3]},
			"https://api.openai.com/auth":{
				"chatgpt_account_id":"acct-edge",
				"chatgpt_plan_type":"plus",
				"ignored":true
			}
		}`))
		if err != nil || !hasExactChatGPTProviderImportAudience(claims.Audience, chatGPTAPIAudience) ||
			claims.AccountID != "acct-edge" || claims.PlanType != "plus" {
			t.Fatalf("claims = %#v, error = %v", claims, err)
		}
		if hasExactChatGPTProviderImportAudience(nil, chatGPTAPIAudience) ||
			hasExactChatGPTProviderImportAudience([]string{"other"}, chatGPTAPIAudience) {
			t.Fatal("audience helper accepted an empty or foreign audience")
		}
	})

	t.Run("typed claims reject malformed critical fields", func(t *testing.T) {
		for _, raw := range []string{
			`[]`,
			`{}`,
			`{"iss":"https://auth.openai.com","aud":[],"exp":1,"https://api.openai.com/auth":{"chatgpt_account_id":"acct"}}`,
			`{"iss":"https://auth.openai.com","aud":"a","exp":"bad","https://api.openai.com/auth":{"chatgpt_account_id":"acct"}}`,
			`{"iss":"https://auth.openai.com","aud":"a","exp":1,"iat":"bad","https://api.openai.com/auth":{"chatgpt_account_id":"acct"}}`,
			`{"iss":"https://auth.openai.com","aud":"a","exp":1,"email":"a","email":"b","https://api.openai.com/auth":{"chatgpt_account_id":"acct"}}`,
			`{"iss":"https://auth.openai.com","aud":"a","exp":1,"https://api.openai.com/auth":[]}`,
			`{"iss":"https://auth.openai.com","aud":"a","exp":1,"https://api.openai.com/auth":{"chatgpt_account_id":"one","chatgpt_account_id":"two"}}`,
		} {
			if _, err := decodeVerifiedChatGPTProviderImportClaims([]byte(raw)); err == nil {
				t.Fatalf("claims %s error = nil, want rejection", raw)
			}
		}
	})

	t.Run("json object key helper rejects non-object tokens", func(t *testing.T) {
		decoder := json.NewDecoder(strings.NewReader(`[1]`))
		if _, err := readJSONObjectKey(decoder); err == nil {
			t.Fatal("readJSONObjectKey error = nil, want non-key rejection")
		}
	})
}

func TestVerifyChatGPTProviderImportCandidates_NilJWKSResponseIsUnavailable(t *testing.T) {
	service := NewService(Config{HTTPClient: stubHTTPDoer{do: func(*http.Request) (*http.Response, error) {
		return nil, nil
	}}})
	err := service.VerifyChatGPTProviderImportCandidates(context.Background(), []ChatGPTProviderImportCandidate{{CandidateID: "candidate"}})
	if !errors.Is(err, ErrChatGPTProviderImportJWKSUnavailable) {
		t.Fatalf("error = %v, want ErrChatGPTProviderImportJWKSUnavailable", err)
	}
}
