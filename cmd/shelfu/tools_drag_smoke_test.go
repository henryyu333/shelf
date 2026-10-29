//go:build darwin

package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/ebitengine/purego"
	"github.com/ebitengine/purego/objc"
	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"shelf/internal/layout"
	"shelf/internal/mac"
)

// Inspect WindowServer's list, not merely the application's window pointers.
// Only this test process's windows are retained in diagnostics.
type smokeWindowInfo struct {
	ID, Layer int
	Frame     mygo.Rectangle
	Onscreen  bool
	Title     string
}

// isDragCardWindow reports whether w is a card-sized drag window: the
// preview and the committed pin are exactly one card. The shared smoke
// shelf's sheet is a larger window at the same place and must not be
// mistaken for the gesture's card.
func isDragCardWindow(w smokeWindowInfo) bool {
	return w.Frame.Height == pinCardH && (w.Frame.Width == pinCardW || w.Frame.Width == pinCardW2)
}

func ownSmokeWindows(t *testing.T) []smokeWindowInfo {
	t.Helper()
	lib, err := purego.Dlopen("/System/Library/Frameworks/CoreGraphics.framework/CoreGraphics", purego.RTLD_LAZY)
	if err != nil {
		t.Fatal(err)
	}
	var copyList func(uint32, uint32) uintptr
	purego.RegisterLibFunc(&copyList, lib, "CGWindowListCopyWindowInfo")
	var out []smokeWindowInfo
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
			if integer(d, "kCGWindowOwnerPID") != os.Getpid() {
				continue
			}
			b := get(d, "kCGWindowBounds")
			out = append(out, smokeWindowInfo{
				ID: integer(d, "kCGWindowNumber"), Layer: integer(d, "kCGWindowLayer"),
				Frame:    mygo.Rectangle{X: integer(b, "X"), Y: integer(b, "Y"), Width: integer(b, "Width"), Height: integer(b, "Height")},
				Onscreen: objcSend(get(d, "kCGWindowIsOnscreen"), "boolValue") != 0,
				Title:    objcGoStr(get(d, "kCGWindowName")),
			})
		}
	})
	return out
}

// Reject unsafe HID injection before posting anything: neither a menu-bar
// coordinate nor a window owned by a different process is a valid target.
func requireOwnMouseTarget(t *testing.T, x, y float64) {
	t.Helper()
	p := mygo.Point{X: int(x), Y: int(y)}
	d := mygo.Screen.DisplayNearestPoint(p)
	if !insideRect(p, d.WorkArea) || p.Y < d.WorkArea.Y+2 {
		t.Fatalf("refusing mouse event outside the work area: %+v", p)
	}
	var hit int
	onMain(func() {
		primary := mygo.Screen.PrimaryDisplay()
		hit = mac.WindowAtPoint(x, y, float64(primary.Bounds.Y+primary.Bounds.Height))
	})
	// The notch shell takes the mouse only while the pointer is on it, so
	// WindowServer's answer depends on where the user left the cursor. The
	// caller is about to click this test-window point: prove the point is
	// inside one of our frames first, move the pointer there (as clickAt
	// does), and give the gate's 10 ms poll a beat to re-ask before judging
	// the native target. Without the move a still pointer keeps the shell
	// click-through and the click would land on whatever is below.
	insideOwnFrame := false
	for _, w := range ownSmokeWindows(t) {
		if insideRect(p, w.Frame) {
			insideOwnFrame = true
			break
		}
	}
	if !insideOwnFrame {
		t.Fatalf("refusing mouse event at %+v: point is outside every test window", p)
	}
	warpPointerTo(t, x, y)
	time.Sleep(60 * time.Millisecond)
	onMain(func() {
		primary := mygo.Screen.PrimaryDisplay()
		hit = mac.WindowAtPoint(x, y, float64(primary.Bounds.Y+primary.Bounds.Height))
	})
	for _, w := range ownSmokeWindows(t) {
		if w.ID == hit && w.Onscreen && insideRect(p, w.Frame) {
			return
		}
	}
	t.Fatalf("refusing mouse event at %+v: native target %d is not an onscreen test window", p, hit)
}

