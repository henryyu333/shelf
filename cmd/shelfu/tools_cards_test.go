//go:build darwin

package main

import (
	"bytes"
	"image"
	"math"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"shelf/internal/aiusage"
	"shelf/internal/layout"
	"shelf/internal/settings"
	"shelf/internal/store"
	"shelf/locales"
)

// TestToolCatalogShapes checks the catalog invariants the picker and the
// grid rely on: unique ids, spans in range, every entry available-gated
// or not but never nil where it must answer.
func TestToolCatalogShapes(t *testing.T) {
	seen := map[string]bool{}
	for _, def := range toolCatalog() {
		if def.id == "" || seen[def.id] {
			t.Fatalf("bad or duplicated id %q", def.id)
		}
		seen[def.id] = true
		if def.span != 1 && def.span != 2 {
			t.Fatalf("%s: span %d", def.id, def.span)
		}
		if def.title == "" || def.icon == nil {
			t.Fatalf("%s: missing title or icon", def.id)
		}
		if def.accepts == nil || def.available == nil {
			t.Fatalf("%s: missing predicates", def.id)
		}
	}
}

// TestToolCardAccepts checks each card's file-class rule against the
// design table (§3): image actions take only images, 转 Word images or
// PDFs, 解压 archives, zip / 隔空投送 everything non-empty, widgets
// nothing.
func TestToolCardAccepts(t *testing.T) {
	want := map[string][4]bool{
		"ocr":     {true, false, false, false},
		"fit1m":   {true, false, false, false},
		"topdf":   {true, false, false, false},
		"tojpeg":  {true, false, false, false},
		"toword":  {true, true, false, false},
		"extract": {false, false, true, false},
		"resize":  {true, false, false, false},
		"zip":     {true, true, true, true},
		"airdrop": {true, true, true, true},
		"playing": {false, false, false, false},
		"awake":   {false, false, false, false},
		"recent":  {false, false, false, false},
		"clip":    {false, false, false, false},
		"usage":   {false, false, false, false},
	}
	classes := []dropClass{classImages, classPDFs, classArchives, classMixed}
	for id, expect := range want {
		def := toolCardDefFor(id)
		if def == nil {
			t.Fatalf("%s not in the catalog", id)
		}
		for i, class := range classes {
			if got := def.accepts(class); got != expect[i] {
				t.Fatalf("%s accepts %v = %v, want %v", id, class, got, expect[i])
			}
		}
	}
}

// TestToolCardDropAction checks the card→drop-zone-cell mapping: the
// conversion target a release runs (设计 §3: 规则和投放区动作格相同).
func TestToolCardDropAction(t *testing.T) {
	a := &shelf{}
	one := []string{"/tmp/a.png"}
	two := []string{"/tmp/a.png", "/tmp/b.png"}
	cases := []struct {
		id     string
		paths  []string
		target convertTarget
		air    bool
		ocr    bool
	}{
		{"ocr", one, 0, false, true},
		{"fit1m", one, toFit1M, false, false},
		{"tojpeg", one, toJPEG, false, false},
		{"resize", one, toMedium, false, false},
		{"topdf", one, toPDF, false, false},
		{"topdf", two, toMergedPDF, false, false}, // images merge past the first
		{"toword", one, toWord, false, false},
		{"extract", one, toExtract, false, false},
		{"zip", two, toZip, false, false},
		{"airdrop", one, 0, true, false},
	}
	for _, tc := range cases {
		act := a.toolCardDropAction(tc.id, tc.paths)
		if act.isAirDrop != tc.air {
			t.Fatalf("%s: airdrop flag wrong", tc.id)
		}
		if act.isOCRCopy != tc.ocr {
			t.Fatalf("%s: isOCRCopy = %v, want %v", tc.id, act.isOCRCopy, tc.ocr)
		}
		if !tc.air && !tc.ocr && act.target != tc.target {
			t.Fatalf("%s: target %v, want %v", tc.id, act.target, tc.target)
		}
	}
}

