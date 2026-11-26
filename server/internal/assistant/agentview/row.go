package agentview

import "github.com/nwverse/persona/server/internal/domainkit/defect"

// Row is where fleetcount put a session among the Agents list's Needs you rows (fleetcount.AgentsNeedsYou on the
// Agents list's surface), which the caller hands in as it was decided. The zero Row is nil; Build refuses it.
type Row interface {
	Accept(v RowVisitor)
	row() // sealed: only this package makes one
}

// RowVisitor is told where the session's row is.
type RowVisitor interface {
	VisitByCards(cards CardCount, holding Holding, counting Counting) // a row its cards stand for, its folded ones' too
	VisitByDialog(counting Counting)                                  // a row for a dialog its terminal shows
	VisitElsewhere(cards CardCount, holding Holding)                  // no row of its own: its cards are under another or count nowhere
	VisitSupervised()                                                 // no row: its task's supervisor holds every card it asked
	VisitNone()                                                       // no row, and no card
}

type rowByCards struct {
	cards    CardCount
	holding  Holding
	counting Counting
}

type rowByDialog struct{ counting Counting }

type rowElsewhere struct {
	cards   CardCount
	holding Holding
}

type rowSupervised struct{}

type rowNone struct{}

// RowByCards is a row of the session's own that its cards stand for, the cards of what folds under it included. A
// zero Holding or Counting is a defect.
func RowByCards(cards CardCount, holding Holding, counting Counting) Row {
	if holding == (Holding{}) || counting == (Counting{}) {
		defect.Raise("agentview.RowByCards", "the zero Holding or Counting")
	}
	return rowByCards{cards: cards, holding: holding, counting: counting}
}

// RowByDialog is a row of the session's own for a dialog its terminal shows that no card stands for. The zero
// Counting is a defect.
func RowByDialog(counting Counting) Row {
	if counting == (Counting{}) {
		defect.Raise("agentview.RowByDialog", "the zero Counting")
	}
	return rowByDialog{counting: counting}
}

// RowElsewhere is a session with cards and no row of its own: they sit under the row it folds under, or count nowhere
// (a directory-hidden session, one of the assistant's conversations). The zero Holding is a defect.
func RowElsewhere(cards CardCount, holding Holding) Row {
	if holding == (Holding{}) {
		defect.Raise("agentview.RowElsewhere", "the zero Holding")
	}
	return rowElsewhere{cards: cards, holding: holding}
}

// RowSupervised is a session in no row because its task's supervisor holds every card it asked (D11).
func RowSupervised() Row { return rowSupervised{} }

// RowNone is a session in no row with no card.
func RowNone() Row { return rowNone{} }

func (r rowByCards) Accept(v RowVisitor)  { v.VisitByCards(r.cards, r.holding, r.counting) }
func (r rowByDialog) Accept(v RowVisitor) { v.VisitByDialog(r.counting) }
func (r rowElsewhere) Accept(v RowVisitor) {
	v.VisitElsewhere(r.cards, r.holding)
}
func (rowSupervised) Accept(v RowVisitor) { v.VisitSupervised() }
func (rowNone) Accept(v RowVisitor)       { v.VisitNone() }
func (rowByCards) row()                   {}
func (rowByDialog) row()                  {}
func (rowElsewhere) row()                 {}
func (rowSupervised) row()                {}
func (rowNone) row()                      {}
