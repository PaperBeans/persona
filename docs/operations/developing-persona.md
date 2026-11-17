# Developing Persona: recording and exporting a device's diagnostics

**Who it serves**: people developing or investigating Persona (the owner, testers), not everyday Assistant users. So it
lives only in Settings › About, is off by default and shows nothing anywhere else (#9460).

A switch, **Developing Persona**, in each client's About. While it is on, the device records its own logs, errors and
energy use; **Export Diagnostics…** packs them into one zip the owner can share or send to an Agent. Nothing goes to the
hub or anyone else unless the owner sends the file, and then only as an ordinary file in one of their own chats.

| Client | State |
| --- | --- |
| iPhone / iPad | #9460: switch, recording, MetricKit, export with Share File; Send to Agent follows |
| Web, desktop app | planned: same switch and package (the desktop app adds its main process) |
| Android | planned, separate issue |
| Watch | not in the first version (MetricKit is not available on watchOS) |

## The switch

- Settings › About, under Version. Stored on the device (iOS `UserDefaults` `devMode.on`, `devMode.since`), never
  synced. Footer: what it records, that the data stays on the device, that turning it off deletes it.
- While on, the group adds **Recorded** (since when, how much) and **Export Diagnostics…**.
- Turning it off deletes everything recorded. When something is recorded it asks first: **Export First…** opens the
  export page, **Turn Off and Delete** goes ahead.

## What is recorded (iOS)

Only numbers and words the app's code chose. Kept at most 24 hours of events, 14 days of MetricKit payloads, 7 days of
errors and 50 MB in all, the oldest dropped first (`DevStore`, `Application Support/dev-mode`, out of backups).

- **Events** (`events.jsonl`, one JSON object a line, `t` in UTC and `kind`):
  - `minute`: one a minute while the app is in front, one for each stretch in the background. The app process's CPU
    time, interrupt and idle wakeups and energy for that span (`task_info(TASK_POWER_INFO_V2)`, the numbers the
    simulator's `proc_pid_rusage` gives), hub requests by route pattern (count, bytes in and out, median and longest
    time, status codes), hub WebSocket frames by message type, bytes, connects and drops, seconds per screen, network
    path (`NWPathMonitor`: Wi-Fi, cellular, other; expensive, constrained), thermal state, battery, Low Power Mode, build.
  - `foreground`: one per period in front: length, CPU, wakeups, energy, requests, bytes, frames, screens and their
    seconds, thermal state and battery at start and end.
  - `app`, `page`, `thermal`, `power`, `battery`, `path`, `socket`, `request_error` (the first of each failing route and
    status in a minute).
- **Errors** (`errors.jsonl`): the app's error reports (`ErrorReports`, uncaught exceptions with call stacks), kept here
  too while the switch is on; whether they also go to the hub's `error_reports` is unchanged.
- **MetricKit** (`metrickit/`): daily `MXMetricPayload`s and `MXDiagnosticPayload`s (crashes, hangs, CPU and disk-write
  exceptions) as delivered, plus `pastPayloads` / `pastDiagnosticPayloads` when the switch is turned on.
- **Log** (`log.txt`): this launch's own `Logger` lines (subsystem `persona`), read from `OSLogStore` at export; iOS
  keeps no earlier launch's log for the app.

Screens are named by the code: the tab (`assistant`, `agents`, `inbox`, `settings`, `feishu`) or a screen opened over it
with `.devScreen(…)` (`chat`, `assistant-chat`, `screen`, `knowledge`).

**Cost while on**: no timer in the background; in front one wakeup a minute rolls the summary. Counters live in memory
behind a lock and lines are appended in batches on a utility queue (crash reports are written at once). While off, the
hooks (`DevProbe`, `HubRedirects.finished`) are nil checks.

## The package

`persona-diagnostics-<platform>-<yyyyMMdd-HHmm>.zip` (local time), a folder of the same name inside:

```
README.md          for the Agent reading it: what each file is, how to read it, what was scrubbed
manifest.json      format "persona-diagnostics", version 1, platform, os, os_version, device_model, app_version, build,
                   commit, utc_offset_minutes, recording_since, exported_at, window {from, to}, categories, counts
events.jsonl       the timeline above
errors.jsonl       error reports, in the hub's error report format
metrickit/*.json   MetricKit payloads as delivered (strings scrubbed, identifiers kept)
log.txt            this launch's log lines
```

The export page lists four categories; the first three can be left out: **Logs** (`log.txt` and the event kinds
other than the energy ones), **Errors and Crashes** (`errors.jsonl`, `metrickit/diagnostics-*`), **Energy and
Performance** (`minute`, `foreground`, `thermal`, `power`, `battery`, `path`, `metrickit/metrics-*`), and **Device and App
Version** (`manifest.json`, `README.md`, always).

## What is never included

Message text, knowledge base content, keys, tokens, passwords, cookies, pairing links, the device's name, the hub's
address, machine names, accounts. Recorded lines carry no free text; free text that does go in (error messages, log
lines, MetricKit strings) passes `DevScrub.text`:

- a URL keeps its path only (no scheme, host, query or fragment, so no pairing code or token);
- `Bearer` / `Basic` values, `token=`, `key=`, `secret=`, `password=`, `cookie=`, `authorization:` values, JWTs and runs of 32
  or more letters and digits become `<redacted>`; e-mail addresses `<email>`; IPv4 and IPv6 addresses `<ip>`; host names
  `<host>`; `/Users/<name>` and `/home/<name>` `~`;
- each text is cut to 1 KB.

Route patterns are the web error reports' (`pathPattern` in `web/src/lib/error-reports.ts`): every path segment that is
not one of the API's words is `:id`. `DevModeTests` keeps the Swift list equal to the web's.

## Code (iOS)

`apple/Persona/DevMode/`: `DevMode` (the switch, lifecycle, device observers, the minute roll), `DevRecorder` (counting
and lines), `DevStore` (files, limits), `DevMetrics` (MetricKit), `DevPackage` (the zip), `DevScrub`, `DevModeViews`
(About's group, Export Diagnostics). Shared code reports through `apple/Shared/DevProbe.swift`; requests through
`HubRedirects.finished` in PersonaWire. DEBUG: `-openAbout YES` opens About, `-openAbout export` its export page.
Tests: `apple/PersonaTests/DevModeTests.swift`.
