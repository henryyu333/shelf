//go:build darwin

package main

// Real-window evidence for 大阶段 F's first-level overview: every state the
// overview can be in, the now-playing page, the four pages, the drop zone,
// the welcome and new-version cards, both system appearances and both
// languages — plus a per-frame CSV of opening, growing to a page and
// folding back. Run with SHELF_BASKET_SMOKE=1; everything lands in
// /tmp/shelf-stagef-A. The test instance uses the smoke data dir and never
// touches ~/Library/Application Support/ShelfU.

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

	"shelf/internal/mac"
)

const stageADir = "/tmp/shelf-stagef-A"

// stageAShotRegion captures the window and the desktop around it, so an
// appearance change can be judged against what is behind the panel.
func stageAShotRegion(t *testing.T, a *shelf, name string) string {
	t.Helper()
	if a.win == nil {
		t.Fatal("no notch window")
	}
	var r mygo.Rectangle
	onMain(func() {
		if actual := a.win.Bounds(); actual.Width > 0 {
			r = actual
		} else {
			r = a.desiredFrame()
		}
	})
	time.Sleep(400 * time.Millisecond)
	if err := os.MkdirAll(stageADir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(stageADir, name+".png")
	pad := 140
	out, err := exec.Command("screencapture", "-x", "-o", "-R",
		fmt.Sprintf("%d,%d,%d,%d", r.X-pad, max(r.Y-pad, 0), r.Width+2*pad, r.Height+2*pad), path).CombinedOutput()
	if err != nil {
		t.Fatalf("%s: screencapture failed (%v): %s", name, err, out)
	}
	t.Logf("shot %s -> %s", name, path)
	return path
}

// stageAShot screenshots the resident window into name.png.
func stageAShot(t *testing.T, a *shelf, name string) string {
	t.Helper()
	if a.win == nil {
		t.Fatal("no notch window")
	}
	num := mac.WindowNumber(a.win.NativeHandle())
	if num == 0 {
		t.Fatalf("%s: window has no CGWindowID", name)
	}
	time.Sleep(400 * time.Millisecond) // let the drawn frame settle
	if err := os.MkdirAll(stageADir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(stageADir, name+".png")
	out, err := exec.Command("screencapture", "-x", "-o", fmt.Sprintf("-l%d", num), path).CombinedOutput()
	if err != nil {
		t.Fatalf("%s: screencapture failed (%v): %s", name, err, out)
	}
	t.Logf("shot %s -> %s", name, path)
	return path
}

// stageAShelf is the shared smoke shelf, reset to the overview with the
// springs at rest.
func stageAShelf(t *testing.T) *shelf {
	t.Helper()
	// The test binary's resource path is cmd/shelfu/resources; the brand
	// artwork lives in the repository's resources/otter.
	if _, err := os.Stat("../../resources/otter"); err == nil {
		otterDir = func() string { return "../../resources/otter" }
		resetOtters()
	}
	a := redesignShelf(t)
	onMain(func() {
		a.place(mygo.Screen.CursorScreenPoint())
		a.status, a.statusKind, a.statusUntil = "", statusInfo, time.Time{}
		a.update, a.updateInstalled, a.updateInstalling = nil, false, false
		a.conv = nil
		a.welcomePending, a.welcomeUp = false, false
		a.whatsNewPending, a.whatsNewUp = false, false
		a.level, a.tab, a.tabFrom = 1, pageOverview, pageOverview
		a.openPage(pageOverview)
		a.visible = true
		a.progress, a.animationFrom = 1, 1
		a.animationStart = time.Time{}
		a.peeking = true
		a.peekProgress, a.peekFrom = 1, 1
		a.peekAnimStart = time.Time{}
		a.syncMousePassThrough()
		a.syncWindowFrame()
		a.win.Invalidate()
	})
	return a
}

// fillShelf puts a little of everything in the store: five tray items, a
// clipboard history and a playing track.
func fillShelf(t *testing.T, a *shelf) {
	t.Helper()
	onMain(func() {
		files := t.TempDir()
		for i, name := range []string{"报告.pdf", "截图 10.42.png", "草稿.docx", "表格.xlsx", "设计稿.png"} {
			p := writeFile(t, files, name, testPNGBytes(t))
			if err := a.store.AddFiles([]string{p}); err != nil {
				t.Fatal(err)
			}
			_ = i
		}
		for i := range 12 {
			if err := a.store.AddClipText(fmt.Sprintf("剪贴板内容 %d · 一段足够长的文字用于检查省略", i), "com.apple.Safari"); err != nil {
				t.Fatal(err)
			}
		}
		a.settings.IslandNowPlaying = true
		fakeTrack(a, true)
		a.modules = []module{a.np}
	})
}

func TestSmokeOverviewEvidence(t *testing.T) {
	if os.Getenv("SHELF_BASKET_SMOKE") != "1" {
		t.Skip("set SHELF_BASKET_SMOKE=1 to capture the panel evidence")
	}
	// The seams keep the shots reproducible whatever this Mac's appearance
	// is set to; the real appearance is restored afterwards.
	origDark, origReduce := macSystemDark, macReduceTransparency
	t.Cleanup(func() { macSystemDark, macReduceTransparency = origDark, origReduce })
	// The shots say which language they are: Chinese here, English in its
	// own subtest (the app follows the system otherwise).
	oldLocale := mygo.App.Locale()
	mygo.App.SetLocale("zh-Hans")
	t.Cleanup(func() { mygo.App.SetLocale(oldLocale) })
	macSystemDark = func() bool { return true }
	macReduceTransparency = func() bool { return false }

	t.Run("overview-music", func(t *testing.T) {
		a := stageAShelf(t)
		fillShelf(t, a)
		stageAShot(t, a, "overview-music")
	})

	t.Run("overview-empty", func(t *testing.T) {
		a := stageAShelf(t)
		stageAShot(t, a, "overview-empty")
	})

	t.Run("overview-copied", func(t *testing.T) {
		a := stageAShelf(t)
		onMain(func() {
			if err := a.store.AddClipText("明天下午三点在 3 楼会议室", "com.tencent.xinWeChat"); err != nil {
				t.Fatal(err)
			}
		})
		stageAShot(t, a, "overview-copied")
	})

	t.Run("overview-converting", func(t *testing.T) {
		a := stageAShelf(t)
		fillShelf(t, a)
		onMain(func() {
			job := &convertJob{target: toJPEG, started: time.Now().Add(-2 * time.Second)}
			job.total.Store(3)
			job.done.Store(1)
			a.conv = &converter{host: a, job: job}
		})
		stageAShot(t, a, "overview-converting")
	})

	t.Run("overview-error", func(t *testing.T) {
		a := stageAShelf(t)
		fillShelf(t, a)
		onMain(func() {
			a.status = "没转成功：报告.pdf 已损坏"
			a.statusKind = statusErr
			a.statusUntil = time.Now().Add(2 * time.Second)
		})
		stageAShot(t, a, "overview-error")
	})

	t.Run("overview-update", func(t *testing.T) {
		a := stageAShelf(t)
		fillShelf(t, a)
		onMain(func() { a.updateInstalled = true })
		stageAShot(t, a, "overview-update")
	})

	t.Run("overview-light", func(t *testing.T) {
		macSystemDark = func() bool { return false }
		t.Cleanup(func() { macSystemDark = func() bool { return true } })
		// A bright, high-contrast window behind the panel: the shot then
		// shows whether the desktop behind is blurred or merely dimmed.
		var probe *mygo.Window
		onMain(func() {
			probe = mygo.NewWindow(mygo.WindowOptions{Title: "probe", Width: 1000, Height: 420, Frameless: true})
			probe.SetPosition(260, 20)
			probe.Show()
			probe.Page().LoadHTML(`<body style="margin:0;background:#fff">
<div style="font:700 56px -apple-system;color:#000;padding:8px 14px">BLUR TEST 1 2 3</div>
<div style="font:700 56px -apple-system;color:#d00;padding:8px 14px">BLUR TEST 4 5 6</div>
<div style="font:700 56px -apple-system;color:#06f;padding:8px 14px">BLUR TEST 7 8 9</div></body>`, "")
		})
		time.Sleep(500 * time.Millisecond)
		a := stageAShelf(t)
		fillShelf(t, a)
		onMain(func() {
			a.refreshAppearance(time.Now().Add(time.Second))
			a.syncWindowFrame()
			a.win.Invalidate()
		})
		stageAShot(t, a, "overview-light")
		stageAShotRegion(t, a, "overview-light-desktop")
		onMain(func() { a.setTab(pageClipboard) })
		stageAShot(t, a, "clipboard-light")
		stageAShotRegion(t, a, "clipboard-light-desktop")
		onMain(func() { probe.Destroy() })
	})

	t.Run("pages", func(t *testing.T) {
		a := stageAShelf(t)
		fillShelf(t, a)
		for _, p := range []struct {
			name string
			id   pageID
		}{
			{"page-playing", pagePlaying},
			{"page-tray", pageTray},
			{"page-clipboard", pageClipboard},
			{"page-tools", pageTools},
		} {
			onMain(func() {
				a.openPage(p.id)
				a.win.Invalidate()
			})
			stageAShot(t, a, p.name)
		}
		// The clipboard scrolled to the middle: both edges blur.
		onMain(func() {
			a.openPage(pageClipboard)
			if p, ok := a.page(pageClipboard).(*clipboardPage); ok {
				p.scroll.Y = 60
			}
			a.win.Invalidate()
		})
		stageAShot(t, a, "page-clipboard-scrolled")
		// The tools grid in edit mode.
		onMain(func() {
			a.openPage(pageTools)
			if p := a.toolsPage(); p != nil {
				p.editing = true
				p.editAt = time.Now()
			}
			a.win.Invalidate()
		})
		stageAShot(t, a, "page-tools-edit")
		onMain(func() {
			if p := a.toolsPage(); p != nil {
				p.editing = false
			}
		})
	})

	t.Run("drop-zone", func(t *testing.T) {
		a := stageAShelf(t)
		fillShelf(t, a)
		t.Setenv("SHELF_DEBUG_DROP_FILES", "/tmp/shelf-stagef-a.png,/tmp/shelf-stagef-b.png")
		onMain(func() {
			a.dragging = true
			a.inBand = true
			a.openPage(pageTray)
			a.dropZoneSnap(true)
			a.win.Invalidate()
		})
		stageAShot(t, a, "drop-zone")
		onMain(func() {
			a.dragging = false
			a.drop = dropZoneState{}
		})
	})

	t.Run("welcome", func(t *testing.T) {
		a := stageAShelf(t)
		onMain(func() {
			a.welcomePending, a.welcomeUp = true, true
			a.win.Invalidate()
		})
		stageAShot(t, a, "welcome")
	})

	t.Run("whats-new", func(t *testing.T) {
		a := stageAShelf(t)
		onMain(func() {
			a.whatsNewPending, a.whatsNewUp = true, true
			a.win.Invalidate()
		})
		stageAShot(t, a, "whats-new")
	})

	t.Run("english", func(t *testing.T) {
		old := mygo.App.Locale()
		mygo.App.SetLocale("en")
		t.Cleanup(func() { mygo.App.SetLocale(old) })
		a := stageAShelf(t)
		fillShelf(t, a)
		stageAShot(t, a, "overview-en")
		onMain(func() { a.openPage(pageTray) })
		stageAShot(t, a, "page-tray-en")
	})
}

// TestSmokeOverviewFrames records the native window's size and the drawn
// springs per frame while the panel opens, grows to a page and folds back,
// so the timing and the overshoot can be read off the numbers.
func TestSmokeOverviewFrames(t *testing.T) {
	if os.Getenv("SHELF_BASKET_SMOKE") != "1" {
		t.Skip("set SHELF_BASKET_SMOKE=1 to capture the frame timings")
	}
	a := stageAShelf(t)
	fillShelf(t, a)
	if err := os.MkdirAll(stageADir, 0o755); err != nil {
		t.Fatal(err)
	}
	num := mac.WindowNumber(a.win.NativeHandle())

	sample := func(name string, start func(), span time.Duration) {
		t.Helper()
		path := filepath.Join(stageADir, name+".csv")
		f, err := os.Create(path)
		if err != nil {
			t.Fatal(err)
		}
		defer f.Close()
		w := csv.NewWriter(f)
		defer w.Flush()
		_ = w.Write([]string{"ms", "width", "height", "openK", "levelK"})
		base := time.Now()
		onMain(start)
		for time.Since(base) < span {
			var rw, rh, okF, openK, levelK float64
			onMain(func() {
				if r, got := mac.WindowScreenRect(num); got {
					rw, rh, okF = r.Size.Width, r.Size.Height, 1
				}
				openK, levelK = float64(a.fx.openK), float64(a.fx.levelK)
			})
			if okF == 1 {
				_ = w.Write([]string{
					strconv.FormatInt(time.Since(base).Milliseconds(), 10),
					strconv.FormatFloat(rw, 'f', 1, 64),
					strconv.FormatFloat(rh, 'f', 1, 64),
					strconv.FormatFloat(openK, 'f', 3, 64),
					strconv.FormatFloat(levelK, 'f', 3, 64),
				})
			}
			time.Sleep(4 * time.Millisecond)
		}
		t.Logf("frames -> %s", path)
	}

	// Close first, so the open starts from the notch.
	onMain(func() {
		a.hide(hideExplicit)
		a.closeAt = time.Time{}
		a.finishClose()
		a.syncWindowFrame()
	})
	time.Sleep(300 * time.Millisecond)
	sample("frames-open", func() { a.show(false) }, 600*time.Millisecond)
	time.Sleep(200 * time.Millisecond) // the opening spring comes to rest
	sample("frames-grow", func() { a.openPage(pageTray) }, 600*time.Millisecond)
	time.Sleep(200 * time.Millisecond)
	sample("frames-close", func() { a.hide(hideExplicit) }, 450*time.Millisecond)
}

// TestSmokeAppearanceProbe reports what the system appearance seam answers,
// so the evidence set says which appearance the shots ran in.
func TestSmokeAppearanceProbe(t *testing.T) {
	if os.Getenv("SHELF_BASKET_SMOKE") != "1" {
		t.Skip("set SHELF_BASKET_SMOKE=1 to read the system appearance")
	}
	dark, reduce := mac.SystemDark(), mac.ReduceTransparency()
	t.Logf("system appearance: dark=%v reduceTransparency=%v", dark, reduce)
	if !dark {
		a := stageAShelf(t)
		fillShelf(t, a)
		onMain(func() {
			a.refreshAppearance(time.Now().Add(time.Second))
			a.syncWindowFrame()
			a.win.Invalidate()
		})
		stageAShot(t, a, "overview-system-light")
	}
}
