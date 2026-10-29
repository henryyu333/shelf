//go:build darwin

package main

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"shelf/internal/mac"
	"shelf/internal/store"
)

// The redesign shots render the real windows of DESIGN.md so a human can
// compare them with design/mock.png. Run with SHELF_BASKET_SMOKE=1; each
// PNG lands in /tmp/shelf-redesign/<name>.png. The test instance uses the
// smoke data dir and never touches ~/Library/Application Support/ShelfU.
const redesignShotDir = "/tmp/shelf-redesign"

// shotWindow screenshots the native window w to name.png and returns its
// path.
func shotWindow(t *testing.T, w *mygo.Window, name string) string {
	t.Helper()
	num := mac.WindowNumber(w.NativeHandle())
	if num == 0 {
		t.Fatalf("%s: window has no CGWindowID", name)
	}
	time.Sleep(350 * time.Millisecond) // let the drawn frame settle
	if err := os.MkdirAll(redesignShotDir, 0o755); err != nil {
		t.Fatal(err)
	}
	shot := filepath.Join(redesignShotDir, name+".png")
	out, err := exec.Command("screencapture", "-x", "-o", fmt.Sprintf("-l%d", num), shot).CombinedOutput()
	if err != nil {
		t.Fatalf("%s: screencapture failed (%v): %s", name, err, out)
	}
	t.Logf("shot %s -> %s", name, shot)
	return shot
}

// shotNotch screenshots the resident notch window.
func shotNotch(t *testing.T, a *shelf, name string) string {
	t.Helper()
	if a.win == nil {
		t.Fatal("no notch window")
	}
	return shotWindow(t, a.win, name)
}

// testCover returns a colourful 4-quadrant artwork so the card and pill
// have a recognisable cover.
func testCover() *image.RGBA {
	art := image.NewRGBA(image.Rect(0, 0, 64, 64))
	for y := range 64 {
		for x := range 64 {
			switch {
			case x < 32 && y < 32:
				art.Set(x, y, color.RGBA{R: 224, G: 88, B: 64, A: 255})
			case x >= 32 && y < 32:
				art.Set(x, y, color.RGBA{R: 64, G: 96, B: 224, A: 255})
			case x < 32:
				art.Set(x, y, color.RGBA{R: 240, G: 190, B: 60, A: 255})
			default:
				art.Set(x, y, color.RGBA{R: 150, G: 64, B: 200, A: 255})
			}
		}
	}
	return art
}

// fakeTrack installs a playing track (with the test cover) on the shelf's
// module without starting the helper host.
func fakeTrack(a *shelf, playing bool) {
	mod := &nowPlayingModule{host: a}
	mod.rememberArt(testCover(), false)
	mod.have = true
	mod.last = mac.NowPlayingState{
		Playing: playing, Title: "Fashion Killa", Artist: "A$AP Rocky", App: "com.apple.Music",
		Duration: 236, Elapsed: 81, Rate: 1, Received: time.Now(),
	}
	a.np = mod
}

// expandShelf puts the real notch window into the fully expanded state:
// the sheet on its current page, with the springs already at rest.
func expandShelf(t *testing.T, a *shelf) {
	t.Helper()
	onMain(func() {
		a.place(mygo.Screen.CursorScreenPoint())
		a.peeking = true
		a.peekProgress, a.peekFrom = 1, 1
		want := a.tab // show() opens on the overview; keep the test's page
		a.show(false)
		a.setLevel(2)
		if want != "" && want != pageOverview {
			a.setTab(want)
		}
		a.jumpLevel = true
		a.progress, a.animationFrom = 1, 1
		a.animationStart = time.Time{}
		a.syncMousePassThrough()
		a.syncWindowFrame()
		a.win.Invalidate()
	})
}

// peekShelf puts the window into the resting peek state.
func peekShelf(a *shelf) {
	onMain(func() {
		a.visible, a.edgeOpened = false, false
		a.progress, a.animationFrom = 0, 0
		a.animationStart = time.Time{}
		a.peeking = true
		a.peekProgress, a.peekFrom = 1, 1
		a.peekAnimStart = time.Time{}
		a.syncMousePassThrough()
		a.syncWindowFrame()
		a.win.Invalidate()
	})
}

