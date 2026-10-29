//go:build darwin

package mac

import (
	"errors"
	"fmt"
	"sync"
	"unsafe"

	"github.com/ebitengine/purego"
	"github.com/ebitengine/purego/objc"
)

// Panel pointer policy: the panel is a non-activating NSPanel, so ShelfU is
// never the active app while it is open. mygo's own tracking area is
// NSTrackingActiveInActiveApp (0x40) and therefore never fires here, and no
// other cursor owner is left to reset the pointer the app underneath set —
// a text I-beam or a zoom cursor from the window behind stays on the panel.
// The panel's content view gets its own area with CursorUpdate (0x04),
// ActiveAlways (0x80) and InVisibleRect (0x200): while the pointer is over
// the panel (and only then) the arrow is declared, and the same view's other
// tracking areas keep their behaviour.
const (
	nsTrackingCursorUpdate  = 0x04
	nsTrackingActiveAlways  = 0x80
	nsTrackingInVisibleRect = 0x200
	panelCursorOptions      = nsTrackingCursorUpdate | nsTrackingActiveAlways | nsTrackingInVisibleRect

	// OBJC_ASSOCIATION_RETAIN: the tracking area holds its owner weakly,
	// so the view's association keeps the Go-side owner alive.
	objcAssociationRetain = 1
)

var (
	cursorOwnerOnce  sync.Once
	cursorOwnerClass id
	cursorOwnerErr   error

	cursorBridgeOnce    sync.Once
	msgInitTrackingArea func(area id, cmd objc.SEL, rect NSRect, options uint, owner id, userInfo id) id
	msgAddTrackingArea  func(view id, cmd objc.SEL, area id)

	// assocCursorOwner marks the content view of a panel that already has
	// the cursor area, and keeps its owner alive for the view's lifetime.
	assocCursorOwner byte
)

// cursorArrowUpdate is the tracking area's cursorUpdate: — a plain arrow,
// declared from inside the panel.
func cursorArrowUpdate(_ id, _ objc.SEL, _ id) {
	send(send(class("NSCursor"), "arrowCursor"), "set")
}

// newCursorOwner allocates one NSTrackingArea owner that answers
// cursorUpdate: with the arrow. The object carries no state: the tracking
// area names the view it belongs to.
func newCursorOwner() id {
	cursorOwnerOnce.Do(func() {
		cls, err := objc.RegisterClass("ShelfCursorOwner", objc.GetClass("NSObject"), nil, nil, []objc.MethodDef{
			{Cmd: sel("cursorUpdate:"), Fn: cursorArrowUpdate},
		})
		if err != nil {
			cursorOwnerErr = fmt.Errorf("mac: panel cursor owner: %w", err)
			return
		}
		cursorOwnerClass = id(cls)
	})
	if cursorOwnerErr != nil || cursorOwnerClass == 0 {
		return 0
	}
	return send(send(cursorOwnerClass, "alloc"), "init")
}

// installPanelCursor adds the panel's own cursor area to the content view
// of this window. It is called for the notch panel only (ConfigureNotchWindow);
// sibling windows of the same classes — Settings, pins, the basket — never
// get it. A repeated call on the same window is a no-op. Main thread only.
func installPanelCursor(window id) error {
	if window == 0 {
		return nil
	}
	view := send(window, "contentView")
	if view == 0 {
		return errors.New("mac: panel cursor: window has no content view")
	}
	if getAssocObject(view, unsafe.Pointer(&assocCursorOwner)) != 0 {
		return nil
	}
	if err := Init(); err != nil {
		return err
	}
	cursorBridgeOnce.Do(func() {
		purego.RegisterFunc(&msgInitTrackingArea, msgSendAddr)
		purego.RegisterFunc(&msgAddTrackingArea, msgSendAddr)
	})
	owner := newCursorOwner()
	if owner == 0 {
		return cursorOwnerErr
	}
	area := msgInitTrackingArea(send(class("NSTrackingArea"), "alloc"),
		sel("initWithRect:options:owner:userInfo:"), NSRect{}, panelCursorOptions, owner, 0)
	if area == 0 {
		return errors.New("mac: panel cursor: could not create the tracking area")
	}
	msgAddTrackingArea(view, sel("addTrackingArea:"), area)
	setAssocObject(view, unsafe.Pointer(&assocCursorOwner), owner, objcAssociationRetain)
	send(area, "release")
	return nil
}

// setArrowCursor declares the arrow once, for the moment the overlay takes
// the mouse back from the app underneath. The tracking area handles every
// later move; this covers the switch itself, when the pointer is already
// standing on the panel and no cursorUpdate: is sent.
func setArrowCursor() {
	if err := Init(); err != nil {
		return
	}
	withPool(func() { cursorArrowUpdate(0, 0, 0) })
}
