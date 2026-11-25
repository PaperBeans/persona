// Package chatsearch is what the assistant's chat search index keeps of a message and how a search matches it: the
// owner's words and the assistant's replies as the chat shows them, never a hidden tag, a quoted context, a thought or
// a message Persona wrote for the model (docs/design/proposals/assistant-search.md "One rule for what is
// searchable"). It predates the domain rules (docs/server/architecture.md "Domain model"): its imports outside them
// are frozen in server/internal/archguard/testdata/domain_layers/legacy_edges.txt, and it is not an example to copy.
//
//persona:layer core
package chatsearch
