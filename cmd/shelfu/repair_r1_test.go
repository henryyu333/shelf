//go:build darwin

package main

// Repairs from the stage review (2026-10-29): the tools grid's widget
// taps, its drag-out bounds and preview height, the catalogue's scrolling,
// the pinned window's width, the legacy pin rectangles, the light
// appearance's material sync and corner, the first-run test, the card
// marks and the overview's own deadlines. Each test fails on the build
// before its repair.

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"github.com/egoist/mygo/transfer"

	"shelf/internal/layout"
	"shelf/internal/mac"
	"shelf/internal/store"
)

// toolsFixture is a headless shelf showing the tools page.
func toolsFixture(t *testing.T) (*shelf, *ui.Tester) {
	t.Helper()
	s, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	a := &shelf{store: s, dataDir: t.TempDir(), tab: pageTools, level: 2, visible: true}
	return a, ui.NewTester(a.view, sheetW, sheetH)
}

// TestWidgetCardTapRunsItsAction: a whole-card tap on a widget does what
// that widget always did — keep-awake toggles, the clipboard card copies
// its newest capture, AI 用量 opens its page — while the music card, whose
// transport buttons own every press, stays inert to a tap beside them.
func TestWidgetCardTapRunsItsAction(t *testing.T) {
	a, _ := toolsFixture(t)
	p := a.toolsPage()

	// 保持唤醒: the tap starts and stops the hold.
	power := newFakePower()
	a.awake = newAwakeStub(power)
	a.awake.host = a
	p.toolCardTapped("awake")
	if !a.awake.active() || power.held() != 1 {
		t.Fatalf("a tap on 保持唤醒 did not start it: active=%v held=%d", a.awake.active(), power.held())
	}
	p.toolCardTapped("awake")
	if a.awake.active() {
		t.Fatal("a second tap did not stop 保持唤醒")
	}

	// 剪贴板最新: the newest capture goes to the clipboard.
	if err := a.store.AddClipText("最新的剪贴板内容", "com.example.App"); err != nil {
		t.Fatal(err)
	}
	rec := &recordClip{}
	a.write = rec
	p.toolCardTapped("clip")
	if rec.text != "最新的剪贴板内容" {
		t.Fatalf("a tap on 剪贴板最新 copied %q", rec.text)
	}

	// AI 用量: the page opens.
	usage := newUsageFixture(t, true)
	a.registerPage(usage.f)
	a.usage = usage.f
	p.toolCardTapped("usage")
	if a.tab != pageUsage || a.currentLevel() != 2 {
		t.Fatalf("a tap on AI 用量 left tab=%s level=%d", a.tab, a.currentLevel())
	}

	// 音乐: a tap must not toggle playback; its own buttons do.
	before := a.status
	p.toolCardTapped("playing")
	if a.status != before {
		t.Fatalf("a tap on the music card acted: %q", a.status)
	}

	// 最近文件 has nothing to act on and must not jiggle as a failed action.
	p.toolCardTapped("recent")
	if p.jiggleID == "recent" {
		t.Fatal("a tap on 最近文件 reported a failed action")
	}
}

// TestToolsDropBelowTheViewportPins: a card released below the rows the
// scroll shows is dragged out of the panel, not reordered into a row the
// user cannot see.
func TestToolsDropBelowTheViewportPins(t *testing.T) {
	pinCanvasDrag(t)
	a, _ := toolsFixture(t)
	a.settings = settingsForTest()
	a.x, a.y = 400, 0
	a.frame = mygo.Rectangle{X: a.x, Y: a.y, Width: sheetW, Height: sheetH}
	p := a.toolsPage()
	p.editing = true
	view := ui.NewTester(a.view, sheetW, sheetH)
	view.Frame()
	view.Frame()
	if p.viewport.H <= 0 || p.scroll.MaxY <= 0 {
		t.Fatalf("fixture is not scrollable: viewport=%+v scroll=%+v", p.viewport, p.scroll)
	}
	// Below the visible viewport, still inside the grid's total height.
	py := p.scroll.Y + p.viewport.H + 40
	before := a.toolsLayout().Cards
	last := before[len(before)-1].ID
	d := &toolPress{id: last, dragging: true, px: 60, py: py, index: 0, cardX: 0, cardY: 0}
	p.toolCardDrop(d, mygo.Point{X: 60, Y: 2000})
	if len(a.pins) != 1 || a.pins[0].cardID != last {
		t.Fatalf("a release below the viewport did not pin: %d pins", len(a.pins))
	}
	if got := a.toolsLayout().Cards; len(got) != len(before)-1 {
		t.Fatalf("the release also reordered the grid: %d -> %d cards", len(before), len(got))
	}
}

