//go:build darwin

package main

// Real-window proof for the draggable island: with 刘海样式 = 灵动岛 on the
// built-in screen, a real HID press on the capsule drags it to the middle of
// the screen, the release saves it, a tap there opens the panel, and a drag
// into the screen's bottom-right corner stops where the capsule is still
// whole — the Dock's rows included — while the panel it opens is lifted to
// the visible frame's bottom edge. Screenshots land in /tmp/shelf-island-drag/;
// every mouse event is proved to land on a window of this process inside a
// test-owned backdrop over the whole screen, never over another app. Run with
// SHELF_BASKET_SMOKE=1.

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/ebitengine/purego"
	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"shelf/internal/mac"
	"shelf/internal/settings"
)

const islandDragShotDir = "/tmp/shelf-island-drag"

// islandDragShot captures a screen region of the test backdrop.
func islandDragShot(t *testing.T, r mygo.Rectangle, name string) string {
	t.Helper()
	if err := os.MkdirAll(islandDragShotDir, 0o755); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(islandDragShotDir, name+".png")
	cmd := exec.Command("screencapture", "-x", "-o", "-R",
		fmt.Sprintf("%d,%d,%d,%d", r.X, r.Y, r.Width, r.Height), out)
	if b, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("screencapture %s: %v: %s", name, err, b)
	}
	t.Logf("shot %s -> %s", name, out)
	return out
}

// islandDragWindowShot captures the resident window by CGWindowID.
func islandDragWindowShot(t *testing.T, a *shelf, name string) string {
	t.Helper()
	num := mac.WindowNumber(a.win.NativeHandle())
	if num == 0 {
		t.Fatalf("%s: window has no CGWindowID", name)
	}
	if err := os.MkdirAll(islandDragShotDir, 0o755); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(islandDragShotDir, name+".png")
	cmd := exec.Command("screencapture", "-x", "-o", fmt.Sprintf("-l%d", num), out)
	if b, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("screencapture %s: %v: %s", name, err, b)
	}
	t.Logf("shot %s -> %s (CGWindowID=%d, frame %+v)", name, out, num, a.desiredFrame())
	return out
}

// islandDragTicker runs the production 30 ms monitor for this test: the
// smoke harness never starts startMonitoring, and the drag is driven by it.
func islandDragTicker(t *testing.T, a *shelf) {
	t.Helper()
	stop := make(chan struct{})
	t.Cleanup(func() { close(stop) })
	go func() {
		tk := time.NewTicker(pointerInterval)
		defer tk.Stop()
		for {
			select {
			case <-stop:
				return
			case now := <-tk.C:
				mygo.RunOnMain(func() {
					if a.quitting {
						return
					}
					a.animate(now)
					a.monitorDrag(now)
					a.syncWindowFrame()
					a.syncPointerGate()
				})
			}
		}
	}()
}

// newIslandBackdrop covers a display's whole screen rectangle with a
// test-owned window. The island may now be dragged into the Dock's rows —
// below the visible frame — so a backdrop limited to the work area would
// refuse the very points these tests post. The window takes the shared
// overlay policy without the panel's cursor tracking area, and sits one
// level under the shelf's resting level: above the Dock (20) and under the
// overlay (25), so a posted event always lands on a window of this process.
func newIslandBackdrop(t *testing.T, b mygo.Rectangle) *mygo.Window {
	t.Helper()
	var w *mygo.Window
	onMain(func() {
		w = mygo.NewWindow(mygo.WindowOptions{Title: "ShelfU island test backdrop", Hidden: true,
			Width: b.Width, Height: b.Height, X: b.X, Y: b.Y, Frameless: true, Panel: true,
			SkipTaskbar: true, DisableShadow: true, Content: ui.View(func(c *ui.Context) {
				ui.Box(c).Fill().Background(ui.Hex("#253445"))
			})})
		h := w.NativeHandle()
		mac.ConfigureOverlayWindow(h)
		mac.SetWindowLevel(h, mac.StatusWindowLevel-1)
		mac.SetWindowFrame(h, b.X, b.Y, b.Width, b.Height)
		w.ShowInactive()
	})
	t.Cleanup(func() { onMain(w.Destroy) })
	time.Sleep(150 * time.Millisecond)
	return w
}

