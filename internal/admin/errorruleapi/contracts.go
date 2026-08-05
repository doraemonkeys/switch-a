package errorruleapi

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/doraemonkeys/switch-a/internal/apicontract"
	"github.com/doraemonkeys/switch-a/internal/errorrule"
	"github.com/doraemonkeys/switch-a/internal/model"
	"github.com/doraemonkeys/switch-a/internal/responseanalysis"
)

const (
	SchemaVersion = 1

	MaxRuleMutationRequestBytes = 64 * 1024
	MaxRuleReorderRequestBytes  = 32 * 1024
	MaxTestMessageRequestBytes  = responseanalysis.MaxTestMessageRequestBytes
	MaxTestMessageWireBodyBytes = responseanalysis.MaxTestMessageWireBodyBytes
)

type mutationRequest struct {
	SchemaVersion *int             `json:"schema_version"`
	Rule          *ruleSpecRequest `json:"rule"`
}

func (r mutationRequest) domainRule() (errorrule.RuleSpec, *apiError) {
	if err := validateSchemaVersion(r.SchemaVersion); err != nil {
		return errorrule.RuleSpec{}, err
	}
	if r.Rule == nil {
		return errorrule.RuleSpec{}, validationError("rule", "rule is required", nil)
	}
	return r.Rule.domainRule("rule")
}

type reorderRequest struct {
	SchemaVersion  *int                `json:"schema_version"`
	OrderedRuleIDs *[]errorrule.RuleID `json:"ordered_rule_ids"`
}

func (r reorderRequest) ruleIDs() ([]errorrule.RuleID, *apiError) {
	if err := validateSchemaVersion(r.SchemaVersion); err != nil {
		return nil, err
	}
	if r.OrderedRuleIDs == nil {
		return nil, validationError("ordered_rule_ids", "ordered_rule_ids is required", nil)
	}
	ids := append([]errorrule.RuleID(nil), (*r.OrderedRuleIDs)...)
	seen := make(map[errorrule.RuleID]struct{}, len(ids))
	for index, id := range ids {
		field := fmt.Sprintf("ordered_rule_ids[%d]", index)
		if err := id.Validate(); err != nil {
			return nil, validationError(field, field+" must be a lowercase canonical UUIDv4", err)
		}
		if _, duplicate := seen[id]; duplicate {
			return nil, validationError(field, "ordered_rule_ids must not contain duplicates", nil)
		}
		seen[id] = struct{}{}
	}
	return ids, nil
}

type ruleSpecRequest struct {
	Name      *string         `json:"name"`
	Enabled   *bool           `json:"enabled"`
	Target    *targetRequest  `json:"target"`
	APIType   json.RawMessage `json:"api_type"`
	Keywords  *[]string       `json:"keywords"`
	MatchMode *string         `json:"match_mode"`
	Action    *actionRequest  `json:"action"`
}

func (r ruleSpecRequest) domainRule(prefix string) (errorrule.RuleSpec, *apiError) {
	if r.Name == nil {
		return errorrule.RuleSpec{}, requiredField(prefix + ".name")
	}
	if r.Enabled == nil {
		return errorrule.RuleSpec{}, requiredField(prefix + ".enabled")
	}
	if r.Target == nil {
		return errorrule.RuleSpec{}, requiredField(prefix + ".target")
	}
	if len(r.APIType) == 0 {
		return errorrule.RuleSpec{}, requiredField(prefix + ".api_type")
	}
	if r.Keywords == nil {
		return errorrule.RuleSpec{}, requiredField(prefix + ".keywords")
	}
	if r.MatchMode == nil {
		return errorrule.RuleSpec{}, requiredField(prefix + ".match_mode")
	}
	if r.Action == nil {
		return errorrule.RuleSpec{}, requiredField(prefix + ".action")
	}

	target, apiErr := r.Target.domainTarget(prefix + ".target")
	if apiErr != nil {
		return errorrule.RuleSpec{}, apiErr
	}
	apiType, apiErr := parseNullableAPIType(r.APIType, prefix+".api_type")
	if apiErr != nil {
		return errorrule.RuleSpec{}, apiErr
	}
	action, apiErr := r.Action.domainAction(prefix + ".action")
	if apiErr != nil {
		return errorrule.RuleSpec{}, apiErr
	}
	spec := errorrule.RuleSpec{
		Name:      *r.Name,
		Enabled:   *r.Enabled,
		Target:    target,
		APIType:   apiType,
		Keywords:  append([]string(nil), (*r.Keywords)...),
		MatchMode: errorrule.MatchMode(*r.MatchMode),
		Action:    action,
	}
	normalized, err := errorrule.NormalizeRuleSpec(spec)
	if err != nil {
		field := domainRuleErrorField(prefix, err)
		return errorrule.RuleSpec{}, validationError(field, err.Error(), err)
	}
	return normalized, nil
}

