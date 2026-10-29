//go:build darwin

package main

import (
	"fmt"
	"math"
	"testing"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"shelf/internal/settings"
)

// TestPanelSpotGridKeepsTheSheetOnScreen sweeps the island over the whole
// box of five displays — the built-in whose visible frame starts at (0,25),
// the same screen with a Dock taking the bottom rows and with a Dock on its
// left, a second display to its right and one to its left and above — and
// checks every placement of both panel sizes: the box stays inside the
// visible frame, takes the side it has room for, keeps attached to the
// island's top (down) or bottom (up, or to the work area's edge when the
// Dock lifts it off the island) edge, and keeps its clamped axis.
func TestPanelSpotGridKeepsTheSheetOnScreen(t *testing.T) {
	displays := []struct {
		name   string
		bounds mygo.Rectangle
		work   mygo.Rectangle
	}{
		{"built-in", mygo.Rectangle{X: 0, Y: 0, Width: 1470, Height: 956},
			mygo.Rectangle{X: 0, Y: 25, Width: 1470, Height: 931}},
		{"built-in with a Dock", mygo.Rectangle{X: 0, Y: 0, Width: 1470, Height: 956},
			mygo.Rectangle{X: 0, Y: 25, Width: 1470, Height: 870}},
		{"built-in with a Dock on the left", mygo.Rectangle{X: 0, Y: 0, Width: 1470, Height: 956},
			mygo.Rectangle{X: 90, Y: 25, Width: 1380, Height: 931}},
		{"external to the right", mygo.Rectangle{X: 1470, Y: 0, Width: 2560, Height: 1440},
			mygo.Rectangle{X: 1470, Y: 25, Width: 2560, Height: 1415}},
		{"external left and above", mygo.Rectangle{X: -1920, Y: -120, Width: 1920, Height: 1080},
			mygo.Rectangle{X: -1920, Y: -120, Width: 1920, Height: 1080}},
	}
	// axis steps across [lo,hi], both ends included.
	axis := func(lo, hi int) []int {
		step := max((hi-lo)/10, 1)
		out := []int{lo}
		for v := lo + step; v < hi; v += step {
			out = append(out, v)
		}
		return append(out, hi)
	}
	for _, d := range displays {
		bounds, work := d.bounds, d.work
		// The island's own reach: whole on the display with its hover row,
		// its top never above the style's row. The visible frame does not
		// bound it.
		for _, centre := range axis(bounds.X+islandIdleW/2, bounds.X+bounds.Width-islandIdleW/2) {
			for _, top := range axis(bounds.Y+islandTopGap, bounds.Y+bounds.Height-islandHoverH) {
				for _, size := range []struct {
					name string
					w, h int
				}{
					{"sheet", sheetW, sheetH},
					{"overview", overviewW, overviewH},
				} {
					p := panelSpot(centre, top, islandIdleH, work, size.w, size.h)
					where := fmt.Sprintf("%s %s at %d,%d", d.name, size.name, centre, top)
					if p.X < work.X || p.X+p.W > work.X+work.Width {
						t.Errorf("%s: panel X %d..%d leaves the work area %+v", where, p.X, p.X+p.W, work)
					}
					// A downward panel hangs on the island's own row, which
					// may sit above the menu bar's 25 (the legacy default
					// row starts at 7); every other top is the work area's.
					aboveWork := !p.Up && p.Y == top
					if (p.Y < work.Y && !aboveWork) || p.Y+p.H > work.Y+work.Height {
						t.Errorf("%s: panel Y %d..%d leaves the work area %+v", where, p.Y, p.Y+p.H, work)
					}
					wantUp := top+size.h > work.Y+work.Height
					if p.Up != wantUp {
						t.Errorf("%s: up=%v, want %v for a %dpt panel", where, p.Up, wantUp, size.h)
					}
					if want := min(max(centre, work.X+p.W/2), work.X+work.Width-p.W/2); p.X+p.W/2 != want {
						t.Errorf("%s: panel centre %d, want %d", where, p.X+p.W/2, want)
					}
					if p.Up {
						// The bottom sits on the island's, or on the work
						// area's edge when the Dock forced the panel up.
						want := min(top+islandIdleH, work.Y+work.Height)
						if bottom := p.Y + p.H; bottom != want && p.Y != work.Y {
							t.Errorf("%s: upward panel bottom %d, want %d", where, bottom, want)
						}
					} else if p.Y != top {
						t.Errorf("%s: downward panel top %d, want the island's %d", where, p.Y, top)
					}
					// The panel never floats away from the island's column.
					if p.X >= centre+islandIdleW/2 || p.X+p.W <= centre-islandIdleW/2 {
						t.Errorf("%s: panel %d..%d is off the island's column", where, p.X, p.X+p.W)
					}
				}
			}
		}
	}
	// The default placement keeps the panel exactly where it was: below the
	// island, on its axis, even though the visible frame starts 25pt down.
	builtin := displays[0].work
	if p := panelSpot(735, 38, islandIdleH, builtin, sheetW, sheetH); p.Up || p.X != 735-sheetW/2 || p.Y != 38 {
		t.Fatalf("default placement = %+v, want the anchored downward box", p)
	}
	// The island's resting row keeps its own 7pt gap, not the menu bar's 25.
	if p := panelSpot(735, islandTopGap, islandIdleH, builtin, sheetW, sheetH); p.Up || p.Y != islandTopGap {
		t.Fatalf("resting-row placement = %+v, want the panel under the island's row %d", p, islandTopGap)
	}
	// An island the Dock covers keeps its panel whole: the box is lifted to
	// the work area's bottom edge, off the island it grew from.
	dock, screen := displays[1].work, displays[1].bounds
	low := screen.Y + screen.Height - islandHoverH // the island's lowest reachable row
	p := panelSpot(735, low, islandIdleH, dock, sheetW, sheetH)
	if !p.Up || p.Y+p.H != dock.Y+dock.Height {
		t.Fatalf("panel for an island in the Dock = %+v, want it bottom-aligned to the work area %+v", p, dock)
	}
	if low+islandIdleH <= dock.Y+dock.Height {
		t.Fatalf("the Dock case did not put the island below the work area: row %d", low)
	}
}

