//go:build darwin

package main

// The two scrolling pages and their edges: the tray grid draws every tile
// exactly as it is at every scroll offset, and the clipboard list melts
// only on the rows that reach into an edge with more behind it.

import (
	"fmt"
	"testing"

	"github.com/egoist/mygo/ui"

	"shelf/internal/settings"
	"shelf/internal/store"
)

// blurredRows collects every row a frame hands the painter a blur, by row
// id and sigma.
func blurredRows(t *testing.T, render func()) map[string]float32 {
	t.Helper()
	got := map[string]float32{}
	old := onRowBlur
	onRowBlur = func(_ *shelf, id string, sigma float32) { got[id] = sigma }
	t.Cleanup(func() { onRowBlur = old })
	render()
	onRowBlur = old
	return got
}

// blurShelf is a headless shelf on level 2 with an empty store and one
// thumbnail sprite: every tile in the tests below holds the same picture, so
// two tiles at the same scroll offset must render the same sharp pixels.
func blurShelf(t *testing.T) (*shelf, *ui.Tester) {
	t.Helper()
	s, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	a := &shelf{
		store: s, dataDir: t.TempDir(), level: 2, visible: true, settings: settings.Default(),
		thumbnails: map[string]*ui.Bitmap{}, thumbPending: map[string]bool{}, appNames: map[string]string{},
	}
	return a, ui.NewTester(a.view, sheetW, sheetH)
}

// TestTrayGridNeverBlursARow: the tray grid is the page's content, not rows
// melting into it. A tile is 85pt tall and the column 5 tiles wide, so an
// edge melt would blur the whole thumbnail and its name — the user read the
// scrolled tray as entirely blurred. Scrolled past the first screen, to the
// middle and to the end, no tile carries a blur.
func TestTrayGridNeverBlursARow(t *testing.T) {
	a, view := blurShelf(t)
	png := testPNGBytes(t)
	// The same picture under the same name in a directory of its own: the
	// store tells the files apart by path, the grid shows 12 identical
	// tiles, and the shots of two tiles can be compared pixel for pixel.
	for range 12 {
		path := writeFile(t, t.TempDir(), "图片.png", png)
		if err := a.store.AddFiles([]string{path}); err != nil {
			t.Fatal(err)
		}
	}
	a.setTab(pageTray)
	view.Frame()
	view.Frame()
	p := a.page(pageTray).(*trayPage)
	if got := len(p.items()); got != 12 {
		t.Fatalf("the tray holds %d items, want the 12 the test added", got)
	}
	if p.scroll.MaxY <= 0 {
		t.Fatalf("the 12 tiles do not scroll: MaxY=%.0f", p.scroll.MaxY)
	}
	// The tiles really drew at the top: a blank grid would make the checks
	// below vacuous.
	if r, ok := view.Find(itemTitle(p.items()[0])); !ok || r.H < tileH {
		t.Fatalf("the grid drew no full tile to judge: %+v", r)
	}
	for _, y := range []float32{0, p.scroll.MaxY / 2, p.scroll.MaxY} {
		blur := blurredRows(t, func() {
			p.scroll.Y = y
			view.Frame()
			view.Frame()
		})
		if got := p.scroll.Y; got != y {
			t.Fatalf("the grid did not scroll to %.0f: Y=%.0f", y, got)
		}
		if len(blur) > 0 {
			t.Fatalf("at Y=%.0f of %.0f the tray grid blurs %d rows: %v", y, p.scroll.MaxY, len(blur), blur)
		}
	}
}

// TestClipboardListMeltsOnlyAtTheEdges: the clipboard keeps its edge melt —
// a row that reaches into an active band is blurred, most at the edge — but
// a row in the open is not, and the shot of the list shows the same split:
// the edge row's meta text is several times less sharp than the middle's.
func TestClipboardListMeltsOnlyAtTheEdges(t *testing.T) {
	a, view := blurShelf(t)
	for i := range 24 {
		if err := a.store.AddClipText(fmt.Sprintf("剪贴板第 %d 条：把这段文字复制到别处 %d", i+1, i+1), "com.example.App"); err != nil {
			t.Fatal(err)
		}
	}
	a.setTab(pageClipboard)
	view.Frame()
	view.Frame()
	p := a.page(pageClipboard).(*clipboardPage)
	list := p.items()
	if p.scroll.MaxY <= 0 {
		t.Fatalf("the 24 rows do not scroll: MaxY=%.0f", p.scroll.MaxY)
	}
	p.scroll.Y = p.scroll.MaxY / 2
	var blur map[string]float32
	blur = blurredRows(t, func() {
		view.Frame()
		view.Frame()
	})
	if len(blur) == 0 {
		t.Fatal("a list scrolled to its middle blurs no row at all: the melt is gone")
	}
	// The rows on screen, from the layout: a row touching the top or the
	// bottom of the visible list reaches into a band and must melt; a row
	// between them must not.
	top, bottom := float32(0), float32(0)
	boxes := map[string]ui.Rect{}
	for _, item := range list {
		r, ok := view.Find(itemTitle(item))
		if !ok || r.W == 0 {
			continue
		}
		boxes[item.ID] = r
		if bottom == 0 || r.Y < top {
			top = r.Y
		}
		if r.Y+r.H > bottom {
			bottom = r.Y + r.H
		}
	}
	if len(boxes) < 4 {
		t.Fatalf("only %d rows are on screen, too few to tell an edge row from a middle one", len(boxes))
	}
	middle := 0
	for _, item := range list {
		r, ok := boxes[item.ID]
		if !ok {
			continue
		}
		edge := r.Y <= top+0.5 || r.Y+r.H >= bottom-0.5
		sigma, melted := blur[item.ID]
		switch {
		case edge && !melted:
			t.Fatalf("the row at %+v reaches the edge of the %.0f..%.0f list and does not melt", r, top, bottom)
		case !edge && melted:
			t.Fatalf("the row at %+v is in the open, %.0fpt from either edge, and blurs by %.1f", r, r.Y-top, sigma)
		case melted && sigma > edgeBandFor(clipRowH)*edgeBlurPerPT+0.001:
			t.Fatalf("the edge row blurs by %.2f, more than the %.2f the edge allows", sigma, edgeBandFor(clipRowH)*edgeBlurPerPT)
		case !melted:
			middle++
		}
	}
	if middle < 2 {
		t.Fatalf("the middle of the list holds %d sharp rows: the list is not long enough to show the split", middle)
	}
	// The same words on every row's right end: the edge rows' meta text is
	// what the blur softens, and the sharp rows all carry it alike.
	var sharp, soft int
	for _, item := range list {
		r, ok := boxes[item.ID]
		if !ok {
			continue
		}
		got := bandSharpness(view.Image(), int(r.X+r.W-140), int(r.X+r.W-8), int(r.Y+8), int(r.Y+24))
		if _, melted := blur[item.ID]; melted {
			soft = max(soft, got)
			continue
		}
		sharp = max(sharp, got)
	}
	if sharp < 3*soft {
		t.Fatalf("the open rows are not sharper than the edge rows: %d against %d", sharp, soft)
	}
}