// TestToolsPickerScrolls: with every card removed the catalogue is taller
// than the page, and its rows scroll inside the card instead of being cut.
func TestToolsPickerScrolls(t *testing.T) {
	a, view := toolsFixture(t)
	a.toolsLay = &layout.Layout{Version: layout.Version, Cards: []layout.Card{{ID: "ocr"}}}
	p := a.toolsPage()
	p.editing, p.picking = true, true
	view.Frame()
	view.Frame()
	last, ok := view.Find("隔空投送")
	if !ok {
		t.Fatalf("the catalogue is missing its last row; texts %v", view.Texts())
	}
	bodyBottom := float32(a.sheetTop() + bandH + pageActionH + actionsGap + contentH)
	// With no scrolling the last row would be laid out below the page (the
	// rows add up to far more than 174pt) and cut off; inside a scroll it
	// starts inside the box.
	if last.Y >= bodyBottom {
		t.Fatalf("the last catalogue row at %+v is below the page (bottom %.0f): the list does not scroll",
			last, bodyBottom)
	}
	// The list really moves: a wheel over it pulls the later rows up.
	before := last.Y
	view.Scroll(last.X+last.W/2, last.Y+last.H/2, 0, 120)
	view.Frame()
	view.Frame()
	after, ok := view.Find("隔空投送")
	if !ok {
		t.Fatal("the last row vanished after scrolling")
	}
	if after.Y >= before {
		t.Fatalf("the wheel did not scroll the catalogue: %+v -> %+v", before, after.Y)
	}
}

