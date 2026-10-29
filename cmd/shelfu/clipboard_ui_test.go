//go:build darwin

package main

// Tests for the clipboard tab: filtering, search, pinned-first order, the
// pin and exclude-app menu items, and the ↑/↓/⏎ keyboard flow. They run
// headless through ui.Tester with a temporary store and data dir; copying
// goes to a test recorder instead of the system clipboard.

import (
	"bytes"
	"encoding/json"
	"errors"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"
	"time"

	"shelf/internal/settings"
	"shelf/internal/store"
)

// recordClip collects copies for assertions; shelf.writer picks it over
// the system clipboard.
type recordClip struct {
	text string
	png  []byte
}

func (r *recordClip) writeText(text string) error { r.text = text; return nil }
func (r *recordClip) writeImage(b []byte) error   { r.png = b; return nil }

// clipShelf returns a shelf showing texts as clipboard captures (newest
// first, matching store order) plus a headless view and the copy recorder.
func clipShelf(t *testing.T, texts ...string) (*shelf, *ui.Tester, *recordClip) {
	t.Helper()
	dir := t.TempDir()
	s, err := store.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	// 倒序加入，使 texts[0] 最终在列表最前（newest first）。
	for i := len(texts) - 1; i >= 0; i-- {
		if err := s.AddClipText(texts[i], "com.example.Source"); err != nil {
			t.Fatal(err)
		}
	}
	rec := &recordClip{}
	a := &shelf{
		store: s, dataDir: dir, visible: true, settings: settings.Default(),
		thumbnails: map[string]*ui.Bitmap{}, thumbPending: map[string]bool{},
		appNames: map[string]string{}, write: rec,
	}
	return a, ui.NewTester(a.view, sheetW, sheetH), rec
}

func clipTexts(a *shelf) []string {
	var out []string
	for _, it := range a.visibleItems() {
		out = append(out, it.Text)
	}
	return out
}

func TestClipboardExcluded(t *testing.T) {
	if clipboardExcluded("", []string{"com.example.App"}) {
		t.Error("an unknown source is never excluded")
	}
	if !clipboardExcluded("com.example.App", []string{"com.example.app"}) {
		t.Error("exclusion matches the bundle id without case")
	}
	if !clipboardExcluded("com.example.App", []string{"  com.example.App "}) {
		t.Error("exclusion ignores padding in the setting")
	}
	if clipboardExcluded("com.example.App", nil) || clipboardExcluded("com.example.Other", []string{"com.example.App"}) {
		t.Error("unlisted apps must not be excluded")
	}
}

func TestClipCaptureHonoursExclusions(t *testing.T) {
	types := []string{"public.utf8-plain-text"}
	if got := clipCapture(types, "com.example.App", []string{"com.example.app"}); got != clipSkip {
		t.Fatalf("excluded app still recorded: %v", got)
	}
	if got := clipCapture(types, "com.example.Other", []string{"com.example.app"}); got != clipText {
		t.Fatalf("unlisted app was skipped: %v", got)
	}
	if got := clipCapture([]string{"public.png"}, "", nil); got != clipImage {
		t.Fatalf("unknown source image was skipped: %v", got)
	}
}

// 剪贴板标签只显示 Source 为 clipboard 的条目；托盘标签排除未置顶的
// 剪贴板文字（它只属于剪贴板标签），但保留文件、图片与置顶文字。
func TestClipboardTabFiltersToCaptures(t *testing.T) {
	a, view, _ := clipShelf(t, "从剪贴板来的")
	if err := a.store.AddText("拖进来的文字"); err != nil {
		t.Fatal(err)
	}
	view.Frame()
	if !view.HasText("拖进来的文字") {
		t.Fatal("tray tab should show dropped text")
	}
	if view.HasText("从剪贴板来的") {
		t.Fatal("tray tab showed an unpinned clipboard capture")
	}
	a.setTab(pageClipboard)
	view.Frame()
	if view.HasText("拖进来的文字") {
		t.Fatal("clipboard tab showed a non-clipboard item")
	}
	if !view.HasText("从剪贴板来的") {
		t.Fatal("clipboard tab missing a capture")
	}
	if !view.HasText("搜索剪贴板") {
		t.Fatal("clipboard tab has no search field")
	}
}

