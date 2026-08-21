package sqlite

import (
	"fmt"
	"strings"

	"github.com/doraemonkeys/switch-a/internal/apicontract"
	"github.com/doraemonkeys/switch-a/internal/instant"
	"github.com/doraemonkeys/switch-a/internal/tokenanalytics"
)

const (
	classWithoutUsage    = "without_usage"
	classInvalid         = "invalid"
	classUnknownSemantic = "unknown_semantics"
	classPartial         = "partial"
	classComparable      = "comparable"

	createdAtUnixNanoIndex         = "idx_request_logs_created_at_unix_nano"
	providerCreatedAtUnixNanoIndex = "idx_request_logs_provider_created_at_unix_nano"
	modelCreatedAtUnixNanoIndex    = "idx_request_logs_model_created_at_unix_nano"
	apiTypeCreatedAtUnixNanoIndex  = "idx_request_logs_api_type_created_at_unix_nano"
)

const projectionSQLTemplate = `
WITH
semantic_contract(api_type, token_semantics) AS (
	VALUES %s
),
projection_contract(
	unknown_semantics,
	anthropic_semantics,
	openai_semantics,
	google_semantics,
	without_usage_class,
	invalid_class,
	unknown_semantics_class,
	partial_class,
	comparable_class
) AS (
	VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
),
filtered AS (
	SELECT
		rl.provider_id,
		rl.api_type,
		rl.model,
		rl.created_at_unix_nano,
		rl.prompt_tokens,
		rl.completion_tokens,
		rl.total_tokens,
		rl.reasoning_tokens,
		rl.cache_read_input_tokens,
		rl.cache_creation_input_tokens,
		COALESCE(sc.token_semantics, pc.unknown_semantics) AS token_semantics
	FROM request_logs AS rl INDEXED BY %s
	CROSS JOIN projection_contract AS pc
	LEFT JOIN semantic_contract AS sc ON sc.api_type = rl.api_type
	WHERE %s
),
validated AS (
	SELECT
		filtered.*,
		(prompt_tokens IS NOT NULL OR completion_tokens IS NOT NULL OR total_tokens IS NOT NULL) AS observed,
		CASE
			WHEN
				(prompt_tokens IS NOT NULL AND prompt_tokens < 0) OR
				(completion_tokens IS NOT NULL AND completion_tokens < 0) OR
				(total_tokens IS NOT NULL AND total_tokens < 0) OR
				(reasoning_tokens IS NOT NULL AND reasoning_tokens < 0) OR
				(cache_read_input_tokens IS NOT NULL AND cache_read_input_tokens < 0) OR
				(cache_creation_input_tokens IS NOT NULL AND cache_creation_input_tokens < 0)
				THEN 1
			WHEN token_semantics = pc.anthropic_semantics
				AND total_tokens IS NOT NULL
				AND prompt_tokens IS NOT NULL
				AND completion_tokens IS NOT NULL
				AND total_tokens != prompt_tokens + completion_tokens
				THEN 1
			WHEN token_semantics = pc.openai_semantics
				AND (
					(total_tokens IS NOT NULL AND prompt_tokens IS NOT NULL AND completion_tokens IS NOT NULL
						AND total_tokens != prompt_tokens + completion_tokens) OR
					(prompt_tokens IS NOT NULL AND cache_read_input_tokens IS NOT NULL
						AND cache_read_input_tokens > prompt_tokens) OR
					(prompt_tokens IS NOT NULL AND cache_creation_input_tokens IS NOT NULL
						AND cache_creation_input_tokens > prompt_tokens) OR
					(prompt_tokens IS NOT NULL AND cache_read_input_tokens IS NOT NULL
						AND cache_creation_input_tokens IS NOT NULL
						AND cache_read_input_tokens + cache_creation_input_tokens > prompt_tokens) OR
					(completion_tokens IS NOT NULL AND reasoning_tokens IS NOT NULL
						AND reasoning_tokens > completion_tokens)
				)
				THEN 1
			WHEN token_semantics = pc.google_semantics
				AND (
					(total_tokens IS NOT NULL AND total_tokens > 0 AND prompt_tokens IS NOT NULL
						AND total_tokens < prompt_tokens) OR
					(total_tokens IS NOT NULL AND total_tokens > 0
						AND prompt_tokens IS NOT NULL AND completion_tokens IS NOT NULL
						AND total_tokens < prompt_tokens + completion_tokens) OR
					(prompt_tokens IS NOT NULL AND cache_read_input_tokens IS NOT NULL
						AND cache_read_input_tokens > prompt_tokens)
				)
				THEN 1
			ELSE 0
		END AS invalid
	FROM filtered
	CROSS JOIN projection_contract AS pc
),
canonical AS (
	SELECT
		validated.*,
		CASE
			WHEN token_semantics = pc.anthropic_semantics
				AND prompt_tokens IS NOT NULL
				AND cache_read_input_tokens IS NOT NULL
				AND cache_creation_input_tokens IS NOT NULL
				THEN prompt_tokens + cache_read_input_tokens + cache_creation_input_tokens
			WHEN token_semantics IN (pc.openai_semantics, pc.google_semantics) AND prompt_tokens IS NOT NULL
				THEN prompt_tokens
		END AS canonical_input,
		CASE
			WHEN token_semantics IN (pc.anthropic_semantics, pc.openai_semantics) AND completion_tokens IS NOT NULL
				THEN completion_tokens
			WHEN token_semantics = pc.google_semantics
				AND total_tokens IS NOT NULL AND total_tokens > 0
				AND prompt_tokens IS NOT NULL
				THEN total_tokens - prompt_tokens
			WHEN token_semantics = pc.google_semantics
				AND (total_tokens IS NULL OR total_tokens = 0)
				AND completion_tokens IS NOT NULL
				THEN completion_tokens
		END AS canonical_output,
		CASE
			WHEN token_semantics = pc.anthropic_semantics
				AND prompt_tokens IS NOT NULL
				AND completion_tokens IS NOT NULL
				AND cache_read_input_tokens IS NOT NULL
				AND cache_creation_input_tokens IS NOT NULL
				THEN prompt_tokens + cache_read_input_tokens + cache_creation_input_tokens + completion_tokens
			WHEN token_semantics = pc.openai_semantics
				AND prompt_tokens IS NOT NULL AND completion_tokens IS NOT NULL
				THEN COALESCE(total_tokens, prompt_tokens + completion_tokens)
			WHEN token_semantics = pc.google_semantics
				AND total_tokens IS NOT NULL AND total_tokens > 0
				AND prompt_tokens IS NOT NULL
				THEN total_tokens
			WHEN token_semantics = pc.google_semantics
				AND (total_tokens IS NULL OR total_tokens = 0)
				AND prompt_tokens IS NOT NULL AND completion_tokens IS NOT NULL
				THEN prompt_tokens + completion_tokens
		END AS canonical_total,
		CASE
			-- A missing cache counter makes the Anthropic canonical input partial,
			-- so attributing its prompt component would violate row-level conservation.
			WHEN token_semantics = pc.anthropic_semantics
				AND prompt_tokens IS NOT NULL
				AND cache_read_input_tokens IS NOT NULL
				AND cache_creation_input_tokens IS NOT NULL
				THEN prompt_tokens
			WHEN token_semantics = pc.openai_semantics
				AND prompt_tokens IS NOT NULL
				AND cache_read_input_tokens IS NOT NULL
				AND cache_creation_input_tokens IS NOT NULL
				THEN prompt_tokens - cache_read_input_tokens - cache_creation_input_tokens
			WHEN token_semantics = pc.google_semantics
				AND prompt_tokens IS NOT NULL
				AND cache_read_input_tokens IS NOT NULL
				THEN prompt_tokens - cache_read_input_tokens
			ELSE 0
		END AS fresh_input,
		CASE
			WHEN token_semantics = pc.anthropic_semantics
				AND cache_read_input_tokens IS NOT NULL
				THEN cache_read_input_tokens
			WHEN token_semantics = pc.openai_semantics
				AND prompt_tokens IS NOT NULL
				AND cache_read_input_tokens IS NOT NULL
				AND cache_creation_input_tokens IS NOT NULL
				THEN cache_read_input_tokens
			WHEN token_semantics = pc.google_semantics
				AND prompt_tokens IS NOT NULL
				AND cache_read_input_tokens IS NOT NULL
				THEN cache_read_input_tokens
			ELSE 0
		END AS cache_read_input,
		CASE
			WHEN token_semantics = pc.anthropic_semantics
				AND cache_creation_input_tokens IS NOT NULL
				THEN cache_creation_input_tokens
			WHEN token_semantics = pc.openai_semantics
				AND prompt_tokens IS NOT NULL
				AND cache_read_input_tokens IS NOT NULL
				AND cache_creation_input_tokens IS NOT NULL
				THEN cache_creation_input_tokens
			ELSE 0
		END AS cache_creation_input,
		CASE
			WHEN token_semantics = pc.openai_semantics
				AND prompt_tokens IS NOT NULL
				AND (cache_read_input_tokens IS NULL OR cache_creation_input_tokens IS NULL)
				THEN prompt_tokens
			WHEN token_semantics = pc.google_semantics
				AND prompt_tokens IS NOT NULL
				AND cache_read_input_tokens IS NULL
				THEN prompt_tokens
			ELSE 0
		END AS unclassified_input,
		CASE
			WHEN token_semantics = pc.openai_semantics
				AND completion_tokens IS NOT NULL
				AND reasoning_tokens IS NOT NULL
				THEN completion_tokens - reasoning_tokens
			-- Positive Gemini totals need prompt presence before output components
			-- can be partitioned without attributing tokens on a partial row.
			WHEN token_semantics = pc.google_semantics
				AND total_tokens IS NOT NULL AND total_tokens > 0
				AND prompt_tokens IS NOT NULL
				AND completion_tokens IS NOT NULL
				THEN completion_tokens
			WHEN token_semantics = pc.google_semantics
				AND (total_tokens IS NULL OR total_tokens = 0)
				AND completion_tokens IS NOT NULL
				THEN completion_tokens
			ELSE 0
		END AS standard_output,
		CASE
			WHEN token_semantics = pc.openai_semantics
				AND completion_tokens IS NOT NULL
				AND reasoning_tokens IS NOT NULL
				THEN reasoning_tokens
			WHEN token_semantics = pc.google_semantics
				AND total_tokens IS NOT NULL AND total_tokens > 0
				AND prompt_tokens IS NOT NULL
				AND completion_tokens IS NOT NULL
				THEN total_tokens - prompt_tokens - completion_tokens
			ELSE 0
		END AS reasoning_output,
		CASE
			WHEN token_semantics = pc.anthropic_semantics AND completion_tokens IS NOT NULL
				THEN completion_tokens
			WHEN token_semantics = pc.openai_semantics
				AND completion_tokens IS NOT NULL
				AND reasoning_tokens IS NULL
				THEN completion_tokens
			WHEN token_semantics = pc.google_semantics
				AND total_tokens IS NOT NULL AND total_tokens > 0
				AND prompt_tokens IS NOT NULL
				AND completion_tokens IS NULL
				THEN total_tokens - prompt_tokens
			ELSE 0
		END AS unclassified_output
	FROM validated
	CROSS JOIN projection_contract AS pc
),
classified AS (
	SELECT
		canonical.*,
		CASE
			WHEN observed = 0 THEN pc.without_usage_class
			WHEN invalid = 1 THEN pc.invalid_class
			WHEN token_semantics = pc.unknown_semantics THEN pc.unknown_semantics_class
			WHEN canonical_input IS NULL OR canonical_output IS NULL OR canonical_total IS NULL THEN pc.partial_class
			ELSE pc.comparable_class
		END AS class
	FROM canonical
	CROSS JOIN projection_contract AS pc
)
`

