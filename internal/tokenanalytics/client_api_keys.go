package tokenanalytics

import "context"

// ClientAPIKey is a selectable usage identity, including unregistered and
// deleted keys that still have request history. Name reflects the registry now.
type ClientAPIKey struct {
	Fingerprint string
	Name        string
	MaskedKey   string
}

// ClientAPIKeys enumerates independently of the selected key and time window so
// a zero-usage selection never hides the other available keys.
func (s *Service) ClientAPIKeys(ctx context.Context) (keys []ClientAPIKey, err error) {
	snapshot, err := s.reader.OpenSnapshot(ctx)
	if err != nil {
		return nil, NewFailure(FailureStageSnapshotOpen, FailureCodeRepository, err)
	}
	if snapshot == nil {
		return nil, NewFailure(FailureStageSnapshotOpen, FailureCodeSnapshotUnavailable, errNilSnapshot)
	}
	defer func() {
		if closeErr := snapshot.Close(); closeErr != nil && err == nil {
			keys = nil
			err = NewFailure(FailureStageClientAPIKeys, FailureCodeSnapshotClose, closeErr)
		}
	}()
	keys, err = snapshot.ReadClientAPIKeys(ctx)
	if err != nil {
		return nil, NewFailure(FailureStageClientAPIKeys, FailureCodeRepository, err)
	}
	if keys == nil {
		keys = []ClientAPIKey{}
	}
	return keys, nil
}
