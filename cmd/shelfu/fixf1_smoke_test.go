//go:build darwin

package main

// Fix F acceptance evidence, run with SHELF_BASKET_SMOKE=1 (the shared
// TestMain hosts the app loop). Shots land in /tmp/shelf-fixf1/:
//
//	wings-overview.png    the overview after a press on the hover wings
//	                      with a track playing
//	tools-scroll-mid.png  the tools page scrolled to the middle: no card
//	                      melts at either edge
//	tools-music-card.png  the 2 × 2 music card with the pointer on its
//	                      progress rail
//	pin-music-card.png    the same card pinned to the desktop
//
// The instance works in the smoke data dir and never touches
// ~/Library/Application Support/ShelfU.

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"shelf/internal/mac"
)

const fixf1ShotDir = "/tmp/shelf-fixf1"

// fixf1Shot captures a real window by CGWindowID into fixf1ShotDir. The
// smoke harness has no monitor tick, so the animation the real app would
// have run is stepped here until the springs rest, and the frame that
// follows is applied: a shot mid-overshoot would show the window's
// transparent margin beside the sheet.
func fixf1Shot(t *testing.T, a *shelf, w *mygo.Window, name string) string {
	t.Helper()
	for range 12 {
		onMain(func() {
			a.animate(time.Now())
			a.syncWindowFrame()
			a.win.Invalidate()
		})
		time.Sleep(30 * time.Millisecond)
	}
	time.Sleep(150 * time.Millisecond)
	num := mac.WindowNumber(w.NativeHandle())
	if num == 0 {
		t.Fatalf("%s: window has no CGWindowID", name)
	}
	if err := os.MkdirAll(fixf1ShotDir, 0o755); err != nil {
		t.Fatal(err)
	}
	shot := filepath.Join(fixf1ShotDir, name+".png")
	out, err := exec.Command("screencapture", "-x", "-o", fmt.Sprintf("-l%d", num), shot).CombinedOutput()
	if err != nil {
		t.Fatalf("%s: screencapture failed (%v): %s", name, err, out)
	}
	t.Logf("shot %s -> %s (CGWindowID=%d)", name, shot, num)
	return shot
}

// TestFixFWingsOpenTheOverview: with a track playing, a press on the hover
// wings expands the panel on the overview — never on the player page — and
// the real window shows that overview.
func TestFixFWingsOpenTheOverview(t *testing.T) {
	a := redesignShelf(t)
	onMain(func() {
		a.settings.IslandNowPlaying = true
		fakeTrack(a, true)
	})
	peekShelf(a)
	time.Sleep(300 * time.Millisecond)
	// The press goes through the same view the window draws, on the main
	// thread: the wings' own handler is what decides the page.
	var tab pageID
	var level int
	onMain(func() {
		view := ui.NewTester(a.view, sheetW, sheetH)
		if err := view.Click("正在播放：Fashion Killa · 展开 ShelfU"); err != nil {
			t.Errorf("pressing the wings: %v", err)
			return
		}
		tab, level = a.tab, a.currentLevel()
		a.progress, a.animationFrom = 1, 1
		a.animationStart = time.Time{}
		a.jumpLevel = true
		a.syncWindowFrame()
		a.win.Invalidate()
	})
	if tab != pageOverview || level != 1 {
		t.Fatalf("the wings opened %s (level %d), want the overview", tab, level)
	}
	shot := fixf1Shot(t, a, a.win, "wings-overview")
	if img, err := pngDecode(shot); err == nil {
		if box := opaqueBounds(img); box.Dx() < 200 {
			t.Fatalf("the overview shot is %dpt wide; the panel did not draw", box.Dx())
		}
	} else {
		t.Fatalf("%s: %v", shot, err)
	}
	motionClosed(a)
}