// TestToolCardTarget checks the tap conversion table.
func TestToolCardTarget(t *testing.T) {
	a := &shelf{}
	if a.toolCardTarget("topdf", 1) != toPDF || a.toolCardTarget("topdf", 3) != toMergedPDF {
		t.Fatal("topdf target wrong")
	}
	if a.toolCardTarget("resize", 1) != toMedium {
		t.Fatal("resize maps to toMedium (1600px)")
	}
	if a.toolCardTarget("airdrop", 1) != -1 || a.toolCardTarget("clip", 1) != -1 {
		t.Fatal("non-converting cards must answer -1")
	}
}

// TestToolCardTargets checks the tray-selection filter: a card applies
// only to selected items it could run on, exactly the classes a file
// drop on it takes.
func TestToolCardTargets(t *testing.T) {
	dir := t.TempDir()
	png := writeFile(t, dir, "a.png", testPNGBytes(t))
	pdf := writeFile(t, dir, "b.pdf", []byte("%PDF"))
	s, err := store.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	a := &shelf{store: s}
	// A selection of one image + one PDF: the PDF card keeps only the
	// image, toWord keeps both, zip keeps both.
	for i, it := range []store.Item{
		{ID: "i1", Kind: store.KindFile, Path: png},
		{ID: "i2", Kind: store.KindFile, Path: pdf},
	} {
		s.Items = append(s.Items, it)
		_ = i
	}
	a.selection.onlyAll([]string{"i1", "i2"})
	if got := a.toolCardTargets("topdf"); len(got) != 1 || got[0].ID != "i1" {
		t.Fatalf("topdf targets = %v", got)
	}
	if got := a.toolCardTargets("toword"); len(got) != 2 {
		t.Fatalf("toword targets = %v", got)
	}
	if got := a.toolCardTargets("extract"); len(got) != 0 {
		t.Fatalf("extract targets = %v", got)
	}
	if got := a.toolCardTargets("zip"); len(got) != 2 {
		t.Fatalf("zip targets = %v", got)
	}
	// Nothing selected → nothing to run: the tap jiggles instead.
	a.selection.clear()
	if got := a.toolCardTargets("zip"); len(got) != 0 {
		t.Fatalf("empty selection produced targets: %v", got)
	}
}

// TestLayoutDefaultCards checks the persisted default names real catalog
// ids — a typo here ships a dead cell.
func TestLayoutDefaultCards(t *testing.T) {
	for _, card := range layout.Default().Cards {
		def := toolCardDefFor(card.ID)
		if def == nil {
			t.Fatalf("default card %q not in the catalog", card.ID)
		}
		if card.Span != def.span {
			t.Fatalf("default %s span %d, catalog %d", card.ID, card.Span, def.span)
		}
	}
}

// TestOCRCardTapStartsScreenOCR wires the card's tap to task C's entry:
// runToolCard("ocr") must call startScreenOCR — the capture seam runs
// and the recognized text lands on the clipboard through the shared
// finish path.
func TestOCRCardTapStartsScreenOCR(t *testing.T) {
	ocrSeams(t)
	ocrPreflight = func() bool { return true }
	ocrCapture = func(path string) error { writePixelPNG(t, path); return nil }
	ocrRecognize = func(string) ([]string, error) { return []string{"识别出的文字"}, nil }
	var copied atomic.Value
	a := &shelf{visible: false, write: recordClipFunc(func(text string) error { copied.Store(text); return nil })}
	if !a.runToolCard("ocr") {
		t.Fatal("tapping 框选文字 must answer true — it ran, there is nothing to jiggle")
	}
	got, _ := copied.Load().(string)
	if got != "识别出的文字" {
		t.Fatalf("the card tap never reached capture+copy: %q", got)
	}
}

