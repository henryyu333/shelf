//go:build darwin

package main

// Level geometry and navigation: the island opens first, a page icon opens
// the sheet, and Esc / outside clicks / the shortcut close it in the
// documented order. Every page must fit the fixed 396 × 174 content box
// without clipped or wrapped text.

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"shelf/internal/aiusage"
	"shelf/internal/mac"
	"shelf/internal/settings"
	"shelf/internal/store"
)

// levelShelf is a headless shelf on the overview with a little of both
// stores so every page has content.
func levelShelf(t *testing.T) (*shelf, *ui.Tester) {
	t.Helper()
	s, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	if err := s.AddText("托盘里一条"); err != nil {
		t.Fatal(err)
	}
	for i := range 12 {
		if err := s.AddClipText(fmt.Sprintf("剪贴板内容 %d · 一段足够长的文字", i), "com.example.Source"); err != nil {
			t.Fatal(err)
		}
	}
	a := &shelf{
		store: s, dataDir: t.TempDir(), level: 1, settings: settings.Default(),
		thumbnails: map[string]*ui.Bitmap{}, thumbPending: map[string]bool{}, appNames: map[string]string{},
	}
	return a, ui.NewTester(a.view, sheetW, sheetH)
}

// TestOverviewOpensFirstAndPageIconOpensThePage: one click on the notch is
// the overview; a page icon grows the same shell to the page's size.
func TestOverviewOpensFirstAndPageIconOpensThePage(t *testing.T) {
	a, view := levelShelf(t)
	a.visible = true
	view.Frame()
	if a.currentLevel() != 1 {
		t.Fatalf("level = %d, want the overview", a.currentLevel())
	}
	if !view.HasText("托盘 1 项") || !view.HasText("剪贴板 12 条") {
		t.Fatalf("the overview lost its counts; texts %v", view.Texts())
	}
	if err := view.Click("托盘"); err != nil {
		t.Fatalf("page icon: %v", err)
	}
	if a.currentLevel() != 2 || a.tab != pageTray {
		t.Fatalf("page icon: level=%d tab=%s, want the page on the tray", a.currentLevel(), a.tab)
	}
	view.Frame()
	if !view.HasText("托盘里一条") {
		t.Fatalf("the tray page did not draw its item; texts %v", view.Texts())
	}
	// Esc steps back to the overview; the overview's Esc closes.
	view.Key(0, ui.KeyEscape)
	if a.currentLevel() != 1 || !a.visible {
		t.Fatalf("Esc left level %d visible=%v, want the overview", a.currentLevel(), a.visible)
	}
	view.Frame()
	view.Key(0, ui.KeyEscape)
	if a.visible {
		t.Fatal("the overview's Esc did not close the panel")
	}
}

// TestPageTabsCarryNoBackControl: the band carries the page icons, the
// page's own row carries no ‹ and no 「返回」 wording.
func TestPageTabsCarryNoBackControl(t *testing.T) {
	a, view := levelShelf(t)
	a.visible, a.level = true, 2
	for _, id := range []pageID{pageTray, pageClipboard, pageTools} {
		a.setTab(id)
		view.Frame()
		view.Frame()
		for _, text := range view.Texts() {
			if strings.Contains(text, "‹") || strings.Contains(text, "返回") {
				t.Fatalf("%s: the page row still shows a back control %q; texts %v", id, text, view.Texts())
			}
		}
		if _, ok := view.Find("设置"); !ok {
			t.Fatalf("%s: the band lost its settings button", id)
		}
	}
}

// TestOutsideClickAndShortcutCloseFromEitherLevel closes from both sizes.
func TestOutsideClickAndShortcutCloseFromEitherLevel(t *testing.T) {
	a, _ := levelShelf(t)
	for _, level := range []int{1, 2} {
		a.visible, a.level = true, level
		a.mouseDown(false, 0, mygo.Point{X: a.x, Y: 400})
		if a.visible {
			t.Fatalf("level %d: an outside click did not close the panel", level)
		}
		a.toggle()
		if !a.visible || a.currentLevel() != 1 {
			t.Fatalf("level %d: toggle opened level %d visible=%v, want the overview", level, a.currentLevel(), a.visible)
		}
		a.toggle()
		if a.visible {
			t.Fatalf("level %d: toggle did not close", level)
		}
		a.closeAt = time.Time{} // no fade to wait for between cases
	}
}

