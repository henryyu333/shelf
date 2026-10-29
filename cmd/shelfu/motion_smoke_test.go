//go:build darwin

package main

// Real-window proof for the motion work: the elastic opening (a numeric
// trace of the drawn shell, frame by frame, plus a shot), the completion
// capsule in its three states, its yield to hover, and the bottom fade of a
// scrolled clipboard list. Run with SHELF_BASKET_SMOKE=1; PNGs
// land in /tmp/shelf-fix/motion/. The instance reads the smoke data dir, never
// ~/Library/Application Support/ShelfU.

import (
	"fmt"
	"image"
	"image/png"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/egoist/mygo"
	"shelf/internal/mac"
)

const motionShotDir = "/tmp/shelf-fix/motion"

// motionShot captures the notch window to motionShotDir after
// letting wait pass, so a mid-animation or mid-shake state is what shows.
func motionShot(t *testing.T, a *shelf, name string, wait time.Duration) string {
	t.Helper()
	if a.win == nil {
		t.Fatal("no notch window")
	}
	num := mac.WindowNumber(a.win.NativeHandle())
	if num == 0 {
		t.Fatalf("%s: window has no CGWindowID", name)
	}
	time.Sleep(wait)
	// The smoke harness never runs the 30 ms tick that follows the drawing,
	// so apply the frame the real app would have by now.
	onMain(a.syncWindowFrame)
	time.Sleep(120 * time.Millisecond)
	if err := os.MkdirAll(motionShotDir, 0o755); err != nil {
		t.Fatal(err)
	}
	shot := filepath.Join(motionShotDir, name+".png")
	out, err := exec.Command("screencapture", "-x", "-o", fmt.Sprintf("-l%d", num), shot).CombinedOutput()
	if err != nil {
		t.Fatalf("%s: screencapture failed (%v): %s", name, err, out)
	}
	t.Logf("shot %s -> %s", name, shot)
	return shot
}

// pngDecode reads a screenshot back into pixels for extent checks.
func pngDecode(path string) (image.Image, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return png.Decode(f)
}

// motionClosed puts the real window into the resting closed state.
func motionClosed(a *shelf) {
	onMain(func() {
		a.hide(hidePassive)
		a.visible, a.edgeOpened = false, false
		a.progress, a.animationFrom = 0, 0
		a.animationStart = time.Time{}
		a.peeking = false
		a.peekProgress, a.peekFrom = 0, 0
		a.peekAnimStart = time.Time{}
		a.fb = feedback{}
		a.status, a.statusUntil, a.statusSeen, a.statusSeenAt = "", time.Time{}, 0, time.Time{}
		a.place(mygo.Screen.CursorScreenPoint())
		a.win.SetOpacity(1)
		a.win.ShowInactive()
		a.syncMousePassThrough()
		a.syncWindowFrame()
		a.win.Invalidate()
	})
}

// TestSmokeOpeningIsElasticAndNotClipped traces the drawn shell of the real
// window while it opens: the height must overshoot its final size, and the
// window must hold the shell in every sampled frame.
func TestSmokeOpeningIsElasticAndNotClipped(t *testing.T) {
	a := redesignShelf(t)
	motionClosed(a)
	time.Sleep(300 * time.Millisecond)
	openSheet := func() {
		onMain(func() {
			a.show(false)
			a.setLevel(2)
			a.jumpLevel = true
		})
	}
	openSheet()
	// A screenshot mid-flight proves the look; it takes its own opening so
	// its frame-sync sleep cannot sit on the trace's peak.
	motionShot(t, a, "open-mid", 90*time.Millisecond)
	motionClosed(a)
	time.Sleep(200 * time.Millisecond)
	openSheet()
	final := float32(0)
	peak := float32(0)
	for i := range 80 {
		var h, w float32
		var frame mygo.Rectangle
		onMain(func() {
			h, w, frame = a.fx.h, a.fx.w, a.desiredFrame()
		})
		peak = max(peak, h)
		final = h
		if float64(frame.Width) < float64(w) || frame.Height < int(h+0.5) {
			// Either dimension alone clipping counts: the shell must fit.
			t.Fatalf("frame %d: window %+v cuts the shell %.1fx%.1f", i, frame, w, h)
		}
		// Sampling by hopping to the main thread starves the window's own
		// frames if it is too tight; 10 ms keeps both the trace and the draw
		// dense enough.
		time.Sleep(10 * time.Millisecond)
	}
	t.Logf("drawn shell: peak %.1f, later %.1f", peak, final)
	if peak <= final+1 {
		// The trace samples the main thread every ~15-30 ms, so it can miss
		// the very top of the flight; the headless test pins the size.
		t.Fatalf("the real window never overshot: peak %.1f, end %.1f", peak, final)
	}
	motionShot(t, a, "open-settled", 250*time.Millisecond)
	motionClosed(a)
}

