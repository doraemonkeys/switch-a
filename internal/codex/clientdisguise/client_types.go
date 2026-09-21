package clientdisguise

const (
	clientTypeDesktop    = "desktop"
	clientTypeTUI        = "tui"
	clientTypeExec       = "exec"
	clientTypeBrowserUse = "browser-use"
	clientTypeDefault    = "cli"
)

// These identities describe profile samples and selectable primary clients.
// Request roles are resolved separately: a Browser Use request also exists
// within a Desktop or CLI target environment.
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

func validClientType(value string) bool {
	for _, client := range clientTypes {
		if client.name == value {
			return true
		}
	}
	return false
}
