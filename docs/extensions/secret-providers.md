# Secret providers

An extension can supply the values of secret **references** such as `op://vault/item/field`, so a secret Persona needs comes from the user's password manager when it is used instead of being pasted into Settings. The first provider is the 1Password extension in the public extensions repository. Design and decisions: #3962.

This page describes what is built so far: the extension point, the manager's calls to the provider, the record of each use, and references in an extension's secrets. Launcher and model provider keys and the phone-approved hand-off of a value to one Agent run follow in their own steps of #3962.

## The contribution

```json
"contributes": {"secret_providers": [{"id": "vault", "name": "1Password", "schemes": ["op"]}]}
```

- `name` (1–80 characters) is what the settings show: **From 1Password: op://…**.
- `schemes`: 1–4 schemes, written without `://`, matching `^[a-z][a-z0-9+.-]{0,15}$`. `http`, `https`, `file`, `data`, `persona`, `about` and `javascript` are refused: addresses and the app's own links are never references.
- A reference names its provider by its scheme alone, so two installed extensions cannot claim one scheme: the second fails its preview and install (`Registry.conflicts`).
- The program must speak the protocol. The manager asks it only while it is enabled, without a problem, and with the contribution switched on (`Registry.SecretProviderFor`).
- The install page says what it agrees to: "Supply secret values: when one of your settings holds an op:// reference, or you approve handing one to an Agent on the phone, Persona asks it for the value. It learns which reference is used and what for, never your other secrets." The claimed schemes are part of the permission digest (empty for every other extension, whose digests do not change), so an upgrade that adds a scheme is agreed to again and lists it under what grew.

## Methods

Namespace `secrets.`, types in `schema/extension/protocol.schema.json`. Only the manager calls them; no other extension, device or token can.

| Method | Parameters → answer | Notes |
| --- | --- | --- |
| `secrets.describe` | `{contribution, reference}` → `{title, detail?}` | Words for people (the item and field, its vault), never a value. Kept by the manager for 10 minutes per reference, so settings pages and cards do not ask each time. |
| `secrets.resolve` | `{contribution, reference, purpose}` → `{value}` | `purpose` is `{kind, id, machine?, session?}`: `extension` (an extension's secret; `id` its id), `launcher` (a launcher's or model provider's key; `id` its name, `machine` where it goes) or `agent_run` (handed to one Agent run after the user approved it; `id` the variable, `machine` and `session` the Agent's). The value is 1 byte to 16 KB. |
| `secrets.used` (event) | `{contribution, reference, purpose, at, approved_by}` | Sent after each successful resolution, so the provider can keep the usage record its own page shows. `approved_by`: `settings` (a reference saved in Settings), `rule` (an Always rule) or the approving device's id. Never a value. |

Errors a provider answers with a code: `not_found`, `not_signed_in`, `rate_limited` (with `data.retry_after` in seconds) and `refused` (its words are shown). Anything else is a failure with its words. An older provider that does not know the method (`unknown_method`, `no_contribution`) is a failure that says so.

## The manager's side

Code: `server/internal/extmgr/secret_providers.go` (`Manager.ResolveSecret`, `Manager.DescribeSecret`, `Manager.SecretProviderOf`), `server/internal/extension/secret_providers.go` (`ReferenceScheme`, `Registry.SecretProviderFor`).

- **Bounds**: 10 seconds per call; at most 60 resolutions per provider a minute, the rest refused as `rate_limited` before they reach it; a value over 16 KB is refused.
- **Errors** are `*SecretError` with a code for callers (`not_reference`, `no_provider`, `provider_off`, `not_found`, `not_signed_in`, `rate_limited`, `refused`, `failed`, `too_long`) and words that read in the asker's language (`hub-extension_secrets.*`). The provider's own words are put on one line and clipped to 200 characters.
- **Never a value** in a log line, an error, an audit detail or an event. The log says which provider resolved for which purpose, never the reference's value or the reference's target words.
- **The record of each use**: every resolution that reaches a provider, done or not, is a `plugin_audit` row on the provider: `secret_resolved` or `secret_resolve_failed`, the actor who approved it (`settings`, `rule` or a device), and the detail `{reference, purpose, code?}`. It reads with `GET /api/v1/extensions/-/audit?plugin=<provider>`. A reference whose scheme no provider claims is not recorded (there is no provider to record it on).

## References in an extension's secrets

Code: `server/internal/extension/secret_refs.go`, `server/internal/extmgr/secret_refs.go`. Feature `extension_secret_references` in hello.

- **Saving**: `PUT /api/v1/extensions/{id}/-/secrets/{name}` with `{"reference": "op://…"}` instead of `{"value": …}` (both at once: 400). Never guessed from a value's prefix: a value that starts with `op://` is a value. Refused (400 `invalid_secret`): text that is not a reference, a scheme no enabled extension supplies, and the extension's own scheme (a provider cannot supply its own secrets).
- **Stored** in the same sealed `plugin_secrets` row a value would be, behind an internal marker no typed value can carry (a NUL), so no table changes and a secret's history stays one row. The audit's `secret_set` says `reference` (the reference only, as always never a value).
- **Approval**: saving it is the owner's standing approval to hand this extension that secret's value whenever it starts, with no card per use. It ends when the reference is deleted or replaced, or the provider is switched off or removed.
- **Resolved** each time the manager hands the extension its secrets: just before `initialize` (or into `PERSONA_EXTENSION_SECRETS` for a program without the protocol), for its programs on computers (the names `node.secrets` sends), and for a settings check, which checks the value the reference stands for. The extension gets the value as if it had been typed in; nothing changes for it. A reference that does not resolve fails the whole hand-over: the program does not start with a secret missing or a reference in its place, and its error says why in the provider's words.
- **Its provider off**: the extension is `needs_setup` ("Secret “Key”: no extension that is on supplies op:// references"). Switching a provider on, off, upgrading or removing it applies again every extension that holds one of its references (not a dependency: the provider can still be switched off without a cascade).
- **Details**: the secret answers `set: true`, no `hint`, and `reference: {provider, provider_name, reference, title}`, where `title` is what `secrets.describe` says the reference names (waited for at most 2 seconds; the reference itself otherwise). The apps show **From 1Password: op://…** with the title under it. A provider's details answer `secret_users: [{extension, extension_name, secret, secret_title, reference}]`: every installed extension's secret that holds one of its references, so the user sees what each standing approval covers.

## What comes next (#3962)

1. Launcher and model provider keys: resolved when a computer pulls the key, kept in the manager's memory for 10 minutes and dropped on a settings change, the provider switching off or a failed resolution.
2. The hand-off to one Agent run: a reserved phone card naming the item, field, Agent and computer; values staged on the node over the manager's own connection (`secret.stage`), a one-time ticket in the start, a wrapper that puts them in the Agent's environment, the session's synced output scrubbed. Never cached.
3. The web and iPhone clients; Android in #3986, which shows the hand-off card with "Approve on iPhone or the web" until then.