// 搜索是大小写不敏感的子串匹配：只筛文字项，图片在非空查询下不显示，
// 清空搜索后恢复。
func TestClipboardSearchFiltersBySubstring(t *testing.T) {
	a, view, _ := clipShelf(t, "需要退回的购物清单", "Dentist on Tuesday")
	img := image.NewRGBA(image.Rect(0, 0, 4, 4))
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	if err := a.store.AddClipImage(buf.Bytes(), "com.apple.screenshot"); err != nil {
		t.Fatal(err)
	}
	a.setTab(pageClipboard)
	view.Frame()
	if len(a.visibleItems()) != 3 {
		t.Fatalf("want 3 clipboard rows, got %d", len(a.visibleItems()))
	}
	a.search = "dentist"
	view.Frame()
	if got := clipTexts(a); !slices.Equal(got, []string{"Dentist on Tuesday"}) {
		t.Fatalf("search by substring+case failed: %v", got)
	}
	if view.HasText("购物清单") {
		t.Fatal("search left a non-matching row")
	}
	a.search = "清单"
	view.Frame()
	if got := clipTexts(a); !slices.Equal(got, []string{"需要退回的购物清单"}) {
		t.Fatalf("CJK substring search failed: %v", got)
	}
	a.search = ""
	view.Frame()
	if len(a.visibleItems()) != 3 {
		t.Fatalf("empty search must restore images too, got %v", clipTexts(a))
	}
}

// 置顶行排在列表最前，带小图钉；取消置顶后回到原来的位置。
func TestClipboardPinnedFirstAndPinMenu(t *testing.T) {
	a, view, _ := clipShelf(t, "alpha top", "beta mid", "gamma bottom")
	a.setTab(pageClipboard)
	view.Frame()

	// Pin the last item; it must move to the top of the visible list.
	if err := view.RightClick("gamma bottom"); err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(view.Menu(), "置顶") {
		t.Fatalf("no pin item in %v", view.Menu())
	}
	if err := view.ChooseMenuItem("置顶"); err != nil {
		t.Fatal(err)
	}
	view.Frame()
	if got := clipTexts(a); !slices.Equal(got, []string{"gamma bottom", "alpha top", "beta mid"}) {
		t.Fatalf("pinned item did not come first: %v", got)
	}
	if !view.HasText("已置顶") {
		t.Fatal("pinned row shows no pin mark")
	}

	// Unpin it again from the context menu.
	if err := view.RightClick("gamma bottom"); err != nil {
		t.Fatal(err)
	}
	if err := view.ChooseMenuItem("取消置顶"); err != nil {
		t.Fatalf("unpin item: %v (%v)", err, view.Menu())
	}
	view.Frame()
	if got := clipTexts(a); !slices.Equal(got, []string{"alpha top", "beta mid", "gamma bottom"}) {
		t.Fatalf("unpinned item did not go back: %v", got)
	}
}

// 托盘页同样把置顶项排最前（同组内保持原顺序），⇧ 范围选择跟着显示顺序走；
// 取消置顶后回到原来的位置。
func TestTrayPinnedFirst(t *testing.T) {
	// 托盘标签排除未置顶的剪贴板文字，所以钉住那条剪贴板文字后它出现
	// 在托盘最前；拖入的两条文字保持其后。
	a, view, _ := clipShelf(t)
	for _, txt := range []string{"alpha top", "beta mid"} {
		if err := a.store.AddText(txt); err != nil {
			t.Fatal(err)
		}
	}
	if err := a.store.AddClipText("gamma bottom", "com.example.Source"); err != nil {
		t.Fatal(err)
	}
	view.Frame()
	if a.tab != pageTray {
		t.Fatal("expected the tray tab")
	}
	// Items are newest-first: beta mid, alpha top; the unpinned capture
	// stays off the tray.
	if got := clipTexts(a); !slices.Equal(got, []string{"beta mid", "alpha top"}) {
		t.Fatalf("unpinned clipboard text must stay off the tray: %v", got)
	}
	if err := a.store.SetPinned(a.store.Items[0].ID, true); err != nil {
		t.Fatal(err)
	}
	view.Frame()
	if got := clipTexts(a); !slices.Equal(got, []string{"gamma bottom", "beta mid", "alpha top"}) {
		t.Fatalf("pinned item did not come first on the tray tab: %v", got)
	}

	// ⇧-选择覆盖显示顺序里的整段：点击第一行再 ⇧ 点第三行 = 全部三行。
	if err := view.Click("gamma bottom"); err != nil {
		t.Fatal(err)
	}
	if err := view.ClickWith(ui.Shift, "alpha top"); err != nil {
		t.Fatal(err)
	}
	if n := a.selection.count(); n != 3 {
		t.Fatalf("⇧ range over the displayed order selected %d rows, want 3", n)
	}

	a.selection.clear()
	if err := view.RightClick("gamma bottom"); err != nil {
		t.Fatal(err)
	}
	if err := view.ChooseMenuItem("取消置顶"); err != nil {
		t.Fatalf("unpin item: %v (%v)", err, view.Menu())
	}
	view.Frame()
	// 取消置顶后剪贴板文字回到剪贴板标签，托盘又只剩拖入的两条。
	if got := clipTexts(a); !slices.Equal(got, []string{"beta mid", "alpha top"}) {
		t.Fatalf("unpinned capture did not leave the tray tab: %v", got)
	}
}

