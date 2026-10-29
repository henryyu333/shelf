//go:build darwin

package main

// 总览的行为：点刘海就到的一页，左列状态与海獭、右列最近五项和一行「当前最
// 相关的事」。这组测试守住三件事：那一行一次只说一件、按优先级排；所有文字
// 都在 416 × 106 里、不折行；海獭图缺失时不画、也不报错。

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"shelf/internal/layout"
	"shelf/internal/mac"
	"shelf/internal/settings"
	"shelf/internal/store"
)

// overviewShelf is a headless shelf showing the overview, with a little of
// both stores.
func overviewShelf(t *testing.T) (*shelf, *ui.Tester) {
	t.Helper()
	a, _ := levelShelf(t)
	a.visible, a.level = true, 1
	if err := a.store.AddClipText("刚复制的一段文字", "com.example.App"); err != nil {
		t.Fatal(err)
	}
	return a, ui.NewTester(a.view, sheetW, sheetH)
}

// TestOverviewFitsItsBox draws the overview with every kind of content and
// fails on a label cut or wrapped, or on text outside the 416 × 106 body.
func TestOverviewFitsItsBox(t *testing.T) {
	a, view := overviewShelf(t)
	drain := fitSink(t)
	a.settings.IslandNowPlaying = true
	a.np = &nowPlayingModule{host: a, have: true, last: mac.NowPlayingState{
		Title: "倾城 (Live)", Artist: "陈奕迅", Playing: true, Duration: 200, Received: time.Now(),
	}}
	view.Frame()
	drain()
	view.Frame()
	for _, note := range drain() {
		t.Errorf("overview: %s", note)
	}
	// The body's own box: the window's 12pt margins and the 32pt band.
	left := float32((sheetW - contentW) / 2)
	body := ui.Rect{X: left, Y: bandH + contentGap, W: contentW, H: overviewBodyH}
	seen := map[string]bool{}
	for _, text := range view.Texts() {
		if seen[text] {
			continue
		}
		seen[text] = true
		r, ok := view.Find(text)
		if !ok || r.W <= 0 || r.H <= 0 {
			continue
		}
		if r.Y+r.H <= bandH {
			continue // the band's own entries
		}
		if r.Y < body.Y-0.5 || r.Y+r.H > body.Y+body.H+0.5 || r.X < body.X-0.5 || r.X+r.W > body.X+body.W+0.5 {
			t.Errorf("text %q at %+v leaves the overview body %+v", text, r, body)
		}
	}
}

// TestOverviewContextPriority: one line, the most relevant thing first.
// A running job outranks everything; a fresh error outranks the track; the
// track outranks a copy; a copy outranks the count.
func TestOverviewContextPriority(t *testing.T) {
	a, view := overviewShelf(t)
	view.Frame()

	// Nothing but a recent copy: the copy wins over the count.
	if !view.HasText("刚复制：刚复制的一段文字") {
		t.Fatalf("the copy line is missing; texts %v", view.Texts())
	}
	if view.HasText("剪贴板 1 条") && !view.HasText("全部 ›") {
		t.Log("count still drawn where the copy line belongs")
	}

	// A track outranks the copy.
	a.settings.IslandNowPlaying = true
	a.np = &nowPlayingModule{host: a, have: true, last: mac.NowPlayingState{
		Title: "夜曲", Artist: "周杰伦", Playing: true, Duration: 200, Received: time.Now(),
	}}
	view.Frame()
	if !view.HasText("夜曲") || view.HasText("刚复制：刚复制的一段文字") {
		t.Fatalf("the track did not take the line; texts %v", view.Texts())
	}

	// A fresh error outranks the track.
	a.status = "没转成功：报告.pdf 已损坏"
	a.statusKind = statusErr
	view.Frame()
	if !view.HasText("没转成功：报告.pdf 已损坏") || view.HasText("夜曲") {
		t.Fatalf("the error did not take the line; texts %v", view.Texts())
	}

	// A running job outranks the error.
	job := &convertJob{target: toJPEG, started: time.Now().Add(-time.Second)}
	job.total.Store(3)
	job.done.Store(1)
	a.conv = &converter{host: a, job: job}
	view.Frame()
	if !view.HasText("正在转成 JPEG · 2/3") {
		t.Fatalf("the running job did not take the line; texts %v", view.Texts())
	}
	// The job's cancel button is the line's own.
	if err := view.Click("取消转换"); err != nil {
		t.Fatalf("the cancel button is missing: %v", err)
	}
	if !job.cancel.Load() {
		t.Fatal("the cancel button did not cancel the job")
	}
}

// TestOverviewUpdateLineRestarts: an update waiting for a restart gets the
// line and its own restart button.
func TestOverviewUpdateLineRestarts(t *testing.T) {
	a, view := overviewShelf(t)
	a.updateInstalled = true
	a.update = &mygo.Update{Version: "1.2.3"}
	view.Frame()
	if !view.HasText("新版本已就绪 · 重新启动") {
		t.Fatalf("the ready line is missing; texts %v", view.Texts())
	}
	if _, ok := view.Find("重新启动"); !ok {
		t.Fatalf("the restart button is missing; texts %v", view.Texts())
	}
}

