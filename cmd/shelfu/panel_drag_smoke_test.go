//go:build darwin

package main

// Real-window proof for the draggable open panel: with 刘海样式 = 灵动岛 on
// the built-in screen, the panel is opened from the capsule and then dragged
// by a blank stretch of its band — first to the middle of the right side,
// then to the bottom-right corner. Every press is a real HID event proved to
// land on a window of this process inside a test-owned backdrop over the
// whole screen; no event is posted over another app. After each release the
// panel is whole inside the visible frame, its band icons still click, and a
// point 2pt outside its box reaches the backdrop instead of the shelf.
// Screenshots land in /tmp/shelf-task-isldrag/. Run with
// SHELF_BASKET_SMOKE=1.

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/egoist/mygo"

	"shelf/internal/mac"
	"shelf/internal/settings"
)

const panelDragShotDir = "/tmp/shelf-task-isldrag"

// panelDragShot captures a screen region for the evidence report.
func panelDragShot(t *testing.T, r mygo.Rectangle, name string) string {
	t.Helper()
	if err := os.MkdirAll(panelDragShotDir, 0o755); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(panelDragShotDir, name+".png")
	cmd := exec.Command("screencapture", "-x", "-o", "-R",
		fmt.Sprintf("%d,%d,%d,%d", r.X, r.Y, r.Width, r.Height), out)
	if b, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("screencapture %s: %v: %s", name, err, b)
	}
	t.Logf("shot %s -> %s", name, out)
	return out
}

// panelDragSmoke drags the open panel by a band point. It parks the pointer
// on the band, proves the press reaches the shelf overlay, posts the press
// and the move steps, lets the caller shoot the frame in flight, then
// releases on the target. Every event is a real HID event inside the test
// backdrop.
func panelDragSmoke(t *testing.T, a *shelf, from, to mygo.Point, during func()) {
	t.Helper()
	postIslandMouse(t, float64(from.X), float64(from.Y), 5)
	warpPointerTo(t, float64(from.X), float64(from.Y))
	time.Sleep(250 * time.Millisecond)
	requireIslandOverlayPress(t, a, from)
	btn := &islandButton{t: t}
	btn.press(float64(from.X), float64(from.Y))
	var armed, blank bool
	onMain(func() {
		armed = a.islandPress.active && a.islandPress.panel
		blank = a.bandBlank(from)
	})
	if !blank {
		t.Fatalf("the press at %+v is not on a blank stretch of the band", from)
	}
	if !armed {
		t.Fatal("the real press did not arm a panel move")
	}
	const steps = 12
	for i := 1; i <= steps; i++ {
		x := float64(from.X) + float64(to.X-from.X)*float64(i)/steps
		y := float64(from.Y) + float64(to.Y-from.Y)*float64(i)/steps
		btn.drag(x, y)
		time.Sleep(60 * time.Millisecond)
		if i == steps/2 && during != nil {
			during()
		}
	}
	btn.release(float64(to.X), float64(to.Y))
	// The side change, if the new place calls for one, runs on the panel's
	// spring; give it the whole flight plus the settle.
	time.Sleep(700 * time.Millisecond)
}

// requirePanelInWork fails unless the panel's whole box sits in the visible
// frame, the island is whole on the display, and the window covers the box
// it draws.
func requirePanelInWork(t *testing.T, a *shelf, where string) panelPlacement {
	t.Helper()
	var place panelPlacement
	var island, frame, bounds, work mygo.Rectangle
	onMain(func() {
		place, island, frame = a.panelPlacement(), a.islandRect(), a.desiredFrame()
		bounds, work = a.dispBounds, a.workArea()
	})
	if place.X < work.X || place.X+place.W > work.X+work.Width ||
		place.Y < work.Y || place.Y+place.H > work.Y+work.Height {
		t.Fatalf("%s: the panel %+v leaves the visible frame %+v", where, place, work)
	}
	if island.X < bounds.X || island.X+island.Width > bounds.X+bounds.Width ||
		island.Y < bounds.Y || island.Y+island.Height > bounds.Y+bounds.Height {
		t.Fatalf("%s: the island %+v leaves the display %+v", where, island, bounds)
	}
	if frame.X > place.X || frame.X+frame.Width < place.X+place.W ||
		frame.Y > place.Y || frame.Y+frame.Height < place.Y+place.H {
		t.Fatalf("%s: the window frame %+v does not cover the panel %+v", where, frame, place)
	}
	return place
}

