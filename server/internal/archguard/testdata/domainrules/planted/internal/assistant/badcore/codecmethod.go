package badcore

// Grade is how urgent a card is. The zero Grade is none.
type Grade struct{ grade uint8 }

func (g Grade) String() string { return "grade" } // core-codec-method, core-accessor, core-primitive-signature
