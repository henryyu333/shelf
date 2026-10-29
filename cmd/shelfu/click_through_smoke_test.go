//go:build darwin

package main

import (
	"testing"
	"time"

	"github.com/egoist/mygo"

	"shelf/internal/mac"
)

// While idle, the resident overlay must let clicks through everywhere —
// beside the notch where the hover wings grow into the menu bar — and
// while the wings show it must take them. Asked of WindowServer itself: a
// view refusing hitTest: still swallows the click, which is how the idle
// strip once blocked a 400×172 area under the notch. Run with
// SHELF_BASKET_SMOKE=1.
func TestIdleOverlayLetsClicksThrough(t *testing.T) {
	a := redesignShelf(t)
	oldCursor := cursorPoint
	t.Cleanup(func() { onMain(func() { cursorPoint = oldCursor }) })
	onMain(func() {
		a.settings.IslandNowPlaying = true
		fakeTrack(a, true) // the strip reserves the hover wings' size
		a.modules = []module{a.np}
		a.place(mygo.Screen.CursorScreenPoint())
		cursorPoint = func() mygo.Point {
			return mygo.Point{X: a.stripCenter(), Y: a.y + a.notchH/2}
		}
		a.peeking, a.peekProgress, a.visible, a.progress = false, 0, false, 0
		a.syncMousePassThrough()
		a.win.Invalidate()
	})
	time.Sleep(400 * time.Millisecond)
	check := func(wantShelf bool, state string) {
		windows := screenWindows(t)
		var foreign smokeWindow
		var hasForeign bool
		onMain(func() {
			// Another copy of ShelfU's panel at the notch takes these
			// points; the hit test could only report it, not ours.
			if w, ok := foreignIn(windows, mygo.Point{X: a.stripCenter(), Y: a.dispBounds.Y + a.notchH/2}, mac.StatusWindowLevel); ok {
				foreign, hasForeign = w, true
				return
			}
			shelf := mac.WindowNumber(a.win.NativeHandle())
			f := a.desiredFrame()
			screenH := float64(a.dispBounds.Y + a.dispBounds.Height)
			cx := float64(a.stripCenter())
			sampleW := f.Width
			if wantShelf {
				// The ear margins are transparent below the top arc.
				// Hit-test the visible wings; idle still checks the
				// entire native strip, margins included.
				sampleW = int(a.fx.w)
			}
			// The wings are barely taller than the notch: sample their own
			// band (top, notch bottom, near the shell's bottom edge), not
			// the old player's 172pt depth.
			for _, y := range []float64{6, float64(a.notchH) - 6, float64(f.Height) - 6} {
				for _, dx := range []float64{0, -float64(sampleW)/2 + 10, float64(sampleW)/2 - 10} {
					got := mac.WindowAtPoint(cx+dx, float64(a.dispBounds.Y)+y, screenH) == shelf
					if got != wantShelf {
						t.Errorf("%s: click at strip (%+.0f, %.0f) reached the shelf = %v, want %v (strip %+v)", state, dx, y, got, wantShelf, f)
					}
				}
			}
		})
		if hasForeign {
			t.Skipf("%s: window %d (pid %d) sits at the notch: another copy of ShelfU",
				state, foreign.ID, foreign.Owner)
		}
	}
	check(false, "idle")
	onMain(func() {
		a.setPeek(true)
	})
	time.Sleep(200 * time.Millisecond)
	check(true, "hover wings")
	peekRestore(t, a)
}