type targetRequest struct {
	Kind       *string         `json:"kind"`
	ProviderID json.RawMessage `json:"provider_id"`
}

func (r targetRequest) domainTarget(prefix string) (errorrule.Target, *apiError) {
	if r.Kind == nil {
		return errorrule.Target{}, requiredField(prefix + ".kind")
	}
	switch errorrule.TargetKind(*r.Kind) {
	case errorrule.TargetGlobal:
		if len(r.ProviderID) != 0 {
			return errorrule.Target{}, validationError(
				prefix+".provider_id", prefix+" contains fields outside its discriminator", nil,
			)
		}
		return errorrule.NewGlobalTarget(), nil
	case errorrule.TargetProvider:
		if len(r.ProviderID) == 0 {
			return errorrule.Target{}, requiredField(prefix + ".provider_id")
		}
		var providerID string
		if apiErr := decodeUnionField(r.ProviderID, prefix+".provider_id", &providerID); apiErr != nil {
			return errorrule.Target{}, apiErr
		}
		target, err := errorrule.NewProviderTarget(errorrule.ProviderID(providerID))
		if err != nil {
			return errorrule.Target{}, validationError(prefix+".provider_id", err.Error(), err)
		}
		return target, nil
	default:
		return errorrule.Target{}, validationError(prefix+".kind", "unknown rule target kind", nil)
	}
}

type actionRequest struct {
	Type          *string                          `json:"type"`
	MaxRetries    json.RawMessage                  `json:"max_retries"`
	Backoff       json.RawMessage                  `json:"backoff"`
	VisiblePolicy *errorrule.VisibleResponsePolicy `json:"visible_response"`
}

func (r actionRequest) domainAction(prefix string) (errorrule.Action, *apiError) {
	if r.Type == nil {
		return errorrule.Action{}, requiredField(prefix + ".type")
	}
	switch errorrule.ActionType(*r.Type) {
	case errorrule.ActionPassthrough:
		if len(r.MaxRetries) != 0 {
			return errorrule.Action{}, validationError(
				prefix+".max_retries", prefix+" contains fields outside its discriminator", nil,
			)
		}
		if len(r.Backoff) != 0 {
			return errorrule.Action{}, validationError(
				prefix+".backoff", prefix+" contains fields outside its discriminator", nil,
			)
		}
		if r.VisiblePolicy != nil {
			return errorrule.Action{}, validationError(
				prefix+".visible_response", prefix+" contains fields outside its discriminator", nil,
			)
		}
		return errorrule.NewPassthroughAction(), nil
	case errorrule.ActionRetryOnly, errorrule.ActionRetryThenSwitch:
		if len(r.MaxRetries) == 0 {
			return errorrule.Action{}, requiredField(prefix + ".max_retries")
		}
		if len(r.Backoff) == 0 {
			return errorrule.Action{}, requiredField(prefix + ".backoff")
		}
		var maxRetries int
		if apiErr := decodeUnionField(r.MaxRetries, prefix+".max_retries", &maxRetries); apiErr != nil {
			return errorrule.Action{}, apiErr
		}
		var backoffRequest backoffRequest
		if apiErr := decodeUnionField(r.Backoff, prefix+".backoff", &backoffRequest); apiErr != nil {
			return errorrule.Action{}, apiErr
		}
		backoff, apiErr := backoffRequest.domainBackoff(prefix + ".backoff")
		if apiErr != nil {
			return errorrule.Action{}, apiErr
		}
		var (
			action errorrule.Action
			err    error
		)
		visiblePolicy := errorrule.VisibleResponseDisconnect
		if r.VisiblePolicy != nil {
			visiblePolicy = *r.VisiblePolicy
		}
		if errorrule.ActionType(*r.Type) == errorrule.ActionRetryOnly {
			action, err = errorrule.NewRetryOnlyActionWithVisibleResponse(maxRetries, backoff, visiblePolicy)
		} else {
			action, err = errorrule.NewRetryThenSwitchActionWithVisibleResponse(maxRetries, backoff, visiblePolicy)
		}
		if err != nil {
			return errorrule.Action{}, validationError(domainActionErrorField(prefix, err), err.Error(), err)
		}
		return action, nil
	default:
		return errorrule.Action{}, validationError(prefix+".type", "unknown rule action type", nil)
	}
}

