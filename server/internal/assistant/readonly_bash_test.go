package assistant

import (
	"context"
	"encoding/json"
	"testing"
	"testing/synctest"
	"time"

	"github.com/nwverse/persona/server/internal/assistantwire"
	"github.com/nwverse/persona/server/internal/proto"
)

func TestReadonlyTaskSafeBashClassifier(t *testing.T) {
	const cwd = "/code/app"
	allowed := []struct {
		name string
		cmd  string
	}{
		{"git show", `git show HEAD`},
		{"git show stat", `git show --stat abc1234`},
		{"git diff", `git diff origin/main...HEAD -- server/internal/assistant`},
		{"git diff quoted pathspec", `git diff -- '*.go'`},
		{"git log", `git log -n 20 --oneline`},
		{"git log quoted pathspec", `git log -- 'server/*.go'`},
		{"git status", `git status -sb`},
		{"git no pager and -C cwd", `git --no-pager -C /code/app diff --stat`},
		{"git -C relative subdir", `git -C server log -n 5`},
		{"git grep", `git grep -n normalizeReadonlyPermission`},
		{"git grep -ne pattern", `git grep -nefoo`},
		{"git grep -e literal -O", `git grep -e -O`},
		{"git grep context and regex", `git grep -C 2 "foo.*bar"`},
		{"git grep after-context and single-quoted dollar", `git grep -A 3 'end$'`},
		{"git grep max-depth and regex", `git grep --max-depth 2 "foo[0-9]"`},
		{"git grep quoted pathspec", `git grep foo -- '*.go'`},
		{"gh pr view", `gh pr view 123 --json title,body,files`},
		{"gh pr view repo attached", `gh pr view 123 -Rowner/repo`},
		{"gh pr diff", `gh pr diff 123`},
		{"gh pr list", `gh pr list --state open --limit 10`},
		{"gh pr checks", `gh pr checks 123`},
		{"gh issue view", `gh issue view 456 --json title,body`},
		{"rg relative", `rg -n "foo..bar" server/internal/assistant`},
		{"rg quoted glob and regex", `rg -g "*.go" "foo{1,3}" server/internal/assistant`},
		{"rg attached glob containing f", `rg -g'*.feature' foo server/internal/assistant`},
		{"rg attached glob containing e", `rg -g'*test*' 'foo.*bar' server/internal/assistant`},
		{"rg single-quoted dollar", `rg -n 'end$' server/internal/assistant`},
		{"rg color separate value", `rg --color never -n "test" server/internal/assistant`},
		{"rg context separate value", `rg --context 3 -n "test" server/internal/assistant`},
		{"rg files relative dir", `rg --files server/internal/assistant`},
		{"rg with -e and cwd path", `rg -e "pattern" /code/app/server`},
		{"grep relative", `grep -n -i "test" README.md`},
		{"grep color equals", `grep --color=auto -n "test" README.md`},
		{"grep context equals", `grep --context=3 -n "test" README.md`},
		{"cat relative", `cat server/internal/assistant/tasks_placement.go`},
		{"cat inside cwd", `cat /code/app/README.md`},
		{"ls relative", `ls -la server/internal/assistant`},
		{"ls color equals", `ls --color=never -la server/internal/assistant`},
		{"pipeline git diff head", `git diff HEAD~1 | head -n 50`},
		{"pipeline git log grep stderr", `git log -n 20 --oneline 2>&1 | grep -i fix`},
		{"chained read-only", `git status -sb && git diff --stat`},
		{"pwd", `pwd`},
		{"wc", `wc -l server/internal/assistant/tasks_placement.go`},
	}
	for _, tc := range allowed {
		t.Run("allow/"+tc.name, func(t *testing.T) {
			raw, _ := json.Marshal(map[string]string{"command": tc.cmd})
			if !readonlyTaskSafeBash(raw, cwd) {
				t.Fatalf("expected safe read-only bash to be allowed: %q", tc.cmd)
			}
		})
	}

	blocked := []struct {
		name string
		cmd  string
	}{
		{"empty", ``},
		{"whitespace", `   `},
		{"git commit", `git commit -m "wip"`},
		{"git push", `git push origin main`},
		{"git checkout", `git checkout main`},
		{"git reset", `git reset --hard HEAD`},
		{"git -c pager injection", `git -c core.pager='touch /tmp/pwn' log`},
		{"git config-env", `git --config-env=core.pager=X log`},
		{"git ext-diff", `git diff --ext-diff`},
		{"git textconv", `git diff --textconv`},
		{"git no-index", `git diff --no-index /etc/passwd /dev/null`},
		{"git output file", `git diff --output=patch.diff`},
		{"git -C outside cwd", `git -C /etc status`},
		{"git -C traversal", `git -C ../other status`},
		{"git grep -f", `git grep -f .env`},
		{"git grep -f attached", `git grep -f.env .`},
		{"git grep -nf bundle", `git grep -nf .env .`},
		{"git grep -O pager rce", `git grep -O 'touch /tmp/pwn #' package`},
		{"git grep -O attached", `git grep -Osh package`},
		{"git grep -nO bundle", `git grep -nO 'sh -c id' package`},
		{"git grep open-files-in-pager", `git grep --open-files-in-pager='sh -c "id > /tmp/pwn"' package`},
		{"git grep open-files-in-pager bare", `git grep --open-files-in-pager package`},
		{"git grep open-files-in-pager abbrev", `git grep --op='sh -c id' package`},
		{"git grep no-index abbrev", `git grep --no-ind . /etc/passwd`},
		{"git grep untracked", `git grep --untracked . .env`},
		{"git grep untracked abbrev", `git grep --unt . .env`},
		{"git grep untracked short abbrev", `git grep --u . .env`},
		{"git grep no-exclude-standard", `git grep --no-exclude-standard . .env`},
		{"git grep no-exclude-standard abbrev", `git grep --no-ex . .env`},
		{"git grep textconv abbrev", `git grep --textc foo`},
		{"git cat-file filters", `git cat-file --filters HEAD:README.md`},
		{"git cat-file filters abbrev", `git cat-file --fi HEAD:README.md`},
		{"git cat-file textconv short abbrev", `git cat-file --t HEAD:README.md`},
		{"git cat-file batch-command abbrev", `git cat-file --batch-co`},
		{"git blame contents", `git blame --contents /etc/passwd README.md`},
		{"git blame contents equals", `git blame --contents=/etc/passwd README.md`},
		{"git blame contents abbrev", `git blame --con /etc/passwd README.md`},
		{"git blame ignore-revs-file", `git blame --ignore-revs-file=/etc/passwd README.md`},
		{"git blame -S revs file", `git blame -S /etc/passwd README.md`},
		{"git blame -nS bundled revs file", `git blame -nS/etc/passwd README.md`},
		{"git diff implicit no-index outside", `git diff /dev/null /etc/passwd`},
		{"git diff implicit no-index env", `git diff /dev/null .env`},
		{"git diff implicit no-index tilde", `git diff /dev/null ~/.ssh/id_rsa`},
		{"git diff dashdash outside", `git diff -- /dev/null /etc/passwd`},
		{"git diff -o attached", `git diff -o/tmp/patch.diff`},
		{"git log -x attached", `git log -xsh`},
		{"git brace expansion flag", `git ""{,-c} core.pager=sh log`},
		{"git diff brace expansion output", `git diff ""{,--output=/tmp/pwn}`},
		{"git diff var expansion", `git diff "$EXTRA_FLAGS"`},
		{"git ansi-c quoting", `git $'\x2dc' core.pager=sh log`},
		{"git diff ansi-c output", `git diff $'\x2d\x2doutput=/tmp/pwn'`},
		{"double-quoted backslash newline rg pre", "rg \"--pr\\\ne=sh\" foo ."},
		{"double-quoted backslash newline cat abs", "cat \"\\\n/etc/passwd\""},
		{"double-quoted backslash newline cat env", "cat \".\\\nenv\""},
		{"inline env override", `GIT_PAGER=sh git log`},
		{"env wrapper", `env git log`},
		{"output redirect", `git show HEAD > patch.diff`},
		{"append redirect", `git log >> out.txt`},
		{"command substitution", `git show $(touch /tmp/pwn)`},
		{"backtick substitution", "git show `id`"},
		{"heredoc", "cat <<EOF\nhi\nEOF"},
		{"subshell", `(git status)`},
		{"chained write", `git diff && rm -rf /tmp/x`},
		{"gh pr merge", `gh pr merge 123 --squash`},
		{"gh pr comment", `gh pr comment 123 --body "ok"`},
		{"gh pr review", `gh pr review 123 --approve`},
		{"gh pr view web", `gh pr view 123 --web`},
		{"gh pr view web equals", `gh pr view 123 --web=true`},
		{"gh pr view -w equals", `gh pr view 123 -w=true`},
		{"gh pr view -cw bundle", `gh pr view 123 -cw`},
		{"gh pr checks watch", `gh pr checks 123 --watch`},
		{"gh issue create", `gh issue create --title hi`},
		{"gh api", `gh api repos/nwverse/persona`},
		{"rm", `rm -rf build`},
		{"sed inplace", `sed -i 's/a/b/' file.go`},
		{"awk", `awk '{print}' file.go`},
		{"rg pre hook", `rg --pre sh foo .`},
		{"rg hostname-bin", `rg --hostname-bin=env --hyperlink-format=default . .`},
		{"rg -z", `rg -z foo .`},
		{"rg -f", `rg -f .env .`},
		{"rg -uu hidden no-ignore", `rg -uu . .`},
		{"rg -. --no-ignore-vcs", `rg -. --no-ignore-vcs . .`},
		{"rg --follow symlinks", `rg --follow . .`},
		{"rg -L symlinks", `rg -L . .`},
		{"rg attached -Tmake skips path", `rg -e . -Tmake /etc/passwd`},
		{"rg attached -g ending e skips path", `rg -e . -g'!e' /etc/passwd`},
		{"rg outside cwd", `rg foo /etc`},
		{"rg files outside cwd", `rg --files /etc`},
		{"rg files home tilde", `rg --files ~/.ssh`},
		{"rg no pattern", `rg -n`},
		{"grep -f", `grep -f .env README.md`},
		{"grep -R symlinks", `grep -R foo .`},
		{"grep dereference-recursive abbrev", `grep --der foo .`},
		{"grep exclude-from", `grep --exclude-from /etc/passwd foo README.md`},
		{"grep exclude-from abbrev", `grep --exclude-f=/etc/passwd foo README.md`},
		{"grep color skips path", `grep -e . --color /etc/passwd`},
		{"grep color skips tilde path", `grep -e . --color ~/.ssh/id_rsa`},
		{"grep color positional path", `grep --color . /etc/passwd`},
		{"grep context skips path", `grep -e . --context /etc/passwd`},
		{"ls color skips tilde path", `ls --color ~/.ssh`},
		{"ls color skips outside path", `ls -la --color /etc`},
		{"ls bsd -w skips tilde path", `ls -w ~/.ssh`},
		{"ls bsd -I skips tilde path", `ls -I ~/.ssh`},
		{"cat .env", `cat .env`},
		{"cat env glob star", `cat .env*`},
		{"cat env glob qmark", `cat .e?v`},
		{"cat traversal glob", `cat .?/.?/.?/etc/passwd`},
		{"cat home tilde", `cat ~/.persona/hub.json`},
		{"cat tilde user", `cat ~root/.bashrc`},
		{"cat var expansion unquoted", `cat $HOME/.bashrc`},
		{"cat var expansion double-quoted", `cat "$HOME/.bashrc"`},
		{"cat brace expansion midword", `cat ""{,/}etc/passwd`},
		{"cat traversal", `cat ../../etc/passwd`},
		{"cat outside cwd", `cat /etc/passwd`},
		{"cat credential in cwd", `cat /code/app/.env.local`},
		{"cat pem key", `cat certs/server.pem`},
		{"tail follow", `tail -f server.log`},
		{"tail long follow", `tail --follow server.log`},
		{"tail follow abbrev", `tail --f server.log`},
		{"tail pid abbrev", `tail --p=1 server.log`},
		{"wc files0-from", `wc --files0-from=/etc/passwd`},
		{"wc files0-from abbrev", `wc --f=/etc/passwd`},
		{"unbalanced quote", `git log --grep="unclosed`},
	}
	for _, tc := range blocked {
		t.Run("block/"+tc.name, func(t *testing.T) {
			raw, _ := json.Marshal(map[string]string{"command": tc.cmd})
			if readonlyTaskSafeBash(raw, cwd) {
				t.Fatalf("expected unsafe bash to be blocked: %q", tc.cmd)
			}
		})
	}

	if readonlyTaskSafeBash(nil, cwd) {
		t.Fatal("nil ToolInput must not be allowed")
	}
	if readonlyTaskSafeBash(json.RawMessage(`{"command":"git status"}`), "/home/user/.persona") {
		t.Fatal("guarded cwd must not be allowed")
	}
}

