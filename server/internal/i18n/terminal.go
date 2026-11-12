package i18n

import (
	"os"
	"strings"
)

// TerminalEnv is the environment Terminal reads, the first one set wins: an explicit choice for Persona's commands,
// then the POSIX locale's messages category in its own order of precedence.
var TerminalEnv = []string{"PERSONA_LANG", "LC_ALL", "LC_MESSAGES", "LANG"}

// Terminal is the language of what a command line prints for people (docs/protocol/i18n.md "Command line", #5520):
// personad's commands, Persona Link's and the installer's hand-offs. It is read from the environment, not from the
// hub, so a command run over SSH speaks the language of the terminal it runs in. C and POSIX, a language no catalog
// has, and no setting at all are en. Machine-readable output (JSON, RESULT lines) does not go through it.
func Terminal() Lang {
	for _, name := range TerminalEnv {
		if v := strings.TrimSpace(os.Getenv(name)); v != "" {
			return terminalLocale(v)
		}
	}
	return En
}

// terminalLocale is the language of a POSIX locale (language[_territory][.codeset][@modifier], zh_CN.UTF-8) or of a
// BCP 47 tag or catalog code (PERSONA_LANG=zh-Hans).
func terminalLocale(v string) Lang {
	v, _, _ = strings.Cut(v, ".")
	v, _, _ = strings.Cut(v, "@")
	if v == "C" || v == "POSIX" {
		return En
	}
	return Resolve(v).Or(En)
}
