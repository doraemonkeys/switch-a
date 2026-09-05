package codexkeyring

import (
	"bytes"
	"errors"
	"testing"
)

func TestPortableHMACAtomicMergeAndConflict(t *testing.T) {
	source := parseTestKeyring(t, testDocument(t, "h2", "a2"), bytes.NewReader(bytes.Repeat([]byte{1}, 256)))
	targetDocument := `{"schema_version":1,"hmac":{"current":"h1","keys":{"h1":"` + testMaterial(1) + `"}},"aead":{"current":"a1","keys":{"a1":"` + testMaterial(11) + `"}}}`
	target := parseTestKeyring(t, targetDocument, bytes.NewReader(bytes.Repeat([]byte{2}, 256)))
	material := source.ExportHMAC()
	expected, err := source.Sign(HMACClientScope, []byte("client"))
	if err != nil {
		t.Fatal(err)
	}
	sentinel := errors.New("database commit failed")
	err = target.WithHMACImport(material, func(view *Keyring) error {
		if err := view.Verify(HMACClientScope, []byte("client"), expected); err != nil {
			t.Fatal(err)
		}
		return sentinel
	})
	if !errors.Is(err, sentinel) {
		t.Fatal(err)
	}
	if err := target.Verify(HMACClientScope, []byte("client"), expected); err == nil {
		t.Fatal("uncommitted keys published")
	}
	for range 2 {
		if err := target.WithHMACImport(material, nil); err != nil {
			t.Fatal(err)
		}
	}
	if err := target.Verify(HMACClientScope, []byte("client"), expected); err != nil {
		t.Fatal(err)
	}
	issued, err := target.Sign(HMACClientScope, []byte("client"))
	if err != nil || issued.Version != "h1" {
		t.Fatal("current issuance changed", issued, err)
	}
	lookups, err := target.LookupDigests(HMACClientScope, []byte("client"))
	if err != nil || len(lookups) != 2 {
		t.Fatal(lookups, err)
	}
	copied := target.ExportHMAC()
	copied[0].Key[0] ^= 1
	if err := target.WithHMACImport(copied, nil); err == nil {
		t.Fatal("different material accepted")
	}
	if err := target.WithHMACImport([]HMACMaterial{{Version: "new", Purpose: HMACClientScope, Key: make([]byte, 32)}}, nil); err == nil {
		t.Fatal("incomplete version accepted")
	}
	if err := target.WithHMACImport([]HMACMaterial{{Version: "invalid!"}}, nil); err == nil {
		t.Fatal("invalid version accepted")
	}
}
