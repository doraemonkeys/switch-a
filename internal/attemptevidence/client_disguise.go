package attemptevidence

import (
	"bytes"
	"encoding/json"
	"fmt"
)

// ClientDisguise records application transformation separately from upstream
// error rules so gateway conversion faults cannot imply an unhealthy account.
type ClientDisguise struct {
	DiagnosticID        string               `json:"diagnostic_id"`
	Truncated           bool                 `json:"truncated,omitempty"`
	RequestID           string               `json:"request_id,omitempty"`
	OperationID         string               `json:"operation_id,omitempty"`
	ProviderID          string               `json:"provider_id,omitempty"`
	CredentialSessionID string               `json:"credential_session_id,omitempty"`
	AccountID           string               `json:"account_id,omitempty"`
	ClientIdentityID    string               `json:"client_identity_id,omitempty"`
	GenerationID        string               `json:"generation_id,omitempty"`
	DeviceID            string               `json:"device_id,omitempty"`
	ClientVersion       string               `json:"client_version,omitempty"`
	RevisionID          string               `json:"revision_id,omitempty"`
	TransportSampleID   string               `json:"transport_sample_id,omitempty"`
	SourceID            string               `json:"source_id,omitempty"`
	CapturedAt          string               `json:"captured_at,omitempty"`
	ClientType          string               `json:"client_type,omitempty"`
	Platform            string               `json:"platform,omitempty"`
	Arch                string               `json:"arch,omitempty"`
	AppliedScopes       []string             `json:"applied_scopes,omitempty"`
	Decision            string               `json:"decision"`
	Phase               string               `json:"phase,omitempty"`
	PlatformFacts       map[string]string    `json:"platform_facts,omitempty"`
	Candidates          []DisguiseCandidate  `json:"candidates,omitempty"`
	Differences         []DisguiseDifference `json:"differences,omitempty"`
	Failure             *DisguiseFailure     `json:"failure,omitempty"`
}
type DisguiseCandidate struct {
	ProviderID          string `json:"provider_id"`
	CredentialSessionID string `json:"credential_session_id,omitempty"`
	Outcome             string `json:"outcome"`
	Reason              string `json:"reason,omitempty"`
	Platform            string `json:"platform,omitempty"`
}
type DisguiseDifference struct {
	Carrier  string `json:"carrier"`
	Location string `json:"location"`
	Original string `json:"original"`
	Derived  string `json:"derived"`
}
type DisguiseFailure struct {
	Phase           string   `json:"phase"`
	Location        string   `json:"location"`
	ErrorChain      []string `json:"error_chain"`
	OriginalSnippet string   `json:"original_snippet,omitempty"`
	DerivedSnippet  string   `json:"derived_snippet,omitempty"`
}

// EncodeClientDisguise preserves sibling evidence and the common envelope limit.
func EncodeClientDisguise(existing []byte, evidence *ClientDisguise) ([]byte, error) {
	if evidence == nil {
		return Encode(existing, nil)
	}
	if evidence.DiagnosticID == "" || evidence.Decision == "" {
		return nil, fmt.Errorf("client disguise evidence requires diagnostic_id and decision")
	}
	encoded, err := Encode(existing, nil)
	if err != nil {
		return nil, err
	}
	envelope := map[string]json.RawMessage{}
	if len(bytes.TrimSpace(encoded)) > 0 {
		if err := json.Unmarshal(encoded, &envelope); err != nil {
			return nil, err
		}
	}
	envelope["v"] = json.RawMessage("2")
	bounded := boundClientDisguise(evidence)
	for {
		raw, err := marshalWithoutHTMLEscaping(&bounded)
		if err != nil {
			return nil, err
		}
		envelope["client_disguise"] = raw
		merged, err := marshalWithoutHTMLEscaping(envelope)
		if err != nil {
			return nil, err
		}
		if len(merged) <= MaxAttemptEvidenceBytes {
			return Encode(merged, nil)
		}
		if !trimClientDisguise(&bounded) {
			return nil, fmt.Errorf("%w: client disguise terminal evidence cannot fit beside existing evidence", ErrEvidenceTooLarge)
		}
	}
}
func EncodeClientDisguiseString(existing *string, evidence *ClientDisguise) (*string, error) {
	var raw []byte
	if existing != nil {
		raw = []byte(*existing)
	}
	encoded, err := EncodeClientDisguise(raw, evidence)
	if err != nil || encoded == nil {
		return nil, err
	}
	value := string(encoded)
	return &value, nil
}