// redesignShelf returns the shared smoke shelf with a clean slate: store
// emptied (pinned items included), now-playing off, tabs at tray.
func redesignShelf(t *testing.T) *shelf {
	t.Helper()
	if os.Getenv("SHELF_BASKET_SMOKE") != "1" {
		t.Skip("set SHELF_BASKET_SMOKE=1 to run the redesign smoke tests")
	}
	a := smokeShelf
	if a.win == nil {
		onMain(func() { a.newNotchWindow() })
	}
	if a.win == nil {
		t.Fatal("no notch window")
	}
	// Esc/Space registration would bind global keys from the test process.
	origReg, origUnreg := registerKey, unregisterKey
	registerKey = func(string, func()) error { return nil }
	unregisterKey = func(string) {}
	t.Cleanup(func() { registerKey, unregisterKey = origReg, origUnreg })
	onMain(func() {
		// Clear() keeps pinned items; unpin everything first so each shot
		// starts from an empty shelf.
		for _, it := range a.store.Items {
			if it.Pinned {
				_ = a.store.SetPinned(it.ID, false)
			}
		}
		_ = a.store.Clear()
		a.dropStaleThumbnails()
		a.modules, a.np = nil, nil
		a.search, a.focusMode = "", false
		a.settings.IslandNowPlaying = false
		a.setTab(pageTray)
		// Each shot starts from the closed panel; a previous test may have
		// left it open, and redesignShelf is the shared reset.
		a.visible, a.closeAt, a.progress, a.animationFrom = false, time.Time{}, 0, 0
		a.animationStart = time.Time{}
		a.peeking, a.peekProgress, a.peekFrom, a.peekAnimStart = false, 0, 0, time.Time{}
		a.overviewDue = time.Time{}
	})
	return a
}

// TestRedesignPeekMusic shoots the music variant of the peek pill: the
// cover at the left and the EQ bars at the right, notch +120 wide.
func TestRedesignPeekMusic(t *testing.T) {
	a := redesignShelf(t)
	onMain(func() {
		a.settings.IslandNowPlaying = true
		fakeTrack(a, true)
	})
	peekShelf(a)
	shotNotch(t, a, "peek-music")
	peekRestore(t, a)
}

// TestRedesignPeekPlain shoots the no-music variant: the downward chevron.
func TestRedesignPeekPlain(t *testing.T) {
	a := redesignShelf(t)
	onMain(func() {
		a.settings.IslandNowPlaying = false
		a.np = nil
	})
	peekShelf(a)
	shotNotch(t, a, "peek-plain")
	peekRestore(t, a)
}

// TestRedesignPeekDrop shoots the drop pill a file drag raises inside the
// notch band: the 「松开放入」 line must sit wholly below the hardware notch.
func TestRedesignPeekDrop(t *testing.T) {
	a := redesignShelf(t)
	onMain(func() { a.dragging, a.dropHot = true, true })
	peekShelf(a)
	shotNotch(t, a, "peek-drop")
	onMain(func() { a.dragging, a.dropHot = false, false })
	peekRestore(t, a)
}

// TestRedesignPeekDragHint shoots the new drag-in-flight hint: a band-high
// shell, a little wider than the notch, no glyph — the state every drag
// outside the notch band now sees.
func TestRedesignPeekDragHint(t *testing.T) {
	a := redesignShelf(t)
	onMain(func() { a.dragging, a.dropHot = true, false })
	peekShelf(a)
	shotNotch(t, a, "peek-drag-hint")
	onMain(func() { a.dragging = false })
	peekRestore(t, a)
}

// peekRestore returns the window to its resting collapsed state.
func peekRestore(t *testing.T, a *shelf) {
	onMain(func() {
		a.peeking = false
		a.peekProgress, a.peekFrom = 0, 0
		a.visible, a.progress = false, 0
		a.syncMousePassThrough()
		a.syncWindowFrame()
		a.win.Invalidate()
	})
}

