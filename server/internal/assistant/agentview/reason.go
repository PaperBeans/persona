package agentview

// Reason is why a session is in its phase. Each phase has its own reasons. The zero Reason is nil; a View never holds
// it.
type Reason interface {
	Accept(v ReasonVisitor)
	reason() // sealed: only this package makes one
}

// ReasonVisitor is told why the session is in its phase.
type ReasonVisitor interface {
	VisitCards(cards CardCount, holding Holding) // needs you: cards stand for it
	VisitTerminalDialog()                        // needs you: a dialog its terminal shows
	VisitTurn()                                  // working: its turn runs
	VisitBackground(background Background)       // working: only its background work runs, or only waits
	VisitUnseen()                                // done: the owner has not seen the result
	VisitJustFinished()                          // done: seen, and it finished a short while ago
	VisitAsks(seeing Seeing)                     // done: it asks the owner, who has seen the reply or not
	VisitUsageLimit(failure Failure)             // error: a usage limit (FailureUsageLimit or FailureUsageLimitResuming)
	VisitModel()                                 // error: a model API error
	VisitStartRefused()                          // error: its start was refused
	VisitRestartInterrupted(resumability Resumability)
	VisitSeen()        // idle: seen
	VisitNotRunning()  // idle: no process runs it
	VisitHostUnknown() // idle: its host does not say (D-B)
}

type (
	reasonCards struct {
		cards   CardCount
		holding Holding
	}
	reasonTerminalDialog struct{}
	reasonTurn           struct{}
	reasonBackground     struct{ background Background }
	reasonUnseen         struct{}
	reasonJustFinished   struct{}
	reasonAsks           struct{ seeing Seeing }
	reasonUsageLimit     struct{ failure Failure }
	reasonModel          struct{}
	reasonStartRefused   struct{}
	reasonInterrupted    struct{ resumability Resumability }
	reasonSeen           struct{}
	reasonNotRunning     struct{}
	reasonHostUnknown    struct{}
)

func (r reasonCards) Accept(v ReasonVisitor)        { v.VisitCards(r.cards, r.holding) }
func (reasonTerminalDialog) Accept(v ReasonVisitor) { v.VisitTerminalDialog() }
func (reasonTurn) Accept(v ReasonVisitor)           { v.VisitTurn() }
func (r reasonBackground) Accept(v ReasonVisitor)   { v.VisitBackground(r.background) }
func (reasonUnseen) Accept(v ReasonVisitor)         { v.VisitUnseen() }
func (reasonJustFinished) Accept(v ReasonVisitor)   { v.VisitJustFinished() }
func (r reasonAsks) Accept(v ReasonVisitor)         { v.VisitAsks(r.seeing) }
func (r reasonUsageLimit) Accept(v ReasonVisitor)   { v.VisitUsageLimit(r.failure) }
func (reasonModel) Accept(v ReasonVisitor)          { v.VisitModel() }
func (reasonStartRefused) Accept(v ReasonVisitor)   { v.VisitStartRefused() }
func (r reasonInterrupted) Accept(v ReasonVisitor)  { v.VisitRestartInterrupted(r.resumability) }
func (reasonSeen) Accept(v ReasonVisitor)           { v.VisitSeen() }
func (reasonNotRunning) Accept(v ReasonVisitor)     { v.VisitNotRunning() }
func (reasonHostUnknown) Accept(v ReasonVisitor)    { v.VisitHostUnknown() }

func (reasonCards) reason()          {}
func (reasonTerminalDialog) reason() {}
func (reasonTurn) reason()           {}
func (reasonBackground) reason()     {}
func (reasonUnseen) reason()         {}
func (reasonJustFinished) reason()   {}
func (reasonAsks) reason()           {}
func (reasonUsageLimit) reason()     {}
func (reasonModel) reason()          {}
func (reasonStartRefused) reason()   {}
func (reasonInterrupted) reason()    {}
func (reasonSeen) reason()           {}
func (reasonNotRunning) reason()     {}
func (reasonHostUnknown) reason()    {}
