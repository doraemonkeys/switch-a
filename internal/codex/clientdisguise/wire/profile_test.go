package wire

import (
	disguise "github.com/doraemonkeys/switch-a/internal/codex/clientdisguise"
	"github.com/doraemonkeys/switch-a/internal/codex/clientdisguise/useragent"
)

func newPrimarySession(target disguise.TargetSnapshot, operationID string) *Session {
	return NewSession(target, operationID, disguise.PlatformFacts{RequestRole: useragent.Primary})
}
