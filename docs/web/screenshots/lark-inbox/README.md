# Lark Inbox text and card presentation

The `open-source-*` captures show the real Inbox detail component with synthetic
transport and messages after adding the independent source-opening action. The
button remains beside AI draft reply. They are component fixture evidence;
native-client positioning is a separate manual check after local deployment.

Synthetic examples for #856 L6. No screenshot contains a real account, message,
repository, routing ID, host or private URL. The page is the actual web Inbox;
its API and WebSocket transport were mocked with `Example Assistant`,
`example/service` and links under `https://example.com`.

The captures cover desktop light/dark, 390px light/dark and 320px dark, plus the largest web text size on desktop and 390px in both themes. A merged
change puts its linked subject and description first; the long second description
can expand and collapse. Author, repository and original source text remain in
Details. The friendly reply destination explicitly says when its message preview
is unavailable; its synthetic routing ID is only in disclosure.

These are UI fixture evidence, not real-tenant or production acceptance. Local
verification recorded no page errors, horizontal overflow, write requests or
external network requests. Private real-source acceptance artifacts are not part
of this directory.
