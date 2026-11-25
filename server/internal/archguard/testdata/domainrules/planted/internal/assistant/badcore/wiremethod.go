package badcore

// Pair is two words. The zero Pair is none.
type Pair struct {
	left  Words
	right Words
}

func (p Pair) Wire() Words { return p.left } // core-wire-method
