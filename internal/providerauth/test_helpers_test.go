package providerauth

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/doraemonkeys/switch-a/internal/providerauth/accountclient"
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

func testAccountOperation(t *testing.T, service *Service, sessionID string) accountclient.Operation {
	t.Helper()
	operation, err := service.clientProfiles.Resolve(context.Background(), sessionID, accountclient.UsageQuery)
	if err != nil {
		t.Fatal(err)
	}
	return operation
}
