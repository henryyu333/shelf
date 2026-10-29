//go:build darwin

package main

import (
	"os"
	"os/exec"
	"strconv"
	"testing"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"shelf/internal/mac"
	"shelf/internal/settings"
)

// TestSettingsWindowAnswersClicksAndScroll is the regression test for the
// class-level swizzle leak: the notch overlay's native patches used to be
// installed on the shared mygo classes, so every window inherited them and
// the Settings window's controls and scroll container went dead. The
// remaining patch (constrainFrameRect:toScreen:) gates on the window
// pointer; this test proves a real Settings window still answers clicks
// everywhere and scrolls its content next to the notch overlay. Run with
// SHELF_BASKET_SMOKE=1 (the shared real-app harness); uses a temporary
// data dir and never the user's ShelfU data.
func TestSettingsWindowAnswersClicksAndScroll(t *testing.T) {
	if os.Getenv("SHELF_BASKET_SMOKE") != "1" {
		t.Skip("set SHELF_BASKET_SMOKE=1 to run the native window smoke test")
	}
	a := smokeShelf
	// A writable data dir with the real defaults, so the window shows what a
	// user sees (the zero value would render every switch off).
	a.dataDir = t.TempDir()
	a.settings, _ = settings.Load(a.dataDir)
	onMain(func() {
		a.newNotchWindow()
		a.place(mygo.Screen.CursorScreenPoint())
		a.syncMousePassThrough() // idle: the overlay ignores the mouse
		a.win.ShowInactive()
		a.openSettings()
	})
	if a.win == nil || a.settingsWin == nil {
		t.Fatal("expected both the notch overlay and the Settings window")
	}
	defer onMain(func() {
		if a.settingsWin != nil {
			a.settingsWin.Close()
		}
	})
	// Give AppKit a beat to order the windows in and run the first frames.
	time.Sleep(300 * time.Millisecond)

	notch := a.win.NativeHandle()
	settings := a.settingsWin.NativeHandle()
	// Class hygiene: the patches gate on window/view pointers inside one
	// shared-class method — no subclassing, no reclassing, so every window
	// keeps its stock mygo class (a reclass would break AppKit's KVO
	// isa-swizzling on the titlebar view).
	t.Logf("notch class=%s settings class=%s", mac.WindowClassName(notch), mac.WindowClassName(settings))
	if cls := mac.WindowClassName(settings); cls != "MyGoWindow" {
		t.Fatalf("Settings window class %q, want plain MyGoWindow", cls)
	}
	if cls := mac.WindowClassName(notch); cls != "MyGoPanel" {
		t.Fatalf("notch window class %q, want plain MyGoPanel", cls)
	}

	// Hit-test, while the notch is idle: the Settings surface answers
	// across its content.
	onMain(func() {
		for _, p := range [][2]float64{{230, 60}, {230, 260}, {230, 450}, {60, 300}} {
			if !mac.SurfaceHitTest(settings, p[0], p[1]) {
				t.Errorf("Settings hitTest(%v,%v) rejected", p[0], p[1])
			}
		}
	})
	if t.Failed() {
		return
	}

	// Scroll: a wheel event on the Settings surface must move the tracked
	// scroll state. The 快捷键 page is taller than the 560pt window (the
	// 通用 page fits, and must stay scroll-free).
	var before, after float32
	onMain(func() {
		a.settingsPage = settingsPageShortcuts
		a.settingsScroll.Y = 0
		a.settingsWin.Invalidate()
	})
	time.Sleep(150 * time.Millisecond)
	onMain(func() {
		a.settingsWin.Invalidate()
		before = a.settingsScroll.Y
		if !mac.SurfaceScroll(settings, 230, 300, 240) {
			t.Error("SurfaceScroll delivered no event")
		}
	})
	time.Sleep(150 * time.Millisecond)
	onMain(func() {
		a.settingsWin.Invalidate()
		after = a.settingsScroll.Y
	})
	if after == before || a.settingsScroll.MaxY <= 0 {
		t.Fatalf("scroll event moved nothing: ScrollState.Y stayed %v (MaxY=%v)", after, a.settingsScroll.MaxY)
	}
	t.Logf("scroll moved %v → %v (max %v)", before, after, a.settingsScroll.MaxY)

	// Screenshot the scrolled window for eyeballing; the id pins
	// `screencapture -l` to this window alone.
	if n := mac.WindowNumber(settings); n != 0 {
		out := os.TempDir() + "/shelf-settings-scrolled.png"
		if err := exec.Command("screencapture", "-l", strconv.Itoa(n), "-x", out).Run(); err == nil {
			t.Logf("scrolled Settings screenshot: %s", out)
		}
	}

	// The basket is the other mygo panel: it must keep the stock surface
	// class (the graft is per-instance, and only the notch asks for it).
	onMain(func() {
		a.showBasket(mygo.Point{X: 200, Y: 300})
	})
	defer onMain(a.closeBasket)
	if a.basket.win == nil {
		t.Fatal("basket window was not created")
	}
	if cls := mac.WindowClassName(a.basket.win.NativeHandle()); cls != "MyGoPanel" {
		t.Errorf("basket window class %q carries a graft it never asked for", cls)
	}
	if !mac.SurfaceHitTest(a.basket.win.NativeHandle(), 110, 80) {
		t.Error("basket surface rejected a centered hit")
	}
}

func TestSettingsClipboardAlwaysOnNative(t *testing.T) {
	if os.Getenv("SHELF_BASKET_SMOKE") != "1" {
		t.Skip("set SHELF_BASKET_SMOKE=1 to run the native window smoke test")
	}
	stubSettingsSeams(t, false, nil, nil)
	a := &shelf{dataDir: t.TempDir(), settings: settings.Default(),
		appNames: map[string]string{}, thumbnails: map[string]*ui.Bitmap{}, thumbPending: map[string]bool{}}
	onMain(a.openSettings)
	t.Cleanup(func() {
		onMain(func() {
			if a.settingsWin != nil {
				a.settingsWin.Close()
			}
		})
	})
	time.Sleep(300 * time.Millisecond)
	onMain(func() {
		a.settingsPage = settingsPageClipboard
		a.settingsScroll.Y = min(400, a.settingsScroll.MaxY)
		a.settingsWin.Invalidate()
	})
	time.Sleep(250 * time.Millisecond)
	var id int
	onMain(func() { id = mac.WindowNumber(a.settingsWin.NativeHandle()) })
	if id == 0 {
		t.Fatal("settings window was not published to WindowServer")
	}
	if err := os.MkdirAll("/tmp/shelf-fix-L", 0o755); err != nil {
		t.Fatal(err)
	}
	path := "/tmp/shelf-fix-L/after-settings-clipboard.png"
	if b, err := exec.Command("screencapture", "-l", strconv.Itoa(id), "-x", "-o", path).CombinedOutput(); err != nil {
		t.Fatalf("settings screenshot: %v: %s", err, b)
	}
	t.Log(path)
}