// TestOCRCardDropCopiesText wires the card's file drop to 识别文字并复
// 制: the images' text goes to the clipboard, nothing is shelved and the
// panel stays on the tools page (design §3's 拖文件到卡片上 column).
func TestOCRCardDropCopiesText(t *testing.T) {
	ocrSeams(t)
	ocrRecognize = func(p string) ([]string, error) {
		return []string{"from " + filepath.Base(p)}, nil
	}
	var copied atomic.Value
	a := &shelf{tab: pageTools, visible: true,
		write: recordClipFunc(func(text string) error { copied.Store(text); return nil })}
	a.drop.files = []string{"/tmp/one.png", "/tmp/two.png"}
	a.runDropAction(a.toolCardDropAction("ocr", a.drop.files), fileDrop(t, a.drop.files...))
	got, _ := copied.Load().(string)
	if got != "from one.png\nfrom two.png" {
		t.Fatalf("dropped images must be recognized in order: %q", got)
	}
	if a.tab != pageTools {
		t.Fatalf("an OCR copy must not jump to the tray page: %v", a.tab)
	}
}

// musicCardShelf is the tools page with a real track and the volume seam:
// the 2 × 2 music card's rows are laid out for real.
func musicCardShelf(t *testing.T) (*shelf, *ui.Tester, *fakeVolume, *stubNowPlaying) {
	t.Helper()
	s := newStubNowPlaying(t, true)
	s.feed(t, `{"ok":true,"isPlaying":true,"appState":1,"title":"夜曲","artist":"周杰伦","duration":200,"elapsed":20}`)
	a := &shelf{
		store: mustStore(t), dataDir: t.TempDir(), tab: pageTools, visible: true, level: 2,
		thumbnails: map[string]*ui.Bitmap{}, thumbPending: map[string]bool{}, appNames: map[string]string{},
	}
	vol := &fakeVolume{v: 0.4, ok: true}
	a.vol = vol
	s.mod.host = a
	a.np = s.mod
	view := ui.NewTester(a.view, sheetW, sheetH)
	view.Frame()
	view.Frame()
	return a, view, vol, s
}

// dragRail presses a rail at from, drags to to and releases there.
func dragRail(t *testing.T, view *ui.Tester, label string, from, to float32) {
	t.Helper()
	r, ok := view.Find(label)
	if !ok {
		t.Fatalf("no %q rail on screen; texts %v", label, view.Texts())
	}
	cy := r.Y + r.H/2
	view.Press(r.X+r.W*from, cy)
	view.Move(r.X+r.W*(from+to)/2, cy)
	view.Move(r.X+r.W*to, cy)
	view.Release(r.X+r.W*to, cy)
}

// seekTarget parses the "seek <seconds>" the stub helper received.
func seekTarget(t *testing.T, cmds <-chan string) float64 {
	t.Helper()
	select {
	case cmd := <-cmds:
		num, ok := strings.CutPrefix(cmd, "seek ")
		if !ok {
			t.Fatalf("the rail sent %q, want a seek", cmd)
		}
		secs, err := strconv.ParseFloat(num, 64)
		if err != nil {
			t.Fatalf("seek %q: %v", num, err)
		}
		return secs
	case <-time.After(2 * time.Second):
		t.Fatal("the rail sent nothing: its release did not seek")
	}
	return 0
}

