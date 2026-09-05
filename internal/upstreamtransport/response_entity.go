package upstreamtransport

import "net/http"

// AllowsBody distinguishes entity bytes from representation metadata. HEAD and
// body-forbidden statuses can describe an encoded representation without sending
// that representation, so their validators/coding must not enter conversion.
func (h ResponseHead) AllowsBody() bool {
	if h.RequestMethod == http.MethodHead {
		return false
	}
	if h.StatusCode >= http.StatusContinue && h.StatusCode < http.StatusOK {
		return false
	}
	switch h.StatusCode {
	case http.StatusNoContent, http.StatusResetContent, http.StatusNotModified:
		return false
	}
	return h.RequestMethod != http.MethodConnect || h.StatusCode < http.StatusOK || h.StatusCode >= http.StatusMultipleChoices
}
