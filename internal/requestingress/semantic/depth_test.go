package semantic

import (
	"context"
	"reflect"
	"strings"
	"testing"
)

func TestLegacyDepthBoundary(t *testing.T) {
	for _, depth := range []int{9998, 9999, 10000, 10001} {
		body := "{\"reasoning\":{\"effort\":\"high\"},\"input\":" + strings.Repeat("[", depth) + "0" + strings.Repeat("]", depth) + "}"
		got := Project(context.Background(), strings.NewReader(body), Options{ReasoningContract: ReasoningCodex})
		want := legacyRequestedReasoning("codex", []byte(body))
		if !reflect.DeepEqual(got.Reasoning.Value, want) {
			t.Errorf("depth %d reasoning state %v, legacy %v", depth, *got.Reasoning.Value.State, *want.State)
		}
	}
}
