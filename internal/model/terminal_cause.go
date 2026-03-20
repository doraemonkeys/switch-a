package model

// TerminalCause lives in the model package because proxy, storage, and admin APIs
// all reason about the same lifecycle outcome. Keeping the enum here prevents each
// layer from inventing its own string contract.
type TerminalCause string

const (
	// TerminalUnknown keeps historical rows queryable when legacy logs do not contain
	// enough evidence to reconstruct the real terminal cause.
	TerminalUnknown TerminalCause = "unknown"

	TerminalProviderUnavailable        TerminalCause = "provider_unavailable"
	TerminalProviderConfigurationError TerminalCause = "provider_configuration_error"
	TerminalCleanClose                 TerminalCause = "clean_close"
	TerminalClientDisconnect           TerminalCause = "client_disconnect"
	TerminalUpstreamTransportError     TerminalCause = "upstream_transport_error"
	TerminalUpstreamSemanticError      TerminalCause = "upstream_semantic_error"
	TerminalUpstreamHandshakeRejected  TerminalCause = "upstream_handshake_rejected"
	TerminalClientUpgradeRejected      TerminalCause = "client_upgrade_rejected"
	TerminalInternalError              TerminalCause = "internal_error"
)

// CommitSource belongs beside TerminalCause for the same reason: persistence and
// diagnostics need a stable domain-level vocabulary, not proxy-local strings.
type CommitSource string

const (
	CommitSemantic        CommitSource = "semantic_event"
	CommitUpstreamMessage CommitSource = "upstream_message"
	CommitUnknown         CommitSource = "unknown"
)

// IsValidTerminalCause keeps admin/query parsing aligned with the persisted enum
// so callers fail fast instead of silently issuing filters that can never match.
func IsValidTerminalCause(cause TerminalCause) bool {
	switch cause {
	case TerminalUnknown,
		TerminalProviderUnavailable,
		TerminalProviderConfigurationError,
		TerminalCleanClose,
		TerminalClientDisconnect,
		TerminalUpstreamTransportError,
		TerminalUpstreamSemanticError,
		TerminalUpstreamHandshakeRejected,
		TerminalClientUpgradeRejected,
		TerminalInternalError:
		return true
	default:
		return false
	}
}
