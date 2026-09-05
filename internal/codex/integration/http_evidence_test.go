package codexintegration_test

import codexheaders "github.com/doraemonkeys/switch-a/internal/codex/headers"

func testHTTPClientEvidence(wire, semantic []byte) codexheaders.ClientEvidence {
	if len(wire) == 0 {
		return codexheaders.ClientEvidence{}
	}
	return codexheaders.InspectClientPayload(wire, semantic).ClientEvidence()
}
