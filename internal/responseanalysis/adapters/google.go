package adapters

import (
	"strings"

	"github.com/doraemonkeys/switch-a/internal/responseanalysis/framing"
	"github.com/doraemonkeys/switch-a/internal/responseanalysis/tokenusage"
)

const (
	googleErrorInfoType    = "google.rpc.ErrorInfo"
	googleErrorInfoURLType = "type.googleapis.com/google.rpc.ErrorInfo"
)

type googleAdapter struct{ baseAdapter }

func (a googleAdapter) Observe(frame framing.Frame) Result {
	if frame.Done {
		return Result{Class: EventClientVisible}
	}
	document, terminal := a.begin(frame)
	if terminal != nil {
		return *terminal
	}
	resources := a.resources()
	usage := googleRootUsage(document, &resources)

	if result, matched := a.observeProviderError(document, usage, &resources); matched {
		return result
	}
	candidates, candidatesPresent := document.objectField(document.root, "candidates")
	usageOnly := frame.Event == "" &&
		((candidatesPresent && document.arrayEmpty(candidates)) || document.objectFieldCount(document.root) == 1)
	return a.nonError(usage, usageOnly, &resources)
}

func (a googleAdapter) observeProviderError(
	document jsonDocument,
	usage *tokenusage.TokenUsage,
	resources *resourceContext,
) (Result, bool) {
	providerError, ok := objectField(document, document.root, "error")
	if !ok {
		return Result{}, false
	}
	status, statusField := stringField(document, providerError, "status", a.limits.TypeBytes, resources)
	code, codeStatus := canonicalIntegerField(document, providerError, "code", a.limits.CodeBytes, resources)
	hasDiscriminator := statusField == fieldValid || statusField == fieldTooLarge ||
		codeStatus == fieldValid || codeStatus == fieldTooLarge
	if !hasDiscriminator {
		return Result{}, false
	}

	message, messageStatus := stringField(document, providerError, "message", a.limits.MessageBytes, resources)
	if messageStatus == fieldTooLarge || statusField == fieldTooLarge || codeStatus == fieldTooLarge {
		return resources.finish(Result{Class: EventFailOpen, Failure: framing.FailureSemanticFieldTooLarge}), true
	}
	if messageStatus != fieldValid || (statusField != fieldValid && codeStatus != fieldValid) {
		return Result{}, false
	}

	reason, reasonStatus := a.errorInfoReason(document, providerError, resources)
	if reasonStatus == fieldTooLarge {
		return resources.finish(Result{Class: EventFailOpen, Failure: framing.FailureSemanticFieldTooLarge}), true
	}
	fields := SemanticFields{Type: status, Code: code, Message: message, Reason: reason}
	fields.preserveEmptyPresence(status, statusField, presenceType)
	fields.preserveEmptyPresence(code, codeStatus, presenceCode)
	fields.preserveEmptyPresence(message, messageStatus, presenceMessage)
	fields.preserveEmptyPresence(reason, reasonStatus, presenceReason)
	return errorResult(fields, false, usage, resources), true
}

func canonicalIntegerField(
	document jsonDocument,
	object jsonValue,
	name string,
	maxBytes int,
	resources *resourceContext,
) (string, fieldStatus) {
	raw, ok := document.objectField(object, name)
	if !ok {
		return "", fieldAbsent
	}
	transient := resourceContext{reserver: resources.reserver}
	value, status := canonicalNumber(document.data, raw, maxBytes, &transient)
	if transient.err != nil {
		resources.err = transient.err
		transient.release()
		return "", fieldInvalid
	}
	if status != fieldValid || strings.ContainsRune(value, '.') {
		transient.release()
		if status == fieldTooLarge {
			return "", status
		}
		return "", fieldInvalid
	}
	if err := transient.grants.MoveTo(&resources.grants); err != nil {
		transient.release()
		resources.err = err
		return "", fieldInvalid
	}
	return value, fieldValid
}

func (a googleAdapter) errorInfoReason(
	document jsonDocument,
	providerError jsonValue,
	resources *resourceContext,
) (string, fieldStatus) {
	details, ok := document.objectField(providerError, "details")
	if !ok || details.kind != jsonArray {
		return "", fieldAbsent
	}
	reason, status := "", fieldAbsent
	document.arrayValues(details, func(detail jsonValue) bool {
		if detail.kind != jsonObject {
			return true
		}
		direct, _ := exactStringFieldEquals(document, detail, "@type", googleErrorInfoType, a.limits.TypeBytes)
		url, _ := exactStringFieldEquals(document, detail, "@type", googleErrorInfoURLType, a.limits.TypeBytes)
		if !direct && !url {
			return true
		}
		reason, status = stringField(document, detail, "reason", a.limits.ReasonBytes, resources)
		return false
	})
	return reason, status
}
