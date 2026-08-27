package providerauth

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/doraemonkeys/switch-a/internal/codex/credentialsession"
)

const (
	maxSub2APIImportAccounts        = 500
	maxReportedSub2APIProxies       = 1000
	maxSub2APIAccountNameCharacters = 200
	maxSub2APIProviderConcurrency   = 1_000_000
	maxSub2APIProviderPriority      = 1_000_000

	sub2APIPlatformOpenAI   = "openai"
	sub2APIAccountTypeOAuth = "oauth"
)

type sub2APIExportDocument struct {
	accounts   []json.RawMessage
	proxyCount boundedSub2APICount
}

type boundedSub2APICount struct {
	value  int
	capped bool
}

type sub2APIAccountDocument struct {
	Name               string          `json:"name"`
	Platform           string          `json:"platform"`
	Type               string          `json:"type"`
	Credentials        json.RawMessage `json:"credentials"`
	Extra              json.RawMessage `json:"extra"`
	Concurrency        json.RawMessage `json:"concurrency"`
	Priority           json.RawMessage `json:"priority"`
	RateMultiplier     json.RawMessage `json:"rate_multiplier"`
	AutoPauseOnExpired json.RawMessage `json:"auto_pause_on_expired"`
}

type sub2APIOAuthCredentials struct {
	AccessToken      string `json:"access_token"`
	RefreshToken     string `json:"refresh_token"`
	IDToken          string `json:"id_token"`
	ChatGPTAccountID string `json:"chatgpt_account_id"`
}

type sub2APIAccountRouting struct {
	name        string
	concurrency int
	priority    int
}

type sub2APIOAuthTokenSet struct {
	accessToken       string
	refreshToken      string
	idToken           string
	explicitAccountID string
}

type parsedSub2APIChatGPTImport struct {
	items      []ChatGPTProviderImportPreviewItem
	candidates []ChatGPTProviderImportCandidate
	warnings   []ChatGPTProviderImportWarning
}

func (s *Service) parseSub2APIChatGPTImport(raw []byte, now time.Time) (parsedSub2APIChatGPTImport, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 {
		return parsedSub2APIChatGPTImport{}, fmt.Errorf("%w: document is empty", ErrChatGPTProviderImportInvalidDocument)
	}

	document, err := decodeSub2APIExportDocument(trimmed)
	if err != nil {
		return parsedSub2APIChatGPTImport{}, err
	}
	accounts := document.accounts
	if len(accounts) == 0 {
		return parsedSub2APIChatGPTImport{}, fmt.Errorf("%w: accounts is empty", ErrChatGPTProviderImportInvalidDocument)
	}

	parsed := parsedSub2APIChatGPTImport{
		items:      make([]ChatGPTProviderImportPreviewItem, 0, len(accounts)),
		candidates: make([]ChatGPTProviderImportCandidate, 0, len(accounts)),
		warnings:   sub2APIGlobalImportWarnings(document.proxyCount, accounts),
	}
	seenCandidateIDs := make(map[string]struct{}, len(accounts))
	seenAccountIDs := make(map[string]string, len(accounts))
	for sourceIndex, rawAccount := range accounts {
		candidateID, err := s.generateOpaqueImportID(seenCandidateIDs)
		if err != nil {
			return parsedSub2APIChatGPTImport{}, err
		}
		seenCandidateIDs[candidateID] = struct{}{}

		item, candidate := parseSub2APIAccount(rawAccount, candidateID, sourceIndex, now)
		if candidate.State == ChatGPTProviderImportCandidateStateReady {
			accountID := strings.TrimSpace(string(candidate.Credential.Subject.Value))
			if earlierCandidateID, duplicate := seenAccountIDs[accountID]; duplicate {
				item.State = ChatGPTProviderImportCandidateStateDuplicate
				candidate.State = ChatGPTProviderImportCandidateStateDuplicate
				candidate.Credential = credentialsession.Snapshot{}
				warning := ChatGPTProviderImportWarning{
					Code: ChatGPTProviderImportWarningDuplicateAccount,
					Message: fmt.Sprintf(
						"Account duplicates candidate %s in this import.",
						earlierCandidateID,
					),
				}
				item.Warnings = append(item.Warnings, warning)
				candidate.Warnings = append(candidate.Warnings, warning)
			} else {
				seenAccountIDs[accountID] = candidateID
			}
		}
		parsed.items = append(parsed.items, item)
		parsed.candidates = append(parsed.candidates, candidate)
	}
	return parsed, nil
}

