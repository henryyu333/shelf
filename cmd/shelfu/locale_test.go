//go:build darwin

package main

// English coverage: the whole interface says English with no Chinese left in
// it, every page still fits the fixed boxes, and a language change shows at
// once without a restart. The rest of the suite runs in the source language.

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"
	"unicode"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/i18n"
	"github.com/egoist/mygo/transfer"
	"github.com/egoist/mygo/ui"

	"shelf/internal/aiusage"
	"shelf/internal/settings"
	"shelf/internal/store"
	"shelf/locales"
)

// hasHan reports whether s has a Han character, the residue an English
// interface must never show. Only user data may.
func hasHan(s string) bool {
	for _, r := range s {
		if unicode.Is(unicode.Han, r) {
			return true
		}
	}
	return false
}

func assertNoHan(t *testing.T, where string, texts []string) {
	t.Helper()
	for _, text := range texts {
		if hasHan(text) {
			t.Errorf("%s: Chinese left in the English interface: %q", where, text)
		}
	}
}

// englishShelf is levelShelf with English user data and the app showing
// English: the panel's language and the native surfaces' language agree, as
// they do in the app.
func englishShelf(t *testing.T) (*shelf, *ui.Tester) {
	t.Helper()
	s, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	if err := s.AddText("A note from the tray"); err != nil {
		t.Fatal(err)
	}
	for i := range 11 {
		if err := s.AddClipText(fmt.Sprintf("Clipboard line %d, long enough to fill the row", i), "com.example.Source"); err != nil {
			t.Fatal(err)
		}
	}
	// The newest row, and the one a context menu is opened on.
	if err := s.AddClipText("Clipboard newest", "com.example.Source"); err != nil {
		t.Fatal(err)
	}
	a := &shelf{
		store: s, dataDir: t.TempDir(), level: 1, settings: settings.Default(), locale: "en",
		thumbnails: map[string]*ui.Bitmap{}, thumbPending: map[string]bool{}, appNames: map[string]string{},
	}
	view := ui.NewTester(a.view, sheetW, sheetH)
	view.SetLocale("en")
	return a, view
}

// TestEnglishPanelPagesHaveNoChineseAndFit draws the overview and every
// page in English: no label may say Chinese, and the 416 × 174 audit must
// find nothing cut or wrapped.
func TestEnglishPanelPagesHaveNoChineseAndFit(t *testing.T) {
	a, view := englishShelf(t)
	drain := fitSink(t)

	a.visible = true
	view.Frame()
	view.Frame()
	assertNoHan(t, "overview", view.Texts())
	if !view.HasText(a.appLoc().OverviewTrayCount(1)) {
		t.Fatalf("the overview does not say its tray count in English; texts %v", view.Texts())
	}
	if notes := drain(); len(notes) > 0 {
		t.Errorf("overview: %v", notes)
	}

	a.level = 2
	for _, id := range []pageID{pageTray, pageClipboard, pageTools, pagePlaying} {
		a.setTab(id)
		view.Frame()
		drain() // the first frame of a tab has no boxes yet
		view.Frame()
		assertNoHan(t, string(id), view.Texts())
		assertPageFits(t, view, string(id), drain(), a.sheetTop())
	}

	// The tools catalogue says English too.
	a.setTab(pageTools)
	if p := a.toolsPage(); p != nil {
		p.editing, p.picking = true, true
		view.Frame()
		drain() // the first frame of the state has no boxes yet
		view.Frame()
		assertNoHan(t, "tools-picker", view.Texts())
		if notes := drain(); len(notes) > 0 {
			t.Errorf("tools-picker: %v", notes)
		}
		p.editing, p.picking = false, false
	}
}