// While the completion capsule is up, WindowServer must hand presses on
// the drawn capsule to ShelfU and let everything beside it — the frame's
// transparent shake/growth margin — fall to whatever is underneath.
// Acceptance is pointer-gated per frame, so the cursor is parked where
// the press lands first. Run with SHELF_BASKET_SMOKE=1.
func TestCapsuleTakesClicksOnlyOnItsBody(t *testing.T) {
	a := redesignShelf(t)
	oldCursor := cursorPoint
	t.Cleanup(func() { onMain(func() { cursorPoint = oldCursor }) })
	onMain(func() {
		a.place(mygo.Screen.CursorScreenPoint())
		a.visible, a.progress, a.peeking, a.peekProgress = false, 0, false, 0
		a.toastStatus("已复制", time.Now().Add(4*time.Second), statusOK)
		a.win.Invalidate()
	})
	time.Sleep(800 * time.Millisecond) // grow-out settles; WindowServer
	// publishes the resized frame

	var shelf int
	var r mygo.Rectangle
	onMain(func() {
		shelf = mac.WindowNumber(a.win.NativeHandle())
		r = a.peekPillRect()
		if r.Width <= 0 {
			t.Fatal("no drawn capsule to hit-test")
		}
		cursorPoint = func() mygo.Point {
			return mygo.Point{X: r.X + r.Width/2, Y: r.Y + r.Height/2}
		}
		a.syncMousePassThrough()
	})
	time.Sleep(150 * time.Millisecond) // let WindowServer apply ordering

	screenH := float64(a.dispBounds.Y + a.dispBounds.Height)
	onMain(func() {
		for _, p := range [][2]float64{
			{float64(r.X + r.Width/2), float64(r.Y + r.Height/2)},
			{float64(r.X+r.Width) - 8, float64(r.Y+r.Height) - 4},
		} {
			hit := mac.WindowAtPoint(p[0], float64(a.dispBounds.Y)+p[1]-float64(a.y), screenH)
			if hit != shelf {
				t.Errorf("click inside the capsule body missed ShelfU at %v (hit %d)", p, hit)
			}
		}
	})

	// A press in the transparent margin — still inside the window's
	// frame — must reach whatever is underneath. The flip is applied on
	// the next cursor gate, so each probe point gets its own settle.
	f := a.desiredFrame()
	for _, p := range [][2]float64{
		{float64(r.X) - 14, float64(r.Y) + 4},
		{float64(r.X+r.Width) + 14, float64(r.Y) + 4},
	} {
		if p[0] <= float64(f.X) || p[0] >= float64(f.X+f.Width) {
			continue // outside the window: nothing to prove
		}
		px, py := int(p[0]), int(p[1])
		onMain(func() {
			cursorPoint = func() mygo.Point { return mygo.Point{X: px, Y: py + a.y} }
			a.syncMousePassThrough()
		})
		time.Sleep(150 * time.Millisecond)
		var hit int
		onMain(func() {
			hit = mac.WindowAtPoint(float64(px), float64(a.dispBounds.Y)+float64(py)-float64(a.y), screenH)
		})
		if hit == shelf {
			t.Errorf("click on the capsule's margin hit ShelfU at %v", p)
		}
	}
	onMain(func() {
		a.fb = feedback{}
		a.status = ""
		a.syncMousePassThrough()
	})
}

// While the failure capsule is shaking, the hit box must follow the drawn
// offset: a press where the capsule currently is reaches ShelfU; a press
// where it was (the unshifted box) falls through. Run with
// SHELF_BASKET_SMOKE=1.
func TestShakingCapsuleHitBoxFollowsTheOffset(t *testing.T) {
	a := redesignShelf(t)
	oldCursor := cursorPoint
	t.Cleanup(func() { onMain(func() { cursorPoint = oldCursor }) })
	onMain(func() {
		a.place(mygo.Screen.CursorScreenPoint())
		a.visible, a.progress, a.peeking, a.peekProgress = false, 0, false, 0
		a.toastStatus("转换失败：文件无法读取", time.Now().Add(4*time.Second), statusErr)
		a.win.Invalidate()
	})
	time.Sleep(600 * time.Millisecond) // grow-out done, shake in flight

	var shelf int
	var r mygo.Rectangle
	onMain(func() {
		shelf = mac.WindowNumber(a.win.NativeHandle())
		r = a.peekPillRect() // X already carries the shake offset
		if r.Width <= 0 {
			t.Fatal("no capsule to hit-test")
		}
		if a.fx.shakeX == 0 {
			t.Logf("shake offset is 0 this frame; the hit-box check runs unshifted")
		}
		cursorPoint = func() mygo.Point {
			return mygo.Point{X: r.X + r.Width/2, Y: r.Y + r.Height/2}
		}
		a.syncMousePassThrough()
	})
	time.Sleep(150 * time.Millisecond)

	screenH := float64(a.dispBounds.Y + a.dispBounds.Height)
	onMain(func() {
		r := a.peekPillRect() // re-read: the shake may have moved the box
		hit := mac.WindowAtPoint(float64(r.X+r.Width/2), float64(a.dispBounds.Y)+float64(r.Y+r.Height/2)-float64(a.y), screenH)
		if hit != shelf {
			t.Errorf("a press on the shaking capsule missed ShelfU (hit %d, shake=%.1f)", hit, a.fx.shakeX)
		}
	})
	onMain(func() {
		a.fb = feedback{}
		a.status = ""
		a.syncMousePassThrough()
	})
}

