package clientdisguise

const (
	clientTypeDesktop    = "desktop"
	clientTypeTUI        = "tui"
	clientTypeExec       = "exec"
	clientTypeBrowserUse = "browser-use"
	clientTypeDefault    = "cli"
)

// Each app-server caller is a separate entry point. Browser Use shares the
// Codex protocol with the CLI, but its caller identity is part of the observed
// UA and must remain selectable independently.
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
