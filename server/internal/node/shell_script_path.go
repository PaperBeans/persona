package node

import (
	"runtime"
	"strings"
)

// shellScriptGOOS is the platform the scripts below are quoted for (tests set it).
var shellScriptGOOS = runtime.GOOS

// scriptPath quotes a native path for a script the node hands to a shell as an argument (`<shell> -ic <script>`,
// loginShell): the assistant's launch, the Codex app-server, the model list, the launcher and sign-in checks. Elsewhere
// it is shellQuote. On Windows that shell is Git Bash started by a native process, and the argument boundary eats the
// backslashes shellQuote escapes, leaving an unmatched quote (#5487). There the path goes with forward slashes, which
// Git Bash and Windows programs both read as the same path, and with no backslash in its quoting: an apostrophe is
// written '"'"' (a double quote survives the boundary, as "$@" does).
// Not for commands typed into a terminal (herdr panes): no argument boundary there, launcherCommand quotes as before.
func scriptPath(path string) string {
	if shellScriptGOOS != "windows" {
		return shellQuote(path)
	}
	path = strings.ReplaceAll(path, `\`, "/")
	if !strings.Contains(path, "'") {
		return shellQuote(path) // without a backslash or an apostrophe, its quoting has no backslash
	}
	return "'" + strings.ReplaceAll(path, "'", `'"'"'`) + "'"
}

// launcherForScript is launcherCommand for such a script: its paths (a managed launcher's personad, claude_start_dir)
// quoted with scriptPath.
func (n *Node) launcherForScript(name string) string { return n.launcherCommandQ(name, scriptPath) }

// launcherInDirScript is the login-shell script the launcher and sign-in checks run: launcher name with args, in dir,
// reading nothing from the terminal.
func (n *Node) launcherInDirScript(name, dir, args string) string {
	return "cd " + scriptPath(dir) + " && " + n.launcherForScript(name) + args + " </dev/null"
}

// scriptSettingsFlag is claude's --settings flag for such a script, path quoted with scriptPath.
func scriptSettingsFlag(path string) string { return " --settings " + scriptPath(path) }