func TestIncomingDragOpensThePageOnTheTray(t *testing.T) {
	a, view := levelShelf(t)
	t.Setenv("SHELF_DEBUG_DROP_FILES", "/tmp/a.png,/tmp/b.png")
	a.dragging = true
	a.inBand = true
	a.show(true)
	view.Frame()
	if a.currentLevel() != 2 || a.tab != pageTray {
		t.Fatalf("drag open: level=%d tab=%s, want the page on the tray", a.currentLevel(), a.tab)
	}
	if !view.HasText("放入托盘") || !view.HasText("投放") {
		t.Fatalf("the drop zone is missing; texts %v", view.Texts())
	}
}

func TestClipboardShortcutOpensThePageOnSearch(t *testing.T) {
	a, view := levelShelf(t)
	a.openClipboardSearch()
	view.Frame()
	if a.currentLevel() != 2 || a.tab != pageClipboard || !a.focusMode {
		t.Fatalf("⌃⌥V: level=%d tab=%s focus=%v", a.currentLevel(), a.tab, a.focusMode)
	}
	if !view.Focused(searchLabel) {
		t.Fatal("⌃⌥V did not focus the search field")
	}
}

// fitSink installs the measuring seams' sink for one page audit: panelLabel,
// panelLines and the home pages' fitText all report through fitIssue.
// drain returns the notes collected since the last call.
func fitSink(t *testing.T) (drain func() []string) {
	t.Helper()
	old := fitIssue
	var notes []string
	fitIssue = func(s string) { notes = append(notes, s) }
	t.Cleanup(func() { fitIssue = old })
	return func() []string {
		out := notes
		notes = nil
		return out
	}
}

// pageRows are the three rows a sheet page draws in: the menu-bar row, the
// back row (返回行 — audited like any other row, not skipped) and the 396 ×
// 174 content box. A text outside all three has escaped its parent.
func pageRows(top int) []ui.Rect {
	x := float32((sheetW - contentW) / 2)
	return []ui.Rect{
		{X: 0, Y: float32(top), W: sheetW, H: bandH},
		{X: x, Y: float32(top + bandH), W: contentW, H: pageActionH},
		{X: x, Y: float32(top + bandH + pageActionH + actionsGap), W: contentW, H: contentH},
	}
}

// assertPageFits is the page audit. It fails when
//   - a label's words do not fit the box the layout gave them: panelLabel
//     and panelLines measure every label at its own font (the same
//     MeasureText call the home pages' fitText makes) and report one that
//     needs more width than its box or more height than its lines spell, so
//     an ellipsis can no longer be how a label passes;
//   - a text — the back row's own included — is drawn outside its parent
//     row: the menu-bar band, the back row or the content box (the rows a
//     sheet page is made of), not merely outside the content box;
//   - a measured label is taller than one line of its own font size.
//
// notes are the seam's reports for this frame. top is where the sheet
// starts inside the window (sheetTop). The tester answers one box per
// distinct text, so a row that carries its words as its own label is
// audited through those words' measurement rather than through the row's
// box; text drawn with a raw ui.Text (files outside panel.go and
// usage_page.go) has no measurement to report and is checked by the rows
// above only.
func assertPageFits(t *testing.T, view *ui.Tester, page string, notes []string, top int) {
	t.Helper()
	for _, note := range notes {
		t.Errorf("%s: %s", page, note)
	}
	rows := pageRows(top)
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
		inside := false
		for _, row := range rows {
			if r.X >= row.X-0.5 && r.X+r.W <= row.X+row.W+0.5 &&
				r.Y >= row.Y-0.5 && r.Y+r.H <= row.Y+row.H+0.5 {
				inside = true
				break
			}
		}
		if !inside {
			t.Errorf("%s: text %q at %+v leaves its row (rows %v)", page, text, r, rows)
			continue
		}
		// One line at the text's own font size: the seam recorded the
		// height the words need, so a second line is caught whatever page
		// font grew it. User content is drawn SingleLine and ellipsizes
		// instead of wrapping. A measured label is checked through its own
		// box: Tester.Find answers the first node carrying the text, which
		// can be the 24pt pill or the 30pt row around the label.
		height := r.H
		if b, ok := labelBox(text); ok {
			height = b.H
		}
		if lh, ok := labelLineHeight(text); ok && height > lh*1.5 {
			t.Errorf("%s: text %q is %.1fpt tall for a %.1fpt line: it wrapped", page, text, height, lh)
		}
	}
}

