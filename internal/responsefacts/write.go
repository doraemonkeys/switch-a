package responsefacts

// Write records results returned by the gateway's writer, never client receipt.
// For WebSocket, confirmed bytes cover successful whole-message writes only.
type Write struct {
	Calls           uint64 `json:"calls"`
	SuccessfulCalls uint64 `json:"successful_calls"`
	FailedCalls     uint64 `json:"failed_calls"`
	ConfirmedBytes  int64  `json:"confirmed_bytes"`
	LastError       string `json:"last_error,omitempty"`
}

func (w *Write) Record(n int, err error) {
	w.Calls++
	w.ConfirmedBytes += int64(n)
	if err != nil {
		w.FailedCalls++
		w.LastError = err.Error()
	} else {
		w.SuccessfulCalls++
	}
}