// TestSmokeCapsuleSuccess shoots the success capsule: green ✓ dot, the
// toast's words under the notch, gone after its hold.
func TestSmokeCapsuleSuccess(t *testing.T) {
	a := redesignShelf(t)
	motionClosed(a)
	time.Sleep(300 * time.Millisecond)
	onMain(func() {
		a.toastStatus("已压缩 3 张 · 4.6 MB → 1.8 MB", time.Now().Add(3*time.Second), statusOK)
	})
	// While the capsule is still a sliver, nothing it draws may reach past
	// the shell's edge — opaque paint beside it would smear the menu bar.
	// This shot skips motionShot's frame-sync sleep: it wants a mid-grow
	// frame, taken as the window stands right now.
	time.Sleep(50 * time.Millisecond)
	grow := filepath.Join(motionShotDir, "capsule-grow.png")
	var num int
	onMain(func() { num = mac.WindowNumber(a.win.NativeHandle()) })
	if num != 0 {
		if out, err := exec.Command("screencapture", "-x", "-o", fmt.Sprintf("-l%d", num), grow).CombinedOutput(); err != nil {
			t.Fatalf("capsule-grow: screencapture failed (%v): %s", err, out)
		}
		t.Logf("shot capsule-grow -> %s", grow)
	}
	var gw float32
	onMain(func() { gw = a.fx.w })
	if img, err := pngDecode(grow); err == nil {
		box := opaqueBounds(img)
		// The opaque extent can never be wider than the capsule's final
		// width; the pre-fix bug painted the full-width glow box from the
		// first frame, so a narrow shell with a wide opaque bar fails here.
		cw, _ := a.fb.capsule(180, 32)
		if img2w := box.Dx(); gw > 0 && gw < 200 && img2w > int(cw)+8 {
			t.Fatalf("capsule-grow: opaque paint %dpx wide while the shell is %.1fpt", img2w, gw)
		}
		t.Logf("capsule-grow: shell %.1fpt wide at capture, opaque extent %dpx", gw, box.Dx())
	}
	shot := motionShot(t, a, "capsule-success", 400*time.Millisecond)
	if shot == "" {
		t.Fatal("no shot")
	}
	var drawn bool
	onMain(func() { drawn = a.fb.drawn })
	if !drawn {
		t.Fatal("the success capsule is not drawn in the real window")
	}
	// The capsule is a real click target even though the window lets the
	// press through: the global monitor expands the shelf for it.
	// A press there reaches the shelf only through the global monitor, which
	// AppKit installs once ShelfU has Accessibility access (the same grant the
	// hover pill's click needs). Without it the press is not observable here.
	// The smoke harness never runs ready(), so the app's mouse monitor is
	// installed here: the capsule's press must arrive the way production's
	// does, through the global monitor while the window lets it through.
	var stop func()
	onMain(func() {
		stop = mac.ObserveMouseDown(a.win.NativeHandle(), func(inside bool, button int) {
			a.mouseDown(inside, button, mygo.Screen.CursorScreenPoint())
		})
	})
	t.Cleanup(func() { onMain(stop) })
	var pill mygo.Rectangle
	onMain(func() { pill = a.peekPillRect() })
	if mac.AccessibilityTrusted() {
		// The capsule intentionally passes through native hit-testing.
		// Catch the click in our own small receiver, not another app.
		x, y := pill.X+pill.Width/2, pill.Y+pill.Height-8
		receiver := newSmokePointerBackdrop(t, mygo.Rectangle{X: x - 2, Y: y - 2, Width: 4, Height: 4})
		onMain(func() { mac.SetWindowLevel(receiver.NativeHandle(), mac.StatusWindowLevel+1) })
		var stopReceiver func()
		onMain(func() {
			stopReceiver = mac.ObserveMouseDown(receiver.NativeHandle(), func(inside bool, button int) {
				if inside {
					// Source observers ignore other test windows. Route
					// this receiver as an outside-source click, the input
					// the global monitor receives from another process.
					a.mouseDown(false, button, mygo.Screen.CursorScreenPoint())
				}
			})
		})
		t.Cleanup(func() { onMain(stopReceiver) })
		clickAt(t, float64(pill.X+pill.Width/2), float64(pill.Y+pill.Height-8))
		var visible bool
		onMain(func() { visible = a.visible })
		t.Logf("capsule press at %+v: visible=%v", pill, visible)
		if !visible {
			t.Error("a press on the capsule did not expand the shelf")
		}
	} else {
		// Without the grant the HID press goes nowhere observable; the
		// monitor's own handler still proves the same path here.
		t.Logf("capsule press at %+v not exercised: no Accessibility access for this test process", pill)
		onMain(func() { a.mouseDown(false, 0, mygo.Point{X: pill.X + pill.Width/2, Y: pill.Y + pill.Height - 8}) })
	}
	motionShot(t, a, "capsule-click-expands", 300*time.Millisecond)
	// End the toast at its source before folding: hide() hands a live
	// status back to the capsule (handoffStatus), so a still-running hold
	// would re-draw it after the panel collapsed — the 3s hold raced this
	// ~2s section on main and the check flapped.
	onMain(func() {
		a.status, a.statusUntil = "", time.Now().Add(-time.Second)
		a.fb.until = time.Now()
		a.hide(hidePassive)
	})
	time.Sleep(400 * time.Millisecond)
	onMain(func() { drawn = a.fb.drawn })
	if drawn {
		t.Fatal("the capsule stayed drawn after the panel opened")
	}
	motionClosed(a)
}

