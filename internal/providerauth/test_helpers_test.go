package providerauth

import (
	"net/http"
	"time"
)

type fixedClock struct {
	now time.Time
}

func (c fixedClock) Now() time.Time {
	return c.now
}

func (fixedClock) NewTicker(d time.Duration) *time.Ticker {
	return time.NewTicker(d)
}

type stubHTTPDoer struct {
	do func(req *http.Request) (*http.Response, error)
}

func (s stubHTTPDoer) Do(req *http.Request) (*http.Response, error) {
	return s.do(req)
}