// 右键剪贴板条目可以把来源应用加入排除列表并写入 settings.json。
func TestExcludeAppMenuWritesSettings(t *testing.T) {
	a, view, _ := clipShelf(t, "from the recorder")
	a.setTab(pageClipboard)
	view.Frame()
	a.appNames["com.example.Source"] = "Recorder" // 模拟已解析的显示名
	if err := view.RightClick("from the recorder"); err != nil {
		t.Fatal(err)
	}
	want := "不再记录来自「Recorder」的内容"
	if !slices.Contains(view.Menu(), want) {
		t.Fatalf("no exclude item in %v", view.Menu())
	}
	if err := view.ChooseMenuItem(want); err != nil {
		t.Fatal(err)
	}
	prefs, err := settings.Load(a.dataDir)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(prefs.ExcludedApps, "com.example.Source") {
		t.Fatalf("settings.json lacks the excluded app: %+v", prefs.ExcludedApps)
	}
	// 菜单不再重复显示同一应用的条目。
	if err := view.RightClick("from the recorder"); err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(view.Menu(), want) {
		t.Fatalf("exclude item missing on second open: %v", view.Menu())
	}
	view.CloseMenu()
	// 已排除后来源仍显示同样的标签（选择器按位置+标签匹配）；真正跳过
	// 由 clipCapture 负责。
	a.settingsBroken = true
	if err := view.RightClick("from the recorder"); err != nil {
		t.Fatal(err)
	}
	if err := view.ChooseMenuItem(want); err == nil {
		t.Fatal("a broken settings file must disable the exclude item")
	}
	view.CloseMenu()
}

// ↑/↓ 在剪贴板标签移动选中行，⏎ 把它复制到剪贴板并收起托盘。
func TestClipboardKeyboardFlow(t *testing.T) {
	a, view, rec := clipShelf(t, "first clip", "second clip", "third clip")
	a.setTab(pageClipboard)
	a.focusMode = true
	view.Frame()

	if !view.Focused(searchLabel) {
		t.Fatal("search field did not take the keyboard")
	}
	view.Key(0, ui.KeyDown)
	if a.selection.anchor != a.visibleItems()[0].ID {
		t.Fatal("↓ did not select the first row")
	}
	view.Key(0, ui.KeyDown)
	if got, want := a.selection.anchor, a.visibleItems()[1].ID; got != want {
		t.Fatalf("second ↓ landed on %q, want %q", got, want)
	}
	view.Key(0, ui.KeyUp)
	if got, want := a.selection.anchor, a.visibleItems()[0].ID; got != want {
		t.Fatalf("↑ landed on %q, want %q", got, want)
	}
	view.Key(0, ui.KeyEnter)
	if rec.text != "first clip" {
		t.Fatalf("⏎ copied %q, want %q", rec.text, "first clip")
	}
	if a.visible {
		t.Fatal("⏎ did not collapse the shelf")
	}
	view.Advance(time.Second)
	view.Frame()
	if a.tab != pageOverview || a.search != "" {
		t.Fatal("⏎ did not settle back to the overview after the fade")
	}
}

