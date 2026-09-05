package codexhttp

import codexheaders "github.com/doraemonkeys/switch-a/internal/codex/headers"

func testClientEvidence(wire, semantic []byte) codexheaders.ClientEvidence {
	if len(wire) == 0 {
		return codexheaders.ClientEvidence{}
	}
	return codexheaders.InspectClientPayload(wire, semantic).ClientEvidence()
}