// TestUpwardPanelCoversTheIslandAndTakesItsClicks: with the island dragged
// low, the window covers the panel above it and the island itself, the
// panel's body takes clicks, its transparent margin does not, and a press
// delivered to another app on the panel's upper half no longer counts as a
// press outside.
func TestUpwardPanelCoversTheIslandAndTakesItsClicks(t *testing.T) {
	syntheticDisplays(t, false, false)
	a := islandShelf(false, settings.ShellStyleIsland)
	a.dataDir = t.TempDir()
	a.settings.IslandPos = map[string]settings.IslandPos{islandSpotKey(1): {X: 1400, Y: 850}}
	a.place(mygo.Point{X: 735, Y: 38})
	a.visible, a.level, a.progress = true, 2, 1
	a.fx.w, a.fx.h = a.panelBox()
	a.fx.openK = 1
	place := a.panelPlacement()
	if !place.Up {
		t.Fatalf("the panel at the screen's right edge did not grow upward: %+v", place)
	}
	// Clamped into the work area, so off the island's axis.
	if place.X != a.dispWork.X+a.dispWork.Width-sheetW {
		t.Fatalf("panel X = %d, want it clamped to the work area's right edge", place.X)
	}
	if got := a.panelDropRect(); got != (mygo.Rectangle{X: place.X, Y: place.Y, Width: sheetW, Height: sheetH}) {
		t.Fatalf("drop box = %+v, want %+v", got, place)
	}
	// The window starts at the panel's top and reaches under the island.
	f := a.desiredFrame()
	if f.Y != place.Y {
		t.Fatalf("window frame %+v does not start at the panel %+v", f, place)
	}
	if f.Y+f.Height < place.Y+place.H {
		t.Fatalf("window frame %+v clips the panel %+v", f, place)
	}
	// The panel's upper half is on the shell; a point just outside its
	// left edge, in the frame's transparent margin, is not.
	mid := mygo.Point{X: place.X + place.W/2, Y: place.Y + 4}
	if !a.overShell(mid) {
		t.Fatalf("the panel's upper half %+v is not on the shell %+v", mid, place)
	}
	if a.overShell(mygo.Point{X: place.X - 3, Y: mid.Y}) {
		t.Fatal("the frame's transparent margin beside the panel takes the shell's clicks")
	}
	// The island below is still the shell's own box.
	r := a.islandRect()
	if !a.overShell(mygo.Point{X: r.X + r.Width/2, Y: r.Y + r.Height/2}) {
		t.Fatalf("the island %+v is no longer on the shell", r)
	}
	// A press on the panel's upper half, delivered to another app, keeps
	// the panel open; a press well away folds it.
	a.mouseDown(false, 0, mid)
	if !a.visible {
		t.Fatal("a press on the upward panel's upper half closed the panel")
	}
	a.mouseDown(false, 0, mygo.Point{X: a.dispWork.X + 200, Y: a.dispWork.Y + 300})
	if a.visible {
		t.Fatal("a press away from the panel did not fold it")
	}
}