// requireIslandOwnWindow refuses to post an event WindowServer would hand to
// another process. Both the shelf overlay and the test backdrop belong to
// this process, so either is a safe target; the menu bar or a sibling build
// of ShelfU is not this test's to hit. Returns the window number.
func requireIslandOwnWindow(t *testing.T, x, y float64) int {
	t.Helper()
	p := mygo.Point{X: int(x), Y: int(y)}
	var hit int
	onMain(func() {
		primary := mygo.Screen.PrimaryDisplay()
		hit = mac.WindowAtPoint(x, y, float64(primary.Bounds.Y+primary.Bounds.Height))
	})
	for _, w := range ownSmokeWindows(t) {
		if w.ID == hit && w.Onscreen && insideRect(p, w.Frame) {
			return hit
		}
	}
	t.Fatalf("refusing mouse event at %+v: WindowServer routes it to window %d, which is not an onscreen window of this test", p, hit)
	return 0
}

// requireIslandOverlayPress proves the press reaches the shelf's own window:
// the pointer gate must have accepted the shell at p, or the press would hit
// the backdrop and move nothing.
func requireIslandOverlayPress(t *testing.T, a *shelf, p mygo.Point) {
	t.Helper()
	var id int
	onMain(func() { id = mac.WindowNumber(a.win.NativeHandle()) })
	if hit := requireIslandOwnWindow(t, float64(p.X), float64(p.Y)); hit != id {
		t.Fatalf("a press at %+v reaches window %d, not the shelf overlay %d: the pointer gate has not accepted the shell there", p, hit, id)
	}
}

// postIslandMouse posts one real HID mouse event per type at a screen point,
// the same CGEvent path a physical mouse takes. Unlike postMouse it does not
// require the work area — the island now legitimately rests in the Dock's
// rows — and it proves before every event that WindowServer routes the point
// to a window of this test process. It never warps the pointer after
// posting: a warp would hide where the posted events actually landed.
// events are CGEvent types (5 moved, 1 leftDown, 2 leftUp, 6 leftDragged,
// 3 rightDown, 4 rightUp).
func postIslandMouse(t *testing.T, x, y float64, events ...uint32) {
	t.Helper()
	lib, err := purego.Dlopen("/System/Library/Frameworks/CoreGraphics.framework/CoreGraphics", purego.RTLD_LAZY)
	if err != nil {
		t.Fatalf("CoreGraphics: %v", err)
	}
	var (
		create  func(source uintptr, typ uint32, p mac.CGPoint, button uint32) uintptr
		post    func(tap uint32, ev uintptr)
		release func(cf uintptr)
	)
	purego.RegisterLibFunc(&create, lib, "CGEventCreateMouseEvent")
	purego.RegisterLibFunc(&post, lib, "CGEventPost")
	libCF, err := purego.Dlopen("/System/Library/Frameworks/CoreFoundation.framework/CoreFoundation", purego.RTLD_LAZY)
	if err != nil {
		t.Fatalf("CoreFoundation: %v", err)
	}
	purego.RegisterLibFunc(&release, libCF, "CFRelease")
	button := uint32(0)
	for _, typ := range events {
		if typ == 3 || typ == 4 || typ == 27 {
			button = 1
		}
		requireIslandOwnWindow(t, x, y)
		ev := create(0, typ, mac.CGPoint{X: x, Y: y}, button)
		if ev == 0 {
			t.Fatal("CGEventCreateMouseEvent returned nil")
		}
		post(0, ev)
		release(ev)
		time.Sleep(30 * time.Millisecond)
	}
}

// islandButton is a left button this test holds. Its cleanup releases the
// button at the last point the own-window gate accepted, so an assertion
// aborted mid-drag can never leave the user's mouse button down.
type islandButton struct {
	t    *testing.T
	at   mygo.Point
	down bool
}

// press posts a real leftDown and arms the failure release.
func (b *islandButton) press(x, y float64) {
	b.t.Helper()
	postIslandMouse(b.t, x, y, 1)
	b.at, b.down = mygo.Point{X: int(x), Y: int(y)}, true
	b.t.Cleanup(func() {
		if b.down {
			b.down = false
			postIslandMouse(b.t, float64(b.at.X), float64(b.at.Y), 2)
		}
	})
}

// drag posts a real leftDragged and keeps the failure release there.
func (b *islandButton) drag(x, y float64) {
	b.t.Helper()
	postIslandMouse(b.t, x, y, 6)
	b.at = mygo.Point{X: int(x), Y: int(y)}
}

