//go:build darwin

package main

// The shell's mouse shape and the level the window follows. The overlay is
// a rectangle above the menu bar while the shell inside it is not, and
// WindowServer routes a press to a window before any view is hit-tested, so
// the overlay may only accept the mouse while the pointer is on the drawn
// shell. Both rules failed the stage review: a settled sheet's whole window
// took the menu-bar icons beside it, and an island opened on the sheet's
// 451pt box.

import (
	"testing"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

// settleFrames draws n frames 16 ms apart, the monitor loop's cadence.
func settleFrames(view *ui.Tester, n int) {
	for range n {
		view.Frame()
		view.Advance(16 * time.Millisecond)
	}
}

// TestShellHitIsTheDrawnShape checks the shape test itself: body, ears and
// rounded bottom corners, with the capsule's shake following the shape.
func TestShellHitIsTheDrawnShape(t *testing.T) {
	a := &shelf{notchX: 600, notchW: 180, notchH: 32, y: 0,
		fx: shellMotion{w: sheetW, h: sheetH, levelK: 1}}
	cx := a.stripCenter()
	right, left := cx+sheetW/2, cx-sheetW/2
	body := a.y + 125
	for _, tc := range []struct {
		name string
		p    mygo.Point
		want bool
	}{
		{"center", mygo.Point{X: cx, Y: body}, true},
		{"just inside the right flank", mygo.Point{X: right - 1, Y: 20}, true},
		{"just outside the right flank", mygo.Point{X: right + 1, Y: 20}, false},
		{"5pt outside the right flank", mygo.Point{X: right + 5, Y: 20}, false},
		{"just outside the left flank", mygo.Point{X: left - 1, Y: 20}, false},
		// The ear: the top edge runs earR past the flank at y=0 and meets
		// it earR below, so 2pt down is black 4.2pt out — 5pt is not.
		{"outer ear corner", mygo.Point{X: right + 5, Y: 2}, false},
		{"inside the ear", mygo.Point{X: right + 2, Y: 2}, true},
		{"inside the left ear", mygo.Point{X: left - 2, Y: 2}, true},
		// The bottom rounds off by 24pt at the sheet level.
		{"beside the bottom corner", mygo.Point{X: right - 2, Y: sheetH - 2}, false},
		{"inside the bottom corner", mygo.Point{X: right - 20, Y: sheetH - 2}, true},
		{"below the shell", mygo.Point{X: cx, Y: sheetH + 1}, false},
	} {
		if got := a.overShell(tc.p); got != tc.want {
			t.Errorf("%s at %v: overShell = %v, want %v", tc.name, tc.p, got, tc.want)
		}
	}
	// The capsule's shake moves the drawn shell, and the hit shape with it.
	a.fx.shakeX = 5
	if !a.overShell(mygo.Point{X: right + 3, Y: 20}) {
		t.Error("the shaken shell is not where it is drawn")
	}
	a.fx.shakeX = 0
	// A point on the top edge outside the ears' reach is transparent.
	if a.overShell(mygo.Point{X: right + earR + 1, Y: 20}) {
		t.Error("the frame margin beside the shell takes the mouse")
	}
}

// TestSettledSheetTakesMouseOnlyOnItsShell is the review's case: with the
// sheet settled, a press 5pt right of the shell's edge at y = 20 sits
// inside the window (the frame keeps the ear margin) and above the menu
// bar, and must go through to the menu-bar icon there; a press on the
// shell must reach ShelfU.
func TestSettledSheetTakesMouseOnlyOnItsShell(t *testing.T) {
	// A settled sheet, headless: the shell's geometry is the drawn one and
	// winH marks the live surface the gate would apply to.
	a := motionShelf()
	a.level, a.visible = 2, true
	a.fx = shellMotion{w: sheetW, h: sheetH, openK: 1, levelK: 1}
	a.winH = 1
	old := cursorPoint
	t.Cleanup(func() { cursorPoint = old })

	f := a.desiredFrame()
	shellRight := a.stripCenter() + int(a.fx.w/2)
	outside := mygo.Point{X: shellRight + 5, Y: a.y + 20}
	if outside.X < f.X || outside.X >= f.X+f.Width || outside.Y < f.Y || outside.Y >= f.Y+f.Height {
		t.Fatalf("the probe %v is outside the window %+v: it would not test the hit shape", outside, f)
	}
	cursorPoint = func() mygo.Point { return outside }
	a.syncMousePassThrough()
	if !a.ignoringMouse {
		t.Fatalf("the transparent margin 5pt beside the shell (window %+v) still takes the mouse", f)
	}
	inside := mygo.Point{X: shellRight - 20, Y: a.y + 20}
	cursorPoint = func() mygo.Point { return inside }
	a.syncMousePassThrough()
	if a.ignoringMouse {
		t.Fatal("a press on the shell itself is ignored")
	}
	// The back row's icons are inside the shell: a press on them reaches
	// ShelfU too, whatever the pointer did a moment before.
	cursorPoint = func() mygo.Point { return mygo.Point{X: a.stripCenter(), Y: a.y + 40} }
	a.syncPointerGate()
	if a.ignoringMouse {
		t.Fatal("the pointer gate did not follow the pointer back onto the shell")
	}
}

// TestPointerGateHandsTheMouseBackOnMove: a settled panel draws no frames,
// so the gate must be re-applied from pointer moves alone — the case where
// the pointer leaves the shell and the next press belongs to the menu bar.
func TestPointerGateHandsTheMouseBackOnMove(t *testing.T) {
	a := motionShelf()
	a.level, a.visible = 2, true
	a.fx = shellMotion{w: sheetW, h: sheetH, openK: 1, levelK: 1}
	a.winH = 1
	old := cursorPoint
	t.Cleanup(func() { cursorPoint = old })
	shellRight := a.stripCenter() + int(a.fx.w/2)
	cursorPoint = func() mygo.Point { return mygo.Point{X: shellRight - 10, Y: a.y + 100} }
	a.syncMousePassThrough()
	if a.ignoringMouse {
		t.Fatal("the shell does not take the mouse to begin with")
	}
	// No frame is drawn between these two: only the move reports it.
	cursorPoint = func() mygo.Point { return mygo.Point{X: shellRight + 8, Y: a.y + 100} }
	a.syncPointerGate()
	if !a.ignoringMouse {
		t.Fatal("the pointer left the shell but the window kept the mouse")
	}
	// And back: the press that follows a move in must reach the shell.
	cursorPoint = func() mygo.Point { return mygo.Point{X: shellRight - 10, Y: a.y + 100} }
	a.syncPointerGate()
	if a.ignoringMouse {
		t.Fatal("the pointer came back onto the shell but the window still ignored it")
	}
}

// TestOpenUnderStillPointerTakesTheMouse: the user clicks the notch to open
// the island and clicks an icon on it without moving. No move event
// arrives to re-ask the gate with, and the drawn shell is still the closed
// strip, so the shape the state settles at must count: the press belongs to
// the island, not to whatever sits under the window.
func TestOpenUnderStillPointerTakesTheMouse(t *testing.T) {
	a := motionShelf()
	a.level = 1 // the plain toggle opens the island
	a.winH = 1  // pretend a live native surface
	a.fx = shellMotion{}
	old := cursorPoint
	t.Cleanup(func() { cursorPoint = old })
	cursorPoint = func() mygo.Point {
		// The island's body, below the notch band and above its bottom
		// corner: where its page icons sit.
		return mygo.Point{X: a.stripCenter(), Y: a.y + overviewH - 8}
	}
	a.syncMousePassThrough()
	if !a.ignoringMouse {
		t.Fatal("the closed shelf takes the mouse before it is opened")
	}
	a.show(false)
	if a.ignoringMouse {
		t.Fatal("the opening island ignores a press under a still pointer")
	}
	// And the transparent margin beside the island still belongs to the
	// window underneath, before and after the opening.
	cursorPoint = func() mygo.Point {
		return mygo.Point{X: a.stripCenter() + overviewW/2 + 8, Y: a.y + 20}
	}
	a.syncPointerGate()
	if !a.ignoringMouse {
		t.Fatal("the island's transparent margin takes the mouse")
	}
}

// TestLevelSwitchUnderStillPointerKeepsTheMouse: the panel is on the island
// and the pointer rests in the column the sheet will grow into. The switch
// to the sheet changes the shell's shape under a still pointer, so the gate
// must be re-asked from the state, not from a move event: the press is the
// sheet's.
func TestLevelSwitchUnderStillPointerKeepsTheMouse(t *testing.T) {
	a := motionShelf()
	a.level, a.visible = 1, true
	a.fx = shellMotion{w: overviewW, h: overviewH}
	a.winH = 1
	old := cursorPoint
	t.Cleanup(func() { cursorPoint = old })
	// The two sizes share their width: the column the page grows into is
	// the one below the overview's bottom edge.
	cursorPoint = func() mygo.Point { return mygo.Point{X: a.stripCenter(), Y: a.y + overviewH + 20} }
	a.syncMousePassThrough()
	if !a.ignoringMouse {
		t.Fatal("the overview's transparent margin takes the mouse before the switch")
	}
	a.setLevel(2)
	if a.ignoringMouse {
		t.Fatal("the sheet ignores a press under a still pointer it now covers")
	}
	// Outside both shells the window stays click-through.
	cursorPoint = func() mygo.Point { return mygo.Point{X: a.stripCenter() + sheetW/2 + 8, Y: a.y + 20} }
	a.syncPointerGate()
	if !a.ignoringMouse {
		t.Fatal("the transparent margin beside the sheet takes the mouse")
	}
	// Below the sheet's bottom edge it stays click-through too.
	cursorPoint = func() mygo.Point { return mygo.Point{X: a.stripCenter(), Y: a.y + sheetH + 2} }
	a.syncPointerGate()
	if !a.ignoringMouse {
		t.Fatal("a press below the sheet is swallowed by the overlay")
	}
}

// TestIslandOpeningFramesStayOnTheIslandBox: opening the island used to
// hold the sheet's 451pt frame for the whole spring. Every frame of the
// flight must stay within the island's box and the spring's room.
func TestIslandOpeningFramesStayOnTheIslandBox(t *testing.T) {
	a := motionWindow()
	a.level = 1
	view := ui.NewTester(a.view, sheetW, sheetH)
	view.Frame() // the idle frame the spring starts from
	a.visible = true
	iw, ih := islandFrame()
	for i := range 120 {
		if f := a.desiredFrame(); f.Width > iw || f.Height > ih {
			t.Fatalf("frame %d: window %d×%d exceeds the island's %d×%d", i, f.Width, f.Height, iw, ih)
		}
		if f := a.desiredFrame(); f.Width < int(a.fx.w+0.5)+2*earR || f.Height < int(a.fx.h+0.5) {
			t.Fatalf("frame %d: window %d×%d clips the shell %.1f×%.1f", i, f.Width, f.Height, a.fx.w, a.fx.h)
		}
		view.Frame()
		view.Advance(16 * time.Millisecond)
	}
	if a.fx.openMoving {
		t.Fatal("the opening spring never came to rest")
	}
	if f := a.desiredFrame(); f.Width != iw || f.Height != overviewH {
		t.Fatalf("settled island frame = %d×%d, want %d×%d", f.Width, f.Height, iw, overviewH)
	}
}

// TestReopenAfterASheetCloseStartsOnTheIsland: closing from the sheet left
// the level progress at the sheet, so the next opening drew one frame of
// the sheet's box. The collapse resets it, and the first frame of the
// reopen is the island's.
func TestReopenAfterASheetCloseStartsOnTheIsland(t *testing.T) {
	a := motionWindow()
	a.level, a.visible = 2, true
	view := ui.NewTester(a.view, sheetW, sheetH)
	settleFrames(view, 80)
	if a.fx.levelK < 0.999 {
		t.Fatalf("the sheet never settled: levelK %.2f", a.fx.levelK)
	}
	a.hide(hideExplicit)
	if a.fx.levelK != 0 {
		t.Fatalf("the collapse left the level progress at %.2f", a.fx.levelK)
	}
	settleFrames(view, 40) // the fold
	// show() places the window at the pointer, which a headless shelf has
	// no screen module for: drop the placeholder window around the call,
	// then put it back for the frame that matters.
	win := a.win
	a.win = nil
	a.show(false)
	a.win = win
	iw, _ := islandFrame()
	if f := a.desiredFrame(); f.Width != iw {
		t.Fatalf("the reopened window is %d wide, want the island's %d", f.Width, iw)
	}
	view.Frame()
	if a.fx.levelK > 0.001 {
		t.Fatalf("the first reopened frame drew the sheet's level (levelK %.2f)", a.fx.levelK)
	}
	if a.fx.w > overviewW+1 {
		t.Fatalf("the first reopened frame drew a %.1fpt shell, wider than the island's %d", a.fx.w, overviewW)
	}
}
