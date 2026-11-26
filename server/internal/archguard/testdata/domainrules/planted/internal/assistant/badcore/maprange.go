package badcore

func count(seen map[Words]bool) int {
	n := 0
	for range seen { // core-map-range
		n++
	}
	return n
}
