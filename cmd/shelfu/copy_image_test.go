//go:build darwin

package main

// Tests for 「复制图片」: the context-menu entry, the tray page's ⌘C, and
// the PNG encoding of picture files that are not PNG already.

import (
	"bytes"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"shelf/internal/settings"
	"shelf/internal/store"
)

// copyImageShelf returns a shelf whose tray holds one picture: a managed
// clipboard screenshot (KindImage) or a PNG file (KindFile), a headless
// view of it, the copy recorder and the item.
func copyImageShelf(t *testing.T, file bool) (*shelf, *ui.Tester, *recordClip, store.Item, []byte) {
	t.Helper()
	dir := t.TempDir()
	s, err := store.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	pngBytes := testPNG(t)
	if file {
		p := filepath.Join(t.TempDir(), "海边.png")
		if err := os.WriteFile(p, pngBytes, 0o600); err != nil {
			t.Fatal(err)
		}
		if err := s.AddFiles([]string{p}); err != nil {
			t.Fatal(err)
		}
	} else if err := s.AddImage(pngBytes); err != nil {
		t.Fatal(err)
	}
	rec := &recordClip{}
	a := &shelf{
		store: s, dataDir: dir, visible: true, settings: settings.Default(),
		thumbnails: map[string]*ui.Bitmap{}, thumbPending: map[string]bool{},
		appNames: map[string]string{}, write: rec,
	}
	return a, ui.NewTester(a.view, sheetW, sheetH), rec, a.visibleItems()[0], pngBytes
}