// TestPinnedWideWidgetsKeepTwoColumns: the desktop form of the music,
// recent-file and clipboard widgets is 278pt even when the card sits in one
// grid column; a card that fits 136pt stays 136.
func TestPinnedWideWidgetsKeepTwoColumns(t *testing.T) {
	if got := pinCardWidth("playing", 1); got != pinCardW2 {
		t.Fatalf("a one-column music card pins %d wide, want %d", got, pinCardW2)
	}
	if got := pinCardWidth("recent", 1); got != pinCardW2 {
		t.Fatalf("a one-column recent card pins %d wide, want %d", got, pinCardW2)
	}
	if got := pinCardWidth("clip", 1); got != pinCardW2 {
		t.Fatalf("a one-column clipboard card pins %d wide, want %d", got, pinCardW2)
	}
	if got := pinCardWidth("awake", 1); got != pinCardW {
		t.Fatalf("保持唤醒 pins %d wide, want %d", got, pinCardW)
	}
	if got := pinCardWidth("awake", 2); got != pinCardW2 {
		t.Fatalf("a two-column 保持唤醒 pins %d wide, want %d", got, pinCardW2)
	}
	// The pin from a 1 × 1 grid card is wide on the desktop but returns to
	// its single column.
	pinCanvasDrag(t)
	a, _ := toolsFixture(t)
	a.settings = settingsForTest()
	a.mutateTools(func(l *layout.Layout) { l.SetCardSize("playing", 1, 1) })
	a.x, a.y = 400, 0
	a.frame = mygo.Rectangle{X: a.x, Y: a.y, Width: sheetW, Height: sheetH}
	p := a.toolsPage()
	p.editing = true
	p.toolCardDrop(&toolPress{id: "playing", dragging: true, px: 60, py: 2000, grabDX: 10, grabDY: 10}, mygo.Point{X: 60, Y: 2000})
	if len(a.pins) != 1 || a.pins[0].cardW != pinCardW2 {
		t.Fatalf("the pinned music card is %+v, want a %dpt window", a.pins, pinCardW2)
	}
	disk, err := layout.Load(a.dataDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(disk.Pins) != 1 || disk.Pins[0].Span != 1 || disk.Pins[0].Rows != 1 {
		t.Fatalf("the pin recorded %+v, want the card's 1 × 1 grid size", disk.Pins)
	}
}

// TestPreviewHeightIsTheCardHeight: the dragged copy leaves the page at the
// card's real height, so a 2 × 2 widget's preview opens where the drawn card
// reaches the edge, not 90pt later.
func TestPreviewHeightIsTheCardHeight(t *testing.T) {
	v := ui.Rect{W: 416, H: 174}
	// At 80pt down the box a one-row card (84pt) still fits (80 + 89 ≤ 174)
	// while a 2 × 2 widget (174pt) reaches past it — the case the review
	// found: the preview opened 90pt late for the tall card.
	top := float32(80)
	if previewLeaves(v, 0, top, toolColW(2), toolRowH(1)) {
		t.Fatal("the one-row copy left the box at 80pt")
	}
	if !previewLeaves(v, 0, top, toolColW(2), toolRowH(2)) {
		t.Fatal("the two-row copy does not leave the box at 80pt")
	}
}

// TestLegacyPinKeepsItsReturnRect: a pin written by the build before the
// grid union stored the window's span (widened to two columns) and the
// canvas rectangle; the rectangle is the return size, and it survives the
// first save.
func TestLegacyPinKeepsItsReturnRect(t *testing.T) {
	dir := t.TempDir()
	old := `{
  "version": 2,
  "cards": [{"id": "ocr"}],
  "pins": [{"id": "playing", "span": 2, "display": "1", "x": 10, "y": 20,
            "homeX": 1, "homeY": 0, "homeW": 1, "homeH": 2}]
}`
	if err := os.WriteFile(filepath.Join(dir, layout.FileName), []byte(old), 0o600); err != nil {
		t.Fatal(err)
	}
	l, err := layout.Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(l.Pins) != 1 {
		t.Fatalf("pins = %+v", l.Pins)
	}
	pin := l.Pins[0]
	if pin.Span != 1 || pin.Rows != 2 {
		t.Fatalf("legacy pin return size = %d × %d, want the recorded 1 × 2", pin.Span, pin.Rows)
	}
	if pin.HomeW != 0 || pin.HomeH != 0 {
		t.Fatal("the legacy rectangle is still applied after the migration")
	}
	// The first save keeps it: 放回刘海 returns the music card 1 × 2.
	if err := layout.Write(dir, l); err != nil {
		t.Fatal(err)
	}
	again, err := layout.Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if again.Pins[0].Span != 1 || again.Pins[0].Rows != 2 {
		t.Fatalf("after a save the pin returns %d × %d", again.Pins[0].Span, again.Pins[0].Rows)
	}
}

// TestFreshOnlyForAnEmptyFolder: any file the app keeps in the data
// directory means an earlier use, so no welcome card — even when the
// history itself is empty.
func TestFreshOnlyForAnEmptyFolder(t *testing.T) {
	empty := t.TempDir()
	s, err := store.Open(empty)
	if err != nil {
		t.Fatal(err)
	}
	if !s.Fresh() {
		t.Fatal("an empty data directory is not a first run")
	}
	s.Close()

	for _, name := range []string{"settings.json", "layout.json", "ui.json"} {
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, name), []byte("{}"), 0o600); err != nil {
			t.Fatal(err)
		}
		s, err := store.Open(dir)
		if err != nil {
			t.Fatal(err)
		}
		if s.Fresh() {
			t.Fatalf("a directory holding %s counts as a first run", name)
		}
		s.Close()
	}
}

// TestMaterialTracksTheShellAndTheAppearance: the backdrop material is
// re-placed on every drawn frame (the shell moves while the window does
// not) and whenever the system appearance changes while the panel is open;
// its radius is the shell's own.
func TestMaterialTracksTheShellAndTheAppearance(t *testing.T) {
	calls := 0
	old := onSyncMaterial
	onSyncMaterial = func() { calls++ }
	t.Cleanup(func() { onSyncMaterial = old })

	if got := materialCorner(156, 0); got != overviewRadius {
		t.Fatalf("overview corner = %.0f, want %d", got, overviewRadius)
	}
	if got := materialCorner(250, 1); got != sheetRadius {
		t.Fatalf("page corner = %.0f, want %d", got, sheetRadius)
	}
	if got := materialCorner(36, 0); got != 18 {
		t.Fatalf("peek corner = %.0f, want half the height", got)
	}

	// Every drawn frame syncs: the shell can change size while the native
	// window stays put (the spring moves inside a frame the level set).
	a, view := toolsFixture(t)
	calls = 0
	view.Frame()
	if calls == 0 {
		t.Fatal("a drawn frame did not sync the material")
	}

	// An appearance change reaches the open panel at once.
	oldDark := macSystemDark
	macSystemDark = func() bool { return false }
	t.Cleanup(func() { macSystemDark = oldDark })
	a.systemDark = true // the cached answer the change is measured against
	calls = 0
	a.refreshAppearance(time.Now().Add(time.Hour))
	if calls == 0 {
		t.Fatal("an appearance change while the panel is open did not sync the material")
	}
}

