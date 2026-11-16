#!/usr/bin/env bash
# Compile an independent C client against the exact release library and check its dynamic C exports (#8560).
# This is Linux host ABI evidence only, not Swift importer, Apple ABI, NAPI, device or performance evidence.
set -euo pipefail
ROOT=$(cd "$(dirname "$0")/../../.." && pwd)
WORK="$ROOT/native/app-core"
RELEASE="$WORK/target/release/libpersona_core.so"
OUT=${1:-"$WORK/target/c-abi-smoke"}
CC_BIN=${CC:-cc}
NM_BIN=${NM:-nm}
command -v "$CC_BIN" >/dev/null 2>&1 || { printf 'test-c-abi: FAIL C compiler not found: %s\n' "$CC_BIN" >&2; exit 2; }
command -v "$NM_BIN" >/dev/null 2>&1 || { printf 'test-c-abi: FAIL nm not found: %s\n' "$NM_BIN" >&2; exit 2; }
[ -f "$RELEASE" ] || { printf 'test-c-abi: FAIL build the exact release library first: %s\n' "$RELEASE" >&2; exit 2; }
mkdir -p "$OUT"
python3 -I -c 'import json,pathlib,sys
root=pathlib.Path(sys.argv[1]); out=pathlib.Path(sys.argv[2])
doc=json.loads((root/"spec/fixtures/sort/filters.json").read_text())
case=next(c for c in doc["cases"] if c["name"]=="empty_list")
(out/"empty-list.json").write_text(json.dumps(case["input"],ensure_ascii=False,separators=(",",":")))
(out/"expected.json").write_text(json.dumps(case["expected"],ensure_ascii=False,separators=(",",":")))' "$ROOT" "$OUT"
"$CC_BIN" -std=c11 -Wall -Wextra -Werror -pedantic \
  -I"$WORK/crates/persona-core-ffi/include" "$WORK/tests/c_abi_smoke.c" \
  -L"$WORK/target/release" -Wl,-rpath,"$WORK/target/release" -lpersona_core -o "$OUT/c_abi_smoke"
"$OUT/c_abi_smoke" "$OUT/empty-list.json" "$OUT/expected.json"
"$NM_BIN" -D --defined-only "$RELEASE" > "$OUT/exports.txt"
python3 -I -c 'import pathlib,re,sys
header=pathlib.Path(sys.argv[1]).read_text()
exports=pathlib.Path(sys.argv[2]).read_text().splitlines()
required=sorted(set(re.findall(r"\b(persona_core_[A-Za-z0-9_]+)\s*\(",header)))
# A defined data object with the same name is not a callable function export.
actual={fields[2] for fields in (line.split() for line in exports)
        if len(fields) == 3 and fields[1] in {"T", "W"}}
missing=[name for name in required if name not in actual]
if not required or missing:
    raise SystemExit("test-c-abi: FAIL missing or non-function dynamic C exports: "+", ".join(missing))
print("test-c-abi: ok independently compiled C client and",len(required),"required dynamic exports")' \
  "$WORK/crates/persona-core-ffi/include/persona_core.h" "$OUT/exports.txt"
printf 'test-c-abi: ok %s\n' "$OUT"