// warpPointerTo moves the real cursor, the same call clickAt makes, so a
// test that has to click a hover-gated surface does not depend on where
// the user left the pointer.
func warpPointerTo(t *testing.T, x, y float64) {
	t.Helper()
	lib, err := purego.Dlopen("/System/Library/Frameworks/CoreGraphics.framework/CoreGraphics", purego.RTLD_LAZY)
	if err != nil {
		t.Fatalf("CoreGraphics: %v", err)
	}
	var warp func(p mac.CGPoint) int32
	purego.RegisterLibFunc(&warp, lib, "CGWarpMouseCursorPosition")
	warp(mac.CGPoint{X: x, Y: y})
}

// A test-owned desktop substitute makes the entire drag path safe, including
// the portion outside the panel. It never covers or targets the menu bar.
func newSmokePointerArena(t *testing.T) (*mygo.Window, mygo.Rectangle) {
	t.Helper()
	d := mygo.Screen.PrimaryDisplay()
	r := mygo.Rectangle{X: d.WorkArea.X + 24, Y: d.WorkArea.Y + 80,
		Width: min(1100, d.WorkArea.Width-48), Height: min(650, d.WorkArea.Height-120)}
	return newSmokePointerBackdrop(t, r), r
}

func newSmokePointerBackdrop(t *testing.T, r mygo.Rectangle) *mygo.Window {
	t.Helper()
	var w *mygo.Window
	onMain(func() {
		w = mygo.NewWindow(mygo.WindowOptions{Title: "ShelfU pointer test arena", Hidden: true,
			Width: r.Width, Height: r.Height, X: r.X, Y: r.Y, Frameless: true, Panel: true,
			SkipTaskbar: true, DisableShadow: true, Content: ui.View(func(c *ui.Context) {
				ui.Box(c).Fill().Background(ui.Hex("#253445"))
			})})
		mac.ConfigurePinWindow(w.NativeHandle())
		mac.SetWindowLevel(w.NativeHandle(), 2)
		mac.SetWindowFrame(w.NativeHandle(), r.X, r.Y, r.Width, r.Height)
		w.ShowInactive()
	})
	t.Cleanup(func() { onMain(w.Destroy) })
	time.Sleep(150 * time.Millisecond)
	return w
}