// TestDefaultUpwardFlagKeepsTheOldFrame: an island on its default row still
// opens the panel downward, on the same frame the geometry tests pin.
func TestDefaultUpwardFlagKeepsTheOldFrame(t *testing.T) {
	syntheticDisplays(t, false, false)
	a := islandShelf(false, settings.ShellStyleIsland)
	a.dataDir = t.TempDir()
	a.place(mygo.Point{X: 735, Y: 38})
	a.visible, a.level = true, 2
	a.fx.w, a.fx.h = a.panelBox()
	if p := a.panelPlacement(); p.Up || p.Y != a.y+a.islandTop || p.X != a.stripCenter()-sheetW/2 {
		t.Fatalf("default placement = %+v, want the anchored downward box", p)
	}
	f := a.desiredFrame()
	if f.Y != a.y || f.Height != a.islandTop+sheetH {
		t.Fatalf("default frame = %+v, want Y=%d height %d", f, a.y, a.islandTop+sheetH)
	}
}

// TestUpwardPanelBandTakesClicksAtTheClampedAxis: the band sits at the
// panel's top, which an upward panel at a side edge moves off the island's
// axis. The shifted content must take clicks where it is drawn, not at the
// island's centre.
func TestUpwardPanelBandTakesClicksAtTheClampedAxis(t *testing.T) {
	a, _ := levelShelf(t)
	a.dispBounds = mygo.Rectangle{X: 0, Y: 0, Width: 1470, Height: 956}
	a.dispWork = mygo.Rectangle{X: 0, Y: 25, Width: 1470, Height: 931}
	a.hasNotch, a.notchX, a.notchY, a.notchW, a.notchH = true, 645, 0, 180, 32
	a.island, a.islandTop = true, 7
	a.y = 843 // the island's top row: 843 + 7
	a.islandDX = 1400 - 735
	a.settings.ShellStyle = settings.ShellStyleIsland
	a.visible, a.level, a.tab = true, 2, pageTray
	a.fx.w, a.fx.h = a.panelBox()
	a.fx.openK = 1
	place := a.panelPlacement()
	if !place.Up || place.X+place.W != a.dispWork.X+a.dispWork.Width {
		t.Fatalf("the panel did not clamp upward at the right edge: %+v", place)
	}
	f := a.desiredFrame()
	view := ui.NewTester(a.view, f.Width, f.Height)
	view.Frame()
	r, ok := view.Find("剪贴板")
	if !ok {
		t.Fatalf("the band is missing; texts %v", view.Texts())
	}
	// The band's own box starts at the panel's left edge in the canvas,
	// which is the frame's centre plus the clamped shift.
	wantLeft := float32(f.Width)/2 + float32(place.X+place.W/2-a.stripCenter()) - sheetW/2
	if r.X < wantLeft || r.X > wantLeft+120 {
		t.Fatalf("the band icon at %+v is not at the panel's left edge %.1f", r, wantLeft)
	}
	if err := view.Click("剪贴板"); err != nil {
		t.Fatal(err)
	}
	if a.tab != pageClipboard {
		t.Fatalf("a click on the shifted band did not switch pages: tab=%s", a.tab)
	}
}