func TestEveryPageFitsTheContentBox(t *testing.T) {
	a, view := levelShelf(t)
	a.visible, a.level = true, 2
	drain := fitSink(t)
	for _, id := range []pageID{pageTray, pageClipboard, pageTools, pagePlaying} {
		a.setTab(id)
		view.Frame()
		drain() // the first frame has no boxes to measure yet
		view.Frame()
		assertPageFits(t, view, string(id), drain(), a.sheetTop())
	}
	// The tools page's edit grid and its open catalogue draw their own
	// labels and rows; both states fit too.
	a.setTab(pageTools)
	p := a.toolsPage()
	for _, state := range []struct {
		name          string
		editing, pick bool
	}{
		{"tools-edit", true, false},
		{"tools-picker", true, true},
	} {
		p.editing, p.picking = state.editing, state.pick
		view.Frame()
		drain()
		view.Frame()
		// The state must really be on screen, or the audit proves nothing.
		want := "完成"
		if state.pick {
			want = "卡片目录"
		}
		if !view.HasText(want) {
			t.Fatalf("%s: the page did not draw its state (%q missing); texts %v", state.name, want, view.Texts())
		}
		assertPageFits(t, view, state.name, drain(), a.sheetTop())
	}
	p.editing, p.picking = false, false
}

// usageStates draws the AI 用量 page in the states the local panel and the
// quota cards can be in, and each one must fit: every range, both login
// failures, a quota answer that only failed to arrive, the first scan and a
// local read error.
func TestUsagePageStatesFitTheContentBox(t *testing.T) {
	now := time.Date(2026, 10, 19, 12, 0, 0, 0, time.Local)
	summary := func(r aiusage.Range) aiusage.Summary {
		return aiusage.Summary{
			Range:   r,
			Total:   aiusage.Totals{Input: 8_400_000, Output: 1_100_000, CacheRead: 5_200_000, CacheWrite: 90_000, Requests: 412, Unpriced: 7, Cost: 3.72},
			ByTool:  map[aiusage.Tool]aiusage.Totals{aiusage.ToolClaude: {Requests: 260, Cost: 2.4}, aiusage.ToolCodex: {Requests: 152, Cost: 1.32}},
			Buckets: []aiusage.Bucket{{Start: now, Tokens: map[aiusage.Tool]int64{aiusage.ToolOmp: 1_000_000, aiusage.ToolClaude: 400_000}}},
		}
	}
	build := func(t *testing.T, mutate func(*usageFixture)) (*ui.Tester, int, []string) {
		t.Helper()
		drain := fitSink(t)
		fx := newUsageFixture(t, true)
		fx.codex.q = okQuota(aiusage.ToolCodex, "Plus", now)
		fx.claude.q = okQuota(aiusage.ToolClaude, "Pro", now)
		fx.scan.summaries = map[aiusage.Range]aiusage.Summary{}
		for _, r := range []aiusage.Range{aiusage.RangeToday, aiusage.Range7d, aiusage.Range30d, aiusage.RangeAll} {
			fx.scan.summaries[r] = summary(r)
		}
		// The page must be the visible one before the tick: gotLocal drops
		// a summary published while another page is current (and the old
		// version of this test ticked before switching, so the KPI grid
		// and the chart were never drawn).
		fx.a.level, fx.a.visible, fx.a.tab = 2, true, pageUsage
		if mutate != nil {
			mutate(fx)
		}
		fx.f.tick(now)
		fx.view.Frame()
		drain()
		fx.view.Frame()
		if !fx.f.hasSummary && fx.f.localErr == nil {
			if _, ok := fx.view.Find("正在统计本机用量…"); ok {
				t.Fatal("the fixture lost its summary: the audit would only see the scan card")
			}
		}
		return fx.view, fx.a.sheetTop(), drain()
	}
	for _, tc := range []struct {
		name   string
		kpi    bool
		mutate func(*usageFixture)
	}{
		{"today", true, nil},
		{"7 days", true, func(fx *usageFixture) { fx.f.rangeSel = aiusage.Range7d }},
		{"30 days", true, func(fx *usageFixture) { fx.f.rangeSel = aiusage.Range30d }},
		{"all", true, func(fx *usageFixture) { fx.f.rangeSel = aiusage.RangeAll }},
		{"not logged in", false, func(fx *usageFixture) {
			fx.codex.q = aiusage.Quota{Tool: aiusage.ToolCodex, Status: aiusage.QuotaNotLoggedIn, FetchedAt: now}
			fx.claude.q = aiusage.Quota{Tool: aiusage.ToolClaude, Status: aiusage.QuotaNotLoggedIn, FetchedAt: now}
		}},
		{"expired", false, func(fx *usageFixture) {
			fx.codex.q = aiusage.Quota{Tool: aiusage.ToolCodex, Status: aiusage.QuotaExpired, FetchedAt: now}
			fx.claude.q = aiusage.Quota{Tool: aiusage.ToolClaude, Status: aiusage.QuotaExpired, FetchedAt: now}
		}},
		{"quota unavailable", false, func(fx *usageFixture) {
			fx.codex.q = aiusage.Quota{Tool: aiusage.ToolCodex, Status: aiusage.QuotaFailed, Err: "dial tcp: no route to host", FetchedAt: now}
			fx.claude.q = aiusage.Quota{Tool: aiusage.ToolClaude, Status: aiusage.QuotaRateLimited, RetryAfter: time.Minute, FetchedAt: now}
		}},
		{"limit reached", false, func(fx *usageFixture) {
			fx.codex.q = aiusage.Quota{Tool: aiusage.ToolCodex, Status: aiusage.QuotaLimitReached, FetchedAt: now}
			fx.claude.q = aiusage.Quota{Tool: aiusage.ToolClaude, Status: aiusage.QuotaLimitReached,
				LimitResetsAt: now.Add(2*time.Hour + 7*time.Minute), FetchedAt: now}
		}},
		{"first scan", false, func(fx *usageFixture) {
			fx.f.hasSummary = false
			fx.f.localBusy = true
			fx.f.scanDone.Store(37)
			fx.f.scanTotal.Store(100)
		}},
		{"local read failed", false, func(fx *usageFixture) {
			fx.f.hasSummary = false
			fx.f.localErr = errors.New("读取本机记录失败：权限不足")
		}},
		{"full windows", true, func(fx *usageFixture) {
			// 100% left is the widest a row gets; both readings must fit.
			for i := range fx.codex.q.Windows {
				fx.codex.q.Windows[i].UsedPct = 0
			}
			fx.a.settings.AIUsage.QuotaLeft = true
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			view, top, notes := build(t, tc.mutate)
			assertPageFits(t, view, "usage/"+tc.name, notes, top)
			// The metric cells are the point of the local panel: a state
			// that should show numbers must draw them, not the scan card.
			if tc.kpi {
				for _, want := range []string{"Token", "请求", "费用", "缓存命中"} {
					if !view.HasText(want) {
						t.Fatalf("%s: the metric cell %q is missing; texts %v", tc.name, want, view.Texts())
					}
				}
				if !view.HasText("本机用量") {
					t.Fatalf("%s: the local panel header is missing", tc.name)
				}
			}
		})
	}
}