func decodeUnionField(raw json.RawMessage, field string, target any) *apiError {
	if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return validationError(field, field+" must not be null", nil)
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return validationError(field, field+" has an invalid JSON value", err)
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return validationError(field, field+" must contain exactly one JSON value", err)
	}
	return nil
}

type backoffRequest struct {
	InitialDelay *model.Duration `json:"initial_delay"`
	MaxDelay     *model.Duration `json:"max_delay"`
	Multiplier   *float64        `json:"multiplier"`
	Jitter       *bool           `json:"jitter"`
}

func (r backoffRequest) domainBackoff(prefix string) (model.BackoffPolicy, *apiError) {
	if r.InitialDelay == nil {
		return model.BackoffPolicy{}, requiredField(prefix + ".initial_delay")
	}
	if r.MaxDelay == nil {
		return model.BackoffPolicy{}, requiredField(prefix + ".max_delay")
	}
	if r.Multiplier == nil {
		return model.BackoffPolicy{}, requiredField(prefix + ".multiplier")
	}
	if r.Jitter == nil {
		return model.BackoffPolicy{}, requiredField(prefix + ".jitter")
	}
	return model.BackoffPolicy{
		InitialDelay: *r.InitialDelay,
		MaxDelay:     *r.MaxDelay,
		Multiplier:   *r.Multiplier,
		Jitter:       *r.Jitter,
	}, nil
}

func validateSchemaVersion(version *int) *apiError {
	if version == nil {
		return requiredField("schema_version")
	}
	if *version != SchemaVersion {
		return validationError("schema_version", "schema_version must equal 1", nil)
	}
	return nil
}

func requiredField(field string) *apiError {
	return validationError(field, field+" is required", nil)
}

func parseNullableAPIType(raw json.RawMessage, field string) (*apicontract.APIType, *apiError) {
	if bytes.Equal(raw, []byte("null")) {
		return nil, nil
	}
	var value string
	if err := json.Unmarshal(raw, &value); err != nil {
		return nil, validationError(field, field+" must be a string or null", err)
	}
	apiType := apicontract.APIType(value)
	return &apiType, nil
}

func domainRuleErrorField(prefix string, err error) string {
	message := err.Error()
	switch {
	case strings.Contains(message, "rule name"):
		return prefix + ".name"
	case strings.Contains(message, "provider_id"), strings.Contains(message, "target"):
		return prefix + ".target"
	case strings.Contains(message, "API type"):
		return prefix + ".api_type"
	case strings.Contains(message, "keyword"):
		return prefix + ".keywords"
	case strings.Contains(message, "match_mode"):
		return prefix + ".match_mode"
	case strings.Contains(message, "action"):
		return prefix + ".action"
	default:
		return prefix
	}
}

func domainActionErrorField(prefix string, err error) string {
	message := err.Error()
	switch {
	case strings.Contains(message, "max_retries"):
		return prefix + ".max_retries"
	case strings.Contains(message, "initial_delay"):
		return prefix + ".backoff.initial_delay"
	case strings.Contains(message, "max_delay"):
		return prefix + ".backoff.max_delay"
	case strings.Contains(message, "multiplier"):
		return prefix + ".backoff.multiplier"
	default:
		return prefix
	}
}

type testMessageRequest struct {
	SchemaVersion   *int             `json:"schema_version"`
	APIType         *string          `json:"api_type"`
	ProviderID      json.RawMessage  `json:"provider_id"`
	ContentType     *string          `json:"content_type"`
	ContentEncoding *string          `json:"content_encoding"`
	Body            *messageBodyWire `json:"body"`
}

type messageBodyWire struct {
	Encoding *string `json:"encoding"`
	Value    *string `json:"value"`
}