// TestFreshWhenTheDirectoryIsNew: the real first-launch path has no data
// directory at all; creating it is the store's job, and the run is a first
// one. An error that could hide files (permissions) still reads as an
// existing user.
func TestFreshWhenTheDirectoryIsNew(t *testing.T) {
	root := filepath.Join(t.TempDir(), "ShelfU") // never created
	if _, err := os.Stat(root); !os.IsNotExist(err) {
		t.Fatalf("the fixture created %s", root)
	}
	s, err := store.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if !s.Fresh() {
		t.Fatal("a data directory that did not exist counts as an existing user")
	}
}

// TestLeaveFocusModeHandsTheKeyboardBack: leaving the clipboard search's
// keyboard mode goes back to the app the user came from at once, while the
// panel stays open; the later close has nothing left to restore and must
// not lose the remembered app on the way out.
func TestLeaveFocusModeHandsTheKeyboardBack(t *testing.T) {
	oldActive, oldActivate, oldResign := appIsActive, activateApp, resignSelf
	var activated []int
	resigned := 0
	appIsActive = func() bool { return true }
	activateApp = func(pid int) bool { activated = append(activated, pid); return true }
	resignSelf = func() { resigned++ }
	t.Cleanup(func() { appIsActive, activateApp, resignSelf = oldActive, oldActivate, oldResign })

	a, view := overviewShelf(t)
	// The ⌃⌥V state: the clipboard page, its search focused, the app the
	// user came from remembered.
	a.tab, a.tabFrom, a.level = pageClipboard, pageClipboard, 2
	a.focusMode = true
	a.prevAppPID = 4242
	a.search = "x"
	view.Frame()
	view.Key(0, ui.KeyEscape) // clears nothing, leaves the search mode
	view.Key(0, ui.KeyEscape) // backs out to the overview
	if a.focusMode {
		t.Fatal("Esc left the keyboard mode on")
	}
	if len(activated) != 1 || activated[0] != 4242 {
		t.Fatalf("the app the user came from was not reactivated: %v", activated)
	}
	if a.prevAppPID != 0 {
		t.Fatalf("the remembered app is still pending a restore: %d", a.prevAppPID)
	}
	if !a.visible || a.currentLevel() != 1 {
		t.Fatalf("the panel did not stay open on the overview: visible=%v level=%d", a.visible, a.currentLevel())
	}
	// The final close has nothing to hand back and must not activate again.
	a.hide(hideExplicit)
	view.Frame()
	if len(activated) != 1 {
		t.Fatalf("the close restored the app a second time: %v", activated)
	}
	if resigned != 0 {
		t.Fatalf("the panel resigned itself %d times", resigned)
	}
}

// TestWelcomeFinishesTheVersionToo: finishing the welcome card records the
// version as read, so the same build's new-version card never follows it.
func TestWelcomeFinishesTheVersionToo(t *testing.T) {
	oldProbe := appVersionProbe
	appVersionProbe = func() string { return "1.11.0" }
	t.Cleanup(func() { appVersionProbe = oldProbe })
	dir := t.TempDir()
	a := &shelf{dataDir: dir, freshInstall: true, settings: settingsForTest()}
	a.planOverviewCards()
	a.welcomeUp = true
	a.dismissWelcome()
	if st, _ := loadUIState(dir); st.WhatsNew != "1.11.0" {
		t.Fatalf("the welcome card recorded whatsNew=%q, want the version", st.WhatsNew)
	}
	b := &shelf{dataDir: dir, settings: settingsForTest()}
	b.planOverviewCards()
	if b.whatsNewPending {
		t.Fatal("the new-version card follows the welcome card of the same build")
	}
}