// TestEnglishSettingsWindowHasNoChinese: every page of the Settings window
// says English.
func TestEnglishSettingsWindowHasNoChinese(t *testing.T) {
	a, view := newSettingsShelf(t)
	a.locale = "en"
	view.SetLocale("en")
	l := locales.In("en")
	for _, nav := range []string{
		l.General(), l.SettingsNotchNav(), l.SettingsTraySection(), l.SettingsClipboardSection(),
		l.SettingsAISection(), l.SettingsShortcutsSection(), l.PermTitle(), l.SettingsAboutPage(),
	} {
		openSettingsPage(t, view, nav)
		view.Frame()
		// The language's own name is shown in itself ("简体中文"), as macOS
		// shows language menus: that is not a leftover translation.
		texts := view.Texts()
		for i, text := range texts {
			if text == "简体中文" {
				texts[i] = ""
			}
		}
		assertNoHan(t, "settings/"+nav, texts)
	}
	joined := strings.Join(view.Texts(), "\n")
	for _, want := range []string{
		l.SettingsAboutPage(), l.SettingsAboutVersion(appVersion(), runtime.GOARCH),
		l.SettingsAboutWhatsNew(), l.SettingsOpenDataFolder(),
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("settings: %q missing from the English About page", want)
		}
	}
	openSettingsPage(t, view, l.General())
	for _, want := range []string{
		l.SettingsLoginRow(), l.SettingsAutoCheckRow(), l.LanguageRow(),
		l.LanguageFollowSystem(), l.LanguageEnglish(),
	} {
		if !strings.Contains(strings.Join(view.Texts(), "\n"), want) {
			t.Errorf("settings: %q missing from the English general page", want)
		}
	}
}

// TestEnglishSettingsWindowLayout is the Settings window's truncation and
// overlap audit: in English no group's text leaves the window, no two texts
// overlap, no single-line label is cut (the settingsFit seam), and a caption
// that wraps grows its row instead of pressing the hairline or the next row.
func TestEnglishSettingsWindowLayout(t *testing.T) {
	var cut []string
	orig := settingsFit
	settingsFit = func(s string) { cut = append(cut, s) }
	t.Cleanup(func() { settingsFit = orig })

	a, view := newSettingsShelf(t)
	a.locale = "en"
	view.SetLocale("en")
	l := locales.In("en")

	// Every page, both ends of its scroll: nothing is cut, nothing leaves
	// the window, nothing overlaps.
	audit := func(where string) {
		view.Frame()
		view.Frame()
		assertSettingsLayout(t, view, where)
	}
	for _, nav := range []string{
		l.General(), l.SettingsNotchNav(), l.SettingsTraySection(), l.SettingsClipboardSection(),
		l.SettingsAISection(), l.SettingsShortcutsSection(), l.PermTitle(), l.SettingsAboutPage(),
	} {
		openSettingsPage(t, view, nav)
		a.settingsScroll.Y = 0
		audit("top/" + nav)
		a.settingsScroll.Y = 1e6
		audit("bottom/" + nav)
	}

	// A caption that wraps ends above the next row's text, never on it.
	openSettingsPage(t, view, l.SettingsNotchNav())
	a.settingsScroll.Y = 0
	view.Frame()
	view.Frame()
	for _, pair := range [][2]string{
		{l.ShellStyleCaption(), l.SettingsIslandPosRow()},
	} {
		cap, ok := view.Find(pair[0])
		if !ok {
			t.Fatalf("caption %q missing", pair[0])
		}
		next, ok := view.Find(pair[1])
		if !ok {
			continue // the island row only exists on a floating island
		}
		if gap := next.Y - (cap.Y + cap.H); gap < 2 {
			t.Errorf("%q ends %.1fpt above %q: the caption presses the next row", pair[0], gap, pair[1])
		}
	}

	// A one-line row keeps the design's 50pt height.
	openSettingsPage(t, view, l.General())
	a.settingsScroll.Y = 0
	view.Frame()
	view.Frame()
	if r, ok := view.Find(l.SettingsLoginRow()); ok && r.H != 50 {
		t.Errorf("a one-line row is %.1fpt tall, want 50", r.H)
	}
	for _, note := range cut {
		t.Errorf("settings: %s", note)
	}
}

// assertSettingsLayout checks the Settings window's boxes: no text leaves the
// window sideways, and no two texts drawn in it overlap.
func assertSettingsLayout(t *testing.T, view *ui.Tester, where string) {
	t.Helper()
	type box struct {
		text string
		r    ui.Rect
	}
	var boxes []box
	seen := map[ui.Rect]bool{}
	for _, text := range view.Texts() {
		r, ok := view.Find(text)
		if !ok || r.W <= 0 || r.H <= 0 || seen[r] {
			continue
		}
		seen[r] = true
		if r.X < -0.5 || r.X+r.W > settingsWidth+0.5 {
			t.Errorf("%s: %q leaves the window sideways: %+v", where, text, r)
		}
		if r.Y < -0.5 || r.Y+r.H > settingsHeight+0.5 {
			continue // scrolled out of the viewport
		}
		boxes = append(boxes, box{text, r})
	}
	// A toggle row carries its label as its accessible name, so Find answers
	// the row, which holds its own label and caption: a box that contains
	// another box is a container, not a line of text.
	var lines []box
	for i, b := range boxes {
		container := false
		for j, o := range boxes {
			if i != j && o.r.X >= b.r.X-0.5 && o.r.Y >= b.r.Y-0.5 &&
				o.r.X+o.r.W <= b.r.X+b.r.W+0.5 && o.r.Y+o.r.H <= b.r.Y+b.r.H+0.5 {
				container = true
				break
			}
		}
		if !container {
			lines = append(lines, b)
		}
	}
	boxes = lines
	for i := range boxes {
		for j := i + 1; j < len(boxes); j++ {
			a, b := boxes[i], boxes[j]
			if a.r.X < b.r.X+b.r.W-0.5 && b.r.X < a.r.X+a.r.W-0.5 &&
				a.r.Y < b.r.Y+b.r.H-0.5 && b.r.Y < a.r.Y+a.r.H-0.5 {
				t.Errorf("%s: %q at %+v overlaps %q at %+v", where, a.text, a.r, b.text, b.r)
			}
		}
	}
}

