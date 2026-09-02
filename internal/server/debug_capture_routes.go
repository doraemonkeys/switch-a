package server

import (
	"encoding/json"
	"net/http"
	"path"
	"strings"

	"github.com/doraemonkeys/switch-a/internal/admin"
	admindebugcapture "github.com/doraemonkeys/switch-a/internal/admin/debugcapture"
	"github.com/doraemonkeys/switch-a/internal/model"

	"go.uber.org/zap"
)

const debugCaptureAPIPrefix = "/admin/api/debug-capture"

// secureDebugCaptureBoundary runs before ServeMux because ServeMux redirects
// non-canonical paths before dispatching to registered handlers. Rejecting those
// paths also prevents a mistakenly supplied query capability from being copied
// into a redirect Location header.
func secureDebugCaptureBoundary(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestPath := r.URL.Path
		nonCanonical := hasNonCanonicalPathSegments(requestPath)
		capturePath := isDebugCaptureAPIPath(requestPath)
		if !capturePath && nonCanonical {
			capturePath = isDebugCaptureAPIPath(path.Clean(requestPath))
		}
		if !capturePath {
			next.ServeHTTP(w, r)
			return
		}

		admindebugcapture.ApplySensitiveResponseHeaders(w)
		if nonCanonical {
			w.Header().Set("Content-Type", admin.ContentTypeJSON)
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(model.ErrorResponse{
				Code:    admin.ErrCodeValidation,
				Message: "Debug capture API path must be canonical",
			})
			return
		}
		next.ServeHTTP(w, r)
	})
}

func isDebugCaptureAPIPath(requestPath string) bool {
	return requestPath == debugCaptureAPIPrefix || strings.HasPrefix(requestPath, debugCaptureAPIPrefix+"/")
}

func hasNonCanonicalPathSegments(requestPath string) bool {
	if strings.Contains(requestPath, "//") {
		return true
	}
	for segment := range strings.SplitSeq(requestPath, "/") {
		if segment == "." || segment == ".." {
			return true
		}
	}
	return false
}

// registerDebugCaptureRoutes keeps the capability endpoint and bearer-protected
// resources under one outer response-security boundary. The download handler is
// deliberately registered by path rather than method so wrong methods receive a
// 405 without falling through to bearer authentication.
func (s *AdminServer) registerDebugCaptureRoutes(
	mux *http.ServeMux,
	cfg AdminConfig,
	auth *admin.AuthMiddleware,
) {
	handler := admindebugcapture.NewHandler(admindebugcapture.Config{
		Providers: cfg.Store,
		Sessions:  cfg.CaptureSessions,
		Queries:   cfg.CaptureQueries,
		Exports:   cfg.CaptureExports,
		Logger:    cfg.Logger,
	})

	captureMux := http.NewServeMux()
	captureMux.Handle("POST "+debugCaptureAPIPrefix+"/sessions", auth.WrapFunc(handler.StartSession))
	captureMux.Handle("GET "+debugCaptureAPIPrefix+"/status", auth.WrapFunc(handler.Status))
	captureMux.Handle("DELETE "+debugCaptureAPIPrefix+"/sessions/{session_id}", auth.WrapFunc(handler.StopSession))
	captureMux.Handle("GET "+debugCaptureAPIPrefix+"/sessions/{session_id}/records", auth.WrapFunc(handler.ListRecords))
	captureMux.Handle("GET "+debugCaptureAPIPrefix+"/sessions/{session_id}/records/{record_id}", auth.WrapFunc(handler.GetRecord))
	captureMux.Handle("POST "+debugCaptureAPIPrefix+"/sessions/{session_id}/exports", auth.WrapFunc(handler.CreateExport))
	captureMux.Handle(debugCaptureAPIPrefix+"/exports/{export_id}/download", http.HandlerFunc(handler.DownloadExport))
	captureMux.Handle(debugCaptureAPIPrefix+"/", auth.WrapFunc(s.handleDebugCaptureNotFound))
	captureMux.Handle(debugCaptureAPIPrefix, auth.WrapFunc(s.handleDebugCaptureNotFound))

	secured := admindebugcapture.SensitiveResponses(captureMux)
	mux.Handle(debugCaptureAPIPrefix+"/", secured)
	mux.Handle(debugCaptureAPIPrefix, secured)
}

// handleDebugCaptureNotFound deliberately omits the raw path. A capability or
// captured payload can be pasted into an unknown route, and the observability
// boundary must not turn that attacker-controlled value into retained log data.
func (s *AdminServer) handleDebugCaptureNotFound(w http.ResponseWriter, r *http.Request) {
	s.logger.Warn("unmatched debug capture api route",
		zap.String("method", r.Method),
		zap.String("remote_addr", r.RemoteAddr),
	)
	writeAdminAPINotFound(w)
}
