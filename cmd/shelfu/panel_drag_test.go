//go:build darwin

package main

import (
	"testing"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"shelf/internal/settings"
	"shelf/internal/store"
)

// Dragging the open panel by its band: on the island shell a press on the
// band's blank stretches moves the island and the panel together, the side
// the panel grew on stays put for the gesture, and the release saves the
// island's place like any drag of the closed shell. The band's controls and
// the page below it keep their presses. These tests draw the panel in a
// headless tester so the band's boxes are the ones the app would read, then
// drive mouseDown and the 30 ms monitor exactly as the app does.

// panelDragShelf is a headless shelf on the island, its panel open, with a
// tester that has drawn it twice: the band's control boxes come from the
// previous frame, so a press needs the second one.
func panelDragShelf(t *testing.T, level int, tab pageID, spot settings.IslandPos) (*shelf, *ui.Tester) {
	t.Helper()
	syntheticDisplays(t, false, false)
	a := islandShelf(false, settings.ShellStyleIsland)
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	a.store = st
	a.dataDir = t.TempDir()
	a.appNames = map[string]string{}
	if spot != (settings.IslandPos{}) {
		a.settings.IslandPos = map[string]settings.IslandPos{islandSpotKey(1): spot}
	}
	a.place(mygo.Point{X: 735, Y: 100})
	a.visible, a.level, a.progress, a.tab = true, level, 1, tab
	a.fx.w, a.fx.h = a.panelBox()
	a.fx.openK = 1
	origReg, origUnreg := registerKey, unregisterKey
	registerKey = func(string, func()) error { return nil }
	unregisterKey = func(string) {}
	t.Cleanup(func() { registerKey, unregisterKey = origReg, origUnreg })
	view := ui.NewTester(a.view, sheetW, sheetH)
	view.Frame()
	view.Frame()
	return a, view
}

// bandPoint is a screen point at x, y inside the band, relative to its
// top-left — the shell's — as the drawn band measures it.
func bandPoint(a *shelf, x, y int) mygo.Point {
	place := a.panelPlacement()
	return mygo.Point{X: place.X + x, Y: place.Y + y}
}

// requireBandDrawn fails unless the tester has drawn the band and its
// controls, which is what a press classifies against.
func requireBandDrawn(t *testing.T, a *shelf) {
	t.Helper()
	if a.bandBox.Width != sheetW || a.bandBox.Height != bandH {
		t.Fatalf("band box = %+v, want %d×%d", a.bandBox, sheetW, bandH)
	}
	if len(a.bandHits) == 0 {
		t.Fatal("the band recorded no controls")
	}
	if !a.bandBlank(bandPoint(a, sheetW/2, bandH/2)) {
		t.Fatal("the band's middle is not blank")
	}
}

// A press on the band's blank stretch that travels past the threshold moves
// the island and the panel together on the locked side, and the release
// saves the island's place. The panel stays open on its page.
func TestPanelDragMovesTheIslandAndSaves(t *testing.T) {
	a, _ := panelDragShelf(t, 1, pageOverview, settings.IslandPos{})
	requireBandDrawn(t, a)
	place := a.panelPlacement()
	p := bandPoint(a, sheetW/2, bandH/2)
	a.mouseDown(true, 0, p)
	if !a.islandPress.active || !a.islandPress.panel {
		t.Fatal("a blank band press did not arm the panel move")
	}
	// Four points of travel is still under the threshold: not a move yet.
	short := mygo.Point{X: p.X + islandDragStart, Y: p.Y + islandDragStart}
	dragIslandTick(t, a, short, true)
	if a.islandDrag {
		t.Fatalf("a press under the threshold became a move")
	}
	end := mygo.Point{X: p.X + 10, Y: p.Y + 10}
	dragIslandTick(t, a, end, true)
	if !a.islandDrag || !a.visible {
		t.Fatalf("the move: drag=%v visible=%v", a.islandDrag, a.visible)
	}
	want := panelPlacement{X: place.X + 10, Y: place.Y + 10, W: sheetW, H: overviewH}
	if got := a.panelPlacement(); got != want {
		t.Fatalf("the dragged panel = %+v, want %+v", got, want)
	}
	if got := a.islandSpotNow(); got != (islandSpot{Centre: want.X + want.W/2, Top: want.Y}) {
		t.Fatalf("the island spot = %+v, want it derived from the panel %+v", got, want)
	}
	dragIslandTick(t, a, end, false)
	if !a.visible || a.tab != pageOverview {
		t.Fatalf("the release changed the panel: visible=%v tab=%s", a.visible, a.tab)
	}
	if a.islandPress.active || a.islandDrag {
		t.Fatal("the release left the gesture open")
	}
	if a.flip.active {
		t.Fatal("a downward panel that stayed downward started a side change")
	}
	saved, err := settings.Load(a.dataDir)
	if err != nil {
		t.Fatal(err)
	}
	wantSpot := settings.IslandPos{X: want.X + want.W/2, Y: want.Y}
	if got := saved.IslandPos[islandSpotKey(1)]; got != wantSpot {
		t.Fatalf("the release saved %+v, want %+v", got, wantSpot)
	}
}

