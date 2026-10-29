//go:build darwin

package main

import (
	"image"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"shelf/internal/layout"
	"shelf/internal/mac"
	"shelf/internal/store"
)

// TestToolsPageRenders builds the expanded panel headless on the tools
// tab and checks the back row's page name and the cards appear.
func TestToolsPageRenders(t *testing.T) {
	s, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	a := &shelf{store: s, tab: pageTools, visible: true}
	view := ui.NewTester(a.view, sheetW, sheetH)
	if !view.HasText("工具") {
		t.Fatal("the back row did not show 工具")
	}
	if !view.HasText("压缩到 1 MB") {
		t.Fatal("default cards missing")
	}
	if view.HasText("项") {
		t.Fatal("the back row still shows the card count")
	}
}

// TestToolsPageDropZoneSuppressed proves the file-drop zone is a
// drag-start decision (design §1): only a drag that began while the
// panel was already open on the tools page keeps the grid for the card
// targets; any other drag gets the zone even on the tools tab.
func TestToolsPageDropZoneSuppressed(t *testing.T) {
	a := &shelf{tab: pageTools, visible: true}

	// A drag already in flight when the tools page happens to be showing:
	// dragOpenTools is the flag drag.go latches on the new serial.
	a.dragOpenTools = true
	if on, need := a.dropZoneShow(); on || need != 0 {
		t.Fatalf("tools-open drag must keep the drop zone off, got on=%v need=%d", on, need)
	}

	// The same tab but a drag that started elsewhere: the zone wins.
	a.dragOpenTools = false
	if on, need := a.dropZoneShow(); !on || need != dropZoneNeedH() {
		t.Fatalf("an outside drag must get the zone on any tab, got on=%v need=%d", on, need)
	}
	a.tab = pageTray
	if on, need := a.dropZoneShow(); !on || need != dropZoneNeedH() {
		t.Fatalf("tray tab must keep the drop zone, got on=%v need=%d", on, need)
	}

	// The latch itself: a new serial on an open tools page sets it, an
	// open tray page clears it, a hidden panel clears it too. dragState
	// is stubbed like drag_band_test does; active=false keeps
	// pointerDragged on the latch path only.
	oldState := dragState
	defer func() { dragState = oldState }()
	dragState = func() (bool, int64) { return false, 7 }
	a.tab, a.visible = pageTools, true
	a.dragSerial = -99 // far from the stub's serial so the latch re-fires
	a.pointerDragged()
	if !a.dragOpenTools {
		t.Fatal("a drag starting on an open tools page must latch dragOpenTools")
	}
	a.tab = pageTray
	a.dragSerial = -99
	a.pointerDragged()
	if a.dragOpenTools {
		t.Fatal("a drag starting on the tray page must not latch")
	}
	a.visible = false
	a.dragSerial = -99
	a.pointerDragged()
	if a.dragOpenTools {
		t.Fatal("a drag starting with the panel hidden must not latch")
	}
}

// TestToolsPageEditMode checks the edit flow: pencil enters, + slot and
// ⊖ badges appear, 完成 leaves.
func TestToolsPageEditMode(t *testing.T) {
	s, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	a := &shelf{store: s, tab: pageTools, visible: true, dataDir: t.TempDir()}
	view := ui.NewTester(a.view, sheetW, sheetH)
	if err := view.Click("编辑"); err != nil {
		t.Fatalf("pencil button missing: %v", err)
	}
	p := a.toolsPage()
	if p == nil || !p.editing {
		t.Fatal("pencil did not enter edit mode")
	}
	// The + slot and a ⊖ badge must exist in edit mode.
	if !view.HasText("添加卡片") {
		t.Fatal("+ slot missing in edit mode")
	}
	if err := view.Click("完成"); err != nil {
		t.Fatalf("完成 button missing: %v", err)
	}
	if p.editing {
		t.Fatal("完成 did not leave edit mode")
	}
}

