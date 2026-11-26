package badcore

// Box carries the caller's value. The zero Box is empty.
type Box[H comparable] struct{ box H } // core-type-param