func TestReadonlyTaskSafeBashNoCard(t *testing.T) {
	t.Run("safe readonly bash on agy readonly task needs no card", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			s, f := readonlyTaskService(t)
			f.launcherList = companionLaunchers()
			setSettings(t, s, func(v *Settings) { v.Main = "readonly-chat" })
			task := readonlyTestTask("")
			task.Launcher = "agy"
			if _, err := s.toolTaskPlan(context.Background(), "readonly-chat", readonlyTaskArgs(t, task)); err != nil {
				t.Fatal(err)
			}
			synctest.Wait()
			todo := onlyTodo(s)
			if len(todo.Agents) != 1 {
				t.Fatalf("expected dispatched agy agent: %+v", todo)
			}
			agent := todo.Agents[0]
			for _, cmd := range []string{
				"git show HEAD",
				"git diff origin/main...HEAD",
				"git log -n 5 --oneline",
				"gh pr view 123 --json title,body",
				"rg -n readonly server/internal/assistant",
				"git diff | head -n 40",
			} {
				raw, _ := json.Marshal(map[string]string{"command": cmd})
				ix := proto.Interaction{
					ID: "ro-bash-" + cmd, MachineID: agent.MachineID, SessionID: agent.SessionID,
					Cwd: "/code/app", Kind: proto.IxPermission, ToolName: "Bash", ToolInput: raw,
				}
				ctx, cancel := context.WithTimeout(context.Background(), time.Second)
				r, err := s.nodeInteraction(ctx, ix)
				cancel()
				got, _ := json.Marshal(r)
				if err != nil || string(got) != `{"allow":true}` {
					t.Fatalf("cmd %q: got %s, err=%v, want single-call allow", cmd, got, err)
				}
			}
			if cards := s.workCards(); len(cards) != 0 {
				t.Fatalf("safe readonly bash created cards: %+v", cards)
			}
			f.mu.Lock()
			defer f.mu.Unlock()
			for _, ev := range f.told {
				if ev.event == assistantwire.EventFleetInteraction {
					t.Fatal("safe readonly bash emitted a user permission card")
				}
			}
		})
	})

	t.Run("unsafe bash, writable tasks and inbox origin still ask", func(t *testing.T) {
		for _, tc := range []struct {
			name     string
			readonly bool
			inbox    bool
			cmd      string
		}{
			{"git commit", true, false, `git commit -m "x"`},
			{"gh pr merge", true, false, `gh pr merge 123 --squash`},
			{"redirect write", true, false, `git show HEAD > /tmp/out`},
			{"credential cat", true, false, `cat .env`},
			{"empty command", true, false, ``},
			{"writable task git diff", false, false, `git diff`},
			{"inbox origin git diff", true, true, `git diff`},
		} {
			t.Run(tc.name, func(t *testing.T) {
				synctest.Test(t, func(t *testing.T) {
					s, f := readonlyTaskService(t)
					f.launcherList = companionLaunchers()
					setSettings(t, s, func(v *Settings) { v.Main = "readonly-chat" })
					task := readonlyTestTask("")
					task.Launcher, task.Readonly = "agy", tc.readonly
					ctx := context.Background()
					if _, err := s.toolTaskPlan(ctx, "readonly-chat", readonlyTaskArgs(t, task)); err != nil {
						t.Fatal(err)
					}
					synctest.Wait()
					if !tc.readonly {
						plan := readonlyFeedPlan(t, s, "readonly-chat")
						if _, err := s.startPlan(ctx, plan.ID, nil, false); err != nil {
							t.Fatal(err)
						}
						synctest.Wait()
					}
					agent := onlyTodo(s).Agents[0]
					var raw json.RawMessage
					if tc.cmd != "" {
						raw, _ = json.Marshal(map[string]string{"command": tc.cmd})
					}
					ix := proto.Interaction{
						ID: "guarded-bash", MachineID: agent.MachineID, SessionID: agent.SessionID,
						Cwd: "/code/app", Kind: proto.IxPermission, ToolName: "Bash", ToolInput: raw,
					}
					callCtx, cancel := context.WithCancel(ctx)
					defer cancel()
					if tc.inbox {
						callCtx = withInteractionOrigin(callCtx, OriginInbox)
					}
					done := make(chan proto.Resolution, 1)
					go func() {
						r, _ := s.nodeInteraction(callCtx, ix)
						done <- r
					}()
					synctest.Wait()
					select {
					case r := <-done:
						t.Fatalf("guarded bash %q returned directly: %+v", tc.cmd, r)
					default:
					}
					s.fleet.mu.Lock()
					pending := s.fleet.interactions[ix.ID] != nil
					s.fleet.mu.Unlock()
					if !pending {
						t.Fatalf("guarded bash %q did not register an approval card", tc.cmd)
					}
					cancel()
					synctest.Wait()
					if r := <-done; r.Allow != nil || r.Approve != nil {
						t.Fatalf("cancellation granted permissions: %+v", r)
					}
				})
			})
		}
	})
}
