# Approval clarification

An assistant-owned permission card can carry **Have a question** in addition to Allow and Deny. A question asks about the exact proposal on that card; it is not an approval decision and never executes, declines, closes, or cancels the operation.

## Owner flow

- Web shows a question editor when the interaction has `can_clarify`. `POST /api/v1/interactions/{id}/clarify` takes only `{text, client_msg_id}`. The trimmed question is at most 1,000 Unicode code points. An unchanged retry reuses its `client_msg_id`; a deliberate new question uses a new one.
- Feishu shows the same action only on a public card. Every press must be a fresh, one-time owner action bound to that channel, card, and permission kind. The answer run returns to the card's explicit chat and message target as well as the originating assistant conversation.
- The owner's original text is delivered to the interaction's exact machine and session. An archived conversation is explicitly reopened. A deleted conversation or ended room returns a visible error; delivery never falls back to another conversation.
- A successful question returns the original tool call with a non-final Clarifying outcome. Its result says that the same card remains valid and must not be recreated. The card stays pending for Allow or Deny, and further questions are allowed.
- Private interactions remain in their private Web conversation: they are not drawn or answered in Feishu. Field cards may be questioned, but field values are never put in the question and channel Allow remains unavailable. Only the owner can clarify; an assistant, supervisor, answer-only principal, remembered grant, or standing authorization cannot execute the prepared operation.

## Prepared operation and compatibility

`approval.Operation` is the process-local continuation behind one immutable proposal. It contains Apply, optional Discard, replacement identity, and the existing Denied/Unanswered tool results. Public interactions gain only the additive `can_clarify` boolean; older clients ignore it and keep their existing Allow/Deny behavior. Clarification is an action, not a new `proto.Resolution`.

For a hub-owned setting, `fleet.ask_prepared` retains the executable handle in the hub and returns `approval.Outcome` to the manager. Only the opaque interaction ID crosses the trusted bus. `fleet.approval_apply` consumes that manager-owned handle once after a fresh owner Allow; `fleet.approval_discard` releases it without applying. A structured stale result survives the bus boundary. Existing `fleet.ask` callers retain one-shot approval behavior.

Immediately before Apply, the operation re-reads current state and verifies that it still produces the visible before/after proposal. A revision-checked operation keeps its original revision baseline rather than rebasing it. A stale proposal applies nothing and ends as stale/cancelled with a request for a fresh card. An equivalent newer proposal replaces and closes an older Clarifying card so both cannot execute. Subscription deletion uses an atomic revision predicate.

## Timing and terminal delivery

Before the first question, the card keeps its original decision deadline. While an accepted question is being answered, that remaining decision time is paused. The pause has a cumulative 24-hour budget per card; repeated questions do not reset it. The turn that produced the card does not count as its answer turn, and a question accepted during an answer turn waits for the next turn before timing resumes.

The coordinator makes one final claim under the fleet mutex. An in-flight question and a final Allow, Deny, expiry, or replacement are explicitly arbitrated. Allow executes at most once, Deny executes nothing, and a proxy-attributed Allow executes nothing.

Once a card has clarified, every terminal path sends exactly one result containing the literal interaction ID to the original conversation, including Allow, Deny, expiry, replacement, stale cancellation, and restart cancellation. Delivery uses the stable ID `approval-result-<interaction-id>` and the durable relay queue. The journal stores only the ID, original target, delivery state, status, and result text—never field values, callbacks, or executable closures.

An unfinished Clarifying card fails closed on restart without executing. If the process stops after recording `applying`, recovery reports the execution outcome as uncertain rather than claiming success or reapplying it.