// TestToolsMusicCardIsTheFullPlayer: the 2 × 2 music card is the player the
// design's 音乐 2 × 2 小组件 is — the two clocks and the seekable rail, the
// transport and the volume rail — and those rails take the pointer, so a
// drag on them neither reorders the card nor pins it.
func TestToolsMusicCardIsTheFullPlayer(t *testing.T) {
	a, view, vol, s := musicCardShelf(t)
	for _, label := range []string{"播放进度", "音量", "上一首", "暂停", "下一首"} {
		if _, ok := view.Find(label); !ok {
			t.Fatalf("the 2×2 card lacks %q; texts %v", label, view.Texts())
		}
	}
	card, ok := view.Find("playing") // the card's own accessibility label
	if !ok {
		t.Fatal("the music card is not on screen")
	}
	if want := toolColW(2); card.W != want {
		t.Fatalf("the music card is %.1fpt wide, want the 2 × 2 grid's %.1f", card.W, want)
	}
	if want := toolRowH(2); card.H != want {
		t.Fatalf("the music card is %.1fpt tall, want the 2 × 2 grid's %.1f", card.H, want)
	}
	// Every row the player draws stays inside the card.
	for _, label := range []string{"播放进度", "音量", "上一首", "下一首"} {
		r, _ := view.Find(label)
		if r.X < card.X-0.5 || r.X+r.W > card.X+card.W+0.5 || r.Y < card.Y-0.5 || r.Y+r.H > card.Y+card.H+0.5 {
			t.Fatalf("%q at %+v leaves the card %+v", label, r, card)
		}
	}

	// A seek drag: the release sends one seek, the drag never becomes a
	// card press (no reorder, no drag out).
	dragRail(t, view, "播放进度", 0.25, 0.5)
	if secs := seekTarget(t, s.cmds); secs < 98 || secs > 102 {
		t.Fatalf("the rail seeked to %.2fs, want about 100s (half of 200)", secs)
	}
	if p := a.toolsPage(); p.press != nil {
		t.Fatalf("a drag on the seek rail started a card press: %+v", p.press)
	}
	if len(a.pins) != 0 {
		t.Fatalf("a drag on the seek rail pinned the card: %d pins", len(a.pins))
	}
	// A volume drag writes the system volume once, on release.
	view.Frame()
	dragRail(t, view, "音量", 0.25, 0.75)
	if len(vol.sets) != 1 {
		t.Fatalf("the volume rail wrote %v, want exactly one write", vol.sets)
	}
	if got := vol.sets[0]; got < 0.7 || got > 0.8 {
		t.Fatalf("the volume rail wrote %.2f, want about 0.75", got)
	}
	if p := a.toolsPage(); p.press != nil {
		t.Fatalf("a drag on the volume rail started a card press: %+v", p.press)
	}
	// The transport still works from the card.
	view.Frame()
	if err := view.Click("暂停"); err != nil {
		t.Fatal(err)
	}
	select {
	case cmd := <-s.cmds:
		if cmd != "toggle" {
			t.Fatalf("the card's toggle sent %q, want toggle", cmd)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("the card's toggle sent nothing")
	}
}

// TestToolsMusicCardEditingKeepsThePlayer: the editing form of the 2 × 2
// music card is the same player, not a name-only tile: the rows stay and
// they still take their own presses.
func TestToolsMusicCardEditingKeepsThePlayer(t *testing.T) {
	a, view, vol, _ := musicCardShelf(t)
	p := a.toolsPage()
	p.editing, p.editAt = true, time.Now()
	view.Frame()
	view.Frame()
	for _, label := range []string{"播放进度", "音量", "上一首", "下一首"} {
		if _, ok := view.Find(label); !ok {
			t.Fatalf("the editing card lacks %q; texts %v", label, view.Texts())
		}
	}
	// The ⊖ badge and the size handle are still on the card.
	if _, ok := view.Find(locales.In("zh-Hans").Remove()); !ok {
		t.Fatalf("the editing card lost its ⊖ badge; texts %v", view.Texts())
	}
	view.Frame()
	dragRail(t, view, "音量", 0.2, 0.6)
	if len(vol.sets) != 1 || vol.sets[0] < 0.55 || vol.sets[0] > 0.65 {
		t.Fatalf("the editing card's volume rail wrote %v, want one write about 0.6", vol.sets)
	}
	if p.press != nil {
		t.Fatalf("a drag on the editing card's rail started a reorder: %+v", p.press)
	}
}

// TestPinnedMusicCardShowsTheFullPlayer: the 2 × 2 music card pinned to the
// desktop keeps the full player in the pin's wide, short window, and its
// rails work there too.
func TestPinnedMusicCardShowsTheFullPlayer(t *testing.T) {
	s := newStubNowPlaying(t, true)
	s.feed(t, `{"ok":true,"isPlaying":true,"appState":1,"title":"夜曲","artist":"周杰伦","duration":200,"elapsed":20}`)
	vol := &fakeVolume{v: 0.3, ok: true}
	a := &shelf{dataDir: t.TempDir()}
	a.vol = vol
	s.mod.host = a
	a.np = s.mod
	it := a.pinRegister(&pinItem{
		cardID: "playing", span: 2, rows: 2, cardW: pinCardW2, display: 1,
		edge: pinFree, expanded: true,
		frame: mygo.Rectangle{Width: pinCardW2, Height: pinCardH},
	})
	view := ui.NewTester(func(c *ui.Context) { it.view(c) }, pinCardW2, pinCardH)
	view.Frame()
	view.Frame()
	for _, label := range []string{"播放进度", "音量", "上一首", "暂停", "下一首"} {
		if _, ok := view.Find(label); !ok {
			t.Fatalf("the pinned card lacks %q; texts %v", label, view.Texts())
		}
	}
	dragRail(t, view, "音量", 0.1, 0.5)
	if len(vol.sets) != 1 || vol.sets[0] < 0.45 || vol.sets[0] > 0.55 {
		t.Fatalf("the pinned card's volume rail wrote %v, want one write about 0.5", vol.sets)
	}
	if it.pressed || it.moving {
		t.Fatal("a drag on the pinned card's rail moved the card's window")
	}
	// The volume drag reached the device, not the player.
	select {
	case cmd := <-s.cmds:
		t.Fatalf("a volume drag sent the player %q", cmd)
	case <-time.After(50 * time.Millisecond):
	}
	// And the seek rail still seeks on its release.
	view.Frame()
	dragRail(t, view, "播放进度", 0.5, 1)
	// The pin's rail is 38pt wide: a point is five seconds of the track.
	if secs := seekTarget(t, s.cmds); secs < 190 || secs > 210 {
		t.Fatalf("the pinned card's seek rail went to %.2fs, want about 200s", secs)
	}
}

// TestToolCardDimOnlyActionCards: a file drag dims the action cards whose
// class it does not fit, and nothing else. The widget cards keep their
// brightness and their subtitles whatever the drag holds.
func TestToolCardDimOnlyActionCards(t *testing.T) {
	classes := []dropClass{classImages, classPDFs, classArchives, classMixed}
	catalog := toolCatalog()
	for i := range catalog {
		def := &catalog[i]
		if def.kind != cardWidget {
			continue
		}
		for _, class := range classes {
			if toolCardDimmed(def, class) {
				t.Fatalf("widget %s dims on %v", def.id, class)
			}
		}
	}
	pdf := toolCardDefFor("topdf")
	if !toolCardDimmed(pdf, classPDFs) {
		t.Fatal("转成 PDF stays bright for a PDF drag it cannot take")
	}
	if toolCardDimmed(pdf, classImages) {
		t.Fatal("转成 PDF dims for the images it takes")
	}
	if toolCardDimmed(toolCardDefFor("zip"), classPDFs) {
		t.Fatal("打成 zip dims for a PDF it takes")
	}
}

// TestToolCardDragOnlyWhileDragging: the tools page's drag picture reads
// the live drag flag, not the snapshot alone. The snapshot outlives the
// mouse-up by dropHoldGrace so the drop still finds its cells; that held
// set must not paint the page.
func TestToolCardDragOnlyWhileDragging(t *testing.T) {
	a := &shelf{settings: settings.Default()}
	a.drop.files = []string{"/tmp/a.pdf"}
	a.drop.class = classPDFs
	a.drop.loaded = true
	a.dragging = true
	if class, n, live := a.toolCardDrag(); !live || n != 1 || class != classPDFs {
		t.Fatalf("live drag = %v, %d, %v; want the PDF set", class, n, live)
	}
	a.dragging = false
	if _, _, live := a.toolCardDrag(); live {
		t.Fatal("a finished drag still paints the tools page")
	}
	a.dragging, a.ownDrag = true, true
	if _, _, live := a.toolCardDrag(); live {
		t.Fatal("ShelfU's own drag-out paints the tools page as an incoming drag")
	}
}

// TestDropSnapshotEndsWithItsDrop: once a drop is consumed the tools page
// goes back to its normal brightness on the next frame — the v1.11 bug
// left the released set (and its “N 个 PDF” subtitles) until the next drag
// or a restart.
func TestDropSnapshotEndsWithItsDrop(t *testing.T) {
	a := &shelf{settings: settings.Default()}
	a.dragSerial = 7
	a.drop = dropZoneState{
		serial: 7, count: 3, loaded: true,
		files: []string{"/tmp/a.pdf", "/tmp/b.pdf"}, class: classPDFs,
		done: true, // the card or the tray cell answered the release
	}
	a.dragging = false
	if got := a.dropZoneSnap(false); got != nil {
		t.Fatalf("a consumed drop still draws cells: %v", got)
	}
	if len(a.drop.files) != 0 || a.drop.class != 0 || a.drop.loaded || a.drop.on {
		t.Fatalf("the released set outlived its drop: %+v", a.drop)
	}
	if _, n, live := a.toolCardDrag(); live || n != 0 {
		t.Fatalf("the tools page still reads a drag: %d files, live %v", n, live)
	}
}

// TestDropSnapshotClearsWhenPanelCloses: a set left when the panel closes
// does not survive into the next open.
func TestDropSnapshotClearsWhenPanelCloses(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	a := &shelf{store: st, dataDir: t.TempDir(), settings: settings.Default()}
	a.drop.files = []string{"/tmp/a.pdf"}
	a.drop.class = classPDFs
	a.drop.loaded = true
	_ = ui.NewTester(a.view, sheetW, sheetH)
	if len(a.drop.files) != 0 {
		t.Fatalf("a closed panel kept %v", a.drop.files)
	}
}

// TestPageIconsDiffer: the overview and the tools page sit next to each
// other in the top bar, so their glyphs must read differently — the same
// four-square grid for both (v1.12.0) was the bug the user reported.
func TestPageIconsDiffer(t *testing.T) {
	render := func(icon *ui.SVG) []byte {
		return ui.NewTester(func(c *ui.Context) {
			ui.Column(c.Key("icon-col")).FillWidth().FillHeight().Center().Children(func() {
				ui.Icon(c, icon).Size(20, 20).TextColor(ink)
			})
		}, 20, 20).Image().Pix
	}
	overview, tools := render(overviewNavSymbol), render(toolsNavSymbol)
	inked := func(pix []byte) bool {
		for i := 3; i < len(pix); i += 4 {
			if pix[i] != 0 {
				return true
			}
		}
		return false
	}
	if !inked(overview) || !inked(tools) {
		t.Fatal("an icon drew nothing: the comparison would be empty")
	}
	if bytes.Equal(overview, tools) {
		t.Fatal("the overview and tools icons draw the same glyph")
	}
}

// TestToolCardDragPaintSurfaces: only the panel's tools page answers a file
// drag. A pin floating on the desktop redraws whenever it is invalidated,
// and the drag must leave it exactly as it is.
func TestToolCardDragPaintSurfaces(t *testing.T) {
	if !toolCardDragPaint(cardPanel, true) || !toolCardDragPaint(cardEdit, true) {
		t.Fatal("the panel's cards stopped answering a live drag")
	}
	if toolCardDragPaint(cardDesktop, true) {
		t.Fatal("a pinned card answers the panel's drag")
	}
	for _, surf := range []toolCardSurface{cardPanel, cardEdit, cardDesktop} {
		if toolCardDragPaint(surf, false) {
			t.Fatalf("surface %v paints a drag that is over", surf)
		}
	}
}

// TestPinnedCardIgnoresPanelDrag draws a pinned card while files hover the
// panel: the card keeps its picture — no 38% fade on the actions it cannot
// take, no 「N 个 PDF」 subtitle on the zip it could — because the drag has
// nothing to do with the desktop.
func TestPinnedCardIgnoresPanelDrag(t *testing.T) {
	for _, id := range []string{"zip", "topdf"} {
		a := &shelf{dataDir: t.TempDir(), settings: settings.Default()}
		w := pinCardWidth(id, 1)
		it := &pinItem{owner: a, cardID: id, span: 1, rows: 1, cardW: w, edge: pinFree, expanded: true,
			frame: mygo.Rectangle{Width: w, Height: pinCardH}}
		clean := ui.NewTester(it.view, w, pinCardH).Image()

		a.dragging = true
		a.drop = dropZoneState{files: []string{"/tmp/a.pdf"}, class: classPDFs, loaded: true}
		tr := ui.NewTester(it.view, w, pinCardH)
		tr.Frame()
		if tr.HasText("1 个 PDF") || tr.HasText("1 项") {
			t.Fatalf("the pinned %s card shows the panel drag's count", id)
		}
		if drag := tr.Image(); !bytes.Equal(clean.Pix, drag.Pix) {
			t.Fatalf("the pinned %s card changed while files hovered the panel", id)
		}
	}
}

// usageCardFixture draws the 工具 page with the AI 用量 card fed fake
// quota answers: the fixture is switched to the grid, so its own 1 × 1
// card renders instead of the usage page.
func usageCardFixture(t *testing.T, quotaLeft bool, codex, claude aiusage.Quota) *usageFixture {
	t.Helper()
	fx := newUsageFixture(t, true)
	fx.a.settings.AIUsage.QuotaLeft = quotaLeft
	fx.a.tab = pageTools
	fx.f.quota[quotaIndex(aiusage.ToolCodex)] = usageQuotaState{latest: codex, good: codex}
	fx.f.quota[quotaIndex(aiusage.ToolClaude)] = usageQuotaState{latest: claude, good: claude}
	fx.view.Frame()
	return fx
}

// usageCardBodyImage renders only the 1 × 1 card body over a dark card,
// the way the grid draws it, and returns the frame.
func usageCardBodyImage(t *testing.T, a *shelf) *image.RGBA {
	t.Helper()
	w, h := int(toolColW(1)), toolCellH
	view := ui.NewTester(func(c *ui.Context) {
		ui.Column(c).Size(float32(w), float32(h)).Background(ui.RGB(10, 10, 12)).Children(func() {
			card := ui.Column(c).Size(float32(w), float32(h))
			a.toolUsageBody(c, card, 1)
		})
	}, w, h)
	view.Frame()
	return view.Image()
}

// usageCardMarks counts the pixels of the ring's centre hole that look
// like a mark: rust for Claude Code's Spark, bright white for Codex's
// Blossom. The gauge's ink3 and the ring tracks are dimmer than both.
func usageCardMarks(img *image.RGBA) (rust, bright, dim int) {
	b := img.Bounds()
	cx, cy := float64(b.Dx())/2, float64(b.Dy())/2
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			if math.Hypot(float64(x-b.Min.X)+0.5-cx, float64(y-b.Min.Y)+0.5-cy) > 9 {
				continue
			}
			r, g, bl, a := img.At(x, y).RGBA()
			r, g, bl = r>>8, g>>8, bl>>8
			if a>>8 < 250 {
				continue
			}
			switch {
			case r > 180 && g >= 90 && g <= 140 && bl >= 55 && bl <= 115:
				rust++
			case r > 210 && g > 210 && bl > 210:
				bright++
			case r > 60 && r < 200 && g > 60 && g < 200 && bl > 60 && bl < 200:
				dim++
			}
		}
	}
	return rust, bright, dim
}

