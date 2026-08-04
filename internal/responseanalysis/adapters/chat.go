package adapters

import "github.com/doraemonkeys/switch-a/internal/responseanalysis/framing"

type chatAdapter struct{ baseAdapter }

func (a chatAdapter) Observe(frame framing.Frame) Result {
	if frame.Done {
		if frame.Event == "" {
			return Result{Class: EventControl}
		}
		return Result{Class: EventClientVisible}
	}
	document, terminal := a.begin(frame)
	if terminal != nil {
		return *terminal
	}
	resources := a.resources()
	usage := a.usage(document, "", &resources)

	if a.kind != framing.KindSSE || frame.Event == "" || frame.Event == "error" {
		if providerError, ok := objectField(document, document.root, "error"); ok {
			hasDiscriminator := fieldHasKind(document, providerError, "type", jsonString, jsonNumber) ||
				fieldHasKind(document, providerError, "code", jsonString, jsonNumber)
			if hasDiscriminator && fieldHasKind(document, providerError, "message", jsonString) {
				fields, tooLarge := extractFields(document, providerError, a.limits, true, &resources)
				return errorResult(fields, tooLarge, usage, &resources)
			}
		}
	}
	choices, choicesPresent := document.objectField(document.root, "choices")
	usageOnly := frame.Event == "" &&
		((choicesPresent && document.arrayEmpty(choices)) || document.objectFieldCount(document.root) == 1)
	return a.nonError(usage, usageOnly, &resources)
}
