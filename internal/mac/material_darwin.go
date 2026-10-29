//go:build darwin

package mac

// The expanded panel's backdrop material: an NSVisualEffectView below the
// window's own content, framed and rounded to the shell the panel draws.
// It is the only way to blur what is behind the window in the shell's
// shape (the mygo glass plugin blurs the window's own content instead), so
// the light appearance uses it and the dark appearance does not.
//
// The view takes no pointer of its own (it sits below the content view)
// and is hidden whenever the panel is pure black.

import (
	"strings"
	"unsafe"
)

// ShellMaterial is one effect view installed in a window.
type ShellMaterial struct {
	view id
}

// NewShellMaterial finds the effect view the window's own vibrancy call
// installed (mygo's SetVibrancy) and returns it, so the material is the one
// AppKit already renders below the page; nil when the window has none.
func NewShellMaterial(window uintptr) *ShellMaterial {
	load()
	if window == 0 {
		return nil
	}
	win := id(window)
	cv := send(win, "contentView")
	if cv == 0 {
		return nil
	}
	subs := send(cv, "subviews")
	count := sendInt(subs, "count")
	for i := 0; i < count; i++ {
		v := send(subs, "objectAtIndex:", uintptr(i))
		if v == 0 {
			continue
		}
		if strings.Contains(className(v), "VisualEffectView") {
			return &ShellMaterial{view: v}
		}
	}
	return nil
}

// className is the object's class name.
func className(v id) string {
	p := classGetName(send(v, "class"))
	if p == nil {
		return ""
	}
	var b []byte
	for i := 0; i < 128; i++ {
		c := *(*byte)(unsafe.Add(p, i))
		if c == 0 {
			break
		}
		b = append(b, c)
	}
	return string(b)
}

// SetFrame places the material in window coordinates (AppKit's origin is
// the bottom-left corner) with a corner radius applied to the two bottom
// corners, matching the shell's rounded bottom.
func (m *ShellMaterial) SetFrame(x, y, w, h, radius float64) {
	if m == nil || m.view == 0 {
		return
	}
	msgSetRect(m.view, sel("setFrame:"), NSRect{Origin: NSPoint{X: x, Y: y}, Size: NSSize{Width: w, Height: h}})
	layer := send(m.view, "layer")
	if layer == 0 {
		return
	}
	msgSetFloat(layer, sel("setCornerRadius:"), radius)
	// kCALayerMinXMinYCorner | kCALayerMaxXMinYCorner: the bottom two in a
	// non-flipped view.
	send(layer, "setMaskedCorners:", 3)
}

// SetVisible shows or hides the material.
func (m *ShellMaterial) SetVisible(v bool) {
	if m == nil || m.view == 0 {
		return
	}
	n := uintptr(0)
	if !v {
		n = 1
	}
	send(m.view, "setHidden:", n)
}

// Remove takes the material out of its window.
func (m *ShellMaterial) Remove() {
	if m == nil || m.view == 0 {
		return
	}
	send(m.view, "removeFromSuperview")
	m.view = 0
}

// SystemDark reports the application's effective appearance: true for the
// dark one. The panel follows it while it runs.
func SystemDark() bool {
	app := send(class("NSApplication"), "sharedApplication")
	appearance := send(app, "effectiveAppearance")
	name := goString(send(appearance, "name"))
	return strings.Contains(name, "Dark")
}

// ReduceTransparency reports the system's Reduce Transparency setting.
func ReduceTransparency() bool {
	ws := send(class("NSWorkspace"), "sharedWorkspace")
	return send(ws, "accessibilityDisplayShouldReduceTransparency") != 0
}
