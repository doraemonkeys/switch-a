package clientdisguise

const (
	clientTypeDesktop    = "desktop"
	clientTypeTUI        = "tui"
	clientTypeExec       = "exec"
	clientTypeBrowserUse = "browser-use"
	clientTypeDefault    = "cli"
)

// Observations include auxiliary request roles as well as primary clients.
// Browser Use samples remain learnable, but cannot define a login's main client.
var clientTypes = []struct {
	name       string
	originator string
	sourceURL  string
}{
	{clientTypeDesktop, "Codex Desktop", builtinSourceURL},
	{clientTypeTUI, "codex-tui", builtinTUISourceURL},
	{clientTypeExec, "codex_exec", builtinExecSourceURL},
	{clientTypeBrowserUse, "codex-browser-use", builtinBrowserUseSourceURL},
	{clientTypeDefault, "codex_cli_rs", builtinSourceURL},
}

// Browser Use can arrive before its parent client. Its host is useful evidence,
// but its entry point must never become the login's ordinary request identity.
func (t Tuple) PrimaryClient() bool {
	return t.Valid() && t.ClientType != clientTypeBrowserUse
}

func validClientType(value string) bool {
	for _, client := range clientTypes {
		if client.name == value {
			return true
		}
	}
	return false
}