// TestSmokeCapsuleFailure shoots the failure capsule twice: during its
// shake and after it, showing the red status and the settled offset.
func TestSmokeCapsuleFailure(t *testing.T) {
	a := redesignShelf(t)
	motionClosed(a)
	time.Sleep(300 * time.Millisecond)
	onMain(func() {
		a.toastStatus("压缩包已加密，无法解压", time.Now().Add(4*time.Second), statusErr)
	})
	motionShot(t, a, "capsule-failure-shake", 418*time.Millisecond)
	motionShot(t, a, "capsule-failure-hold", 500*time.Millisecond)
	var offset float32
	onMain(func() { offset = a.fb.shake(time.Now(), false) })
	if offset != 0 {
		t.Fatalf("the failure capsule is still shaking after its run: %.2f", offset)
	}
	motionClosed(a)
}

// TestSmokeCapsuleRunning holds a converted job open under a closed panel:
// the orange capsule reports its progress until the job ends.
func TestSmokeCapsuleRunning(t *testing.T) {
	a := redesignShelf(t)
	motionClosed(a)
	time.Sleep(300 * time.Millisecond)
	job := &convertJob{target: toJPEG, unit: "张", started: time.Now().Add(-time.Second)}
	job.total.Store(3)
	job.done.Store(1)
	onMain(func() {
		a.conv = &converter{host: a, job: job}
		a.win.Invalidate()
	})
	t.Cleanup(func() { onMain(func() { a.conv = nil }) })
	// The capsule grows out of the notch: its strip must be resized before
	// the shell outgrows the old one — shell, ears and shake only, no glow.
	for i := range 24 {
		var w, h float32
		var frame mygo.Rectangle
		onMain(func() {
			a.syncWindowFrame()
			w, h, frame = a.fx.w, a.fx.h, a.frame
		})
		if frame.Width < int(w+0.5) || frame.Height < int(h+0.5) {
			t.Fatalf("frame %d: window %+v cuts the capsule drawn at %.1fx%.1f", i, frame, w, h)
		}
		if i == 23 && frame.Width < capsuleFrameW() {
			t.Fatalf("frame %d: strip %+v cannot hold the capsule's full width", i, frame)
		}
		time.Sleep(10 * time.Millisecond)
	}
	shot := motionShot(t, a, "capsule-running", 300*time.Millisecond)
	if shot == "" {
		t.Fatal("no shot")
	}
	var text string
	var live bool
	onMain(func() { text, live = a.fb.text, a.fb.live })
	if !live || text != "正在转成 JPEG · 2/3" {
		t.Fatalf("running capsule = %q live=%v", text, live)
	}
	// Hover takes the peek shape back: the chevron, not the capsule.
	onMain(func() {
		a.peeking, a.peekProgress, a.peekFrom = true, 1, 1
		a.peekAnimStart = time.Time{}
		a.win.Invalidate()
	})
	motionShot(t, a, "capsule-yields-to-hover", 300*time.Millisecond)
	onMain(func() {
		if a.capsuleUp(time.Now()) {
			t.Error("the capsule must yield while the pointer hovers the notch")
		}
		pw, ph := a.peekSize()
		t.Logf("hover shell drawn %.1fx%.1f, chevron asks for %.1fx%.1f", a.fx.w, a.fx.h, pw, ph)
		if a.fx.w > pw+2 || a.fx.h > ph+2 {
			t.Errorf("the hover shape still carries the capsule's size: %.1fx%.1f", a.fx.w, a.fx.h)
		}
	})
	motionClosed(a)
}