func (r testMessageRequest) input() (TestMessageInput, *apiError) {
	if err := validateSchemaVersion(r.SchemaVersion); err != nil {
		return TestMessageInput{}, err
	}
	if r.APIType == nil {
		return TestMessageInput{}, requiredField("api_type")
	}
	definition, supported := apicontract.Lookup(*r.APIType)
	if !supported || !definition.SemanticErrorSupported {
		return TestMessageInput{}, validationError("api_type", "api_type must be a supported built-in API type", nil)
	}
	providerID, apiErr := parseNullableProviderID(r.ProviderID)
	if apiErr != nil {
		return TestMessageInput{}, apiErr
	}
	if r.ContentType == nil {
		return TestMessageInput{}, requiredField("content_type")
	}
	if r.ContentEncoding == nil {
		return TestMessageInput{}, requiredField("content_encoding")
	}
	if r.Body == nil {
		return TestMessageInput{}, requiredField("body")
	}
	body, apiErr := r.Body.decode()
	if apiErr != nil {
		return TestMessageInput{}, apiErr
	}
	return TestMessageInput{
		APIType:         definition.APIType,
		ProviderID:      providerID,
		ContentType:     *r.ContentType,
		ContentEncoding: *r.ContentEncoding,
		Body:            body,
	}, nil
}

func parseNullableProviderID(raw json.RawMessage) (*string, *apiError) {
	if len(raw) == 0 {
		return nil, requiredField("provider_id")
	}
	if bytes.Equal(raw, []byte("null")) {
		return nil, nil
	}
	var providerID string
	if err := json.Unmarshal(raw, &providerID); err != nil {
		return nil, validationError("provider_id", "provider_id must be a string or null", err)
	}
	if providerID == "" || strings.TrimSpace(providerID) != providerID {
		return nil, validationError("provider_id", "provider_id must be non-empty without surrounding whitespace", nil)
	}
	return &providerID, nil
}

func (r messageBodyWire) decode() ([]byte, *apiError) {
	if r.Encoding == nil {
		return nil, requiredField("body.encoding")
	}
	if r.Value == nil {
		return nil, requiredField("body.value")
	}
	var body []byte
	switch *r.Encoding {
	case "utf8":
		body = []byte(*r.Value)
	case "base64":
		decoded, err := base64.StdEncoding.Strict().DecodeString(*r.Value)
		if err != nil || base64.StdEncoding.EncodeToString(decoded) != *r.Value {
			return nil, validationError("body.value", "body.value must be canonical padded base64", err)
		}
		body = decoded
	default:
		return nil, validationError("body.encoding", "body.encoding must be utf8 or base64", nil)
	}
	if len(body) > MaxTestMessageWireBodyBytes {
		return nil, requestTooLargeError(MaxTestMessageWireBodyBytes)
	}
	return body, nil
}

type RuleListResponse struct {
	SchemaVersion   int        `json:"schema_version"`
	RuleSetRevision string     `json:"rule_set_revision"`
	Rules           []RuleWire `json:"rules"`
}

type RuleResponse struct {
	SchemaVersion   int      `json:"schema_version"`
	RuleSetRevision string   `json:"rule_set_revision"`
	Rule            RuleWire `json:"rule"`
}

type RuleWire struct {
	ID        errorrule.RuleID     `json:"id"`
	Name      string               `json:"name"`
	Enabled   bool                 `json:"enabled"`
	Target    errorrule.Target     `json:"target"`
	APIType   *apicontract.APIType `json:"api_type"`
	Keywords  []string             `json:"keywords"`
	MatchMode errorrule.MatchMode  `json:"match_mode"`
	Action    ActionWire           `json:"action"`
	Position  int64                `json:"position"`
	CreatedAt time.Time            `json:"created_at"`
	UpdatedAt time.Time            `json:"updated_at"`
}

type ActionWire struct {
	Type          errorrule.ActionType             `json:"type"`
	MaxRetries    *int                             `json:"max_retries,omitempty"`
	Backoff       *BackoffWire                     `json:"backoff,omitempty"`
	VisiblePolicy *errorrule.VisibleResponsePolicy `json:"visible_response,omitempty"`
}

type BackoffWire struct {
	InitialDelay model.Duration `json:"initial_delay"`
	MaxDelay     model.Duration `json:"max_delay"`
	Multiplier   float64        `json:"multiplier"`
	Jitter       bool           `json:"jitter"`
}

func newRuleListResponse(revision errorrule.Revision, rules []errorrule.Rule) RuleListResponse {
	response := RuleListResponse{
		SchemaVersion: SchemaVersion, RuleSetRevision: revision.String(), Rules: make([]RuleWire, len(rules)),
	}
	for index, rule := range rules {
		response.Rules[index] = newRuleWire(rule)
	}
	return response
}