// TestToolUsageCard1x1DrawsNoText: the AI 用量 card at 1 × 1 is the ring
// alone — its numbers live in the accessible name and the hover tip.
func TestToolUsageCard1x1DrawsNoText(t *testing.T) {
	now := time.Date(2026, 10, 19, 12, 0, 0, 0, time.Local)
	fx := usageCardFixture(t, false,
		okQuota(aiusage.ToolCodex, "Plus", now), okQuota(aiusage.ToolClaude, "Pro", now))
	view := ui.NewTester(func(c *ui.Context) {
		card := ui.Column(c).Size(toolColW(1), float32(toolCellH))
		fx.a.toolUsageBody(c, card, 1)
	}, int(toolColW(1)), toolCellH)
	if texts := view.Texts(); len(texts) > 0 {
		t.Fatalf("the 1 × 1 AI 用量 card must draw no text; texts=%v", texts)
	}
}

// TestToolUsageCardDrawsSelectedToolMark: the ring's centre is the
// least-remaining tool's own mark, and the gauge only when there is no
// quota data at all.
func TestToolUsageCardDrawsSelectedToolMark(t *testing.T) {
	now := time.Date(2026, 10, 19, 12, 0, 0, 0, time.Local)
	// Claude Code's 5-hour window is the lowest: its Spark sits in the ring.
	fx := usageCardFixture(t, false,
		windowUsed(okQuota(aiusage.ToolCodex, "Plus", now), 0, 20),
		windowUsed(okQuota(aiusage.ToolClaude, "Pro", now), 0, 60))
	if rust, bright, _ := usageCardMarks(usageCardBodyImage(t, fx.a)); rust == 0 || bright != 0 {
		t.Fatalf("Claude Code is the least-remaining tool: rust=%d bright=%d, want its Spark only", rust, bright)
	}
	// Codex's: the white Blossom, and no rust at all.
	fx = usageCardFixture(t, false,
		windowUsed(okQuota(aiusage.ToolCodex, "Plus", now), 0, 60),
		windowUsed(okQuota(aiusage.ToolClaude, "Pro", now), 0, 20))
	if rust, bright, _ := usageCardMarks(usageCardBodyImage(t, fx.a)); bright == 0 || rust != 0 {
		t.Fatalf("Codex is the least-remaining tool: rust=%d bright=%d, want its Blossom only", rust, bright)
	}
	// No answer at all: the bare tracks and the gauge.
	fx = usageCardFixture(t, false,
		aiusage.Quota{Tool: aiusage.ToolCodex, Status: aiusage.QuotaNotLoggedIn, FetchedAt: now},
		aiusage.Quota{Tool: aiusage.ToolClaude, Status: aiusage.QuotaNotLoggedIn, FetchedAt: now})
	if rust, bright, dim := usageCardMarks(usageCardBodyImage(t, fx.a)); rust != 0 || bright != 0 || dim == 0 {
		t.Fatalf("no quota data: rust=%d bright=%d dim=%d, want the gauge only", rust, bright, dim)
	}
}

