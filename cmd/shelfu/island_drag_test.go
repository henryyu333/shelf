//go:build darwin

package main

import (
	"testing"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"shelf/internal/settings"
)

// The floating island is draggable: a press on the closed shell becomes a
// move once it travels past islandDragStart, the shell follows the pointer
// clamped onto its display, and the place is saved on the release. A tap
// under that distance still opens the panel. These tests drive the same
// entry points the app uses — mouseDown and one 30 ms monitor tick — with
// the display and housing lookups stubbed for a headless run.

// syntheticDisplays replaces the display and housing lookups with a fixed
// layout: a 1470 × 956 built-in at the origin (25pt menu bar, 180 × 32
// housing when notch), and optionally a 2560 × 1440 external to its right
// with no housing. Both stubs are restored when the test ends.
func syntheticDisplays(t *testing.T, notch, two bool) {
	t.Helper()
	builtin := mygo.Display{
		ID: 1, Primary: true,
		Bounds:   mygo.Rectangle{X: 0, Y: 0, Width: 1470, Height: 956},
		WorkArea: mygo.Rectangle{X: 0, Y: 25, Width: 1470, Height: 931},
	}
	external := mygo.Display{
		ID:       2,
		Bounds:   mygo.Rectangle{X: 1470, Y: 0, Width: 2560, Height: 1440},
		WorkArea: mygo.Rectangle{X: 1470, Y: 25, Width: 2560, Height: 1415},
	}
	oldD, oldN := displayNearest, notchRect
	displayNearest = func(p mygo.Point) mygo.Display {
		if two && p.X >= external.Bounds.X {
			return external
		}
		return builtin
	}
	notchRect = func(id int64) (int, int, int, int, bool) {
		if id == 1 && notch {
			return 645, 0, 180, 32, true
		}
		return 0, 0, 0, 0, false
	}
	t.Cleanup(func() { displayNearest, notchRect = oldD, oldN })
}

// dragIslandTick runs one 30 ms monitor tick with the pointer at p and the
// left button either held or released, exactly as the app's ticker drives
// it.
func dragIslandTick(t *testing.T, a *shelf, p mygo.Point, down bool) {
	t.Helper()
	oldCursor, oldState, oldTypes, oldDown := cursorPoint, dragState, dragTypes, dragLeftDown
	cursorPoint = func() mygo.Point { return p }
	dragState = func() (bool, int64) { return false, 0 }
	dragTypes = func() []string { return nil }
	dragLeftDown = func() bool { return down }
	a.monitorDrag(time.Now())
	cursorPoint, dragState, dragTypes, dragLeftDown = oldCursor, oldState, oldTypes, oldDown
}

func TestClampIslandSpot(t *testing.T) {
	bounds := mygo.Rectangle{X: 100, Y: 50, Width: 1440, Height: 900}
	work := mygo.Rectangle{X: 100, Y: 75, Width: 1440, Height: 850}
	pw, ph := islandPanelRoom()
	def := 38
	for _, tt := range []struct {
		name string
		in   islandSpot
		want islandSpot
	}{
		{"a spot with room stays", islandSpot{Centre: 700, Top: 300}, islandSpot{Centre: 700, Top: 300}},
		{"the shell stops at its default row", islandSpot{Centre: 700, Top: -400}, islandSpot{Centre: 700, Top: def}},
		{"the panel's bottom stops at the work area", islandSpot{Centre: 700, Top: 5000}, islandSpot{Centre: 700, Top: work.Y + work.Height - bounds.Y - ph}},
		{"the panel's left edge stops at the work area", islandSpot{Centre: -1000, Top: 300}, islandSpot{Centre: work.X - bounds.X + pw/2, Top: 300}},
		{"the panel's right edge stops at the work area", islandSpot{Centre: 9000, Top: 300}, islandSpot{Centre: work.X + work.Width - bounds.X - pw/2, Top: 300}},
	} {
		if got := clampIslandSpot(tt.in, bounds, work, def); got != tt.want {
			t.Errorf("%s: clamp = %+v, want %+v", tt.name, got, tt.want)
		}
	}
	// The island may sit on its default row but never above it: the row is
	// the menu bar / the housing's clearance.
	if got := clampIslandSpot(islandSpot{Centre: 700, Top: def - 1}, bounds, work, def); got.Top != def {
		t.Errorf("a spot above the default row clamped to top %d, want %d", got.Top, def)
	}
	// A visible area narrower than the panel has exactly one centre.
	narrow := mygo.Rectangle{Width: 300, Height: 900}
	if got := clampIslandSpot(islandSpot{Centre: 5, Top: 40}, narrow, narrow, 7); got.Centre != 150 || got.Top != 40 {
		t.Errorf("a display narrower than the panel clamped to %+v, want centre 150 top 40", got)
	}
	// A visible area shorter than the panel keeps the shell's own row.
	short := mygo.Rectangle{Width: 1440, Height: 100}
	if got := clampIslandSpot(islandSpot{Centre: 700, Top: 500}, short, short, 7); got.Top != 7 {
		t.Errorf("a display shorter than the panel clamped to top %d, want 7", got.Top)
	}
}