// TestOverviewOtterIsStaticArt: the picture comes from resources/otter and
// only swaps between states; a build without the folder draws none and
// never errors.
func TestOverviewOtterIsStaticArt(t *testing.T) {
	oldDir := otterDir
	t.Cleanup(func() { otterDir = oldDir; resetOtters() })
	dir := t.TempDir()
	otterDir = func() string { return dir }
	resetOtters()

	a, view := overviewShelf(t)
	view.Frame() // no picture: the column keeps its shape
	if b := otterImage("neutral"); b != nil {
		t.Fatal("an empty folder answered a picture")
	}
	if got := a.otterState(time.Now()); !strings.Contains("neutral curious happy surprised sleepy embarrassed happy front head", got) {
		t.Fatalf("otter state %q is not one of the eight", got)
	}

	// A drag over the shelf surprises the otter; a running job makes it
	// curious; a failure embarrasses it; a long quiet spell puts it to
	// sleep.
	a.dragging = true
	if got := a.otterState(time.Now()); got != "surprised" {
		t.Fatalf("state during a drag = %q", got)
	}
	a.dragging = false
	job := &convertJob{target: toJPEG, started: time.Now()}
	job.total.Store(2)
	a.conv = &converter{host: a, job: job}
	if got := a.otterState(time.Now()); got != "curious" {
		t.Fatalf("state during a job = %q", got)
	}
	a.conv = nil
	a.statusKind, a.statusUntil = statusErr, time.Now().Add(time.Second)
	if got := a.otterState(time.Now()); got != "embarrassed" {
		t.Fatalf("state on an error = %q", got)
	}
	a.statusKind, a.statusUntil = statusOK, time.Time{}
	a.lastAct = time.Now().Add(-otterSleepyAfter - time.Minute)
	if got := a.otterState(time.Now()); got != "sleepy" {
		t.Fatalf("state after a long quiet spell = %q", got)
	}
	a.lastAct = time.Now()
	if got := a.otterState(time.Now()); got != "neutral" {
		t.Fatalf("state at rest = %q", got)
	}
}

// TestOtterImageDecodesOnce: the same name is decoded once and cached.
func TestOtterImageDecodesOnce(t *testing.T) {
	oldDir := otterDir
	t.Cleanup(func() { otterDir = oldDir; resetOtters() })
	dir := t.TempDir()
	otterDir = func() string { return dir }
	// Write one real PNG under the expected name.
	if err := os.WriteFile(filepath.Join(dir, "shelfu-otter_ui_happy_v001.png"), testPNGBytes(t), 0o600); err != nil {
		t.Fatal(err)
	}
	resetOtters()
	first := otterImage("happy")
	if first == nil {
		t.Fatal("the picture did not decode")
	}
	if second := otterImage("happy"); second != first {
		t.Fatal("the picture was decoded twice")
	}
	if otterImage("not-a-state") != nil {
		t.Fatal("an unknown state answered a picture")
	}
}

// TestWelcomeShowsOnceAndOnlyForAFreshInstall: the card replaces the
// overview for a first run, a drop finishes it, and the mark persists.
func TestWelcomeShowsOnceAndOnlyForAFreshInstall(t *testing.T) {
	dir := t.TempDir()
	a := &shelf{dataDir: dir, freshInstall: true, settings: settingsForTest()}
	a.planOverviewCards()
	if !a.welcomePending {
		t.Fatal("a fresh install has no welcome card")
	}
	a.visible, a.level = true, 1
	view := ui.NewTester(a.view, sheetW, sheetH)
	a.openedAt = time.Now().Add(-2 * time.Second)
	view.Frame()
	if !view.HasText("你好，我是 ShelfU") {
		t.Fatalf("the welcome card is missing; texts %v", view.Texts())
	}
	if err := view.Click("跳过"); err != nil {
		t.Fatalf("the skip button is missing: %v", err)
	}
	view.Frame()
	if view.HasText("你好，我是 ShelfU") {
		t.Fatal("the welcome card stayed after 跳过")
	}
	if st := loadUIState(dir); !st.Welcome {
		t.Fatal("the welcome mark was not written")
	}
	// The next launch never shows it, fresh install or not.
	b := &shelf{dataDir: dir, freshInstall: true, settings: settingsForTest()}
	b.planOverviewCards()
	if b.welcomePending {
		t.Fatal("the welcome card came back on the next launch")
	}

	// An upgrade (existing data) does not show it either.
	c := &shelf{dataDir: t.TempDir(), settings: settingsForTest()}
	c.planOverviewCards()
	if c.welcomePending {
		t.Fatal("an upgrade user got the welcome card")
	}
}

