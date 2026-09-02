package adapters

import (
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"
	"unsafe"

	"github.com/doraemonkeys/switch-a/internal/responseanalysis/allocation"
	"github.com/doraemonkeys/switch-a/internal/responseanalysis/framing"
	"github.com/doraemonkeys/switch-a/internal/responseanalysis/tokenusage"
)

const (
	maxUsageServiceTierBytes = 256
	semanticFieldsBytes      = int(unsafe.Sizeof(SemanticFields{}))
	tokenUsageBytes          = int(unsafe.Sizeof(tokenusage.TokenUsage{}))
	cacheCreationBytes       = int(unsafe.Sizeof(tokenusage.CacheCreation{}))
)

type fieldStatus uint8

const (
	fieldAbsent fieldStatus = iota
	fieldValid
	fieldInvalid
	fieldTooLarge
)

type resourceContext struct {
	reserver allocation.Reserver
	grants   allocation.Bundle
	err      error
}

func (r *resourceContext) reserve(capacity int) bool {
	if capacity == 0 {
		return true
	}
	if r == nil || r.err != nil {
		return false
	}
	if r.reserver == nil {
		r.err = allocation.ErrNilReserver
		return false
	}
	grant, err := r.reserver.Reserve(allocation.ClassSemanticFields, capacity)
	if err != nil {
		r.err = err
		return false
	}
	if grant == nil {
		r.err = allocation.ErrNilGrant
		return false
	}
	if err := r.grants.Add(grant); err != nil {
		grant.Release()
		r.err = err
		return false
	}
	return true
}

func (r *resourceContext) finish(result Result) Result {
	if r == nil {
		return Result{Class: EventFailOpen, Failure: framing.FailureInternal}
	}
	if r.err != nil {
		r.grants.Release()
		return Result{Class: EventFailOpen, AllocationError: r.err}
	}
	result.resources = r.grants.Take()
	return result
}

func (r *resourceContext) release() {
	if r != nil {
		r.grants.Release()
	}
}

func objectField(document jsonDocument, parent jsonValue, name string) (jsonValue, bool) {
	value, ok := document.objectField(parent, name)
	return value, ok && value.kind == jsonObject
}

func fieldHasKind(document jsonDocument, object jsonValue, name string, kinds ...jsonKind) bool {
	value, ok := document.objectField(object, name)
	if !ok {
		return false
	}
	return slices.Contains(kinds, value.kind)
}

func stringField(document jsonDocument, object jsonValue, name string, maxBytes int, resources *resourceContext) (string, fieldStatus) {
	raw, ok := document.objectField(object, name)
	if !ok {
		return "", fieldAbsent
	}
	return trimmedString(document.data, raw, maxBytes, resources)
}

func usageIdentifierField(document jsonDocument, object jsonValue, name string, maxBytes int, resources *resourceContext) (string, fieldStatus) {
	raw, ok := document.objectField(object, name)
	if !ok {
		return "", fieldAbsent
	}
	return canonicalUsageString(document.data, raw, maxBytes, resources)
}

func scalarField(document jsonDocument, object jsonValue, name string, maxBytes int, resources *resourceContext) (string, fieldStatus) {
	raw, ok := document.objectField(object, name)
	if !ok {
		return "", fieldAbsent
	}
	if raw.kind == jsonString {
		return trimmedString(document.data, raw, maxBytes, resources)
	}
	return canonicalNumber(document.data, raw, maxBytes, resources)
}

func preferredStringField(
	document jsonDocument,
	primary jsonValue,
	primaryName string,
	fallback jsonValue,
	fallbackName string,
	maxBytes int,
	resources *resourceContext,
) (string, fieldStatus) {
	value, status := stringField(document, primary, primaryName, maxBytes, resources)
	if status == fieldValid || status == fieldTooLarge {
		return value, status
	}
	return stringField(document, fallback, fallbackName, maxBytes, resources)
}

func preferredScalarField(
	document jsonDocument,
	primary jsonValue,
	primaryName string,
	fallback jsonValue,
	fallbackName string,
	maxBytes int,
	resources *resourceContext,
) (string, fieldStatus) {
	value, status := scalarField(document, primary, primaryName, maxBytes, resources)
	if status == fieldValid || status == fieldTooLarge {
		return value, status
	}
	return scalarField(document, fallback, fallbackName, maxBytes, resources)
}

