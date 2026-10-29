//go:build darwin

package main

// Real-window proof for the island shell: the resting capsule, the hover
// wings, both panel levels, a file dropping onto the island and the
// completion capsule, all drawn by the shared resident window on this
// notched screen with 刘海样式 = 灵动岛, plus one notch-style shot proving
// the default did not change. Shots land in /tmp/shelf-island/; the smoke
// harness reads a temporary data dir and never touches
// ~/Library/Application Support/ShelfU. Run with SHELF_BASKET_SMOKE=1.

import (
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"shelf/internal/mac"
	"shelf/internal/settings"
)

const islandShotDir = "/tmp/shelf-island"

// islandArena is the test-owned backdrop the shots are cut against: a
// light surface just below the menu bar, so the black capsule reads as a
// floating shape. Only test windows are in the captured rect.
func islandArena(a *shelf) mygo.Rectangle {
	center := a.stripCenter()
	top := a.y + a.islandTop
	return mygo.Rectangle{X: center - 360, Y: top, Width: 720, Height: 340}
}

// newIslandArena creates that backdrop below the overlay's level.
func newIslandArena(t *testing.T, r mygo.Rectangle) {
	t.Helper()
	onMain(func() {
		w := mygo.NewWindow(mygo.WindowOptions{
			Title: "ShelfU island backdrop", Hidden: true,
			Width: r.Width, Height: r.Height, X: r.X, Y: r.Y,
			Frameless: true, Panel: true, SkipTaskbar: true, DisableShadow: true,
			Content: ui.View(func(c *ui.Context) {
				ui.Box(c).Fill().Background(ui.Hex("#ECEDEF"))
				ui.Text(c, "ShelfU 灵动岛 · 测试背景").FontSize(11).TextColor(ui.RGBA(0, 0, 0, 0.28)).
					Padding(8, 12, 0, 12)
			}),
		})
		mac.ConfigurePinWindow(w.NativeHandle())
		mac.SetWindowLevel(w.NativeHandle(), 2)
		mac.SetWindowFrame(w.NativeHandle(), r.X, r.Y, r.Width, r.Height)
		w.ShowInactive()
		t.Cleanup(func() { onMain(w.Destroy) })
	})
	time.Sleep(150 * time.Millisecond)
}

// islandShot screenshots the arena rect after letting the springs settle
// and applying the frame the 30 ms tick would have applied.
func islandShot(t *testing.T, a *shelf, name string, settle time.Duration) string {
	t.Helper()
	if a.win == nil {
		t.Fatal("no notch window")
	}
	time.Sleep(settle)
	onMain(func() {
		a.syncMousePassThrough()
		a.syncWindowFrame()
		a.win.Invalidate()
	})
	time.Sleep(150 * time.Millisecond)
	if err := os.MkdirAll(islandShotDir, 0o755); err != nil {
		t.Fatal(err)
	}
	r := islandArena(a)
	shot := filepath.Join(islandShotDir, name+".png")
	if out, err := exec.Command("screencapture", "-x", "-o", "-R",
		fmt.Sprintf("%d,%d,%d,%d", r.X, r.Y, r.Width, r.Height), shot).CombinedOutput(); err != nil {
		t.Fatalf("%s: screencapture failed (%v): %s", name, err, out)
	}
	t.Logf("shot %s -> %s", name, shot)
	return shot
}

// darkBounds is the black shell's extent in a shot cut over the light
// arena: a pure-black capsule leaves the label and grid well above the
// threshold.
func darkBounds(img image.Image, maxY int) image.Rectangle {
	b := img.Bounds()
	box := image.Rectangle{}
	for y := b.Min.Y; y < b.Max.Y && y < maxY; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			r, g, bl, a := img.At(x, y).RGBA()
			if a == 0 || (r>>8) > 40 || (g>>8) > 40 || (bl>>8) > 40 {
				continue
			}
			if box.Empty() {
				box = image.Rect(x, y, x+1, y+1)
				continue
			}
			box = box.Union(image.Rect(x, y, x+1, y+1))
		}
	}
	return box
}