// TestSettingsLabelReportsCutText proves the seam the settings audit relies
// on: a single-line label wider than its box is reported.
func TestSettingsLabelReportsCutText(t *testing.T) {
	var cut []string
	orig := settingsFit
	settingsFit = func(s string) { cut = append(cut, s) }
	t.Cleanup(func() { settingsFit = orig })

	view := ui.NewTester(func(c *ui.Context) {
		ui.Row(c).Width(80).Children(func() {
			settingsLabel(c, "A label far wider than eighty points", 13, 0)
		})
	}, 200, 40)
	view.Frame()
	view.Frame()
	if len(cut) == 0 {
		t.Fatal("a label wider than its box was not reported")
	}
}

// TestEnglishMenusHaveNoChinese: the tray menu and an item's context menu
// say English, labels and submenu labels alike.
func TestEnglishMenusHaveNoChinese(t *testing.T) {
	a, view := englishShelf(t)
	labels := menuLabels(a.trayMenu())
	for _, want := range []string{"Show / Hide ShelfU", "Settings…", "Quit ShelfU"} {
		if !slices.Contains(labels, want) {
			t.Fatalf("the tray menu lacks %q; labels %v", want, labels)
		}
	}
	assertNoHan(t, "tray menu", labels)

	// The clipboard row's context menu: open it on a row and read it.
	a.visible, a.level = true, 0 // level 0 without a native window is the sheet
	a.setTab(pageClipboard)
	view.Frame()
	view.Frame()
	if err := view.RightClick("Clipboard newest"); err != nil {
		t.Fatalf("right-click a clipboard row: %v", err)
	}
	menu := view.Menu()
	if len(menu) == 0 {
		t.Fatal("no context menu on a clipboard row")
	}
	for _, want := range []string{"Copy Text", "Pin", "Remove"} {
		if !slices.Contains(menu, want) {
			t.Fatalf("the item menu lacks %q; menu %v", want, menu)
		}
	}
	assertNoHan(t, "item menu", menu)
}

// TestEnglishToastsHaveNoChinese: a status and an error the user sees say
// English.
func TestEnglishToastsHaveNoChinese(t *testing.T) {
	a, _ := englishShelf(t)
	a.toastStatus(a.appLoc().ItemCopied(), time.Now().Add(time.Second), statusOK)
	assertNoHan(t, "status", []string{a.status})
	a.report(errors.New("boom"))
	assertNoHan(t, "report", []string{a.status})
	if !strings.Contains(a.status, "boom") {
		t.Fatalf("the failure lost its detail: %q", a.status)
	}
}

// TestEnglishUsagePageHasNoChineseAndFits draws the AI usage page in English
// while it has no data yet and once it has a summary.
func TestEnglishUsagePageHasNoChineseAndFits(t *testing.T) {
	fx := newUsageFixture(t, true)
	fx.a.locale = "en"
	fx.view.SetLocale("en")
	fx.a.setTab(pageUsage)
	fx.a.level = 2
	drain := fitSink(t)
	for _, name := range []string{"empty", "empty-again"} {
		fx.view.Frame()
		drain()
		fx.view.Frame()
		assertNoHan(t, "usage-"+name, fx.view.Texts())
		assertPageFits(t, fx.view, "usage-"+name, drain(), fx.a.sheetTop())
	}
}