// A press on a page icon, the settings button or the page below the band
// never starts a move: the island keeps its place however far the pointer
// travels.
func TestPanelDragIgnoresTheBandControlsAndThePage(t *testing.T) {
	a, _ := panelDragShelf(t, 1, pageOverview, settings.IslandPos{})
	requireBandDrawn(t, a)
	place := a.panelPlacement()
	before := a.islandSpotNow()
	start := bandPoint(a, 0, 0)
	first, last := a.bandHits[0], a.bandHits[len(a.bandHits)-1]
	cases := []struct {
		name string
		r    mygo.Rectangle
	}{
		{"page icon", first},
		{"settings button", last},
	}
	for _, tc := range cases {
		p := mygo.Point{X: start.X + tc.r.X + tc.r.Width/2, Y: start.Y + tc.r.Y + tc.r.Height/2}
		if a.bandBlank(p) {
			t.Fatalf("the %s was taken for blank band", tc.name)
		}
		a.mouseDown(true, 0, p)
		if a.islandPress.active {
			t.Fatalf("a press on the %s armed a move", tc.name)
		}
		dragIslandTick(t, a, mygo.Point{X: p.X + 200, Y: p.Y + 200}, true)
		dragIslandTick(t, a, mygo.Point{X: p.X + 200, Y: p.Y + 200}, false)
		if got := a.islandSpotNow(); got != before {
			t.Fatalf("a drag from the %s moved the island: %+v -> %+v", tc.name, before, got)
		}
	}
	// Below the band is the page's own: same result.
	content := mygo.Point{X: place.X + sheetW/2, Y: place.Y + bandH + 40}
	if a.bandBlank(content) {
		t.Fatal("the content area was taken for blank band")
	}
	a.mouseDown(true, 0, content)
	if a.islandPress.active {
		t.Fatal("a press in the content armed a move")
	}
	dragIslandTick(t, a, mygo.Point{X: content.X + 200, Y: content.Y + 200}, true)
	dragIslandTick(t, a, mygo.Point{X: content.X + 200, Y: content.Y + 200}, false)
	if got := a.islandSpotNow(); got != before {
		t.Fatalf("a drag in the content moved the island: %+v -> %+v", before, got)
	}
}

