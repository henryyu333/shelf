//go:build darwin

package main

import (
	"testing"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"shelf/internal/mac"
	"shelf/internal/settings"
)

// dragShelf is a closed shelf on a notched display, mid incoming drag.
func dragShelf() *shelf {
	return &shelf{
		notchX: 600, notchY: 0, notchW: 180, notchH: 32,
		x: 410, y: 0,
		dispBounds:   mygo.Rectangle{X: 0, Y: 0, Width: 1470, Height: 956},
		dragging:     true,
		settings:     settings.Default(),
		thumbnails:   map[string]*ui.Bitmap{},
		thumbPending: map[string]bool{},
	}
}

// driveMonitor stubs the drag/cursor seams and runs monitorDrag with p.
func driveMonitor(t *testing.T, a *shelf, p mygo.Point, active bool, serial int64, types []string) {
	t.Helper()
	old := cursorPoint
	oldS, oldT, oldD := dragState, dragTypes, dragLeftDown
	cursorPoint = func() mygo.Point { return p }
	dragState = func() (bool, int64) { return active, serial }
	dragTypes = func() []string { return types }
	dragLeftDown = func() bool { return active }
	a.monitorDrag(time.Now())
	cursorPoint, dragState, dragTypes, dragLeftDown = old, oldS, oldT, oldD
}

// driveMonitorAt is driveMonitor with an explicit tick time, for the
// edgeLeaveDelay clock.
func driveMonitorAt(t *testing.T, a *shelf, now time.Time, p mygo.Point, active bool, serial int64, types []string) {
	t.Helper()
	old := cursorPoint
	oldS, oldT, oldD := dragState, dragTypes, dragLeftDown
	cursorPoint = func() mygo.Point { return p }
	dragState = func() (bool, int64) { return active, serial }
	dragTypes = func() []string { return types }
	dragLeftDown = func() bool { return active }
	a.monitorDrag(now)
	cursorPoint, dragState, dragTypes, dragLeftDown = old, oldS, oldT, oldD
}

// A drag that never entered the menu-bar band gets nothing: no inBand,
// no panel, a menu-bar-high frame and a menu-bar-high guard corridor —
// even on the rows directly below the bar inside the band's width.
func TestDragBelowTheBarIsNotTheBand(t *testing.T) {
	a := dragShelf()
	a.dragging = false
	cx := a.stripCenter()
	g := a.dragGuardRegion()
	if g.Height != menuBandH(a.notchH)-1 {
		t.Fatalf("unentered guard corridor %d high, want menu-bar %d", g.Height, menuBandH(a.notchH)-1)
	}
	for _, y := range []int{menuBandH(a.notchH), menuBandH(a.notchH) + 8, dropBandH(a.notchH) - 1} {
		driveMonitor(t, a, mygo.Point{X: cx, Y: y}, true, 1, []string{"public.file-url"})
		if a.inBand || a.dropHot || a.visible {
			t.Fatalf("Y=%d below the bar: inBand=%v dropHot=%v visible=%v", y, a.inBand, a.dropHot, a.visible)
		}
		if f := a.desiredFrame(); f.Height > menuBandH(a.notchH) {
			t.Fatalf("Y=%d: frame %dpt tall exceeds the menu bar", y, f.Height)
		}
		if g := a.dragGuardRegion(); g.Height != menuBandH(a.notchH)-1 {
			t.Fatalf("Y=%d: corridor grew to %d without band entry", y, g.Height)
		}
	}
}

