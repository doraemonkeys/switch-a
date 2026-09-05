package selector

import (
	"encoding/hex"
	"strings"

	"github.com/doraemonkeys/switch-a/internal/codex/identity"
	"github.com/doraemonkeys/switch-a/internal/model"
)

const stickyCodexAPIType = "codex"

// BuildContinuityKey derives the sticky/continuity key from the request
// dimensions already known before provider selection. Unknown models degrade to
// api_type scope even when sticky mode prefers model affinity.
func BuildContinuityKey(req *model.SelectRequest) model.StickyKey {
	key := model.StickyKey{
		IP:      reqClientIP(req),
		User:    reqUser(req),
		APIType: reqAPIType(req),
	}
	if req != nil && key.APIType == stickyCodexAPIType {
		// String deliberately redacts the digest; the binary codec retains both
		// key version and digest so separate client credentials cannot collide.
		if scope, err := req.ClientScope.MarshalBinary(); err == nil {
			key.ClientScope = hex.EncodeToString(scope)
		}
	}
	if stickyModeConsumesModel(reqStickyMode(req)) {
		key.Model = requestSelectionModel(req)
	}
	return key
}

func reqClientIP(req *model.SelectRequest) string {
	if req == nil {
		return ""
	}
	return req.ClientIP
}

func reqOperationID(req *model.SelectRequest) string {
	if req == nil {
		return ""
	}
	return strings.TrimSpace(req.OperationID)
}

func reqUser(req *model.SelectRequest) string {
	if req == nil {
		return ""
	}
	return req.User
}

func reqAPIType(req *model.SelectRequest) string {
	if req == nil {
		return ""
	}
	return strings.TrimSpace(req.APIType)
}

func reqModel(req *model.SelectRequest) string {
	if req == nil {
		return ""
	}
	return req.Model
}

func reqStickyMode(req *model.SelectRequest) model.StickyMode {
	if req == nil {
		return model.StickyModeOff
	}
	return req.StickyMode
}

func reqRequiredAuthority(req *model.SelectRequest) *codexidentity.UpstreamAuthority {
	if req == nil {
		return nil
	}
	return req.RequiredAuthority
}

func reqPreferredRouteTargetID(req *model.SelectRequest) string {
	if req == nil || req.RequiredAuthority == nil {
		return ""
	}
	return strings.TrimSpace(req.PreferredRouteTargetID)
}

func reqVisibleContinuitySeedCandidate(req *model.SelectRequest) *model.VisibleContinuitySeedCandidate {
	if req == nil {
		return nil
	}
	return req.EffectiveVisibleContinuitySeedCandidate()
}