// release posts the real leftUp and disarms the failure release.
func (b *islandButton) release(x, y float64) {
	b.t.Helper()
	postIslandMouse(b.t, x, y, 2)
	b.down = false
}

// tapShelfSmoke taps the shelf at p with real events: a move onto it, then a
// press and a release that travel nowhere. On the closed capsule that opens
// the panel; on the band's page icon it switches the page.
func tapShelfSmoke(t *testing.T, a *shelf, p mygo.Point) {
	t.Helper()
	postIslandMouse(t, float64(p.X), float64(p.Y), 5)
	time.Sleep(250 * time.Millisecond)
	requireIslandOverlayPress(t, a, p)
	btn := &islandButton{t: t}
	btn.press(float64(p.X), float64(p.Y))
	btn.release(float64(p.X), float64(p.Y))
	time.Sleep(700 * time.Millisecond)
}

// dragIslandSmoke grips the closed capsule at from's centre, drags it to the
// screen point (x, y) in steps the way a hand moves, and releases. It leaves
// the pointer at the target.
func dragIslandSmoke(t *testing.T, a *shelf, from mygo.Rectangle, x, y int) {
	t.Helper()
	cx, cy := float64(from.X+from.Width/2), float64(from.Y+from.Height/2)
	// A real move puts the pointer on the capsule, so the hover gate has
	// accepted the shell before the press.
	postIslandMouse(t, cx, cy, 5)
	// Establish the hardware position before the down observer reads it;
	// never warp after a dragged event, whose response is what we measure.
	warpPointerTo(t, cx, cy)
	time.Sleep(250 * time.Millisecond)
	requireIslandOverlayPress(t, a, mygo.Point{X: int(cx), Y: int(cy)})
	btn := &islandButton{t: t}
	btn.press(cx, cy)
	var armed bool
	onMain(func() { armed = a.islandPress.active })
	if !armed {
		t.Fatal("the real press did not arm an island gesture")
	}
	tx, ty := float64(x), float64(y)
	const steps = 10
	for i := 1; i <= steps; i++ {
		btn.drag(cx+(tx-cx)*float64(i)/steps, cy+(ty-cy)*float64(i)/steps)
		time.Sleep(70 * time.Millisecond)
	}
	btn.release(tx, ty)
	time.Sleep(300 * time.Millisecond)
}