// TestEnglishUsagePageStatesHaveNoChineseAndFit draws the AI usage page in
// English with quotas, a local summary and their failure states: the 84pt
// quota cards, the 64pt columns and the chart must all fit, and no Chinese
// may show (dates and weekdays in English too).
func TestEnglishUsagePageStatesHaveNoChineseAndFit(t *testing.T) {
	now := time.Date(2026, 10, 19, 12, 0, 0, 0, time.Local)
	summary := func(r aiusage.Range) aiusage.Summary {
		return aiusage.Summary{
			Range:   r,
			Total:   aiusage.Totals{Input: 8_400_000, Output: 1_100_000, CacheRead: 5_200_000, CacheWrite: 90_000, Requests: 412, Unpriced: 7, Cost: 3.72},
			ByTool:  map[aiusage.Tool]aiusage.Totals{aiusage.ToolClaude: {Requests: 260, Cost: 2.4}, aiusage.ToolCodex: {Requests: 152, Cost: 1.32}},
			Buckets: []aiusage.Bucket{{Start: now, Tokens: map[aiusage.Tool]int64{aiusage.ToolOmp: 1_000_000, aiusage.ToolClaude: 400_000}}},
		}
	}
	for _, tc := range []struct {
		name   string
		mutate func(*usageFixture)
	}{
		{"data", nil},
		{"not logged in", func(fx *usageFixture) {
			fx.codex.q = aiusage.Quota{Tool: aiusage.ToolCodex, Status: aiusage.QuotaNotLoggedIn, FetchedAt: now}
			fx.claude.q = aiusage.Quota{Tool: aiusage.ToolClaude, Status: aiusage.QuotaNotLoggedIn, FetchedAt: now}
		}},
		{"limit reached", func(fx *usageFixture) {
			fx.codex.q = aiusage.Quota{Tool: aiusage.ToolCodex, Status: aiusage.QuotaLimitReached, FetchedAt: now}
			fx.claude.q = aiusage.Quota{Tool: aiusage.ToolClaude, Status: aiusage.QuotaLimitReached,
				LimitResetsAt: now.Add(2*time.Hour + 7*time.Minute), FetchedAt: now}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			drain := fitSink(t)
			fx := newUsageFixture(t, true)
			fx.codex.q = okQuota(aiusage.ToolCodex, "Plus", now)
			fx.claude.q = okQuota(aiusage.ToolClaude, "Pro", now)
			fx.scan.summaries = map[aiusage.Range]aiusage.Summary{}
			for _, r := range []aiusage.Range{aiusage.RangeToday, aiusage.Range7d, aiusage.Range30d, aiusage.RangeAll} {
				fx.scan.summaries[r] = summary(r)
			}
			fx.a.level, fx.a.visible, fx.a.tab = 2, true, pageUsage
			fx.a.locale = "en"
			fx.view.SetLocale("en")
			if tc.mutate != nil {
				tc.mutate(fx)
			}
			fx.f.tick(now)
			fx.view.Frame()
			drain()
			fx.view.Frame()
			assertNoHan(t, "usage/"+tc.name, fx.view.Texts())
			assertPageFits(t, fx.view, "usage/"+tc.name, drain(), fx.a.sheetTop())
			if tc.name == "data" && !fx.view.HasText("Requests") {
				t.Fatalf("the local panel says no English metrics; texts %v", fx.view.Texts())
			}
		})
	}
}

// TestResolveLanguage is the one rule every moment obeys: a language
// settings.json names is shown as it is; Follow System reads the system's
// FIRST preferred language only, Chinese for anything zh (Traditional
// included) and English for every other — never a second language, and never
// mygo's fallback to the source language.
func TestResolveLanguage(t *testing.T) {
	prefs := [][]string{
		{"fr-FR"},
		{"ja-JP"},
		{"fr-FR", "zh-Hans-CN"},
		{"zh-Hant-TW", "en-US"},
		{"zh-Hans-CN"},
		{"en-US", "zh-Hans-CN"},
		{}, // a system that lists no language
	}
	// Follow System: only the first language counts.
	follow := map[string]string{
		"fr-FR":      "en",
		"ja-JP":      "en",
		"zh-Hant-TW": "zh-Hans",
		"zh-TW":      "zh-Hans",
		"zh-HK":      "zh-Hans",
		"zh":         "zh-Hans",
		"zh-Hans-CN": "zh-Hans",
		"en-US":      "en",
		"en-GB":      "en",
		"":           "en",
	}
	for _, list := range prefs {
		first := ""
		if len(list) > 0 {
			first = list[0]
		}
		// "" (Follow System) — a settings language of "" may name neither.
		if got, want := resolveLanguage("", list), follow[first]; got != want {
			t.Errorf("resolveLanguage(\"\", %v) = %q, want %q", list, got, want)
		}
		// An explicit language is shown exactly, on every system.
		for _, tag := range []string{"zh-Hans", "en"} {
			if got := resolveLanguage(tag, list); got != tag {
				t.Errorf("resolveLanguage(%q, %v) = %q, want %q", tag, list, got, tag)
			}
		}
	}
	// A settings.json the user edited by hand names no language of ours: that
	// follows the system, as an empty one does.
	for _, junk := range []string{"fr", "de-DE", "ZH-hans", " zh-Hans"} {
		if got := resolveLanguage(junk, []string{"zh-Hant-TW"}); got != "zh-Hans" {
			t.Errorf("resolveLanguage(%q) = %q, want the system's zh-Hans", junk, got)
		}
	}
}

