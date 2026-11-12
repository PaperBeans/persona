# Recordings in the knowledge base

Recordings feed spoken notes, meetings, and information captured on site to Persona; this is not a second recording product. Both iPhone and Apple Watch record in segments; the watch sends them to the phone via WatchConnectivity, and the phone transcribes locally.
The resulting text (timestamped segments) automatically syncs into `recordings` in the knowledge base, where the assistant and Agents can find it with `kb_search` / `kb_list` / `kb_get`. The knowledge base retains only the most recent N days (14 by default).

Since 2026-11-05 (#3235), there is no longer a "Composer + › Recording context" entry: user feedback was that this entry was wrong. Recordings should be stored in the knowledge base and retrieved by search and queries, not manually inserted into the composer each time.

Rolling recording (the watch keeps recording and only retains the last 6 hours, with health bookmarks) is a mode of this same system — see `docs/apple/recording-rolling.md`. Pulled ranges are transcribed on device and sync into Knowledge like any other recording; there is no composer "as context" path.

All-day recording (the watch records the whole day, the phone transcribes it as it arrives and appends the text to the hub, which writes a daily recap) is the recording kind `day`; its contract, the watch-to-phone transfer included, is `docs/assistant/day-recap.md`. How the watch and the phone carry it out is "All-day recording (Apple)" below.

## Why send only text

- Audio is roughly 14 MB / hour; the knowledge base searches text, so text is enough. The assistant needs "who said what at that day's meeting," not the audio itself.
- Privacy: original audio stays on the phone (watch audio is still cleaned up after transfer to the phone) and never leaves the device; the hub holds only transcripts.
- Recording entries are separate from meetings (`meetings`): a meeting is an external item from the Lark calendar, stored as one JSON document and batch-tagged by a model; a recording has a full transcript, its own retention period, and UUID-based writes and deletes from the phone. Their lifecycles differ, and combining them complicates cleanup and display.

A recording marked as a meeting (`kind: "meeting"`, with the phone calendar's event in `calendar`) also gets structured minutes from the hub: [meeting-minutes.md](../assistant/meeting-minutes.md).

## Synchronization

See [protocol.md](../protocol/protocol.md) ("Recording synchronization") for the API and [knowledge.md](../assistant/knowledge.md) ("Recordings") for storage in the knowledge base.

- The phone uses each recording's UUID as the key for `PUT /api/v1/recordings/{id}`, sending the complete state so far each time (full replacement). `revision` increases monotonically: replayed or out-of-order uploads retain only the newest copy.
- Upload available segments before transcription finishes (`complete: false`), then upload again as transcription progresses, recording ends, or a language change triggers retranscription; once `complete: true`, the hub generates a summary and tags.
- Deleting a recording on the phone → `DELETE /api/v1/recordings/{id}`, deleting it from the knowledge base too; the hub retains a tombstone, returns 410 for late uploads, and the phone discards the pending upload.
- When offline, after hub restarts, or when the manager is disconnected (503), the phone queues retries; `GET /api/v1/recordings` reconciles state (uploading recordings absent from the hub or with an older revision there, and removing those deleted on the hub).
- Retention: the assistant setting `recording_days` (14 days by default, 1–365) can be read or changed through `assistant.recording_days` in `persona_settings`; the hub deletes expired files and index entries together, and recordings older than the retention window are no longer uploaded.
  A meeting with minutes is the one exception, and only on the hub: past `recording_days` it keeps the minutes (not the transcript) according to `minutes_days` (forever by default: unset or `0`, otherwise 1–3650 days, never shorter than `recording_days`; the user’s delete still removes everything; #3780 / #3862, [meeting-minutes.md](../assistant/meeting-minutes.md) "Privacy"). Such a recording stays in reconciliation's `recordings` at its revision, with `trimmed_at`, and an upload of it is `expired`.
  The phone uses `days` from reconciliation to remove expired local recordings as well (audio and text together, leaving active recordings untouched): the hub is authoritative for text, and local retention never exceeds knowledge-base retention. `days` stays `recording_days`, never `minutes_days`: the phone's own copies (audio and transcript) follow the recordings' retention, not the minutes'.
- The iOS implementation is `RecordingSync` (`apple/Persona/Recording/`): every `RecordingLibrary.save` triggers a round (1-second debounce); `revision` is this state's `updatedAt` (milliseconds, guaranteed to increase on save).
  Uploaded revisions, pending deletions, rejections (400), and `days` are stored in `sync.json` in the recording directory; after restart, only changed recordings are uploaded. Transient connection failures retry with exponential backoff starting at 5 seconds, up to 10 minutes; returning to the foreground or a new save triggers an immediate retry.
  `kb_disabled` / `kb_read_only` pause automatic retries without dropping uploads or pending deletions. Fix the manager configuration, then reopen the app to retry; another transcript save cannot enable knowledge. Authentication, permission and unsupported-route failures also pause. Failed reconciliation stops the pass before writes. A DELETE 404 does not confirm deletion: the remote copy stays pending, and the list explains that it has not yet been removed.
  Reconcile on startup and return to the foreground (at most once every 10 minutes). A 410 or an entry in `deleted` during reconciliation means it was deleted elsewhere (such as the web), so delete it locally too. Recordings without any text (no recognized speech) are not uploaded, except a meeting that Persona attends, which is uploaded once when it starts ([meeting-minutes.md](../assistant/meeting-minutes.md) "Attending a meeting").

Phone recordings can use the microphone on glasses or a Bluetooth headset (Bluetooth HFP); see `docs/apple/meta-glasses.md`.

## Migration sources and boundaries

Reuse Hourhand's `Shared/SegmentRecorder.swift` (segmented AAC), `HourhandWatch/TransferQueue.swift` (persistence and transfer backoff), and `Hourhand/Transcription/SpeechEngines.swift` (on-device engines and timestamps). After migration, standardize on the `Recording` prefix and remove product accounts, payments, cloud services, location, and separate recording-library navigation.

- Audio: 16 kHz mono AAC, 32 kbps, roughly 14 MB / hour; segments are 30 seconds in the foreground and 60 seconds in the background. watchOS 11 and later finalize with `AVAudioFile.close()`; watchOS 10 releases the last file reference before the callback and must not retain a local reference inside the write loop.
- Storage: Application Support/Recordings on each device, one UUID directory per recording, with atomic updates to `recording.json`. This is not a cache and must not be deleted by cache cleanup.
- Abnormal exit: retain finalized segments and delete the unfinished tail; do not automatically restart the microphone. At most the current 30–60 seconds are lost.
- Privacy: no location collection and no audio uploads; transcript text syncs into the hub's knowledge base ("Synchronization" above) and is not automatically sent to any chat, except to the session attending a meeting the user chose to attend (#2981). Transcription uses only Apple's on-device models, with no implicit fallback to Apple servers or cloud APIs.
- The initial version has no recording summaries, speaker identification, route maps, payments, or cloud services. The user explicitly selects the recording language.

## Incremental commits

1. Shared recording format, segmentation engine, persistence, and recovery after abnormal exits.
2. On-device transcription, timestamps, resumability, and context generation.
3. Watch recording, persistent transfer, end messages, and cleanup after acknowledgment.
4. Phone recording page, preview / deletion, screenshots, and regression verification.
5. Knowledge-base synchronization (#3235): hub `recordings` and APIs, retention, web / Android knowledge pages; remove the iOS composer entry and switch to automatic sync.

## Meeting recordings

In-person meetings and hallway discussions went unrecorded: the recorder sat four taps deep (Knowledge › Recordings, the last kind chip), and nobody remembered it in the moment. A meeting recording starts in one step from wherever the user is, and every entry point that starts one has the same name, **Record** (zh-Hans 录音, ja 録音), and does the same thing: it starts a meeting recording, or opens the one running (#5243). The drawer's row is the Recordings page itself, a place like the rows under it, so it opens the page and leaves the start to its Record button (#5517: as Record it started at a tap, and in zh-Hans and ja it reads exactly as the page's name). Record always records a meeting (`kind: meeting`, minutes, and Persona can attend with the hub's default role), since impromptu talks are meetings too; the calendar only names it.

| Where | How | Code |
| --- | --- | --- |
| The drawer | **Recordings**, first among the places: opens the Recordings page, whose Record starts a recording (the row starts nothing); while a recording runs the row reads "Recording… Open" and opens it | `AssistantSidebar.meetingRow`, `ShellView.openRecordingsPage` |
| Persona's main conversation | **Record** in the composer's + list (the iPhone's touch and hold on +, the attach panel's More tab, the iPad's + menu; #3723): starts recording and opens Recordings; while a recording runs it reads "Recording… Open" and opens it. Not in other conversations | `AttachMoreSection.common(meeting:)`, `Composer.attachMoreSections` |
| Recordings page | On the iPhone, **Record** (prominent, the page's one start button) sits in a bar at the bottom of the page, under the thumb, with the calendar line under it (#5522); while a recording runs, the bar holds its now-recording card instead. The options stay at the top of the list, one row each, every row with its icon and its words under the title: **Type** (Meeting / Note), then for a meeting Persona, Private Meeting (the system switch) or Who Attends, then language and microphone. At accessibility text sizes the calendar line moves from the bar to the list's first row. The options and Record share one `RecordingStartForm`. Knowledge › Recordings (and visionOS) keep Record at the top of the section. While the start runs (calendar, microphone) the button keeps its label and shows a spinner | `RecordingCaptureControls` (`Part`), `RecordingStartForm`, `RecordingStart`, `RecordingsSection`, `RecordingsPage` |
| Home screen icon's menu | **Record** | `QuickAction.meetingRecording` |
| Control Center, Lock Screen, Action button | the **Record** control, without opening the app | `MeetingRecordingControl` → `StartMeetingRecordingIntent` |
| Siri, Shortcuts, Spotlight | **Record**: "Record with Persona", "Record a meeting with Persona" and their zh-Hans / ja phrases | `PersonaShortcuts`, `StartMeetingRecordingIntent` |

- **Meeting or Note** (`RecordingKindChoice`): a note has no minutes and no Persona (`kind: note`). It is a secondary choice, never a second button. Before the start, the Recordings page's **Type** row is Meeting for every start; Note, picked for that one start, hides the meeting's options (and the calendar line) and the same Record button records a plain note ("No minutes, and Persona stays out."). It is never remembered, and every other entry point starts a meeting. While it records, the live sheet's page menu (`RecordingLiveView`'s, beside Hide, and the meeting page's) has the same choice, Meeting and Note with their marks, under the menu's other items (`RecordingKindPicker` → `MeetingRecorder.setMeeting`). Meeting → Note: `meeting` goes (the upload says `kind: note` with no attendance or `room`, so the hub turns Persona off and writes no minutes), the role goes off from that moment and Persona stops speaking; the attendance stays on the phone, off, so the live log the hub has, Persona's spoken stretches and a room meeting's room are kept. The sheet then shows the live view. Note → Meeting: named after the calendar event on now if it has no name yet (else "Meeting · <date time>"), Persona's part as a fresh start would get it (`MeetingAttendees.startAttendance`, attending from that moment: `MeetingAttendees.resumed`; a meeting that had a room keeps it), and the hub hears of it at once (`RecordingSync.reupload`, so live lines and asks wait for the new upload's `200`). Either way what was said before stays the recording's transcript. The Live Activity follows (Meeting or Recording, and Persona's role only while it attends).
- The identifiers stay what they were, so shortcuts, controls, home screen items and tests made before keep working: `StartMeetingRecordingIntent`, the control's kind `persona.control.meeting-recording`, the item type `persona.meeting-recording`, `sidebar-meeting-recording`, and `recording-meeting-start` for Record (the old plain start, `recording-start`, is gone on the iPhone; visionOS keeps its earlier controls). The watch keeps its two entries for now (`WatchMeetingEntries`).

- `StartMeetingRecordingIntent` is an `AudioRecordingIntent`: the system lets it open the microphone in the background, on condition that a Live Activity shows for as long as the recording runs (`RecordingActivity` starts one before `perform` returns). Before the microphone was ever allowed it continues in the foreground (`continueInForeground`) so the system can ask.
- **The calendar names it** (`MeetingCalendar`): the event on now (all-day, declined and untitled ones aside; one starting within 5 minutes counts; of overlapping ones, the one that started last) gives the title, and a copy of the event goes into `RecordingEntry.meeting` (`RecordingMeeting`: event id, title, start / end, location, attendees by name). It reads only with full access already granted, so a start never waits on a prompt; a meeting recording started in the app asks once, after the recording has begun, and renames it if an event turns up. Without an event the title is "Meeting · <date time>" (`RecordingEntry.displayTitle`).
- A watch recording made during a calendar event is that meeting's too: the phone tags it when its first segment or end arrives (`MeetingRecorder.tagWatchRecording`, by the watch's `createdAt`). The watch has no calendar.
- Language: every start uses the one last picked on the Recordings page (`RecordingDefaults.localeKey`), Chinese until then. The on-device Chinese model writes the English words said among Chinese in Latin letters; there is no mixed-language model on device (the hub's realtime service is #2083).
- **Live Activity** (`docs/apple/live-activity.md`, "Recording"): any recording on the phone, a meeting's or a note, shows its name, what it is, bookmarks and the time on the Lock Screen and in the Dynamic Island, with **Bookmark** and **Stop** (they work locked, as a voice memo's). Tapping it opens Recordings (`persona://record`).
- **A call**: the interruption pauses the recording and closes the open segment at once (`RecordingSegmentWriter.cut`), so an app stopped during a long call keeps everything said before it. When the system ends the interruption the recording resumes by itself (whether or not it says `shouldResume`: the meeting goes on). If the microphone can't be opened again in the background, the phone posts "Recording Paused" (`MeetingRecorder.notifyPaused`), the activity says "Paused by a call · Open Persona to continue", and coming back to the app resumes it (`resumeAfterInterruption`, on `didBecomeActive`). A media services reset while paused stops the recording (its engine is gone) and keeps what was saved.
- **Stopped in the background** (the activity's Stop with the phone locked): the audio session ends, and with it the app's right to run. `MeetingRecorder.finishInBackground` holds a background task for up to 30 s, until the last segment is transcribed and uploaded, so the minutes don't wait for the next launch.

## Apple Watch

The watch records a meeting in one step too (#2720), and shows its minutes once the phone has them.

- **Starting**: **Meeting Recording** on the home list, right above Record (`WatchMeetingEntries`): one tap opens the recording page and starts at once (`WatchRecordingView(startsMeeting:)`); while something records, the card opens it. The **Meeting Recording** complication (watch face corner / circular / rectangular / inline, and the Smart Stack; `MeetingRecordingComplication`) opens the app straight into it (`persona://record?meeting=1`). The recording page also has Meeting Recording above Start Recording, and the unpaired page has it beside Record. The Ultra's Action button isn't wired yet: it needs an App Shortcut in the watch app, with its own phrase catalog.
- **The meeting mark**: a meeting recording carries `meeting: true` in every `RecordingTransfer` (each segment and the end message); a watch with an older app sends none, which reads as a note. The phone keeps the mark when the recording arrives (`RecordingLibrary.receive`), names it after the calendar event at the recording's start (`MeetingRecorder.tagWatchRecording`; the watch reads no calendar), and uploads `kind: meeting` with the event as `calendar` when one matched. A note made on the watch during a calendar event becomes that meeting's too, as the contract allows.
- **Minutes on the watch**: the phone relays a short version of its newest meetings' minutes (at most 8 meetings from the last week; 4 points, 4 decisions, 4 action items each, 120 characters an item; `WatchMinutes`) in the WatchConnectivity application context, and the watch shows them under **Meeting Minutes** (`WatchMinutesList`, `WatchMinutesView`): ready, "Writing the minutes…", or failed. The watch never talks to the hub for them. `MinutesRelay` reads them from the hub when the phone app comes to the front (again every 3 minutes while it stays there and some are still being written) and whenever a recording's page loads them, so minutes written while the phone app is closed reach the watch the next time it is opened (the hub's "minutes ready" notification is the usual reason to open it).
- **Long recordings**: the watch app records with the `audio` background mode, so recording goes on with the wrist down and the screen off; an hour is about 14 MB (32 kbps AAC). Segments are 30 s while the phone is reachable and 60 s otherwise; the watch sends them in batches every 3 minutes (5 in Low Power Mode) and keeps each until the phone confirms it is saved. It stops by itself, keeping what was recorded, under 5 % battery off the charger or under 150 MB of free space. A call or Siri pauses it like the phone's (the open segment is closed at once) and it resumes when they end. Battery use is not measured yet: continuous microphone capture, AAC encoding and the transfers are the costs; check it on a real watch with a one-hour recording.
- **Verified in the simulator**: the watch app builds; screenshots of the home entry, a meeting being recorded and the short minutes (`-showMeetingSample recording|minutes`, `-openWatchItem minutes|minutes-writing`). The real watch checks are in the device checklist: wrist down for an hour, the screen off, the phone out of range and back, and minutes reaching the watch.

## The live recording sheet and the wrap-up (#4240)

Design and reasons: `docs/design/proposals/meeting-recording.md`. On iPhone a running recording has one place, a sheet (`RecordingLiveSheet`, large detent, own navigation stack) that `.recordingLiveSheet()` presents from both hosts of `RecordingsSection` (Knowledge's list and `RecordingsPage`): when a recording starts from this page, when the page appears while one runs (Live Activity tap, drawer, Siri, notification: `MeetingRecorder.openRecordings` bumps `RecordingLivePresenter.serial`), and from the now-recording card. **Hide** (or a swipe down) keeps recording and leaves the card, one row with dot, title, time and state that is not repeated in Recent Recordings.

- Top to bottom: title (with the page menu, Meeting / Note, and Hide), Meeting chip and state line (dot, or a pause glyph, never color alone), the timer (`Text(timerInterval:)`, the system ticks it), a level meter (`RecordingLevelMeter`, 30 fps clock, one still frame with Reduce Motion or Low Power; the level is `RecordingCapture.inputLevel`, the writer's own smoothed level), the Persona strip when the hub offers attending (role menu, Who Attends…, problem text as a `NoticeBanner`), and one timeline (`RecordingTimeline`): transcript sentences, bookmarks and Persona exchanges in time order. Sentences arrive per finished segment (about every 30 s), so the audio newer than the last sentence shows as "Transcribing the last N s" and an empty timeline says "Listening…". The timeline follows the newest item unless the reader scrolled up; then **Jump to Latest** appears with the count of new items (`RecordingFollow`).
- The bottom bar stays under the thumb: **Ask Persona** (prominent, only when Persona attends; keyboard button for a typed question, written in a row of the bar), then Pause / Continue, Stop and Bookmark. At accessibility sizes it stacks, holds Ask Persona and Stop, and a More menu has Pause, Bookmark and Type a Question.
- **Stop asks first** ("Stop Recording?", the audio is saved on the device and its text goes to Knowledge, a stopped recording can't be continued). The Live Activity's, Control's and Siri's Stop stay one tap.
- **Wrap-up** (`RecordingWrapUp`): after Stop the same sheet cross-fades to the recording's steps, worked out from state the phone already has (`RecordingPhase`): Saved, Transcribed, Syncing to Knowledge (with "Waiting for the hub" when unpaired or offline), Writing minutes (meetings), Minutes ready. A failed step shows its reason and the existing action (Resume Transcribing, Retry for minutes). **Open Minutes** / **Open Recording** (the one solid button) pushes the recording's page inside the sheet, **Done** (top trailing, where Hide is while recording) closes it; everything goes on in the background.
- DEBUG: `-showMeetingSample flow` (no microphone) records a made-up meeting by itself: the clock and level move, a line is heard every 6 s, Pause, Bookmark and Stop work through DEBUG sample paths in `RecordingCapture` (the entry ends, `onSaved` runs), so the wrap-up reaches the minutes against the mock hub (`POST /mock/recording_minutes {"mode":"ready"}` before the launch; the app asks again every 15 s). The existing values (`recording|paused|interrupted|listening|taking-part|answered|minutes|pending`) work as before, and their Stop now ends the sample too. UI test: `MeetingRecordingUITests.testLiveSheetFlowReachesTheMinutes`.
- The recording's page (`RecordingDetailView`): title "Recording", a compact header (title, when and where on one line, attendees, one source line), the minutes straight after it, the transcript, then the actions and, last, the retention paragraph. "Writing the minutes…" is a row with a spinner; a failed write shows the reason and a **Retry** button (`minutes-retry`); the sections unfold with `Theme.Motion.reveal` (not with Reduce Motion); time chips have a 44 pt hit area; transcript rows are identified by where their sentence starts.
- visionOS keeps the earlier in-list controls; the new views are iOS only.

## Minutes

The hub writes minutes for a meeting recording once its text is complete (#2710; the hub side and the field contract are `docs/assistant/meeting-minutes.md`). Every upload says `kind: "meeting"` or `"note"`, and a meeting the device matched to a calendar event adds the event as a hint, `calendar: {title, attendees, location}` (no event id: the hub can't see the phone's calendar). `RecordingUpload` is the one place that writes them. A phone recording is a note only when Note was picked for it (at the start or on its live page); a watch recording becomes a meeting only when the phone matches it to an event.

- On iPhone, once they are ready, the recording's page gets a segmented switch right under its header, **Summary | Transcript** (#5026). **Summary** is the default and takes the place of the minutes' sections; the choice lasts while the page is open (no setting). Before the minutes are ready (still recording, waiting for the text, being written, failed, not a meeting) the page is as it was, with no switch.
  - **Summary** (`RecordingMinutesSummary`): a header card (the minutes' title when it differs from the recording's, when · where, the attendees; "Nothing to note" when every group is empty; Updating, a failed rewrite, an expired transcript and **Regenerate**, below), then one card per group with something in it, in this order: Key Points, Decisions, Open Questions, Action Items, each headed by its icon, title and count. An item shows its words, for an action who and by when, the meeting photo it cites (below), and its time as a chip: a tap switches to **Transcript**, scrolls to that line and lights it. Its chevron opens, in place, the transcript lines around its time (the line `RecordingMinutes.sentenceIndex` picks, `Theme.Recording.contextBefore` before it and `contextAfter` after it), so the details read without leaving the Summary. The Action Items card carries **Add to To-Dos** / **Added** on each item and ends on **Add All** (below).
  - **Transcript**: the transcript as before, without the minutes' sections above it.
  - What the Summary shows is worked out by `RecordingMinutesCards` (`apple/Shared/Recording/RecordingMinutes.swift`) when the minutes or the transcript change, not in `body`; tests: `apple/PersonaTests/MinutesCardsTests.swift`, `apple/PersonaUITests/MinutesCardsUITests.swift`.
- visionOS shows them above the transcript (`RecordingMinutesSections`): the title when it differs, when and where, the attendees, then key points (who said it, when known), decisions, action items (who, by when, as said) and open questions. Each item carries `at`, seconds into the recording; tapping the time scrolls the transcript to that line and highlights it for two seconds (`RecordingMinutes.sentenceIndex`: the last line that started by then).
- When they were is written from `start` / `end`, in the header's date style through `RecordingFormat.span` (the end's time alone on the same day, its date too on another; the hub's `when` only from a hub without them, #4875), in the Summary's header card and in the sections alike (`RecordingMinutes.whenLine`).
- **Copy** and **Share** send them as Markdown rendered on the phone (`RecordingMinutes.text`), every item with its time. On iPhone they sit under the switch, the same in both views.
- **Action items as to-dos** (#4062, as the web and Android): each action item has **Add to To-Dos**, or **Added** once it is one (`todo_id` set). **Add All** appears when more than one is left. Both send `POST /api/v1/recordings/{id}/minutes/todos` with the `version` shown, and the page reads the recording back before another write is allowed. `409 minutes_stale` (rewritten since they were shown) shows the new minutes and says they changed; nothing is sent again. A hub whose minutes have no `version` or action `id` offers neither. On iPhone they are in the Summary's Action Items card (Add All at its end).
- **Regenerate** sits under Copy and Share (on iPhone in the Summary's header card, `RecordingMinutesUpdates`) when the whole transcript is on the hub, nothing is being written and the transcript hasn't expired (`trimmed_at`). The minutes shown stay while the hub writes new ones ("Updating the minutes…", read again every 15 s). If the new ones fail, the shown ones stay and the page says so. An expired transcript keeps its minutes and says so, with no Regenerate.
- Until they exist the section says what they wait for: the recording to end and be transcribed, the text to reach the hub, or "Writing the minutes…" while `detail.meta.minutes_state` is `pending` (asked again every 15 s while the page is open: `RecordingMinutesModel`). `failed` offers **Retry** (the wrap-up says the same) (`POST /api/v1/recordings/{id}/minutes`; a `409` means the hub doesn't have the complete transcript yet). A note shows minutes only if the hub wrote some.
- Read from `GET /api/v1/kb/recordings/{id}`: `detail.minutes` (`version`, `title`, `when`, `start` / `end` (RFC 3339 with the user's offset), `where`, `attendees`, `points`, `decisions`, `actions` with `id` / `who` / `what` / `by` / `due_date` / `todo_id`, `open`), `detail.meta.minutes_state` and `detail.meta.trimmed_at`. Sections a hub leaves out are empty.
- Mock hub: `MOCK_RECORDING_MINUTES=ready|pending|failed` (`scripts/mock/mock_hub.py`; `POST /mock/recording_minutes` switches it) writes made-up minutes in that shape from a meeting's own sentences when it is stored complete (`when`, `start` and `end` from its start and length, as the hub writes them), leaves them pending, or fails them. `POST /mock/recording_trim {"id"}` turns a stored one with minutes or a day recap into a trimmed recording (its transcript expired); `MOCK_RECORDING_DAYS` and `MOCK_MINUTES_DAYS` start the two retentions. Its minutes carry `version` and action ids. `POST …/minutes/todos` makes the to-dos (`409 minutes_stale` for an old version), `POST …/minutes` keeps the shown minutes while the next version is written (3 s later in `ready` mode), and `POST /mock/recording_minutes_rewrite {"id"}` writes the next version at once. Day recordings are on by default (`MOCK_NO_RECORDING_DAY=1` disables them); `MOCK_DAY_RECAP=ready|pending|failed|none` sets their recap mode (default `ready`), and `POST /mock/recording_day {"on": true, "recap": "ready"}` switches it (`GET` reads it back).

## Persona in meetings

Persona can attend a meeting being recorded on the iPhone (#2981; the iPhone side is #3375). The contract, with every field and error, is [meeting-minutes.md](../assistant/meeting-minutes.md) "Attending a meeting"; this is what the app does with it. Only a hub that lists the `meeting_attendance` feature gets any of it: with another hub the Recordings page, the upload and the Live Activity are exactly as before. The redesign (#3862) replaces the attending session with the meeting's own room conversation and a meeting page, on a hub that lists `meeting_conversation`: [meeting-minutes.md](../assistant/meeting-minutes.md) "Meeting conversation (#3862)". The iPhone does phase 1 of it, phase 3, photos, and phase 6, talking about any recording ("The meeting conversation (#3862)" below); a meeting started on a hub without it, or before the hub listed it, keeps everything this section describes.

- **Role, chosen as the recording starts**: **Off** (the hub writes the minutes after the meeting, as before), **Listen** (Persona follows the meeting; answers show on screen only), or **Take Part** (they are also said aloud). The Recordings page shows the choice under **Record** for a meeting (`RecordingCaptureControls.personaRow`), pre-selected from the hub's `meeting_attendance_default`, which **Settings › Meetings** changes (`MeetingSettingsView`, `PUT /api/v1/assistant`). A pick on the page is for that one recording. The drawer, the home screen's menu, the Control, Siri and the Action button start with the default and the session picked last unless that was the main chat, else with Persona off, so they stay one tap (never the main chat by default, #3862); the last word from the hub (whether it offers attending, its default) is kept for a start without the app in front (`MeetingAttendanceDefaults`).
- **Who attends**: one session per recording (`MeetingAttendees`): the main chat (sent by leaving `attending_session` out), Persona's other open chats, or an Agent that takes messages; never one the hub would refuse ("Sessions that never attend": a work computer's, another manager's, the health space's, a chat whose replies leave the apps or that an extension opened, one in `bypassPermissions`). The one picked last is pre-selected while it can still attend, unless it was the main chat: the main chat attends only when picked for that meeting (`RecordingAttendance.mainPicked`; owner decision in #3862, since every line fed to a chat fills its context). With nobody chosen the role shows Off; Listen or Take Part shows Who Attends: Choose with "Choose who attends. Until then Persona stays out of the meeting.", a start without a choice starts with Persona off, the picker never pre-selects the main chat, and turning Persona on mid-meeting (the strip's Turn on Persona or its role menu; `MeetingRecorder.setRole` returns false) opens the picker first and turns on once someone is picked. The upload holds it whatever built the attendance: an attending role with no session named and the main chat not picked goes as `off` (`RecordingAttendance.sentRole`, which the stored mark, live lines and asks follow too), so `attending_session` is never left out by default. Kept on the recording as `RecordingEntry.attendance` (`RecordingAttendance`), on this phone only.
- **The upload** (`RecordingUpload`, `RecordingSync`): an attending recording goes at once, ahead of the others and without the save debounce, before it has any text, and every later upload carries the same `attendance` and `attending_session`. A meeting never attended carries neither. The `200` says what the hub took (`attendance`, `attendance_problem`); a `200` without `attendance` is an older manager, and the phone stops attending that meeting (`stopped: unsupported`).
- **Live lines** (`MeetingLive`): every finished sentence that ended while Persona attended (`RecordingAttendance.periods`) and is not its own voice joins the recording's append-only live log, once per segment, and goes to `POST …/live` from the hub's `next`, at most 200 a request, only after the hub stored the current attendance. `409 live_gap` asks where the hub is and goes on from there; `404 recording_not_found` and `409 not_attending` upload first; a `404` without that code is an older manager (stop attending); `413 live_full` stops the lines but not the questions; `503` and a dropped connection back off (2 s … 60 s). The hub's `problem` shows on the recording.
- **While it records** (`MeetingAttendanceSection`, below the recording's controls): "Persona is listening" / "Persona is taking part" as the visible notice, the role (changeable for the rest of the meeting: turning it on mid-meeting sends only what is said from then on), who attends, **Ask Persona** and **Type**, and the last three answers with what they drew on (`sources`).
- **Asking, by a tap only** (no wake phrase; `MeetingAsk`): **Ask Persona** asks about what was just said: the open segment is cut at the tap (`RecordingCapture.flushSegment`), transcribed and its lines sent, then `POST …/ask` goes with `at` and `live_next`, and the question the hub heard replaces "What was just said". **Type** opens a question row in the live sheet's bottom bar, not a system alert (#4486, DESIGN_SYSTEM.md 4.4): the composer's field, which grows to a few lines, and its send button, which waits for words; Return sends too, and the keyboard button becomes Cancel. The timeline and the earlier answers stay in sight while typing. Each question has the phone's own id; a `202`, a timeout or a server error sends the same request again a few seconds later, for up to 4 minutes. A failure says what happened in plain words and offers Retry with the same id. An answer names the earlier meetings and projects it drew on (`sources`), a meeting with when it was in the minutes' date style (its `start` / `end`; `when` from a hub without them, #4875).
- **Spoken answers** (`MeetingSpeaker`): an answer with `speak` (taking part) is said at most once, only for a question asked in this run, only while the recording runs and the role still speaks (checked again just before the first sound), and only when it came within a minute of the tap. It is said in the realtime voice of voice chats when the hub offers it (#5004): the voice extension runs, is set up and lists `speak` in `/info` (`MeetingVoiceRoutes`, asked again after a minute). The phone sends the model of the use `plugin.<id>` and the voice picked on this device (`VoiceTimbre`) to `POST …/speak` and plays the PCM as it streams in (`MeetingVoicePlayer`, an engine of its own in the recording's session, holding the first 0.3 s). Without that voice, on an error (offline, `no_upstream`, `upstream_*`, an older hub's 404), with no audio at all, or when no audio has come within 8 seconds, the phone's on-device voice for its `lang` says it instead (`AVSpeechSynthesizer`, `MeetingDeviceVoice`). Either way it plays in the recording's own audio session, so the recording goes on. A realtime answer cut off midway plays what came and is not said again. Where it plays follows the voice call's route rule (`VoiceRoute.output`, #3015): a connected headset, else the loudspeaker, never the earpiece; the recording keeps its microphone. A recording that starts as Take Part lets a Bluetooth headset play (`allowBluetoothA2DP`, listening only; `RecordingCapture.playsAloud`), which an Off recording's session never does. **Stop Speaking** cuts it short.
- **Its own voice is not the meeting's**: every stretch the phone speaks in is kept on the recording (`RecordingAttendance.spoken`), from when its audio actually begins (the realtime voice may take a moment to start; what the room says meanwhile is the room's), and a sentence at least half inside one is left out of the transcript, the upload and the live log (`RecordingEntry.sentences`; a line transcribed while the answer is still being said is checked against the stretch so far). Persona's answers stay on the hub as the meeting's `asks`, not transcript lines.
- **The Live Activity**: under the name, "Persona listening" or "Persona taking part"; the latest answer, cut to two lines (the hub posts no notification for answers); **Ask Persona**, and **Turn Off Persona** (`TurnOffMeetingAttendanceIntent`) beside Bookmark and Stop, which give up their words for their marks so the four fit. Ask and Turn Off work from the Lock Screen without unlocking, as Stop does: the hub answers a meeting only from meeting material.
- In the attending session the meeting's batches show as messages from **Meeting** (`MessageSource` kind `meeting`).
- Screenshots and UI tests: `-showMeetingSample listening|taking-part|answered` (a made-up meeting Persona listens to, takes part in, or takes part in with two answers, the newest being said); `-openMeetingSettings YES` for Settings › Meetings. The mock hub serves the whole contract (`scripts/mock/mock_hub.py`, "Attending a meeting"); `POST /mock/meeting_attendance {"on", "default"}` switches the feature and the default before a launch, and `GET /mock/meeting_attendance` reads back each recording's role, live log and questions (`MeetingAttendanceUITests`).

### The meeting conversation (#3862)

Phase 1 of the contract ([meeting-minutes.md](../assistant/meeting-minutes.md) "Phase 1: the meeting conversation"), on a
hub that lists `meeting_conversation` beside `meeting_attendance`. The page's design and its identifiers are in
[DESIGN_SYSTEM.md](../design/DESIGN_SYSTEM.md) "Meeting page (#3862)".

- **A room meeting**: a phone meeting recording started while the hub listed the feature (`MeetingAttendees.conversation`,
  from the last `hello`; every hub `hello` keeps the meeting flags in `MeetingAttendanceDefaults` for a start without the
  app in front, so a cold one-tap start goes by the last hub it heard). The recording keeps
  what it started with (`RecordingAttendance.room`, `photos`, `privateMark`; `RecordingEntry.roomMeeting`), whatever the
  hub lists later.
- **Meeting content never reaches the main chat**: a room meeting's uploads carry its role only inside `room`, never the
  top-level `attendance` or `attending_session`, whatever its attendance holds (`RecordingUpload`), so the hub feeds its
  lines and asks to the room alone and an older manager after a rollback sees a meeting that is off; only Just me (the
  owner's explicit switch) sends to the main chat. One-tap starts on such a hub are room meetings too. A meeting started
  on an older hub follows "Persona in meetings" above, where the main chat attends only when picked. Pinned by
  `MeetingConversationTests` (`aRoomMeetingNeverNamesAnAttendingSession`, the one-tap start),
  `MeetingAttendanceTests` (the old hub's defaults and the upload's guard) and
  `MeetingConversationUITests.testRoomMeetingAnswersATypedQuestionInTheThread` (the room holds the meeting's lines; the
  main chat's items are the same before the meeting and after its lines and a question).
- **Starting**: the Recordings page shows the role and **Private Meeting** (off; "Its minutes are never used when Persona
  answers in later meetings."), and no Who Attends: the meeting's own room conversation attends. One-tap starts (drawer,
  home screen menu, Control, Siri, Action button) use the default role and not private (`MeetingAttendees.startAttendance`).
  A room meeting stores its attendance whatever the role, Off included, so it uploads at once.
- **The upload**: every upload of a room meeting carries `room: {"attendance": <role>}` (`off` too) and `private`, never
  the top-level `attendance` or `attending_session`. Live lines and asks wait for a `200` that carries `room` (the stored
  mark is `room|<role>`); its `room` goes to `MeetingConversation` (`RecordingSync.onRoom`), and an upload with `ended_at`
  that the hub stored is noted (`RecordingSync.endedStored`).
- **An older manager after a rollback**: a `200` without `room` is not permanent. The recording goes on uploading `room`,
  no `…/live` or `…/ask` goes, the page says the hub can't take part right now (`RecordingSync.roomMissing`), and the
  Live Activity shows Persona as not attending, until a `200` carries `room` again. One rule decides whether Persona can
  be asked (`MeetingAsk.canAsk`: it attends and the room isn't missing): the page's subtitle and header, the bar (the field
  asks nothing of the room, [+] goes), `MeetingAsk.ask` and `retry` (no exchange, so no forced upload) and the Live
  Activity follow it. Just me stays. After the meeting such a hub has no follow-up: the composer says so.
- **The hub's state of the meeting** (`MeetingConversation`): the room (`MeetingRoom`: state, ids, problem; a state the
  app doesn't know reads as `starting`), the private mark, the follow-up and the asks, from the upload's `200`, the `200`
  of `POST …/live` and `GET …/live`. A page on screen watches its recording (one watch per page) and reads it whole once;
  a `meeting_live` event reads only the parts it names that are newer than those read (`?parts=`), and only the parts
  phase 1 shows (`room`, `asks`, `private`, `followup`); a reconnect reads every watched meeting again. An upload's or a
  live POST's late answer never takes the room back to an earlier state. `404` / `410` mean the hub has no such recording.
- **The meeting page in the sheet**: `RecordingLiveSheet` shows `MeetingPage` for a room meeting in place of the live
  view, and after Stop in its after mode in place of the wrap-up; every way in (the drawer row, the Live Activity, a
  notification, Recordings) still opens the sheet. The page has the state line, a subtitle with the role, "On this
  iPhone" and "Private", the room's problem (`runtime_unsupported`, `node_too_old`, `machine_offline`, `start_failed`: then
  questions are answered from the meeting so far), the **Live** box (the last three lines, how far behind the transcript
  is, **Full Transcript**), the thread, and the bar: Just me, the composer and its `[+]` menu (**What Was Just Said**, the
  heard ask), Pause, Bookmark, Stop.
- **The thread** (`MeetingThreadView`, read apart by `MeetingThread`, a port of the web's `meeting-thread.ts`): once the
  room has ids the page subscribes to it like any chat (WS `subscribe`, **Show Earlier** pages back) and shows its items:
  Persona joined, batches as "N lines from the meeting", heard questions as "What was just said (m:ss)", the owner's
  questions, Persona's answers (consecutive replies merged), the end. An ask the room's thread doesn't hold yet, a failed
  one and one answered from material keep the `MeetingExchangeRow` look; without ids ("Persona is joining the meeting…",
  or a room that never came up) every ask shows that way.
- **Asking**: the composer asks the room (`…/ask` as #3746 does, one at a time); the answer records how it went (`via`).
  A room ask the hub fails at once with `room_unavailable` (the room went unavailable) is asked once more under a new id,
  which the hub answers from the meeting's material; `meeting_ended`, `room_unavailable` and `no_answer` have their own
  words.
- **Just me**: with the switch on, the composer sends `POST …/just-me` (id, text, images) and the thread shows those
  questions and the main chat's answers after each one (by its `client_msg_id`) until the end of its turn: the next user
  text of any kind, one the hub puts in (a task's notice, a scheduled run) included (`MeetingThread.justMeAnswer`). The
  main chat's reply being written shows only while the question is in the chat and its turn the newest; a card the turn
  raises (since the question, while its turn is the newest) is answered in the main chat, which the thread says. Just me lives only in
  `MeetingConversation`: never in `MeetingAsk`, the Live Activity, `MeetingSpeaker`, VoiceOver announcements or the room.
  Retries follow the hub's receipts: the same id only when no answer came, on `503 assistant_offline` or a pending
  receipt; any other answer ends that id and **Retry** sends the words under a new one. A `404 recording_not_found`
  uploads the recording first and sends once more.
- **The private mark**: the page menu's **Private Meeting** (`PUT …/private`); shown as the hub has it once it said
  anything (a read of `private`, a change, an answer carrying it: `Meeting.privateRead`), absent counting as private; the
  start's choice stands in only until then. The recording's page has the same switch for every meeting on such a hub
  (hidden after `409 not_meeting`). A refused change says "Couldn't change Private Meeting. Try again." unless the hub
  names a reason (`MeetingConversation.privateMessage`).
- **After the meeting**: the after page shows the status card (when, the minutes' state, **Open Minutes** / **Open
  Recording** in the sheet and where the minutes push opened it, kept above the thread), the room's thread with the asks answered from material where they
  were asked (or the hub's `asks` when the room never came up), **After the Meeting**, then the follow-up. Its composer
  waits while the hub would answer `409 meeting_running` (`MeetingConversation.running`): the recording runs, an upload
  on its way may carry the `ended_at` the hub doesn't have yet (within 12 hours of the last upload it stored, after which
  the hub ends the meeting itself), the room is still starting, ready or unavailable, or a room meeting's room isn't read
  yet. The hub's word goes first: a room `ended` or `deleted` has passed the end, and a whole read without a room, like
  an older manager, has no room to wait for. A refused upload or a paused sync isn't on its way: the composer says the
  sync's problem instead of "in a moment" until those 12 hours are over (`followupHold`). The first message goes
  to `POST …/followup` (it makes the follow-up with that message), the next ones to the follow-up's
  `POST /api/v1/sessions/{mid}/{sid}/messages` with the message's id as `client_msg_id` and op id; a send keeps the route
  it started on. A `400` / `409` / `413` ends that id (Retry sends the words under a new one); no answer, a pending
  receipt, a 5xx or a `404` keep it. A sent message leaves the pending list once the follow-up's items hold its
  `client_msg_id` (`followup-<id>` or the id). Just me and `…/followup` take at most 8000 characters: over that Send
  stays off with a caption saying so, and nothing is cut (the follow-up's own messages have no such limit).
- **The recording's page**: for a meeting on such a hub a **Private Meeting** row, and for any finished recording with
  something to talk about **Talk about this meeting**, which pushes the after page ("Talk about this meeting (phase 6)"
  below).
- **Screenshots and tests**: `-showMeetingSample room-listening|room-taking-part` (the made-up meeting as a room meeting,
  listening in or taking part). The mock hub serves the phase (`POST /mock/meeting_conversation {"on", "room",
  "end_seconds"}`, `GET` reads back each meeting's room, mark and follow-up; the fresh start `POST /mock/recording_minutes`
  also takes Just me's messages and their replies back out of the main chat). UI tests: `MeetingConversationUITests` (the
  start's choices on a hub with and without the feature, a typed question answered in the room's thread, Just me reaching
  only the main chat, the private switch, the follow-up after Stop); `MeetingAttendanceUITests` turns the feature off to
  keep covering #3746. Unit tests: `MeetingConversationTests` (the upload and its `200`, the hub's state and events, the
  private mark and when the hub's word replaces the start's, Just me and the follow-up with their retries and the 8000
  limit, when the follow-up waits, asks while the room is missing, `room_unavailable`, the words) and `MeetingThreadTests`
  (the web's cases, the mock's items, where a Just me answer ends, asks answered from material in place).

#### Talk about this meeting (phase 6)

The contract's "Phase 6: talk about this meeting" ([meeting-minutes.md](../assistant/meeting-minutes.md)): the follow-up
from any recording, by text and voice, through phase 1's `POST …/followup`; no flag of its own, so it shows on a hub that
lists `meeting_conversation`, and an older hub keeps today's page.

- **Which recordings** (`MeetingTalk.offered`): any recording on this phone that ended (not a rolling or an all-day one),
  whatever recorded it (a note, a meeting no room attended, a #3746 meeting, the watch's), with words in its transcript,
  minutes ready, or a conversation the hub holds for it (a follow-up, or a room that came up). Whether the transcript has
  words is worked out when the recording changes (`MeetingTalk.hasText`, stopping at the first line; performance.md P4),
  not on every redraw. Past retention (`RecordingSync` status `expired`) the words still on this phone don't count: the
  hub has cut the transcript (and the room with it) or deleted the recording, so only minutes, a follow-up or a room
  whose conversation the hub still has do. Its page has one row,
  **Talk about this meeting** (`recording-talk`), in the section under its header beside Private Meeting; while it
  records a room meeting's page is the live sheet instead.
- **The thread without a room**: the row pushes `MeetingPage` in its after mode, which reads `GET …/live` (any recording
  answers it) and never waits on a room: a recording with no room, or whose room is `none`, `ended` or `deleted`, writes
  its follow-up as soon as the hub has its end (`MeetingConversation.running`). The thread is the hub's `asks`, if any, then
  the follow-up; with nothing before it there is no **After the Meeting** divider, and until the follow-up starts a line
  says what to ask ("Ask Persona about this meeting: what was decided, who does what, or a follow-up email."), once the
  hub has answered for the meeting (`loaded` or `missing`): before that read the follow-up and the room aren't known even
  where they exist, and the line would flash. The first
  message makes the follow-up (`…/followup`), the next go to its messages, as after a room meeting.
- **Voice**: once the follow-up exists, the composer has **Voice Chat** before the field (`meeting-voice`), where the hub
  has a voice extension that speaks protocol 2 (`VoiceSupport`, asked at most once a minute) and nothing records. The call
  is an ordinary one bound to the follow-up (`target` the follow-up, no meeting mode, `VoiceEntry.start`); its bar takes
  the composer's place while it runs (`VoiceBar`, as in a chat), with the line a call that ended by itself leaves, and
  leaving the page ends it. With no follow-up yet there is no voice: the first request is typed, so no turn runs on the
  meeting's material alone. Dictating that first request is not done yet.
- **The minutes push** carries the recording's id, `recording_id`, at the top of its payload beside `machine_id` and
  `session_id` (a field older hubs leave out); a tap opens Recordings with that recording's thread
  pushed over it (`MeetingRecorder.openMeeting`, `MeetingPageOpener`; a request no Recordings page took within 30 seconds is
  dropped). Its Back returns to Recordings, so the page keeps **Open Minutes** / **Open Recording** to the recording's page
  (`opensRecording`, with no Done), as in the sheet. With another recording running, the live sheet doesn't present
  itself as Recordings appears for the push (`RecordingLivePresenter.holdOnAppear`, once, within those 30 seconds), so it
  doesn't cover the meeting; the now-recording card is the way back to it. A push without the field, a recording that isn't on this phone (made on another device) and a dismissal open
  what they opened before: the main chat (`MeetingTalk.threadToOpen`).
- **Tests**: `MeetingTalkTests` (which recordings offer it, past retention too, `hasText`, a note writing its follow-up at
  once, the push's field and where a tap goes, the opener, the live sheet held for the page the push opened); `TalkAboutMeetingUITests` (a finished meeting no room attended: the row, the thread without a
  divider, a question, the mock's follow-up answer; screenshots `talk-<appearance>-detail` and `-thread`).

#### Photos (phase 3)

The contract's "Phase 3: photos" ([meeting-minutes.md](../assistant/meeting-minutes.md)), for a room meeting started
while the hub listed `meeting_photos` (`RecordingAttendance.photos`, kept for life like the rest of the start; a hub that
lists it later adds nothing to a meeting already running). The row, its states, the delete confirmation and the viewer
are in [DESIGN_SYSTEM.md](../design/DESIGN_SYSTEM.md) "Meeting page (#3862)". The core case is a whiteboard: shoot it,
see it in the thread at once, then "Used as material", then the room's note on what in it matters.

- **Adding**: while the meeting records, the composer's `[+]` menu has **Take Photo** (only where `CameraPicker.isAvailable`)
  and **Choose Photos** (`PhotosPicker`, images only, at most `Theme.Recording.photoPickLimit` at once). Not with Just me
  on (photos are the meeting's material, never private), not after the meeting, not for a meeting started without the
  feature. A photo's `at` is the recording's clock (`RecordingCapture.shared.timeline`) when it was taken or picked.
  Picked photos show at once as Preparing… rows (`MeetingPhotos.pick`); Photos hands each one's bytes over when its
  turn to be prepared comes, and one it can't (still downloading from iCloud) leaves again and is said in a line under
  the field (at accessibility sizes among what the page scrolls). Adding a photo plays no haptic.
- **Prepared on the phone, off the main actor, one at a time** (`MeetingPhotoPipeline`, the store's queue; the original
  is let go of once downscaled, before Vision reads the small copy): ImageIO downscales it to at most 1600 px on its
  long side (never up) and encodes a JPEG at quality 0.75, then 0.6, 0.45 and 0.3 until it is at most 1 MiB; a PNG's
  transparency goes onto white. Vision (`VNRecognizeTextRequest`, `.accurate`, language correction) reads its words in
  the recording's languages (`zh-CN`: Simplified Chinese and English, `ja-JP`: Japanese and English, otherwise English);
  the lines go as `text`, cut to 8000 characters, and no `text` when nothing was read. A camera picture becomes JPEG
  bytes off the main actor before it goes in; nothing decodes an image with `UIImage(data:)`
  (`scripts/test/test_apple_perf_patterns.py`).
- **The upload** (`MeetingPhotos`): `POST …/photos` with `id`, `at`, `mime`, `data` and `text`, at once, under the photo's
  own id (lowercase). No answer, a 5xx, `408` or `429` sends the same id again after 2, 4, 8, 16, 30 and 60 s, then it
  fails and **Retry** sends the same id. `404 recording_not_found` uploads the recording first and sends the same id once
  more; `409 op_id_reused` sends it under a new id; `410 photo_deleted` (stored, then deleted elsewhere) and `410
  recording_deleted` drop it for good (its JPEG and its entry in `photos.json` too). Any other refusal fails at once with
  its own words (`photos_full`, `transcript_expired`, `not_meeting`, `kb_read_only`, `forbidden`, a picture the hub can't
  read); a `404` without a code is a hub without the photo routes, said so and not tried again on its own. Whether Retry
  can help is kept with the failure (`Local.State.failed(…, retry:)`, `MeetingPhotos.canRetry(after:)`), over a relaunch
  too: not for a picture the hub can't take, `not_meeting`, `transcript_expired`, `forbidden` or a hub without the
  routes, which show Delete Photo alone; yes after no answer, a 5xx, `kb_read_only` or `photos_full`. A `200` stores it:
  its photo joins the hub's list, which is read again soon after, and the local copy leaves once the list has it (the
  thread keys photo rows by the lowercase id, so the row stays where it is). **An answer lost**: a list that has a photo
  still uploading or failed here (its first `POST` stored, the answer lost, every try since failed or waiting out the
  backoff) settles it: the local copy leaves (its JPEG stays for the thumbnail) and `photos.json` forgets it, so the
  row reads Used as material, the next try never goes, and a try still on its way changes nothing when it comes back
  (nor does a late `200` bring back a photo deleted meanwhile). A relaunch whose list came first doesn't show it failed
  again.
- **Kept over a relaunch**: a photo not stored yet keeps its JPEG at `<recording folder>/photos/<id>.jpg` and its state in
  `<recording folder>/photos.json`, written atomically off the main actor. `MeetingRecorder.start()` calls
  `MeetingPhotos.shared.start()`, which sends again those that were on their way, keeps a failed one (with Retry when it
  can help) and drops one still being prepared (its original is gone). A stored photo's JPEG stays for the thread's
  thumbnail while the hub lists it: once a list read whole (every entry read, or at least named) no longer has it
  (deleted elsewhere, cut with the transcript), its JPEG and its thumbnails go too. The folder is checked against the
  list after each such read, once the launch read of `photos.json` is in, so this holds after a relaunch, and a photo
  still to send keeps its JPEG.
- **The hub's list**: `GET …/photos` (`MeetingPhotos.refresh`, one read per recording at a time, calls meanwhile sharing
  the next; it says whether a list was applied) is the truth for every stored photo: its number, the room's `note`,
  `cited`, `forwarded`. For a meeting that takes photos `MeetingConversation` also reads the `photos` revision of `GET
  …/live` (`?parts=photos` carries only the revision): a `meeting_live` event or a `…/live` read with a newer one reads
  the list again, as does a page that starts watching, and the revision counts as read only once that list applied, so
  a read that failed (a timeout, a hub switch) is tried again by the next event or `…/live` read. Only `404
  recording_not_found` and `410 recording_deleted` list nothing; any other refusal (a `404` without a code: a manager
  without the routes) leaves what was listed, and a list never read stays unread.
- **The pages' view** (`MeetingPhotos.Album`, one per recording): each photo's row content worked out once per change
  (its number, time, first lines, note, `added_at` read once, whether it has a picture and whether Retry can help), the
  order of those the room's part doesn't hold, the numbers for the minutes, whether the list was read, and the deletes
  running and their problems. The thread reads only the order; each photo row is its own view that reads the rest
  (docs/apple/performance.md P4, P5).
- **In the thread** (`MeetingThread`): the room's two items of a forwarded photo (the image and the line asking for its
  note, both `client_msg_id` `photo-<id>`) are one `.photo` entry where the room got it, never an image, a batch or a
  question. The hub's line withdrawing a deleted photo (`photo-withdrawn-<id>`) shows nothing; the end batch is only the
  room's last line from the meeting, and only once the meeting ended (`room(…, ended:)`). Photos the room thread doesn't
  hold (not forwarded: the role off, the room not up; forwarded with its message not among the room's pages loaded; or
  still on their way) follow the room's entries, oldest `at` first, those the hub doesn't list yet last, and move to
  their message once a page with it is loaded; none is ever hidden. A `photo-<id>` the hub's list no longer has, once it was read, is **Photo
  removed**. The room's note shows under its photo; "Persona is looking at the photo…" shows while a forwarded photo has
  no note and the room is ready, for at most `Theme.Recording.photoNoteWait` after it was added. After the meeting the
  page shows the same photos from the list, with their notes and **Cited in the minutes**; it adds none.
- **Thumbnails and the viewer**: from the JPEG kept on the phone, else `GET …/photos/{n}`, downsampled off the main actor
  to `Theme.Recording.photoThumbSide` points at the screen's scale and kept in a memory cache that counts bytes. The viewer
  shows the whole picture from the same sources.
- **Deleting** (`MeetingPhotos.remove`): the photo's long-press menu, a failed photo's Delete Photo button or its
  accessibility action, then a confirmation that says whether Persona already saw it, then `DELETE …/photos/{n}`. A
  failed photo whose upload may have been stored (no refusal that comes after the hub's check for a stored repeat:
  `kb_read_only`, `not_meeting`, `transcript_expired`, `photos_full`) reads the list first: one it has is deleted there
  by its number, one it doesn't only leaves this phone, and with no list (offline) it stays and says so. `404
  photo_not_found`, `404 recording_not_found` and `410 recording_deleted` count as done; the JPEG on the phone goes too;
  a refusal says "Couldn't delete the photo. Retry." unless it has words of its own (`kb_read_only`, no connection).
- **Cited in the minutes**: a minutes item may cite a photo (`RecordingMinutes.Item.photo`, its `n`). The recording page's
  minutes (on iPhone the Summary's cards, #5026) show that photo's thumbnail under the item (`MinutesPhoto`, iPhone only;
  a tap opens the viewer), Photo removed once the list was read and no longer has it, the thumbnail's place while it
  isn't; Copy and Share add "(photo N)" to the item as the hub's Markdown does.
- Nothing of photos reaches the Live Activity. The camera's purpose string (`NSCameraUsageDescription`) names a meeting's
  whiteboard beside pairing and chat photos.
- **Screenshots and tests**: `-showMeetingSample room-whiteboard` is `room-taking-part` started with photos; once its
  upload's `200` carried `room` it draws a made-up whiteboard (2000×1500 px) and adds it as the camera does, so the real
  pipeline, upload and note run. The mock hub serves the photo routes (`POST /mock/meeting_photos {"on", "note",
  "note_seconds"}`; `MOCK_NO_MEETING_PHOTOS=1` is a manager without them), forwards each photo to a ready room and
  writes the room's note `note_seconds` later; the fresh start `POST /mock/recording_minutes` forgets every photo. UI
  tests in `MeetingConversationUITests`: the whiteboard's note (`testWhiteboardPhotoGetsTheRoomsNote`, which also checks
  what the hub stored), a delete leaving **Photo removed** (`testDeletePhotoShowsPhotoRemoved`), and the photo items
  only in the meeting started with photos, both ways round (`testPhotoMenuFollowsTheMeetingsStart`: `room-whiteboard`
  offers Choose Photos, `room-taking-part` doesn't). Unit tests: `MeetingPhotosTests` (the pipeline and recognition, the
  upload body, retries under the same id, a new id after `op_id_reused`, each refusal's words and whether Retry can help,
  the list and which refusals empty it, an answer lost and settled by the list (after a relaunch, during the backoff, a
  late failure or `200`), the delete of a failed photo, the JPEGs of photos that went, one photo prepared at a time, rows
  at once for a pick, the album per recording, `photos.json` over a relaunch, thumbnails), `MeetingConversationTests`
  (the photos revision kept only once its read applied; a start takes photos only when the hello listed
  `meeting_photos` with the conversation), `MeetingRecordingTests` (a minutes item citing a photo) and
  `MeetingThreadTests` (photo entries, the withdrawn line, the end batch).

## All-day recording (Apple)

The contract (the wire between watch, phone and hub, retention, the recap) is `docs/assistant/day-recap.md` (#4741); this is how the Apple apps carry it out. The rules are pure and unit tested on the phone's target (`apple/Shared/Recording/`: `RecordingDay.swift` for the contract's values and wire types, `RecordingDayOutbox` for what the watch sends and how, `AllDayAlertPolicy` for what the wrist, Notification Center and the face show, `AllDaySnapshot` for the complication). On the watch `WatchAllDay` carries them out, `RecordingCapture` records in day mode and `WatchDayTransfer` sends; on the phone `RecordingDayReceiver` hands chunks to `RecordingDayStore`, then `RecordingDayTranscriber`, `RecordingDaySync`, `RecordingDayKeeper` and `RecordingDayCatchUp` take them on (`apple/Persona/Recording/`).

### On the watch

- **The mode**: All-Day Recording on the home list (`WatchAllDayCard`), on the Record page, on its own page (`WatchAllDayView`: state, time since it last started, chunks waiting for the iPhone, Pause, Bookmark, Mark Meeting, Erase Last 10 Minutes, Stop for Today, Continue) and the complication. It shows only once the phone set `recordingDay`; while it is `0` and nothing records, the page says "Agree on your iPhone to start" in place of its controls, and a short version of the consent (`WatchAllDayExplanation`) is on the page whatever the state: the watch never asks for consent itself (`AllDayAlertPolicy.access`). The phone's own start request stands for its consent, which its application context may bring later. One microphone: it doesn't start while another recording runs ("Stop the other recording first.").
- **First start**: the first start ever asks once to allow notifications (with the screen off a pause reaches the wrist only as one). The first start of a local day makes the day recording before the microphone opens (`RecordingCapture.openDay`): a UUID, the IANA zone the watch is in, the date there and `recording_started_at`, saved with its seq and event counters. Starting again the same day (after Pause, Stop for Today or a halt) continues the same recording and counters. `RecordingCapture.allDayIntentKey` keeps the wish to record from a start until Stop for Today, so a launch tells a day the app lost from one the user ended.
- **Chunks**: 10 s while the phone is reachable, 60 s otherwise (`RecordingDayContract.liveChunkSeconds`, `backlogChunkSeconds`); the day transfer pushes reachability into the capture (`linkReachable`), which changes the writer's length on the fly. Each chunk carries its mean RMS level over all its samples (`RecordingDayLevel`) for the phone's silence skip. AVAudioFile's m4a writer leaves a `free` box of about 23 KB between `moov` and `mdat`, so 10 s of 32 kbps AAC came to about 65 KB, over the live envelope's 48 000 bytes: `RecordingSegmentWriter` drops it from each finished all-day chunk (`compactsFiles`, `RecordingM4A`, the chunk offsets moved back), which leaves about 42 KB. Every other recording keeps its files byte for byte.
- **Seqs and crash recovery**: the writer takes each chunk's seq from the recording's counter just before it opens the file (`RecordingLibrary.allocateSeq`), and the counter is on disk before the seq is returned, so no seq is handed out twice. At launch, before any seq is taken, `RecordingLibrary.recover` keeps the finished chunks, deletes the half-written one and reports every seq handed out without a chunk as `lost` (with its times when it had a file); a recording whose microphone was open also gets the `paused` (`stopped`) event the exit never wrote. The day stays open under the same id.
- **Midnight in the recording's zone**: moving to another zone never ends a day, and a 23- or 25-hour day ends where it does (`RecordingDayCalendar`). While recording, the writer cuts at the exact sample without closing the microphone: the old day gets `closed`, the next day's recording (named in advance, made with its first sample) a `started`. A day left open while nothing records is closed by a background refresh about 5 minutes after its midnight (`AllDayAlertPolicy.closeRefreshDate`; `WatchBackgroundRefresh` shares the app's one request with the Milk Frog's) or by whatever opens the app next; a pause through the night leaves no empty day.
- **Control events**: `started`; `paused` with its reason, `paused` (Pause, Stop for Today), `interrupted` (a call, Siri, another app, the microphone changing, a resume that failed) or `stopped` (battery, storage, a failed write, an audio services reset, a crash); `lost`; `erase`; `closed`; and the app's own `mark` ("Beyond the contract"). Each is saved with the entry in the same write and kept until acknowledged.
- **Erase** (Erase Last 10 Minutes, while recording or after Stop for Today while the day is open): the chunk being written is cut first (`cutForErase`), then every day recording the window touches (both, just after midnight) gets its `erase` event, naming the chunks it deletes before the phone acknowledged them, and only then are they deleted. An erase the phone asks for: the watch cuts the open chunk when the range reaches it, deletes what it holds in the range and answers with those seqs, kept 7 days (`dayErases`) so a repeated request gets the same answer.
- **Deleted on the phone**: the watch deletes every chunk and the entry (leaving a tombstone). When it was recording into that day it stops (no event: the recording is gone), says so (a stop haptic in front, "Recording Stopped: Today's all-day recording was deleted on your iPhone.", the page's line) and is off, not stopped: a start later that day opens a new recording.
- **Interruptions**: a call or Siri closes the open chunk and pauses. The watch plays a failure haptic only while the app is in front (watchOS ignores haptics otherwise), posts a local notification itself (`WatchAllDayNotices`: "Recording Paused" with **Continue**, or "Recording Stopped" for battery, storage, a failed write, an audio reset), writes the complication's snapshot and hands everything waiting to the system's transfers before it is suspended. One tap continues: **Continue** on the notification, the complication, the card or the page brings the app to the front, the only place the microphone can open (`AllDayNotice`, `WatchAppDelegate`). Coming to the front also resumes any recording a call paused, unless the call's audio still holds the session. In front, a notice goes to Notification Center without a banner; recording again withdraws it.
- **The dead-man's switch**: while recording, a "Recording Stopped" notification is scheduled 10 minutes ahead (`AllDayAlertPolicy.deadManSeconds`) and moved on by the saved chunks once less than 5 minutes is left (`deadManRenewBelow`), so if the app dies it fires 5 to 10 minutes later. At the next launch a day the app ended in while recording gets its `paused` (`stopped`) event and, unless the switch already spoke, the same notice (`alertsOnLaunch`).
- **watchOS / iOS 27**: `RecordingSessionObserver` observes `AVAudioSession`'s `didBecomeInactive` (a system interruption told apart from the app's own deactivation), `resumptionRecommendation` and `didBecomeActive` behind `#if compiler(>=6.4)` and `#available(… 27.0)`, and `AVAudioSession.interruptionNotification` everywhere else; only one of the two, so nothing is handled twice. Every recording uses it, on the phone and the watch.
- **Low Power Mode**: chunk messages wait until the oldest of them ended `RecordingDayOutbox.lowPowerHoldSeconds` ago and go together, so audio recorded in reach is at most `RecordingAudioSpec.lowPowerTransferBatchSeconds` (5 minutes) behind; a backlog drains as fast as the system delivers. The same constant now also spaces the batches of clips and rolling recordings (`WatchRecordingTransfer`: 3 minutes, 5 in Low Power Mode).
- **Meeting marks**: while all-day recording runs, Meeting Recording (the home entry, the Record page) becomes **Mark Meeting**: one microphone, so the meeting is a mark in the day, not a second recording. Bookmark adds a plain mark. Both are `mark` events and reach the hub as `bookmarks` (`label` `meeting` or empty), a hint for the recap; a second tap of the same kind within a second adds nothing.
- **The complication** (`AllDayRecordingComplication`: circular, corner, rectangular, inline and the Smart Stack) draws `AllDaySnapshot` (`allday.json` in the watch App Group), which the app writes on every change and every 10 minutes while recording (`heartbeatEvery`); a recording snapshot older than 25 minutes (`staleAfter`) shows "Check recording". A tap opens `persona://allday?action=start|continue|open`.

### Watch to phone

- **Live** (reachable): `WatchDayTransfer` carries out `RecordingDayOutbox`'s plan. A chunk goes by `sendMessageData` in the version 1 envelope (`RecordingDayHeader.frame`: the header's length in 4 bytes big-endian, its JSON, the audio file unchanged), the whole envelope under 48 000 bytes (`maxMessageBytes`), and the reply is its acknowledgement; at most 3 messages wait for a reply at once. Events go as messages.
- **Background**: a chunk over the live limit, or any chunk while the phone is out of reach, goes by `transferFile` with its header under `personaDayChunk` as soon as it is finished; events as `transferUserInfo`. Becoming reachable cancels the outstanding files of chunks that fit a message and sends them live, oldest first; the 60 s chunks stay files. Before the app may be suspended (a pause, a stop, a halt, leaving the front, a background wake while nothing records) everything unacknowledged, messages awaiting their reply included, is handed to the system's queued transfers (`handOff`), which outlive the app.
- **Acknowledgements and retries**: no acknowledgement 30 s after a live send, or an error, sends it again after a backoff of 2 s doubling to 5 minutes; a file delivered without an acknowledgement 60 s later goes again; three chunk messages failing in a row count the live path as down even while the session says reachable; a chunk whose message failed goes as a file once it is 5 minutes old; becoming reachable retries at once.
- **The age limit**: a chunk leaves the watch when acknowledged, erased, or still unacknowledged `recordingDayKeepDays` local days after its day ended (at that midnight in its zone; `RecordingDayWatchLedger.ageOut`), which is reported as `lost`, the watch's only age-based loss. A chunk whose file can't be read 3 times is lost too.
- **The gate**: the watch sends day chunks and its state only while the stored `recordingDay` is 1 or more; a phone from before never sees one. `WatchRecordingTransfer` keeps its WatchConnectivity slots for clips and rolling recordings and fans its events (activation, reachability, a saved chunk) out to `WatchDayTransfer`, which owns the day slots (acknowledgements, the phone's erase and delete requests).
- **Waking the watch**: what the phone sends a suspended or closed watch app goes as user info, and `.backgroundTask(.watchConnectivity)` lets the system wake the app to take it; `WatchAllDay.receiveConnectivity` waits (at most 20 s) until the session has nothing pending and the notices it led to are posted. It never opens the microphone: a start waits for a tap on its notice.

### On the phone

- **Consent** (`RecordingDayConsent`, `RecordingDayConsentView`): five points in the contract's order (everyone around is recorded, so ask them; the audio stays on the watch and iPhone and is deleted within 7 days; the text goes to the hub and its AI model; Agents and extensions that read Knowledge can read it; the text is kept `recording_days`, the recap as long as minutes), then **Agree** and **Not Now**. The agreement is kept with the sheet's version, and a new version asks again. `RecordingDayPhoneCapabilities` tells the watch `recordingDay` (1 once agreed, else 0), `recordingDayHub` (the last hello's `recording_day`, cached by `RecordingDayDefaults` for launches in the background) and `recordingDayKeepDays` (`min(7, recording_days)`, 7 until reconciliation said), when the receiver starts and whenever one changes.
- **Stored before acknowledged** (`RecordingDayIntake`, on WatchConnectivity's queue): a chunk's audio and header (`seg-NNN.m4a`, `seg-NNN.json`) or an event (`events.log`) is on disk before its acknowledgement goes; what can't be stored isn't acknowledged, so the watch sends it again. A duplicate is acknowledged and not stored twice; a chunk of a deleted day, inside an erased range or past the day's audio deadline is acknowledged and dropped (the last settled as `lost`). The day's whole record (`day.json`) is rebuilt from those files, without its transcript, if it is missing or unreadable; the folder layout is described in `RecordingDayStore`. Events apply strictly in `n` order: a later one waits for those before it, and one already applied is acknowledged again, not applied twice.
- **Transcription** (`RecordingDayTranscriber`): a chunk quieter than −50 dBFS (`RecordingDayContract.silenceDBFS`) is skipped without the engine (recorded silence is not a gap). The rest go in runs of contiguous chunks up to 60 s (`runSeconds`), so words at a chunk's edge aren't cut; each sentence is saved on the chunk it starts in, with its hub position in the same write. A missing seq is waited for 2 minutes while later chunks keep coming. A run that fails 3 times leaves its chunks `untranscribed`; a missing speech permission stops the lane without counting a try, and a language with no model (or its model not installed) stops only the days in that language, also without a try, while days in other languages go on (`RecordingSpeechError.isEntryLevel`, `isPerLanguage`). The lane runs only while the ordinary transcription queue is idle, so a recording the user waits for goes first.
- **In the background**: a WatchConnectivity delivery may wake the app; the day's lanes work only inside the background time the system grants (`beginBackgroundTask`) or the catch-up task, never download a model there, and stop when the time ends without losing anything. The catch-up (`RecordingDayCatchUp`) is a `BGProcessingTask` (`persona.recording.day-catchup`) that needs external power but no network (`requiresNetworkConnectivity` is false) and starts 15 minutes after it is asked for at the earliest: when the app goes to the background with a day of the last 8 days, and from arriving chunks at most every 15 minutes. It transcribes, uploads and applies retention, and asks again while work is left.
- **Upload** (`RecordingDaySync`): only to a hub that lists `recording_day`; until then the text stays on the phone. A create `PUT` with no segments, then appends: text placed since the last append waits until 20 s after it while the day records (`appendInterval`; the meta-only updates too), text already waiting then (a backlog going in batches) goes at once, and everything goes at once after the day ended; up to 500 sentences and about 900 KB a request, one request at a time; a sentence over 64 KB is split before it gets its position. A repair `PUT` (every positioned sentence, an empty one in each erased one's place) after an erase, a language change, or whenever the phone's log and the hub's disagree. On `413 day_full` the part closes and the day goes on in a next part (a new id, the same day and clock); `RecordingSync` keeps the parts' ids, so deleting the day deletes all of them. `coverage.to` counts only text the hub holds once the request is stored; the gaps come from the control events, the seqs the watch still holds or lost, the erases and the untranscribed chunks (`RecordingDayCoverage`, `RecordingDayGap`). An older manager's refusal stops a day until the next hello or launch (`unsupportedUntilHello`), each of which lets it try once more.
- **Retention** (`RecordingDayKeeper`): a day's audio goes at the earliest of 3 days after its first final recap (reconciliation's `recap_final_at`), 7 days after its day and `recording_days` after its day, whether or not its text reached the hub; the whole day (audio, text, upload state, its row) goes once it began more than `recording_days` ago, applied only once the hub said its `recording_days`. `RecordingSync` and `RecordingTranscription` leave day recordings to their own lanes, and `MeetingRecorder.tagWatchRecording` never sees one.
- **Today** (Knowledge › Recordings, between the capture controls and Recent Recordings; `RecordingDayTodaySection`): shown with a paired watch that has Persona, whatever the hub supports, or while a day recording of today (the date in its own zone) is on the phone; past days alone don't show it. The **All-Day Recording** switch mirrors the watch (on while it records or is paused, `RecordingDayWatchState.isOn`; off for a day the watch stopped by itself, whose line says "Stopped — tap Continue on your Apple Watch") and sends it a start or stop (`RecordingDaySwitch`); a command on its way holds the switch and says so for up to 2 minutes (a start opens the microphone at once while the watch app is in front, otherwise it waits for a tap on the watch's notice); then the switch follows the watch again and its line says the watch hasn't answered for 10 more minutes. Until the user agreed, **Agree to Start on Apple Watch** opens the consent sheet, and so does turning the switch on (the start goes once they agree). Today's day is a row here and is not repeated in Recent Recordings, where past days are rows (`RecordingDayRow`). A hub without `recording_day` gets a caution banner: the day's text stays on this iPhone.
- **The day's page** (`RecordingDayView`): the date, when it was recorded, how much is transcribed, its state word and its gaps by reason; the recap in the minutes' layout (summary with Copy and Share, whether it is final, the timeline under Morning / Afternoon / Evening with meetings marked, the suggested to-dos read-only, confirmed on the web), every time chip opening the transcript at its line, lit for 2 s; before a recap, what it waits for, "Writing the recap…" (looked at again every 30 s), or a failure with **Retry** (`POST …/minutes`). Then one section: a **Transcript** link to the day's transcript by the hour on its own page (`RecordingDayTranscriptView`), **Erase Last 10 Minutes** (today's open day only, asked first) and **Delete Recording**.
- **Erase and delete**: Erase removes the last 10 minutes from every day it touches on the phone, keeps the request for the watch until it confirms or 8 days pass (`RecordingDayWatchRequests.lifetime`, `maxKeepDays` + 1: by then the watch holds nothing of the day; `day-requests.json`, sent again at launch and when the watch shows up) and repairs the hub. Delete removes the day on the phone, the hub (every part) and the watch; when the watch records into it, the confirmation says All-Day Recording stops there. A day deleted on the hub (reconciliation's `deleted`, a `410`) is deleted on the phone and the watch too.
- The Mac compiles this code but never shows a day; visionOS receives days but works on them only in front (it grants no background time).

### Beyond the contract

Apple-only additions, marked `[ext]` in `apple/Shared/Recording/RecordingDay.swift`; a phone or watch that doesn't know them ignores them.

| Addition | Way | What for |
| --- | --- | --- |
| `mark` event (`at`, `label` empty or `meeting`) | watch → phone, `personaDayEvent` | Bookmark and Mark Meeting; uploaded as `bookmarks` |
| `personaDayCommand` (`command` `start` or `stop`, `sent_at`) | phone → watch, message or user info | The Today switch. The watch acts only on a command newer than the last one it acted on and at most 10 minutes old (`WatchDayCommandOrder`); a start while the app isn't in front becomes a notice with **Start** |
| `recordingDayWatch` application context (`RecordingDayWatchState`: `recording`, `day`, `state`, `since`, `next`, `pending`) | watch → phone | The Today switch and row; a new state goes at once, progress at most once a minute |
| coverage gap reason `untranscribed` | phone → hub | Chunks the phone could not transcribe in 3 tries; the hub keeps a reason it doesn't know |

### What the simulator cannot verify

Paired simulators don't deliver `transferFile` and never run background tasks, and a simulator can't stand in for a real microphone or a day on the wrist, so the stream is checked in process (below) and these need a real watch and iPhone:

- Wrist down and screen off for hours (the `audio` background mode); battery, heat and storage over a whole day.
- The phone out of reach and back: the 60 s backlog as files, the small chunks taken back and sent live, the age limit.
- A call or Siri: the notice reaching the wrist with the screen off, Continue taking one tap, the complication showing Paused.
- A force quit or crash while recording: the dead-man's notice within 5 to 10 minutes, and the next launch's report.
- Midnight while recording and while paused (when the background refresh really comes).
- Low Power Mode batching; the iPhone woken by WatchConnectivity and transcribing within its grants; the catch-up while charging.
- The switch starting the watch from its notice; a delete on the phone stopping the watch.
- The watchOS 27 / iOS 27 session messages, which need a 27 system and an Xcode with Swift 6.4.
- On-device speech models for the day's language, and envelope sizes from the watch's own encoder.

### Screenshots and tests

- DEBUG launch arguments, phone: `-showDaySample today|paused|recap|pending|failed|consent` (yesterday's day in Recent Recordings and today's in Today, transcribed and stored as the watch would send them, so the phone uploads them as real days; `paused`: waiting for Continue after a call; `consent`: a user who hasn't agreed), `-openDayRecap YES` (with `-showDaySample`: the past day's page), `-openDayCatchUp YES` (runs the catch-up now). Watch: `-showAllDaySample recording|paused|interrupted|stopped|off|consent|deleted` (no microphone, 3 h 25 min in, 4 chunks waiting), `-openWatchItem allday` (its page), `-openWatchItem allday-complication` (every family in every state), `-recording.phoneDayHub NO` (the "needs a hub update" line), `-showLargeText YES` (an accessibility text size).
- Unit tests: `RecordingDay*Tests` (the contract's values, store, transcriber, upload, coverage, retention, status, recap, writer, outbox) and `AllDayAlertPolicyTests`. `RecordingDayStreamTests` runs the watch's real writer, the outbox's plan, the live envelope and the files, and the phone's real intake, store, transcription lane and upload, over an in-memory WatchConnectivity link to a fake hub, with a fake engine answering after 5 ms and one wall clock the test moves: every chunk stored once and in order, events applied in order whichever way they came, erases reaching the phone. Its latency is the in-process work alone (no radio, no system scheduling, no speech model): per chunk, from finished to stored and acknowledged, to transcribed and to on the hub, printed by the test as `LATENCY` lines (p50, p95, max, n). It fails above its bounds: for live chunks a p95 of 0.5, 1 and 2 s; for a backlog a maximum of 5, 6 and 8 s from each chunk's end, and 2, 3 and 5 s from the phone being back in reach.
- UI: `RecordingDayUITests` against the mock hub (`docs/operations/testing.md`, "All-day recording").

## Entry points

- Phone: Knowledge › "Recordings" (the last type capsule; screenshot arguments `-tab assistant -openAssistantPage knowledge -showKnowledgeKind recordings`, with `-showRecordingSample YES` to add a fictional recording (`second` adds the same one under another id, for a test that deletes one) and `-openRecording YES` to open it).
  On the phone, this category lists local recordings (including those being recorded or transcribed), not hub copies; Mac has no recording feature and lists hub `recordings`. Existing interface styles and layout are preserved.
- Apple Watch: "Record" on the home screen; recording can start even when unpaired or the hub is offline. Start, pause / resume, bookmark, stop; watchOS 11 and later support Double Tap to start or add a bookmark.
- Phone recording section: choose Chinese / English / Japanese (and Meeting or Note), then tap Record; recording continues after leaving the page or locking the screen, and is stopped on return. Each list row shows source, duration, and state: Recording, Waiting for watch transfer, Transcribing, Waiting to sync, Syncing, Synced to knowledge base (Partially synced), or Not accepted by knowledge base; when the hub is unreachable, the reason appears below the list.
- Details: bookmarks and timestamped transcript; resume transcription, change the language and retranscribe (resync replaces the old version), or delete (immediate local deletion after confirmation, synchronized deletion from the knowledge base, queued offline). A row in the list also deletes with a left swipe, with the same confirmation and the same path; a recording still being made on the watch has no Delete. Original audio is not uploaded.
- Knowledge base: iOS, web, and Android knowledge pages all have a "Recordings" category. Lists show name, time, duration, and source device; details show summary, bookmarks, and full timestamped text.
- The watch cleans up transferred audio only after receiving the phone's application-level persistence acknowledgment; segments whose transfer succeeded but have not been acknowledged remain. The end message transfers independently to avoid displaying Recording forever when stopped at a segment boundary.
- iOS uses `SpeechTranscriber`, trying `DictationTranscriber` if the language is unsupported; system models download on demand. If no model is available, show the reason and retain the recording for retry, without using the cloud.
- Dictation and long recordings share one microphone ownership lock, so neither can reconfigure or close the other's audio session. Deletion retains a UUID tombstone so late watch segments cannot recreate the recording.

## Verified (from #3235 onward)

- iOS / watch simulator and Mac (`CODE_SIGNING_ALLOWED=NO`) builds; `RecordingSyncTests`: upload only once until changed, queued offline retries, eventual deletion after deleting offline, local deletion following knowledge-base deletion, reconciliation restoring recordings lost by the hub, expiry removing audio too, and no upload without text.
- End-to-end UI (`RecordingKnowledgeUITests`, `TEST_RUNNER_PERSONA_MOCK_HUB`, read back through mock hub `/api/v1/recordings` and `/mock/recordings`): a fictional recording appears in Knowledge › Recordings and automatically syncs to the hub (only text and timestamps); deleting from details also deletes it from the hub, with no later upload; no recording entry in the + menu of the new-chat composer.
  `TEST_RUNNER_PERSONA_SHOTS=<directory>` and `TEST_RUNNER_PERSONA_SHOTS_APPEARANCE=light|dark` save three screenshots: list, details, and + menu.

## Verified (composer-entry era, before #3235)

- Phone and watch simulator builds; Mac shared composer build.
- Unit tests for recording format, out-of-order and duplicate segments, abnormal-exit recovery, transcription checkpoints, timestamps, and XML context.
- End-to-end UI: selecting "Use as context" sends no message to the mock hub; tapping Send delivers timestamped text without audio.
- Phone transcript preview screenshots in dark / light mode; watch entry screenshot. A watch Picker compressed into a narrow strip was replaced with single-line language-switch buttons.

## Verification limits

Paired simulator `transferFile` does not deliver, and simulator microphones and speech models cannot replace real-device acceptance. Unit tests cover out-of-order delivery, duplicates, stopping at segment boundaries, recovery, and transcription checkpoints; phone and watch pages were screenshotted on dedicated simulators. Final real-device checks are required for continued recording while locked, incoming calls, transfer of watch offline backlogs, long recordings, model downloads, and permission denial.

All-day recording has its own device checklist: "What the simulator cannot verify" under "All-day recording (Apple)".
