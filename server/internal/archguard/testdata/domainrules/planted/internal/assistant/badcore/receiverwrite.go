package badcore

// Tags is a card's tags. The zero Tags is none.
type Tags struct{ tags []Words }

func (t Tags) first(w Words) Tags {
	t.tags[0] = w // core-receiver-write
	return t
}
