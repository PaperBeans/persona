package badcore

// Note is a note on a card. The zero Note is no note.
type Note struct {
	title Words
	body  Words
}

// Title hands the title out.
func (n Note) Title() Words { return n.title } // core-accessor

// NoteTitle is a domain service in name only: it hands the title out.
func NoteTitle(n Note) Words { return n.title } // core-accessor

// NewWordsOf is a constructor in name only: it hands the body out.
func NewWordsOf(n Note) Words { return n.body } // core-accessor

// ID names the note by its title, which is no identity: an ID is the package's own <X>ID type or a shared kernel's.
func (n Note) ID() Words { return n.title } // core-accessor

// NoteBuilder builds a note. The zero NoteBuilder is an empty note.
type NoteBuilder struct{ noteBuilder Note }

// Build hands the builder's title out instead of an outcome of a note.
func (b NoteBuilder) Build() Words { return b.noteBuilder.title } // core-accessor
