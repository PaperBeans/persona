package badcore

// rawText is raw text under an alias of the package's own.
type rawText = string

// amount is a number as a named type of the package's own.
type amount int

// wordList is a slice under a name of the package's own.
type wordList []Words

// onWord is a callback under a name of the package's own.
type onWord func(w Words)

// Ticket holds raw values behind the package's own names. The zero Ticket is no ticket.
type Ticket struct {
	title rawText // core-primitive-field
	seats amount  // core-primitive-field
}

// NewTicket takes raw text behind the package's own name.
func NewTicket(title rawText, w Words) Ticket { return Ticket{title: title} } // core-primitive-signature

// Wanted hands a list out behind the package's own name.
func Wanted(t Ticket) wordList { return nil } // core-collection

// Each takes a callback behind the package's own name.
func (t Ticket) Each(f onWord) Ticket { return t } // core-interface-param
