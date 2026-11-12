# Apple send feedback

The main conversation and Agent sessions use the same local send lifecycle on iPhone, iPad and the existing Mac client.
The UI does not modify the runtime's session state, session ordering or notifications.

- **Sending** starts synchronously when the client accepts the send or retry.
- **Processing** starts when the send API succeeds. Runtime startup and first output are separate, later milestones.
- Transcript echo does not remove the attempt record. A response that arrives after echo still updates acceptance.
- A reply, a fresh failure or terminal runtime update ends the fallback. A late acknowledgement cannot revive a finished attempt.
- Waiting for the network or a refused send ends optimistic activity; the existing outbox retains the message and its retry safety.
- Reopening the app restores Processing from a recent sent outbox entry, without another request. Its saved creation time
  is a conservative deadline bound when the exact acceptance time was not persisted.
- Accepted-but-not-yet-running feedback retains the existing 60-second fallback bound, measured from acceptance rather than the tap.
  Sending itself has no presentation timeout; the request/outbox determines its result.
- Queued messages keep their existing queue strip as their one progress home. Inspecting a subagent does not show the parent's send state.

No new setting, style, token, timer or polling loop is introduced. The existing typing indicator, work summary, deadline task,
failed-message bubble and translations are reused. Only the feedback wording and lifecycle change.

## Regression fixture

`apple/PersonaUITests/Fixtures/send_feedback_hub.py` reuses the repository's synthetic mock data. It accepts ordinary sends
in 150 ms but withholds transcript echo for ten seconds; `Slow` sends take two seconds and `Fail` sends return a refusal.
`GET /mock/send-feedback` reports receipt and response timestamps without real accounts or messages.

Run the actual optimized simulator app with `SendFeedbackUITests`. The suite records the send command's duration,
asserts that the status exists with a laid-out frame, and keeps screenshots as result-bundle attachments.
`check_timing.py` enforces the one-second budget from XCTest's **Synthesize event** timestamp to the successful
accessibility observation. The raw test duration also includes button lookup and idle waits before input; it is not
physical tap latency. The checker preserves that raw duration, fails closed if timestamps are missing, and adds a
20 ms conservative margin for rounded step logs. `test_check_timing.py` covers fast input with slow automation,
actual slow presentation, missing timestamps and failed observations.
The app's DEBUG
`SEND_FEEDBACK_DRAW` trace records send-attempt-to-status-view appearance separately from UI automation overhead.
Use the same simulator and build settings for baseline and fixed runs, then inspect light and dark screenshots.
For host-side captures, set `FEEDBACK_SIM` and `FEEDBACK_SHOTS` on the fixture and `TEST_RUNNER_PERSONA_SHOTS` on the UI run.
The test requests `xcrun simctl io <assigned simulator> screenshot` while Processing is visible. Capturing only after
XCTest ends captures Springboard instead; those images are not UI evidence.

`ChatSendFeedbackTests` covers acknowledgement-before-output, echo-before-acknowledgement, late acknowledgements,
request correlation, failure/offline recovery, stale snapshots and the acceptance deadline.

The client currently treats successful send responses as acceptance. A future server event can update the same
attempt by message ID; it must not replace reliable outbox delivery with speculative success.