// TestRedesignTrayMusic shoots the expanded tray page: the ear bar, the
// now-playing activity, the module bar and the item grid.
func TestRedesignTrayMusic(t *testing.T) {
	a := redesignShelf(t)
	// Fixtures: a text item (note-card tile), an image with a warmed
	// thumbnail and a file with its Finder icon.
	var pngb []byte
	var thumb *ui.Bitmap
	{
		img := image.NewRGBA(image.Rect(0, 0, 48, 32))
		for y := range 32 {
			for x := range 48 {
				img.Set(x, y, color.RGBA{R: uint8(4 * x), G: 100, B: uint8(6 * y), A: 255})
			}
		}
		thumb = ui.NewBitmap(img)
		var buf bytes.Buffer
		if err := png.Encode(&buf, img); err != nil {
			t.Fatal(err)
		}
		pngb = buf.Bytes()
	}
	dir := t.TempDir()
	doc := filepath.Join(dir, "发布说明.md")
	if err := os.WriteFile(doc, []byte("hi"), 0o600); err != nil {
		t.Fatal(err)
	}
	onMain(func() {
		a.settings.IslandNowPlaying = true
		fakeTrack(a, true)
		a.modules = []module{a.np} // the activity strip asks modules for live activities
		_ = a.store.AddText("周五发布 ShelfU v1.0 · 记得更新截图")
		_ = a.store.AddImage(pngb)
		_ = a.store.AddFiles([]string{doc})
		// 置顶图片项：展示右上角图钉角标。
		for _, it := range a.store.Items {
			if it.Kind == store.KindImage {
				_ = a.store.SetPinned(it.ID, true)
				break
			}
		}
		// 预热缩略图：测试里 a.win 存在但异步解码名额不可控，直接注入。
		for _, it := range a.store.Items {
			switch it.Kind {
			case store.KindImage:
				a.thumbnails[it.ID] = thumb
			case store.KindFile:
				if icon := mac.FileIcon(it.Path, 56); len(icon) > 0 {
					a.thumbnails[it.ID], _ = ui.DecodeBitmap(icon)
				}
			}
		}
		a.setTab(pageTray)
	})
	expandShelf(t, a)
	shotNotch(t, a, "tray-with-music")
}

// TestRedesignTrayEmpty shoots the tray's empty state.
func TestRedesignTrayEmpty(t *testing.T) {
	a := redesignShelf(t)
	onMain(func() {
		a.np = nil
		_ = a.store.Clear()
		a.dropStaleThumbnails()
		a.setTab(pageTray)
	})
	expandShelf(t, a)
	shotNotch(t, a, "tray-empty")
}

// TestRedesignClipboardSearch shoots the clipboard tab mid-search: the
// field filled, rows filtered, the hint pill at the bottom.
func TestRedesignClipboardSearch(t *testing.T) {
	a := redesignShelf(t)
	onMain(func() {
		a.np = nil
		_ = a.store.AddClipText("购物清单：牛奶、鸡蛋、面包", "com.apple.Notes")
		_ = a.store.AddClipText("https://example.com/release-notes", "com.apple.Safari")
		_ = a.store.AddClipText("货架设计评审 周五 15:00", "com.apple.Calendar")
		a.setTab(pageClipboard)
		a.search = "清"
		a.focusMode = true
	})
	expandShelf(t, a)
	shotNotch(t, a, "clipboard-search")
	onMain(func() { a.search, a.focusMode = "", false })
}

// TestRedesignClipboardMusic shoots the clipboard page opened by mouse:
// the ears stay in the notch band and the activity strip sits above the
// list, as on the tray.
func TestRedesignClipboardMusic(t *testing.T) {
	a := redesignShelf(t)
	onMain(func() {
		a.settings.IslandNowPlaying = true
		fakeTrack(a, true)
		a.modules = []module{a.np}
		_ = a.store.AddClipText("购物清单：牛奶、鸡蛋、面包", "com.apple.Notes")
		_ = a.store.AddClipText("https://example.com/release-notes", "com.apple.Safari")
		_ = a.store.AddClipText("货架设计评审 周五 15:00", "com.apple.Calendar")
		a.setTab(pageClipboard)
	})
	expandShelf(t, a)
	shotNotch(t, a, "clipboard-music")
}

// TestRedesignBasket shoots the shake-summoned drop card, idle and with
// the accent ring on. A live drag-over can't be faked, so the ring shot
// uses the dropped state, which shares the same border path.
func TestRedesignBasket(t *testing.T) {
	a := redesignShelf(t)
	peekRestore(t, a)
	cursor := mygo.Screen.CursorScreenPoint()
	onMain(func() { a.showBasket(cursor) })
	if a.basket.win == nil {
		t.Fatal("basket window was not created")
	}
	defer onMain(a.closeBasket)
	shotWindow(t, a.basket.win, "basket")
	onMain(func() {
		a.basket.dropped = true
		a.basket.win.Invalidate()
	})
	shotWindow(t, a.basket.win, "basket-over")
	onMain(func() { a.basket.dropped = false })
}

var _ = ui.Color{} // keep ui imported for the module seam types
