# Native Lark image and file sending (#410)

> **Since 2026-11-04 (#1520, plugin step 3c)**: the entire Lark-gateway integration (app `/api/v1/lark/…`, Inbox and knowledge sources, Moments, assistant `ol` gate and credentials, /agent, personal Agent) is no longer in the open-source repository. It is provided by the Lark plugin (`docs/extensions/extensions.md`, `docs/plans/plan-extensions.md`, “Step 3”).
> The plugin preserves the behavior below; referenced hub source files (`hub/lark*.go`, the `internal/lark` client…) have been removed. Open-source retains official lark-cli, Lark bots, and Lark mail.

The existing `LarkChatView` composer's “＋” selects photos or files, with preview and removal before sending. Previews reuse the image viewer with pull-down dismissal and system Quick Look; messages retain native Lark formatting.

## Sending and confirmation

- Text, images, and files submit in order through one session send queue. Waiting for an echo does not block the next submission.
- Each message has a stable `client_id`. File selection saves a private snapshot; retries do not reread originals that may change or lose access. Startup cleans selection copies left by the previous process; durable media uses a separate directory excluded from cleanup.
- Successful requests save `message_id`; echoes replace local messages by `client_id` / `message_id`. Do not remove attachments based on empty text, filename, or timeout.
- Failures retain attachments for retry, restoration to composer, or deletion; leaving and reopening chat restores them. Queues isolate hub group, actual Lark account, and session.
- Quoted text still uses the main reply API; topic replies retain existing behavior. Text and attachments send separately.
- Images and files preserve quotes too (#1035): each attachment quotes the same message rather than silently sending normally. Requires hub extension capability `lark_attachment_reply`. Without it, older extensions would ignore quotes and send, so block and explain “Update the Lark Extension on Your Hub to Send Images and Files Quoting Messages”; text, attachments, quote remain in composer. Outbox stores quote, topic flag, and `threadRoot`, preserving routing on restart / retry. Topic sheets opened from a chat also select images / files (#1035 part 2), using **that chat's same durable outbox** with `thread` + `threadRoot`. Recovery, account isolation / verification remain. Chat pending rows omit topic replies; topic page displays only its own. Topic refresh reconciles against topic replies; before retry reread latest topic replies to avoid duplicates. Unknown-result files offer verification only, never reupload. Topics opened without chat outbox, or unsupported hub extension, have no attachment controls. Topic text retains original send path.

## Hub APIs

Dedicated `images` / `files` bind the configured gateway account, reuse device authentication / roles, never expose a generic write proxy. Images validate encoded size and actual MIME; files stream with Content-Length limits. Preserve gateway login-expiration codes. Old hubs require upgrade before attachment sending.
Quotes / topics (`lark_attachment_reply`): images JSON adds `reply_to` and `thread`; files query adds `reply_to` / `thread_root`. Upstream maps to images + `replyTo`, `image-uploads` + `messages/{id}/replies` with `replyMode: thread`, files + `replyTo` / threadRootId. Files bypass gateway outbox and lack idempotent retry; unknown outcomes require history checks, no automatic reupload.

## Limits and regression coverage

The Feishu composer on iOS and Web holds up to four attachments per batch. Each attachment makes a separate send through the Lark gateway. The clients accept files from one byte through 100 MiB and JPEG photos up to 7.5 MiB after conversion, with the longest side at most 2560 px and location metadata removed. Originals and animated images can be sent as files.

The Web client uses the hub's dedicated Lark image and file routes. iOS also supports an attachment route on the active extension messaging source. Before the integration moved into the plugin, Persona streamed raw bytes to the configured gateway's `/conversations/{feed}/files` endpoint with a 100 MiB limit. The public client ceiling describes which files Persona will submit. The installed plugin's contract determines its upstream API and successful upload sizes.

Feishu Open Platform documents separate limits for its IM APIs.

- Image and file messages each reference a single resource key. The image upload endpoint allows nonempty images up to 10 MB, with a 12000 × 12000 px resolution limit and a 2000 × 2000 px GIF limit. The file upload endpoint allows nonempty files up to 30 MB. Each upload endpoint documents 1000 requests per minute and 50 per second. [Message content](https://open.feishu.cn/document/server-docs/im-v1/message-content-description/create_json) · [Image upload](https://open.feishu.cn/document/server-docs/im-v1/image/create) · [File upload](https://open.feishu.cn/document/server-docs/im-v1/file/create)
- The send-message endpoint documents 1000 requests per minute and 50 per second. Sends to one user or group also have a 5-per-second limit, shared by bots in the group. Group throttling can return `230020` with HTTP 400. The platform's general HTTP 429 response supplies `x-ogw-ratelimit-reset` in seconds. [Send message](https://open.feishu.cn/document/server-docs/im-v1/message/create) · [Rate limits](https://open.feishu.cn/document/ukTMukTMukTM/uUzN04SN3QjL1cDN)

Keep four selections until the gateway exposes a verified pacing and rejection contract. A batch of four limits composer size. Sequential submission still permits bursts across consecutive batches or devices. A change to nine needs pacing across sends to the same chat and bounded retry for definitive throttling responses. An ambiguous file result still goes through the receipt check described below. The gateway's upload contract governs any change to the 100 MiB file selection ceiling.

Normal-chat text, quotes, attachment drafts, outbox persist together (#688). Files in Application Support, excluded from backup, system file protection. Records isolated by digest of hub group / actual Lark user ID / chat; no credentials or original paths. Same-group hub address / device-token changes reuse draft; account switches use separate drafts. After initial online account verification, local drafts open offline; before sending reverify actual account using existing read-only accounts API. Failure retains content and sends no write. Legacy text / current-and-backup-address outboxes lack account identity, requiring user attribution confirmation; never auto-import across accounts. Topic replies retain original implementation.

Before send, atomically save “clear draft + add outbox”; only then clear input and request network. Persistence failure retains draft. Persist attempt marker before network. Restart marks only attempted submissions Unconfirmed Send Result; unsent queued items allow explicit retry, none automatically resend. Reconcile receipts by client_id / message_id; older messages outside latest page checked read-only by original ID, not retained forever at bottom.

Gateway file uploads do not use text / single-image outbox retry. Definitive rejection or confirmed failure to connect allows retry. A transport timeout, disconnect during upload, HTTP 5xx, or damaged receipt leads to Check Send Result or Remove Local Record. Web also records an HTTP 408 as unconfirmed, while iOS treats a returned 408 as a failure eligible for manual retry. Align that case before adding automatic file retries. Successful file receipt may omit message_id: retain Sent, no retry; Remove Local Record removes only phone record, not sent Lark message, and never infer echo from same filename. Client_id alone does not prove idempotent file retry. Upstream read-only send-status work is tracked in gateway issue referenced by extension instructions.

Restart recovery loads / reconciles only on opening that chat, no background bulk resend. Corrupt / newer-version state stops sending. Restore Input → confirm Clear Draft and Start Over deletes chat's local records / media and legacy recovery backups. Storage failures offer retry, not false version-incompatibility errors. Temporary selected copies release with references; durable copies cleaned after atomic new-state write.

`LarkAttachmentTests` covers snapshot isolation, size bounds, original request bytes, retry IDs, exact echoes, quote routing / account isolation, attachment quote / topic fields, capability gates, restart-preserved quote / root. `LarkAttachmentFlowTests.testQuotedPhotoFollowsWhatTheHubCanCarry` runs current / old mock (`MOCK_LARK_OLD_ATTACHMENTS=1`): preserves quote or warns / retains draft.
`LarkAttachmentFlowTests` / `LarkSendTests` use local mock hub for previews, pull-down dismissal, failure / leave / reopen, retry, text / quotes / topic replies.
Screenshots: Attachment regressions, Persistence regressions. Real accounts inspected read-only, no test messages sent.

Read-only gateway verification 2026-11-03: `GET /agent/v1/messages/{id}` for two synced messages, one own / one other, returned `item.id/feedId/isMine/localState` matching identity / chat / ID. Hub forwards fields; mock adds exact message route. No bodies / IDs / credentials recorded, no messages / read receipts sent.

Upload total budget estimated at 128 KiB/s plus one minute processing, minimum three minutes, ~14 minutes for 100 MiB; phone and hub use same budget. User cancellation propagates immediately. Gateway pending / failed local copies do not display as sent.

Text edits coalesce into disk transaction once per second; attachment changes, page exit, background save immediately. Send / restore cancel old delayed saves. Composer locks only during local commit, not network send / reconciliation / latest fetch. Disconnect hub / remove group invalidates queue / cancels tasks first, then deletes group drafts / attachments / account bindings; late HTTP / disk callbacks cannot resurrect cleaned data.

Read-only `/agent/v1/accounts` verification 2026-11-03: `activeId` / account id are strings, selected account unique, boolean `signedIn` / nonempty string userId. Match hub-request-selected account, not first primary. No real IDs / accounts / credentials recorded.
