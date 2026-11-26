package botcard

import "encoding/json"

// MaxBytes and MaxElements bound one card; each entry's real marshaled JSON is measured against them.
const (
	MaxBytes    = 24000
	MaxElements = 50
)

// ElementBytes is the JSON length of add()'s elements; json.Marshal escapes &<>, Card.JSON does not, so it errs high.
func ElementBytes(add func(*Card)) int {
	c := NewCard("", "", "")
	add(c)
	n := 0
	for _, el := range c.Body.Elements {
		b, _ := json.Marshal(el)
		n += len(b) + 1 // its joining comma inside the body's element array
	}
	return n
}

// Segments packs entries within the budgets; an entry is never cut, one always fits on an empty card.
// measure returns an entry's marshaled bytes and element count. head1 and headN reserve header bytes
// on the first and later cards. The first card reserves two element slots for section labels, later
// cards one; hr is the cost of a divider between entries.
func Segments[T any](es []T, head1, headN, hr int, measure func(T) (bytes, elements int)) [][]T {
	segs := [][]T{{}}
	ne, nb := 2, head1
	for _, e := range es {
		ib, ie := measure(e)
		cur := &segs[len(segs)-1]
		if len(*cur) > 0 {
			ie, ib = ie+1, ib+hr
			if ne+ie > MaxElements || nb+ib > MaxBytes {
				segs = append(segs, nil)
				cur, ne, nb, ie, ib = &segs[len(segs)-1], 1, headN, ie-1, ib-hr
			}
		}
		*cur, ne, nb = append(*cur, e), ne+ie, nb+ib
	}
	return segs
}