// TestClosingFrameHoldsTheDisplacedShell: a panel clamped off the island's
// axis folds back onto the island's line, so its shell is drawn off-centre
// for the whole close. The window must hold that side of the shell on every
// frame — a strip-centred frame of the same width would cut it.
func TestClosingFrameHoldsTheDisplacedShell(t *testing.T) {
	syntheticDisplays(t, false, false)
	a := islandShelf(false, settings.ShellStyleIsland)
	a.dataDir = t.TempDir()
	a.settings.IslandPos = map[string]settings.IslandPos{islandSpotKey(1): {X: 120, Y: 200}}
	a.place(mygo.Point{X: 735, Y: 38})
	place := a.panelPlacement()
	if place.Up || place.X != a.dispWork.X {
		t.Fatalf("the panel at the left edge did not clamp onto the work area: %+v", place)
	}
	if shift := place.X + place.W/2 - a.stripCenter(); shift <= 0 {
		t.Fatalf("the panel is not off the island's axis: %+v", place)
	}
	top := a.y + a.islandTop
	for _, k := range []float32{0.02, 0.25, 0.5, 0.75, 0.98} {
		a.visible = false
		a.progress = 0
		a.fx = shellMotion{
			w:     islandIdleW + (sheetW-islandIdleW)*k,
			h:     islandIdleH + (sheetH-islandIdleH)*k,
			openK: k,
		}
		f := a.desiredFrame()
		cx := a.shellCentreX(k)
		left, right := float32(cx)-a.fx.w/2, float32(cx)+a.fx.w/2
		if float32(f.X) > left || float32(f.X+f.Width) < right {
			t.Errorf("openK %.2f: frame X %d..%d clips the shell at %.0f..%.0f",
				k, f.X, f.X+f.Width, left, right)
		}
		if f.Y > top || f.Y+f.Height < top+int(a.fx.h+0.5) {
			t.Errorf("openK %.2f: frame Y %d..%d clips the shell %d..%d",
				k, f.Y, f.Y+f.Height, top, top+int(a.fx.h+0.5))
		}
		if f.Width < int(math.Ceil(float64(a.fx.w))) {
			t.Errorf("openK %.2f: frame %dpt wide is narrower than the shell's %.0f",
				k, f.Width, a.fx.w)
		}
	}
}

// TestUpwardFrameReachesOverTheDrawnShell: an upward shell hangs from the
// island's bottom edge, so a shell taller than the level's box — the open
// spring's overshoot, or a page still drawn while the overview folds back —
// reaches above the panel's own top. The window's top must follow it (and
// its own drawing must not land on a negative row) through every phase.
func TestUpwardFrameReachesOverTheDrawnShell(t *testing.T) {
	syntheticDisplays(t, false, false)
	a := islandShelf(false, settings.ShellStyleIsland)
	a.dataDir = t.TempDir()
	a.settings.IslandPos = map[string]settings.IslandPos{islandSpotKey(1): {X: 735, Y: 850}}
	a.place(mygo.Point{X: 735, Y: 38})
	bottom := a.anchorScreenTop() + islandIdleH
	for _, ph := range []struct {
		name    string
		visible bool
		level   int
		openK   float32
		levelK  float32
		h       float32
	}{
		{"opening out of the island", true, 2, 0.3, 1, 96},
		{"open spring overshoot", true, 2, 1, 1, 258},
		{"page folding to the overview", true, 1, 1, 1, 250},
		{"overview level mid-shrink", true, 1, 1, 0.5, 203},
		{"overview settled", true, 1, 1, 0, 156},
		{"closing onto the island", false, 2, 0.35, 1, 170},
	} {
		t.Run(ph.name, func(t *testing.T) {
			a.visible, a.level = ph.visible, ph.level
			a.progress = 0
			if ph.visible {
				a.progress = ph.openK
			}
			a.fx = shellMotion{w: sheetW, h: ph.h, openK: ph.openK, levelK: ph.levelK}
			place := a.panelPlacement()
			if !place.Up {
				t.Fatalf("the panel did not grow upward: %+v", place)
			}
			f := a.desiredFrame()
			shellTop := bottom - int(math.Ceil(float64(ph.h)))
			if f.Y > shellTop {
				t.Errorf("frame top %d clips the shell's top %d", f.Y, shellTop)
			}
			if f.Y+f.Height < bottom+2 {
				t.Errorf("frame bottom %d does not reach under the island %d", f.Y+f.Height, bottom)
			}
			if f.Y > place.Y || f.Y+f.Height < place.Y+place.H {
				t.Errorf("frame %+v clips the settled panel %+v", f, place)
			}
			// The drawing measures its top from the window: an upward
			// shell may never be drawn at a negative row.
			if local := a.shellTopLocal(ph.h, ph.openK); local < 0 {
				t.Errorf("the shell is drawn at local top %.1f", local)
			}
			// The hit shape follows the drawn shell exactly.
			if !a.overShell(mygo.Point{X: a.stripCenter(), Y: shellTop + 2}) {
				t.Errorf("the shell's own top row %d does not take the mouse", shellTop+2)
			}
			if ph.h > 40 && a.overShell(mygo.Point{X: a.stripCenter(), Y: shellTop - 1}) {
				t.Errorf("a row at %d above the shell top %d takes the mouse", shellTop-1, shellTop)
			}
		})
	}
}