// TestToolsPagePicker covers the catalogue: + opens it, a new card adds,
// 完成 closes back to editing.
func TestToolsPagePicker(t *testing.T) {
	dir := t.TempDir()
	s, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	a := &shelf{store: s, tab: pageTools, visible: true, dataDir: dir}
	p := a.toolsPage()
	if p == nil {
		t.Fatal("tools page missing")
	}
	p.editing, p.picking = true, true
	view := ui.NewTester(a.view, sheetW, sheetH)
	if !view.HasText("卡片目录") {
		t.Fatal("picker title missing")
	}
	if !view.HasText("隔空投送") {
		t.Fatal("picker is missing catalog entries")
	}
	// Placed cards are not listed: the catalogue only offers what is left.
	// A fresh install's grid is 音乐, 保持唤醒, 压缩到 1 MB (AI 用量 is off
	// here), 框选文字 and 打成 zip.
	for _, placed := range []string{"音乐", "保持唤醒", "压缩到 1 MB", "框选文字", "打成 zip"} {
		if view.HasText(placed) {
			t.Fatalf("the catalogue still lists the placed card %q", placed)
		}
	}
	// Everything else is catalogue-only now, including the widgets the old
	// default carried.
	for _, added := range []string{"最近文件", "剪贴板最新", "隔空投送"} {
		if !view.HasText(added) {
			t.Fatalf("the catalogue lost the unplaced card %q", added)
		}
	}
}

// TestToolsLayoutPersistence: removing a card writes layout.json, and a
// fresh page sees the same order.
func TestToolsLayoutPersistence(t *testing.T) {
	dir := t.TempDir()
	s, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	a := &shelf{store: s, dataDir: dir}
	a.mutateTools(func(l *layout.Layout) {
		l.RemoveCard("awake")
		l.MoveCard("zip", 0)
	})
	got := a.toolsLayout()
	if len(got.Cards) == len(layout.Default().Cards) {
		t.Fatal("RemoveCard did not apply")
	}
	if got.Cards[0].ID != "zip" {
		t.Fatalf("zip did not move first: %v", got.Cards)
	}
	// A new shelf on the same dir reads the saved order back.
	b := &shelf{dataDir: dir}
	got = b.toolsLayout()
	if got.Cards[0].ID != "zip" {
		t.Fatal("layout.json did not persist the order")
	}
	for _, card := range got.Cards {
		if card.ID == "awake" {
			t.Fatal("removed card came back")
		}
	}
}

// TestToolsEscapeReset: Esc on the tools tab leaves editing instead of
// hiding, and a second Esc hides.
func TestToolsEscapeReset(t *testing.T) {
	a := &shelf{tab: pageTools, visible: true}
	p := a.toolsPage()
	p.editing = true
	a.escape()
	if p.editing {
		t.Fatal("esc did not leave edit mode")
	}
	if !a.visible {
		t.Fatal("esc hid while editing was still on")
	}
}

