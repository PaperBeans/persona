package assistantwire

// What the core knows of an agent's question, permission, plan or terminal dialog that its card in the Feishu chat
// shows or goes by (the lark module's asks.go and ask_cards.go, #13404). The module asks for each as one value and
// lays the card out from it; it reads nothing else of the core for a card.

// AskView is what an interaction's card says besides the interaction itself: who asks, the task, the conversation's
// page, and what the core's own wording of the interaction is.
type AskView struct {
	// Agent is the session as the card names it, Machine its computer ("" when the card names none) and Secondary
	// the session's other name when the agent goes by its herdr tab name.
	Agent, Machine, Secondary string
	// Task is the task the agent works on as the cards name it, its direction first ("" when it works on none).
	// TaskSame: the agent is named after its task, so Task is the direction alone and goes before the agent.
	Task     string
	TaskSame bool
	// OpenURL is the conversation's page in the web app the hub serves and OpenLabel what a link to it says; both ""
	// when the hub has no http(s) address for it. OpenInline: the address can be a Markdown link.
	OpenLabel, OpenURL string
	OpenInline         bool
	// Tool is the tool a permission asks about, as a sentence names it.
	Tool string
	// Terminal: a terminal dialog that is an ordinary choice; TerminalText is then its question and choices as text.
	Terminal     bool
	TerminalText string
	// ExactProposal: the card is an owner-action proposal, shown whole.
	ExactProposal bool
}

// AskRoute is where an interaction's card goes, as the channels' runs say now.
type AskRoute struct {
	// Driven: a run of the chat is on the interaction's conversation (or, for an approval of the hub's own that
	// names none, a Persona run is on its machine).
	Driven bool
	// Main: the interaction's conversation is the main conversation.
	Main bool
	// Private is the latest run of the conversation asked in the owner's private chat, Topic the latest asked in a
	// topic (nil: none; no topic while a "/read" of the conversation goes on).
	Private, Topic *AskRun
}

// AskRun is a run an interaction's card answers to: its message, and where it was asked.
type AskRun struct {
	ReplyTo string
	To      ChannelTarget
}