// TestOpeningPanelTakesTheMouseAtItsSettledAxis: while the panel opens near
// a side edge, the shape it settles at is clamped away from the island's
// axis. A press inside that settled box must reach ShelfU even though the
// shell drawn so far is still on the island's axis.
func TestOpeningPanelTakesTheMouseAtItsSettledAxis(t *testing.T) {
	syntheticDisplays(t, false, false)
	a := islandShelf(false, settings.ShellStyleIsland)
	a.dataDir = t.TempDir()
	a.settings.IslandPos = map[string]settings.IslandPos{islandSpotKey(1): {X: 120, Y: 200}}
	a.place(mygo.Point{X: 735, Y: 38})
	a.visible, a.level, a.progress = true, 1, 0.15
	a.fx = shellMotion{w: 190, h: 70, openK: 0.15}
	a.winH = 1
	place := a.panelPlacement()
	if place.Up || place.X != a.dispWork.X {
		t.Fatalf("the overview panel did not clamp onto the work area: %+v", place)
	}
	// Inside the settled clamped panel, outside both the drawn shell and
	// the settled shape if that shape were tested on the opening axis.
	p := mygo.Point{X: place.X + place.W - 20, Y: place.Y + 60}
	if a.overShell(p) {
		t.Fatalf("the drawn shell already reaches %+v", p)
	}
	if a.overShellShapeAt(p, overviewW, overviewH, 0, 0, a.fx.openK) {
		t.Fatal("the settled shape was tested on the opening axis")
	}
	old := cursorPoint
	t.Cleanup(func() { cursorPoint = old })
	cursorPoint = func() mygo.Point { return p }
	if !a.overlayAcceptsMouse() {
		t.Fatalf("the settled panel's box %+v does not take the mouse while it opens", p)
	}
	// Beside the settled panel, in the frame's transparent margin, the
	// window still lets the press through.
	cursorPoint = func() mygo.Point { return mygo.Point{X: place.X - 6, Y: p.Y} }
	if a.overlayAcceptsMouse() {
		t.Fatal("the frame's transparent margin takes the mouse")
	}
}

