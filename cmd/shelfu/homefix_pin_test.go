//go:build darwin

package main

// Desktop-pin regressions for the unified tools grid: 放回刘海 returning a
// card to its place after a restart, and a pinned widget's content never
// being clipped by a narrow pin window.

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"shelf/internal/layout"
	"shelf/internal/store"
)

// pinCanvasDrag stubs the seams a grid drag-out needs: the display set,
// the window opener, and a release point on the desktop, clear of the
// panel and of both snap edges.
func pinCanvasDrag(t *testing.T) {
	t.Helper()
	withPinDisplays(t, pinTestDisplays)
	oldOpen, oldCursor := pinOpenWindow, cursorPoint
	pinOpenWindow = func(*pinItem) {}
	cursorPoint = func() mygo.Point { return mygo.Point{X: 1400, Y: 500} }
	t.Cleanup(func() { pinOpenWindow, cursorPoint = oldOpen, oldCursor })
}

// pinFixture fills the tray and clipboard the widget cards read.
func pinFixture(t *testing.T, a *shelf) {
	t.Helper()
	a.store.Items = append(a.store.Items,
		store.Item{ID: "r1", Kind: store.KindFile, Path: filepath.Join(t.TempDir(), "alpha.png")},
		store.Item{ID: "r2", Kind: store.KindFile, Path: filepath.Join(t.TempDir(), "bravo.pdf")},
		store.Item{ID: "r3", Kind: store.KindFile, Path: filepath.Join(t.TempDir(), "charlie.txt")},
		store.Item{ID: "c1", Kind: store.KindText, Text: "剪贴板里的一段很长的文字", Source: store.SourceClipboard},
	)
}

// dropPin drags one card out of the panel and returns the shelf and the pin
// it created. The release point is on the desktop, outside the panel box.
func dropPin(t *testing.T, id string, size ...int) (*shelf, *pinItem) {
	t.Helper()
	a, _ := musicShelf(t)
	pinFixture(t, a)
	a.x, a.y = 400, 0
	a.frame = mygo.Rectangle{X: a.x, Y: a.y, Width: sheetW, Height: sheetH}
	a.setTab(pageTools)
	if len(size) == 2 {
		a.mutateTools(func(l *layout.Layout) { l.SetCardSize(id, size[0], size[1]) })
	}
	p := a.toolsPage()
	p.editing = true
	d := &toolPress{id: id, dragging: true, grabDX: 10, grabDY: 10, px: 60, py: 1200}
	p.toolCardDrop(d, mygo.Point{X: 60, Y: 1200})
	if len(a.pins) != 1 || a.pins[0].cardID != id {
		t.Fatalf("drag-out did not pin %s: %+v", id, a.pins)
	}
	return a, a.pins[0]
}