func parseSub2APIAccount(
	raw json.RawMessage,
	candidateID string,
	sourceIndex int,
	now time.Time,
) (ChatGPTProviderImportPreviewItem, ChatGPTProviderImportCandidate) {
	item := ChatGPTProviderImportPreviewItem{
		CandidateID: candidateID,
		SourceIndex: sourceIndex,
		State:       ChatGPTProviderImportCandidateStateInvalid,
		Warnings:    []ChatGPTProviderImportWarning{},
	}
	candidate := ChatGPTProviderImportCandidate{
		CandidateID: candidateID,
		SourceIndex: sourceIndex,
		State:       ChatGPTProviderImportCandidateStateInvalid,
		Warnings:    []ChatGPTProviderImportWarning{},
	}

	var account sub2APIAccountDocument
	if err := json.Unmarshal(raw, &account); err != nil {
		return invalidSub2APIAccount(item, candidate, "Account entry is not a valid object: "+err.Error())
	}

	routing, validationMessage := parseSub2APIAccountRouting(account)
	item.Name = routing.name
	item.Concurrency = routing.concurrency
	item.Priority = routing.priority
	candidate.Name = routing.name
	candidate.Concurrency = routing.concurrency
	candidate.Priority = routing.priority
	if validationMessage != "" {
		return invalidSub2APIAccount(item, candidate, validationMessage)
	}
	platform := strings.ToLower(strings.TrimSpace(account.Platform))
	accountType := strings.ToLower(strings.TrimSpace(account.Type))
	if platform != sub2APIPlatformOpenAI || accountType != sub2APIAccountTypeOAuth {
		item.State = ChatGPTProviderImportCandidateStateUnsupported
		candidate.State = ChatGPTProviderImportCandidateStateUnsupported
		warning := ChatGPTProviderImportWarning{
			Code:    ChatGPTProviderImportWarningUnsupportedAccount,
			Message: "Only openai/oauth accounts are supported; this account uses a different platform or type.",
		}
		item.Warnings = append(item.Warnings, warning)
		candidate.Warnings = append(candidate.Warnings, warning)
		return item, candidate
	}

	tokens, validationMessage := parseSub2APIOAuthTokenSet(account.Credentials)
	if validationMessage != "" {
		return invalidSub2APIAccount(item, candidate, validationMessage)
	}
	credential, err := newChatGPTCredentialFromImportedTokens(tokens.accessToken, tokens.refreshToken, tokens.idToken, now)
	if err != nil {
		return invalidSub2APIAccount(item, candidate, "Account token claims could not be accepted: "+err.Error())
	}
	if tokens.explicitAccountID != "" && tokens.explicitAccountID != credential.AccountID {
		warning := ChatGPTProviderImportWarning{
			Code:    ChatGPTProviderImportWarningAccountIDMismatch,
			Message: "The exported account ID does not match the account ID in the token claims.",
		}
		item.Warnings = append(item.Warnings, warning)
		candidate.Warnings = append(candidate.Warnings, warning)
		return item, candidate
	}

	usage, usageWarnings := parseSub2APIUsageSnapshot(account.Extra, credential.PlanType, now)
	credential.Usage = usage
	item.Warnings = append(item.Warnings, usageWarnings...)
	candidate.Warnings = append(candidate.Warnings, usageWarnings...)
	if !credential.ExpiresAt.IsZero() && !credential.ExpiresAt.After(now) {
		warning := ChatGPTProviderImportWarning{
			Code:    ChatGPTProviderImportWarningTokenExpired,
			Message: "The imported token is expired and will require a refresh before use.",
		}
		item.Warnings = append(item.Warnings, warning)
		candidate.Warnings = append(candidate.Warnings, warning)
	}

	snapshot, err := chatGPTCredentialSessionSnapshot(credential, "")
	if err != nil {
		return invalidSub2APIAccount(item, candidate, "Account credentials could not be staged: "+err.Error())
	}
	item.State = ChatGPTProviderImportCandidateStateReady
	item.Auth = BuildCredentialSessionAuthView(&snapshot)
	candidate.State = ChatGPTProviderImportCandidateStateReady
	candidate.Credential = snapshot
	return item, candidate
}

