package extmgr

// refreshTrackKinds asks the in-process assistant to reread declarative kinds
// after a successful migration. Registry changes use the same callback.
func (h *Manager) tracksChanged() {
	if h.opts.TracksChanged != nil {
		h.opts.TracksChanged()
	}
}