// imageFileShelf returns a shelf whose tray holds the named PNG files.
func imageFileShelf(t *testing.T, names ...string) (*shelf, *ui.Tester) {
	t.Helper()
	s, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	files := t.TempDir()
	for _, name := range names {
		p := filepath.Join(files, name)
		if err := os.WriteFile(p, testPNG(t), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := s.AddFiles([]string{p}); err != nil {
			t.Fatal(err)
		}
	}
	a := &shelf{
		store: s, dataDir: t.TempDir(), visible: true, settings: settings.Default(),
		thumbnails: map[string]*ui.Bitmap{}, thumbPending: map[string]bool{},
		appNames: map[string]string{}, write: &recordClip{},
	}
	return a, ui.NewTester(a.view, sheetW, sheetH)
}

// The item menu offers 「复制图片」 for both screenshots and picture files,
// and choosing it writes the picture's PNG bytes, with the usual toast.
func TestItemMenuCopiesImageData(t *testing.T) {
	for _, file := range []bool{false, true} {
		name := "screenshot"
		if file {
			name = "picture file"
		}
		t.Run(name, func(t *testing.T) {
			a, view, rec, item, pngBytes := copyImageShelf(t, file)
			if err := view.RightClick(itemTitle(item)); err != nil {
				t.Fatal(err)
			}
			if !slices.Contains(view.Menu(), "复制图片") {
				t.Fatalf("menu = %v, want a 复制图片 entry", view.Menu())
			}
			if err := view.ChooseMenuItem("复制图片"); err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(rec.png, pngBytes) {
				t.Fatalf("copied %d bytes, want the item's %d PNG bytes", len(rec.png), len(pngBytes))
			}
			if rec.text != "" {
				t.Fatalf("copying an image also wrote text %q", rec.text)
			}
			if !strings.Contains(a.status, "已复制图片") {
				t.Fatalf("status = %q, want the 已复制图片 toast", a.status)
			}
		})
	}
}

// ⌘C on the tray page copies exactly one selected picture; plain files keep
// their existing behavior (no clipboard write).
func TestTrayCmdCCopiesSelectedPicture(t *testing.T) {
	_, view, rec, item, pngBytes := copyImageShelf(t, false)
	if err := view.Click(itemTitle(item)); err != nil {
		t.Fatal(err)
	}
	view.Key(ui.Cmd, ui.KeyC)
	if !bytes.Equal(rec.png, pngBytes) {
		t.Fatalf("⌘C wrote %d PNG bytes, want %d", len(rec.png), len(pngBytes))
	}

	// A text file stays silent: ⌘C is only the picture shortcut here.
	files, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { files.Close() })
	p := filepath.Join(t.TempDir(), "notes.txt")
	if err := os.WriteFile(p, []byte("hello"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := files.AddFiles([]string{p}); err != nil {
		t.Fatal(err)
	}
	rec2 := &recordClip{}
	b := &shelf{
		store: files, dataDir: t.TempDir(), visible: true, settings: settings.Default(),
		thumbnails: map[string]*ui.Bitmap{}, thumbPending: map[string]bool{},
		appNames: map[string]string{}, write: rec2,
	}
	bview := ui.NewTester(b.view, sheetW, sheetH)
	if err := bview.Click("notes.txt"); err != nil {
		t.Fatal(err)
	}
	bview.Key(ui.Cmd, ui.KeyC)
	if rec2.png != nil || rec2.text != "" {
		t.Fatalf("⌘C on a text file copied %q/%d bytes; want nothing", rec2.text, len(rec2.png))
	}
}

// 复制图片 only works on one item: a multi-selection targets every row and
// the entry is disabled.
func TestCopyImageDisabledForMultipleSelection(t *testing.T) {
	_, view := imageFileShelf(t, "a.png", "b.png")
	if err := view.Click("a.png"); err != nil {
		t.Fatal(err)
	}
	if err := view.ClickWith(ui.Cmd, "b.png"); err != nil {
		t.Fatal(err)
	}
	if err := view.RightClick("b.png"); err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(view.Menu(), "复制图片") {
		t.Fatalf("menu = %v, want a 复制图片 entry", view.Menu())
	}
	err := view.ChooseMenuItem("复制图片")
	if err == nil || !strings.Contains(err.Error(), "disabled") {
		t.Fatalf("multi-selection chose 复制图片 (%v); want it disabled", err)
	}
}

// The tray's ⌘C cannot reach a non-activating panel (the overlay never
// becomes the key window), so the key is held globally while exactly one
// selected picture can be copied, and released everywhere else so the
// frontmost app keeps it (PLAN.md 验收反馈).
func TestWantsCopyKeyOnlyForOneSelectedPicture(t *testing.T) {
	a, _ := imageFileShelf(t, "one.png", "two.png")
	pick := func(ids ...string) {
		t.Helper()
		if len(ids) == 0 {
			a.selection.clear()
		} else {
			a.selection.onlyAll(ids)
		}
	}
	images := a.visibleItems()
	if len(images) != 2 {
		t.Fatalf("tray has %d items, want 2", len(images))
	}

	pick(images[0].ID)
	if !a.wantsCopyKey() {
		t.Fatal("one selected picture must want ⌘C")
	}
	pick(images[0].ID, images[1].ID)
	if a.wantsCopyKey() {
		t.Fatal("two selected pictures cannot be copied: ⌘C must go to the app")
	}
	pick()
	if a.wantsCopyKey() {
		t.Fatal("no selection must not hold ⌘C")
	}

	pick(images[0].ID)
	a.tab = pageClipboard
	if a.wantsCopyKey() {
		t.Fatal("the clipboard page must not take ⌘C")
	}
	a.tab = pageTray
	a.visible = false
	if a.wantsCopyKey() {
		t.Fatal("a hidden panel must not hold ⌘C")
	}
	a.visible = true
	a.focusMode = true
	if a.wantsCopyKey() {
		t.Fatal("clipboard search must keep ⌘C for the search field")
	}
	a.focusMode = false
	a.settingsWin = &mygo.Window{}
	if a.wantsCopyKey() {
		t.Fatal("the settings window's keyboard must keep ⌘C")
	}
}

// syncCopyKey registers the global key only while the tray wants it, and
// the registered callback is the real copy path.
func TestSyncCopyKeyHoldsAndReleases(t *testing.T) {
	dir := t.TempDir()
	s, err := store.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	pngBytes := testPNG(t)
	if err := s.AddImage(pngBytes); err != nil {
		t.Fatal(err)
	}
	item := s.Items[0]
	rec := &recordClip{}
	a := &shelf{store: s, dataDir: dir, visible: true, level: 2, tab: pageTray,
		write: rec, win: &mygo.Window{}, escHeld: true}
	var held string
	var press func()
	origReg, origUnreg := registerKey, unregisterKey
	registerKey = func(acc string, fn func()) error {
		held, press = acc, fn
		return nil
	}
	unregisterKey = func(acc string) {
		if held == acc {
			held, press = "", nil
		}
	}
	t.Cleanup(func() { registerKey, unregisterKey = origReg, origUnreg })

	a.syncCopyKey()
	if held != "" {
		t.Fatalf("held %q with nothing selected", held)
	}
	a.selection.only(item.ID)
	a.syncCopyKey()
	a.syncCopyKey() // idempotent: no second registration
	if held != copyKey || press == nil {
		t.Fatalf("one selected picture must hold %s, held=%q", copyKey, held)
	}
	press()
	if !bytes.Equal(rec.png, pngBytes) {
		t.Fatalf("the held key copied %d bytes, want %d", len(rec.png), len(pngBytes))
	}
	a.selection.clear()
	a.syncCopyKey()
	if held != "" {
		t.Fatalf("%s still held with nothing selected", held)
	}

	// releaseKeys is the one exit every hide/handover takes.
	a.selection.only(item.ID)
	a.syncCopyKey()
	a.releaseKeys()
	if held != "" {
		t.Fatalf("%s survived releaseKeys", held)
	}
	a.syncCopyKey()
	if held != "" {
		t.Fatal("rendering after an outside click must not reclaim ⌘C")
	}
}

// A text file is not a picture: ⌘C keeps going to the frontmost app.
func TestWantsCopyKeyIgnoresPlainFiles(t *testing.T) {
	a, _ := imageFileShelf(t, "notes.txt")
	items := a.visibleItems()
	if len(items) != 1 {
		t.Fatalf("tray has %d items, want 1", len(items))
	}
	a.selection.only(items[0].ID)
	if a.wantsCopyKey() {
		t.Fatal("a text file must not hold ⌘C")
	}
}

// Formats ImageIO can read are re-encoded once so the clipboard always
// carries PNG data.
func TestItemPNGReencodesOtherFormats(t *testing.T) {
	p := filepath.Join(t.TempDir(), "photo.jpg")
	img := image.NewRGBA(image.Rect(0, 0, 3, 2))
	for x := range 3 {
		for y := range 2 {
			img.Set(x, y, color.RGBA{R: uint8(40 * x), G: uint8(80 * y), B: 120, A: 255})
		}
	}
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, nil); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, buf.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := itemPNG(store.Item{Kind: store.KindFile, Path: p})
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.HasPrefix(got, pngSignature) {
		t.Fatal("itemPNG did not return PNG data")
	}
	cfg, err := png.DecodeConfig(bytes.NewReader(got))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Width != 3 || cfg.Height != 2 {
		t.Fatalf("re-encoded picture is %dx%d, want 3x2", cfg.Width, cfg.Height)
	}
}
