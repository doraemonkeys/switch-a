package providercookie

import (
	"crypto/sha256"
	"testing"

	"github.com/doraemonkeys/switch-a/internal/codex/identity"
)

func mustJarID(t *testing.T, label string) JarID {
	t.Helper()
	digest := sha256.Sum256([]byte("jar:" + label))
	id, err := JarIDFromBytes(digest[:])
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func mustCookieAuthority(t *testing.T, label string) codexidentity.CookieAuthority {
	t.Helper()
	origin, err := codexidentity.ParseOrigin("https://" + label + ".example")
	if err != nil {
		t.Fatal(err)
	}
	subject, err := codexidentity.NewAccountCredentialSubject("account-" + label)
	if err != nil {
		t.Fatal(err)
	}
	authority, err := codexidentity.NewUpstreamAuthority("openai", origin, subject)
	if err != nil {
		t.Fatal(err)
	}
	return authority.CookieAuthority()
}