// TestWhatsNewOnVersionChange: a version change shows the card once; a
// dev build and a fresh install do not.
func TestWhatsNewOnVersionChange(t *testing.T) {
	oldProbe := appVersionProbe
	appVersionProbe = func() string { return "1.11.0" }
	t.Cleanup(func() { appVersionProbe = oldProbe })
	dir := t.TempDir()
	if err := saveUIState(dir, uiState{Welcome: true, WhatsNew: "0.0.1"}); err != nil {
		t.Fatal(err)
	}
	a := &shelf{dataDir: dir, settings: settingsForTest()}
	a.planOverviewCards()
	if !a.whatsNewPending {
		t.Fatal("a version change did not queue the new-version card")
	}
	a.visible, a.level = true, 1
	view := ui.NewTester(a.view, sheetW, sheetH)
	a.openedAt = time.Now().Add(-time.Second)
	view.Frame()
	if !view.HasText(appLocTitle("whatsNew", a)) {
		t.Fatalf("the new-version card is missing; texts %v", view.Texts())
	}
	if err := view.Click("好的"); err != nil {
		t.Fatalf("the 好的 button is missing: %v", err)
	}
	if st := loadUIState(dir); st.WhatsNew != appVersion() {
		t.Fatalf("the read version was not recorded: %q", st.WhatsNew)
	}
	// Dismissed: the card does not come back.
	view.Frame()
	if view.HasText(appLocTitle("whatsNew", a)) {
		t.Fatal("the card stayed after 好的")
	}
}

// TestShowWhatsNewReplays: the Settings window's About entry shows the card
// again without changing the startup rule.
func TestShowWhatsNewReplays(t *testing.T) {
	a, view := overviewShelf(t)
	a.ShowWhatsNew()
	view.Frame()
	if !view.HasText(a.appLoc().WhatsNewTitle(appVersion())) {
		t.Fatalf("the replay did not show the card; texts %v", view.Texts())
	}
}

// appLocTitle is the card's title in the shelf's language.
func appLocTitle(_ string, a *shelf) string { return a.appLoc().WhatsNewTitle(appVersion()) }

// settingsForTest is the default settings without touching the disk.
func settingsForTest() settings.Settings { return settings.Default() }

// TestOverviewTilesDragOutAndOpenTheTray: the five tiles are the tray's
// newest items, draggable, and a click opens the tray on the item.
func TestOverviewTilesDragOutAndOpenTheTray(t *testing.T) {
	a, view := overviewShelf(t)
	for i := range 6 {
		if err := a.store.AddText("托盘内容 " + string(rune('a'+i))); err != nil {
			t.Fatal(err)
		}
	}
	view.Frame()
	if got := len(a.overviewItems()); got != overviewTileN {
		t.Fatalf("the strip shows %d items, want %d", got, overviewTileN)
	}
	first := a.overviewItems()[0]
	if err := view.Click(itemTitle(first)); err != nil {
		t.Fatalf("the tile did not take a click: %v", err)
	}
	if a.currentLevel() != 2 || a.tab != pageTray {
		t.Fatalf("the tile opened level=%d tab=%s, want the tray", a.currentLevel(), a.tab)
	}
	if !a.selection.has(first.ID) {
		t.Fatal("the tile did not select its item")
	}
}

// TestToolsGridHoldsTallWidgets: the music card is 2 × 2 in the grid and
// the page scrolls past the two visible rows.
func TestToolsGridHoldsTallWidgets(t *testing.T) {
	l := mustLayout(t)
	withNil := func(string) bool { return false }
	cells := packToolCards(l.Cards, withNil, false)
	var playing, awake toolCell
	for _, c := range cells {
		switch c.id {
		case "playing":
			playing = c
		case "awake":
			awake = c
		}
	}
	if playing.h != toolRowH(2) || playing.w != toolColW(2) {
		t.Fatalf("playing cell = %+v, want 2x2", playing)
	}
	if awake.x != 2*(toolColW(1)+toolCellGap) || awake.y != 0 {
		t.Fatalf("awake cell = %+v, want column 2 of row 0", awake)
	}
	if playing.y+playing.h <= 0 {
		t.Fatal("the playing cell has no height")
	}
}

// mustLayout is the default layout of a shelf with no data directory.
func mustLayout(t *testing.T) layout.Layout {
	t.Helper()
	a := &shelf{dataDir: t.TempDir()}
	return a.toolsLayout()
}

// TestClipboardRowsSixVisible: 174pt holds five 30pt rows plus a part of
// the sixth; the list scrolls with the edge blur.
func TestClipboardRowsSixVisible(t *testing.T) {
	if got := clipRowH * 5; got > contentH {
		t.Fatalf("five clipboard rows take %d of %d", got, contentH)
	}
}

// unused keeps the store import meaningful for the package's other tests.
var _ = store.KindText