func exactStringFieldEquals(document jsonDocument, object jsonValue, name, expected string, maxBytes int) (bool, fieldStatus) {
	raw, ok := document.objectField(object, name)
	if !ok {
		return false, fieldAbsent
	}
	if raw.kind != jsonString {
		return false, fieldInvalid
	}
	length := 0
	valid := walkJSONString(document.data, raw, func(current rune) {
		length += utf8.RuneLen(current)
	})
	if !valid {
		return false, fieldInvalid
	}
	if length > maxBytes {
		return false, fieldTooLarge
	}
	return decodedStringEquals(document.data, raw, expected), fieldValid
}

// canonicalNumberForm retains the source slices and normalized decimal shape.
// Code fields are identifiers, so the renderer avoids float64 precision loss
// and exponent aliases while producing one bounded matching value.
type canonicalNumberForm struct {
	raw          []byte
	digitsStart  int
	mantissaEnd  int
	firstNonZero int
	lastNonZero  int
	point        int
	negative     bool
}

func canonicalNumber(data []byte, value jsonValue, maxBytes int, resources *resourceContext) (string, fieldStatus) {
	form, ok := parseCanonicalNumberForm(data, value)
	if !ok {
		return "", fieldInvalid
	}
	if form.firstNonZero < 0 {
		if maxBytes < 1 {
			return "", fieldTooLarge
		}
		return "0", fieldValid
	}

	exponent, ok := boundedExponent(form.raw[form.mantissaEnd:], len(form.raw)+maxBytes+1)
	if !ok {
		return "", fieldTooLarge
	}
	form.point += exponent - form.firstNonZero
	significantLength := form.lastNonZero - form.firstNonZero + 1
	length := canonicalNumberLength(form.point, significantLength, form.negative)
	if length > maxBytes {
		return "", fieldTooLarge
	}
	if !resources.reserve(length) {
		return "", fieldInvalid
	}
	return renderCanonicalNumber(form, significantLength, length), fieldValid
}

func parseCanonicalNumberForm(data []byte, value jsonValue) (canonicalNumberForm, bool) {
	if value.kind != jsonNumber || value.start < 0 || value.start > value.end || value.end > len(data) {
		return canonicalNumberForm{}, false
	}
	raw := data[value.start:value.end]
	if len(raw) == 0 {
		return canonicalNumberForm{}, false
	}
	digitsStart := 0
	negative := raw[0] == '-'
	if negative {
		digitsStart++
	}
	mantissaEnd, point, firstNonZero, lastNonZero := scanCanonicalMantissa(raw, digitsStart)
	return canonicalNumberForm{
		raw:          raw,
		digitsStart:  digitsStart,
		mantissaEnd:  mantissaEnd,
		firstNonZero: firstNonZero,
		lastNonZero:  lastNonZero,
		point:        point,
		negative:     negative,
	}, true
}

func scanCanonicalMantissa(raw []byte, digitsStart int) (mantissaEnd, point, firstNonZero, lastNonZero int) {
	mantissaEnd = len(raw)
	for cursor := digitsStart; cursor < len(raw); cursor++ {
		if raw[cursor] == 'e' || raw[cursor] == 'E' {
			mantissaEnd = cursor
			break
		}
	}

	digitIndex := 0
	firstNonZero, lastNonZero = -1, -1
	hasPoint := false
	for cursor := digitsStart; cursor < mantissaEnd; cursor++ {
		if raw[cursor] == '.' {
			point = digitIndex
			hasPoint = true
			continue
		}
		if raw[cursor] != '0' {
			if firstNonZero < 0 {
				firstNonZero = digitIndex
			}
			lastNonZero = digitIndex
		}
		digitIndex++
	}
	if !hasPoint {
		point = digitIndex
	}
	return mantissaEnd, point, firstNonZero, lastNonZero
}

func canonicalNumberLength(point, significantLength int, negative bool) int {
	length := significantLength
	switch {
	case point <= 0:
		length += 2 - point
	case point >= significantLength:
		length = point
	default:
		length++
	}
	if negative {
		length++
	}
	return length
}

func renderCanonicalNumber(form canonicalNumberForm, significantLength, length int) string {
	var builder strings.Builder
	builder.Grow(length)
	if form.negative {
		builder.WriteByte('-')
	}
	switch {
	case form.point <= 0:
		builder.WriteString("0.")
		writeZeroes(&builder, -form.point)
		writeSignificantDigits(&builder, form, -1)
	case form.point >= significantLength:
		writeSignificantDigits(&builder, form, -1)
		writeZeroes(&builder, form.point-significantLength)
	default:
		writeSignificantDigits(&builder, form, form.point)
	}
	return builder.String()
}

