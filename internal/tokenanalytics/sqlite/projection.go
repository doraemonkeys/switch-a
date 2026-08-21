package sqlite

import (
	"fmt"
	"strings"

	"github.com/doraemonkeys/switch-a/internal/apicontract"
	"github.com/doraemonkeys/switch-a/internal/tokenanalytics"
)

const (
	classWithoutUsage    = "without_usage"
	classInvalid         = "invalid"
	classUnknownSemantic = "unknown_semantics"
	classPartial         = "partial"
	classComparable      = "comparable"
)

const projectionSQLTemplate = `
WITH
semantic_contract(api_type, token_semantics) AS (
	VALUES %s
),
filtered AS (
	SELECT
		rl.provider_id,
		rl.api_type,
		rl.model,
		rl.created_at,
		rl.prompt_tokens,
		rl.completion_tokens,
		rl.total_tokens,
		rl.reasoning_tokens,
		rl.cache_read_input_tokens,
		rl.cache_creation_input_tokens,
		COALESCE(sc.token_semantics, %s) AS token_semantics
	FROM request_logs AS rl
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
			WHEN token_semantics = %s
				AND total_tokens IS NOT NULL
				AND prompt_tokens IS NOT NULL
				AND completion_tokens IS NOT NULL
				AND total_tokens != prompt_tokens + completion_tokens
				THEN 1
			WHEN token_semantics = %s
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
			WHEN token_semantics = %s
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
),
canonical AS (
	SELECT
		validated.*,
		CASE
			WHEN token_semantics = %s
				AND prompt_tokens IS NOT NULL
				AND cache_read_input_tokens IS NOT NULL
				AND cache_creation_input_tokens IS NOT NULL
				THEN prompt_tokens + cache_read_input_tokens + cache_creation_input_tokens
			WHEN token_semantics IN (%s, %s) AND prompt_tokens IS NOT NULL
				THEN prompt_tokens
		END AS canonical_input,
		CASE
			WHEN token_semantics IN (%s, %s) AND completion_tokens IS NOT NULL
				THEN completion_tokens
			WHEN token_semantics = %s
				AND total_tokens IS NOT NULL AND total_tokens > 0
				AND prompt_tokens IS NOT NULL
				THEN total_tokens - prompt_tokens
			WHEN token_semantics = %s
				AND (total_tokens IS NULL OR total_tokens = 0)
				AND completion_tokens IS NOT NULL
				THEN completion_tokens
		END AS canonical_output,
		CASE
			WHEN token_semantics = %s
				AND prompt_tokens IS NOT NULL
				AND completion_tokens IS NOT NULL
				AND cache_read_input_tokens IS NOT NULL
				AND cache_creation_input_tokens IS NOT NULL
				THEN prompt_tokens + cache_read_input_tokens + cache_creation_input_tokens + completion_tokens
			WHEN token_semantics = %s
				AND prompt_tokens IS NOT NULL AND completion_tokens IS NOT NULL
				THEN COALESCE(total_tokens, prompt_tokens + completion_tokens)
			WHEN token_semantics = %s
				AND total_tokens IS NOT NULL AND total_tokens > 0
				AND prompt_tokens IS NOT NULL
				THEN total_tokens
			WHEN token_semantics = %s
				AND (total_tokens IS NULL OR total_tokens = 0)
				AND prompt_tokens IS NOT NULL AND completion_tokens IS NOT NULL
				THEN prompt_tokens + completion_tokens
		END AS canonical_total,
		CASE
			WHEN token_semantics = %s AND prompt_tokens IS NOT NULL
				THEN prompt_tokens
			WHEN token_semantics = %s
				AND prompt_tokens IS NOT NULL
				AND cache_read_input_tokens IS NOT NULL
				AND cache_creation_input_tokens IS NOT NULL
				THEN prompt_tokens - cache_read_input_tokens - cache_creation_input_tokens
			WHEN token_semantics = %s
				AND prompt_tokens IS NOT NULL
				AND cache_read_input_tokens IS NOT NULL
				THEN prompt_tokens - cache_read_input_tokens
			ELSE 0
		END AS fresh_input,
		CASE
			WHEN token_semantics = %s
				AND cache_read_input_tokens IS NOT NULL
				THEN cache_read_input_tokens
			WHEN token_semantics = %s
				AND prompt_tokens IS NOT NULL
				AND cache_read_input_tokens IS NOT NULL
				AND cache_creation_input_tokens IS NOT NULL
				THEN cache_read_input_tokens
			WHEN token_semantics = %s
				AND prompt_tokens IS NOT NULL
				AND cache_read_input_tokens IS NOT NULL
				THEN cache_read_input_tokens
			ELSE 0
		END AS cache_read_input,
		CASE
			WHEN token_semantics = %s
				AND cache_creation_input_tokens IS NOT NULL
				THEN cache_creation_input_tokens
			WHEN token_semantics = %s
				AND prompt_tokens IS NOT NULL
				AND cache_read_input_tokens IS NOT NULL
				AND cache_creation_input_tokens IS NOT NULL
				THEN cache_creation_input_tokens
			ELSE 0
		END AS cache_creation_input,
		CASE
			WHEN token_semantics = %s
				AND prompt_tokens IS NOT NULL
				AND (cache_read_input_tokens IS NULL OR cache_creation_input_tokens IS NULL)
				THEN prompt_tokens
			WHEN token_semantics = %s
				AND prompt_tokens IS NOT NULL
				AND cache_read_input_tokens IS NULL
				THEN prompt_tokens
			ELSE 0
		END AS unclassified_input,
		CASE
			WHEN token_semantics = %s
				AND completion_tokens IS NOT NULL
				AND reasoning_tokens IS NOT NULL
				THEN completion_tokens - reasoning_tokens
			WHEN token_semantics = %s
				AND completion_tokens IS NOT NULL
				AND (total_tokens IS NULL OR total_tokens = 0 OR total_tokens > 0)
				THEN completion_tokens
			ELSE 0
		END AS standard_output,
		CASE
			WHEN token_semantics = %s
				AND completion_tokens IS NOT NULL
				AND reasoning_tokens IS NOT NULL
				THEN reasoning_tokens
			WHEN token_semantics = %s
				AND total_tokens IS NOT NULL AND total_tokens > 0
				AND prompt_tokens IS NOT NULL
				AND completion_tokens IS NOT NULL
				THEN total_tokens - prompt_tokens - completion_tokens
			ELSE 0
		END AS reasoning_output,
		CASE
			WHEN token_semantics = %s AND completion_tokens IS NOT NULL
				THEN completion_tokens
			WHEN token_semantics = %s
				AND completion_tokens IS NOT NULL
				AND reasoning_tokens IS NULL
				THEN completion_tokens
			WHEN token_semantics = %s
				AND total_tokens IS NOT NULL AND total_tokens > 0
				AND prompt_tokens IS NOT NULL
				AND completion_tokens IS NULL
				THEN total_tokens - prompt_tokens
			ELSE 0
		END AS unclassified_output
	FROM validated
),
classified AS (
	SELECT
		canonical.*,
		CASE
			WHEN observed = 0 THEN %s
			WHEN invalid = 1 THEN %s
			WHEN token_semantics = %s THEN %s
			WHEN canonical_input IS NULL OR canonical_output IS NULL OR canonical_total IS NULL THEN %s
			ELSE %s
		END AS class
	FROM canonical
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

	valueRows := make([]string, 0, len(definitions))
	args := make([]any, 0, len(definitions)*2+5)
	for _, definition := range definitions {
		valueRows = append(valueRows, "(?, ?)")
		args = append(args, string(definition.APIType), string(definition.TokenUsageSemantics))
	}

	predicates := []string{"rl.created_at < ?"}
	args = append(args, query.Window.End.UTC())
	if query.Window.HasResolvedStart() {
		predicates = append(predicates, "rl.created_at >= ?")
		args = append(args, query.Window.Start.UTC())
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

	semanticsUnknown := sqlQuote(string(apicontract.TokenUsageSemanticsUnknown))
	semanticsAnthropic := sqlQuote(string(apicontract.TokenUsageSemanticsAnthropicMessages))
	semanticsOpenAI := sqlQuote(string(apicontract.TokenUsageSemanticsOpenAICompatible))
	semanticsGoogle := sqlQuote(string(apicontract.TokenUsageSemanticsGoogleGenerateContent))

	sql := fmt.Sprintf(projectionSQLTemplate,
		strings.Join(valueRows, ", "),
		semanticsUnknown,
		strings.Join(predicates, " AND "),
		semanticsAnthropic,
		semanticsOpenAI,
		semanticsGoogle,
		semanticsAnthropic,
		semanticsOpenAI, semanticsGoogle,
		semanticsAnthropic, semanticsOpenAI,
		semanticsGoogle,
		semanticsGoogle,
		semanticsAnthropic,
		semanticsOpenAI,
		semanticsGoogle,
		semanticsGoogle,
		semanticsAnthropic,
		semanticsOpenAI,
		semanticsGoogle,
		semanticsAnthropic,
		semanticsOpenAI,
		semanticsGoogle,
		semanticsAnthropic,
		semanticsOpenAI,
		semanticsOpenAI,
		semanticsGoogle,
		semanticsOpenAI,
		semanticsGoogle,
		semanticsOpenAI,
		semanticsGoogle,
		semanticsAnthropic,
		semanticsOpenAI,
		semanticsGoogle,
		sqlQuote(classWithoutUsage),
		sqlQuote(classInvalid),
		semanticsUnknown,
		sqlQuote(classUnknownSemantic),
		sqlQuote(classPartial),
		sqlQuote(classComparable),
	)
	return projection{sql: sql, args: args}, nil
}

func sqlQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "''") + "'"
}
