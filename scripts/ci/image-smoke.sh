#!/bin/bash
set -euo pipefail
# Most checks are quiet pipes: name the one that failed, or the log ends with only an exit code.
trap 'status=$?; printf "FAIL image smoke line %s (exit %s): %s\n" "$LINENO" "$status" "$BASH_COMMAND" >&2' ERR
# A check's grep reads all of its input: grep -q stops at the first match, and the writer (docker compose exec, curl)
# then fails on the closed pipe, which pipefail reports as the check failing (`hub pair` writes a QR code after its link).
holds() { grep "$@" >/dev/null; }
cd "${CI_IMAGE_FIXTURE:?}"
smoke_url="http://127.0.0.1:${CI_IMAGE_PORT:?}"
docker run --rm persona-hub:ci version | grep -Fx "${CI_IMAGE_VERSION:?}"
healthy() {
  for attempt in $(seq 1 60); do
    if curl --max-time 3 -fsS "$smoke_url/api/v1/health"; then return 0; fi
    sleep 2
  done
  printf 'TIMEOUT hub health endpoint\n' >&2
  return 1
}
# The container's programs (docs/operations/docker.md "Programs"): the hub's, then the built-in manager's, which starts
# once it is bound, a moment after the hub answers.
runs() {
  for attempt in $(seq 1 30); do
    if docker compose exec -T hub ps -o args | holds -Ex "$2"; then
      docker compose exec -T hub ps -o args | holds -Ex "$1"
      return
    fi
    sleep 2
  done
  printf 'TIMEOUT no process %s\n' "$2" >&2
  docker compose exec -T hub ps -o args >&2
  return 1
}
docker compose up -d
healthy
printf 'Checking the hub and the built-in manager run as persona-hub and persona-manager\n'
runs '/opt/persona/nodes/persona-hub run' '/opt/persona/nodes/persona-manager assistant'
# Drain producer output under pipefail: an early grep exit can make Docker exec
# return 255 even when the expected pairing link was present.
printf 'Checking pairing and tailcat key\n'
docker compose exec -T hub personad hub pair | holds 'persona://pair?'
docker compose exec -T hub personad hub tailcat-key | holds '^nodekey:'
curl --max-time 10 -fsS "$smoke_url/" | holds -i '<html'
# Password login must ship WASM bytes and their licenses, not the SPA's HTML fallback.
curl --max-time 10 -fsS "$smoke_url/pake/client.wasm" -o password-client.wasm
python3 -c 'from pathlib import Path; assert Path("password-client.wasm").read_bytes()[:4] == b"\x00asm"'
curl --max-time 10 -fsS "$smoke_url/pake/THIRD_PARTY_NOTICES.txt" | holds 'github.com/bytemare/opaque'
curl --max-time 10 -fsSk "https://127.0.0.1:${CI_IMAGE_TLS_PORT:?}/api/v1/health" >/dev/null
docker compose exec -T --user 0 hub test -s /home/persona/.persona/docker/db-password
docker compose exec -T --user 0 hub grep -q '"first_setup": "code"' /home/persona/.persona/hub.json
# The default LAN scope and first_setup=code are independent choices. This
# fixture explicitly selects loopback to test isolation through the mapped port.
docker compose exec -T hub wget -qO- http://127.0.0.1:18660/admin > admin-local.html
holds 'admin/assets/' < admin-local.html
docker compose exec -T hub wget -qO- http://127.0.0.1:18660/api/v1/admin/state | holds '"password_set":false'
curl --max-time 10 -fsS "$smoke_url/" > client.html
curl --max-time 10 -fsS "$smoke_url/admin" > admin-mapped.html
# Out-of-scope admin pages intentionally return the ordinary web client. Both
# pages contain the product name, so that string cannot identify admin exposure.
if cmp -s admin-local.html client.html; then
  printf 'FAIL local admin page is indistinguishable from the client fixture\n' >&2
  exit 1
fi
if holds 'admin/assets/' < admin-mapped.html; then
  printf 'FAIL admin UI exposed through mapped port\n' >&2
  exit 1
fi
cmp client.html admin-mapped.html
admin_code=$(curl --max-time 10 -sS -o admin-state.json -w '%{http_code}' "$smoke_url/api/v1/admin/state")
app_code=$(curl --max-time 10 -sS -o app-state.json -w '%{http_code}' "$smoke_url/api/v1/machines")
[ "$admin_code" = 401 ] && [ "$app_code" = 401 ]
cmp admin-state.json app-state.json
rm admin-local.html client.html admin-mapped.html admin-state.json app-state.json password-client.wasm
if docker info --format '{{json .SecurityOptions}}' | holds 'name=rootless'; then
  mapped_uid="$(docker run --rm --entrypoint awk persona-hub:ci -v uid="$(id -u)" '$1 <= uid && uid < $1 + $3 { print $2 + uid - $1 }' /proc/self/uid_map)"
  [ -n "$mapped_uid" ] && [ "$(stat -c %u data)" = "$mapped_uid" ]
else
  [ "$(stat -c %u data/hub.json)" = "$(id -u)" ]
fi
[ "$(docker compose exec -T --user 0 hub stat -c %u /home/persona/.persona/hub.json)" = "$(id -u)" ]
[ "$(docker compose exec -T --user 0 hub stat -c %a /home/persona/.persona/hub.json)" = 600 ]
printf 'Checking PERSONA_SPLIT_PROGRAMS=0 runs both on personad\n'
PERSONA_SPLIT_PROGRAMS=0 docker compose up -d hub
healthy
runs '/opt/persona/nodes/personad-linux-[a-z0-9]+ hub run' '/opt/persona/nodes/personad-linux-[a-z0-9]+ assistant'
printf 'PASS hub image: health, TLS, pairing, web, admin isolation, file ownership and its programs\n'