func writeSignificantDigits(builder *strings.Builder, form canonicalNumberForm, point int) {
	logical, written := 0, 0
	for cursor := form.digitsStart; cursor < form.mantissaEnd; cursor++ {
		if form.raw[cursor] == '.' {
			continue
		}
		if logical >= form.firstNonZero && logical <= form.lastNonZero {
			if written == point {
				builder.WriteByte('.')
			}
			builder.WriteByte(form.raw[cursor])
			written++
		}
		logical++
	}
}

func writeZeroes(builder *strings.Builder, count int) {
	for range count {
		builder.WriteByte('0')
	}
}

func boundedExponent(raw []byte, bound int) (int, bool) {
	if len(raw) == 0 {
		return 0, true
	}
	index := 1
	negative := false
	if index < len(raw) && (raw[index] == '+' || raw[index] == '-') {
		negative = raw[index] == '-'
		index++
	}
	value := 0
	for ; index < len(raw); index++ {
		digit := int(raw[index] - '0')
		if value > (bound-digit)/10 {
			return 0, false
		}
		value = value*10 + digit
	}
	if negative {
		value = -value
	}
	return value, true
}

func extractFields(document jsonDocument, object jsonValue, limits Limits, numericType bool, resources *resourceContext) (SemanticFields, bool) {
	var fields SemanticFields
	var typeStatus fieldStatus
	if numericType {
		fields.Type, typeStatus = scalarField(document, object, "type", limits.TypeBytes, resources)
	} else {
		fields.Type, typeStatus = stringField(document, object, "type", limits.TypeBytes, resources)
	}
	if typeStatus == fieldTooLarge {
		return SemanticFields{}, true
	}
	fields.preserveEmptyPresence(fields.Type, typeStatus, presenceType)

	var codeStatus fieldStatus
	if fields.Code, codeStatus = scalarField(document, object, "code", limits.CodeBytes, resources); codeStatus == fieldTooLarge {
		return SemanticFields{}, true
	}
	fields.preserveEmptyPresence(fields.Code, codeStatus, presenceCode)

	var messageStatus fieldStatus
	if fields.Message, messageStatus = stringField(document, object, "message", limits.MessageBytes, resources); messageStatus == fieldTooLarge {
		return SemanticFields{}, true
	}
	fields.preserveEmptyPresence(fields.Message, messageStatus, presenceMessage)

	var reasonStatus fieldStatus
	if fields.Reason, reasonStatus = stringField(document, object, "reason", limits.ReasonBytes, resources); reasonStatus == fieldTooLarge {
		return SemanticFields{}, true
	}
	fields.preserveEmptyPresence(fields.Reason, reasonStatus, presenceReason)
	return fields, false
}

func errorResult(fields SemanticFields, tooLarge bool, usage *tokenusage.TokenUsage, resources *resourceContext) Result {
	if tooLarge {
		return resources.finish(Result{Class: EventFailOpen, Failure: framing.FailureSemanticFieldTooLarge})
	}
	if !resources.reserve(semanticFieldsBytes) {
		return resources.finish(Result{})
	}
	return resources.finish(Result{Class: EventError, Fields: &fields, Usage: usage})
}