// TestUpwardContentUnfoldsFromTheIslandEdge: an upward panel keeps its
// content block's bottom edge on the island's and only as tall as the
// level's own box. While the shell grows, only the rows at the island are
// uncovered, so the overview's words arrive before its band and the band is
// not drawn until the shell reaches the overview's 156. At rest the band and
// the overview are both on the shell, the band takes clicks, and a page's
// 250 block is shown whole with its band still clickable.
func TestUpwardContentUnfoldsFromTheIslandEdge(t *testing.T) {
	stubKeyboard(t) // layout clicks must never register real global shortcuts
	a, _ := levelShelf(t)
	a.win = &mygo.Window{}
	a.dispBounds = mygo.Rectangle{X: 0, Y: 0, Width: 1470, Height: 956}
	a.dispWork = mygo.Rectangle{X: 0, Y: 25, Width: 1470, Height: 931}
	a.hasNotch, a.notchX, a.notchY, a.notchW, a.notchH = false, 645, 0, 180, 32
	a.island, a.islandTop = true, islandTopGap
	a.y = 843 // the island's top row 850, its bottom 880: the panel grows up
	a.settings.ShellStyle = settings.ShellStyleIsland
	a.visible, a.level = false, 1
	// The canvas holds the tallest frame either level needs.
	a.fx = shellMotion{w: sheetW, h: sheetH, openK: 1, levelK: 1}
	big := a.desiredFrame()
	view := ui.NewTester(a.view, big.Width, big.Height)
	view.Frame() // the closed strip the spring starts from
	a.visible = true
	// Mid-flight: the shell is still short of the overview's 156.
	h := a.fx.h
	for i := 0; i < 40 && h < 78; i++ {
		view.Frame()
		view.Advance(16 * time.Millisecond)
		h = a.fx.h
	}
	// The frame Advance just drew is the one Find reads.
	if h < 78 || h > 124 {
		t.Fatalf("the shell is at %.1f tall, want a mid-flight height", h)
	}
	clipTop := a.upShellBottom(a.fx.openK) - int(math.Ceil(float64(h))) - a.desiredFrame().Y
	band, ok := view.Find("剪贴板")
	if !ok {
		t.Fatalf("the band icon is missing; texts %v", view.Texts())
	}
	if band.W > 0 && band.H > 0 {
		t.Fatalf("the shell at %.1f tall already draws the band at %+v, above its top %d", h, band, clipTop)
	}
	low, ok := view.Find("托盘里一条")
	if !ok {
		t.Fatalf("the overview's last row is missing; texts %v", view.Texts())
	}
	if low.W <= 0 || low.H <= 0 {
		t.Fatalf("nothing of the overview is uncovered at the island edge (shell top %d)", clipTop)
	}
	if low.Y < float32(clipTop)-0.5 || float32(clipTop)+float32(h) < low.Y {
		t.Fatalf("the uncovered overview row at %+v is not at the island edge of the shell %d..%d",
			low, clipTop, clipTop+int(h))
	}
	// Open: the shell reaches the overview's own 156.
	for i := 0; i < 60 && a.fx.h < overviewH-0.5; i++ {
		view.Frame()
		view.Advance(16 * time.Millisecond)
	}
	view.Advance(2 * time.Second) // include the spring's overshoot and settling
	view.Frame()
	if got := a.fx.h; got < overviewH-0.5 || got > overviewH+0.5 {
		t.Fatalf("the shell settled at %.1f tall, want the overview's %d", got, overviewH)
	}
	clipTop = a.upShellBottom(a.fx.openK) - int(math.Ceil(float64(a.fx.h))) - a.desiredFrame().Y
	band, ok = view.Find("剪贴板")
	if !ok {
		t.Fatalf("the band is missing at rest; texts %v", view.Texts())
	}
	low, ok = view.Find("托盘里一条")
	if !ok {
		t.Fatalf("the overview is missing at rest; texts %v", view.Texts())
	}
	for _, r := range []struct {
		name string
		box  ui.Rect
	}{{"band", band}, {"overview row", low}} {
		if r.box.Y < float32(clipTop)-0.5 || r.box.Y+r.box.H > float32(clipTop+overviewH)+0.5 {
			t.Errorf("the settled overview's %s at %+v is not inside the shell %d..%d",
				r.name, r.box, clipTop, clipTop+overviewH)
		}
	}
	if err := view.Click("托盘"); err != nil {
		t.Fatalf("the settled overview's band is not clickable: %v", err)
	}
	if a.level != 2 || a.tab != pageTray {
		t.Fatalf("a band click on the upward overview left level=%d tab=%s", a.level, a.tab)
	}
	// The level's shelf grows upward and carries the block with it: at no
	// frame is the band cut off above the shell.
	for i := 0; i < 60 && a.fx.h < sheetH-0.5; i++ {
		view.Frame()
		view.Advance(16 * time.Millisecond)
		if got := a.fx.h; got > 200 && got < sheetH-2 {
			shellTop := a.upShellBottom(a.fx.openK) - int(math.Ceil(float64(got))) - a.desiredFrame().Y
			band, ok := view.Find("剪贴板")
			if !ok {
				t.Fatalf("the band is missing at %.1f tall; texts %v", got, view.Texts())
			}
			if band.H <= 0 || band.W <= 0 {
				t.Fatalf("the band at %+v is clipped at %.1f tall (shell top %d)", band, got, shellTop)
			}
			if band.Y < float32(shellTop)-0.5 || band.Y+band.H > float32(shellTop+sheetH) {
				t.Fatalf("the band at %+v left the shell %d..%d at %.1f tall",
					band, shellTop, shellTop+int(math.Ceil(float64(got))), got)
			}
		}
	}
	view.Advance(2 * time.Second)
	view.Frame()
	if got := a.fx.h; got < sheetH-0.5 || got > sheetH+0.5 {
		t.Fatalf("the page settled at %.1f tall, want the sheet's %d", got, sheetH)
	}
	clipTop = a.upShellBottom(a.fx.openK) - int(math.Ceil(float64(a.fx.h))) - a.desiredFrame().Y
	band, ok = view.Find("剪贴板")
	if !ok {
		t.Fatalf("the band is missing on the page; texts %v", view.Texts())
	}
	low, ok = view.Find("托盘里一条")
	if !ok {
		t.Fatalf("the page lost its rows; texts %v", view.Texts())
	}
	for _, r := range []struct {
		name string
		box  ui.Rect
	}{{"band", band}, {"page row", low}} {
		if r.box.Y < float32(clipTop)-0.5 || r.box.Y+r.box.H > float32(clipTop+sheetH)+0.5 {
			t.Errorf("the settled page's %s at %+v is not inside the shell %d..%d",
				r.name, r.box, clipTop, clipTop+sheetH)
		}
	}
	if err := view.Click("剪贴板"); err != nil {
		t.Fatalf("the settled page's band is not clickable: %v", err)
	}
	if a.level != 2 || a.tab != pageClipboard {
		t.Fatalf("a band click on the upward page left level=%d tab=%s", a.level, a.tab)
	}
}

