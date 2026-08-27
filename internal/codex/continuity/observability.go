package codexcontinuity

import "time"

type Event struct {
	At              time.Time
	Action          string
	Outcome         string
	OperationID     string
	SessionID       string
	Generation      Generation
	BindingKind     Kind
	Lifecycle       Lifecycle
	KeyVersion      string
	ClientVersion   string
	ProtocolScope   string
	RouteTargetHint string
}

// Observer receives structured, secret-free decisions. Adapters can translate
// these events to the application's logger without coupling the deep module to
// a concrete logging framework.
type Observer interface {
	ObserveContinuity(Event)
}

type ObserverFunc func(Event)

func (f ObserverFunc) ObserveContinuity(event Event) { f(event) }

func observe(observer Observer, event Event) {
	if observer != nil {
		observer.ObserveContinuity(event)
	}
}