func TestUsageHoverDetailStaysInsideTheContentBox(t *testing.T) {
	now := time.Date(2026, 10, 19, 12, 0, 0, 0, time.Local)
	drain := fitSink(t)
	fx := newUsageFixture(t, true)
	fx.codex.q = okQuota(aiusage.ToolCodex, "Plus", now)
	fx.claude.q = okQuota(aiusage.ToolClaude, "Pro", now)
	fx.a.level, fx.a.visible, fx.a.tab = 2, true, pageUsage
	fx.f.tick(now)
	fx.view.Frame()
	fx.view.Move(100, 100) // over the first quota card
	fx.view.Frame()
	if !fx.view.HasText("比均匀用量") {
		t.Fatalf("the hover detail did not appear; texts %v", fx.view.Texts())
	}
	drain()
	fx.view.Frame()
	assertPageFits(t, fx.view, "usage-hover", drain(), fx.a.sheetTop())
}

func TestDragOverTheIslandOpensTheSheet(t *testing.T) {
	a, view := levelShelf(t)
	t.Setenv("SHELF_DEBUG_DROP_FILES", "/tmp/a.png")
	a.visible, a.level = true, 1
	a.dragging = true
	view.Frame()
	if a.currentLevel() != 2 || a.tab != pageTray {
		t.Fatalf("island drag: level=%d tab=%s, want the sheet on the tray", a.currentLevel(), a.tab)
	}
	if !view.HasText("放入托盘") {
		t.Fatalf("the drop zone is missing; texts %v", view.Texts())
	}
}

