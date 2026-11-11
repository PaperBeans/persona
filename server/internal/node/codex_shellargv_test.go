package node

import "testing"

// The App's command line loses the login shell around it as a rollout's argv does, the command unquoted as a POSIX shell
// would; a command line that is not one shell word after the flag is kept whole.
func TestCodexShellArgv(t *testing.T) {
	for in, want := range map[string]string{
		`/bin/bash -lc 'git status'`:              `git status`,
		`/bin/zsh -lc 'echo '\''hi'\'''`:          `echo 'hi'`,
		`bash -c "ls -la"`:                        `ls -la`,
		`/bin/bash -lc "echo \"\$HOME\" 'x'"`:     `echo "$HOME" 'x'`,
		`/bin/bash -lc 'printf '\''a\nb'\'''`:     `printf 'a\nb'`,
		"/bin/bash -lc \"cat <<'EOF'\nhi\nEOF\"":  "cat <<'EOF'\nhi\nEOF",
		`/bin/bash -lc 'echo $HOME '"'a' "'$PWD'`: `echo $HOME 'a' $PWD`,
		`git status`:                         `git status`,
		`/usr/bin/env python3 -c 'print(1)'`: `/usr/bin/env python3 -c 'print(1)'`,
		`/bin/bash -lc 'unterminated`:        `/bin/bash -lc 'unterminated`,
		`/bin/bash -lc 'two' words`:          `/bin/bash -lc 'two' words`,
		`/usr/bin/python3 -c 'print(1)'`:     `/usr/bin/python3 -c 'print(1)'`,
		`ssh -c aes256-ctr`:                  `ssh -c aes256-ctr`,
		`/opt/homebrew/bin/fish -c 'ls'`:     `ls`,
	} {
		argv := codexShellArgv(in)
		if got := argv[len(argv)-1]; got != want {
			t.Errorf("codexShellArgv(%q) = %q, want last %q", in, argv, want)
		}
	}
}