// monitorDrag must walk the band in and out: below-bar rows do not
// enter, the menu bar's own rows do (panel opens edgeOpened), the hold
// range keeps the band through the landing row, and leaving both band
// and panel past edgeLeaveDelay collapses it again.
func TestMonitorDragBandTransitions(t *testing.T) {
	a := dragShelf()
	a.dragging = false // the monitor owns the flag; start idle
	cx := a.stripCenter()
	// Outside the band's entry edge (below the bar): hint only.
	driveMonitor(t, a, mygo.Point{X: cx, Y: a.notchY + menuBandH(a.notchH) + 8}, true, 1, []string{"public.file-url"})
	if !a.dragging || a.inBand || a.dropHot || a.visible {
		t.Fatalf("below bar: dragging=%v inBand=%v dropHot=%v visible=%v", a.dragging, a.inBand, a.dropHot, a.visible)
	}
	if f := a.desiredFrame(); f.Height > menuBandH(a.notchH) {
		t.Fatalf("below the bar the frame is %dpt tall, above the menu bar", f.Height)
	}
	// Inside the menu bar: the drag enters the band and the panel opens.
	driveMonitor(t, a, mygo.Point{X: cx, Y: a.notchY + a.notchH - 1}, true, 1, []string{"public.file-url"})
	if !a.inBand || !a.dropHot || !a.visible || !a.edgeOpened {
		t.Fatalf("inside band: inBand=%v dropHot=%v visible=%v edgeOpened=%v", a.inBand, a.dropHot, a.visible, a.edgeOpened)
	}
	if g := a.dragGuardRegion(); g.Height != dropBandH(a.notchH)-1 {
		t.Fatalf("in-band corridor %d high, want landing %d", g.Height, dropBandH(a.notchH)-1)
	}
	// The hold range keeps the band on the landing row below the bar.
	driveMonitor(t, a, mygo.Point{X: cx, Y: a.notchY + dropBandH(a.notchH) - 1}, true, 1, []string{"public.file-url"})
	if !a.inBand {
		t.Fatal("the landing row dropped out of the band")
	}
	// …and loses it one row lower, or beside the band's width.
	driveMonitor(t, a, mygo.Point{X: cx, Y: a.notchY + dropBandH(a.notchH)}, true, 1, []string{"public.file-url"})
	if a.inBand {
		t.Fatal("one row past the landing row still counts as in-band")
	}
	a.inBand = true
	driveMonitor(t, a, mygo.Point{X: a.notchX - dropBandPadX - 1, Y: 10}, true, 1, []string{"public.file-url"})
	if a.inBand {
		t.Fatal("outside the band's width still counts as in-band")
	}
	// Re-entering the bar re-opens; then leaving band and panel past
	// edgeLeaveDelay collapses the edge-opened panel.
	driveMonitor(t, a, mygo.Point{X: cx, Y: a.notchY + a.notchH - 1}, true, 1, []string{"public.file-url"})
	if !a.inBand || !a.visible {
		t.Fatalf("re-entering the bar: inBand=%v visible=%v", a.inBand, a.visible)
	}
	a.animationStart = time.Time{} // the open animation is done
	driveMonitor(t, a, mygo.Point{X: 100, Y: 800}, true, 1, []string{"public.file-url"})
	driveMonitorAt(t, a, time.Now().Add(edgeLeaveDelay+time.Millisecond), mygo.Point{X: 100, Y: 800}, true, 1, []string{"public.file-url"})
	if a.visible {
		t.Fatal("leaving the band did not collapse the edge-opened panel")
	}
	if a.dropHot || a.inBand {
		t.Fatalf("after leaving: dropHot=%v inBand=%v", a.dropHot, a.inBand)
	}
	// Drag ends: the strip comes back to the no-drag resident frame.
	idle := (&shelf{notchX: 600, notchY: 0, notchW: 180, notchH: 32,
		x: 410, y: 0, settings: settings.Default()}).desiredFrame()
	driveMonitor(t, a, mygo.Point{X: 100, Y: 800}, false, 1, nil)
	if a.dragging || a.dropHot || a.inBand {
		t.Fatalf("drag end: dragging=%v dropHot=%v inBand=%v", a.dragging, a.dropHot, a.inBand)
	}
	if got := a.desiredFrame(); got != idle {
		t.Fatalf("after the drag the frame %+v did not return to idle %+v", got, idle)
	}
}

