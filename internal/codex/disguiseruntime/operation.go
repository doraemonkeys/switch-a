// Package disguiseruntime freezes client-disguise configuration at the logical
// ingress boundary, independently from live provider availability.
package disguiseruntime

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sync"

	"github.com/doraemonkeys/switch-a/internal/codex/clientdisguise"
	"github.com/doraemonkeys/switch-a/internal/model"
)

const APIType = "codex"

type Repository interface {
	EvaluateCandidate(context.Context, string, clientdisguise.AccountBasis, clientdisguise.Policy, clientdisguise.PlatformFacts) (clientdisguise.Candidate, error)
	CommitTarget(context.Context, clientdisguise.Candidate) (clientdisguise.TargetSnapshot, error)
}
type Operation struct {
	repository  Repository
	operationID string
	facts       clientdisguise.PlatformFacts
	mu          sync.Mutex
	candidates  map[string]clientdisguise.Candidate
	targets     map[string]clientdisguise.TargetSnapshot
	exclusions  map[string]model.DisguiseExclusion
}

func New(ctx context.Context, repository Repository, providers []model.Provider, headers http.Header, operationID string) (*Operation, error) {
	if repository == nil {
		return nil, fmt.Errorf("client disguise repository required")
	}
	operation := &Operation{repository: repository, operationID: operationID, facts: clientdisguise.ProjectPlatform(headers), candidates: make(map[string]clientdisguise.Candidate), targets: make(map[string]clientdisguise.TargetSnapshot), exclusions: make(map[string]model.DisguiseExclusion)}
	for i := range providers {
		provider := &providers[i]
		session, ok := provider.CredentialSessionForAPIType(APIType)
		if !ok {
			continue
		}
		basis := clientdisguise.AccountBasis{Kind: string(session.Subject.Kind), Value: append([]byte(nil), session.Subject.Value...), KeyVersion: session.Subject.KeyVersion}
		candidate, err := repository.EvaluateCandidate(ctx, session.SessionID, basis, provider.ClientDisguise.Clone(), operation.facts.Clone())
		if err != nil {
			return nil, fmt.Errorf("client disguise operation %s provider %s: %w", operationID, provider.ID, err)
		}
		operation.candidates[targetKey(provider.ID, session.SessionID)] = candidate.Clone()
	}
	return operation, nil
}
func targetKey(providerID, sessionID string) string { return providerID + "\x00" + sessionID }
func providerKey(provider *model.Provider) (string, string, error) {
	if provider == nil {
		return "", "", fmt.Errorf("provider required")
	}
	session, ok := provider.CredentialSessionForAPIType(APIType)
	if !ok {
		return "", "", fmt.Errorf("provider %s has no codex credential session", provider.ID)
	}
	return targetKey(provider.ID, session.SessionID), session.SessionID, nil
}
func (o *Operation) Evaluate(ctx context.Context, provider *model.Provider) (clientdisguise.Candidate, error) {
	if err := ctx.Err(); err != nil {
		return clientdisguise.Candidate{}, err
	}
	key, _, err := providerKey(provider)
	if err != nil {
		return clientdisguise.Candidate{}, err
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	candidate, ok := o.candidates[key]
	if !ok {
		return candidate, fmt.Errorf("provider %s is outside operation %s disguise snapshot", provider.ID, o.operationID)
	}
	o.recordExclusion(provider.ID, candidate)
	return candidate.Clone(), nil
}
func (o *Operation) Commit(ctx context.Context, provider *model.Provider) (clientdisguise.TargetSnapshot, error) {
	key, _, err := providerKey(provider)
	if err != nil {
		return clientdisguise.TargetSnapshot{}, err
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	if target, ok := o.targets[key]; ok {
		return target.Clone(), nil
	}
	candidate, ok := o.candidates[key]
	if !ok {
		return clientdisguise.TargetSnapshot{}, fmt.Errorf("provider outside disguise snapshot")
	}
	if !candidate.Decision.Allowed {
		o.recordExclusion(provider.ID, candidate)
		return clientdisguise.TargetSnapshot{}, fmt.Errorf("%w: %s", clientdisguise.ErrCandidateExcluded, candidate.Decision.Reason)
	}
	target, err := o.repository.CommitTarget(ctx, candidate.Clone())
	if err != nil {
		if errors.Is(err, clientdisguise.ErrCandidateExcluded) {
			candidate.Decision.Allowed = false
			candidate.Decision.Reason = "concurrent_profile_platform_mismatch"
			o.candidates[key] = candidate
			o.recordExclusion(provider.ID, candidate)
		}
		return clientdisguise.TargetSnapshot{}, err
	}
	o.targets[key] = target.Clone()
	return target.Clone(), nil
}
func (o *Operation) recordExclusion(providerID string, candidate clientdisguise.Candidate) {
	if candidate.Decision.Allowed {
		return
	}
	o.exclusions[providerID] = model.DisguiseExclusion{ProviderID: providerID, CredentialSessionID: candidate.CredentialSessionID, Reason: candidate.Decision.Reason, Decision: candidate.Decision}
}
func (o *Operation) Target(providerID, sessionID string) (clientdisguise.TargetSnapshot, bool) {
	o.mu.Lock()
	defer o.mu.Unlock()
	target, ok := o.targets[targetKey(providerID, sessionID)]
	return target.Clone(), ok
}
func (o *Operation) Exclusions() []model.DisguiseExclusion {
	o.mu.Lock()
	defer o.mu.Unlock()
	result := make([]model.DisguiseExclusion, 0, len(o.exclusions))
	for _, entry := range o.exclusions {
		entry.Decision.Facts = entry.Decision.Facts.Clone()
		result = append(result, entry)
	}
	return result
}
func (o *Operation) Facts() clientdisguise.PlatformFacts { return o.facts.Clone() }
func (o *Operation) OperationID() string                 { return o.operationID }
