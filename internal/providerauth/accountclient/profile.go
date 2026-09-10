// Package accountclient applies credential-owned client features to requests
// initiated by the gateway, without importing downstream conversation headers.
package accountclient

import (
	"context"
	"fmt"
	"net/http"

	"github.com/doraemonkeys/switch-a/internal/buildinfo"
	"github.com/doraemonkeys/switch-a/internal/codex/clientdisguise"
	"github.com/google/uuid"
	"go.uber.org/zap"
)

const (
	TokenRefresh = "token_refresh"
	OAuthLogin   = "oauth_login"
	TokenImport  = "token_import"
	UsageQuery   = "usage_query"
)

// ProfileStore resolves existing session bindings without selecting a provider.
type ProfileStore interface {
	ResolveLoginProfile(context.Context, string) (clientdisguise.LoginProfile, error)
}

type Resolver struct {
	profiles ProfileStore
	logger   *zap.Logger
}

func NewResolver(profiles ProfileStore, logger *zap.Logger) *Resolver {
	if logger == nil {
		logger = zap.NewNop()
	}
	return &Resolver{profiles: profiles, logger: logger}
}

// Operation freezes identity for all transmissions of an account operation,
// including usage endpoint fallback and the callback of a pending OAuth login.
type Operation struct {
	userAgent string
	logger    *zap.Logger
}

func (r *Resolver) Resolve(ctx context.Context, sessionID, kind string) (Operation, error) {
	log := r.logger.With(
		zap.String("operation_id", uuid.NewString()),
		zap.String("operation", kind),
		zap.String("credential_session_id", sessionID),
	)
	profile := clientdisguise.LoginProfile{}
	if sessionID != "" && r.profiles != nil {
		var err error
		profile, err = r.profiles.ResolveLoginProfile(ctx, sessionID)
		if err != nil {
			log.Warn("account_client.profile_resolution_failed", zap.Error(err))
			return Operation{}, fmt.Errorf("resolve account client profile for session %q: %w", sessionID, err)
		}
	}

	ua, source := profile.UserAgent(), "profile"
	if ua == "" {
		ua = buildinfo.Current().UserAgent()
		switch {
		case sessionID == "":
			source = "unassigned_login"
		case r.profiles == nil:
			source = "profile_store_unavailable"
		case profile.Binding.RevisionID == "":
			source = "unbound_session"
		default:
			source = "profile_without_user_agent"
		}
	}
	log = log.With(
		zap.String("profile_revision_id", profile.Binding.RevisionID),
		zap.String("login_generation_id", profile.Login.GenerationID),
		zap.String("version_source", profile.Binding.VersionSource),
		zap.String("official_version", profile.OfficialVersion.Version),
		zap.String("user_agent_source", source),
		zap.String("user_agent", ua),
	)
	log.Debug("account_client.profile_resolved")
	return Operation{userAgent: ua, logger: log}, nil
}

// Apply changes only the client feature supported by these account endpoints.
// Copying a Responses header set would invent conversation and device carriers.
func (o Operation) Apply(request *http.Request) {
	request.Header.Set("User-Agent", o.userAgent)
	o.logger.Debug("account_client.request_prepared",
		zap.String("method", request.Method),
		zap.String("host", request.URL.Host),
		zap.String("path", request.URL.Path),
	)
}