func newRuleResponse(revision errorrule.Revision, rule errorrule.Rule) RuleResponse {
	return RuleResponse{SchemaVersion: SchemaVersion, RuleSetRevision: revision.String(), Rule: newRuleWire(rule)}
}

func newRuleWire(rule errorrule.Rule) RuleWire {
	return RuleWire{
		ID: rule.ID, Name: rule.Name, Enabled: rule.Enabled, Target: rule.Target,
		APIType: cloneAPIType(rule.APIType), Keywords: append([]string(nil), rule.Keywords...),
		MatchMode: rule.MatchMode, Action: newActionWire(rule.Action), Position: rule.Position,
		CreatedAt: rule.CreatedAt.UTC(), UpdatedAt: rule.UpdatedAt.UTC(),
	}
}

func cloneAPIType(value *apicontract.APIType) *apicontract.APIType {
	if value == nil {
		return nil
	}
	clone := *value
	return &clone
}

func newActionWire(action errorrule.Action) ActionWire {
	wire := ActionWire{Type: action.Type()}
	if retry, ok := action.RetryPolicy(); ok {
		maxRetries := retry.MaxRetries
		wire.MaxRetries = &maxRetries
		wire.Backoff = &BackoffWire{
			InitialDelay: retry.Backoff.InitialDelay,
			MaxDelay:     retry.Backoff.MaxDelay,
			Multiplier:   retry.Backoff.Multiplier,
			Jitter:       retry.Backoff.Jitter,
		}
		if action.VisibleResponsePolicy() == errorrule.VisibleResponseCommit {
			policy := errorrule.VisibleResponseCommit
			wire.VisiblePolicy = &policy
		}
	}
	return wire
}

type RuleStatsResponse struct {
	SchemaVersion   int             `json:"schema_version"`
	RuleSetRevision string          `json:"rule_set_revision"`
	Stats           []RuleStatsWire `json:"stats"`
}

type RuleStatsWire struct {
	RuleID    errorrule.RuleID `json:"rule_id"`
	HitCount  string           `json:"hit_count"`
	LastHitAt *time.Time       `json:"last_hit_at"`
}

func newStatsResponse(revision errorrule.Revision, stats []errorrule.RuleStats) RuleStatsResponse {
	response := RuleStatsResponse{
		SchemaVersion: SchemaVersion, RuleSetRevision: revision.String(), Stats: make([]RuleStatsWire, len(stats)),
	}
	for index, item := range stats {
		response.Stats[index] = RuleStatsWire{
			RuleID: item.RuleID, HitCount: strconv.FormatUint(item.HitCount, 10), LastHitAt: utcTime(item.LastHitAt),
		}
	}
	return response
}

func utcTime(value *time.Time) *time.Time {
	if value == nil {
		return nil
	}
	utc := value.UTC()
	return &utc
}

type TestMessageResponse struct {
	SchemaVersion      int                                     `json:"schema_version"`
	RuleSetRevision    string                                  `json:"rule_set_revision"`
	ResponseProtocolID *apicontract.ResponseProtocolID         `json:"response_protocol_id"`
	AnalysisStatus     string                                  `json:"analysis_status"`
	AnalysisReason     *responseanalysis.AnalysisFailureReason `json:"analysis_reason"`
	Errors             []TestMessageError                      `json:"errors"`
	DecisiveErrorIndex *int                                    `json:"decisive_error_index"`
	Winner             *TestMessageWinner                      `json:"winner"`
}

type TestMessageError struct {
	FrameIndex int                `json:"frame_index"`
	Type       *string            `json:"type,omitempty"`
	Code       *string            `json:"code,omitempty"`
	Message    *string            `json:"message,omitempty"`
	Reason     *string            `json:"reason,omitempty"`
	Matches    []TestMessageMatch `json:"matches"`
}

type TestMessageMatch struct {
	RuleID                errorrule.RuleID          `json:"rule_id"`
	MatchedKeywords       []string                  `json:"matched_keywords"`
	MatchedKeywordIndexes []int                     `json:"matched_keyword_indexes"`
	MatchedFields         []errorrule.SemanticField `json:"matched_fields"`
}

type TestMessageWinner struct {
	ErrorIndex int `json:"error_index"`
	TestMessageMatch
}

func cloneProtocolID(value *apicontract.ResponseProtocolID) *apicontract.ResponseProtocolID {
	if value == nil {
		return nil
	}
	clone := *value
	return &clone
}
