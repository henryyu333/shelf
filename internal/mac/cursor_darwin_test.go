//go:build darwin

package mac

import (
	"fmt"
	"testing"
	"unsafe"

	"github.com/ebitengine/purego"
	"github.com/ebitengine/purego/objc"
)

// newStockPanel makes a bare non-activating NSPanel, the class mygo builds
// the overlay from.
func newStockPanel(t *testing.T, initPanel func(id, objc.SEL, NSRect, uint, uint, bool) id) id {
	t.Helper()
	var panel id
	withPool(func() {
		send(class("NSApplication"), "sharedApplication")
		panel = initPanel(send(class("NSPanel"), "alloc"), sel("initWithContentRect:styleMask:backing:defer:"),
			NSRect{Size: NSSize{Width: 280, Height: 252}}, nsWindowStyleMaskNonactivatingPanel, 2, false)
		if panel != 0 {
			send(panel, "setReleasedWhenClosed:", 0)
		}
	})
	if panel == 0 {
		t.Fatal("NSPanel alloc/init failed")
	}
	return panel
}

// cursorAreas counts the tracking areas on view that ask for cursorUpdate.
func cursorAreas(view id) (int, id) {
	var n int
	var area id
	areas := send(view, "trackingAreas")
	count := sendInt(areas, "count")
	for i := range count {
		a := send(areas, "objectAtIndex:", uintptr(i))
		if sendInt(a, "options")&nsTrackingCursorUpdate != 0 {
			n++
			area = a
		}
	}
	return n, area
}

// The panel declares its own arrow cursor: ShelfU is a non-activating
// overlay, so mygo's ActiveInActiveApp tracking area never fires and the
// window underneath leaves its I-beam (or zoom) cursor on the panel. The
// area must be CursorUpdate + ActiveAlways + InVisibleRect, live on the
// panel's content view only, and its owner must set the arrow.
func TestNotchPanelDeclaresTheArrowCursor(t *testing.T) {
	onMain(t, func() error {
		if err := Init(); err != nil {
			return err
		}
		var initPanel func(id, objc.SEL, NSRect, uint, uint, bool) id
		purego.RegisterFunc(&initPanel, msgSendAddr)
		panel := newStockPanel(t, initPanel)
		stock := newStockPanel(t, initPanel)
		defer withPool(func() {
			send(panel, "orderOut:", 0)
			send(panel, "close")
			send(stock, "orderOut:", 0)
			send(stock, "close")
		})
		withPool(func() {
			ConfigureNotchWindow(uintptr(panel))
			// A second configure must not stack a second area.
			ConfigureNotchWindow(uintptr(panel))
		})
		view := send(panel, "contentView")
		if view == 0 {
			return fmt.Errorf("configured panel has no content view")
		}
		if n, area := cursorAreas(view); n != 1 {
			return fmt.Errorf("panel content view has %d cursor areas, want 1", n)
		} else {
			opts := sendInt(area, "options")
			if opts&panelCursorOptions != panelCursorOptions {
				return fmt.Errorf("cursor area options 0x%x, want 0x%x", opts, panelCursorOptions)
			}
			if owner := send(area, "owner"); owner == 0 || owner != getAssocObject(view, unsafe.Pointer(&assocCursorOwner)) {
				return fmt.Errorf("cursor area owner is not the panel's own")
			}
		}
		if n, _ := cursorAreas(send(stock, "contentView")); n != 0 {
			return fmt.Errorf("an unrelated panel got %d cursor areas", n)
		}
		// The owner answers cursorUpdate: with the arrow, whatever the
		// pointer carried a moment before.
		owner := getAssocObject(view, unsafe.Pointer(&assocCursorOwner))
		if owner == 0 {
			return fmt.Errorf("panel content view has no cursor owner mark")
		}
		withPool(func() {
			send(send(class("NSCursor"), "IBeamCursor"), "set")
			if send(class("NSCursor"), "currentCursor") == send(class("NSCursor"), "arrowCursor") {
				t.Errorf("an inactive panel could not carry a text cursor; the test measured nothing")
			}
			send(owner, "cursorUpdate:", 0)
		})
		if got, want := send(class("NSCursor"), "currentCursor"), send(class("NSCursor"), "arrowCursor"); got != want {
			return fmt.Errorf("cursorUpdate: did not set the arrow")
		}
		return nil
	})
}
