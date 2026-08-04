package adapters

import (
	"github.com/doraemonkeys/switch-a/internal/responseanalysis/framing"
	"github.com/doraemonkeys/switch-a/internal/responseanalysis/tokenusage"
)

const openAIResponsesErrorType = "error"

type responsesAdapter struct{ baseAdapter }

func (a responsesAdapter) Observe(frame framing.Frame) Result {
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
	usage := a.usage(document, "response", &resources)

	if a.kind == framing.KindJSON {
		if result, matched := a.observeJSON(document, usage, &resources); matched {
			return result
		}
		return a.nonError(usage, false, &resources)
	}

	if result, matched := a.observeDirectError(document, usage, &resources); matched {
		return result
	}
	if result, matched := a.observeFailedResponse(document, usage, &resources); matched {
		return result
	}

	eventType := responseEventType(document, frame.Event, a.limits.TypeBytes)
	if isResponseControlType(eventType) {
		return resources.finish(Result{Class: EventControl, Usage: usage})
	}
	return a.nonError(usage, eventType == "response.completed", &resources)
}

func (a responsesAdapter) observeJSON(
	document jsonDocument,
	usage *tokenusage.TokenUsage,
	resources *resourceContext,
) (Result, bool) {
	typeError, _ := exactStringFieldEquals(document, document.root, "type", openAIResponsesErrorType, a.limits.TypeBytes)
	statusFailed, _ := exactStringFieldEquals(document, document.root, "status", "failed", a.limits.TypeBytes)
	if !typeError && !statusFailed {
		return Result{}, false
	}
	providerError, ok := objectField(document, document.root, "error")
	if !ok {
		return Result{}, false
	}
	fields, tooLarge := extractFields(document, providerError, a.limits, false, resources)
	if typeError {
		if _, nestedTypePresent := document.objectField(providerError, "type"); !nestedTypePresent {
			// The top-level discriminator is also the provider's error type when
			// no more specific nested classification was supplied.
			fields.Type = openAIResponsesErrorType
		}
	}
	return errorResult(fields, tooLarge, usage, resources), true
}

func (a responsesAdapter) observeDirectError(
	document jsonDocument,
	usage *tokenusage.TokenUsage,
	resources *resourceContext,
) (Result, bool) {
	typeError, _ := exactStringFieldEquals(document, document.root, "type", openAIResponsesErrorType, a.limits.TypeBytes)
	_, codePresent := document.objectField(document.root, "code")
	errorRaw, errorPresent := document.objectField(document.root, "error")
	if !typeError || (!codePresent && !errorPresent) ||
		!fieldHasKind(document, document.root, "message", jsonString) {
		return Result{}, false
	}

	providerError := jsonValue{}
	if errorPresent && errorRaw.kind == jsonObject {
		providerError = errorRaw
	}
	typeValue, typeStatus := stringField(document, providerError, "type", a.limits.TypeBytes, resources)
	if typeStatus == fieldAbsent {
		typeValue = openAIResponsesErrorType
		typeStatus = fieldValid
	}
	code, codeStatus := preferredScalarField(
		document,
		providerError,
		"code",
		document.root,
		"code",
		a.limits.CodeBytes,
		resources,
	)
	message, messageStatus := preferredStringField(
		document,
		providerError,
		"message",
		document.root,
		"message",
		a.limits.MessageBytes,
		resources,
	)
	reason, reasonStatus := preferredStringField(
		document,
		providerError,
		"reason",
		document.root,
		"param",
		a.limits.ReasonBytes,
		resources,
	)
	if typeStatus == fieldTooLarge || codeStatus == fieldTooLarge ||
		messageStatus == fieldTooLarge || reasonStatus == fieldTooLarge {
		return resources.finish(Result{Class: EventFailOpen, Failure: framing.FailureSemanticFieldTooLarge}), true
	}
	if messageStatus != fieldValid {
		return Result{}, false
	}
	fields := SemanticFields{Type: typeValue, Code: code, Message: message, Reason: reason}
	fields.preserveEmptyPresence(typeValue, typeStatus, presenceType)
	fields.preserveEmptyPresence(code, codeStatus, presenceCode)
	fields.preserveEmptyPresence(message, messageStatus, presenceMessage)
	fields.preserveEmptyPresence(reason, reasonStatus, presenceReason)
	return errorResult(fields, false, usage, resources), true
}

func (a responsesAdapter) observeFailedResponse(
	document jsonDocument,
	usage *tokenusage.TokenUsage,
	resources *resourceContext,
) (Result, bool) {
	failedEvent, _ := exactStringFieldEquals(document, document.root, "type", "response.failed", a.limits.TypeBytes)
	if !failedEvent {
		return Result{}, false
	}
	response, ok := objectField(document, document.root, "response")
	if !ok {
		return Result{}, false
	}
	statusFailed, _ := exactStringFieldEquals(document, response, "status", "failed", a.limits.TypeBytes)
	providerError, ok := objectField(document, response, "error")
	if !ok || !statusFailed || !fieldHasKind(document, providerError, "message", jsonString) {
		return Result{}, false
	}
	fields, tooLarge := extractFields(document, providerError, a.limits, false, resources)
	return errorResult(fields, tooLarge, usage, resources), true
}

func responseEventType(document jsonDocument, event string, maxBytes int) string {
	if event != "" {
		return event
	}
	for _, candidate := range []string{"response.created", "response.queued", "response.in_progress", "response.completed"} {
		if matched, _ := exactStringFieldEquals(document, document.root, "type", candidate, maxBytes); matched {
			return candidate
		}
	}
	return ""
}

func isResponseControlType(eventType string) bool {
	return eventType == "response.created" || eventType == "response.queued" || eventType == "response.in_progress"
}