// A press that travels past the threshold is a move: the panel stays shut,
// the shell follows the pointer and the release writes the place to
// settings.json.
func TestIslandDragMovesAndSaves(t *testing.T) {
	syntheticDisplays(t, false, false)
	a := islandShelf(false, settings.ShellStyleNotch)
	a.dataDir = t.TempDir()
	a.place(mygo.Point{X: 700, Y: 100})
	a.fx.w, a.fx.h = islandIdleW, islandIdleH
	if got := a.islandRect(); got != (mygo.Rectangle{X: 675, Y: islandTopGap, Width: islandIdleW, Height: islandIdleH}) {
		t.Fatalf("resting capsule = %+v, want the default spot", got)
	}
	start := mygo.Point{X: 735, Y: islandTopGap + 10} // 60/10 inside the capsule
	a.mouseDown(true, 0, start)
	// Three points is still a tap: nothing moved and nothing opened yet.
	drive := mygo.Point{X: start.X + 3, Y: start.Y + 3}
	dragIslandTick(t, a, drive, true)
	if a.islandDrag || a.visible || a.stripCenter() != 735 {
		t.Fatalf("a press under the threshold moved the island: drag=%v visible=%v", a.islandDrag, a.visible)
	}
	// Past the threshold the shell follows the grab point with no clamp.
	end := mygo.Point{X: 900, Y: 420}
	dragIslandTick(t, a, end, true)
	if a.visible {
		t.Fatal("a moving press opened the panel")
	}
	want := mygo.Rectangle{X: 900 - islandIdleW/2, Y: 410, Width: islandIdleW, Height: islandIdleH}
	if got := a.islandRect(); got != want {
		t.Fatalf("dragged capsule = %+v, want %+v", got, want)
	}
	if a.stripCenter() != 900 || a.y != 403 {
		t.Fatalf("anchors not on the dragged spot: centre %d y %d", a.stripCenter(), a.y)
	}
	// The window that draws it follows: same axis, top on the new row.
	if f := a.desiredFrame(); f.Y != 403 || f.X != 900-f.Width/2 {
		t.Fatalf("window frame %+v did not follow the island", f)
	}
	dragIslandTick(t, a, end, false)
	if a.visible || a.islandDrag || a.islandPress.active {
		t.Fatalf("the release left the gesture open: visible=%v drag=%v", a.visible, a.islandDrag)
	}
	saved, err := settings.Load(a.dataDir)
	if err != nil {
		t.Fatal(err)
	}
	if got := saved.IslandPos[islandSpotKey(1)]; got != (settings.IslandPos{X: 900, Y: 410}) {
		t.Fatalf("saved spot = %+v, want (900, 410)", got)
	}
}