// TestFollowSystemAppliesTheRule: choosing Follow System in Settings shows the
// language the rule resolves for the system's first preferred language — not
// mygo's fallback to the source language — and a system language change while
// the shelf follows the system is re-resolved with the same rule.
func TestFollowSystemAppliesTheRule(t *testing.T) {
	stubSettingsSeams(t, false, nil, nil)
	origSet, origPrefs := setAppLocale, preferredLanguages
	t.Cleanup(func() { setAppLocale, preferredLanguages = origSet, origPrefs })
	var applied []string
	setAppLocale = func(tag string) { applied = append(applied, tag) }
	preferredLanguages = func() []string { return []string{"fr-FR"} }

	a, view := newSettingsShelf(t)
	view.Frame()
	view.Frame()
	view.Click("English")
	if len(applied) != 1 || applied[0] != "en" {
		t.Fatalf("choosing English applied %v, want [en]", applied)
	}
	if loaded, err := settings.Load(a.dataDir); err != nil || loaded.Language != "en" {
		t.Fatalf("settings.json kept %q (err %v), want en", loaded.Language, err)
	}
	// Back to Follow System on a French system: English, by the rule, not "".
	view.Frame()
	view.Click(locales.In("").LanguageFollowSystem())
	if len(applied) != 2 || applied[1] != "en" {
		t.Fatalf("following the system applied %v, want [en en]", applied)
	}
	if loaded, err := settings.Load(a.dataDir); err != nil || loaded.Language != "" {
		t.Fatalf("settings.json kept %q (err %v), want \"\"", loaded.Language, err)
	}

	// A Traditional Chinese system follows into Simplified Chinese, though
	// mygo's own match would read it as English.
	preferredLanguages = func() []string { return []string{"zh-Hant-TW", "en-US"} }
	applied = nil
	view.Frame()
	view.Click(locales.In("").LanguageEnglish())
	if len(applied) != 1 || applied[0] != "en" {
		t.Fatalf("choosing English applied %v, want [en]", applied)
	}
	view.Frame()
	view.Click(locales.In("").LanguageFollowSystem())
	if len(applied) != 2 || applied[1] != "zh-Hans" {
		t.Fatalf("following a zh-Hant system applied %v, want [en zh-Hans]", applied)
	}

	// The system's languages changed while the shelf follows them: the next
	// activation or panel opening re-resolves with the same rule. An explicit
	// language ignores the change.
	a.win = &mygo.Window{}
	a.locale = "zh-Hans"
	preferredLanguages = func() []string { return []string{"en-US"} }
	a.followSystemLanguage()
	if len(applied) != 3 || applied[2] != "en" {
		t.Fatalf("a system language change applied %v, want a third tag en", applied)
	}
	// Nothing new to apply: no second call, no redraw.
	a.locale = "en"
	a.followSystemLanguage()
	if len(applied) != 3 {
		t.Fatalf("an unchanged system language applied %v again", applied)
	}
	a.settings.Language = "zh-Hans"
	a.locale = "en"
	a.followSystemLanguage()
	if len(applied) != 3 {
		t.Fatalf("an explicit language was overridden by the system: %v", applied)
	}
}