// TestBrokenUIStateIsNotOverwritten: a ui.json that does not parse is left
// alone; the cards still work for the session.
func TestBrokenUIStateIsNotOverwritten(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, uiStateFile)
	if err := os.WriteFile(path, []byte("{nope"), 0o600); err != nil {
		t.Fatal(err)
	}
	a := &shelf{dataDir: dir, freshInstall: true, settings: settingsForTest()}
	a.planOverviewCards()
	if !a.uiStateBroken {
		t.Fatal("a broken ui.json was not marked")
	}
	if !a.welcomePending {
		t.Fatal("the cards stopped working with a broken file")
	}
	a.dismissWelcome()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "{nope" {
		t.Fatalf("the broken file was rewritten:\n%s", data)
	}
}

// TestDropLandsBackOnTheOverview: a file dropped on the panel's drop zone
// returns to the overview when it lands; the tray page was the gesture's
// page, not the user's choice.
func TestDropLandsBackOnTheOverview(t *testing.T) {
	a, view := toolsFixture(t)
	a.setTab(pageTray)
	view.Frame()
	dir := t.TempDir()
	file := filepath.Join(dir, "dropped.txt")
	if err := os.WriteFile(file, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	a.visible, a.level = true, 2
	a.drop.on = true // the drag's drop zone is up
	data, err := transfer.FileData(file)
	if err != nil {
		t.Fatal(err)
	}
	if err := a.receive(data); err != nil {
		t.Fatal(err)
	}
	if a.currentLevel() != 1 || a.tab != pageOverview {
		t.Fatalf("after the drop: level=%d tab=%s, want the overview", a.currentLevel(), a.tab)
	}
}

// TestReduceMotionHoverShapeNeverSprings: with Reduce Motion on, the peek
// shape takes its new width and height on the frame the state changes, and
// the following frame does not move it again; with the preference off the
// same change keeps springing. (mygo's AnimateTo also snaps under Reduce
// Motion, so the shell's explicit jumps and the engine agree here: the
// test pins the behaviour either way.)
func TestReduceMotionHoverShapeNeverSprings(t *testing.T) {
	for _, reduce := range []bool{true, false} {
		name := "spring"
		if reduce {
			name = "reduce motion"
		}
		t.Run(name, func(t *testing.T) {
			a := motionWindow()
			a.settings.IslandNowPlaying = false
			a.np = nil
			a.visible, a.level = false, 1
			view := ui.NewTester(a.view, sheetW, sheetH)
			if reduce {
				view.SetPreferences(ui.Preferences{ReduceMotion: true})
			}
			// The plain chevron bulge, settled.
			a.peeking = true
			for range 40 {
				view.Frame()
				view.Advance(16 * time.Millisecond)
			}
			// A track arrives: the wings replace the chevron's bulge, a
			// much wider shape.
			a.settings.IslandNowPlaying = true
			a.np = &nowPlayingModule{host: a, have: true, last: mac.NowPlayingState{
				Title: "夜曲", Playing: true, Duration: 200, Received: time.Now(),
			}}
			view.Frame() // the frame the state changes on
			w0, h0 := a.fx.peekW, a.fx.peekH
			view.Advance(16 * time.Millisecond)
			view.Frame()
			w1, h1 := a.fx.peekW, a.fx.peekH
			if reduce {
				if w1 != w0 || h1 != h0 {
					t.Fatalf("reduce motion moved the shape after its first frame: %.1f×%.1f -> %.1f×%.1f",
						w0, h0, w1, h1)
				}
				return
			}
			if w1 == w0 && h1 == h0 {
				t.Fatalf("the spring never moved the shape (%.1f×%.1f): the fixture does not exercise it", w0, h0)
			}
		})
	}
}

// TestOverviewDeadlinesNeverWakeAClosedPanel: the overview works out when
// its words change and the monitor tick redraws it, but only while the
// overview is on screen — no frame timer is left behind, so a closed panel
// draws nothing when a copy ages out.
func TestOverviewDeadlinesNeverWakeAClosedPanel(t *testing.T) {
	draws := 0
	a, _ := levelShelf(t)
	a.visible, a.level = true, 1
	if err := a.store.AddClipText("刚复制的文字", "com.example.App"); err != nil {
		t.Fatal(err)
	}
	view := ui.NewTester(func(c *ui.Context) {
		draws++
		a.view(c)
	}, sheetW, sheetH)
	view.Frame()
	if a.overviewDue.IsZero() {
		t.Fatal("the overview did not work out a deadline")
	}
	// A frame timer would still be waiting after the panel closes; the
	// tick-driven deadline arms nothing.
	if view.FramePending() {
		t.Fatal("drawing the overview left a frame timer armed")
	}

	// Closing forgets the deadline, and the tick draws nothing afterwards.
	onHide := func() {
		a.visible = false
		a.closeAt = time.Now()
		a.finishClose()
	}
	onHide()
	if !a.overviewDue.IsZero() {
		t.Fatalf("a closed panel kept the deadline %v", a.overviewDue)
	}
	base := draws
	// Time passes with the panel closed: a frame timer left behind would
	// fire here and draw a hidden frame; the tick draws nothing.
	view.Advance(3 * time.Minute)
	if draws != base {
		t.Fatalf("a closed panel drew %d frames when its deadline passed", draws-base)
	}
	a.tickOverviewDeadlines(view.Now().Add(3 * time.Minute))
	if draws != base {
		t.Fatalf("the tick drew %d frames after the close", draws-base)
	}

	// Open again: the deadline is worked out anew and the tick redraws at
	// the deadline itself. (The copy aged out during the advance, so a
	// fresh happy span is what has a deadline now.)
	a.happyUntil = view.Now().Add(2 * time.Second)
	a.visible, a.level = true, 1
	view.Frame()
	if a.overviewDue.IsZero() {
		t.Fatal("the reopened overview has no deadline")
	}
	due := a.overviewDue
	a.tickOverviewDeadlines(due.Add(-time.Second))
	if !a.overviewDue.Equal(due) {
		t.Fatal("the tick fired before the deadline")
	}
	a.tickOverviewDeadlines(due.Add(time.Second))
	if !a.overviewDue.IsZero() {
		t.Fatal("the tick did not consume the passed deadline")
	}
}

// TestReduceMotionJumpsTheShell: with Reduce Motion the shell takes its
// size at once (no spring, no overshoot) and only the content fades; the
// spring still animates when the preference is off.
func TestReduceMotionJumpsTheShell(t *testing.T) {
	a := motionWindow()
	a.level, a.visible, a.jumpLevel = 1, true, true
	view := ui.NewTester(a.view, sheetW, sheetH)
	view.SetPreferences(ui.Preferences{ReduceMotion: true})
	for range 40 {
		view.Frame()
		view.Advance(16 * time.Millisecond)
	}
	if a.fx.w != overviewW || a.fx.h != overviewH {
		t.Fatalf("reduced overview shell = %.1f×%.1f", a.fx.w, a.fx.h)
	}
	// Switching to a page: the first frame after the switch is already the
	// page's size, with no intermediate heights.
	a.setLevel(2)
	view.Frame()
	if a.fx.h != sheetH {
		t.Fatalf("reduced level change drew %.0f tall, want the page at once", a.fx.h)
	}
	view.Advance(16 * time.Millisecond)
	view.Frame()
	if a.fx.h != sheetH {
		t.Fatalf("reduced level change overshot to %.0f", a.fx.h)
	}
}

// TestOverviewSchedulesItsDeadlines: the otter's happiness and the quiet
// spell end on their own — a resting panel draws no frames, so the overview
// asks for the frame where its words and picture change.
func TestOverviewSchedulesItsDeadlines(t *testing.T) {
	a, view := overviewShelf(t)
	base := view.Now()
	a.lastAct = base
	a.happyUntil = base.Add(time.Second)
	view.Frame()
	if got := a.otterState(base); got != "happy" {
		t.Fatalf("state inside the happy span = %q", got)
	}
	// The scheduled frame runs on its own: the tester only advances the
	// clock, and the view has already asked for the deadline.
	view.Advance(1500 * time.Millisecond)
	if got := a.otterState(view.Now()); got == "happy" {
		t.Fatalf("the otter is still %q after its span", got)
	}
	// The quiet spell is scheduled too: an hour without use makes it
	// sleepy and the overview still asks for a frame then.
	a.happyUntil = time.Time{}
	a.lastAct = view.Now().Add(-otterSleepyAfter - time.Second)
	view.Frame()
	if got := a.otterState(view.Now()); got != "sleepy" {
		t.Fatalf("state after the quiet spell = %q", got)
	}
}