// TestFixFToolsPageShots: the tools grid still scrolls (a real wheel moves
// it) and the shot of the settled middle is the evidence that no card melts
// into the edges. The blur's absence is proven where it can be measured, in
// TestToolsGridDrawsNoEdgeBlur; the tray and clipboard pages keep their
// melt (TestSmokeClipListEdgeBlur).
func TestFixFToolsPageShots(t *testing.T) {
	a := redesignShelf(t)
	onMain(func() {
		a.settings.IslandNowPlaying = true
		fakeTrack(a, true)
		a.tab = pageTools
	})
	expandShelf(t, a)
	time.Sleep(400 * time.Millisecond)
	p := a.toolsPage()
	if p == nil {
		t.Fatal("the tools page is missing")
	}
	var maxY float32
	onMain(func() { maxY = p.scroll.MaxY })
	if maxY <= 0 {
		t.Fatalf("the tools grid does not scroll: MaxY=%.0f", maxY)
	}
	// A real wheel over the grid still scrolls it.
	var frame mygo.Rectangle
	onMain(func() { frame = a.frame })
	warpPointerTo(t, float64(frame.X+300), float64(frame.Y+170))
	time.Sleep(60 * time.Millisecond)
	onMain(func() {
		if !mac.SurfaceScroll(a.win.NativeHandle(), 300, 170, 40) {
			t.Error("the scroll event did not reach the tools grid")
		}
	})
	time.Sleep(80 * time.Millisecond)
	var scrolled float32
	onMain(func() { scrolled = p.scroll.Y })
	if scrolled <= 0 {
		t.Fatalf("the wheel left the grid at Y=%.1f: scrolling is broken", scrolled)
	}
	// The shot's state: the settled middle, both edges cut.
	onMain(func() {
		p.scroll.Y = maxY / 2
		a.win.Invalidate()
	})
	time.Sleep(250 * time.Millisecond)
	shot := fixf1Shot(t, a, a.win, "tools-scroll-mid")
	if img, err := pngDecode(shot); err != nil {
		t.Fatalf("%s: %v", shot, err)
	} else if box := opaqueBounds(img); box.Dx() < 200 {
		t.Fatalf("the tools shot is %dpt wide; the page did not draw", box.Dx())
	}
	onMain(func() { a.tab = pageTray })
	motionClosed(a)
}

// TestFixFToolsMusicCardShots: the 2 × 2 music card draws the full player in
// the panel (pointer on its progress rail) and in the pinned window.
func TestFixFToolsMusicCardShots(t *testing.T) {
	a := redesignShelf(t)
	onMain(func() {
		a.settings.IslandNowPlaying = true
		fakeTrack(a, true)
		a.tab = pageTools
	})
	expandShelf(t, a)
	time.Sleep(400 * time.Millisecond)
	p := a.toolsPage()
	onMain(func() { p.scroll.Y = 0; a.win.Invalidate() })
	time.Sleep(250 * time.Millisecond)
	// The card's rail, in window coordinates: the tester lays the same view
	// out in the window's content box.
	var rail ui.Rect
	onMain(func() {
		view := ui.NewTester(a.view, sheetW, sheetH)
		if r, ok := view.Find("播放进度"); ok {
			rail = r
		}
	})
	if rail.W <= 0 {
		t.Fatal("the 2×2 card drew no progress rail")
	}
	var frame mygo.Rectangle
	onMain(func() { frame = a.frame })
	if x, y := float64(frame.X)+float64(rail.X+rail.W/2), float64(frame.Y)+float64(rail.Y+rail.H/2); x > 0 && y > 0 {
		// A real pointer over the rail: the knob shows on hover.
		postMouse(t, x, y, 5)
	}
	time.Sleep(250 * time.Millisecond)
	fixf1Shot(t, a, a.win, "tools-music-card")

	// The pinned card: the same card on a backdrop of its own.
	area := mygo.Screen.PrimaryDisplay().WorkArea
	area.Y += 2
	area.Height -= 2
	newSmokePointerBackdrop(t, area)
	var it *pinItem
	onMain(func() {
		d := mygo.Screen.PrimaryDisplay()
		it = a.addPin("playing", 2, d.ID, pinFree, false,
			mygo.Rectangle{X: area.X + area.Width - 420, Y: area.Y + 220, Width: pinCardW2, Height: pinCardH})
	})
	if it == nil {
		t.Fatal("the music card could not be pinned")
	}
	defer onMain(func() { a.unpin(it, false) })
	time.Sleep(700 * time.Millisecond)
	for _, name := range []string{"播放进度", "音量", "上一首", "下一首"} {
		var found bool
		onMain(func() {
			view := ui.NewTester(it.view, pinCardW2, pinCardH)
			_, found = view.Find(name)
		})
		if !found {
			t.Fatalf("the pinned card lacks %q", name)
		}
	}
	fixf1Shot(t, a, it.win, "pin-music-card")
	onMain(func() { a.tab = pageTray })
	motionClosed(a)
}