func parseSub2APIAccountRouting(account sub2APIAccountDocument) (sub2APIAccountRouting, string) {
	name, nameTruncated := boundedSub2APIAccountName(account.Name)
	routing := sub2APIAccountRouting{name: name}
	concurrency, err := optionalJSONInt(account.Concurrency)
	if err != nil || concurrency < 0 || concurrency > maxSub2APIProviderConcurrency {
		return routing, "Account concurrency must be between 0 and 1,000,000."
	}
	priority, err := optionalJSONInt(account.Priority)
	if err != nil || priority < 0 || priority > maxSub2APIProviderPriority {
		return routing, "Account priority must be between 0 and 1,000,000."
	}
	if name == "" {
		return routing, "Account name is required."
	}
	// Values beyond these limits cannot improve routing behavior and would make
	// review controls, logs, and persisted configuration needlessly expensive.
	if nameTruncated {
		return routing, "Account name must not exceed 200 characters."
	}
	routing.concurrency = concurrency
	routing.priority = priority
	return routing, ""
}

func parseSub2APIOAuthTokenSet(raw json.RawMessage) (sub2APIOAuthTokenSet, string) {
	if len(raw) == 0 {
		return sub2APIOAuthTokenSet{}, "Account credentials are required."
	}
	var credentials sub2APIOAuthCredentials
	if err := json.Unmarshal(raw, &credentials); err != nil {
		return sub2APIOAuthTokenSet{}, "Account credentials are invalid: " + err.Error()
	}
	tokens := sub2APIOAuthTokenSet{
		accessToken:       strings.TrimSpace(credentials.AccessToken),
		refreshToken:      strings.TrimSpace(credentials.RefreshToken),
		idToken:           strings.TrimSpace(credentials.IDToken),
		explicitAccountID: strings.TrimSpace(credentials.ChatGPTAccountID),
	}
	if tokens.accessToken == "" {
		return sub2APIOAuthTokenSet{}, "Account credentials are missing an access token."
	}
	if tokens.refreshToken == "" {
		return sub2APIOAuthTokenSet{}, "Account credentials are missing a refresh token."
	}
	if len(tokens.refreshToken) > maxChatGPTProviderImportRefreshTokenBytes {
		return sub2APIOAuthTokenSet{}, "Account refresh token exceeds the supported size limit."
	}
	if err := validateChatGPTProviderImportCompactJWS(tokens.accessToken); err != nil {
		return sub2APIOAuthTokenSet{}, "Account access token is not a supported compact JWS."
	}
	if tokens.idToken != "" {
		if err := validateChatGPTProviderImportCompactJWS(tokens.idToken); err != nil {
			return sub2APIOAuthTokenSet{}, "Account ID token is not a supported compact JWS."
		}
	}
	return tokens, ""
}

func validateChatGPTProviderImportCompactJWS(token string) error {
	if len(strings.TrimSpace(token)) > maxChatGPTProviderImportCompactJWSBytes {
		return ErrChatGPTProviderImportInvalidCandidate
	}
	header, payload, signature, err := splitCompactJWS(token)
	if err != nil {
		return err
	}
	for _, segment := range []struct {
		value        string
		maximumBytes int
	}{
		{value: header, maximumBytes: maxChatGPTProviderImportJWTHeaderBytes},
		{value: payload, maximumBytes: maxChatGPTProviderImportJWTPayloadBytes},
		{value: signature, maximumBytes: maxChatGPTProviderImportJWTSignatureBytes},
	} {
		if _, err := decodeChatGPTProviderImportJWTSegment(segment.value, segment.maximumBytes); err != nil {
			return err
		}
	}
	return nil
}

func boundedSub2APIAccountName(raw string) (string, bool) {
	trimmed := strings.TrimSpace(raw)
	characters := 0
	for byteIndex := range trimmed {
		if characters == maxSub2APIAccountNameCharacters {
			// Clone prevents a short preview name from retaining a multi-megabyte
			// attacker-controlled backing string after the source document is released.
			return strings.Clone(trimmed[:byteIndex]), true
		}
		characters++
	}
	return strings.Clone(trimmed), false
}

func invalidSub2APIAccount(
	item ChatGPTProviderImportPreviewItem,
	candidate ChatGPTProviderImportCandidate,
	message string,
) (ChatGPTProviderImportPreviewItem, ChatGPTProviderImportCandidate) {
	warning := ChatGPTProviderImportWarning{
		Code:    ChatGPTProviderImportWarningInvalidAccount,
		Message: message,
	}
	item.State = ChatGPTProviderImportCandidateStateInvalid
	candidate.State = ChatGPTProviderImportCandidateStateInvalid
	item.Warnings = append(item.Warnings, warning)
	candidate.Warnings = append(candidate.Warnings, warning)
	return item, candidate
}

