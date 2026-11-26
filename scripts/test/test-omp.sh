#!/bin/bash
# Live check of omp (Oh My Pi) in herdr (docs/integrations/omp.md): an omp in a herdr pane with Persona's extension is
# listed by the node with that pane — what a quick check after an omp or herdr upgrade needs. Its own herdr session, its
# own HOME, config and persona directory (a fresh temp directory): the user's herdr sessions and ~/.omp are not touched,
# and no message is sent to omp (no model request).
#
#   ./scripts/test/test-omp.sh
#
# Needs herdr, omp, Go and Python 3. Prints one RESULT line: ok, fail, or skip (a tool is missing, exit 0).
set -uo pipefail
cd "$(dirname "$0")/../.."

for tool in herdr omp go python3; do
  command -v "$tool" >/dev/null || { echo "RESULT skip: $tool is not installed"; exit 0; }
done

SESSION="persona-test-$$"
export HERDR_SESSION="$SESSION" # never inherit the pane or server the caller was using
unset PI_CODING_AGENT_DIR HERDR_CONFIG_PATH HERDR_SOCKET_PATH # omp and herdr must use only the fresh directories below
# Go's caches stay the user's: the temp HOME below would otherwise download and build everything again.
export GOMODCACHE="$(go env GOMODCACHE)" GOCACHE="$(go env GOCACHE)" GOPATH="$(go env GOPATH)"
WORK=$(mktemp -d /tmp/persona-omp-XXXX) || { echo "RESULT fail: no temp directory"; exit 1; }
export HOME="$WORK/home" XDG_CONFIG_HOME="$WORK/config" PERSONA_HOME="$WORK/persona"
export PERSONA_RUNTIME_SOCKET="$WORK/runtime.sock"
SOCK="$XDG_CONFIG_HOME/herdr/sessions/$SESSION/herdr.sock"
pid=""
watchdog=""
serverPID=""
cleanup() {
  if [ -n "$watchdog" ]; then
    kill "$watchdog" >/dev/null 2>&1 || true
    wait "$watchdog" >/dev/null 2>&1 || true
  fi
  if [ -n "$pid" ]; then
    kill -TERM "$pid" >/dev/null 2>&1 || true
    kill -KILL "$pid" >/dev/null 2>&1 || true
    wait "$pid" >/dev/null 2>&1 || true
  fi
  herdr --session "$SESSION" server stop >/dev/null 2>&1 || true # by name: never the session you work in
  if [ -n "$serverPID" ]; then
    kill "$serverPID" >/dev/null 2>&1 || true # also covers a startup that never opened its socket
    wait "$serverPID" >/dev/null 2>&1 || true
  fi
  rm -rf "$WORK"
}
trap cleanup EXIT

if ! mkdir -p "$HOME" "$WORK/work" "$PERSONA_HOME"; then
  echo "RESULT fail: could not create the isolated directories"
  exit 1
fi

( exec herdr --session "$SESSION" server ) >"$WORK/server.log" 2>&1 </dev/null &
serverPID=$!
for _ in $(seq 1 40); do [ -S "$SOCK" ] && break; sleep 0.25; done
if [ ! -S "$SOCK" ]; then
  echo "RESULT fail: the herdr test session did not start"
  sed -n '1,20p' "$WORK/server.log"
  exit 1
fi
export HERDR_SOCKET_PATH="$SOCK"

PANE=$(herdr workspace create --cwd "$WORK/work" --label persona-omp-test --no-focus 2>/dev/null |
  python3 -c 'import json,sys; print(json.load(sys.stdin)["result"]["root_pane"]["pane_id"])' 2>/dev/null)
if [ -z "${PANE:-}" ]; then
  echo "RESULT fail: no pane in the herdr test session"
  exit 1
fi

# omp with only Persona's extension; nothing is typed into it, so it stays idle and makes no model request.
if ! herdr pane run "$PANE" "omp --no-extensions -e '$PWD/integrations/omp/index.js'" >/dev/null 2>&1; then
  echo "RESULT fail: could not start omp in the isolated herdr pane"
  exit 1
fi

if ! (cd server && go test -c -tags omplive -o "$WORK/node.test" ./internal/node/) >"$WORK/build.log" 2>&1; then
  echo "RESULT fail: the node's test binary did not build"
  tail -20 "$WORK/build.log"
  exit 1
fi

export PERSONA_OMP_LIVE="$WORK"
WATCHDOG_TIMEOUT="${PERSONA_OMP_WATCHDOG_TIMEOUT:-70}"
WATCHDOG_GRACE="${PERSONA_OMP_WATCHDOG_GRACE:-5}"
"$WORK/node.test" -test.run '^TestOmpLiveAttachesToHerdrAndRuntime$' -test.v -test.timeout 65s >"$WORK/live.log" 2>&1 &
pid=$!
(
  sleep "$WATCHDOG_TIMEOUT"
  if kill -TERM "$pid" 2>/dev/null; then
    sleep "$WATCHDOG_GRACE"
    kill -KILL "$pid" 2>/dev/null || true
  fi
) & watchdog=$!
wait "$pid"
rc=$?
pid=""
kill "$watchdog" 2>/dev/null || true
wait "$watchdog" 2>/dev/null || true # reap it: no "Terminated" line in the output
watchdog=""

grep -E '^(=== RUN|--- )| LIVE ' "$WORK/live.log" # t.Logf lines are indented
if [ "$rc" -eq 0 ]; then
  echo "RESULT ok: the node listed the omp pane"
else
  echo "RESULT fail: the node did not list the omp pane (exit $rc)"
  tail -30 "$WORK/live.log"
fi
exit "$rc"