// The drag stops at the display's edges: the shell's top never rises above
// its default row and the panel it opens stays inside the visible frame,
// even when the pointer is dragged far past the bottom-right corner.
func TestIslandDragClampsAtTheDisplayEdges(t *testing.T) {
	syntheticDisplays(t, false, false)
	a := islandShelf(false, settings.ShellStyleNotch)
	a.dataDir = t.TempDir()
	a.place(mygo.Point{X: 700, Y: 100})
	a.fx.w, a.fx.h = islandIdleW, islandIdleH
	a.mouseDown(true, 0, mygo.Point{X: 735, Y: islandTopGap + 10})
	dragIslandTick(t, a, mygo.Point{X: 5000, Y: 5000}, true)
	pw, ph := islandPanelRoom()
	spot := a.islandSpotNow()
	if spot.Centre != a.dispWork.X+a.dispWork.Width-pw/2 || spot.Top != a.dispWork.Y+a.dispWork.Height-ph {
		t.Fatalf("clamped spot = %+v, want the work area's bottom-right corner", spot)
	}
	panel := a.panelDropRect()
	if panel.X < a.dispWork.X || panel.X+panel.Width > a.dispWork.X+a.dispWork.Width ||
		panel.Y+ph < a.dispWork.Y || panel.Y+ph > a.dispWork.Y+a.dispWork.Height {
		t.Fatalf("the panel the island would open at %+v leaves the work area %+v", panel, a.dispWork)
	}
	dragIslandTick(t, a, mygo.Point{X: 5000, Y: -5000}, true)
	if a.y+islandTopGap < a.dispBounds.Y+islandTopGap || a.islandSpotNow().Top != islandTopGap {
		t.Fatalf("dragging above the screen put the island at top %d", a.islandSpotNow().Top)
	}
	dragIslandTick(t, a, mygo.Point{X: 5000, Y: 5000}, false)
}

// A tap — a press and a release under the threshold — opens the panel, the
// same click it was before the island could move.
func TestIslandTapOpensOnRelease(t *testing.T) {
	syntheticDisplays(t, false, false)
	a := islandShelf(false, settings.ShellStyleNotch)
	a.dataDir = t.TempDir()
	a.place(mygo.Point{X: 700, Y: 100})
	a.fx.w, a.fx.h = islandIdleW, islandIdleH
	p := mygo.Point{X: 735, Y: islandTopGap + 10}
	a.mouseDown(true, 0, p)
	dragIslandTick(t, a, mygo.Point{X: p.X + 2, Y: p.Y}, true)
	dragIslandTick(t, a, mygo.Point{X: p.X + 2, Y: p.Y}, false)
	if !a.visible {
		t.Fatal("a tap on the island did not open the panel")
	}
	if _, err := settings.Load(a.dataDir); err != nil {
		t.Fatal(err)
	}
	if got := a.settings.IslandPos[islandSpotKey(1)]; got != (settings.IslandPos{}) {
		t.Fatalf("a tap saved a place: %+v", got)
	}
}

// A drag on a second display measures from that display's own origin: the
// saved spot is display-relative, so a screen arranged to the right (or
// above) replays exactly where it was left.
func TestIslandDragOnAnOffsetDisplay(t *testing.T) {
	syntheticDisplays(t, true, true)
	a := islandShelf(true, settings.ShellStyleIsland)
	a.dataDir = t.TempDir()
	a.place(mygo.Point{X: 2000, Y: 500})
	if a.dispID != 2 {
		t.Fatalf("placed on display %d, want the external", a.dispID)
	}
	a.fx.w, a.fx.h = islandIdleW, islandIdleH
	start := a.islandRect()
	if start.X != 1470+(2560-islandIdleW)/2 {
		t.Fatalf("external resting capsule = %+v", start)
	}
	a.mouseDown(true, 0, mygo.Point{X: start.X + 60, Y: start.Y + 10})
	dragIslandTick(t, a, mygo.Point{X: start.X + 260, Y: start.Y + 310}, true)
	got := a.islandSpotNow()
	if got.Centre != start.X+260-1470 || got.Top != start.Y+300 {
		t.Fatalf("dragged spot = %+v on display 2", got)
	}
	dragIslandTick(t, a, mygo.Point{X: start.X + 260, Y: start.Y + 310}, false)
	saved, err := settings.Load(a.dataDir)
	if err != nil {
		t.Fatal(err)
	}
	if got := saved.IslandPos[islandSpotKey(2)]; got != (settings.IslandPos{X: start.X + 260 - 1470, Y: start.Y + 300}) {
		t.Fatalf("saved spot on display 2 = %+v", got)
	}
	if _, ok := saved.IslandPos[islandSpotKey(1)]; ok {
		t.Fatalf("the drag wrote the other display: %+v", saved.IslandPos)
	}
}

