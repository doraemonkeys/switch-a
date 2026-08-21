package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/doraemonkeys/switch-a/internal/apicontract"
	"github.com/doraemonkeys/switch-a/internal/model"
	"github.com/doraemonkeys/switch-a/internal/tokenanalytics"
)

func TestProjectionBindsCatalogSemantics(t *testing.T) {
	start := time.Date(2026, time.August, 21, 0, 0, 0, 0, time.UTC)
	projection, err := buildProjection(testQuery(start, start.Add(time.Hour), time.Hour))
	if err != nil {
		t.Fatalf("buildProjection() error = %v", err)
	}

	definitions := apicontract.All()
	for index, definition := range definitions {
		argumentOffset := index * 2
		if projection.args[argumentOffset] != string(definition.APIType) ||
			projection.args[argumentOffset+1] != string(definition.TokenUsageSemantics) {
			t.Fatalf("catalog binding %d = (%v, %v), want (%q, %q)",
				index,
				projection.args[argumentOffset],
				projection.args[argumentOffset+1],
				definition.APIType,
				definition.TokenUsageSemantics,
			)
		}
	}

	contractOffset := len(definitions) * 2
	wantContractBindings := []any{
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
	gotContractBindings := projection.args[contractOffset : contractOffset+len(wantContractBindings)]
	if !reflect.DeepEqual(gotContractBindings, wantContractBindings) {
		t.Fatalf("projection contract bindings = %v, want %v", gotContractBindings, wantContractBindings)
	}

	for _, semantics := range wantContractBindings[:4] {
		literal := "'" + semantics.(string) + "'"
		if strings.Contains(projection.sql, literal) {
			t.Errorf("projection SQL embeds protocol semantics %s instead of binding the API contract", literal)
		}
	}
}

func TestProjectionRejectsUnrepresentableWindowBounds(t *testing.T) {
	t.Parallel()

	representable := time.Date(2026, time.August, 21, 0, 0, 0, 0, time.UTC)
	for _, query := range []tokenanalytics.Query{
		testQuery(time.Date(1500, time.January, 1, 0, 0, 0, 0, time.UTC), representable, time.Hour),
		testQuery(representable, time.Date(2300, time.January, 1, 0, 0, 0, 0, time.UTC), time.Hour),
	} {
		if _, err := buildProjection(query); !errors.Is(err, errInvalidWindow) {
			t.Fatalf("buildProjection() error = %v, want %v", err, errInvalidWindow)
		}
	}
}

func TestProjectionNeutralizesComponentsWhenCanonicalInputsArePartial(t *testing.T) {
	database := newTestDatabase(t)
	start := time.Date(2026, time.August, 21, 0, 0, 0, 0, time.UTC)
	database.insertLog(t, model.RequestLog{
		ProviderID:               "anthropic-missing-cache-detail",
		APIType:                  string(apicontract.APITypeClaude),
		CreatedAt:                start.Add(time.Minute),
		PromptTokens:             int64Pointer(10),
		CompletionTokens:         int64Pointer(5),
		TotalTokens:              int64Pointer(15),
		CacheReadInputTokens:     int64Pointer(2),
		CacheCreationInputTokens: nil,
	})
	database.insertLog(t, model.RequestLog{
		ProviderID:       "gemini-missing-prompt",
		APIType:          string(apicontract.APITypeGemini),
		CreatedAt:        start.Add(2 * time.Minute),
		PromptTokens:     nil,
		CompletionTokens: int64Pointer(5),
		TotalTokens:      int64Pointer(8),
	})

	query := testQuery(start, start.Add(time.Hour), time.Hour)

	t.Run("Anthropic missing cache detail", func(t *testing.T) {
		row := readProjectionRow(t, database, query, "anthropic-missing-cache-detail")
		if row.class != classPartial || row.canonicalInput.Valid || row.canonicalTotal.Valid {
			t.Fatalf("projection row = %+v, want partial with unknown canonical input and total", row)
		}
		if row.freshInput != 0 {
			t.Fatalf("fresh input = %d, want neutral value for partial Anthropic input", row.freshInput)
		}
	})

	t.Run("Gemini positive total missing prompt", func(t *testing.T) {
		row := readProjectionRow(t, database, query, "gemini-missing-prompt")
		if row.class != classPartial || row.canonicalInput.Valid || row.canonicalOutput.Valid || row.canonicalTotal.Valid {
			t.Fatalf("projection row = %+v, want partial with unknown canonical input, output, and total", row)
		}
		if row.standardOutput != 0 {
			t.Fatalf("standard output = %d, want neutral value when a positive Gemini total lacks prompt tokens", row.standardOutput)
		}
	})
}

type projectionRow struct {
	class           string
	canonicalInput  sql.NullInt64
	canonicalOutput sql.NullInt64
	canonicalTotal  sql.NullInt64
	freshInput      int64
	standardOutput  int64
}

func readProjectionRow(t *testing.T, database *testDatabase, query tokenanalytics.Query, providerID string) projectionRow {
	t.Helper()
	projection, err := buildProjection(query)
	if err != nil {
		t.Fatalf("buildProjection() error = %v", err)
	}

	var row projectionRow
	err = database.repository.db.QueryRowContext(
		context.Background(),
		projection.sql+`SELECT class, canonical_input, canonical_output, canonical_total, fresh_input, standard_output
FROM classified
WHERE provider_id = ?`,
		appendProjectionArgs(projection.args, providerID)...,
	).Scan(
		&row.class,
		&row.canonicalInput,
		&row.canonicalOutput,
		&row.canonicalTotal,
		&row.freshInput,
		&row.standardOutput,
	)
	if err != nil {
		t.Fatalf("read classified projection row: %v", err)
	}
	return row
}