func toolsDragShot(t *testing.T, r mygo.Rectangle, name string) {
	t.Helper()
	prefix := os.Getenv("SHELF_L_EVIDENCE_PREFIX")
	if prefix == "" {
		prefix = "after"
	}
	if err := os.MkdirAll("/tmp/shelf-fix-L", 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join("/tmp/shelf-fix-L", prefix+"-"+name+".png")
	b, err := exec.Command("screencapture", "-x", "-o", "-R", fmt.Sprintf("%d,%d,%d,%d", r.X, r.Y, r.Width, r.Height), path).CombinedOutput()
	if err != nil {
		t.Fatalf("screenshot: %v: %s", err, b)
	}
	t.Log(path)
}

func TestToolsNativeDragOutStaysVisible(t *testing.T) {
	if os.Getenv("SHELF_BASKET_SMOKE") != "1" {
		t.Skip("native window smoke requires SHELF_BASKET_SMOKE=1")
	}
	arena, area := newSmokePointerArena(t)
	a := &shelf{store: mustStore(t), dataDir: t.TempDir(), tab: pageTools, level: 2, visible: true,
		progress: 1, topInset: 32, notchW: 180, notchH: 32, hasNotch: true,
		x: area.X + 24, y: area.Y + 24, dispBounds: mygo.Screen.PrimaryDisplay().Bounds}
	a.notchX, a.notchY = a.x+(sheetW-180)/2, a.y
	var panelNum, arenaNum int
	onMain(func() {
		a.newNotchWindow()
		a.syncWindowFrame()
		a.win.ShowInactive()
		panelNum, arenaNum = mac.WindowNumber(a.winH), mac.WindowNumber(arena.NativeHandle())
	})
	t.Cleanup(func() {
		onMain(func() {
			for _, it := range a.pins {
				if it.win != nil {
					it.win.Destroy()
				}
			}
			a.resetToolsEdit()
			a.win.Destroy()
		})
	})
	time.Sleep(800 * time.Millisecond)
	// The headless view uses the same layout to locate the actual card box,
	// not a hard-coded title-text coordinate.
	var card ui.Rect
	var frame mygo.Rectangle
	onMain(func() {
		frame = a.frame
		mirror := &shelf{store: a.store, tab: pageTools, level: 2, visible: true,
			topInset: a.topInset, notchW: a.notchW, notchH: a.notchH}
		tr := ui.NewTester(mirror.view, frame.Width, frame.Height)
		var ok bool
		// The widgets lead the grid now, so the first visible action card
		// is 框选文字 (两列宽的状态栏旁边那一格), not the old row's 4th.
		card, ok = tr.Find("框选文字")
		if !ok {
			t.Error("the ocr card is missing from the panel layout")
		}
	})
	if t.Failed() {
		return
	}
	start := mygo.Point{X: frame.X + int(card.X+card.W/2), Y: frame.Y + int(card.Y+card.H/2)}
	// Park the pointer on the card, settle, then press: the overlay takes
	// the mouse only where the pointer is (overlayAcceptsMouse), and a
	// posted move reaches the cursor a few milliseconds after it is sent.
	// A hand moves and then presses; a machine posting both 30 ms apart
	// presses inside the window where the pointer has not been observed
	// yet, which is not a case a user can produce.
	// Same status level as any other copy of ShelfU at the notch: raise
	// ours so the press lands on the panel under test.
	onMain(func() {
		a.win.ShowInactive()
		a.win.Invalidate()
	})
	time.Sleep(120 * time.Millisecond)
	postMouse(t, float64(start.X), float64(start.Y), 5)
	time.Sleep(150 * time.Millisecond)
	postMouse(t, float64(start.X), float64(start.Y), 1)
	// Always release over our arena even if an assertion aborts the test.
	held := true
	t.Cleanup(func() {
		if held {
			postMouse(t, float64(start.X), float64(start.Y), 2)
		}
	})
	time.Sleep(durLongPress + 250*time.Millisecond)
	var editing bool
	onMain(func() { editing = a.toolsPage().editing })
	if !editing {
		t.Fatal("real long press did not enter tool editing")
	}
	toolsDragShot(t, area, "tools-edit")
	end := mygo.Point{X: area.X + area.Width - 180, Y: start.Y + 100}
	for i := 1; i <= 12; i++ {
		x := start.X + (end.X-start.X)*i/12
		y := start.Y + (end.Y-start.Y)*i/12
		postMouse(t, float64(x), float64(y), 6)
	}
	time.Sleep(200 * time.Millisecond)
	windows := ownSmokeWindows(t)
	t.Logf("held cursor=%+v windows=%+v", end, windows)
	visibleCard, previewNum := false, 0
	for _, w := range windows {
		if w.ID != panelNum && w.ID != arenaNum && w.Onscreen && isDragCardWindow(w) && insideRect(end, w.Frame) {
			if visibleCard {
				t.Error("two drag windows follow the same gesture")
			}
			visibleCard, previewNum = true, w.ID
		}
	}
	toolsDragShot(t, area, "drag-held")
	if !visibleCard {
		t.Error("the dragged card has no visible window outside the panel")
	}
	var persisted, live, hit, keyNum int
	onMain(func() {
		persisted, live = len(a.toolsLayout().Pins), len(a.pins)
		d := mygo.Screen.PrimaryDisplay()
		hit = mac.WindowAtPoint(float64(end.X), float64(end.Y), float64(d.Bounds.Height))
		key := objcSend(objcSend(objc.ID(objc.GetClass("NSApplication")), "sharedApplication"), "keyWindow")
		keyNum = int(objcSend(key, "windowNumber"))
	})
	if persisted != 0 || live != 0 {
		t.Error("holding a drag published a pin before release")
	}
	if hit != arenaNum || keyNum == previewNum {
		t.Error("the drag preview intercepts input or takes keyboard focus")
	}
	// Returning to the grid removes the external copy without committing.
	back := mygo.Point{X: a.x + sheetW/2, Y: start.Y + 24}
	postMouse(t, float64(back.X), float64(back.Y), 6)
	time.Sleep(150 * time.Millisecond)
	for _, w := range ownSmokeWindows(t) {
		if w.ID == previewNum && w.Onscreen {
			t.Error("returning to the grid left the drag preview visible")
		}
	}
	toolsDragShot(t, area, "drag-returned")
	postMouse(t, float64(end.X), float64(end.Y), 6)
	time.Sleep(100 * time.Millisecond)
	// Escape must destroy the transient window without creating a pin.
	onMain(a.escape)
	postMouse(t, float64(end.X), float64(end.Y), 2)
	held = false
	time.Sleep(150 * time.Millisecond)
	for _, w := range ownSmokeWindows(t) {
		if w.ID != panelNum && w.ID != arenaNum && w.Onscreen && w.Frame.Width == pinCardW && w.Frame.Height == pinCardH {
			t.Error("cancelling a drag left a card window behind")
		}
	}
	onMain(func() {
		if len(a.pins) != 0 || len(a.toolsLayout().Pins) != 0 {
			t.Error("cancelling a drag persisted a pin")
		}
	})
	// A new long press can still drag out and commit.
	postMouse(t, float64(start.X), float64(start.Y), 5, 1)
	held = true
	time.Sleep(durLongPress + 250*time.Millisecond)
	postMouse(t, float64(end.X), float64(end.Y), 6)
	time.Sleep(100 * time.Millisecond)
	previewNum = 0
	for _, w := range ownSmokeWindows(t) {
		if w.ID != panelNum && w.ID != arenaNum && w.Onscreen && isDragCardWindow(w) && insideRect(end, w.Frame) {
			previewNum = w.ID
		}
	}
	if previewNum == 0 {
		t.Error("a new drag after cancellation has no visible preview")
	}
	postMouse(t, float64(end.X), float64(end.Y), 2)
	held = false
	time.Sleep(400 * time.Millisecond)
	windows = ownSmokeWindows(t)
	var pin *pinItem
	onMain(func() {
		if len(a.pins) == 1 {
			pin = a.pins[0]
		}
	})
	t.Logf("released cursor=%+v livePins=%d windows=%+v", end, len(a.pins), windows)
	raw, err := os.ReadFile(filepath.Join(a.dataDir, layout.FileName))
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("saved layout.json=%s", raw)
	toolsDragShot(t, area, "drag-released")
	if pin == nil {
		t.Fatal("real panel drag-out did not create exactly one pin")
	}
	visiblePin := false
	var id int
	onMain(func() {
		if pin.win != nil {
			id = mac.WindowNumber(pin.win.NativeHandle())
		}
	})
	if id != previewNum {
		t.Error("release replaced the drag window instead of reusing it")
	}
	for _, w := range windows {
		if w.ID == id && w.Onscreen && w.Layer == 3 && insideRect(end, w.Frame) {
			visiblePin = true
		}
	}
	if !visiblePin {
		t.Error("the committed pin is not onscreen at the release point in the floating layer")
	}
	disk, err := layout.Load(a.dataDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(disk.Pins) != 1 || disk.Pins[0].ID != "ocr" {
		t.Fatalf("drag-out pin was not persisted: %+v", disk.Pins)
	}
}