type projection struct {
	sql  string
	args []any
}

func buildProjection(query tokenanalytics.Query) (projection, error) {
	if query.Window.End.IsZero() {
		return projection{}, errInvalidWindow
	}
	if query.Window.HasResolvedStart() && query.Window.Start.After(query.Window.End) {
		return projection{}, errInvalidWindow
	}

	definitions := apicontract.All()
	if len(definitions) == 0 {
		return projection{}, errEmptySemanticContract
	}

	contractBindings := []any{
		string(apicontract.TokenUsageSemanticsUnknown),
		string(apicontract.TokenUsageSemanticsAnthropicMessages),
		string(apicontract.TokenUsageSemanticsOpenAICompatible),
		string(apicontract.TokenUsageSemanticsGoogleGenerateContent),
		classWithoutUsage,
		classInvalid,
		classUnknownSemantic,
		classPartial,
		classComparable,
	}
	valueRows := make([]string, 0, len(definitions))
	args := make([]any, 0, len(definitions)*2+len(contractBindings))
	for _, definition := range definitions {
		valueRows = append(valueRows, "(?, ?)")
		args = append(args, string(definition.APIType), string(definition.TokenUsageSemantics))
	}
	args = append(args, contractBindings...)

	endUnixNano, err := instant.UnixNano(query.Window.End)
	if err != nil {
		return projection{}, fmt.Errorf("%w: end instant: %w", errInvalidWindow, err)
	}
	predicates := []string{"rl.created_at_unix_nano < ?"}
	args = append(args, endUnixNano)
	if query.Window.HasResolvedStart() {
		startUnixNano, err := instant.UnixNano(query.Window.Start)
		if err != nil {
			return projection{}, fmt.Errorf("%w: start instant: %w", errInvalidWindow, err)
		}
		predicates = append(predicates, "rl.created_at_unix_nano >= ?")
		args = append(args, startUnixNano)
	}
	if query.ProviderID != nil {
		predicates = append(predicates, "rl.provider_id = ?")
		args = append(args, *query.ProviderID)
	}
	if query.Model != nil {
		predicates = append(predicates, "rl.model = ?")
		args = append(args, *query.Model)
	}
	if query.APIType != nil {
		predicates = append(predicates, "rl.api_type = ?")
		args = append(args, *query.APIType)
	}

	sql := fmt.Sprintf(projectionSQLTemplate,
		strings.Join(valueRows, ", "),
		projectionIndex(query),
		strings.Join(predicates, " AND "),
	)
	return projection{sql: sql, args: args}, nil
}

// Ranking GROUP BY clauses otherwise tempt SQLite into full scans of an index
// whose leading column only helps output order. Pinning the index that matches
// the active exact filter keeps the instant range bounded for every query shape.
func projectionIndex(query tokenanalytics.Query) string {
	switch {
	case query.APIType != nil:
		return apiTypeCreatedAtUnixNanoIndex
	case query.Model != nil:
		return modelCreatedAtUnixNanoIndex
	case query.ProviderID != nil:
		return providerCreatedAtUnixNanoIndex
	default:
		return createdAtUnixNanoIndex
	}
}
