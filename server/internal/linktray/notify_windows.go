//go:build windows

package linktray

import (
	"unsafe"
)

// notifyIcon is the tray's icon in the notification area, version 4 behavior: a click is NIN_SELECT, the context
// menu WM_CONTEXTMENU, with the anchor point in wParam.
type notifyIcon struct {
	hwnd  uintptr
	id    uint32
	icon  uintptr
	tip   string
	added bool
}

func (n *notifyIcon) data(flags uint32) notifyIconData {
	d := notifyIconData{Wnd: n.hwnd, ID: n.id, Flags: flags, CallbackMessage: wmTrayIcon, Icon: n.icon}
	d.Size = uint32(unsafe.Sizeof(d))
	copyUTF16(d.Tip[:], n.tip)
	return d
}

func shellNotify(op uint32, d *notifyIconData) bool {
	r, _, _ := procShellNotifyIconW.Call(uintptr(op), uintptr(unsafe.Pointer(d)))
	return r != 0
}

// add puts the icon in the notification area (again, after Explorer restarted).
func (n *notifyIcon) add() bool {
	d := n.data(nifMessage | nifIcon | nifTip | nifShowTip)
	if !shellNotify(nimAdd, &d) {
		// Still there from before Explorer restarted, or a previous run of ours that crashed: take it over.
		shellNotify(nimDelete, &d)
		if !shellNotify(nimAdd, &d) {
			return false
		}
	}
	d.Version = notifyVersion
	shellNotify(nimSetVersion, &d)
	n.added = true
	return true
}

// set changes the picture and the tooltip; the previous picture is destroyed once the shell has the new one.
func (n *notifyIcon) set(icon uintptr, tip string) {
	old := n.icon
	n.icon, n.tip = icon, tip
	if n.added {
		d := n.data(nifIcon | nifTip | nifShowTip)
		shellNotify(nimModify, &d)
	}
	if old != 0 && old != icon {
		destroyIcon(old)
	}
}

func (n *notifyIcon) remove() {
	if n.added {
		d := n.data(0)
		shellNotify(nimDelete, &d)
		n.added = false
	}
	destroyIcon(n.icon)
	n.icon = 0
}

// rect is where the icon is on screen; ok is false when it cannot be told (in the overflow, hidden).
func (n *notifyIcon) rect() (rect, bool) {
	id := notifyIconIdentifier{Wnd: n.hwnd, ID: n.id}
	id.Size = uint32(unsafe.Sizeof(id))
	var r rect
	hr, _, _ := procShellNotifyIconGetRect.Call(uintptr(unsafe.Pointer(&id)), uintptr(unsafe.Pointer(&r)))
	return r, hr == 0 && r.width() > 0
}
