package badcore

// Label is a card's label. The zero Label is no label.
type Label struct {
	Text  Words // core-exported-field
	words Words
}
