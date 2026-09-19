package clientdisguise

const (
	clientTypeDesktop = "desktop"
	clientTypeTUI     = "tui"
	clientTypeExec    = "exec"
	clientTypeDefault = "cli"
)

// CLI is the product containing both interactive and exec entry points. Its
// fallback identity must remain distinct so profiles never turn exec into TUI.
var clientTypes = []struct {
	name       string
	originator string
	sourceURL  string
}{
	{clientTypeDesktop, "Codex Desktop", builtinSourceURL},
	{clientTypeTUI, "codex-tui", builtinTUISourceURL},
	{clientTypeExec, "codex_exec", builtinExecSourceURL},
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