// 搜索框里的 Esc 先清空搜索，再收一次才收起托盘。
func TestEscapeClearsSearchBeforeHiding(t *testing.T) {
	a, view, _ := clipShelf(t, "find me")
	a.setTab(pageClipboard)
	a.focusMode = true
	a.search = "find"
	view.Frame()

	view.Key(0, ui.KeyEscape)
	if !a.visible {
		t.Fatal("Esc hid the shelf while the search still held text")
	}
	if a.search != "" {
		t.Fatal("Esc did not clear the search")
	}
	if !view.Focused(searchLabel) {
		t.Fatal("clearing the search lost the field's focus")
	}
	// The second Esc leaves the search's keyboard mode and backs out to the
	// overview, like every other page; the third one closes.
	view.Key(0, ui.KeyEscape)
	if !a.visible || a.currentLevel() != 1 || a.focusMode {
		t.Fatalf("second Esc: visible=%v level=%d focus=%v, want the overview", a.visible, a.currentLevel(), a.focusMode)
	}
	view.Key(0, ui.KeyEscape)
	if a.visible {
		t.Fatal("a third Esc did not collapse the shelf")
	}
	// 收起会重置回托盘标签；捕获本身仍在存储里。
	if a.store == nil || len(a.store.Items) != 1 || a.store.Items[0].Text != "find me" {
		t.Fatal("clearing the search dropped the capture from the store")
	}
}

// 切换到剪贴板标签会把不在其中的选择丢掉；⇧ 选择范围跟着可见列表走。
func TestSelectionFollowsTheVisibleList(t *testing.T) {
	a, view, _ := clipShelf(t, "clip a", "clip b")
	if err := a.store.AddText("tray only"); err != nil {
		t.Fatal(err)
	}
	view.Frame()
	if err := view.Click("tray only"); err != nil {
		t.Fatal(err)
	}
	if a.selection.count() != 1 {
		t.Fatal("click did not select")
	}
	a.setTab(pageClipboard)
	view.Frame()
	if a.selection.count() != 0 {
		t.Fatal("the tray selection survived into the clipboard tab")
	}
	// ⇧ on the clipboard tab spans the visible (pinned-first) order.
	if err := view.Click("clip a"); err != nil {
		t.Fatal(err)
	}
	if err := view.ClickWith(ui.Shift, "clip b"); err != nil {
		t.Fatal(err)
	}
	if a.selection.count() != 2 {
		t.Fatalf("⇧ over the visible list selected %d, want 2", a.selection.count())
	}
}

// 剪贴板图片可以经 ⏎ 以 PNG 形式复制。
func TestEnterCopiesImageAsPNG(t *testing.T) {
	a, _, _ := clipShelf(t)
	img := image.NewRGBA(image.Rect(0, 0, 4, 4))
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	if err := a.store.AddClipImage(buf.Bytes(), "com.apple.screenshot"); err != nil {
		t.Fatal(err)
	}
	a.setTab(pageClipboard)
	a.selection.only(a.visibleItems()[0].ID)
	a.copyClipSelection()
	got := a.write.(*recordClip).png
	if len(got) == 0 {
		t.Fatal("⏎ on an image wrote no PNG")
	}
	if _, err := png.Decode(bytes.NewReader(got)); err != nil {
		t.Fatalf("⏎ on an image did not write a PNG: %v", err)
	}
	if a.visible {
		t.Fatal("copying an image did not collapse the shelf")
	}
}

// hide() 的键盘模式清理：内容淡出（120 ms）后回到总览并交还焦点标记。
func TestHideLeavesKeyboardMode(t *testing.T) {
	a, view, _ := clipShelf(t, "x")
	a.tab, a.search, a.focusMode, a.prevAppPID = pageClipboard, "q", true, 999999
	a.hide(hideExplicit)
	view.Advance(time.Second)
	view.Frame()
	if a.tab != pageOverview || a.search != "" || a.focusMode || a.prevAppPID != 0 {
		t.Fatalf("hide left keyboard state: tab=%s search=%q focus=%v", a.tab, a.search, a.focusMode)
	}
}