// TestDockReducedWorkAreaKeepsTheIslandOnScreen: the Dock takes rows out of
// the visible frame — the bottom 61pt, or a column on the left. The island
// still belongs to the whole screen: it clamps to the screen's own edges, so
// its lowest row is 908 (the work area alone would stop it at 847). The
// panel is what has to stay in the work area, so it is lifted off the
// island's bottom edge (895, not 938), and the drawn shell settles on the
// panel's box while the window still reaches under the island.
func TestDockReducedWorkAreaKeepsTheIslandOnScreen(t *testing.T) {
	syntheticDisplays(t, false, false)
	a := islandShelf(false, settings.ShellStyleIsland)
	a.dataDir = t.TempDir()
	a.place(mygo.Point{X: 735, Y: 38})
	if got := a.islandSpotNow().Top; got != islandTopGap {
		t.Fatalf("the island started at row %d, want the resting %d", got, islandTopGap)
	}
	dock := mygo.Rectangle{X: 0, Y: 25, Width: 1470, Height: 870}
	a.dispWork = dock
	// A drag far past the bottom-right corner still clamps to the screen,
	// although the visible frame has ended 61pt higher.
	a.moveIslandTo(735, 5000)
	if got, want := a.islandSpotNow().Top, a.dispBounds.Height-islandHoverH; got != want || got != 908 {
		t.Fatalf("the island clamped to row %d, want the screen's %d (908), not the work area's 847", got, want)
	}
	if got := a.anchorScreenTop() + islandIdleH; got != 938 {
		t.Fatalf("the island's bottom is %d, want 938", got)
	}
	a.visible, a.level, a.progress = true, 2, 1
	a.fx.w, a.fx.h = a.panelBox()
	a.fx.openK, a.fx.levelK = 1, 1
	place := a.panelPlacement()
	if !place.Up {
		t.Fatalf("the panel did not grow upward: %+v", place)
	}
	if got := place.Y + place.H; got != dock.Y+dock.Height || got != 895 {
		t.Fatalf("the panel bottom is %d, want the work area's 895", got)
	}
	// The drawn shell settles exactly on the panel's box, off the island.
	f := a.desiredFrame()
	shellTop := f.Y + int(math.Round(float64(a.shellTopLocal(a.fx.h, a.fx.openK))))
	if shellTop != place.Y {
		t.Fatalf("the shell top is %d, want the panel's %d", shellTop, place.Y)
	}
	if bottom := shellTop + int(math.Round(float64(a.fx.h))); bottom != place.Y+place.H {
		t.Fatalf("the shell bottom is %d, want the panel's %d", bottom, place.Y+place.H)
	}
	// The window still reaches under the island it grew out of.
	if f.Y > place.Y || f.Y+f.Height < 938+2 {
		t.Fatalf("frame %+v does not hold the panel %+v and the island's bottom 938", f, place)
	}
	// The hit shape is the panel's box: its top and bottom rows take clicks,
	// the empty rows the Dock lifted off claim none.
	mid := place.X + place.W/2
	for _, p := range []mygo.Point{
		{X: mid, Y: place.Y + 2},
		{X: mid, Y: place.Y + place.H - 2},
	} {
		if !a.overShell(p) {
			t.Errorf("the panel's row %d is not on the shell %+v", p.Y, place)
		}
	}
	if a.overShell(mygo.Point{X: mid, Y: place.Y + place.H + 2}) {
		t.Errorf("the row below the panel's %d takes the shell's clicks", place.Y+place.H+2)
	}
	// The shell's bottom edge is animated, not pinned to the clamped panel:
	// it grows out of the island's own bottom and eases onto the panel's.
	if got := a.upShellBottom(0); got != 938 {
		t.Fatalf("the closed shell's bottom is %d, want the island's 938", got)
	}
	if got := a.upShellBottom(1); got != 895 {
		t.Fatalf("the settled shell's bottom is %d, want the panel's 895", got)
	}
	if got := a.upShellBottom(0.5); got <= 895 || got >= 938 {
		t.Fatalf("the half-open shell's bottom %d is not between the panel's 895 and the island's 938", got)
	}
	// A Dock on the left narrows the visible frame, not the island: the
	// capsule may sit on the screen's own edge, and only the panel moves
	// into the work area.
	left := mygo.Rectangle{X: 90, Y: 25, Width: 1380, Height: 931}
	a.dispWork = left
	a.moveIslandTo(-5000, 300)
	if got := a.islandSpotNow().Centre; got != islandIdleW/2 {
		t.Fatalf("the island's centre clamped to %d, want the screen's edge %d", got, islandIdleW/2)
	}
	a.visible, a.level, a.progress = false, 2, 0
	a.fx.w, a.fx.h = a.panelBox()
	a.fx.openK, a.fx.levelK = 0, 0
	side := a.panelPlacement()
	if side.Up || side.X != left.X {
		t.Fatalf("the panel for the island on the screen's edge = %+v, want it down and inside the work area's left edge %d", side, left.X)
	}
	right := mygo.Rectangle{X: 0, Y: 25, Width: 1380, Height: 931}
	a.dispWork = right
	a.moveIslandTo(5000, 300)
	if got := a.islandSpotNow().Centre; got != a.dispBounds.Width-islandIdleW/2 {
		t.Fatalf("the island's centre clamped to %d, want the screen's right edge %d", got, a.dispBounds.Width-islandIdleW/2)
	}
	if side := a.panelPlacement(); side.Up || side.X+side.W != right.X+right.Width {
		t.Fatalf("the panel for the island on the right screen's edge = %+v, want it inside the work area's right edge %d", side, right.X+right.Width)
	}
}

// A centred fold retains the old odd-width native frame exactly. Extra
// off-axis coverage must not round a default-position frame up by 1pt.
func TestCenteredFoldKeepsExistingFrameWidth(t *testing.T) {
	for _, tc := range []struct {
		style settings.ShellStyle
		width int
	}{{settings.ShellStyleNotch, 249}, {settings.ShellStyleIsland, 225}} {
		a := islandShelf(true, tc.style)
		a.fx = shellMotion{w: 225, h: 100, openK: 0.5}
		if frame := a.desiredFrame(); frame.Width != tc.width {
			t.Errorf("%s centred fold width=%d, want legacy width %d", tc.style, frame.Width, tc.width)
		}
	}
}