// TestPinKeepsCardAcrossRestart: the pinned card leaves the grid and goes
// back to it after a restart, with its own size.
func TestPinKeepsCardAcrossRestart(t *testing.T) {
	pinCanvasDrag(t)
	a, _ := dropPin(t, "playing")
	if _, ok := cardByID(a.toolsLayout().Cards, "playing"); ok {
		t.Fatal("the pinned card stayed in the grid")
	}
	raw, err := os.ReadFile(filepath.Join(a.dataDir, layout.FileName))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"id": "playing"`) {
		t.Fatalf("layout.json does not record the pin:\n%s", raw)
	}

	b := &shelf{dataDir: a.dataDir, settings: a.settings}
	b.pinRestore()
	if len(b.pins) != 1 {
		t.Fatalf("restore lost the pin: %+v", b.pins)
	}
	if !b.unpin(b.pins[0], true) {
		t.Fatal("放回刘海 failed after the restart")
	}
	got, ok := cardByID(b.toolsLayout().Cards, "playing")
	if !ok || got.Cols() != 2 || got.Height() != 2 {
		t.Fatalf("放回刘海 restored %+v, want playing 2×2", got)
	}
}

// TestPinWithoutSlotReturnsToTheGrid: a pin saved without a slot goes back
// to the end of the grid at the card's own size.
func TestPinWithoutSlotReturnsToTheGrid(t *testing.T) {
	pinCanvasDrag(t)
	a := &shelf{dataDir: t.TempDir()}
	a.mutateTools(func(l *layout.Layout) {
		l.Cards = []layout.Card{{ID: "playing", Span: 2, Rows: 2}}
		l.AddPin(layout.Pin{ID: "recent", Span: 2, Rows: 1, Display: "1"})
	})
	a.pinRestore()
	if len(a.pins) != 1 {
		t.Fatalf("restore lost the pin: %+v", a.pins)
	}
	if !a.unpin(a.pins[0], true) {
		t.Fatal("放回刘海 failed")
	}
	got, ok := cardByID(a.toolsLayout().Cards, "recent")
	if !ok || got.Cols() != 2 || got.Height() != 1 {
		t.Fatalf("recent returned as %+v, want 2×1", got)
	}
}

// pinLine is one word a pinned card draws: the font it uses, or a minimum
// box for a control whose label is not drawn.
type pinLine struct {
	text   string
	size   float32
	weight int
	min    float32
}

// pinLines lists what each widget's desktop card must show whole.
func pinLines(a *shelf, id string) []pinLine {
	switch id {
	case "playing":
		lines := []pinLine{{text: "座右铭", size: 12, weight: 600},
			{text: "上一首", min: 15}, {text: "播放", min: 15}, {text: "下一首", min: 15}}
		if st, ok := a.np.track(); ok {
			if meta := a.np.cardMeta(st); meta != "" {
				lines = append(lines, pinLine{text: meta, size: 10})
			}
		}
		return lines
	case "recent":
		return []pinLine{
			{text: "最近文件", size: 12, weight: 600},
			{text: "拖出即可", size: 10},
			{text: "alpha.png", min: 28}, {text: "bravo.pdf", min: 28}, {text: "charlie.txt", min: 28},
		}
	case "clip":
		return []pinLine{{text: "剪贴板里的一段很长的文字", size: 12, weight: 600}, {text: "点按复制", size: 10}}
	case "awake":
		return []pinLine{{text: "保持唤醒", size: 12, weight: 600}, {text: "关", size: 10}}
	case "usage":
		return []pinLine{{text: "AI 用量", size: 12, weight: 600}, {text: "暂无数据", size: 10}}
	}
	return nil
}

// measureCardText is the natural width of one line at its font.
func measureCardText(t *testing.T, text string, size float32, weight int) float32 {
	t.Helper()
	var w float32
	view := ui.NewTester(func(c *ui.Context) {
		w, _ = c.MeasureText(0, ui.Span{Text: text, Size: size, Weight: weight})
	}, 400, 100)
	view.Frame()
	return w
}

// pinClipped renders the card body a pin window shows at width w and
// returns what does not fit whole.
func pinClipped(t *testing.T, a *shelf, id string, span int, w float32) []string {
	t.Helper()
	view := ui.NewTester(func(c *ui.Context) {
		a.toolCard(c, layout.Card{ID: id, Span: span}, cardDesktop).Fill()
	}, int(w), pinCardH)
	var bad []string
	for _, line := range pinLines(a, id) {
		r, ok := view.Find(line.text)
		if !ok {
			bad = append(bad, fmt.Sprintf("%q is not drawn", line.text))
			continue
		}
		need := line.min
		if need == 0 {
			need = measureCardText(t, line.text, line.size, line.weight)
		}
		if r.W+0.5 < need {
			bad = append(bad, fmt.Sprintf("%q is %.1fpt of %.1fpt", line.text, r.W, need))
		}
	}
	return bad
}

// TestPinnedWidgetContentIsWhole: a widget pinned from the grid draws its
// whole body inside the pin window at every size it can have.
func TestPinnedWidgetContentIsWhole(t *testing.T) {
	pinCanvasDrag(t)
	for _, id := range []string{"playing", "awake", "usage", "recent", "clip"} {
		for _, size := range cardSizes(id) {
			name := fmt.Sprintf("%s %dx%d", id, size[0], size[1])
			t.Run(name, func(t *testing.T) {
				// The card is really set to this size in the grid: its
				// desktop window follows the widget's rule, not the grid
				// column count.
				a, it := dropPin(t, id, size[0], size[1])
				span := it.span
				if want := pinCardWidth(id, size[0]); it.cardW != want {
					t.Fatalf("%s at %dx%d pins %dpt wide, want %d", id, size[0], size[1], it.cardW, want)
				}
				if bad := pinClipped(t, a, id, span, float32(it.cardW)); len(bad) > 0 {
					t.Fatalf("the %dpt pinned card cuts its content: %s", it.cardW, strings.Join(bad, "; "))
				}
			})
		}
	}

	// Why three widgets pin wide: at one column their bodies are cut.
	a, _ := musicShelf(t)
	pinFixture(t, a)
	for _, id := range []string{"playing", "recent", "clip"} {
		if bad := pinClipped(t, a, id, 1, pinCardW); len(bad) == 0 {
			t.Errorf("%s is whole in a one-column pin, so a wide pin is not needed", id)
		}
		if bad := pinClipped(t, a, id, 2, pinCardW2); len(bad) > 0 {
			t.Errorf("%s is cut in a two-column pin: %s", id, strings.Join(bad, "; "))
		}
	}
	for _, id := range []string{"awake", "usage"} {
		if bad := pinClipped(t, a, id, 1, pinCardW); len(bad) > 0 {
			t.Errorf("%s does not fit a one-column pin: %s", id, strings.Join(bad, "; "))
		}
	}
}
