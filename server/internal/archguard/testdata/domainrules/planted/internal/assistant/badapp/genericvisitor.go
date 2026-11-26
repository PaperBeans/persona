package badapp

// peek is a generic visitor in a file that imports no core: genericread.go instantiates it as peek[slot.Slot,
// fleetcount.Why, instant.Instant, fleetcount.RowCount], a fleetcount.TallyVisitor.
type peek[S, W, I, T any] struct{ total *T }

func (p peek[S, W, I, T]) VisitAgent(S, W, I)       {}                   // out-visitor-impl
func (p peek[S, W, I, T]) VisitCard(S, W)           {}                   // out-visitor-impl
func (p peek[S, W, I, T]) VisitAgentByCard(S, S, W) {}                   // out-visitor-impl
func (p peek[S, W, I, T]) VisitTotal(total T)       { *p.total = total } // out-visitor-impl