// A suppressed drag that is already in-band keeps its window tall
// enough for the guard's landing row — the clamped cursor can never
// escape onto the bar — and the corridor reaches that landing row.
func TestDragGuardLandsBelowMenuBarInsideTheWindow(t *testing.T) {
	a := dragShelf()
	a.dragging = false
	cx := a.stripCenter()
	// Enter the band while suppressed: no pill, no panel, tall window.
	// dragSerial matches the stub so the monitor keeps the flag.
	a.dragSerial, a.suppressed = 1, true
	driveMonitor(t, a, mygo.Point{X: cx, Y: a.notchY + a.notchH - 1}, true, 1, []string{"public.file-url"})
	if !a.inBand || a.dropHot || a.visible {
		t.Fatalf("suppressed entry: inBand=%v dropHot=%v visible=%v", a.inBand, a.dropHot, a.visible)
	}
	g := a.dragGuardRegion()
	landing := g.Y + g.Height
	if landing <= menuBandH(a.notchH) {
		t.Fatalf("in-band landing %d is inside the %dpt menu bar", landing, menuBandH(a.notchH))
	}
	f := a.desiredFrame()
	if f.Height < dropBandH(a.notchH) {
		t.Fatalf("suppressed in-band frame %+v does not reach dropBandH", f)
	}
	if landing >= f.Height {
		t.Fatalf("landing %d outside frame %+v", landing, f)
	}
	// Unsuppressed in-band: same tall frame, landing inside.
	a.suppressed, a.dropHot = false, true
	f = a.desiredFrame()
	if landing >= f.Height {
		t.Fatalf("dropHot: landing %d outside frame %+v", landing, f)
	}
	// Before any entry the corridor is menu-bar high and so is the
	// frame — the push lands on the bar's last row, already covered.
	a.suppressed, a.inBand, a.dropHot = false, false, false
	g = a.dragGuardRegion()
	if g.Height != menuBandH(a.notchH)-1 {
		t.Fatalf("unentered corridor %d high, want %d", g.Height, menuBandH(a.notchH)-1)
	}
	if f := a.desiredFrame(); f.Height > menuBandH(a.notchH) {
		t.Fatalf("unentered frame %dpt tall exceeds the menu bar", f.Height)
	}
}

// The incoming-drag frame must never exceed the menu bar's height while
// nothing taller is drawn: the widened hint stays band-high, and the
// wings/capsule's taller frames are not retained through a drag.
func TestIncomingDragFrameStaysInMenuBand(t *testing.T) {
	a := dragShelf()
	a.settings.IslandNowPlaying = true
	a.np = &nowPlayingModule{host: a, have: true, last: mac.NowPlayingState{Title: "Track"}}
	f := a.desiredFrame()
	bandW := max(180, a.notchW) + 2*dropBandPadX
	hintW := int(180+dropHintExtraW) + 2*earR // band may widen ±ears to cover the hint's corners
	if f.Height > max(32, a.notchH) {
		t.Fatalf("incoming-drag frame %dpt tall left the menu bar", f.Height)
	}
	if f.Width != max(bandW, hintW) {
		t.Fatalf("incoming-drag frame %dpt wide, want %d (band or hint+ears)", f.Width, max(bandW, hintW))
	}
	if f.X != a.stripCenter()-f.Width/2 || f.Y != a.y {
		t.Fatalf("incoming-drag frame %+v lost the notch anchor", f)
	}
	// Outside the band the drawn hint is also band-high: frame and shell
	// agree that nothing reaches below the menu bar.
	if w, h := a.peekSize(); h > float32(max(32, a.notchH)) {
		t.Fatalf("drag hint %.0fx%.0f exceeds the menu bar", w, h)
	}
}