// TestToolUsageCardHintFollowsQuotaLeft: the accessible name and the hover
// tip both state the selected tool and its two shares, in the mode the
// QuotaLeft setting picks.
func TestToolUsageCardHintFollowsQuotaLeft(t *testing.T) {
	now := time.Date(2026, 10, 19, 12, 0, 0, 0, time.Local)
	codex := windowUsed(okQuota(aiusage.ToolCodex, "Plus", now), 0, 20)
	claude := windowUsed(okQuota(aiusage.ToolClaude, "Pro", now), 0, 60)
	for _, tc := range []struct {
		left bool
		want string
	}{
		{false, "Claude Code · 5 小时 60% 已用 · 每周 30% 已用"},
		{true, "Claude Code · 5 小时 40% 剩余 · 每周 70% 剩余"},
	} {
		fx := usageCardFixture(t, tc.left, codex, claude)
		if !fx.view.HasText(tc.want) {
			t.Fatalf("left=%v: the card's accessible name is not %q; texts=%v", tc.left, tc.want, fx.view.Texts())
		}
		r, ok := fx.view.Find(tc.want)
		if !ok {
			t.Fatalf("left=%v: the card has no box to hover", tc.left)
		}
		fx.view.Move(r.X+r.W/2, r.Y+r.H/2)
		fx.view.Advance(700 * time.Millisecond)
		tips := 0
		for _, s := range fx.view.Texts() {
			if s == tc.want {
				tips++
			}
		}
		if tips != 2 {
			t.Fatalf("left=%v: %q appears %d times, want twice — the card's name and its hover tip; texts=%v",
				tc.left, tc.want, tips, fx.view.Texts())
		}
	}
}
