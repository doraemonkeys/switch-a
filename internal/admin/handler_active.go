package admin

import (
	"net/http"
)

// ActiveRequestsResponse represents the response for active requests API.
type ActiveRequestsResponse struct {
	Requests []ActiveRequest `json:"requests"`
	Count    int             `json:"count"`
}

// GetActiveRequests handles GET /admin/api/requests/active.
// Returns a list of currently active (in-flight) requests.
func (h *Handler) GetActiveRequests(w http.ResponseWriter, r *http.Request) {
	if h.activeReqList == nil {
		// No active request registry configured
		writeJSON(w, http.StatusOK, ActiveRequestsResponse{
			Requests: []ActiveRequest{},
			Count:    0,
		})
		return
	}

	requests := h.activeReqList.List()

	// Convert from proxy.ActiveRequest to admin.ActiveRequest
	result := make([]ActiveRequest, len(requests))
	copy(result, requests)

	writeJSON(w, http.StatusOK, ActiveRequestsResponse{
		Requests: result,
		Count:    len(result),
	})
}