// The saved place is replayed whenever the shelf re-measures its display:
// placeAt (show, a display change, a system drag onto the screen) and a
// style switch both land on the same spot, and switching to the notch shape
// clears the offset while switching back restores it.
func TestSavedIslandSpotIsReplayed(t *testing.T) {
	syntheticDisplays(t, true, true)
	prefs := settings.Default()
	prefs.ShellStyle = settings.ShellStyleIsland
	prefs.IslandPos = map[string]settings.IslandPos{
		islandSpotKey(1): {X: 800, Y: 300},
		islandSpotKey(2): {X: 500, Y: 300},
	}
	a := islandShelf(true, settings.ShellStyleIsland)
	a.settings = prefs
	a.dataDir = t.TempDir()
	// The external display sits at X=1470 and has no housing: its default
	// row is the 7pt gap.
	a.place(mygo.Point{X: 2000, Y: 500})
	if a.dispID != 2 {
		t.Fatalf("placed on display %d, want the external", a.dispID)
	}
	want := mygo.Rectangle{X: 1470 + 500 - islandIdleW/2, Y: 300, Width: islandIdleW, Height: islandIdleH}
	if got := a.islandRect(); got != want {
		t.Fatalf("replayed capsule = %+v, want %+v", got, want)
	}
	if f := a.desiredFrame(); f.Y != 300-islandTopGap {
		t.Fatalf("window top %d lost the saved row", f.Y)
	}
	// The notched display keeps its own place through a style switch: the
	// notch shape has no offset at all, the island shape reads it back.
	a.place(mygo.Point{X: 700, Y: 100})
	want = mygo.Rectangle{X: 800 - islandIdleW/2, Y: 300, Width: islandIdleW, Height: islandIdleH}
	if got := a.islandRect(); got != want {
		t.Fatalf("replayed capsule on the built-in = %+v, want %+v", got, want)
	}
	a.setShellStyle(settings.ShellStyleNotch)
	if a.island || a.islandDX != 0 || a.islandDY != 0 || a.y != a.dispBounds.Y {
		t.Fatalf("the notch style kept an island offset: island=%v dx=%d dy=%d y=%d", a.island, a.islandDX, a.islandDY, a.y)
	}
	if got := a.stripCenter(); got != 735 {
		t.Fatalf("the notch style's axis = %d, want the housing's centre", got)
	}
	a.setShellStyle(settings.ShellStyleIsland)
	if got := a.islandRect(); got != want {
		t.Fatalf("switching back to the island forgot the saved place: %+v", got)
	}
}