// TestIslandPanelDragSmoke drives the open panel with real mouse events and
// captures the two places: while held and after the release.
func TestIslandPanelDragSmoke(t *testing.T) {
	if os.Getenv("SHELF_BASKET_SMOKE") != "1" {
		t.Skip("set SHELF_BASKET_SMOKE=1 to run the native window smoke test")
	}
	a := redesignShelf(t)
	b := mygo.Screen.PrimaryDisplay().Bounds
	backdrop := newIslandBackdrop(t, b)
	var backdropID int
	onMain(func() { backdropID = mac.WindowNumber(backdrop.NativeHandle()) })
	t.Cleanup(func() {
		onMain(func() {
			a.hide(hidePassive)
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

	islandStyle(t, a, settings.ShellStyleIsland)
	if !a.island || a.notchW <= 0 || a.notchH <= 0 {
		t.Skip("this screen has no measured camera housing: the island's gate cannot be driven")
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

	// The panel opens from a tap on the resting capsule: the overview.
	var capsule mygo.Rectangle
	onMain(func() { capsule = a.islandRect() })
	tapShelfSmoke(t, a, mygo.Point{X: capsule.X + capsule.Width/2, Y: capsule.Y + capsule.Height/2})
	var visible bool
	var place panelPlacement
	onMain(func() { visible, place = a.visible, a.panelPlacement() })
	if !visible {
		t.Fatal("the tap did not open the panel")
	}
	t.Logf("panel opened at %+v", place)

	// From the band's blank middle to the middle of the right side.
	from := mygo.Point{X: place.X + place.W/2, Y: place.Y + bandH/2}
	to := mygo.Point{X: b.X + b.Width - 320, Y: b.Y + b.Height/2}
	panelDragSmoke(t, a, from, to, func() {
		panelDragShot(t, b, "panel-drag-1-mid")
	})
	first := requirePanelInWork(t, a, "right-middle")
	panelDragShot(t, b, "panel-drag-1-released")
	t.Logf("panel after the first drag: %+v", first)

	// From the band's new place to the bottom-right corner.
	from = mygo.Point{X: first.X + first.W/2, Y: first.Y + bandH/2}
	to = mygo.Point{X: b.X + b.Width - 40, Y: b.Y + b.Height - 60}
	panelDragSmoke(t, a, from, to, func() {
		panelDragShot(t, b, "panel-drag-2-mid")
	})
	second := requirePanelInWork(t, a, "bottom-right")
	panelDragShot(t, b, "panel-drag-2-released")
	t.Logf("panel after the second drag: %+v", second)
	if second == first {
		t.Fatalf("the second drag did not move the panel: %+v", second)
	}

	// The band's page icon still clicks after the move: the second seat
	// switches to the tray as a page.
	var icon mygo.Point
	var wantTab pageID
	var hits int
	onMain(func() {
		hits = len(a.bandHits)
		if hits >= 2 {
			r := a.bandHits[1]
			icon = mygo.Point{X: a.panelPlacement().X + r.X + r.Width/2, Y: a.panelPlacement().Y + r.Y + r.Height/2}
			wantTab = a.tabPages()[1].id()
		}
	})
	if hits < 2 {
		t.Fatalf("the band recorded %d controls, want at least 2", hits)
	}
	tapShelfSmoke(t, a, icon)
	var tab pageID
	var level int
	onMain(func() { tab, level = a.tab, a.currentLevel() })
	if tab != wantTab || level != 2 {
		t.Fatalf("the band icon opened tab %s at level %d, want %s at level 2", tab, level, wantTab)
	}
	panelDragShot(t, b, "panel-drag-3-icon-page")

	// Two points outside the panel's box reach the backdrop: the shell's
	// transparent margin and the window around it take no clicks. The gate
	// answers per pointer, so each point is visited first.
	var p placeProbe
	onMain(func() { p = placeProbe{place: a.panelPlacement(), shelf: mac.WindowNumber(a.win.NativeHandle())} })
	for _, q := range []mygo.Point{
		{X: p.place.X - 2, Y: p.place.Y + bandH/2},
		{X: p.place.X + p.place.W/2, Y: p.place.Y + p.place.H + 2},
	} {
		postIslandMouse(t, float64(q.X), float64(q.Y), 5)
		time.Sleep(120 * time.Millisecond)
		var hit int
		onMain(func() {
			hit = mac.WindowAtPoint(float64(q.X), float64(q.Y), float64(b.Y+b.Height))
		})
		if hit == p.shelf {
			t.Fatalf("the shelf takes the %+v point, 2pt outside its panel %+v", q, p.place)
		}
		if hit != backdropID {
			t.Fatalf("the %+v point reaches window %d, want the test backdrop %d", q, hit, backdropID)
		}
	}
}

// placeProbe is what the outside-click check reads on the main thread.
type placeProbe struct {
	place panelPlacement
	shelf int
}
