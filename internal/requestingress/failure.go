package requestingress

import "errors"

type FailureKind string

const (
	FailureRead    FailureKind = "read"
	FailureLimit   FailureKind = "limit"
	FailureLength  FailureKind = "length"
	FailureStorage FailureKind = "storage"
)

// Failure preserves the concrete cause while separating client input from gateway storage attribution.
type Failure struct {
	Kind  FailureKind
	Cause error
}

func (f *Failure) Error() string { return f.Cause.Error() }
func (f *Failure) Unwrap() error { return f.Cause }

func sourceFailure(kind FailureKind, cause error) error {
	return &Failure{Kind: kind, Cause: cause}
}

func failureKind(err error) FailureKind {
	var failure *Failure
	if errors.As(err, &failure) {
		return failure.Kind
	}
	return ""
}

// Failure is a source transition, independent of the once-only input completion notification.
func (h *Handle) reportFailure() {
	snapshot := h.Snapshot()
	if snapshot.State != Failed {
		return
	}
	h.failureOnce.Do(func() {
		if h.options.OnFailure != nil {
			h.options.OnFailure(snapshot)
		}
		h.emit("failed", "", 0)
	})
}
