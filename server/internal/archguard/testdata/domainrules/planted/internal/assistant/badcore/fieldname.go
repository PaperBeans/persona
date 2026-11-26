package badcore

// Role is whose conversation it is. The zero Role is no role.
type Role struct{ code uint8 } // core-field-name

// State is where a turn stands. The zero State is no state.
type State struct{ code uint8 } // core-field-name, core-distinct-shape