func sub2APIGlobalImportWarnings(
	proxyCount boundedSub2APICount,
	accounts []json.RawMessage,
) []ChatGPTProviderImportWarning {
	warnings := make([]ChatGPTProviderImportWarning, 0, 3)
	if proxyCount.value > 0 {
		countDescription := fmt.Sprintf("%d definitions found", proxyCount.value)
		if proxyCount.capped {
			countDescription = fmt.Sprintf("at least %d definitions found", proxyCount.value)
		}
		warnings = append(warnings, ChatGPTProviderImportWarning{
			Code: ChatGPTProviderImportWarningProxiesIgnored,
			Message: fmt.Sprintf(
				"Proxy definitions and account proxy assignments are not imported (%s).",
				countDescription,
			),
		})
	}

	rateMultiplierCount := 0
	autoPauseCount := 0
	for _, rawAccount := range accounts {
		var account sub2APIAccountDocument
		if json.Unmarshal(rawAccount, &account) != nil {
			continue
		}
		if len(account.RateMultiplier) > 0 {
			rateMultiplierCount++
		}
		if len(account.AutoPauseOnExpired) > 0 {
			autoPauseCount++
		}
	}
	if rateMultiplierCount > 0 {
		warnings = append(warnings, ChatGPTProviderImportWarning{
			Code: ChatGPTProviderImportWarningRateMultiplierIgnored,
			Message: fmt.Sprintf(
				"rate_multiplier is a billing setting and is not imported (%d accounts).",
				rateMultiplierCount,
			),
		})
	}
	if autoPauseCount > 0 {
		warnings = append(warnings, ChatGPTProviderImportWarning{
			Code: ChatGPTProviderImportWarningAutoPauseIgnored,
			Message: fmt.Sprintf(
				"auto_pause_on_expired is not imported because switch-a refreshes OAuth credentials (%d accounts).",
				autoPauseCount,
			),
		})
	}
	return warnings
}

func decodeSub2APIExportDocument(raw []byte) (sub2APIExportDocument, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	opening, err := decoder.Token()
	if err != nil {
		return sub2APIExportDocument{}, invalidSub2APIDocumentCause("parse JSON", err)
	}
	if delimiter, ok := opening.(json.Delim); !ok || delimiter != '{' {
		return sub2APIExportDocument{}, fmt.Errorf("%w: document must be an object", ErrChatGPTProviderImportInvalidDocument)
	}

	var document sub2APIExportDocument
	seenAccounts := false
	seenProxies := false
	for decoder.More() {
		rawKey, keyErr := decoder.Token()
		if keyErr != nil {
			return sub2APIExportDocument{}, invalidSub2APIDocumentCause("parse object key", keyErr)
		}
		key, ok := rawKey.(string)
		if !ok {
			return sub2APIExportDocument{}, fmt.Errorf("%w: object key must be a string", ErrChatGPTProviderImportInvalidDocument)
		}
		switch key {
		case "accounts":
			if seenAccounts {
				return sub2APIExportDocument{}, fmt.Errorf("%w: accounts is duplicated", ErrChatGPTProviderImportInvalidDocument)
			}
			seenAccounts = true
			document.accounts, err = decodeSub2APIAccounts(decoder)
		case "proxies":
			if seenProxies {
				return sub2APIExportDocument{}, fmt.Errorf("%w: proxies is duplicated", ErrChatGPTProviderImportInvalidDocument)
			}
			seenProxies = true
			document.proxyCount, err = decodeBoundedSub2APIArrayCount(decoder, maxReportedSub2APIProxies)
		default:
			err = skipNextJSONValue(decoder)
		}
		if err != nil {
			return sub2APIExportDocument{}, err
		}
	}
	closing, err := decoder.Token()
	if err != nil {
		return sub2APIExportDocument{}, invalidSub2APIDocumentCause("parse document", err)
	}
	if delimiter, ok := closing.(json.Delim); !ok || delimiter != '}' {
		return sub2APIExportDocument{}, fmt.Errorf("%w: document object is not closed", ErrChatGPTProviderImportInvalidDocument)
	}
	if _, err := decoder.Token(); err != io.EOF {
		if err == nil {
			return sub2APIExportDocument{}, fmt.Errorf("%w: document has trailing data", ErrChatGPTProviderImportInvalidDocument)
		}
		return sub2APIExportDocument{}, invalidSub2APIDocumentCause("parse trailing data", err)
	}
	if !seenAccounts {
		return sub2APIExportDocument{}, fmt.Errorf("%w: accounts is required", ErrChatGPTProviderImportInvalidDocument)
	}
	return document, nil
}

