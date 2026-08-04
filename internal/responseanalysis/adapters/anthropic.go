package adapters

import (
	"github.com/doraemonkeys/switch-a/internal/responseanalysis/framing"
)

type anthropicAdapter struct{ baseAdapter }

func (a anthropicAdapter) Observe(frame framing.Frame) Result {
	if frame.Done {
		return Result{Class: EventClientVisible}
	}
	document, terminal := a.begin(frame)
	if terminal != nil {
		return *terminal
	}
	resources := a.resources()
	usage := a.usage(document, "message", &resources)

	rootError, _ := exactStringFieldEquals(document, document.root, "type", "error", a.limits.TypeBytes)
	if rootError &&
		(a.kind != framing.KindSSE || frame.Event == "" || frame.Event == "error") {
		if providerError, ok := objectField(document, document.root, "error"); ok &&
			fieldHasKind(document, providerError, "type", jsonString) &&
			fieldHasKind(document, providerError, "message", jsonString) {
			fields, tooLarge := extractFields(document, providerError, a.limits, false, &resources)
			return errorResult(fields, tooLarge, usage, &resources)
		}
	}

	eventType := frame.Event
	if eventType == "" {
		for _, candidate := range []string{"ping", "message_start", "message_delta"} {
			if matched, _ := exactStringFieldEquals(document, document.root, "type", candidate, a.limits.TypeBytes); matched {
				eventType = candidate
				break
			}
		}
	}
	if eventType == "ping" || eventType == "message_start" {
		return resources.finish(Result{Class: EventControl, Usage: usage})
	}
	return a.nonError(usage, eventType == "message_delta", &resources)
}
