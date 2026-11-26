package badcore

// Longest is the longest of all.
func Longest(all []Words) Words { // core-collection
	var best Words
	for _, w := range all {
		if len(w.words) > len(best.words) {
			best = w
		}
	}
	return best
}
