#!/bin/sh
# A deterministic fake for the larkcli adapter tests. It never reads or writes
# a credential store and emits only synthetic fixture data.
set -eu

base=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
scenario=${LARKCLI_FAKE_SCENARIO:-success}
if [ -n "${LARKCLI_FAKE_LOG:-}" ]; then
  printf '%s\n' "$*" >> "$LARKCLI_FAKE_LOG"
fi

if [ "${1:-}" = "--profile" ]; then
  shift 2
fi

case "${1:-}" in
  --version)
    if [ "$scenario" = "old" ]; then
      printf '%s\n' 'lark-cli version 1.0.71'
    else
      printf '%s\n' 'lark-cli version 1.0.99'
    fi
    exit 0
    ;;
  auth)
    if [ "${2:-}" = "login" ]; then
      if [ -n "${LARKCLI_FAKE_LOGIN_PID:-}" ]; then
        printf '%s\n' "$$" > "$LARKCLI_FAKE_LOGIN_PID"
      fi
      if [ "${LARKCLI_FAKE_LOGIN_DOMAIN:-feishu}" = "lark" ]; then
        printf '%s\n' 'Open https://accounts.larksuite.com/oauth/v1/device/verify?user_code=ABCD-1234&state=example'
      else
        printf '%s\n' 'Open https://accounts.feishu.cn/oauth/v1/device/verify?user_code=ABCD-1234&state=example'
      fi
      case "$scenario" in
        login_failure) printf '%s\n' 'login failed' >&2; exit 17 ;;
        login_wait)
          if [ -n "${LARKCLI_FAKE_LOGIN_RELEASE:-}" ]; then
            while [ ! -e "$LARKCLI_FAKE_LOGIN_RELEASE" ]; do sleep 0.05; done
          else
            sleep 30
          fi
          ;;
      esac
      exit 0
    fi
    if [ "${2:-}" = "logout" ]; then
      exit 0
    fi
    case "$scenario" in
      not_logged_in) cat "$base/auth_not_logged_in.json" ;;
      expired) cat "$base/auth_expired.json" ;;
      auth_missing_scope) cat "$base/auth_missing_scope.json" ;;
      auth_read_scope_missing) cat "$base/auth_read_scope_missing.json" ;;
      needs_refresh) cat "$base/auth_needs_refresh.json" ;;
      ready) cat "$base/auth_ready.json" ;;
      expiring) cat "$base/auth_expiring.json" ;;
      rolling) cat "$base/auth_rolling.json" ;;
      refresh_expiring) cat "$base/auth_refresh_expiring.json" ;;
      *) cat "$base/auth_status.json" ;;
    esac
    exit 0
    ;;
  profile)
    if [ "${2:-}" = "list" ]; then
      cat <<'JSON'
[
  {"name":"example","appId":"cli_example","brand":"feishu","user":"Example User","tokenStatus":"valid"},
  {"name":"other","appId":"cli_other","brand":"lark","user":"","tokenStatus":"signed_out"},
  {"name":"persona","appId":"cli_example","brand":"feishu","user":"Example User","tokenStatus":"valid"}
]
JSON
      exit 0
    fi
    ;;
  config)
    case "${2:-}" in
      show)
        case "$scenario" in
          config_missing) printf '%s\n' '{}';;
          config_different) printf '%s\n' '{"appId":"cli_old","brand":"feishu"}';;
          config_same) printf '%s\n' '{"appId":"cli_bot","brand":"feishu"}';;
          *) printf '%s\n' '{"appId":"cli_example","brand":"feishu"}';;
        esac
        exit 0
        ;;
      init)
        secret=''
        IFS= read -r secret || true
        if [ -n "${LARKCLI_FAKE_INIT_LOG:-}" ]; then
          printf '%s|stdin=%s\n' "$*" "$secret" >> "$LARKCLI_FAKE_INIT_LOG"
        fi
        exit 0
        ;;
    esac
    ;;
  im)
    case "$scenario" in
      timeout) sleep 5; exit 0 ;;
      missing_scope) cat "$base/error_missing_scope.json"; exit 3 ;;
      rate_limited) cat "$base/error_rate_limited.json"; exit 4 ;;
      nonzero) printf '%s\n' 'message-body-example' >&2; exit 17 ;;
    esac
    case "${2:-}" in
      +chat-list) cat "$base/chats.json" ;;
      +chat-messages-list|+threads-messages-list) cat "$base/messages.json" ;;
      +messages-search|+chat-search) cat "$base/search.json" ;;
      *) cat "$base/search.json" ;;
    esac
    exit 0
    ;;
  *)
    if [ "$scenario" = "timeout" ]; then sleep 5; fi
    printf '%s\n' 'message-body-example' >&2
    exit 17
    ;;
esac