func usageFromObject(document jsonDocument, usage jsonValue, google bool, resources *resourceContext) *tokenusage.TokenUsage {
	if usage.kind != jsonObject {
		return nil
	}
	if google {
		prompt := usageInteger(document, usage, "promptTokenCount", resources)
		completion := usageInteger(document, usage, "candidatesTokenCount", resources)
		total := usageInteger(document, usage, "totalTokenCount", resources)
		cacheRead := usageInteger(document, usage, "cachedContentTokenCount", resources)
		if !anyUsageCountPresent(prompt, completion, total, cacheRead) {
			return nil
		}
		if !resources.reserve(tokenUsageBytes) {
			return nil
		}
		return &tokenusage.TokenUsage{
			PromptTokens:         prompt,
			CompletionTokens:     completion,
			TotalTokens:          total,
			CacheReadInputTokens: cacheRead,
		}
	}

	prompt := usageInteger(document, usage, "prompt_tokens", resources)
	if !prompt.Present {
		prompt = usageInteger(document, usage, "input_tokens", resources)
	}
	completion := usageInteger(document, usage, "completion_tokens", resources)
	if !completion.Present {
		completion = usageInteger(document, usage, "output_tokens", resources)
	}
	total := usageInteger(document, usage, "total_tokens", resources)
	cacheRead := usageInteger(document, usage, "cache_read_input_tokens", resources)
	if !cacheRead.Present {
		cacheRead = firstNestedUsageInteger(document, usage, "cached_tokens", resources, "prompt_tokens_details", "input_tokens_details", "input_token_details")
	}
	reasoning := usageInteger(document, usage, "reasoning_tokens", resources)
	if !reasoning.Present {
		reasoning = firstNestedUsageInteger(document, usage, "reasoning_tokens", resources, "completion_tokens_details", "output_tokens_details", "output_token_details")
	}
	cacheCreationInput := usageInteger(document, usage, "cache_creation_input_tokens", resources)
	if !cacheCreationInput.Present {
		cacheCreationInput = firstNestedUsageInteger(document, usage, "cache_write_tokens", resources, "prompt_tokens_details", "input_tokens_details", "input_token_details")
	}
	cacheCreationObject, nested := objectField(document, usage, "cache_creation")
	ephemeral1h := tokenusage.ObservedCount{}
	ephemeral5m := tokenusage.ObservedCount{}
	if nested {
		ephemeral1h = usageInteger(document, cacheCreationObject, "ephemeral_1h_input_tokens", resources)
		ephemeral5m = usageInteger(document, cacheCreationObject, "ephemeral_5m_input_tokens", resources)
	}
	if !anyUsageCountPresent(prompt, completion, total, cacheRead, reasoning, cacheCreationInput, ephemeral1h, ephemeral5m) {
		return nil
	}
	if !resources.reserve(tokenUsageBytes) {
		return nil
	}
	result := &tokenusage.TokenUsage{
		PromptTokens:         prompt,
		CompletionTokens:     completion,
		TotalTokens:          total,
		ReasoningTokens:      reasoning,
		CacheReadInputTokens: cacheRead,
	}
	// Service tiers are categorical accounting identifiers, not provider
	// display text. Canonical casing prevents aliases from disappearing when
	// usage from multiple events is merged.
	if serviceTier, status := usageIdentifierField(document, usage, "service_tier", maxUsageServiceTierBytes, resources); status == fieldValid {
		result.ServiceTier = serviceTier
	}
	if anyUsageCountPresent(cacheCreationInput, ephemeral1h, ephemeral5m) {
		if !resources.reserve(cacheCreationBytes) {
			return nil
		}
		result.CacheCreation = &tokenusage.CacheCreation{
			InputTokens:            cacheCreationInput,
			Ephemeral1hInputTokens: ephemeral1h,
			Ephemeral5mInputTokens: ephemeral5m,
		}
	}
	return result
}

func anyUsageCountPresent(counts ...tokenusage.ObservedCount) bool {
	for _, count := range counts {
		if count.Present {
			return true
		}
	}
	return false
}

func usageInteger(document jsonDocument, object jsonValue, name string, resources *resourceContext) tokenusage.ObservedCount {
	if resources == nil || resources.err != nil {
		return tokenusage.ObservedCount{}
	}
	value, ok := document.objectField(object, name)
	if !ok || value.kind != jsonNumber {
		return tokenusage.ObservedCount{}
	}
	transient := resourceContext{reserver: resources.reserver}
	canonical, status := canonicalNumber(document.data, value, 20, &transient)
	if status != fieldValid || strings.ContainsRune(canonical, '.') {
		if transient.err != nil && resources.err == nil {
			resources.err = transient.err
		}
		transient.release()
		return tokenusage.ObservedCount{}
	}
	number, err := strconv.ParseInt(canonical, 10, 64)
	if transient.err != nil && resources.err == nil {
		resources.err = transient.err
	}
	transient.release()
	if err != nil {
		return tokenusage.ObservedCount{}
	}
	return tokenusage.ObservedCount{Value: number, Present: true}
}

func firstNestedUsageInteger(document jsonDocument, usage jsonValue, child string, resources *resourceContext, parents ...string) tokenusage.ObservedCount {
	for _, parent := range parents {
		object, ok := objectField(document, usage, parent)
		if !ok {
			continue
		}
		if value := usageInteger(document, object, child, resources); value.Present {
			return value
		}
	}
	return tokenusage.ObservedCount{}
}

func standardRootUsage(document jsonDocument, resources *resourceContext) *tokenusage.TokenUsage {
	if usage, ok := objectField(document, document.root, "usage"); ok {
		return usageFromObject(document, usage, false, resources)
	}
	return nil
}

func googleRootUsage(document jsonDocument, resources *resourceContext) *tokenusage.TokenUsage {
	if usage, ok := objectField(document, document.root, "usageMetadata"); ok {
		return usageFromObject(document, usage, true, resources)
	}
	return nil
}

func nestedUsage(document jsonDocument, parentName string, resources *resourceContext) *tokenusage.TokenUsage {
	parent, ok := objectField(document, document.root, parentName)
	if !ok {
		return nil
	}
	usage, ok := objectField(document, parent, "usage")
	if !ok {
		return nil
	}
	return usageFromObject(document, usage, false, resources)
}