// The notch shape cannot be dragged: its press path is untouched and no
// place is ever written.
func TestNotchStyleCannotBeDragged(t *testing.T) {
	syntheticDisplays(t, true, false)
	a := islandShelf(true, settings.ShellStyleNotch)
	a.dataDir = t.TempDir()
	a.place(mygo.Point{X: 700, Y: 100})
	if a.island {
		t.Fatal("a notched screen on the notch shape must not draw the island")
	}
	before := mygo.Rectangle{X: a.stripCenter(), Y: a.y, Width: a.notchW, Height: a.notchH}
	p := mygo.Point{X: a.notchX + a.notchW/2, Y: 10}
	a.mouseDown(true, 0, p)
	if a.islandPress.active {
		t.Fatal("a press on the notch armed an island move")
	}
	dragIslandTick(t, a, mygo.Point{X: p.X + 200, Y: 400}, true)
	dragIslandTick(t, a, mygo.Point{X: p.X + 200, Y: 400}, false)
	if got := (mygo.Rectangle{X: a.stripCenter(), Y: a.y, Width: a.notchW, Height: a.notchH}); got != before {
		t.Fatalf("the notch moved to %+v", got)
	}
	saved, err := settings.Load(a.dataDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(saved.IslandPos) != 0 {
		t.Fatalf("the notch style wrote island places: %+v", saved.IslandPos)
	}
}

// Everything anchored on the island follows it: the peek pill, the drop
// band, the drop-protection corridor and the panel checks.
func TestIslandSurfacesFollowTheMove(t *testing.T) {
	syntheticDisplays(t, true, false)
	a := islandShelf(true, settings.ShellStyleIsland)
	a.dataDir = t.TempDir()
	a.place(mygo.Point{X: 700, Y: 100})
	if !a.notchGuardOn() {
		t.Fatal("the guard must be on while the island sits on its default row")
	}
	a.fx.w, a.fx.h = islandIdleW, islandIdleH
	p := mygo.Point{X: a.stripCenter(), Y: a.islandRect().Y + 10}
	a.mouseDown(true, 0, p)
	dragIslandTick(t, a, mygo.Point{X: 600, Y: 500}, true)
	r := a.islandRect()
	if !a.overPeekPill(mygo.Point{X: r.X + r.Width/2, Y: r.Y + 5}) {
		t.Fatalf("the peek pill %+v did not follow the island %+v", a.peekPillRect(), r)
	}
	if !a.dropBandX(mygo.Point{X: r.X + r.Width/2, Y: r.Y + 5}) {
		t.Fatal("the drop band stayed at the old position")
	}
	if a.dropBandX(mygo.Point{X: r.X + r.Width/2, Y: a.dispBounds.Y + 2}) {
		t.Fatal("the drop band still covers the screen's top row")
	}
	if a.notchGuardOn() {
		t.Fatal("the drop-protection corridor must be off once the island left the top row")
	}
	if !a.pinReturnZone(mygo.Point{X: r.X + r.Width/2, Y: r.Y + r.Height/2}) {
		t.Fatal("a pin dropped on the moved island does not return")
	}
	panel := a.panelDropRect()
	if panel.X != a.stripCenter()-sheetW/2 || panel.Y != a.y {
		t.Fatalf("the panel box %+v did not follow the island", panel)
	}
	dragIslandTick(t, a, mygo.Point{X: 600, Y: 500}, false)
}

// 设置 › 通用 shows 恢复默认位置 under 刘海样式 once the island is the drawn
// shape, and the button puts the capsule back and forgets the place.
func TestResetIslandPositionRow(t *testing.T) {
	syntheticDisplays(t, false, false)
	stubSettingsSeams(t, false, nil, nil)
	a := islandShelf(false, settings.ShellStyleNotch)
	a.dataDir = t.TempDir()
	a.appNames = map[string]string{}
	a.settings.IslandPos = map[string]settings.IslandPos{islandSpotKey(1): {X: 300, Y: 400}}
	a.place(mygo.Point{X: 700, Y: 100})
	if got := a.islandRect(); got.Y != 400 {
		t.Fatalf("the saved place was not applied: %+v", got)
	}
	view := ui.NewTester(a.settingsView, settingsWidth, settingsHeight)
	openSettingsPage(t, view, "外形与提示")
	if !view.HasText("恢复默认位置") {
		t.Fatalf("恢复默认位置 is missing: %v", view.Texts())
	}
	clickVisible(t, view, "恢复默认位置")
	if got := a.islandRect(); got != (mygo.Rectangle{X: 735 - islandIdleW/2, Y: islandTopGap, Width: islandIdleW, Height: islandIdleH}) {
		t.Fatalf("reset left the capsule at %+v", got)
	}
	saved, err := settings.Load(a.dataDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(saved.IslandPos) != 0 {
		t.Fatalf("reset kept the place: %+v", saved.IslandPos)
	}
	// The notch shape has no place of its own: no row.
	b := islandShelf(true, settings.ShellStyleNotch)
	b.dataDir = t.TempDir()
	b.appNames = map[string]string{}
	if view := ui.NewTester(b.settingsView, settingsWidth, settingsHeight); view.HasText("恢复默认位置") {
		t.Fatal("the notch shape offered 恢复默认位置")
	}
}

// The release frame carries the pointer's final place: a press that only
// travels as the button lifts is a move, not the tap that opens the panel,
// and the place written is where the button came up — not the last sample an
// intermediate tick saw.
func TestIslandReleaseFrameUsesTheReleasePoint(t *testing.T) {
	syntheticDisplays(t, false, false)
	a := islandShelf(false, settings.ShellStyleNotch)
	a.dataDir = t.TempDir()
	a.place(mygo.Point{X: 700, Y: 100})
	a.fx.w, a.fx.h = islandIdleW, islandIdleH
	p := mygo.Point{X: 735, Y: islandTopGap + 10}
	a.mouseDown(true, 0, p)
	// No intermediate sample: all 10pt of travel show up on the release.
	dragIslandTick(t, a, mygo.Point{X: p.X + 10, Y: p.Y}, false)
	if a.visible {
		t.Fatal("a press released 10pt away opened the panel")
	}
	if got := a.islandSpotNow(); got != (islandSpot{Centre: 745, Top: islandTopGap}) {
		t.Fatalf("release-only move left the island at %+v, want 745/%d", got, islandTopGap)
	}
	saved, err := settings.Load(a.dataDir)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := saved.IslandPos[islandSpotKey(1)], (settings.IslandPos{X: 745, Y: islandTopGap}); got != want {
		t.Fatalf("saved spot = %+v, want %+v", got, want)
	}
	// A drag ends at the release point, not at the position of its last
	// sampled frame.
	a.mouseDown(true, 0, mygo.Point{X: 745, Y: islandTopGap + 10})
	dragIslandTick(t, a, mygo.Point{X: 900, Y: 420}, true)
	dragIslandTick(t, a, mygo.Point{X: 920, Y: 440}, false)
	if got := a.islandSpotNow(); got != (islandSpot{Centre: 920, Top: 430}) {
		t.Fatalf("drag ended at %+v, want 920/430", got)
	}
	saved, err = settings.Load(a.dataDir)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := saved.IslandPos[islandSpotKey(1)], (settings.IslandPos{X: 920, Y: 430}); got != want {
		t.Fatalf("saved spot = %+v, want %+v", got, want)
	}
}

// A gesture that crosses onto a display whose shell is the notch style ends
// there: the notch has no place to move, so nothing is moved and nothing is
// saved, and its island offset stays zero.
func TestIslandDragOntoTheNotchShellCancels(t *testing.T) {
	syntheticDisplays(t, true, true)
	a := islandShelf(false, settings.ShellStyleNotch)
	a.dataDir = t.TempDir()
	a.place(mygo.Point{X: 2000, Y: 100})
	if a.dispID != 2 || !a.island {
		t.Fatalf("placed on display %d island=%v, want the external island", a.dispID, a.island)
	}
	a.fx.w, a.fx.h = islandIdleW, islandIdleH
	r := a.islandRect()
	a.mouseDown(true, 0, mygo.Point{X: r.X + r.Width/2, Y: r.Y + 10})
	if !a.islandPress.active {
		t.Fatal("the press did not arm the island gesture")
	}
	// Onto the built-in display: the notch style draws the notch there.
	dragIslandTick(t, a, mygo.Point{X: 1000, Y: 300}, true)
	if a.island {
		t.Fatal("the notched display drew the island on the notch style")
	}
	if a.islandDX != 0 || a.islandDY != 0 || a.y != a.dispBounds.Y {
		t.Fatalf("the notch shell acquired an island offset: dx=%d dy=%d y=%d", a.islandDX, a.islandDY, a.y)
	}
	if a.islandPress.active || a.islandDrag {
		t.Fatal("the gesture survived the crossing onto the notch shell")
	}
	if got := a.stripCenter(); got != a.notchX+a.notchW/2 {
		t.Fatalf("the notch shell's axis = %d, want the housing's centre %d", got, a.notchX+a.notchW/2)
	}
	// The release that carries the pointer ends nothing and writes nothing.
	dragIslandTick(t, a, mygo.Point{X: 1020, Y: 320}, false)
	if a.islandDX != 0 || a.islandDY != 0 {
		t.Fatalf("the release gave the notch shell an island offset: dx=%d dy=%d", a.islandDX, a.islandDY)
	}
	saved, err := settings.Load(a.dataDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(saved.IslandPos) != 0 {
		t.Fatalf("the cancelled gesture saved a place: %+v", saved.IslandPos)
	}
}

// An island-to-island crossing keeps the capsule under the pointer, exactly
// as the spec allows: the place is saved for the display the button came up
// on, and the display it started from is left untouched.
func TestIslandDragAcrossDisplaysSavesTheReleaseDisplay(t *testing.T) {
	syntheticDisplays(t, true, true)
	a := islandShelf(true, settings.ShellStyleIsland)
	a.dataDir = t.TempDir()
	a.place(mygo.Point{X: 2000, Y: 300})
	if a.dispID != 2 || !a.island {
		t.Fatalf("placed on display %d island=%v, want the external island", a.dispID, a.island)
	}
	a.fx.w, a.fx.h = islandIdleW, islandIdleH
	r := a.islandRect()
	a.mouseDown(true, 0, mygo.Point{X: r.X + r.Width/2, Y: r.Y + 10})
	dragIslandTick(t, a, mygo.Point{X: 700, Y: 300}, true)
	if a.dispID != 1 || !a.island {
		t.Fatalf("the gesture did not follow onto display %d island=%v", a.dispID, a.island)
	}
	if got := a.islandSpotNow(); got != (islandSpot{Centre: 700, Top: 290}) {
		t.Fatalf("cross-display spot = %+v, want 700/290", got)
	}
	dragIslandTick(t, a, mygo.Point{X: 720, Y: 310}, false)
	if a.islandPress.active || a.islandDrag {
		t.Fatal("the release left the gesture open")
	}
	if got := a.islandSpotNow(); got != (islandSpot{Centre: 720, Top: 300}) {
		t.Fatalf("release spot = %+v, want 720/300", got)
	}
	saved, err := settings.Load(a.dataDir)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := saved.IslandPos[islandSpotKey(1)], (settings.IslandPos{X: 720, Y: 300}); got != want {
		t.Fatalf("saved spot on the release display = %+v, want %+v", got, want)
	}
	if _, ok := saved.IslandPos[islandSpotKey(2)]; ok {
		t.Fatalf("the drag wrote the display it started on: %+v", saved.IslandPos)
	}
}

// The user's own island gesture is not an incoming file drag: the notch's
// event filter must not clamp it, or a horizontal swipe on the default row
// would be pushed down onto the guard's landing row and take the capsule
// with it. The filter is parked for the press through its switch (the same
// decision the native tap reads), and normal file-drag protection is back
// with the release.
func TestIslandGesturePausesTheDragGuard(t *testing.T) {
	syntheticDisplays(t, true, false)
	var parks []bool
	oldPark := parkNotchFilter
	parkNotchFilter = func(parked bool) { parks = append(parks, parked) }
	t.Cleanup(func() { parkNotchFilter = oldPark })
	parkedNow := func() bool { return len(parks) > 0 && parks[len(parks)-1] }
	a := islandShelf(true, settings.ShellStyleIsland)
	a.dataDir = t.TempDir()
	a.place(mygo.Point{X: 700, Y: 100})
	a.fx.w, a.fx.h = islandIdleW, islandIdleH
	// A resting island on its default row still is the notch: the corridor
	// exists and the filter is not parked.
	a.syncDragGuard()
	if !a.notchGuardOn() {
		t.Fatal("the resting island left the notch corridor unarmed")
	}
	if g := a.dragGuardRegion(); g.Height <= 0 || g.Y != a.dispBounds.Y {
		t.Fatalf("resting corridor = %+v, want the display's top edge down into the capsule", g)
	}
	for _, parked := range parks {
		if parked {
			t.Fatalf("an idle shell parked the filter: %v", parks)
		}
	}
	r := a.islandRect()
	a.mouseDown(true, 0, mygo.Point{X: r.X + r.Width/2, Y: r.Y + 10})
	if !a.islandPress.active {
		t.Fatal("the press did not arm the island gesture")
	}
	if !parkedNow() {
		t.Fatalf("the press did not park the notch filter: %v", parks)
	}
	// A purely horizontal swipe on the default row: the island follows the
	// pointer with no vertical travel the user did not make.
	dragIslandTick(t, a, mygo.Point{X: r.X + r.Width/2 + 120, Y: r.Y + 10}, true)
	if got := a.islandSpotNow(); got.Top != a.islandTop {
		t.Fatalf("the gesture was pushed down to top %d, want %d", got.Top, a.islandTop)
	}
	if !parkedNow() {
		t.Fatalf("the filter was unparked mid-gesture: %v", parks)
	}
	dragIslandTick(t, a, mygo.Point{X: r.X + r.Width/2 + 120, Y: r.Y + 10}, false)
	if a.islandPress.active {
		t.Fatal("the release left the gesture open")
	}
	if parkedNow() {
		t.Fatalf("the filter stayed parked after the release: %v", parks)
	}
	// Normal protection: the release left the island on its default row, so
	// the corridor is armed again.
	if !a.notchGuardOn() {
		t.Fatal("the corridor did not come back with the release")
	}
	if g := a.dragGuardRegion(); g.Height <= 0 || g.Y != a.dispBounds.Y {
		t.Fatalf("corridor after the release = %+v", g)
	}
}