func TestActivityChipActionsStillWork(t *testing.T) {
	t.Run("converter cancel", func(t *testing.T) {
		j := &convertJob{target: toJPEG, started: time.Now().Add(-time.Second)}
		j.total.Store(3)
		j.done.Store(1)
		a := &shelf{level: 2}
		a.conv = &converter{host: a, job: j}
		a.modules = []module{a.conv}
		view := ui.NewTester(a.view, sheetW, sheetH)
		view.Frame()
		if !view.HasText("正在转成 JPEG · 2/3") {
			t.Fatalf("the converter chip lost its progress tooltip; texts %v", view.Texts())
		}
		if err := view.Click("正在转成 JPEG · 2/3"); err != nil {
			t.Fatalf("tap the chip: %v", err)
		}
		view.Frame()
		if err := view.Click("取消转换"); err != nil {
			t.Fatalf("the chip did not reveal 取消: %v", err)
		}
		if !j.cancel.Load() {
			t.Fatal("取消 did not cancel the job")
		}
	})
	t.Run("awake stop", func(t *testing.T) {
		p := newFakePower()
		m := newAwakeStub(p)
		m.start(time.Now(), 30*time.Minute)
		a := &shelf{level: 2}
		a.modules = []module{m}
		view := ui.NewTester(a.view, sheetW, sheetH)
		view.Frame()
		if !view.HasText("保持唤醒") {
			t.Fatalf("the awake chip is missing; texts %v", view.Texts())
		}
		if err := view.Click("保持唤醒"); err != nil {
			t.Fatalf("tap the chip: %v", err)
		}
		if m.active() || p.held() != 0 {
			t.Fatal("tapping the chip did not stop the keep-awake hold")
		}
	})
}

func TestOverviewHoldsFivePageIconsAndCounts(t *testing.T) {
	fx := newUsageFixture(t, true)
	a := fx.a
	a.level, a.visible = 1, true
	a.registerPage(&fakePage{on: true}) // a sixth page: the band keeps five
	view := ui.NewTester(a.view, sheetW, sheetH)
	view.Frame()
	for _, label := range []string{"总览", "托盘", "剪贴板", "工具", "AI 用量"} {
		r, ok := view.Find(label)
		if !ok {
			t.Fatalf("the band lost the %q icon; texts %v", label, view.Texts())
		}
		if r.Y+r.H > bandH {
			t.Fatalf("the %q icon left the band: %+v", label, r)
		}
	}
	for _, line := range []string{"托盘 0 项", "剪贴板 0 条"} {
		if _, ok := view.Find(line); !ok {
			t.Fatalf("the overview lost %q; texts %v", line, view.Texts())
		}
	}
}

func TestTwoActivityChipsFitTheRightEar(t *testing.T) {
	a := &shelf{level: 2, notchW: 180, notchH: 32, topInset: 32, settings: settings.Default()}
	a.settings.IslandNowPlaying = true
	a.np = &nowPlayingModule{host: a, have: true, last: mac.NowPlayingState{Playing: true, Title: "夜曲", Artist: "周杰伦"}}
	p := newFakePower()
	awake := newAwakeStub(p)
	awake.start(time.Now(), 30*time.Minute)
	a.modules = []module{a.np, awake}
	view := ui.NewTester(a.view, sheetW, sheetH)
	view.Frame()
	music, ok := view.Find("夜曲")
	if !ok {
		t.Fatalf("the now-playing chip is missing; texts %v", view.Texts())
	}
	hold, ok := view.Find("保持唤醒")
	if !ok {
		t.Fatalf("the awake chip is missing; texts %v", view.Texts())
	}
	housingRight := float32(sheetW-sheetPadX) - (float32(sheetW)-float32(180+12))/2
	settings, ok := view.Find("设置")
	if !ok {
		t.Fatal("the settings button is missing")
	}
	if music.X < housingRight-0.5 || hold.X+hold.W > settings.X-1 {
		t.Fatalf("chips %+v and %+v do not fit the right ear between the housing and settings %+v", music, hold, settings)
	}
}