func decodeSub2APIAccounts(decoder *json.Decoder) ([]json.RawMessage, error) {
	opening, err := decoder.Token()
	if err != nil {
		return nil, invalidSub2APIDocumentCause("parse accounts", err)
	}
	if delimiter, ok := opening.(json.Delim); !ok || delimiter != '[' {
		return nil, fmt.Errorf("%w: accounts must be an array", ErrChatGPTProviderImportInvalidDocument)
	}

	accounts := make([]json.RawMessage, 0, maxSub2APIImportAccounts)
	for decoder.More() {
		if len(accounts) == maxSub2APIImportAccounts {
			return nil, fmt.Errorf(
				"%w: accounts exceeds limit %d",
				ErrChatGPTProviderImportInvalidDocument,
				maxSub2APIImportAccounts,
			)
		}
		var account json.RawMessage
		if err := decoder.Decode(&account); err != nil {
			return nil, invalidSub2APIDocumentCause("parse account", err)
		}
		accounts = append(accounts, account)
	}
	if _, err := decoder.Token(); err != nil {
		return nil, invalidSub2APIDocumentCause("close accounts array", err)
	}
	return accounts, nil
}

func decodeBoundedSub2APIArrayCount(decoder *json.Decoder, limit int) (boundedSub2APICount, error) {
	opening, err := decoder.Token()
	if err != nil {
		return boundedSub2APICount{}, invalidSub2APIDocumentCause("parse proxies", err)
	}
	delimiter, isDelimiter := opening.(json.Delim)
	if !isDelimiter || delimiter != '[' {
		if err := skipJSONValueAfterToken(decoder, opening); err != nil {
			return boundedSub2APICount{}, err
		}
		return boundedSub2APICount{}, nil
	}

	var count boundedSub2APICount
	for decoder.More() {
		if count.value < limit {
			count.value++
		} else {
			count.capped = true
		}
		if err := skipNextJSONValue(decoder); err != nil {
			return boundedSub2APICount{}, err
		}
	}
	if _, err := decoder.Token(); err != nil {
		return boundedSub2APICount{}, invalidSub2APIDocumentCause("close proxies array", err)
	}
	return count, nil
}

func skipNextJSONValue(decoder *json.Decoder) error {
	first, err := decoder.Token()
	if err != nil {
		return invalidSub2APIDocumentCause("parse value", err)
	}
	return skipJSONValueAfterToken(decoder, first)
}

func skipJSONValueAfterToken(decoder *json.Decoder, first json.Token) error {
	delimiter, ok := first.(json.Delim)
	if !ok || (delimiter != '{' && delimiter != '[') {
		return nil
	}
	depth := 1
	for depth > 0 {
		token, err := decoder.Token()
		if err != nil {
			return invalidSub2APIDocumentCause("parse nested value", err)
		}
		if nested, ok := token.(json.Delim); ok {
			switch nested {
			case '{', '[':
				depth++
			case '}', ']':
				depth--
			}
		}
	}
	return nil
}

func invalidSub2APIDocumentCause(operation string, cause error) error {
	// The public sentinel drives the admin response, while the wrapped decoder
	// cause remains available for errors.As diagnostics and focused tests.
	return errors.Join(
		ErrChatGPTProviderImportInvalidDocument,
		fmt.Errorf("%s: %w", operation, cause),
	)
}

func optionalJSONInt(raw json.RawMessage) (int, error) {
	if len(raw) == 0 || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return 0, nil
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var number json.Number
	if err := decoder.Decode(&number); err != nil {
		return 0, err
	}
	value, err := number.Int64()
	if err != nil {
		return 0, err
	}
	if value < minimumJSONSafeInteger || value > maximumJSONSafeInteger {
		return 0, fmt.Errorf("integer exceeds JSON safe range")
	}
	if strconv.IntSize == 32 && (value > math.MaxInt32 || value < math.MinInt32) {
		return 0, fmt.Errorf("integer out of range")
	}
	return int(value), nil
}

func summarizeChatGPTProviderImportItems(items []ChatGPTProviderImportPreviewItem) ChatGPTProviderImportSummary {
	summary := ChatGPTProviderImportSummary{Total: len(items)}
	for _, item := range items {
		switch item.State {
		case ChatGPTProviderImportCandidateStateReady:
			summary.Ready++
		case ChatGPTProviderImportCandidateStateExisting:
			summary.Existing++
		case ChatGPTProviderImportCandidateStateDuplicate:
			summary.Duplicate++
		case ChatGPTProviderImportCandidateStateInvalid:
			summary.Invalid++
		case ChatGPTProviderImportCandidateStateUnsupported:
			summary.Unsupported++
		}
	}
	return summary
}