// islandRawShot is the window-only capture (transparent background), plus
// a copy composited on the same light grey for review.
func islandRawShot(t *testing.T, a *shelf, name string, settle time.Duration) string {
	t.Helper()
	time.Sleep(settle)
	onMain(func() {
		a.syncMousePassThrough()
		a.syncWindowFrame()
		a.win.Invalidate()
	})
	time.Sleep(150 * time.Millisecond)
	if err := os.MkdirAll(islandShotDir, 0o755); err != nil {
		t.Fatal(err)
	}
	num := mac.WindowNumber(a.win.NativeHandle())
	if num == 0 {
		t.Fatalf("%s: window has no CGWindowID", name)
	}
	shot := filepath.Join(islandShotDir, name+".png")
	if out, err := exec.Command("screencapture", "-x", "-o", fmt.Sprintf("-l%d", num), shot).CombinedOutput(); err != nil {
		t.Fatalf("%s: screencapture failed (%v): %s", name, err, out)
	}
	img, err := pngDecode(shot)
	if err != nil {
		t.Fatal(err)
	}
	dst := image.NewRGBA(img.Bounds())
	draw.Draw(dst, dst.Bounds(), image.NewUniform(color.RGBA{R: 236, G: 237, B: 239, A: 255}), image.Point{}, draw.Src)
	draw.Draw(dst, dst.Bounds(), img, img.Bounds().Min, draw.Over)
	light := strings.TrimSuffix(shot, ".png") + "-light.png"
	f, err := os.Create(light)
	if err != nil {
		t.Fatal(err)
	}
	if err := png.Encode(f, dst); err != nil {
		f.Close()
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	t.Logf("shot %s -> %s (and %s)", name, shot, light)
	return shot
}

// islandScale is the screenshot's pixels per point, taken from the window
// frame the shot was cut from.
func islandScale(img image.Image, frame mygo.Rectangle) float64 {
	if frame.Width == 0 {
		return 1
	}
	return float64(img.Bounds().Dx()) / float64(frame.Width)
}

// islandStyle switches the shared smoke shelf to the island and puts it
// back to rest on the current display.
func islandStyle(t *testing.T, a *shelf, style settings.ShellStyle) {
	t.Helper()
	onMain(func() {
		a.settings.ShellStyle = style
		a.applyShellStyle()
		a.hide(hidePassive)
		a.visible, a.edgeOpened, a.received = false, false, false
		a.progress, a.animationFrom, a.animationStart = 0, 0, time.Time{}
		a.peeking, a.peekProgress, a.peekFrom, a.peekAnimStart = false, 0, 0, time.Time{}
		a.fb = feedback{}
		a.drop = dropZoneState{}
		a.place(mygo.Screen.CursorScreenPoint())
		a.win.SetOpacity(1)
		a.win.ShowInactive()
		a.syncMousePassThrough()
		a.syncWindowFrame()
		a.win.Invalidate()
	})
}

// TestIslandSmokeScreenshots is the design pass: it requires the built-in
// notched screen (this machine has no external display), so the island is
// verified through the 设置 choice; the always-island path for screens
// without a housing is covered by island_test.go.
func TestIslandSmokeScreenshots(t *testing.T) {
	if os.Getenv("SHELF_BASKET_SMOKE") != "1" {
		t.Skip("set SHELF_BASKET_SMOKE=1 to run the native island smoke test")
	}
	a := redesignShelf(t)
	islandStyle(t, a, settings.ShellStyleIsland)
	if !a.island || !a.hasNotch {
		t.Fatalf("smoke shelf island=%v hasNotch=%v, want the island below this screen's housing", a.island, a.hasNotch)
	}
	newIslandArena(t, islandArena(a))
	skipIfForeignOverlayOver(t, islandArena(a))

	// Idle: the 120 × 30 capsule floats 6pt below the notch.
	idle := islandShot(t, a, "idle", 350*time.Millisecond)
	img, err := pngDecode(idle)
	if err != nil {
		t.Fatal(err)
	}
	var fw, fh float32
	onMain(func() { fw, fh = a.fx.w, a.fx.h })
	scale := islandScale(img, islandArena(a))
	box := darkBounds(img, img.Bounds().Min.Y+int(120*scale))
	if got := float64(box.Min.Y) / scale; got < -2 || got > 2 {
		t.Fatalf("idle capsule starts at %.1fpt in the arena, want its top edge", got)
	}
	if got := float64(box.Dx()) / scale; got < 118 || got > 122 {
		t.Fatalf("idle capsule is %.1fpt wide, want 120", got)
	}
	if got := float64(box.Dy()) / scale; got < 28 || got > 32 {
		t.Fatalf("idle capsule is %.1fpt tall, want 30", got)
	}
	if fw != islandIdleW || fh != islandIdleH {
		t.Fatalf("drawn idle shell = %.0fx%.0f, want 120x30", fw, fh)
	}

	// Hover with music: the wings grow from the capsule, 224 × 34.
	onMain(func() {
		a.settings.IslandNowPlaying = true
		fakeTrack(a, true)
	})
	peekShelf(a)
	time.Sleep(250 * time.Millisecond)
	onMain(a.syncWindowFrame)
	hover := islandShot(t, a, "hover-music", 250*time.Millisecond)
	if _, err := pngDecode(hover); err != nil {
		t.Fatal(err)
	}
	var wingsW, wingsH float32
	onMain(func() { wingsW, wingsH = a.fx.w, a.fx.h })
	if wingsW != 224 || wingsH != 34 {
		t.Fatalf("drawn wings = %.0fx%.0f, want 224x34", wingsW, wingsH)
	}

	// First level: the 340 × 104 island from the capsule.
	onMain(func() {
		a.place(mygo.Screen.CursorScreenPoint())
		a.peeking = true
		a.peekProgress, a.peekFrom = 1, 1
		a.show(false)
		a.setLevel(1)
		a.jumpLevel = true
		a.progress, a.animationFrom = 1, 1
		a.animationStart = time.Time{}
		a.syncMousePassThrough()
		a.syncWindowFrame()
		a.win.Invalidate()
	})
	level1 := islandShot(t, a, "level-1", 400*time.Millisecond)
	if _, err := pngDecode(level1); err != nil {
		t.Fatal(err)
	}
	var l1w, l1h float32
	onMain(func() { l1w, l1h = a.fx.w, a.fx.h })
	if l1w != overviewW || l1h != overviewH {
		t.Fatalf("level 1 shell = %.0fx%.0f, want 340x104", l1w, l1h)
	}

	// Second level: the 420 × 250 sheet, all four corners round.
	expandShelf(t, a)
	sheet := islandShot(t, a, "sheet", 500*time.Millisecond)
	if _, err := pngDecode(sheet); err != nil {
		t.Fatal(err)
	}
	var sw, sh float32
	onMain(func() { sw, sh = a.fx.w, a.fx.h })
	if sw != sheetW || sh != sheetH {
		t.Fatalf("sheet shell = %.0fx%.0f, want 420x250", sw, sh)
	}

	// A file dragged onto the island: the drop zone replaces the page.
	fix := t.TempDir()
	pic := writeSmokePNG(t, fix, "island-drop.png", 320, 240)
	t.Setenv("SHELF_DEBUG_DROP_FILES", pic)
	onMain(func() {
		a.drop = dropZoneState{}
		if !a.visible {
			a.show(false)
		}
		a.win.Invalidate()
	})
	islandShot(t, a, "drop", 900*time.Millisecond)
	t.Setenv("SHELF_DEBUG_DROP_FILES", "")

	// Completion capsule: one 36pt pill, contents vertically centred.
	onMain(func() {
		a.drop = dropZoneState{}
		a.hide(hidePassive)
		a.visible, a.edgeOpened = false, false
		a.progress, a.animationFrom, a.animationStart = 0, 0, time.Time{}
		a.peeking, a.peekProgress, a.peekFrom, a.peekAnimStart = false, 0, 0, time.Time{}
		a.place(mygo.Screen.CursorScreenPoint())
		a.toastStatus("已复制 2 项", time.Now().Add(6*time.Second), statusOK)
		a.win.Invalidate()
	})
	done := islandShot(t, a, "capsule", 600*time.Millisecond)
	if _, err := pngDecode(done); err != nil {
		t.Fatal(err)
	}
	var capsuleH float32
	onMain(func() { capsuleH = a.fx.h })
	if capsuleH < 34 || capsuleH > 36 {
		t.Fatalf("capsule shell %.0fpt high, want 36", capsuleH)
	}

	// The notch style, unchanged: the same music hover keeps the hardware
	// notch's ears and starts at the display's top edge.
	islandStyle(t, a, settings.ShellStyleNotch)
	onMain(func() {
		a.settings.IslandNowPlaying = true
		fakeTrack(a, true)
	})
	peekShelf(a)
	time.Sleep(250 * time.Millisecond)
	onMain(a.syncWindowFrame)
	notchShot := islandRawShot(t, a, "notch-hover", 250*time.Millisecond)
	nimg, err := pngDecode(notchShot)
	if err != nil {
		t.Fatal(err)
	}
	var nframe mygo.Rectangle
	var nw, nh float32
	var isIsland bool
	onMain(func() {
		nframe, nw, nh, isIsland = a.desiredFrame(), a.fx.w, a.fx.h, a.island
	})
	if isIsland {
		t.Fatal("刘海样式 = 刘海 must keep the hardware-notch shape")
	}
	nscale := islandScale(nimg, nframe)
	nbox := opaqueBounds(nimg)
	if got := float64(nbox.Min.Y) / nscale; got > 2 {
		t.Fatalf("notch shell starts at %.1fpt, want the display's top edge", got)
	}
	if nw <= 224 || nh != float32(max(32, a.notchH))+npWingsBody {
		t.Fatalf("notch wings = %.0fx%.0f, want wider than the island's 224 and %d high", nw, nh, max(32, a.notchH)+npWingsBody)
	}
}

// TestIslandLiveSmokeShots renders A's three activities on the island:
// the charging wings (296 × 34: 120 + 2×88), the headphone capsule and the
// finished-download capsule (36pt, contents centred). They are posted
// through the same queue the modules use, with long holds so the shot
// lands inside them.
func TestIslandLiveSmokeShots(t *testing.T) {
	if os.Getenv("SHELF_BASKET_SMOKE") != "1" {
		t.Skip("set SHELF_BASKET_SMOKE=1 to run the native island smoke test")
	}
	a := redesignShelf(t)
	islandStyle(t, a, settings.ShellStyleIsland)
	newIslandArena(t, islandArena(a))
	onMain(func() {
		a.settings.LiveActivities = settings.LiveActivitySettings{Charging: true, Headphones: true, Downloads: true}
	})
	onMain(func() {
		a.pushLive(liveEvent{kind: liveCharging, label: "正在充电", pct: 80}, 6*time.Second, time.Now())
	})
	time.Sleep(650 * time.Millisecond)
	islandShot(t, a, "live-charging", 80*time.Millisecond)
	var w, h float32
	onMain(func() { w, h = a.fx.w, a.fx.h })
	if w != 296 || h != 34 {
		t.Fatalf("island charging shell = %.0fx%.0f, want 296x34", w, h)
	}
	onMain(func() { a.clearLive(); a.win.Invalidate() })
	time.Sleep(500 * time.Millisecond)

	onMain(func() {
		a.pushLive(liveEvent{kind: liveHeadset, label: "AirPods Pro",
			levels: []string{"左 80%", "右 78%", "盒 50%"}}, 6*time.Second, time.Now())
	})
	time.Sleep(650 * time.Millisecond)
	islandShot(t, a, "live-headphones", 80*time.Millisecond)
	onMain(func() { w, h = a.fx.w, a.fx.h })
	if h != islandCapsuleH || w < 330 {
		t.Fatalf("island headphone shell = %.0fx%.0f, want a >=330pt 36pt capsule", w, h)
	}
	onMain(func() { a.clearLive(); a.win.Invalidate() })
	time.Sleep(500 * time.Millisecond)

	onMain(func() {
		a.pushLive(liveEvent{kind: liveDownloadDone, label: "设计稿-v3.zip"}, 6*time.Second, time.Now())
	})
	time.Sleep(650 * time.Millisecond)
	islandShot(t, a, "live-download-done", 80*time.Millisecond)
	onMain(func() { w, h = a.fx.w, a.fx.h })
	if h != islandCapsuleH || w < 280 {
		t.Fatalf("island download-done shell = %.0fx%.0f, want a 36pt capsule", w, h)
	}
	onMain(func() { a.clearLive(); a.win.Invalidate() })
	time.Sleep(400 * time.Millisecond)
	islandStyle(t, a, settings.ShellStyleNotch)
}
