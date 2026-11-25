package badcore

// MakeWords is a constructor by another name.
func MakeWords(w Words) Words { return w } // core-constructor

// NewEmpty is named for what it makes, yet makes no type of this package.
func NewEmpty(w Words) bool { return w.words == "" } // core-constructor