// TestTimesFollowTheSystemClock: times follow the user's 12- or 24-hour
// clock in every language, so an English interface on a 24-hour Mac says
// 14:05 rather than 2:05 PM.
func TestTimesFollowTheSystemClock(t *testing.T) {
	fixed := time.Date(2026, 10, 19, 14, 5, 0, 0, time.Local)
	render := func(locale string, r i18n.Region) []string {
		view := ui.NewTester(func(c *ui.Context) {
			ui.Text(c, tloc(c).ClipTodayAt(fixed))
			ui.Text(c, stampIn(c.Locale(), c.Region(), fixed))
		}, 300, 100)
		view.SetLocale(locale)
		view.SetRegion(r)
		view.Frame()
		return view.Texts()
	}
	if texts := render("en", i18n.Region{HourCycle: 24}); !slices.Contains(texts, "Today 14:05") {
		t.Fatalf("24-hour English: %v", texts)
	}
	if texts := render("en", i18n.Region{HourCycle: 12}); !slices.Contains(texts, "Today 2:05 PM") {
		t.Fatalf("12-hour English: %v", texts)
	}
	if texts := render("zh-Hans", i18n.Region{HourCycle: 24}); !slices.Contains(texts, "今天 14:05") {
		t.Fatalf("24-hour Chinese: %v", texts)
	}
	// The clipboard row's screenshot title keeps the source language's
	// compact date, and takes the region's date and clock in English.
	if got := stampIn("zh-Hans", i18n.Region{}, fixed); got != "10-19 14:05" {
		t.Fatalf("Chinese stamp = %q", got)
	}
	if got := stampIn("en", i18n.Region{}, fixed); !strings.Contains(got, "2:05") {
		t.Fatalf("English stamp = %q", got)
	}
}

// TestEnglishBasketHasNoChinese: the drop card's three states say English.
func TestEnglishBasketHasNoChinese(t *testing.T) {
	s, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	a := &shelf{store: s, locale: "en"}
	view := ui.NewTester(a.basketView, basketWidth, basketHeight)
	view.SetLocale("en")
	view.Frame()
	assertNoHan(t, "basket", view.Texts())
	if !view.HasText("Drop Here") {
		t.Fatalf("basket prompt is missing; texts %v", view.Texts())
	}
	a.basketReceive(transfer.TextData("hello"))
	view.Frame()
	assertNoHan(t, "basket saved", view.Texts())
	if !view.HasText("Added to ShelfU") {
		t.Fatalf("basket confirmation is missing; texts %v", view.Texts())
	}
}

// TestEnglishRenameWindowHasNoChinese: every mode, row and preview of the
// rename window says English.
func TestEnglishRenameWindowHasNoChinese(t *testing.T) {
	a, view, _ := renameShelf(t, "Report.txt", "Notes.txt")
	a.locale = "en"
	view.SetLocale("en")
	r := openHeadlessRename(t, a)
	r.win = &mygo.Window{}
	for _, mode := range []int{0, 1, 2} {
		a.rn.mode = mode
		view.Frame()
		view.Frame()
		assertNoHan(t, fmt.Sprintf("rename mode %d", mode), view.Texts())
	}
	if !view.HasText("Rename") {
		t.Fatalf("the rename window is not English; texts %v", view.Texts())
	}
}

// TestLanguageSwitchAppliesWithoutRestart: the same view says Chinese and
// then English as the language changes, with no new window, and the native
// menus follow the shelf's language.
func TestLanguageSwitchAppliesWithoutRestart(t *testing.T) {
	s, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	if err := s.AddText("A note from the tray"); err != nil {
		t.Fatal(err)
	}
	a := &shelf{
		store: s, dataDir: t.TempDir(), visible: true, level: 1, settings: settings.Default(),
		thumbnails: map[string]*ui.Bitmap{}, thumbPending: map[string]bool{}, appNames: map[string]string{},
	}
	view := ui.NewTester(a.view, sheetW, sheetH)
	view.Frame()
	view.Frame()
	if !view.HasText("托盘 1 项") {
		t.Fatalf("the source language does not say the counts; texts %v", view.Texts())
	}
	view.SetLocale("en")
	view.Frame()
	view.Frame()
	if view.HasText("托盘 1 项") || !view.HasText(locales.In("en").PageTray()) {
		t.Fatalf("the view did not switch to English; texts %v", view.Texts())
	}
	view.SetLocale("zh-Hans")
	view.Frame()
	view.Frame()
	if !view.HasText("托盘 1 项") {
		t.Fatalf("the view did not switch back; texts %v", view.Texts())
	}

	// Native surfaces read the shelf's language, which OnLocaleChanged sets.
	a.locale = ""
	if labels := menuLabels(a.trayMenu()); !slices.Contains(labels, "显示 / 收起 ShelfU") {
		t.Fatalf("the tray menu is not in the source language: %v", labels)
	}
	a.localeChanged("en")
	if labels := menuLabels(a.trayMenu()); !slices.Contains(labels, "Show / Hide ShelfU") {
		t.Fatalf("the tray menu did not follow the language: %v", labels)
	}
	assertNoHan(t, "tray menu after switch", menuLabels(a.trayMenu()))
}