func TestMusicAndCancelChipsFitTheRightEar(t *testing.T) {
	j := &convertJob{target: toJPEG, started: time.Now().Add(-time.Second)}
	j.total.Store(3)
	j.done.Store(1)
	a := &shelf{level: 2, notchW: 180, notchH: 32, topInset: 32, settings: settings.Default()}
	a.settings.IslandNowPlaying = true
	a.np = &nowPlayingModule{host: a, have: true, last: mac.NowPlayingState{Playing: true, Title: "夜曲"}}
	a.conv = &converter{host: a, job: j, cancelOpen: true}
	a.modules = []module{a.np, a.conv}
	view := ui.NewTester(a.view, sheetW, sheetH)
	view.Frame()
	music, ok := view.Find("夜曲")
	if !ok {
		t.Fatalf("the now-playing chip is missing; texts %v", view.Texts())
	}
	cancel, ok := view.Find("取消转换")
	if !ok {
		t.Fatalf("the cancel state is missing; texts %v", view.Texts())
	}
	housingRight := float32(sheetW-sheetPadX) - (float32(sheetW)-float32(180+12))/2
	settings, ok := view.Find("设置")
	if !ok {
		t.Fatal("the settings button is missing")
	}
	if music.X < housingRight-0.5 || cancel.X+cancel.W > settings.X-1 {
		t.Fatalf("chips %+v and %+v do not fit the right ear between the housing and settings %+v", music, cancel, settings)
	}
}

func TestLevelSpringsBetweenTheTwoSizes(t *testing.T) {
	a := motionWindow()
	a.level, a.visible, a.jumpLevel = 1, true, true
	view := ui.NewTester(a.view, sheetW, sheetH)
	for range 60 {
		view.Frame()
		view.Advance(16 * time.Millisecond)
	}
	if a.fx.w < overviewW-0.5 || a.fx.w > overviewW+1 || a.fx.h < overviewH-0.5 || a.fx.h > overviewH+1 {
		t.Fatalf("island shell = %.1f×%.1f, want %d×%d", a.fx.w, a.fx.h, overviewW, overviewH)
	}
	a.setLevel(2)
	for i := range 80 {
		view.Frame()
		f := a.desiredFrame()
		if f.Width < int(a.fx.w+0.5)+2*earR-1 || f.Height < int(a.fx.h+0.5) {
			t.Fatalf("frame %d cuts the growing shell %.1f×%.1f", i, a.fx.w, a.fx.h)
		}
		view.Advance(16 * time.Millisecond)
	}
	if a.fx.w < sheetW-0.5 || a.fx.h < sheetH-0.5 {
		t.Fatalf("sheet shell = %.1f×%.1f, want %d×%d", a.fx.w, a.fx.h, sheetW, sheetH)
	}
	a.setLevel(1)
	for range 80 {
		view.Frame()
		view.Advance(16 * time.Millisecond)
	}
	if a.fx.w > overviewW+1 || a.fx.h > overviewH+1 {
		t.Fatalf("back to island: shell %.1f×%.1f", a.fx.w, a.fx.h)
	}
}

// A plan with per-model weekly windows lists them in the hover detail; the
// compact rows must still fit the 396 × 174 content box.
func TestUsageScopedHoverDetailStaysInsideTheContentBox(t *testing.T) {
	now := time.Date(2026, 10, 19, 12, 0, 0, 0, time.Local)
	drain := fitSink(t)
	fx := newUsageFixture(t, true)
	fx.codex.q = okQuota(aiusage.ToolCodex, "Plus", now)
	q := okQuota(aiusage.ToolClaude, "Pro", now)
	q.Windows = append(q.Windows,
		aiusage.Window{Name: "7 days · Opus", Model: "opus", UsedPct: 37, Length: 7 * 24 * time.Hour},
		aiusage.Window{Name: "7 days · Sonnet", Model: "sonnet", UsedPct: 2, Length: 7 * 24 * time.Hour})
	fx.claude.q = q
	fx.a.level, fx.a.visible, fx.a.tab = 2, true, pageUsage
	fx.f.tick(now)
	fx.view.Frame()
	fx.view.Move(100, 2*usageCardH+usageCardGap+usageCardH/2) // Claude's card
	fx.view.Frame()
	if !fx.view.HasText("每周 · Opus") {
		t.Fatalf("the scoped windows are missing from the hover detail; texts %v", fx.view.Texts())
	}
	drain()
	fx.view.Frame()
	assertPageFits(t, fx.view, "usage-hover-scoped", drain(), fx.a.sheetTop())
}
