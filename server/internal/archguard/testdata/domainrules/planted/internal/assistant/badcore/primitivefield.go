package badcore

// Seat is an agent's seat. The zero Seat is no seat.
type Seat struct {
	machine string // core-primitive-field
	muted   bool   // core-primitive-field
}