// TestIslandDragSmokeShots drives the drag with real mouse events and
// captures the four states. The capsule now goes anywhere on the display,
// the Dock's rows included; the panel it opens stays whole inside the
// visible frame, lifting off the island when the Dock covers the rows the
// island rests in.
func TestIslandDragSmokeShots(t *testing.T) {
	if os.Getenv("SHELF_BASKET_SMOKE") != "1" {
		t.Skip("set SHELF_BASKET_SMOKE=1 to run the native window smoke test")
	}
	a := redesignShelf(t)
	b := mygo.Screen.PrimaryDisplay().Bounds
	work := mygo.Screen.PrimaryDisplay().WorkArea
	if work.Y+work.Height >= b.Y+b.Height {
		t.Skip("this screen reserves no bottom Dock band: the island has no row below the visible frame to prove")
	}
	// The backdrop is the whole screen, not its visible frame: the drag now
	// legally ends in the Dock's rows, where the work-area backdrop used to
	// refuse every point.
	newIslandBackdrop(t, b)
	t.Cleanup(func() {
		onMain(func() {
			next := a.settings
			next.ShellStyle = settings.ShellStyleNotch
			next.IslandPos = nil
			a.applySettings(next)
			a.applyShellStyle()
			a.place(mygo.Point{X: b.X + b.Width/2, Y: b.Y + 1})
			a.visible, a.progress, a.animationFrom = false, 0, 0
			a.peeking, a.peekProgress = false, 0
			a.syncMousePassThrough()
			a.win.Invalidate()
		})
	})

	// 灵动岛 on the built-in screen, resting on its default row with no
	// saved place from an earlier run.
	islandStyle(t, a, settings.ShellStyleIsland)
	if !a.island || a.notchW <= 0 || a.notchH <= 0 {
		t.Skip("this screen has no measured camera housing: the island's hover gate cannot be driven")
	}
	onMain(func() {
		a.settings.IslandPos = nil
		a.placeIslandAxis()
		a.syncWindowFrame()
		a.win.ShowInactive()
		a.win.Invalidate()
	})
	islandDragTicker(t, a)
	var stopObs func()
	onMain(func() {
		stopObs = mac.ObserveMouseDown(a.win.NativeHandle(), func(inside bool, button int) {
			a.mouseDown(inside, button, mygo.Screen.CursorScreenPoint())
		})
	})
	t.Cleanup(func() { onMain(stopObs) })
	time.Sleep(300 * time.Millisecond)

	var start, spot0 mygo.Rectangle
	onMain(func() { start = a.islandRect(); spot0 = start })
	t.Logf("resting capsule %+v", start)
	islandDragShot(t, b, "1-before")
	islandDragWindowShot(t, a, "1-before-window")

	// Press on the capsule and drag it to the lower middle of the screen in
	// steps, the way a hand moves: every step is a real left-dragged event.
	tx, ty := b.X+b.Width*2/3, b.Y+b.Height*2/3
	dragIslandSmoke(t, a, start, tx, ty)
	// Park the pointer beside the island so the shot is the resting
	// capsule, not the hover pill.
	postIslandMouse(t, float64(b.X+40), float64(b.Y+b.Height/2), 5)
	time.Sleep(300 * time.Millisecond)

	var moved mygo.Rectangle
	onMain(func() { moved = a.islandRect() })
	if moved.Y <= spot0.Y+100 {
		t.Fatalf("the drag did not move the island: %+v -> %+v", spot0, moved)
	}
	// The capsule follows the pointer's centre (the press grabbed the
	// middle of the capsule) and stays whole on the display's own box.
	if moved.X+moved.Width/2 != tx || moved.Y != ty-islandIdleH/2 {
		t.Fatalf("the capsule is %+v, want centre %d and top %d", moved, tx, ty-islandIdleH/2)
	}
	if moved.X < b.X || moved.X+moved.Width > b.X+b.Width ||
		moved.Y < b.Y+a.islandTop || moved.Y+moved.Height > b.Y+b.Height {
		t.Fatalf("the dragged island %+v is not whole on the screen %+v", moved, b)
	}
	saved, err := settings.Load(a.dataDir)
	if err != nil {
		t.Fatal(err)
	}
	key := islandSpotKey(a.dispID)
	wantSpot := settings.IslandPos{X: tx - b.X, Y: ty - islandIdleH/2 - b.Y}
	if got, ok := saved.IslandPos[key]; !ok || got != wantSpot {
		t.Fatalf("the release saved %+v, want %+v (capsule %+v)", saved.IslandPos, wantSpot, moved)
	}
	t.Logf("dragged capsule %+v spot %+v", moved, wantSpot)
	islandDragShot(t, b, "2-after")
	islandDragWindowShot(t, a, "2-after-window")

	// A tap there opens the panel, in the new place.
	tapShelfSmoke(t, a, mygo.Point{X: moved.X + moved.Width/2, Y: moved.Y + moved.Height/2})
	var visible bool
	var open mygo.Rectangle
	var panel mygo.Rectangle
	var place panelPlacement
	onMain(func() {
		visible = a.visible
		open, panel, place = a.islandRect(), a.desiredFrame(), a.panelPlacement()
	})
	if !visible {
		t.Fatal("a tap on the moved island did not open the panel")
	}
	if open != moved {
		t.Fatalf("opening the panel moved the capsule: %+v -> %+v", moved, open)
	}
	// The panel hangs below the island while the level's own box fits there
	// and grows above it when it does not; either way the island's place
	// decides, never the visible frame the panel itself must fit.
	panelBottom := open.Y + place.H
	if (panelBottom > work.Y+work.Height) != place.Up {
		t.Fatalf("the panel %+v grew up=%v with its bottom at %d in the work area %+v", place, place.Up, panelBottom, work)
	}
	if place.Up {
		if want := min(open.Y+open.Height, work.Y+work.Height); place.Y+place.H != want {
			t.Fatalf("the upward panel's bottom %d, want %d (the island's bottom, or the visible frame's)", place.Y+place.H, want)
		}
	} else if place.Y != open.Y {
		t.Fatalf("the downward panel starts at %d, not on the island's top edge %d", place.Y, open.Y)
	}
	if place.X < work.X || place.X+place.W > work.X+work.Width ||
		place.Y < work.Y || place.Y+place.H > work.Y+work.Height {
		t.Fatalf("the panel %+v leaves the visible frame %+v", place, work)
	}
	if panel.X > place.X || panel.X+panel.Width < place.X+place.W ||
		panel.Y > place.Y || panel.Y+panel.Height < place.Y+place.H {
		t.Fatalf("the window frame %+v does not cover the panel %+v", panel, place)
	}
	t.Logf("panel at %+v inside the window %+v", place, panel)
	islandDragShot(t, b, "3-panel")
	islandDragWindowShot(t, a, "3-panel-window")

	// Fold it away, then drag the island into the screen's bottom-right
	// corner: the capsule stops at its lowest row on the screen itself.
	onMain(func() {
		a.hide(hidePassive)
		a.visible, a.progress, a.animationFrom = false, 0, 0
		a.peeking, a.peekProgress = false, 0
		a.syncMousePassThrough()
		a.win.Invalidate()
	})
	time.Sleep(400 * time.Millisecond)
	dragIslandSmoke(t, a, open, b.X+b.Width-20, b.Y+b.Height-20)
	postIslandMouse(t, float64(b.X+40), float64(b.Y+b.Height/2), 5)
	time.Sleep(300 * time.Millisecond)

	// The island goes to the corner itself; the display's box, not the
	// visible frame, is what stops it.
	var clamped mygo.Rectangle
	var clampedSpot islandSpot
	onMain(func() { clamped, clampedSpot = a.islandRect(), a.islandSpotNow() })
	wantTop, wantCentre := b.Y+b.Height-islandHoverH, b.X+b.Width-islandIdleW/2
	if clamped.Y != wantTop {
		t.Fatalf("the island stopped at top %d, want %d (its hover row on the screen)", clamped.Y, wantTop)
	}
	if clamped.X+clamped.Width/2 != wantCentre {
		t.Fatalf("the island stopped at centre %d, want %d (its capsule whole on the screen)", clamped.X+clamped.Width/2, wantCentre)
	}
	if clampedSpot.Top != wantTop-b.Y || clampedSpot.Centre != wantCentre-b.X {
		t.Fatalf("the saved spot is %+v, want centre %d and top %d", clampedSpot, wantCentre-b.X, wantTop-b.Y)
	}
	cornerSaved, err := settings.Load(a.dataDir)
	if err != nil {
		t.Fatal(err)
	}
	if got, ok := cornerSaved.IslandPos[key]; !ok {
		t.Fatalf("the corner release saved no place: %+v", cornerSaved.IslandPos)
	} else if want := (settings.IslandPos{X: wantCentre - b.X, Y: wantTop - b.Y}); got != want {
		t.Fatalf("the corner release saved %+v, want %+v", got, want)
	}
	islandBottom, workBottom := clamped.Y+clamped.Height, work.Y+work.Height
	if islandBottom <= workBottom {
		t.Fatalf("the capsule's bottom %d is not below the visible frame's %d: this screen has no Dock band", islandBottom, workBottom)
	}

	// The panel keeps its whole box by lifting its bottom to the visible
	// frame's edge, off the island it can no longer reach.
	var corner panelPlacement
	onMain(func() { corner = a.panelPlacement() })
	if !corner.Up {
		t.Fatalf("the panel at the bottom-right corner did not grow upward: %+v", corner)
	}
	if corner.X < work.X || corner.X+corner.W > work.X+work.Width ||
		corner.Y < work.Y || corner.Y+corner.H > work.Y+work.Height {
		t.Fatalf("the panel %+v leaves the visible frame %+v", corner, work)
	}
	if corner.Y+corner.H != workBottom {
		t.Fatalf("the upward panel's bottom %d is not on the visible frame's %d", corner.Y+corner.H, workBottom)
	}
	if corner.Y+corner.H == islandBottom {
		t.Fatalf("the panel still ends on the island's bottom %d, %d pt inside the Dock band", islandBottom, islandBottom-workBottom)
	}
	t.Logf("clamped capsule %+v spot %+v panel %+v (%d pt below the visible frame)", clamped, clampedSpot, corner, islandBottom-workBottom)
	islandDragShot(t, b, "4-clamped")
	islandDragWindowShot(t, a, "4-clamped-window")
}
