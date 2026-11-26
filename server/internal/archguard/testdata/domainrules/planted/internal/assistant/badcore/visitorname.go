package badcore

// LabelVisitor is told a label's words.
type LabelVisitor interface {
	Words(w Words) // core-visitor-name
}