// Entering the band aims the frame at the drop pill; mid-morph it still
// covers the drawn shell plus ears, and a fold remnant taller than the
// pill keeps its own room.
func TestDropBandFrameFollowsTheDrawnPill(t *testing.T) {
	a := dragShelf()
	a.dropHot = true
	f := a.desiredFrame()
	pillW := 180 + dropPillExtraW
	if f.Height != 32+dropPillBody || f.Width != pillW+2*earR {
		t.Fatalf("in-band frame %+v must cover the %dx%d pill plus ears", f, pillW, 32+dropPillBody)
	}
	// The pill mid-grow: the frame tracks the drawn shell plus ears.
	a.fx.peek, a.fx.w, a.fx.h = true, 240, 48
	f = a.desiredFrame()
	if f.Height != 64 || f.Width != max(pillW, 240)+2*earR {
		t.Fatalf("frame %+v does not follow the drawn pill", f)
	}
	// A fold remnant taller than the pill keeps its own room.
	a.fx.w, a.fx.h = 300, 90
	f = a.desiredFrame()
	if f.Height != 91 || f.Width != 300+2*earR {
		t.Fatalf("frame %+v clipped the fold remnant 300x90", f)
	}
}

// ShelfU's own drag-out keeps the resident strip — no band, no hint.
func TestOwnDragKeepsTheNormalStrip(t *testing.T) {
	a := dragShelf()
	a.ownDrag = true
	f := a.desiredFrame()
	bandW := max(180, a.notchW) + 2*dropBandPadX
	if f.Width == bandW && f.Height == max(32, a.notchH) {
		t.Fatalf("own drag-out shrank to the drop band %+v", f)
	}
	if w, h := a.peekSize(); w == 180+dropPillExtraW || h == 32+dropPillBody {
		t.Fatalf("own drag-out drew the drop pill %.0fx%.0f", w, h)
	}
}

// Shake-to-summon arms only for drags carrying files or image data.
func TestDragSummonsBasketOnlyForFilesAndImages(t *testing.T) {
	for _, types := range [][]string{
		{"public.file-url"},
		{"NSFilenamesPboardType", "com.apple.finder.node"},
		{"com.apple.pasteboard.promised-file-url"},
		{"com.apple.NSFilePromiseItemMetaData"},
		{"public.png"},
		{"public.tiff", "public.utf8-plain-text"},
		{"public.heic"},
		{"public.file-promise"},
	} {
		if !dragSummonsBasket(types) {
			t.Fatalf("%v must summon the basket", types)
		}
	}
	for _, types := range [][]string{
		nil,
		{"public.utf8-plain-text"},
		{"public.utf8-plain-text", "NSStringPboardType"}, // Tern tab drag
		{"public.url", "public.utf8-plain-text"},         // a dragged link
		{"public.data"},
	} {
		if dragSummonsBasket(types) {
			t.Fatalf("%v must not summon the basket", types)
		}
	}
}

// The mouse-down observer and the peek shape both see the same drag band:
// outside it a drag press belongs to the app underneath, inside it the
// band accepts.
func TestIncomingDragAcceptsOnlyInsideTheBand(t *testing.T) {
	a := dragShelf()
	bandW := max(180, a.notchW) + 2*dropBandPadX
	f := a.desiredFrame()
	hintW := int(180+dropHintExtraW) + 2*earR
	if f.Width != max(bandW, hintW) {
		t.Fatalf("drag frame %d wide, want %d (band or hint+ears)", f.Width, max(bandW, hintW))
	}
	// The band's vertical reach is the guard's landing row: anything
	// below it is neither in the band nor the frame, so the drop
	// belongs to whatever is underneath.
	below := mygo.Point{X: a.notchX + a.notchW/2, Y: a.notchY + dropBandH(a.notchH) + 1}
	if a.dropBand(below) {
		t.Fatalf("point %+v below the band hit it", below)
	}
	if below.Y < f.Y+f.Height && below.X >= f.X && below.X < f.X+f.Width {
		t.Fatalf("point %+v below the band is inside the drag frame %+v", below, f)
	}
}

