//go:build darwin

package main

// The overlay smoke tests read the screen's topmost windows: what is at a
// point, and what the notch area looks like. Another copy of ShelfU (the
// installed one) keeps windows at the same status level, and its hover
// panel or live activity sits exactly over ours. A test that cannot tell
// our window from a foreign one proves nothing, so it skips with the
// window it found instead of failing on the environment.

import (
	"testing"

	"github.com/ebitengine/purego"
	"github.com/ebitengine/purego/objc"
	"github.com/egoist/mygo"

	"shelf/internal/mac"
)

// smokeWindow is one on-screen window as the window server lists it.
type smokeWindow struct {
	ID    int
	Owner int
	Layer int
	Frame mygo.Rectangle
}

// screenWindows lists every on-screen window.
func screenWindows(t *testing.T) []smokeWindow {
	t.Helper()
	lib, err := purego.Dlopen("/System/Library/Frameworks/CoreGraphics.framework/CoreGraphics", purego.RTLD_LAZY)
	if err != nil {
		return nil
	}
	var copyList func(uint32, uint32) uintptr
	purego.RegisterLibFunc(&copyList, lib, "CGWindowListCopyWindowInfo")
	var out []smokeWindow
	onMain(func() {
		list := objc.ID(copyList(1, 0)) // kCGWindowListOptionOnScreenOnly
		if list == 0 {
			return
		}
		defer objcSend(list, "release")
		get := func(d objc.ID, key string) objc.ID { return objcSend(d, "objectForKey:", objcStr(key)) }
		integer := func(d objc.ID, key string) int { return int(objcSend(get(d, key), "intValue")) }
		for i, n := 0, int(objcSend(list, "count")); i < n; i++ {
			d := objcSend(list, "objectAtIndex:", i)
			b := get(d, "kCGWindowBounds")
			out = append(out, smokeWindow{
				ID:    integer(d, "kCGWindowNumber"),
				Owner: integer(d, "kCGWindowOwnerPID"),
				Layer: integer(d, "kCGWindowLayer"),
				Frame: mygo.Rectangle{
					X: integer(b, "X"), Y: integer(b, "Y"),
					Width: integer(b, "Width"), Height: integer(b, "Height"),
				},
			})
		}
	})
	return out
}

// foreignIn is foreignAt over a snapshot: it runs no main-thread work, so
// a caller already inside onMain can use it.
func foreignIn(windows []smokeWindow, p mygo.Point, minLayer int) (smokeWindow, bool) {
	for _, w := range windows {
		if w.Owner == ownPID() || w.Layer < minLayer {
			continue
		}
		if p.X >= w.Frame.X && p.X < w.Frame.X+w.Frame.Width &&
			p.Y >= w.Frame.Y && p.Y < w.Frame.Y+w.Frame.Height {
			return w, true
		}
	}
	return smokeWindow{}, false
}

// foreignAt is foreignIn over a fresh snapshot.
func foreignAt(t *testing.T, p mygo.Point, minLayer int) (smokeWindow, bool) {
	t.Helper()
	return foreignIn(screenWindows(t), p, minLayer)
}

// foreignOverArena is foreignAt for any window overlapping r.
func foreignOverArena(t *testing.T, r mygo.Rectangle, minLayer int) (smokeWindow, bool) {
	t.Helper()
	for _, w := range screenWindows(t) {
		if w.Owner == ownPID() || w.Layer < minLayer {
			continue
		}
		if w.Frame.X < r.X+r.Width && r.X < w.Frame.X+w.Frame.Width &&
			w.Frame.Y < r.Y+r.Height && r.Y < w.Frame.Y+w.Frame.Height {
			return w, true
		}
	}
	return smokeWindow{}, false
}

// skipIfForeignOverlay skips when another process' window sits at the
// overlay level over the point, which is what an installed ShelfU's panel
// or live activity does at the notch.
func skipIfForeignOverlay(t *testing.T, p mygo.Point) {
	t.Helper()
	if w, ok := foreignAt(t, p, mac.StatusWindowLevel); ok {
		t.Skipf("window %d (pid %d, layer %d) covers %+v: another copy of ShelfU or an overlay app is at the notch",
			w.ID, w.Owner, w.Layer, p)
	}
}

// skipIfForeignOverlayOver skips when one covers the whole area.
func skipIfForeignOverlayOver(t *testing.T, r mygo.Rectangle) {
	t.Helper()
	if w, ok := foreignOverArena(t, r, mac.StatusWindowLevel); ok {
		t.Skipf("window %d (pid %d, layer %d) overlaps %+v: another copy of ShelfU or an overlay app is at the notch",
			w.ID, w.Owner, w.Layer, r)
	}
}
