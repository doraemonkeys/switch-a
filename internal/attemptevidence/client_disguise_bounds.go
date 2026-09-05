package attemptevidence

import "sort"

const (
	disguiseSnippetBytes    = 1024
	disguiseCollectionLimit = 16
	disguiseErrorChainLimit = 8
)

// Bounded copies keep diagnostic production independent of mutable wire buffers
// and reserve space for the terminal failure when a request changes many fields.
func boundClientDisguise(source *ClientDisguise) ClientDisguise {
	value := *source
	trim := func(text string) string {
		if len(text) > disguiseSnippetBytes {
			value.Truncated = true
		}
		return truncateUTF8(text, disguiseSnippetBytes)
	}
	value.PlatformFacts = map[string]string{}
	keys := make([]string, 0, len(source.PlatformFacts))
	for key := range source.PlatformFacts {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	if len(keys) > disguiseCollectionLimit {
		keys = keys[:disguiseCollectionLimit]
		value.Truncated = true
	}
	for _, key := range keys {
		value.PlatformFacts[key] = trim(source.PlatformFacts[key])
	}
	differences := source.Differences
	if len(differences) > disguiseCollectionLimit {
		differences = differences[:disguiseCollectionLimit]
		value.Truncated = true
	}
	value.Differences = make([]DisguiseDifference, 0, len(differences))
	for _, difference := range differences {
		difference.Original = trim(difference.Original)
		difference.Derived = trim(difference.Derived)
		difference.Location = trim(difference.Location)
		value.Differences = append(value.Differences, difference)
	}
	candidates := source.Candidates
	if len(candidates) > disguiseCollectionLimit {
		candidates = candidates[:disguiseCollectionLimit]
		value.Truncated = true
	}
	value.Candidates = make([]DisguiseCandidate, 0, len(candidates))
	for _, candidate := range candidates {
		candidate.Reason = trim(candidate.Reason)
		value.Candidates = append(value.Candidates, candidate)
	}
	if source.Failure != nil {
		failure := *source.Failure
		failure.OriginalSnippet = trim(failure.OriginalSnippet)
		failure.DerivedSnippet = trim(failure.DerivedSnippet)
		failure.Location = trim(failure.Location)
		chain := source.Failure.ErrorChain
		if len(chain) > disguiseErrorChainLimit {
			chain = chain[:disguiseErrorChainLimit]
			value.Truncated = true
		}
		failure.ErrorChain = make([]string, 0, len(chain))
		for _, item := range chain {
			failure.ErrorChain = append(failure.ErrorChain, trim(item))
		}
		value.Failure = &failure
	}
	return value
}
func trimClientDisguise(value *ClientDisguise) bool {
	value.Truncated = true
	if len(value.Differences) > 0 {
		value.Differences = value.Differences[:len(value.Differences)-1]
		return true
	}
	if len(value.Candidates) > 0 {
		value.Candidates = value.Candidates[:len(value.Candidates)-1]
		return true
	}
	if len(value.PlatformFacts) > 0 {
		value.PlatformFacts = nil
		return true
	}
	return false
}
