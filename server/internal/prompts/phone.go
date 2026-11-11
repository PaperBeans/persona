package prompts

// RenderPhoneQueuedWrite adds the hub's optional delivery contract to a phone's description.
func RenderPhoneQueuedWrite(description string) string {
	return render("tools/phone_queued_write", description)
}

// RenderPhoneQueueOption describes the argument consumed by the hub before forwarding to a phone.
func RenderPhoneQueueOption() string { return render("tools/phone_queue_option", nil) }

// PhoneClipboardFile is phone_clipboard_set's description with its file argument, which `personad mcp` reads on the
// agent's machine (mcpserver, #3947): the phone's own description and the limits that argument has.
type PhoneClipboardFile struct {
	Description string
	MaxKiB      int // the largest file
	TailMin     int // the shortest text whose last 4 characters the result shows
}

func RenderPhoneClipboardFile(p PhoneClipboardFile) string {
	return render("tools/phone_clipboard_file", p)
}

// RenderPhoneClipboardFileOption and RenderPhoneClipboardTrimOption describe phone_clipboard_set's file and trim.
func RenderPhoneClipboardFileOption() string { return render("tools/phone_clipboard_file_option", nil) }
func RenderPhoneClipboardTrimOption() string { return render("tools/phone_clipboard_trim_option", nil) }
