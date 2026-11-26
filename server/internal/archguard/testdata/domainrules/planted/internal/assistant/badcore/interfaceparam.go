package badcore

// Pick is w when choose says so.
func Pick(w Words, choose func(Words) bool) Words { // core-interface-param
	if choose(w) {
		return w
	}
	return Words{}
}