// TestSmokeClipListEdgeBlur scrolls a long clipboard list in the real
// window and shoots the list at the top and in the middle: rows melt into
// the edge band only where more content lies beyond it, while rows in the
// open stay sharp. The shot is read back to prove the blur reached the
// screen: a sharp strip has several times the horizontal contrast of an
// edge strip.
func TestSmokeClipListEdgeBlur(t *testing.T) {
	a := redesignShelf(t)
	onMain(func() {
		for i := range 24 {
			if err := a.store.AddClipText(fmt.Sprintf("剪贴板第 %d 条：把这段文字复制到别处 %d", i+1, i+1), "com.example.App"); err != nil {
				t.Error(err)
				return
			}
		}
		a.setTab(pageClipboard)
	})
	expandShelf(t, a)
	time.Sleep(400 * time.Millisecond)
	var page *clipboardPage
	onMain(func() { page = a.page(pageClipboard).(*clipboardPage) })
	if page == nil {
		t.Fatal("no clipboard page")
	}
	var maxY float32
	onMain(func() { maxY = page.scroll.MaxY })
	if maxY <= 0 {
		t.Fatalf("the 24-row list does not scroll: MaxY=%.0f", maxY)
	}
	// A real wheel over the list: one event already runs to the end of a
	// 24-row list, the reverse one returns to the top, which proves the
	// gesture reaches this scroll view. The settled middle position the
	// bands need is then set directly.
	//
	// The overlay takes the mouse only while the pointer is on the shell
	// (a press outside it belongs to the window below), so the pointer is
	// put on the list's own area first.
	var frame mygo.Rectangle
	onMain(func() { frame = a.frame })
	warpPointerTo(t, float64(frame.X+280), float64(frame.Y+125))
	time.Sleep(60 * time.Millisecond)
	for _, delta := range []float64{40, -20} {
		onMain(func() {
			if !mac.SurfaceScroll(a.win.NativeHandle(), 280, 125, delta) {
				t.Error("the scroll event did not reach the list")
			}
		})
		time.Sleep(40 * time.Millisecond)
	}
	onMain(func() {
		page.scroll.Y = 0
		a.win.Invalidate()
	})
	time.Sleep(200 * time.Millisecond)
	top, bottom, _ := scrollEdges(&page.scroll, float32(len(page.items()))*(clipRowH+clipRowGap)-clipRowGap)
	if top || !bottom {
		t.Fatalf("at the top the bands are top=%v bottom=%v, want only the bottom", top, bottom)
	}
	sharpShot := motionShot(t, a, "edge-top", 200*time.Millisecond)
	assertEdgeStripsBlurrier(t, sharpShot, a, "top", false, true)

	onMain(func() {
		page.scroll.Y = maxY / 2
		a.win.Invalidate()
	})
	time.Sleep(200 * time.Millisecond)
	var y float32
	onMain(func() { y = page.scroll.Y })
	midShot := motionShot(t, a, "edge-scrolled", 200*time.Millisecond)
	if y <= 0 || y >= maxY {
		t.Fatalf("the wheel left the list at Y=%.1f of %.1f, neither band visible", y, maxY)
	}
	t.Logf("scrolled to %.1f of %.1f", y, maxY)
	assertEdgeStripsBlurrier(t, midShot, a, "middle", true, true)
	onMain(func() {
		a.tab = pageTray
		a.store.Clear()
	})
	motionClosed(a)
}