func TestEdgeLeaveWaitsForToolsPress(t *testing.T) {
	a := dragShelf()
	a.visible, a.edgeOpened, a.progress, a.tab = true, true, 1, pageTools
	p := a.toolsPage()
	p.editing = true
	p.press = &toolPress{id: "zip", dragging: true}
	now := time.Now()
	far := mygo.Point{X: 100, Y: 800}
	driveMonitorAt(t, a, now, far, false, 3, nil)
	driveMonitorAt(t, a, now.Add(2*edgeLeaveDelay), far, false, 3, nil)
	if !a.visible || p.press == nil {
		t.Fatal("edge leave hid the panel and aborted an active tools press")
	}
	// Once the gesture ends, start a fresh leave countdown.
	p.press = nil
	driveMonitorAt(t, a, now.Add(3*edgeLeaveDelay), far, false, 3, nil)
	if !a.visible {
		t.Fatal("the held gesture consumed the leave delay")
	}
	driveMonitorAt(t, a, now.Add(4*edgeLeaveDelay), far, false, 3, nil)
	if a.visible {
		t.Fatal("the panel did not resume passive hiding after the press ended")
	}
}

// A press delivered to our own window is the user's gesture, not an
// incoming drag: the notch corridor must stay parked for as long as the
// button is held. Otherwise a 1–3pt wobble on a band icon that lies under
// the corridor (the AI usage icon is the only one that does) gets the
// pointer warped onto the corridor's landing row and the press is lost.
func TestOwnWindowPressParksTheNotchCorridor(t *testing.T) {
	var parks []bool
	oldPark := parkNotchFilter
	parkNotchFilter = func(parked bool) { parks = append(parks, parked) }
	t.Cleanup(func() { parkNotchFilter = oldPark })
	parkedNow := func() bool { return len(parks) > 0 && parks[len(parks)-1] }

	oldCursor, oldState, oldTypes, oldDown := cursorPoint, dragState, dragTypes, dragLeftDown
	t.Cleanup(func() { cursorPoint, dragState, dragTypes, dragLeftDown = oldCursor, oldState, oldTypes, oldDown })
	a := dragShelf()
	a.dragging = false
	icon := mygo.Point{X: a.notchX - 25 + 7, Y: 12}
	held := false
	cursorPoint = func() mygo.Point { return icon }
	dragState = func() (bool, int64) { return false, 7 }
	dragTypes = func() []string { return nil }
	dragLeftDown = func() bool { return held }

	a.syncDragGuard()
	if parkedNow() {
		t.Fatal("an idle shelf parked the corridor")
	}
	// A press in another app is an incoming gesture: never parked.
	a.mouseDown(false, 0, icon)
	if parkedNow() {
		t.Fatal("a press outside our window parked the corridor")
	}
	// Our own press parks it for the whole hold, even as the button
	// travels the 2pt a real click wobbles.
	a.mouseDown(true, 0, icon)
	if !a.ownPress || !parkedNow() {
		t.Fatalf("the press did not park the corridor: ownPress=%v parks=%v", a.ownPress, parks)
	}
	held = true
	cursorPoint = func() mygo.Point { return mygo.Point{X: icon.X, Y: icon.Y + 2} }
	a.monitorDrag(time.Now())
	if !parkedNow() {
		t.Fatal("the held press was unparked by a 2pt drag")
	}
	// The release brings normal protection back.
	held = false
	a.monitorDrag(time.Now())
	if a.ownPress || parkedNow() {
		t.Fatalf("the release left the corridor parked: ownPress=%v parks=%v", a.ownPress, parks)
	}
	// Dragging our own file out is a real drag: the corridor must protect
	// the top edge again the moment it starts.
	held = true
	a.mouseDown(true, 0, icon)
	if !parkedNow() {
		t.Fatal("the second press did not park the corridor")
	}
	a.beginOwnDrag()
	if parkedNow() {
		t.Fatal("our own drag out left the corridor parked")
	}
	a.ownDrag = false
}