// 菜单的「保留 N 条」写进设置并立即生效。
func TestClipboardMenuSettings(t *testing.T) {
	a, _, _ := clipShelf(t)
	a.setClipboardLimit(100)
	if a.settings.ClipboardLimit != 100 {
		t.Fatal("limit not saved")
	}
	prefs, err := settings.Load(a.dataDir)
	if err != nil {
		t.Fatal(err)
	}
	if prefs.ClipboardLimit != 100 {
		t.Fatalf("settings.json kept %d, want 100", prefs.ClipboardLimit)
	}
	a.settingsBroken = true
	a.setClipboardLimit(300)
	if a.settings.ClipboardLimit != 100 {
		t.Fatal("a broken settings file must lock the retained count")
	}
}

// 交还焦点只发生在用户明确的收起动作；点外面、拖出成功只忘掉原应用。
func TestShouldRestoreApp(t *testing.T) {
	a := &shelf{focusMode: true}
	if !a.shouldRestoreApp(hideExplicit) {
		t.Fatal("explicit hide in keyboard mode must restore the previous app")
	}
	if a.shouldRestoreApp(hidePassive) {
		t.Fatal("a click outside or drag-out must not steal the user back")
	}
	a.focusMode = false
	if a.shouldRestoreApp(hideExplicit) {
		t.Fatal("a shelf that never took the keyboard restores nothing")
	}
}

// 被动收起也忘掉原应用，且不留下键盘状态。
func TestPassiveHideForgetsPreviousApp(t *testing.T) {
	a, _, _ := clipShelf(t, "x")
	a.focusMode, a.prevAppPID = true, 999999
	a.hide(hidePassive)
	if a.prevAppPID != 0 {
		t.Fatal("passive hide must still forget the remembered app")
	}
}

// 剪贴板页的清空保留置顶项；选中剪到仍在的行，被清掉的缩略图作废。
func TestClearKeepsPinnedItems(t *testing.T) {
	a, view, _ := clipShelf(t, "plain clip", "pinned clip")
	if err := a.store.SetPinned(a.store.Items[1].ID, true); err != nil {
		t.Fatal(err)
	}
	a.tab = pageClipboard
	gone := a.store.Items[0].ID
	a.thumbnails[gone] = &ui.Bitmap{}
	a.selection.only(gone) // 选中将被清掉的那行
	view.Frame()
	if err := view.Click("清空"); err != nil {
		t.Fatalf("click clear: %v", err)
	}
	view.Frame()
	if len(a.store.Items) != 1 || !a.store.Items[0].Pinned {
		t.Fatalf("clear removed the pinned item: %+v", a.store.Items)
	}
	if a.selection.count() != 0 {
		t.Fatal("selection kept an item that no longer exists")
	}
	if _, ok := a.thumbnails[gone]; ok {
		t.Fatal("stale thumbnail survived clear")
	}
	if !view.HasText("pinned clip") || view.HasText("plain clip") {
		t.Fatal("visible list did not refresh after clear")
	}
}

// 复制写不进剪贴板时托盘不收起，让用户看到失败提示。
func TestCopyFailureKeepsShelfOpen(t *testing.T) {
	a, view, _ := clipShelf(t, "won't paste")
	a.write = failClip{}
	a.tab = pageClipboard
	a.focusMode = true
	a.selection.only(a.store.Items[0].ID)
	view.Frame()
	a.copyClipSelection()
	if !a.visible {
		t.Fatal("shelf hid although the copy failed")
	}
	if !strings.Contains(a.status, "录音笔") && a.status == "" {
		t.Fatal("failed copy reported nothing")
	}
}

type failClip struct{}

func (failClip) writeText(string) error  { return errors.New("pasteboard full") }
func (failClip) writeImage([]byte) error { return errors.New("pasteboard full") }

// ShelfU 自己在前台（键盘搜索开着）时，复制不记来源。
func TestCaptureSourceSkipsShelfItself(t *testing.T) {
	if got := captureSource(42, 42, "local.y3u.shelf"); got != "" {
		t.Fatalf("ShelfU frontmost recorded a source: %q", got)
	}
	if got := captureSource(10, 42, "com.example.App"); got != "com.example.App" {
		t.Fatalf("real source dropped: %q", got)
	}
}