// TestStartupDialogsUseTheRule: the refusal of a launch that cannot open its
// data directory says the language settings.json names, or the system's by the
// same rule — and that path only reads settings.json, never creates or
// rewrites it (the directory it cannot open must stay as it was).
func TestStartupDialogsUseTheRule(t *testing.T) {
	orig := preferredLanguages
	t.Cleanup(func() { preferredLanguages = orig })
	preferredLanguages = func() []string { return []string{"fr-FR"} }

	dir := t.TempDir()
	path := filepath.Join(dir, settings.FileName)
	fr := locales.In("en")
	if got, want := startupLoc(dir).StartupAlreadyRunning(appName), fr.StartupAlreadyRunning(appName); got != want {
		t.Fatalf("a directory with no settings.json said %q, want the system's %q", got, want)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("the startup dialog path created settings.json: %v", err)
	}

	// settings.json names Chinese: shown as it is, on a French system too.
	prefs := settings.Default()
	prefs.Language = "zh-Hans"
	if err := settings.Save(dir, prefs); err != nil {
		t.Fatal(err)
	}
	zh := locales.In("zh-Hans")
	if got, want := startupLoc(dir).StartupAlreadyRunning(appName), zh.StartupAlreadyRunning(appName); got != want {
		t.Fatalf("settings.json's zh-Hans was not shown: %q, want %q", got, want)
	}
	// Read only: the file is still exactly what Save wrote.
	if data, err := os.ReadFile(path); err != nil {
		t.Fatal(err)
	} else if !strings.Contains(string(data), `"zh-Hans"`) {
		t.Fatalf("the startup path rewrote settings.json: %s", data)
	}

	// The migration that fails before the new directory has a settings.json
	// still reads the old one's: the user's language, not the system's.
	empty := t.TempDir()
	if got, want := startupLoc(empty, dir).StartupAlreadyRunning(appName), zh.StartupAlreadyRunning(appName); got != want {
		t.Fatalf("the old directory's settings.json was ignored: %q, want %q", got, want)
	}

	// A file that does not parse names no language: the system's rule again.
	if err := os.WriteFile(path, []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got, want := startupLoc(dir).StartupAlreadyRunning(appName), fr.StartupAlreadyRunning(appName); got != want {
		t.Fatalf("a broken settings.json said %q, want the system's %q", got, want)
	}
}

// pageTitleText is the back row's page name in the language of l, as the
// panel asks for it.
func pageTitleText(l locales.Messages, p panelPage) string {
	if t, ok := p.(pageText); ok {
		return t.titleIn(l)
	}
	return p.title()
}

// menuLabels returns every label of m and its submenus.
func menuLabels(m *mygo.Menu) []string {
	var out []string
	var walk func(items []*mygo.MenuItem)
	walk = func(items []*mygo.MenuItem) {
		for _, it := range items {
			out = append(out, it.Label)
			walk(it.Submenu)
		}
	}
	walk(m.Items())
	return out
}

// TestDropZoneFitsEveryVariant draws the real drop zone for every file kind a
// drag can carry — one image, several, one PDF, several, an archive, mixed
// files and plain text — in English and in the source language, and audits
// every action cell and the tray row with the panel's measuring seam: no name,
// subtitle or hint may need more width than its 94.5pt column gave it, so an
// ellipsis ("Pages as Ima…", "PNG · One per Pa…") can no longer pass as
// fitting. The Chinese strings are the ones the layout always had: the audit
// guards them too, and proves the English repair changed nothing there.
func TestDropZoneFitsEveryVariant(t *testing.T) {
	dir := t.TempDir()
	png1 := writeFile(t, dir, "one.png", testPNGBytes(t))
	png2 := writeFile(t, dir, "two.png", testPNGBytes(t))
	pdf1 := writeFile(t, dir, "one.pdf", []byte("%PDF-1.4"))
	pdf2 := writeFile(t, dir, "two.pdf", []byte("%PDF-1.4"))
	zip1 := writeFile(t, dir, "pack.zip", []byte("PK"))
	txt := writeFile(t, dir, "notes.txt", []byte("hello"))

	type variant struct {
		name  string
		paths []string
		en    []string // cell names and subtitles drawn in full
		zh    []string
	}
	variants := []variant{
		{"image", []string{png1},
			[]string{"Fit 1 MB", "or smaller", "Make PDF", "1 Page", "To JPEG", "One per File", "AirDrop"},
			[]string{"压缩到 1 MB", "以内", "转成 PDF", "1 页", "转 JPEG", "每张一份", "隔空投送"}},
		{"images", []string{png1, png2},
			[]string{"Merge into PDF", "In Drop Order", "To JPEG", "One per File"},
			[]string{"合成一个 PDF", "按拖入顺序", "转 JPEG", "每张一份"}},
		{"pdf", []string{pdf1},
			[]string{"To Word", "Extract Text", "To Images", "PNG · Per Page", "Zip", "AirDrop"},
			[]string{"转 Word", "提取文字", "每页存成图片", "PNG · 每页一张", "打成 zip", "隔空投送"}},
		{"pdfs", []string{pdf1, pdf2},
			[]string{"To Images", "PNG · Per Page", "2 PDFs"},
			[]string{"每页存成图片", "PNG · 每页一张", "2 个 PDF"}},
		{"archive", []string{zip1},
			[]string{"Extract", "AirDrop"},
			[]string{"解压", "隔空投送"}},
		{"mixed", []string{png1, txt},
			[]string{"Zip", "2 items", "AirDrop"},
			[]string{"打成 zip", "2 项", "隔空投送"}},
		{"text", []string{txt},
			[]string{"Zip"},
			[]string{"打成 zip"}},
	}
	for _, lang := range []struct {
		tag  string
		tray []string
	}{
		{"en", []string{"Put in Tray", "Stays Here — Drag Out Anytime"}},
		{"zh-Hans", []string{"放入托盘", "保存引用，随时拖出"}},
	} {
		for _, v := range variants {
			t.Run(lang.tag+"/"+v.name, func(t *testing.T) {
				drain := fitSink(t)
				a, view := dropViewShelf(t, v.paths)
				a.locale = lang.tag
				view.SetLocale(lang.tag)
				view.Frame()
				drain() // the first frame has no boxes to measure yet
				view.Frame()
				for _, note := range drain() {
					t.Errorf("%s: %s", v.name, note)
				}
				texts := view.Texts()
				want := v.en
				if lang.tag != "en" {
					want = v.zh
				}
				for _, w := range append(want, lang.tray...) {
					if !slices.Contains(texts, w) {
						t.Errorf("%s: %q is not drawn in full; texts %v", v.name, w, texts)
					}
				}
				if lang.tag == "en" {
					assertNoHan(t, "drop/"+v.name, texts)
				}
			})
		}
	}
}

// TestNumbersDoNotGroup formats every message that counts something with
// 1000 in both languages: no count, file name, page, byte or percentage may
// grow a thousands separator. The source language wrote counts with "%d",
// which never grouped, and the English catalog keeps the same rule so a
// generated name like "A 等 1000 张" cannot come back as "A 等 1,000 张".
// reflect walks the generated accessors, so a message added later is covered
// without editing this test.
func TestNumbersDoNotGroup(t *testing.T) {
	for _, lang := range []string{"zh-Hans", "en"} {
		l := locales.In(lang)
		v := reflect.ValueOf(l)
		covered := 0
		for i := 0; i < v.NumMethod(); i++ {
			m := v.Method(i)
			mt := m.Type()
			var args []reflect.Value
			hasInt, ok := false, true
			for j := 0; j < mt.NumIn(); j++ {
				switch mt.In(j).Kind() {
				case reflect.Int:
					hasInt = true
					args = append(args, reflect.ValueOf(1000).Convert(mt.In(j)))
				case reflect.String:
					args = append(args, reflect.ValueOf("A").Convert(mt.In(j)))
				default:
					ok = false
				}
			}
			if !ok || !hasInt || mt.NumOut() != 1 || mt.Out(0).Kind() != reflect.String {
				continue
			}
			covered++
			out := m.Call(args)[0].String()
			if strings.Contains(out, "1,000") {
				t.Errorf("%s: %s grouped a count: %q", lang, mt.String(), out)
			}
			if lang == "zh-Hans" && strings.Contains(out, ",") {
				t.Errorf("zh-Hans: %s wrote an ASCII comma for a count: %q", mt.String(), out)
			}
		}
		// The audit must cover the catalog's counting messages, not nothing.
		if covered < 25 {
			t.Fatalf("%s: only %d counting messages were checked", lang, covered)
		}
		t.Logf("%s: %d counting messages checked", lang, covered)
	}
}
