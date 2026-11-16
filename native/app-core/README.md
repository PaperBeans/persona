# Persona's shared client core

One Rust library under the iOS, Android and HarmonyOS clients' native UIs. Design, decisions and phases:
[the plan](../../docs/plans/plan-rust-core.md) (#6793). Phase 1 is #6871.

## Layout

| Crate | What it is |
| --- | --- |
| `persona-core` | Pure logic: no IO, no clock, no FFI, no `unsafe`. Today it holds `continuity` (a chat's items, how far they are complete, and what to fetch), ported from the rules every client implements (`spec/fixtures/continuity`). `transport` decodes the hub's continuity payloads from raw bytes into `continuity`'s inputs (see "Transport decoding" below). |
| `persona-core-runtime` | Runs `persona-core` on one worker thread. `dispatch` only queues an action's bytes. The worker applies actions, sends requests to the client and builds each chat's patch. `changed` wakes the client once per batch, and the client pulls the patch with `drain`. `wire` holds every type that crosses the boundary. |
| `persona-core-ffi` | The entry points, built as `libpersona_core`: C functions for Swift (`include/persona_core.h`) and JNI functions for Android's `persona.core.rustcore.NativeBridge`. Each catches panics and returns its failure value. |
| `persona-core-ohos` | HarmonyOS's single NAPI library. Its default host build tests the close-safe runtime registry and all shared fixtures; the `ohos` feature adds ohos-rs's NAPI entry points and thread-safe callbacks, supplied by the device runtime. |
| `persona-core-typegen` | Generates the clients' bincode codecs for the `wire` types: Kotlin to android/core/rustcore, Swift to apple/Packages/PersonaCore and ArkTS to harmony/entry/src/main/ets/core/rustcore. All are committed and checked for drift. |

The hub's item fields are preserved in `ListOp::Insert` / `ListOp::Update`, including supervision promotion retained by
iOS. An updated item keeps its stable list identity instead of becoming a remove/insert pair.

## Build and test

Needs rustup; `rust-toolchain.toml` pins the compiler.

```sh
cargo test --locked                                # unit tests and the shared fixtures, directly and through the runtime
cargo run --locked -p persona-core-typegen          # regenerate the clients' codecs after changing a wire type
cargo run --locked -p persona-core-typegen -- --check
cargo build --locked --release -p persona-core-ffi # libpersona_core.{a,so,dylib}
bash scripts/test-c-abi.sh                        # independent C11 client + dynamic C exports (Linux)
python3 -I scripts/ohos-napi-defs.py --check        # napi-derive definitions and index.d.ts
python3 -I scripts/test_ohos_napi_defs.py           # declaration parser and drift-check regressions
```

`scripts/ohos-napi-defs.py` runs a host `cargo check` with the `ohos` feature and `NAPI_TYPE_DEF_TMP_FOLDER` set.
`napi-derive-ohos` 1.2.0 writes one JSON record per exported function there; this generator maps those records to
`harmony/entry/src/main/cpp/types/libpersona_core/index.d.ts` in its stable function order. `Buffer` is exposed as
`Uint8Array`, and every wake/effect callback must have no leading error argument and return `void`; `changedChat`
keeps separate chat and version arguments. The declaration's `CoreHost` facade and method references stay unchanged.
`--check` compares the complete generated file; without it, the same deterministic output is written for inspection.
No NAPI library is linked or loaded. For testing the converter alone, `--records <JSON-lines file>` replaces Cargo,
and `--print-records` prints the validated raw records. Keep the complete generic `ThreadsafeFunction` types on the
`#[napi]` functions: napi-derive does not expand Rust type aliases into their TypeScript callback signatures.

`scripts/ci/core.sh` runs all of these, plus formatting and clippy. On Linux, `scripts/test-c-abi.sh` compiles
`tests/c_abi_smoke.c` as a separate C11 client against the checked-in header, dynamically links it to the exact release
`libpersona_core.so`, and checks that the header's declared functions have dynamic exports. The client replays the
unchanged fixtures through every pure C projection: sort's `empty_list`, `persona_core_project_answer` and
`persona_core_display_answer` for all 15 question + 8 recommendation cases (complete conditional expected objects,
invalid page/option and the U+200B boundaries), `persona_core_project_stream_chunks` for all 21 complete
`{done, tail}` objects, `persona_core_project_turns` for all 57 static cases with the checked-in current-iOS overlay
applied by the client (62 complete turn objects across the 54 non-empty cases), and
`persona_core_project_task_index` for all five complete expected objects plus the background receipt, and
`persona_core_project_viewed_images` (#9017) for the complete post-overlay ordinary viewed line and a source-derived
folded line. Expected objects
are compared completely after a canonical re-serialization (sorted object keys, exact array order and scalars);
exact-name manifests, malformed-input empty buffers and a deliberately wrong expected object are checked per domain.
A missing fixture, client source or release library fails rather than skips. This proves a real C compiler/linker can
consume the C ABI; it is not a Swift importer, Apple ABI, NAPI/HAP, physical-device or performance test. Generated
inputs and the executable stay under the ignored `native/app-core/target/` directory.

Android's `:core:rustcore` tests build a host copy of
the library and replay the same continuity, transport, sort and static-turn fixtures through the real JNI binding. The
turn replay compares complete expected `turns` arrays using the checked-in `spec/fixtures/lines/ios/differences.json`.
Its 57 cases include three that compare empty arrays only; they do not verify layout. It does not compare rendered
lines, execute Swift or connect a production Android adapter. The operation replay (#8815) additionally drives
`RustCore` dispatch/drain through JNI, preserves each raw JSON item and calls the turn projection at all 10 checkpoints
in the four existing operation cases. It compares 13 complete turn objects and exact item IDs against the same checked-in
corpus plus overlay, and separately replays the existing replacement through the standalone `replace` branch. These
runtime operations are not incremental ChatStore rebuild, rendered-line/layout, task-index or device-performance evidence.
The stream replay (#8827) calls `projectStreamChunks` through the real Kotlin/JNI boundary for all 21 existing
`spec/fixtures/stream-chunks/split.json` cases, comparing complete `{done, tail}` expected objects from the unchanged
fixture. It checks malformed input and a wrong expected object. It does not execute Swift or cover StreamRepair, visual
gaps, rendering, timing/coalescing, production adapters, C/NAPI/Swift bindings or physical-device performance.
The answer replay (#8838) calls `projectAnswer` and `displayAnswer` through real Kotlin/JNI for all 15 question and
8 recommendation cases. It compares complete existing expected objects with their conditional fields and checks invalid
input, a wrong expectation and the U+200B boundary. This does not execute Swift, send a resolution to the hub, measure
UI timing or connect a production Android adapter. The TaskIndex replay (#8842) covers the fixture-facing fields from
`TaskIndex.build`: five existing complete expected objects and a background receipt with empty notices/statuses. It
checks real JNI output, malformed input and a wrong expectation. It does not project rendered ChatLine, full task
consumer state, UI badges or actual background execution. The viewed-image leaf (#8860, #8943) uses the existing turn
projection to keep the image entry's owner, compares complete post-overlay and source-derived viewed lines through
real Kotlin/JNI, and checks duplicate/running/error/Read-extension and wrong-expectation cases. Folded images follow
path/size/`i:` identity and join ordinary tool images in work-group order; no-image folds produce no line. Missing
optional fold fields do not reject queued text+image messages (#9024); actual fold grouping is unchanged. This is not
full ChatLine, multiple viewed lines, FoldImage.ref synthesis, UI image loading or actual tool execution.

```sh
cd android && ./gradlew :core:rustcore:test
```

Android's production packaging lives in `:core:rustcore-android`, leaving the JVM/JNI replay above intact. Its pinned
NDK 29.0.14206865 / direct LLVM build, ABI sizes, uncompressed 16 KB packaging and real device fixture replay are
described in `android/core/rustcore-android/README.md`. `scripts/build-android.sh` produces both ABIs without
`cargo-ndk`; the Gradle task declares the core inputs so unchanged sources do not recompile Rust.

## Android continuity adapter (#7448)

`:core:data` routes native chat transport through `ContinuityAdapter`. Kotlin wraps the existing reducer and sync
and remains the production default; `-PpersonaContinuity=rust` selects Rust for repository unit tests. Rust owns one
lazy process-wide core, starts it on a worker after a frame, and applies patches and chat-owned fetch effects on
coalesced frame drains. Only the JVM test scheduler waits with `flush`; the app returns after dispatch, not after a
patch acknowledgement. State wakes use their chat identity.

The adapter consumes #7554 without changing Rust, JNI or codecs: `ChatPatch.verified` owns verification,
`Action::HubChanged` owns changes heard during a fill, and adjacent input/`Ack` submissions distinguish applied no-ops
from queued work. Asynchronous fold responses carry `fold_id`, so independent folds open concurrently. Asynchronous
older/fold calls install a single-use input slot before dispatch and wait with `withTimeoutOrNull`; effects from expired inputs cannot satisfy
later calls. The driver tags effects at the worker's ordered Ack wake and delivers completed slots from `patch.acked`.

Asynchronous fill completion belongs to the applied callback chain. Merely queuing `After` or `Reloaded` neither
finishes nor cancels it; until a response is successfully queued, its request owner reports failures or cancellation
with `FillFailed`. Only content inputs newer than an error's input boundary can clear that error; arbitrary or older
Acks cannot. Each reconciliation waits for its own successful fill-response receipt. Synchronous repository paths
retain the original state-lock sections, serialized HTTP operations, downloaded fold results and error-clearing rules.
They do not use receipt timeouts, operation slots or submitted-history generations. Only asynchronous adapters submit
outside that lock and open concurrent folds. Ack-only frames reuse the item list, skip coverage scans and do not
schedule cache writes.
`Rejected` has no chat
or input identity, so the driver records diagnostics and broadcasts a notification without failing another chat or
releasing its gates. Bounded native waits and the core's duplicate-fill gate keep later retries possible.

Enable the debug-only shadow with `-PpersonaContinuity=shadow` in JVM tests, or launch `DebugEntryActivity` with
`--es debug.continuity shadow`, then force-stop and relaunch the app (use `kotlin` to turn it off). Kotlin remains
authoritative: the shadow serially replays every input on its own background queue, waiting only for its own core,
created after the first frame. It compares item ids/order, items, covered, history, hasMore, loadedOnce and requests.
Kotlin's per-step gap/verified and Rust's persistent verification authority are not equivalent fields: cache restores
and live pushes intentionally differ, covered by a separate real-JNI contract test rather than a second state machine.
Rust-only acknowledgements and concurrent-fold capability do not change the Kotlin result returned to the repository.
`PersonaCoreShadow` reports contain only domain, rule and input kind with counts/flags; never items, identifiers, text,
history or sequence values. Driver-level rejection has its own `rust-rejected` rule, including rejection followed by
an exception, and never logs the rejection reason through the shadow. Native failure disables only that chat's shadow.
Neither accept nor close waits for Rust; close preserves queued inputs. After every 10 completed input comparisons,
and after draining on close, each shadow logs only `compared=<count> mismatches=<count>` to `PersonaCoreShadow`.
`compared` excludes queued inputs, failed native work and rejections; `mismatches` counts individual rule reports.
Release source sets contain neither the shadow nor its preferences.

## HarmonyOS Agents-list adapter (#8957)

`AgentWorkspace` delegates its filtering, ordering and grouping to `NativeAgentListAdapter`, retaining the existing
ArkTS projection and `ResourceRow` identity as the production authority. Only `BuildProfile.DEBUG` dynamically loads
`DebugAgentListShadow`; release never imports that module or calls `coreProjectSort`. The worker receives a plain
core-input/projection snapshot, not `ResourceRow`, owner callbacks or preferences closures. Its serial taskqueue cannot
replace the native rows, and snapshot/native failures affect only diagnostics.

Reports contain the domain, a fixed rule and counts/enum flags (`sessions`, `machines`, pin/mute counts and
query/grouping/quick shape), never IDs, paths, titles, query text, payload content or error messages.
`harmony/tests/agent-list-adapter.test.mjs` covers this contract and the dynamic-import gate; no release HAP scan was
available in this environment.

The core lane also runs `harmony/tests/core-host/agent-sort-adapter.test.mjs`, driving the real core and the production
native adapter with all 30 `agents_list` fixtures. It compares complete groups/rows/visible rows rather than reducing
both sides to unordered IDs. Current host evidence: 1/30 equal; 29 differ in `counts`, `group-order` or
`unread-shown`, tracked by #8966. The unchanged core/fixture comparison passes all 30. Native grouping,
membership/filter and rank parity remain open, so the adapter's existence is not migration or device acceptance.
Physical-device and HAP/NAPI execution remain **blocked: no physical device**; this host environment has no
`devecocli`, so a release HAP was not built.

## Reproducing phase-1 evidence

These are experiment entry points, not claims that every cell has run. A host or simulator result never substitutes
for a physical-device result. Raw library bytes, stripped bytes, an app's packaged increment and store-download size
are separate measurements; gzip is not a store-download report. `docs/plans/plan-rust-core.md` keeps the measured
values and the open cells for #6871.

| Evidence | Command / entry point | What still has to be supplied or measured |
| --- | --- | --- |
| Core tests, codec drift, HarmonyOS binding lints and notices, release FFI and independent C ABI client | `scripts/ci/core.sh` | Rustup, a C compiler and binutils on Linux; no phone or platform SDK. The lane adds the `aarch64-unknown-linux-ohos` Rust target and lints `persona-core-ohos` with its `ohos` feature, which the host build never compiles. The separate C11 client links to the real release library, but this is not evidence of Swift/Apple ABI or a device library. |
| Real host-JNI boundary | `cd android && ./gradlew :core:rustcore:test` | JDK 21; host JVM tests do not need the Android NDK or a device. |
| Android ABIs and packaged bytes | Build the APK, then run `native/app-core/scripts/android-package-report.py` with the exact arguments in `android/core/rustcore-android/README.md`. | x86_64 Linux, JDK 21, SDK 37 and NDK 29.0.14206865. Reports raw/stripped/gzip/APK bytes and ELF/APK alignment, not store downloads. |
| Android real packaged-JNI replay | `native/app-core/scripts/android-device-test.py --serial <owned serial> --suite replay --apk </absolute/path/to/debug androidTest.apk> --output <report directory>` | An explicitly owned emulator or device; no hub is needed. The existing host and software-emulator passes do not establish physical arm64 or a 16 KiB kernel. |
| Android optimized benchmark | The same runner with `--suite benchmark` and the release androidTest APK | Physical arm64 / 16 KiB-kernel evidence is **blocked: no physical device** (maintainer decision, 2026-11-14); do not wait for it. If an arm64 Android 15+ device with a 16 KiB kernel later becomes available, rerun this existing command and retain its named device/build and raw samples. Keep the original 100 warm-ups, 1,000 samples and 50 µs / 1 ms budgets; an emulator failure stays a failure. |
| Apple XCFramework and real-C XCTest | `native/app-core/scripts/build-apple.sh`, then `xcrun swift test --package-path apple/Packages/PersonaCore -Xswiftc -warnings-as-errors` | macOS and Xcode; no physical phone is needed. `DEVELOPER_DIR=<Xcode 26.0.1>/Contents/Developer scripts/ci/apple.sh packages` checks the minimum compiler when that Xcode is installed. Other Xcode versions must be labelled "26.0 not verified". |
| Apple launch, stream, scroll and ten-open/close memory | `scripts/app/perf-test.sh` covers only its listed frame/hitch suites. A launch and ten-open/close memory entry is not yet implemented; do not infer those results from it. | Physical-iPhone evidence is **blocked: no physical device** (maintainer decision, 2026-11-14); do not wait for it. An owned simulator and named optimized build may still supply reference data. App Launch / Allocations / Animation Hitches are the procedures for a later device; an XCFramework or archive is not a download-size measurement. |
| HarmonyOS API 26 library | `OHOS_SDK_NATIVE=<API 26 native SDK> native/app-core/scripts/build-ohos.sh` | An installed API 26 native SDK, the Rust OHOS target and populated offline Cargo cache are still required. The script derives `OHOS_NDK_HOME` (which `napi-build-ohos` reads) from the directory that holds the SDK's `native` directory. With a stub SDK layout the build compiles the release closure and stops at the link step; an actual link has not been run. Host registry tests do not link the device NAPI. |
| ArkTS codec and wire corpus against the real core, on the host | `scripts/ci/core.sh` builds the helper and runs `harmony/tests/core-host/replay.test.mjs` and `transport.test.mjs`. Locally: `cd native/app-core && cargo build --locked -p persona-core-ohos --example host_replay`, then `PERSONA_CORE_HOST_REPLAY=$PWD/target/debug/examples/host_replay node --test '../harmony/tests/core-host/*.test.mjs'` (Node.js 22.15+). | ArkTS-encoded actions drive the real runtime through `persona-core-ohos`'s registry, and ArkTS decodes the effects and patches: 38 cases / 94 steps, plus 42 values built independently in Rust whose bytes must match in both directions. It caught `u32` fields being written and read as 8 bytes (#7485). The wire corpus is also replayed through the transport decoder with byte-identical items, and representative `WirePayload` actions are compared with hand-built `WireBatch` actions. Host evidence only: no NAPI library, HAP or device; callbacks arrive as records, not thread-safe functions. |
| HarmonyOS real-NAPI replay | `devecocli build --modules entry@ohosTest`, then install and run `aa test` with `coreReplay 1`; exact arguments are in `harmony/README.md`. | **Blocked: no physical device** (maintainer decision, 2026-11-14); do not wait for it. A future API 26/HDC environment must also have DevEco CLI and the native SDK. HAP linking, real NAPI callbacks, device timing and packaged increment remain unmeasured. |

Physical-device cells are no longer prerequisites for the next milestones. They stay explicitly blocked above rather than
being marked passed or supplied by a simulator. Xcode 26.0.1 minimum-compiler verification remains a separate Mac
toolchain task, not a physical-device requirement. Device selection, signing, SDK installation and license acceptance
belong to the maintainer's environment if it later becomes available; none is silently substituted by an emulator or
a release upload.

The ArkTS host tests live outside the portable `harmony/tests/*.test.mjs` glob, because the replay needs the helper
binary; a missing helper or too-old Node fails the test rather than skipping it. `harmony/tests/core-host/sort.test.mjs`
replays every Agents-list sort projection and all 13 existing `session_state` / `hub_state` cases against the real core.
The state command calls `persona_core::state::session_state` and `AgentState::from_hub` directly; expectations come only
from the unchanged checked-in fixtures, with exact-name, invalid-input and wrong-expectation checks.
`harmony/tests/core-host/projections.test.mjs` (#8937) replays the unchanged 15 answer + 8 display_answer,
21 stream_chunks and 5 task_index cases plus the background receipt through a host JSON projection command. The answer comparison preserves the fixture's
conditional next-page, picks and display fields. The receipt retains its description and started call with empty
notices/statuses. Exact manifests, malformed inputs and deliberately wrong expected objects are checked per domain.
The `ohos`-feature NAPI exports are `coreProjectAnswer`, `coreDisplayAnswer`, `coreProjectStreamChunks` and
`coreProjectTaskIndex`; their generated declarations are checked for drift. `coreDisplayAnswer` takes a bare option
label, while the other three take JSON documents.
`harmony/tests/rustcore-wire.test.mjs` round-trips the generated codec against itself, which cannot catch a symmetric
mistake, so it is not a substitute. These host projections do not execute the NAPI functions or exercise a state codec,
a loaded NAPI module, NAPI argument conversion or callbacks, a production ArkTS adapter, a HAP/HDC run or a physical
device. No production client uses the new exports yet.

## Host-only boundary diagnostics

On an x86_64 Linux host, `cd android && ./gradlew :core:rustcore:boundaryCostTest` builds a separate optimized
`test-support` FFI library and runs the segmented Runtime/JNI/codec measurements. Ordinary `:core:rustcore:test`
excludes these measurement classes and keeps its debug host-JNI replay. The dedicated task writes raw nanoseconds and
metadata to `core/rustcore/build/reports/boundary-cost/boundary-cost.json` and
`core/rustcore/build/reports/codec-comparison/codec-comparison.json` (paths relative to `android/`). A missing native
library or measurement nonce fails the task rather than silently skipping evidence.

Each run uses 100 warm-ups and 1,000 retained samples per segment, with the same bounded item/action and a real
nonempty patch per frame. The frame is exactly JNI drain + Kotlin decode + list apply on the same patch bytes;
OpenChat and flush are excluded. `PERSONA_MEASUREMENT_DIAGNOSTICS=0` disables runtime diagnostics entirely; do not
interpret the absent native counters in that mode as zero cost. With diagnostics enabled, the `/proc` reads and lock
observation can dominate the measured spans. The registry lease, queue send, patch clone/encode and Kotlin decode
are reported separately and overlap rather than forming an additive wall-clock decomposition.

The codec comparison is an allocation microbenchmark with strict UTF-8 and matching validation on its fixed input,
not a general codec-equivalence or frame-performance proof. Host and emulator values are diagnostic references only:
they do not replace the original Android benchmark, the 50 µs / 1 ms budgets, or physical-device evidence.

## Rules

- **A wire type change ships with its regenerated codecs.** Never edit generated files by hand.
- **Fixtures come first.** A rule changes in `spec/fixtures/<domain>`, then here; expectations are never generated from
  this code.
- **Text is out of scope.** No user-facing text: the core returns codes and enums, and each client words them from its
  own localization resources.
- **The main thread stays cheap.** Calls a client makes on its main thread (`dispatch`, `drain`) never do work that grows
  with a chat; that work belongs to the worker.

## Transport decoding

`persona_core::transport` turns the bytes the host received into the values `continuity` takes (the plan's decision 2:
JSON is decoded once, in Rust). `Action::WirePayload` carries the bytes (below); no production client sends them yet.

| Function | Payload | iOS type it follows |
| --- | --- | --- |
| `decode_latest` | the latest page or an older page of `GET …/items` | `APIClient.ItemsResponse` |
| `decode_newer` | a catch-up page (`after_seq`) | `APIClient.NewerResponse` |
| `decode_range` | a page of an opened fold | `Range` in `APIClient.foldedWork` |
| `decode_frame` | a WebSocket frame: `Frame::Items`, or `Frame::Other` for any other type | `ServerEvent.decode` and its `Envelope` |

- Each item's JSON is kept byte for byte; only the keys the core's rules use are read.
- A refusal is a `DecodeError`: a reason code (`invalid_utf8`, `truncated_json`, `malformed_json`, `not_object`,
  `missing_key`, `wrong_type`, `bad_item`) and a detail made of positions and protocol key names. It never repeats
  payload content. One refused item refuses its page, named by the index of the first one.
- A key that appears more than once keeps its first value, as Foundation's keyed container does.
- `cargo test` replays the 17 wire cases these decoders own (`spec/fixtures/wire`: `items.json`, `item_events.json`
  and the `items` events of `compatibility.json`), all of them accepted payloads, and rebuilds every payload step of
  the continuity corpus from bytes, comparing the step and the state with the hand-built batch. Of the 79 expected
  fields of those cases, 52 are values the decoder produced; 25 are item fields the core does not read, compared with
  the item's kept JSON, and 2 are the frame's type. The same 17 cases are replayed through the real JNI binding as
  `Action.WirePayload` by Android's `TransportBindingFixtureTest` (`./gradlew :core:rustcore:test`, a host library),
  which feeds each case's `input` exactly as the file holds it and compares every published item with its raw source
  text; that is host evidence only, not a device or iOS run.
- The refusal rules are tested by the crate's unit tests: every key a decoder reads, with every kind of wrong value.

### Source-derived WebSocket Envelope projection (#8955)

`persona_core::wire_envelope` is a pure diagnostic projection of the narrow current-iOS slice in
`spec/fixtures/wire-envelope`: the shared scalar and raw-JSON envelope keys, `mesh` / `fleet` tolerance, unknown
types, and routing requirements for `hello`, `inbox`, `items`, `fleet`, `screen` and `sessions_list`. It returns
accepted, ignored or dropped with the documented projected fields; known event types outside that set return
`out_of_scope`, never an assumed acceptance. It does not route an event, publish a patch or replace `decode_frame`.
The existing continuity transport behavior and its known differences are unchanged.

All 64 complete expected objects were hand-derived from the Swift sources cited per case, including duplicate keys
whose input reaches the decoder as raw source text. `cargo test` and `WireEnvelopeBindingFixtureTest` replay exactly
the same file and expectations; the latter calls the real host Kotlin/JNI `projectWireEnvelope` function, and checks
malformed input and a wrong expectation. Integer spellings, encodings, unprojected nested models, raw `JSONValue`
extreme floating-point behavior and full Item / Fleet models remain explicitly limited in the domain README.
Neither replay executed Swift, iOS, a simulator, a production adapter or a physical device.

It does not match iOS everywhere. The module's documentation has the full list; in short:

- **Keys only other events use.** iOS's `Envelope` is one struct for every event type, so iOS also drops an `items`
  frame when such a key (`rows`, `features`, `fleet`, …) has the wrong type. The core checks the keys an `items`
  frame carries and ignores the rest, so it does not follow every unrelated addition to the Swift struct.
- **Nested structs the core does not read.** iOS refuses a payload when an item's `image`, `task`, `compact`,
  `injected` or `model_refusal`, or a fold's `images` or `tasks`, fails to decode. The core accepts it.
- **Number spellings the hub never writes.** The core takes integer literals only and refuses `8.0` or `8e0`, which
  Foundation reads as 8. Reading an integer through a double exactly needs `serde_json`'s `float_roundtrip`
  reader, which added 33,328 B to the stripped x86_64 host library (741,032 B to 774,360 B) when measured.
- **Encoding and kinds.** The core takes only UTF-8 without a byte order mark, and keeps an unknown item `kind` as
  the hub wrote it.

The same decoders are reached by `Action::WirePayload { chat, payload }` (`persona-core-runtime`'s `wire`): a
`Payload` is one response body or WebSocket frame as received, and its variant says which request it answers
(`Latest`, `Newer`, `Reloaded`, `Older`, `Range`, `Frame`). Decoding happens on the worker, never in `dispatch`; a
`Frame` of another type is ignored and one iOS would drop is dropped without an error. A refused page is an
`Effect::Rejected` whose reason is a code and protocol key names, never payload content.
`crates/persona-core-runtime/tests/transport_through_runtime.rs` replays the 17 wire cases through it with the JSON
exactly as the files hold it: 32 items checked byte for byte, every case except the opened-fold range page also
compared with the equivalent hand-built `ChatEvent`, plus separate tests that drive a range page into an opened fold
and an older page into history, and refusal, other-frame and dispatch-cost tests. The same corpus is replayed through the real JNI
binding (`TransportBindingFixtureTest` in `:core:rustcore`) and through ArkTS and the transport decoder on the host
(`harmony/tests/core-host/transport.test.mjs`); both are host evidence, not device runs.

The patch also carries `verified` (the chat's items are known complete, the rule Android used to derive per
client), and `acked` (a host's acknowledgement watermark, so an applied no-op is visible). `Action::HubChanged`
settles as if a gap were found, iOS's `settle(.fill)`. Folds are keyed by id and independent: different folds open
and complete in either order, with `fold_id` on the range request, event and payload; a caller that sends no id keeps
the older single-opening behaviour. `RangeFailed { fold_id: None }` cancels **every** opening, so a transport failure
for one of several keyed folds must report its id or the other folds are cancelled too.

Nothing here was executed on iOS. The rules were read from the Swift sources and from swift-foundation's
`JSONDecoder`; the wire cases' expectations are derived from the iOS sources as well (`derived: true`) and are checked
by the hub's golden test and the Android replay, and no iOS test reads those cases yet.