// A press on the blank band that lifts before the threshold is nothing:
// the panel stays open on its page, the island stays put, and no place is
// written.
func TestPanelTapOnTheBandDoesNothing(t *testing.T) {
	a, _ := panelDragShelf(t, 1, pageOverview, settings.IslandPos{})
	requireBandDrawn(t, a)
	before := a.islandSpotNow()
	p := bandPoint(a, sheetW/2, bandH/2)
	a.mouseDown(true, 0, p)
	dragIslandTick(t, a, mygo.Point{X: p.X + 3, Y: p.Y + 3}, true)
	dragIslandTick(t, a, mygo.Point{X: p.X + 3, Y: p.Y + 3}, false)
	if a.islandDrag || a.islandPress.active {
		t.Fatal("the tap left the gesture open")
	}
	if !a.visible || a.tab != pageOverview {
		t.Fatalf("the tap changed the panel: visible=%v tab=%s", a.visible, a.tab)
	}
	if got := a.islandSpotNow(); got != before {
		t.Fatalf("the tap moved the island: %+v -> %+v", before, got)
	}
	saved, err := settings.Load(a.dataDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(saved.IslandPos) != 0 {
		t.Fatalf("the tap saved a place: %+v", saved.IslandPos)
	}
}

// The side the panel grew on is locked for the gesture: an upward panel
// dragged high enough to have room below keeps hanging above the island
// until the release. There it slides to the other side on the panel's
// spring — never jumping with the box it was dragged on — and the island
// keeps the saved place.
func TestPanelDragLocksTheSideAndSlidesOnRelease(t *testing.T) {
	a, view := panelDragShelf(t, 2, pageTray, settings.IslandPos{X: 735, Y: 800})
	place := a.panelPlacement()
	if !place.Up {
		t.Fatalf("the shelf is not on an upward panel: %+v", place)
	}
	requireBandDrawn(t, a)
	p := bandPoint(a, sheetW/2, bandH/2)
	a.mouseDown(true, 0, p)
	if !a.islandPress.active || !a.islandPress.up {
		t.Fatal("the band press did not lock the upward side")
	}
	// 200pt up: the island now has room for the panel below it, but the
	// gesture keeps the side it started on.
	up := mygo.Point{X: p.X, Y: p.Y - 200}
	dragIslandTick(t, a, up, true)
	want := panelPlacement{X: place.X, Y: place.Y - 200, W: sheetW, H: sheetH, Up: true}
	if got := a.panelPlacement(); got != want {
		t.Fatalf("the dragged upward panel = %+v, want %+v", got, want)
	}
	if got := a.islandSpotNow(); got != (islandSpot{Centre: 735, Top: 600}) {
		t.Fatalf("the island under the dragged panel = %+v, want 735/600", got)
	}
	if a.panelPlacement().Up != true {
		t.Fatal("the panel flipped sides mid-gesture")
	}
	dragIslandTick(t, a, up, false)
	if !a.flip.active {
		t.Fatalf("the release left the panel on the wrong side: %+v", a.panelPlacement())
	}
	if got, want := a.panelPlacement(), (panelPlacement{X: place.X, Y: place.Y - 200, W: sheetW, H: sheetH, Up: true}); got != want {
		t.Fatalf("the first frame of the side change = %+v, want %+v (no jump)", got, want)
	}
	if got := a.islandSpotNow(); got != (islandSpot{Centre: 735, Top: 600}) {
		t.Fatalf("the release moved the island: %+v", got)
	}
	// The spring carries the box down to the side the new place calls for.
	view.Frame() // starts the flight on the next drawn frame
	view.Advance(80 * time.Millisecond)
	if !a.flip.active || a.flip.k <= 0 || a.flip.k >= 1 {
		t.Fatalf("the side change did not run on its spring: active=%v k=%g", a.flip.active, a.flip.k)
	}
	if y := a.panelPlacement().Y; y <= place.Y-200 || y >= 600 {
		t.Fatalf("the sliding panel is at Y %d, want between %d and 600", y, place.Y-200)
	}
	// The window and the hit shape follow the sliding box: the panel is
	// never clipped and never leaves its own clicks behind.
	mid := a.panelPlacement()
	frame := a.desiredFrame()
	if frame.X > mid.X || frame.X+frame.Width < mid.X+mid.W ||
		frame.Y > mid.Y || frame.Y+frame.Height < mid.Y+mid.H {
		t.Fatalf("the window frame %+v does not cover the sliding panel %+v", frame, mid)
	}
	if !a.overShell(mygo.Point{X: mid.X + mid.W/2, Y: mid.Y + bandH/2}) {
		t.Fatalf("the sliding panel's band at %+v does not take its own clicks", mid)
	}
	view.Advance(time.Second)
	if a.flip.active {
		t.Fatal("the side change never settled")
	}
	if got := a.panelPlacement(); got != (panelPlacement{X: place.X, Y: 600, W: sheetW, H: sheetH}) {
		t.Fatalf("the settled panel = %+v, want it below the island at 600", got)
	}
	saved, err := settings.Load(a.dataDir)
	if err != nil {
		t.Fatal(err)
	}
	if got := saved.IslandPos[islandSpotKey(1)]; got != (settings.IslandPos{X: 735, Y: 600}) {
		t.Fatalf("the release saved %+v, want 735/600", got)
	}
}

// Reduce Motion takes the new side at once: the panel is still expanded and
// still draggable, only the slide is gone.
func TestPanelSideChangeUnderReduceMotion(t *testing.T) {
	a, view := panelDragShelf(t, 2, pageTray, settings.IslandPos{X: 735, Y: 800})
	view.SetPreferences(ui.Preferences{ReduceMotion: true})
	place := a.panelPlacement()
	if !place.Up {
		t.Fatalf("the shelf is not on an upward panel: %+v", place)
	}
	requireBandDrawn(t, a)
	p := bandPoint(a, sheetW/2, bandH/2)
	a.mouseDown(true, 0, p)
	up := mygo.Point{X: p.X, Y: p.Y - 200}
	dragIslandTick(t, a, up, true)
	dragIslandTick(t, a, up, false)
	if !a.flip.active {
		t.Fatal("the release did not schedule the side change")
	}
	view.Frame()
	if a.flip.active {
		t.Fatal("Reduce Motion kept the side change in flight")
	}
	if got := a.panelPlacement(); got != (panelPlacement{X: place.X, Y: 600, W: sheetW, H: sheetH}) {
		t.Fatalf("the reduced-motion panel = %+v, want it below the island at 600", got)
	}
	if !a.visible {
		t.Fatal("Reduce Motion closed the panel")
	}
}

// The notch shell has no island to move: pressing its band changes nothing,
// however far the pointer travels.
func TestPanelDragDoesNotExistOnTheNotchShell(t *testing.T) {
	syntheticDisplays(t, true, false)
	a := islandShelf(true, settings.ShellStyleNotch)
	a.dataDir = t.TempDir()
	a.place(mygo.Point{X: 735, Y: 100})
	if a.island {
		t.Fatal("the notched screen on the notch style drew the island")
	}
	a.visible, a.level, a.progress = true, 1, 1
	a.fx.w, a.fx.h = a.panelBox()
	a.fx.openK = 1
	origReg, origUnreg := registerKey, unregisterKey
	registerKey = func(string, func()) error { return nil }
	unregisterKey = func(string) {}
	t.Cleanup(func() { registerKey, unregisterKey = origReg, origUnreg })
	view := ui.NewTester(a.view, sheetW, sheetH)
	view.Frame()
	view.Frame()
	requireBandDrawn(t, a)
	p := bandPoint(a, sheetW/2, bandH/2)
	a.mouseDown(true, 0, p)
	if a.islandPress.active {
		t.Fatal("a notch band press armed a move")
	}
	dragIslandTick(t, a, mygo.Point{X: p.X + 300, Y: p.Y + 300}, true)
	dragIslandTick(t, a, mygo.Point{X: p.X + 300, Y: p.Y + 300}, false)
	if a.islandDX != 0 || a.islandDY != 0 || a.y != a.dispBounds.Y {
		t.Fatalf("the notch shell acquired an island offset: dx=%d dy=%d y=%d", a.islandDX, a.islandDY, a.y)
	}
	saved, err := settings.Load(a.dataDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(saved.IslandPos) != 0 {
		t.Fatalf("the notch press saved a place: %+v", saved.IslandPos)
	}
}
