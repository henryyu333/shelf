//go:build darwin

package main

// Evidence for the stage-review repairs that only a real window can show:
// the light appearance's material following the shell while the panel opens
// (the native window's size changes once, the material's box every frame),
// and a 1 × 1 music widget pinned to the desktop at the wide card size.
// Run with SHELF_BASKET_SMOKE=1; everything lands in /tmp/shelf-stagef-A.

import (
	"encoding/csv"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/egoist/mygo"

	"shelf/internal/layout"
	"shelf/internal/mac"
)

// TestSmokeMaterialFollowsTheShell samples the applied material box while
// the panel opens in the light appearance: the shell's height rises with
// the spring, the window's does not change after its first frame, and the
// material's box matches the shell on every sample.
func TestSmokeMaterialFollowsTheShell(t *testing.T) {
	if os.Getenv("SHELF_BASKET_SMOKE") != "1" {
		t.Skip("set SHELF_BASKET_SMOKE=1 to capture the material frames")
	}
	oldDark, oldReduce := macSystemDark, macReduceTransparency
	macSystemDark = func() bool { return false }
	macReduceTransparency = func() bool { return false }
	t.Cleanup(func() { macSystemDark, macReduceTransparency = oldDark, oldReduce })

	// A bright window behind the panel, so the shots show what the material
	// does to the desktop.
	var probe *mygo.Window
	onMain(func() {
		probe = mygo.NewWindow(mygo.WindowOptions{Title: "probe", Width: 1000, Height: 420, Frameless: true})
		probe.SetPosition(260, 20)
		probe.Show()
		probe.Page().LoadHTML(`<body style="margin:0;background:#fff">
<div style="font:700 56px -apple-system;color:#000;padding:8px 14px">MATERIAL 1 2 3</div>
<div style="font:700 56px -apple-system;color:#d00;padding:8px 14px">MATERIAL 4 5 6</div></body>`, "")
	})
	t.Cleanup(func() { onMain(func() { probe.Destroy() }) })
	time.Sleep(500 * time.Millisecond)

	a := stageAShelf(t)
	fillShelf(t, a)
	// Start folded, then open in the light appearance.
	onMain(func() {
		a.hide(hideExplicit)
		a.closeAt = time.Time{}
		a.finishClose()
		a.syncWindowFrame()
		a.refreshAppearance(time.Now().Add(time.Hour))
	})
	time.Sleep(300 * time.Millisecond)

	path := filepath.Join(stageADir, "frames-material.csv")
	_ = os.MkdirAll(stageADir, 0o755)
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	w := csv.NewWriter(f)
	defer w.Flush()
	_ = w.Write([]string{"ms", "shell_w", "shell_h", "win_w", "win_h", "mat_x", "mat_y", "mat_w", "mat_h", "mat_r", "mat_on"})

	// The hook runs on the threaded frame itself: one row per applied
	// material box, not per poll.
	base := time.Now()
	shots := 0
	oldHook := onMaterialFrame
	onMaterialFrame = func(k materialKey, shellW, shellH float32, winW, winH int) {
		_ = w.Write([]string{
			strconv.FormatInt(time.Since(base).Milliseconds(), 10),
			strconv.FormatFloat(float64(shellW), 'f', 1, 64),
			strconv.FormatFloat(float64(shellH), 'f', 1, 64),
			strconv.Itoa(winW),
			strconv.Itoa(winH),
			strconv.FormatFloat(k.x, 'f', 1, 64),
			strconv.FormatFloat(k.y, 'f', 1, 64),
			strconv.FormatFloat(k.w, 'f', 1, 64),
			strconv.FormatFloat(k.h, 'f', 1, 64),
			strconv.FormatFloat(k.r, 'f', 1, 64),
			strconv.FormatBool(k.on),
		})
		if shots == 3 && time.Since(base) > 120*time.Millisecond {
			shots++
			// A mid-open shot from outside the hook (it runs mid-frame).
			go func() { _ = exec.Command("true").Run() }()
		}
	}
	t.Cleanup(func() { onMaterialFrame = oldHook })
	onMain(func() { a.show(false) })
	time.Sleep(90 * time.Millisecond)
	stageAShotRegion(t, a, "material-mid-open")
	time.Sleep(430 * time.Millisecond)
	onMain(func() { onMaterialFrame = oldHook })
	stageAShotRegion(t, a, "material-settled")
	t.Logf("material frames -> %s", path)
}

// TestSmokePinnedOneColumnMusic: a music widget shrunk to 1 × 1 pins as a
// 278pt desktop card (its player needs the width) while the grid keeps the
// 1 × 1 tile for 放回刘海.
func TestSmokePinnedOneColumnMusic(t *testing.T) {
	if os.Getenv("SHELF_BASKET_SMOKE") != "1" {
		t.Skip("set SHELF_BASKET_SMOKE=1 to capture the pinned card")
	}
	withPinDisplays(t, pinTestDisplays)
	oldOpen, oldCursor := pinOpenWindow, cursorPoint
	t.Cleanup(func() { pinOpenWindow = oldOpen; cursorPoint = oldCursor })

	a := stageAShelf(t)
	fillShelf(t, a)
	a.x, a.y = 400, 0
	a.mutateTools(func(l *layout.Layout) { l.SetCardSize("playing", 1, 1) })
	onMain(func() {
		a.frame = mygo.Rectangle{X: a.x, Y: a.y, Width: sheetW, Height: sheetH}
		a.openPage(pageTools)
		if p := a.toolsPage(); p != nil {
			p.editing = true
		}
		a.win.Invalidate()
	})
	time.Sleep(300 * time.Millisecond)
	onMain(func() {
		p := a.toolsPage()
		p.toolCardDrop(&toolPress{id: "playing", dragging: true, px: 60, py: 2000, grabDX: 10, grabDY: 10},
			mygo.Point{X: 60, Y: 2000})
	})
	time.Sleep(400 * time.Millisecond)
	var num int
	var frame mygo.Rectangle
	onMain(func() {
		if len(a.pins) != 1 {
			t.Fatalf("pins = %+v", a.pins)
		}
		frame = a.pins[0].frame
		num = mac.WindowNumber(a.pins[0].win.NativeHandle())
	})
	if frame.Width != pinCardW2 {
		t.Fatalf("the pinned 1 × 1 music card is %d wide, want %d", frame.Width, pinCardW2)
	}
	time.Sleep(300 * time.Millisecond)
	out := filepath.Join(stageADir, "pin-music-1x1.png")
	shot := fmt.Sprintf("-l%d", num)
	if num == 0 {
		t.Fatal("the pin window has no number")
	}
	if b, err := exec.Command("screencapture", "-x", "-o", shot, out).CombinedOutput(); err != nil {
		t.Fatalf("screencapture: %v: %s", err, b)
	}
	t.Logf("pinned card -> %s", out)
	onMain(func() {
		for _, it := range a.pins {
			if it.win != nil {
				it.win.Destroy()
			}
		}
	})
}