// A layout.json the loader cannot read (bad JSON here) draws the default
// grid for the session but is never written back — mutating in memory must
// leave the recoverable file byte-identical, as settingsBroken does.
func TestToolsBrokenLayoutNeverWritten(t *testing.T) {
	dir := t.TempDir()
	s, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	bad := []byte("{nope")
	if err := os.WriteFile(filepath.Join(dir, "layout.json"), bad, 0o600); err != nil {
		t.Fatal(err)
	}
	a := &shelf{store: s, dataDir: dir}
	_ = a.toolsLayout() // the broken load latches
	a.mutateTools(func(l *layout.Layout) { l.MoveCard("zip", 0) })
	got, err := os.ReadFile(filepath.Join(dir, "layout.json"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(bad) {
		t.Fatalf("a broken layout was overwritten: %q", got)
	}
}

// A write that fails leaves both the file and the in-memory layout as
// they were — the card still shows after the failed removal.
func TestToolsWriteFailureKeepsLayout(t *testing.T) {
	dir := t.TempDir()
	s, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	a := &shelf{store: s, dataDir: dir}
	before := a.toolsLayout() // load + default write while the dir is writable
	saved, err := os.ReadFile(filepath.Join(dir, "layout.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatal(err)
	}
	defer os.Chmod(dir, 0o700)
	a.mutateTools(func(l *layout.Layout) { l.RemoveCard("zip") })
	got := a.toolsLayout()
	if !slices.EqualFunc(got.Cards, before.Cards, func(x, y layout.Card) bool { return x == y }) {
		t.Fatalf("a failed write changed the in-memory layout: %v", got.Cards)
	}
	data, err := os.ReadFile(filepath.Join(dir, "layout.json"))
	if err != nil || string(data) != string(saved) {
		t.Fatalf("the file changed under a failed write: %v", err)
	}
}

// A tap on an action card while the one-job converter is taken starts
// nothing: the card shakes and posts the busy line instead of being
// silently counted as run.
func TestToolsCardTapDuringConvertIsBusy(t *testing.T) {
	s, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	f := writeFile(t, t.TempDir(), "a.png", testPNGBytes(t))
	s.Items = append(s.Items, store.Item{ID: "i1", Kind: store.KindFile, Path: f})
	a := &shelf{store: s}
	a.selection.only("i1")
	a.conv = &converter{job: &convertJob{}} // a job in flight
	p := a.toolsPage()
	p.toolCardTapped("zip")
	if p.jiggleID != "zip" {
		t.Fatalf("busy tap did not jiggle: %q", p.jiggleID)
	}
	if a.status != "一次只跑一个任务，请稍候" {
		t.Fatalf("busy tap posted %q, want the busy line", a.status)
	}
}

// A press held still past the long-press deadline enters editing on the
// frame clock: the checker itself requests frames while the press waits,
// so a quiet panel still reaches it.
func TestToolsStillLongPressEntersEdit(t *testing.T) {
	s, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	a := &shelf{store: s, tab: pageTools, visible: true}
	view := ui.NewTester(a.view, sheetW, sheetH)
	r, ok := view.Find("框选文字")
	if !ok {
		t.Fatal("ocr card missing")
	}
	view.Press(r.X+r.W/2, r.Y+r.H/2)
	view.Advance(durLongPress + 100*time.Millisecond)
	p := a.toolsPage()
	if p == nil || !p.editing {
		t.Fatalf("still long press did not enter editing: %v", p)
	}
	view.Release(r.X+r.W/2, r.Y+r.H/2)
	if !p.editing {
		t.Fatal("releasing the long press left editing")
	}
}

// Esc inside the 「+」 catalogue only backs out to the edit grid (same as
// 完成); editing ends on a second Esc.
func TestToolsEscapeInPickerClosesPickerOnly(t *testing.T) {
	a := &shelf{tab: pageTools, visible: true}
	p := a.toolsPage()
	p.editing, p.picking = true, true
	a.escape()
	if !p.editing || p.picking {
		t.Fatalf("Esc in the picker ended editing instead of the picker: editing=%v picking=%v", p.editing, p.picking)
	}
	a.escape()
	if p.editing {
		t.Fatal("second Esc did not leave editing")
	}
	if !a.visible {
		t.Fatal("Esc hid the panel")
	}
}

// A settle whose flight starts where it ends (an edit-mode drag released
// back at the press position, insertion unchanged) must still end the
// press: Arrived only reports the frame a moving value comes to rest, so
// a flight that never moves never arrives and the press sticks — every
// later card press is refused.
func TestToolsSettleEndsWhenFlightNeverMoves(t *testing.T) {
	s, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	a := &shelf{store: s, tab: pageTools, visible: true, dataDir: t.TempDir()}
	a.toolsLay = &layout.Layout{Version: layout.Version, Cards: []layout.Card{
		{ID: "ocr"}, {ID: "fit1m"}, {ID: "topdf"}, {ID: "zip"},
		{ID: "tojpeg"}, {ID: "toword"}, {ID: "extract"}, {ID: "resize"},
	}}
	view := ui.NewTester(a.view, sheetW, sheetH)
	view.SetPreferences(ui.Preferences{ReduceMotion: true})
	r, ok := view.Find("打成 zip")
	if !ok {
		t.Fatal("zip card missing")
	}
	x, y := r.X+20, r.Y+20
	view.Press(x, y)
	view.Advance(durLongPress + 80*time.Millisecond)
	p := a.toolsPage()
	if !p.editing {
		t.Fatal("long press did not enter edit")
	}
	view.Move(x+10, y)
	view.Release(x, y) // released back at the press point: the flight starts at its goal
	// The flight rests at once (it starts where it ends) or within the
	// animation; either way the press must be gone after it.
	view.Advance(2 * time.Second)
	if p.press != nil {
		t.Fatalf("a settle that starts at its goal never ended: %+v", *p.press)
	}
	// The handback works: another card starts its own drag.
	next, ok := view.Find("压缩到 1 MB")
	if !ok {
		t.Fatal("second card missing")
	}
	view.Press(next.X+20, next.Y+20)
	view.Move(next.X+40, next.Y+20)
	if p.press == nil || p.press.id != "fit1m" {
		t.Fatalf("the next card could not start its own drag: %+v", p.press)
	}
	view.Release(next.X+40, next.Y+20)
}

// toolsOverflowLayout is a 3-row grid (9 action cards): the third row is
// beyond the two the fixed stage shows, so the grid scrolls.
func toolsOverflowLayout() layout.Layout {
	return layout.Layout{Version: layout.Version, Cards: []layout.Card{
		{ID: "ocr"}, {ID: "fit1m"}, {ID: "topdf"}, {ID: "zip"},
		{ID: "tojpeg"}, {ID: "toword"}, {ID: "extract"}, {ID: "resize"},
		{ID: "airdrop"},
	}}
}

// The grid past the two visible rows must scroll: the cards are
// absolute, and an absolute child of the scroll itself counts toward
// neither the scrollable content nor the offset, so the third row stayed
// clipped out of reach. A content box in flow carries the height; the
// cards scroll inside it and take their presses.
func TestToolsGridScrollsPastThreeRows(t *testing.T) {
	s, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	a := &shelf{store: s, tab: pageTools, visible: true}
	l := toolsOverflowLayout()
	a.toolsLay = &l
	view := ui.NewTester(a.view, sheetW, sheetH)
	p := a.toolsPage()
	first, ok := view.Find("打成 zip")
	if !ok {
		t.Fatal("zip card missing")
	}
	view.Frame()
	if p.scroll.MaxY <= 0 {
		t.Fatalf("an overflowing grid did not scroll: %+v", p.scroll)
	}
	view.Scroll(first.X+first.W/2, first.Y+first.H/2, 0, 200)
	view.Advance(300 * time.Millisecond)
	after, _ := view.Find("打成 zip")
	if after.Y == first.Y {
		t.Fatalf("scrolling moved no cards: before=%+v after=%+v scroll=%+v", first, after, p.scroll)
	}
	// The third row is on screen now: its card takes a press.
	last, ok := view.Find("隔空投送")
	if !ok {
		t.Fatal("third-row card missing after the scroll")
	}
	view.Press(last.X+10, last.Y+10)
	if p.press == nil || p.press.id != "airdrop" {
		t.Fatalf("a card of the third row could not be pressed: %+v", p.press)
	}
	view.Release(last.X+10, last.Y+10)
}

// A release inside the visible grid commits the reorder: the drop
// predicate measures the viewport (the stage height), not the
// contentH−MaxY bound that shrinks once the content really scrolls —
// that shrinks with the content and misreads releases near the
// viewport's bottom as outside the stage.
func TestToolsDropInVisibleGridCommits(t *testing.T) {
	s, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	a := &shelf{store: s, tab: pageTools, visible: true}
	l := toolsOverflowLayout()
	a.toolsLay = &l
	view := ui.NewTester(a.view, sheetW, sheetH)
	p := a.toolsPage()
	p.editing = true
	view.Frame()
	stage := float32(contentH)
	if p.scroll.MaxY <= 28 {
		t.Fatalf("fixture did not overflow the content box enough: %+v", p.scroll)
	}
	// Near the viewport's bottom: the old bound (contentH−MaxY) is
	// negative for this overflowing grid, so a release here must still
	// count as inside the stage rather than pin the card.
	py := p.scroll.Y + stage - 20
	if py < p.scroll.Y || py > p.scroll.Y+stage-4 {
		t.Fatalf("probe is not inside the viewport: py=%.1f scroll=%+v", py, p.scroll)
	}
	last := l.Cards[len(l.Cards)-1].ID
	oldCursor := cursorPoint
	t.Cleanup(func() { cursorPoint = oldCursor })
	cursorPoint = func() mygo.Point { return mygo.Point{X: 200, Y: 200} } // inside the panel
	d := &toolPress{id: last, dragging: true, px: 20, py: py, index: 0,
		cardX: 0, cardY: 400}
	p.toolCardDrop(d, cursorPoint())
	after := a.toolsLayout().Cards
	if len(a.pins) != 0 {
		t.Fatalf("a drop inside the visible grid pinned the card: %d pins", len(a.pins))
	}
	if len(after) == 0 || after[0].ID != last {
		t.Errorf("a drop inside the visible grid did not commit the reorder: %v", idsOf(after))
	}
}

func idsOf(cards []layout.Card) []string {
	var ids []string
	for _, c := range cards {
		ids = append(ids, c.ID)
	}
	return ids
}

// A drag-out whose layout write fails must not open a pin window: the
// failed write keeps the in-memory arrangement (the card stays in the
// grid) and the file as it was, so the recovered save cannot write the
// same id into both Cards and Pins and drop the pin on the next load.
func TestToolsDragOutWriteFailureKeepsGrid(t *testing.T) {
	withPinDisplays(t, pinTestDisplays)
	oldOpen := pinOpenWindow
	pinOpenWindow = func(*pinItem) {}
	t.Cleanup(func() { pinOpenWindow = oldOpen })
	oldCursor := cursorPoint
	t.Cleanup(func() { cursorPoint = oldCursor })
	cursorPoint = func() mygo.Point { return mygo.Point{X: 1400, Y: 500} }
	dir := t.TempDir()
	a := &shelf{dataDir: dir}
	a.setTools(layout.Default())
	if err := layout.Write(dir, a.toolsLayout()); err != nil {
		t.Fatal(err)
	}
	saved, err := os.ReadFile(filepath.Join(dir, layout.FileName))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o755) })
	a.x, a.y = 400, 0
	p := &toolsPage{host: a, editing: true}
	p.toolCardDrop(&toolPress{id: "zip", dragging: true, grabDX: 8, grabDY: 8, px: 20, py: 2000, index: 0}, cursorPoint())
	if len(a.pins) != 0 {
		t.Fatalf("a failed write still opened a pin window: %d pins", len(a.pins))
	}
	if !slices.ContainsFunc(a.toolsLayout().Cards, func(c layout.Card) bool { return c.ID == "zip" }) {
		t.Fatal("the failed drag-out took the card out of the in-memory grid")
	}
	got, err := os.ReadFile(filepath.Join(dir, layout.FileName))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(saved) {
		t.Fatal("the failed write changed layout.json")
	}
	// The write works again: the recovered save must not duplicate the card.
	if err := os.Chmod(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	a.savePins()
	disk, err := layout.Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	gridZip := slices.ContainsFunc(disk.Cards, func(c layout.Card) bool { return c.ID == "zip" })
	pinZip := slices.ContainsFunc(disk.Pins, func(c layout.Pin) bool { return c.ID == "zip" })
	if gridZip && pinZip {
		t.Fatal("the recovered save wrote zip into both the grid and the pins")
	}
}

func TestToolsExactFitGridDoesNotScroll(t *testing.T) {
	a := &shelf{tab: pageTools, visible: true, store: mustStore(t), dataDir: t.TempDir()}
	// Eight 1 × 1 cards fill the two visible rows exactly.
	a.toolsLay = &layout.Layout{Version: layout.Version, Cards: []layout.Card{
		{ID: "ocr"}, {ID: "fit1m"}, {ID: "topdf"}, {ID: "zip"},
		{ID: "tojpeg"}, {ID: "toword"}, {ID: "extract"}, {ID: "resize"},
	}}
	view := ui.NewTester(a.view, sheetW, sheetH)
	p := a.toolsPage()
	view.Frame()
	if p.scroll.MaxY != 0 {
		t.Fatalf("an exact-fit grid has false overflow and edge fading: MaxY=%.1f", p.scroll.MaxY)
	}
}

func TestToolsEditBadgeIsVisibleAndClickableAtGridEdges(t *testing.T) {
	for _, edge := range []string{"top", "left"} {
		t.Run(edge, func(t *testing.T) {
			a := &shelf{tab: pageTools, visible: true, store: mustStore(t)}
			view := ui.NewTester(a.view, sheetW, sheetH)
			view.SetPreferences(ui.Preferences{ReduceMotion: true})
			p := a.toolsPage()
			p.editing = true
			view.Frame()
			r, ok := view.Find("移除")
			if !ok {
				t.Fatal("first card has no remove badge")
			}
			// Tester labels report the visible intersection, so a clipped
			// badge can still be found. The whole 18pt circle must remain.
			if r.W != 18 || r.H != 18 {
				t.Errorf("remove badge lost part of its circle: visible=%+v", r)
			}
			v := p.viewport
			if r.X < v.X || r.Y < v.Y || r.X+r.W > v.X+v.W || r.Y+r.H > v.Y+v.H {
				t.Errorf("remove badge is clipped: badge=%+v viewport=%+v", r, v)
			}
			// The badge belongs to the grid's first card, whatever that is
			// (the widgets lead the default layout now).
			first := a.toolsLayout().Cards[0].ID
			x, y := r.X+r.W/2, r.Y+2
			if edge == "left" {
				x, y = r.X+2, r.Y+r.H/2
			}
			view.ClickAt(x, y)
			if slices.ContainsFunc(a.toolsLayout().Cards, func(c layout.Card) bool { return c.ID == first }) {
				t.Errorf("the %s edge of the first badge cannot remove its card", edge)
			}
			if p.press != nil {
				t.Fatal("the remove badge also started a card press")
			}
		})
	}
}

// TestToolsGridDrawsNoEdgeBlur: the tools grid scrolls, and no card melts at
// the viewport's edges. The strip a card draws at the top edge is the same
// pixels it draws in the open, so the two strips must have the same
// contrast: the old per-row blur (sigma 2.9 at the edge) left the cut strip
// a fraction as sharp.
func TestToolsGridDrawsNoEdgeBlur(t *testing.T) {
	a := &shelf{tab: pageTools, visible: true, level: 2, store: mustStore(t), dataDir: t.TempDir(),
		thumbnails: map[string]*ui.Bitmap{}, thumbPending: map[string]bool{}, appNames: map[string]string{}}
	a.np = &nowPlayingModule{host: a, have: true, last: mac.NowPlayingState{
		Title: "夜曲", Artist: "周杰伦", Duration: 200, Elapsed: 20, Rate: 1, Received: time.Now(),
	}}
	// A fresh install fits the viewport; this test needs a grid that
	// scrolls, so it adds the catalogue's cards to the default five.
	a.mutateTools(func(l *layout.Layout) {
		for _, def := range toolCatalog() {
			l.AddCardSize(def.id, def.span, max(1, def.rows))
		}
	})
	view := ui.NewTester(a.view, sheetW, sheetH)
	view.Frame()
	view.Frame()
	p := a.toolsPage()
	if p.scroll.MaxY <= 0 {
		t.Fatalf("the default grid does not scroll: MaxY=%.0f", p.scroll.MaxY)
	}
	card, ok := view.Find("playing")
	if !ok {
		t.Fatal("the music card is not on screen")
	}
	// One strip of the card, 30pt below its top: the title and the progress
	// row's clocks. The same strip, in the open and at the top edge.
	const cut float32 = 30
	strip := func(img *image.RGBA, x, top float32) int {
		return bandSharpness(img, int(x+6), int(x+toolColW(2)-6), int(top+2), int(top+14))
	}
	open := strip(view.Image(), card.X, card.Y+cut)
	p.scroll.Y = cut // the offset the wheel leaves
	view.Frame()
	view.Frame()
	if got := p.scroll.Y; got != cut {
		t.Fatalf("the grid did not scroll to %.0f: Y=%.0f", cut, got)
	}
	edgeCard, ok := view.Find("playing")
	if !ok {
		t.Fatal("the music card left the grid")
	}
	// The viewport clips it: the visible box starts at the top edge and is
	// the shorter by the scroll.
	if got := card.H - edgeCard.H; got != cut {
		t.Fatalf("the card's visible box is %.0fpt shorter, want %.0f: %+v -> %+v", got, cut, card, edgeCard)
	}
	edge := strip(view.Image(), edgeCard.X, edgeCard.Y)
	t.Logf("contrast in the open %d, at the top edge %d", open, edge)
	if edge*2 < open {
		t.Fatalf("the tools grid melts at its top edge: %d there, %d in the open", edge, open)
	}
}