type copyEventReader struct {
	serial  int64
	formats []string
	app     string
	value   string
	png     []byte
}

func (b *copyEventReader) changeCount() int64 { return b.serial }
func (b *copyEventReader) sourceApp() string  { return b.app }
func (b *copyEventReader) types() []string    { return b.formats }
func (b *copyEventReader) text() string       { return b.value }
func (b *copyEventReader) image() []byte      { return b.png }

func TestLegacyDisabledClipboardStillRecordsAndDropsOldKey(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, settings.FileName)
	if err := os.WriteFile(path, []byte(`{"clipboardHistory":false,"clipboardLimit":100,"excludedApps":["com.example.Blocked"]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	prefs, err := settings.Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	a := &shelf{store: mustStore(t), dataDir: dir, settings: prefs}
	a.applyClipboardHistory()
	if a.clip == nil {
		t.Fatal("the legacy false setting prevented clipboard recording from starting")
	}
	board := &copyEventReader{serial: a.clip.count, app: "com.example.Source"}
	a.clip.board = board
	copyNow := func() {
		board.serial++
		a.clip.tick(time.Now())
	}
	board.formats, board.value = []string{"public.utf8-plain-text"}, "Captured despite the legacy false setting"
	copyNow()
	if !slices.ContainsFunc(a.store.Items, func(it store.Item) bool { return it.Text == board.value && it.SourceApp == board.app }) {
		t.Fatal("a new text copy was not recorded")
	}
	board.formats, board.png = []string{"public.png"}, testPNGBytes(t)
	copyNow()
	if !slices.ContainsFunc(a.store.Items, func(it store.Item) bool { return it.Kind == store.KindImage && it.SourceApp == board.app }) {
		t.Fatal("a new clipboard image was not recorded")
	}
	board.formats, board.value = []string{"org.nspasteboard.ConcealedType", "public.utf8-plain-text"}, "private fixture"
	copyNow()
	board.formats, board.app = []string{"public.utf8-plain-text"}, "com.example.Blocked"
	copyNow()
	if len(a.store.Items) != 2 {
		t.Fatal("private or excluded-app copies were recorded")
	}
	if err := settings.Save(dir, a.settings); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var saved map[string]json.RawMessage
	if err := json.Unmarshal(raw, &saved); err != nil {
		t.Fatal(err)
	}
	if _, exists := saved["clipboardHistory"]; exists {
		t.Fatal("saving settings retained the obsolete clipboardHistory key")
	}
	again, err := settings.Load(dir)
	if err != nil || again.ClipboardLimit != 100 || !slices.Equal(again.ExcludedApps, []string{"com.example.Blocked"}) {
		t.Fatalf("saving dropped retained clipboard preferences: %+v, %v", again, err)
	}
}

// clipModuleShelf drives a clipboardModule from copyEventReader on a shelf
// with an empty temporary store. Changing the reader between ticks models
// macOS publishing a change's types and bytes after its changeCount bump.
func clipModuleShelf(t *testing.T) (*shelf, *clipboardModule, *copyEventReader) {
	t.Helper()
	a, _, _ := clipShelf(t)
	board := &copyEventReader{app: "com.example.Source"}
	m := &clipboardModule{host: a, board: board, count: board.changeCount()}
	a.clip = m
	return a, m, board
}

// 截图等复制会在 changeCount 跳变后 20-54ms 才公布类型和字节：落在间隙里的
// tick 必须等后续 tick 再读，并记在这次变化发生瞬间的前台应用名下。
func TestClipboardTickWaitsForLateRepresentations(t *testing.T) {
	a, m, board := clipModuleShelf(t)
	now := time.Unix(1_700_000_000, 0)
	board.app = "com.example.Screenshots"
	board.serial++
	m.tick(now)
	if len(a.store.Items) != 0 {
		t.Fatal("recorded a change whose types had not been published")
	}
	now = now.Add(30 * time.Millisecond)
	m.tick(now) // still unpublished
	if len(a.store.Items) != 0 {
		t.Fatal("recorded a change without types")
	}
	// The types and PNG bytes arrive without another bump, and the frontmost
	// app has changed meanwhile: the copy still belongs to the bump's app.
	board.formats, board.png = []string{"public.png"}, testPNGBytes(t)
	board.app = "com.example.Other"
	now = now.Add(24 * time.Millisecond)
	m.tick(now)
	items := a.store.Items
	if len(items) != 1 || items[0].Kind != store.KindImage || items[0].SourceApp != "com.example.Screenshots" {
		t.Fatalf("late screenshot not recorded once with its own source: %+v", items)
	}
	if m.pending != nil {
		t.Fatal("pending change survived being recorded")
	}
}

// 内容一直不出现时，等待到期就放弃这条变化；之后真正的复制照常记录。
func TestClipboardTickDropsUnreadableChangeAfterDeadline(t *testing.T) {
	a, m, board := clipModuleShelf(t)
	now := time.Unix(1_700_000_000, 0)
	board.serial++
	m.tick(now)
	if m.pending == nil {
		t.Fatal("an unreadable change was not kept pending")
	}
	now = now.Add(clipPublishWait - 30*time.Millisecond)
	m.tick(now)
	if m.pending == nil {
		t.Fatal("gave up before the wait ran out")
	}
	now = now.Add(30 * time.Millisecond)
	m.tick(now)
	if m.pending != nil {
		t.Fatal("pending change outlived the deadline")
	}
	board.serial++
	board.formats, board.value, board.app = []string{"public.utf8-plain-text"}, "later real copy", "com.example.Editor"
	m.tick(now.Add(30 * time.Millisecond))
	if len(a.store.Items) != 1 || a.store.Items[0].Text != "later real copy" || a.store.Items[0].SourceApp != "com.example.Editor" {
		t.Fatalf("a real copy after the drop was not recorded: %+v", a.store.Items)
	}
}

// 隐藏/临时类型当场跳过，不进入等待：同一 changeCount 之后出现的文字也不收。
func TestClipboardPrivateTypesHandledWithoutWaiting(t *testing.T) {
	a, m, board := clipModuleShelf(t)
	now := time.Unix(1_700_000_000, 0)
	board.formats, board.value = []string{"org.nspasteboard.ConcealedType", "public.utf8-plain-text"}, "private fixture"
	board.serial++
	m.tick(now)
	if m.pending != nil {
		t.Fatal("a concealed copy was kept pending")
	}
	board.formats = []string{"public.utf8-plain-text"}
	m.tick(now.Add(30 * time.Millisecond))
	if len(a.store.Items) != 0 {
		t.Fatalf("private copy recorded: %+v", a.store.Items)
	}
}

// 等待期间 changeCount 再次跳变：丢掉旧变化，只收新的那份，来源重新取。
func TestClipboardNewerChangeSupersedesPending(t *testing.T) {
	a, m, board := clipModuleShelf(t)
	now := time.Unix(1_700_000_000, 0)
	board.app = "com.example.First"
	board.serial++
	m.tick(now)
	board.serial++
	board.app = "com.example.Second"
	board.formats, board.value = []string{"public.utf8-plain-text"}, "second copy"
	m.tick(now.Add(30 * time.Millisecond))
	items := a.store.Items
	if len(items) != 1 || items[0].Text != "second copy" || items[0].SourceApp != "com.example.Second" {
		t.Fatalf("newer change not recorded alone: %+v", items)
	}
	if m.pending != nil {
		t.Fatal("pending change survived a newer change")
	}
}

// skipCurrent 连等待中的变化一起清掉：ShelfU 自己写剪贴板后不再回头收它。
func TestClipboardSkipCurrentClearsPending(t *testing.T) {
	a, m, board := clipModuleShelf(t)
	now := time.Unix(1_700_000_000, 0)
	board.serial++
	m.tick(now)
	if m.pending == nil {
		t.Fatal("an unreadable change was not kept pending")
	}
	m.skipCurrent()
	if m.pending != nil {
		t.Fatal("skipCurrent left the pending change behind")
	}
	board.formats, board.png = []string{"public.png"}, testPNGBytes(t)
	m.tick(now.Add(30 * time.Millisecond))
	if len(a.store.Items) != 0 {
		t.Fatalf("a skipped change was recorded: %+v", a.store.Items)
	}
}