// Task D: during an incoming system drag the resident window must shrink
// to the notch's menu-bar band — WindowServer must see points below the
// menu bar at the notch's own X hit whatever is underneath (or nothing),
// never ShelfU. Only inside the band may the drop surface exist.
// Run with SHELF_BASKET_SMOKE=1.
func TestDragBandKeepsShelfInTheMenuBar(t *testing.T) {
	a := redesignShelf(t)
	oldCursor := cursorPoint
	var point mygo.Point
	t.Cleanup(func() {
		onMain(func() {
			cursorPoint = oldCursor
			a.dragging, a.dropHot, a.inBand, a.pointerDrag = false, false, false, false
			a.peeking, a.peekProgress, a.peekFrom = false, 0, 0
			a.syncMousePassThrough()
		})
	})
	onMain(func() {
		a.place(mygo.Screen.CursorScreenPoint())
		a.visible, a.progress, a.peeking, a.peekProgress = false, 0, false, 0
		a.fb = feedback{}
		a.np = nil
		a.settings.IslandNowPlaying = false
		point = mygo.Point{X: 100, Y: a.dispBounds.Y + a.dispBounds.Height - 40} // parked outside the band
		cursorPoint = func() mygo.Point { return point }
		a.dragging, a.pointerDrag, a.dropHot, a.inBand = true, true, false, false
		a.setPeek(true) // the drag-hint widen the band shows
		a.syncMousePassThrough()
		// Previous smoke tests may leave the shared window ordered out.
		// Prove the band of this visible test window, not the installed
		// copy beneath an absent fixture.
		a.win.ShowInactive()
		mac.OrderOverlayFront(a.winH)
	})
	time.Sleep(700 * time.Millisecond)

	var shelf int
	var frame mygo.Rectangle
	onMain(func() {
		shelf = mac.WindowNumber(a.win.NativeHandle())
		frame = a.win.Bounds()
	})
	barH := max(32, a.notchH)
	if frame.Height > barH+1 {
		t.Fatalf("drag frame %+v exceeds the %dpt menu bar", frame, barH)
	}
	if frame.Width > max(180, a.notchW)+2*dropBandPadX+2*earR+2 {
		t.Fatalf("drag frame %+v wider than the notch band", frame)
	}

	screenH := float64(a.dispBounds.Y + a.dispBounds.Height)
	// Below the menu bar at the notch's own centre X — before the drag
	// ever enters the band, WindowServer must find anything-but-ShelfU
	// there, at 8pt and 20pt below the bar alike.
	belowX := float64(a.stripCenter())
	for _, dy := range []float64{8, 20} {
		py := float64(a.dispBounds.Y) + float64(a.y) + float64(barH) + dy
		if hit := mac.WindowAtPoint(belowX, py, screenH); hit == shelf {
			t.Errorf("WindowAtPoint %.0f,%.0f hit ShelfU %.0fpt below the bar before band entry", belowX, py, dy)
		}
	}
	// Inside the band at the strip's centre, ShelfU's surface is present
	// (Spaces-edge protection requires the drop to land on a window).
	bandHit := mac.WindowAtPoint(belowX, float64(a.dispBounds.Y)+float64(a.y)+float64(barH)/2, screenH)
	if bandHit != shelf {
		t.Errorf("WindowAtPoint inside the band hit %d, want ShelfU %d", bandHit, shelf)
	}
	// Entering the band grows the frame past the menu bar so the guard's
	// landing row stays inside the window: the pill and the landing row
	// both hit ShelfU, not the apps underneath.
	onMain(func() {
		point = mygo.Point{X: a.stripCenter(), Y: a.notchY + a.notchH - 1}
		a.inBand, a.dropHot = true, true
		a.syncMousePassThrough()
	})
	time.Sleep(250 * time.Millisecond)
	onMain(func() { frame = a.win.Bounds() })
	g := a.dragGuardRegion()
	landingY := float64(a.dispBounds.Y) + float64(g.Y+g.Height) - 0.5
	if frame.Height <= g.Y+g.Height {
		t.Fatalf("in-band frame %+v does not cover the guard landing %d", frame, g.Y+g.Height)
	}
	landHit := mac.WindowAtPoint(belowX, landingY, screenH)
	if landHit != shelf {
		t.Errorf("WindowAtPoint on the guard landing row hit %d, want ShelfU %d", landHit, shelf)
	}
	// Leaving the band again folds the window back to the menu-bar strip.
	onMain(func() {
		point = mygo.Point{X: 100, Y: a.dispBounds.Y + a.dispBounds.Height - 40}
		a.inBand, a.dropHot = false, false
		a.syncMousePassThrough()
	})
	time.Sleep(250 * time.Millisecond)
	onMain(func() { frame = a.win.Bounds() })
	if frame.Height > barH+1 {
		t.Fatalf("after leaving the band the frame %+v did not fold back to the menu bar", frame)
	}
}