// assertEdgeStripsBlurrier checks a shot of the clipboard page: the 20pt
// strip at each active edge must have clearly less horizontal contrast
// than the same strip in the open, which is what the row blur does.
func assertEdgeStripsBlurrier(t *testing.T, path string, a *shelf, where string, top, bottom bool) {
	t.Helper()
	img, err := pngDecode(path)
	if err != nil {
		t.Fatalf("%s: %v", where, err)
	}
	b := img.Bounds()
	scale := float64(b.Dx()) / float64(a.frame.Width)
	contentTop := int(math.Round(float64(a.sheetTop()+bandH+pageActionH+actionsGap) * scale))
	x0 := int(math.Round(float64((a.frame.Width-contentW)/2+20) * scale))
	x1 := x0 + int(math.Round(float64(contentW-40)*scale))
	strip := int(math.Round(20 * scale))
	mid := bandSharpness(img, x0, x1, contentTop+int(math.Round(60*scale)), contentTop+int(math.Round(80*scale)))
	if top {
		edge := bandSharpness(img, x0, x1, contentTop, contentTop+strip)
		if mid < 2*edge {
			t.Fatalf("%s: the top edge strip is not blurred (edge %d, middle %d)", where, edge, mid)
		}
		t.Logf("%s: top edge contrast %d, middle %d", where, edge, mid)
	}
	if bottom {
		edge := bandSharpness(img, x0, x1, contentTop+int(math.Round(174*scale))-strip,
			contentTop+int(math.Round(174*scale)))
		if mid < 2*edge {
			t.Fatalf("%s: the bottom edge strip is not blurred (edge %d, middle %d)", where, edge, mid)
		}
		t.Logf("%s: bottom edge contrast %d, middle %d", where, edge, mid)
	}
}

// bandSharpness sums the horizontal luminance steps inside a pixel band: a
// sharp row of text and icons has many, a blurred one few.
func bandSharpness(img image.Image, x0, x1, y0, y1 int) int {
	b := img.Bounds()
	x0, x1 = max(x0, b.Min.X), min(x1, b.Max.X)
	y0, y1 = max(y0, b.Min.Y), min(y1, b.Max.Y)
	var sum int
	for y := y0; y < y1; y++ {
		var prev int32
		for x := x0; x < x1; x++ {
			r, g, bl, _ := img.At(x, y).RGBA()
			lum := int32((299*r + 587*g + 114*bl) / 1000)
			if x > x0 {
				d := lum - prev
				if d < 0 {
					d = -d
				}
				sum += int(d)
			}
			prev = lum
		}
	}
	return sum
}

// TestSmokePeekMorphNeverClipped grows the chevron pill into the now-playing
// wings mid-strip: the morph spring overshoots, and the resident strip must
// hold every frame of it.
func TestSmokePeekMorphNeverClipped(t *testing.T) {
	a := redesignShelf(t)
	motionClosed(a)
	time.Sleep(300 * time.Millisecond)
	onMain(func() {
		a.settings.IslandNowPlaying = true
		mod := &nowPlayingModule{host: a}
		mod.have = true
		mod.last = mac.NowPlayingState{Playing: true, Title: "Fashion Killa", Duration: 200, Elapsed: 30, Received: time.Now()}
		a.np = mod
		a.peeking = true
		a.peekProgress, a.peekFrom = 1, 1
		a.peekAnimStart = time.Time{}
		a.syncMousePassThrough()
		a.win.Invalidate()
	})
	t.Cleanup(func() { onMain(func() { a.np = nil; a.peeking = false }) })
	for i := range 40 {
		var w, h float32
		var frame mygo.Rectangle
		onMain(func() {
			a.syncWindowFrame()
			w, h, frame = a.fx.w, a.fx.h, a.desiredFrame()
		})
		if frame.Width < int(w+0.5) || frame.Height < int(h+0.5) {
			t.Fatalf("frame %d: strip %+v cuts the morphing shell %.1fx%.1f", i, frame, w, h)
		}
		time.Sleep(10 * time.Millisecond)
	}
	motionShot(t, a, "peek-wings", 100*time.Millisecond)
	motionClosed(a)
}
