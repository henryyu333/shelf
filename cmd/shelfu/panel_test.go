//go:build darwin

package main

import (
	"bytes"
	"fmt"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/transfer"
	"github.com/egoist/mygo/ui"

	"shelf/internal/mac"
	"shelf/internal/store"
)

func TestLongTextPreviewKeepsFullPayload(t *testing.T) {
	text := "开头 😀\n\t" + strings.Repeat("正文🙂 ", 400) + "不可丢失的末尾"
	s, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err := s.AddText(text); err != nil {
		t.Fatal(err)
	}
	a := &shelf{store: s}
	view := ui.NewTester(a.view, sheetW, sheetH)
	if !view.HasText("开头 😀") {
		t.Fatal("text preview lost its readable beginning")
	}
	for _, label := range view.Texts() {
		if !utf8.ValidString(label) {
			t.Fatal("text preview split a UTF-8 character")
		}
		if utf8.RuneCountInString(label) > 200 {
			t.Fatal("long clipboard text was exposed to layout instead of a bounded preview")
		}
		if strings.Contains(label, "不可丢失的末尾") {
			t.Fatal("preview rendered the distant tail of the full text")
		}
	}
	payload, err := a.dragData(s.Items[0]).Read(transfer.Text)
	if err != nil {
		t.Fatal(err)
	}
	if string(payload) != text || s.Items[0].Text != text {
		t.Fatal("shortening the preview truncated the stored or exported text")
	}
}

// 有内容时头部出现"清空"，每项仍保留独立移除按钮；单项移除只删该项，
// 清空则删除全部、丢掉缩略图缓存、回到空态且不再显示"清空"。
func TestClearAllButtonAndPerItemRemove(t *testing.T) {
	s, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	dir := t.TempDir()
	note := filepath.Join(dir, "note.txt")
	if err := os.WriteFile(note, []byte("hi"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := s.AddFiles([]string{note}); err != nil {
		t.Fatal(err)
	}
	if err := s.AddText("一段文字"); err != nil {
		t.Fatal(err)
	}
	img := image.NewRGBA(image.Rect(0, 0, 4, 4))
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	if err := s.AddImage(buf.Bytes()); err != nil {
		t.Fatal(err)
	}
	a := &shelf{store: s, thumbnails: map[string]*ui.Bitmap{}, thumbPending: map[string]bool{}}
	view := ui.NewTester(a.view, sheetW, sheetH)
	if !view.HasText("清空") {
		t.Fatal("clear-all button missing with items present")
	}
	// 移除入口在右键菜单里（设计去掉了行内按钮）。
	for _, it := range s.Items {
		title := itemTitle(it)
		if err := view.RightClick(title); err != nil {
			t.Fatalf("right-click %q: %v", title, err)
		}
		if !slices.Contains(view.Menu(), "移除") {
			t.Fatalf("per-item menu for %q lacks 移除: %v", title, view.Menu())
		}
		view.CloseMenu()
	}
	// 清空必须作废已经在途的缩略图；无窗口渲染不会启动后台解码。
	for _, it := range s.Items {
		if it.Kind != store.KindText {
			a.thumbPending[it.ID] = true
		}
	}

	if err := view.RightClick("note.txt"); err != nil {
		t.Fatalf("right-click note.txt: %v", err)
	}
	if err := view.ChooseMenuItem("移除"); err != nil {
		t.Fatalf("choose per-item remove: %v (%v)", err, view.Menu())
	}
	if len(s.Items) != 2 {
		t.Fatalf("per-item remove should keep the rest, got %d items", len(s.Items))
	}
	for _, it := range s.Items {
		if it.Kind == store.KindFile {
			t.Fatal("per-item remove deleted the wrong item")
		}
	}
	if _, err := os.Stat(note); err != nil {
		t.Fatalf("original file deleted by remove: %v", err)
	}
	if !view.HasText("清空") {
		t.Fatal("clear-all button disappeared while items remain")
	}

	if err := view.Click("清空"); err != nil {
		t.Fatalf("click clear-all: %v", err)
	}
	if len(s.Items) != 0 {
		t.Fatalf("clear-all left %d items", len(s.Items))
	}
	if !view.HasText("把文件拖到这里") {
		t.Fatal("empty state not shown after clear-all")
	}
	if view.HasText("清空") {
		t.Fatal("clear-all button still rendered on empty shelf")
	}
	for id := range a.thumbPending {
		if !a.thumbDiscard[id] {
			t.Fatalf("in-flight thumbnail %s not marked for discard", id)
		}
	}
	if _, err := os.Stat(note); err != nil {
		t.Fatalf("original file deleted by clear-all: %v", err)
	}
}

// Each page's 清空 removes only what that page shows: clearing the tray
// keeps the clipboard history, and a capture that lives on both pages is
// only hidden from the cleared page — the other page keeps it, together
// with its managed PNG, until that page's own 清空 deletes it. Pinned rows
// stay.
func TestClearOnlyEmptiesItsOwnPage(t *testing.T) {
	s, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	dir := t.TempDir()
	note := filepath.Join(dir, "note.txt")
	if err := os.WriteFile(note, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := s.AddFiles([]string{note}); err != nil {
		t.Fatal(err)
	}
	if err := s.AddClipText("复制的文字", ""); err != nil {
		t.Fatal(err)
	}
	if err := s.AddClipText("置顶的文字", ""); err != nil {
		t.Fatal(err)
	}
	if err := s.AddClipImage(testPNG(t), "com.example.Shot"); err != nil {
		t.Fatal(err)
	}
	// newest-first: image, pinned text, unpinned text, file.
	clipImg := s.Items[0]
	var pinned string
	for _, it := range s.Items {
		if it.Text == "置顶的文字" {
			pinned = it.ID
		}
	}
	if clipImg.Kind != store.KindImage || pinned == "" {
		t.Fatalf("fixture: %+v", s.Items)
	}
	managed := clipImg.Path
	if err := s.SetPinned(pinned, true); err != nil {
		t.Fatal(err)
	}
	a := &shelf{store: s, thumbnails: map[string]*ui.Bitmap{}, thumbPending: map[string]bool{}}
	view := ui.NewTester(a.view, sheetW, sheetH)

	// 托盘页的清空：拖入的文件真删（原文件不动），剪贴板收录只移出托盘。
	if err := view.Click("清空"); err != nil {
		t.Fatalf("tray clear: %v", err)
	}
	if len(s.Items) != 3 {
		t.Fatalf("the tray's 清空 must keep the 3 clipboard captures, got %+v", s.Items)
	}
	for _, it := range s.Items {
		if it.Kind == store.KindFile {
			t.Fatal("the tray's 清空 kept the file item")
		}
		if it.ID == clipImg.ID && !it.OffTray {
			t.Fatalf("tray clear did not hide the capture: %+v", it)
		}
	}
	for _, want := range []string{"复制的文字", "置顶的文字"} {
		found := false
		for _, it := range s.Items {
			found = found || it.Text == want
		}
		if !found {
			t.Fatalf("the tray's 清空 dropped the clipboard text %q: %+v", want, s.Items)
		}
	}
	if _, err := os.Stat(note); err != nil {
		t.Fatalf("original file deleted by tray clear: %v", err)
	}
	if _, err := os.Stat(managed); err != nil {
		t.Fatalf("managed png deleted by tray clear: %v", err)
	}

	// 剪贴板页：截图仍列出（它只是离开托盘）；这一页的清空才真删。
	a.setTab(pageClipboard)
	view.Frame()
	if !view.HasText(itemTitle(clipImg)) {
		t.Fatalf("the capture hidden from the tray is missing from the clipboard page; texts %v", view.Texts())
	}
	a.search = "复制"
	view.Frame()
	if view.HasText("清空") {
		t.Fatal("a narrowed clipboard search must not offer 清空")
	}
	a.search = ""
	view.Frame()
	if err := view.Click("清空"); err != nil {
		t.Fatalf("clipboard clear: %v", err)
	}
	// 截图在托盘页已被移出，两页都不再可见，只能真删；置顶的行留下。
	if len(s.Items) != 1 || s.Items[0].ID != pinned {
		t.Fatalf("the clipboard's 清空 must keep only the pinned row, left %+v", s.Items)
	}
	if _, err := os.Stat(managed); !os.IsNotExist(err) {
		t.Fatalf("managed png should be deleted with a capture off both pages: %v", err)
	}
	if _, err := os.Stat(note); err != nil {
		t.Fatalf("original file deleted by clipboard clear: %v", err)
	}
}

// 剪贴板页的清空只把两页都在的截图移出剪贴板页：它仍在托盘、受管 PNG 不删；
// 托盘页随后的清空才真正删除（连图片）。重新复制同一张图又回到两页。
func TestClipboardClearHidesCaptureFromClipboardPageOnly(t *testing.T) {
	s, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	pngA := testPNG(t)
	if err := s.AddClipImage(pngA, "com.example.Shot"); err != nil {
		t.Fatal(err)
	}
	clip := s.Items[0]
	managed := clip.Path
	a := &shelf{store: s, thumbnails: map[string]*ui.Bitmap{}, thumbPending: map[string]bool{}}
	view := ui.NewTester(a.view, sheetW, sheetH)

	a.setTab(pageClipboard)
	view.Frame()
	if err := view.Click("清空"); err != nil {
		t.Fatalf("clipboard clear: %v", err)
	}
	if len(s.Items) != 1 || s.Items[0].ID != clip.ID || !s.Items[0].OffClip {
		t.Fatalf("the clipboard's 清空 must only hide the capture from its page: %+v", s.Items)
	}
	if _, err := os.Stat(managed); err != nil {
		t.Fatalf("managed png deleted by clipboard clear: %v", err)
	}
	view.Frame()
	if view.HasText(itemTitle(clip)) {
		t.Fatal("the clipboard page still lists the capture it cleared")
	}
	a.setTab(pageTray)
	view.Frame()
	if !view.HasText(itemTitle(clip)) {
		t.Fatalf("the tray lost the capture hidden from the clipboard page; texts %v", view.Texts())
	}

	// 重新复制同一张图：去重命中，两页都回来。
	//
	// 去重会把这条提到最前并刷新 CreatedAt（store.moveToFront），截图标题
	// 精确到秒，所以重新收录后标题可能换一个；这里先把时间往回挪 90 秒，
	// 让“刷新前后的标题不同”每次都发生，断言也必须跟着重新取这条，
	// 否则测试只是在赌两秒落在同一秒里。
	s.Items[0].CreatedAt = time.Now().Add(-90 * time.Second)
	clip = s.Items[0]
	view.Frame()
	if err := s.AddClipImage(pngA, "com.example.Shot"); err != nil {
		t.Fatal(err)
	}
	if len(s.Items) != 1 || s.Items[0].ID != clip.ID {
		t.Fatalf("re-copy must dedupe onto the same item, not add one: %+v", s.Items)
	}
	// 这一帧画的是刷新后的标题：从 store 重新取，而不是用刷新前的副本。
	shown := s.Items[0]
	if shown.CreatedAt.Equal(clip.CreatedAt) {
		t.Fatalf("the re-copy did not refresh CreatedAt: %v", shown.CreatedAt)
	}
	view.Frame()
	if !view.HasText(itemTitle(shown)) {
		t.Fatalf("re-copy did not bring the capture back to the tray: %+v; texts %v", shown, view.Texts())
	}
	a.setTab(pageClipboard)
	view.Frame()
	if !view.HasText(itemTitle(s.Items[0])) {
		t.Fatalf("re-copy did not bring the capture back to the clipboard page; texts %v", view.Texts())
	}

	// 再从剪贴板页清空，然后托盘页清空：这回真的删除，图片一起没了。
	if err := view.Click("清空"); err != nil {
		t.Fatalf("second clipboard clear: %v", err)
	}
	if !s.Items[0].OffClip {
		t.Fatalf("second clipboard clear did not hide the capture: %+v", s.Items[0])
	}
	a.setTab(pageTray)
	view.Frame()
	if err := view.Click("清空"); err != nil {
		t.Fatalf("tray clear: %v", err)
	}
	if len(s.Items) != 0 {
		t.Fatalf("the tray's clear must delete a capture hidden from both pages: %+v", s.Items)
	}
	if _, err := os.Stat(managed); !os.IsNotExist(err) {
		t.Fatalf("managed png should be deleted with the item: %v", err)
	}
}

// On a notched display the ear bar sits in the notch band beside the camera
// housing — never over it — and the page content starts below the notch.
// While the clipboard search holds the keyboard the window drops under the
// menu bar, so the bar moves below it.
func TestBandFlanksTheNotch(t *testing.T) {
	s, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err := s.AddText("有内容"); err != nil {
		t.Fatal(err)
	}
	const notchW, notchH = 180, 32
	a := &shelf{store: s, level: 2, topInset: notchH, notchW: notchW, notchH: notchH, thumbnails: map[string]*ui.Bitmap{}, thumbPending: map[string]bool{}}
	view := ui.NewTester(a.view, sheetW, sheetH)
	housingL := float32(sheetW-notchW) / 2
	housingR := housingL + notchW
	for _, label := range []string{"设置", "总览", "托盘", "剪贴板", "工具"} {
		r, ok := view.Find(label)
		if !ok {
			t.Fatalf("band entry %q missing", label)
		}
		if r.Y+r.H > notchH {
			t.Fatalf("band entry %q at %+v leaves the notch band", label, r)
		}
		if r.X+r.W > housingL && r.X < housingR {
			t.Fatalf("band entry %q at %+v sits under the camera housing [%v, %v]", label, r, housingL, housingR)
		}
	}
	if r, ok := view.Find("清空"); !ok || r.Y < notchH {
		t.Fatalf("the page's 清空 must sit below the notch: %+v %v", r, ok)
	}
	// The page icons live in the band, beside the housing.
	if err := view.Click("剪贴板"); err != nil || a.tab != pageClipboard {
		t.Fatalf("the band's page icon must switch pages: %v tab=%s", err, a.tab)
	}
	a.focusMode = true
	view.Frame()
	if r, ok := view.Find("设置"); !ok || r.Y < notchH+headerTopGap {
		t.Fatalf("with the keyboard taken, 设置 must move below the menu bar: %+v %v", r, ok)
	}
}

// The sheet's left ear holds the first chipMax live modules; the island
// has no activity chips at all (its mini player carries the track).
func TestActivityChipsShowUpToTwoLiveModules(t *testing.T) {
	idle := &fakeActivity{label: "空闲"}
	first := &fakeActivity{label: "第一个活动", isLive: true}
	second := &fakeActivity{label: "第二个活动", isLive: true}
	third := &fakeActivity{label: "第三个活动", isLive: true}
	a := &shelf{tab: pageTray, level: 2}
	a.modules = []module{idle, first, second, third}
	view := ui.NewTester(a.view, sheetW, sheetH)
	if view.HasText("空闲") || view.HasText("第三个活动") {
		t.Fatalf("an idle activity and a third live one must not show")
	}
	if !view.HasText("第一个活动") || !view.HasText("第二个活动") {
		t.Fatalf("the first two live activities must show")
	}
	a.level = 1
	view = ui.NewTester(a.view, sheetW, sheetH)
	view.Frame()
	if view.HasText("第一个活动") || view.HasText("第二个活动") {
		t.Fatal("the overview shows the band chips; its context line carries the job")
	}
}

type fakeActivity struct {
	label  string
	isLive bool
}

func (f *fakeActivity) tick(time.Time) {}
func (f *fakeActivity) live() bool     { return f.isLive }

func (f *fakeActivity) chip(c *ui.Context) ui.Element {
	box := ui.Row(c.Key("chip-"+f.label)).Height(chipH).Padding(0, 6, 0, 6).Radius(chipH / 2).
		Background(fill1).AlignItems(ui.Center)
	box.Children(func() {
		ui.Text(c, f.label).FontSize(11).TextColor(ink)
	})
	return box
}

// Resting on an ear icon shows its name in a tip the user can read: text
// that stands out from the tip's own fill (regression: the panel theme's
// transparent background left the text invisible on a blank white chip).
func TestEarIconTooltipIsReadable(t *testing.T) {
	a := &shelf{topInset: 32, notchW: 180, notchH: 32}
	view := ui.NewTester(a.view, sheetW, sheetH)
	r, ok := view.Find("剪贴板")
	if !ok {
		t.Fatal("clipboard ear icon missing")
	}
	before := view.Image()
	view.Move(r.X+r.W/2, r.Y+r.H/2)
	view.Advance(time.Second)
	after := view.Image()
	var changed, bright int
	b := after.Bounds()
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			r0, g0, b0, _ := before.At(x, y).RGBA()
			r1, g1, b1, _ := after.At(x, y).RGBA()
			if r0 == r1 && g0 == g1 && b0 == b1 {
				continue
			}
			changed++
			if (r1+g1+b1)/3 > 0xB000 {
				bright++
			}
		}
	}
	if changed < 200 {
		t.Fatalf("no tooltip appeared after resting on the icon (%d pixels changed)", changed)
	}
	// Readable text: some bright glyph pixels on a fill that is mostly not
	// the glyphs' colour.
	if bright < 20 || bright > changed/2 {
		t.Fatalf("tooltip text not readable: %d bright of %d changed pixels", bright, changed)
	}
}

// 探头态（peeking）下刘海胶囊显示展开提示，单击后托盘标记为展开；
// 无窗口环境下窗口相关操作安全跳过。
func TestNotchPeekClickExpandsShelf(t *testing.T) {
	s, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	a := &shelf{store: s, peeking: true, peekProgress: 1, notchW: 182, notchH: 32, topInset: 32}
	view := ui.NewTester(a.view, sheetW, sheetH)
	if _, ok := view.Find("展开 ShelfU"); !ok {
		t.Fatal("peek pill missing its expand affordance (chevron)")
	}
	if view.HasText("收起") {
		t.Fatal("peek state should not render the expanded header")
	}
	if err := view.Click("展开 ShelfU"); err != nil {
		t.Fatalf("peek pill not clickable: %v", err)
	}
	if !a.visible {
		t.Fatal("clicking the peek pill did not expand the shelf")
	}
	a.progress = 1 // 展开动画在无窗口测试里不推进，直接到位。
	view.Frame()
	if _, ok := view.Find("收起"); ok {
		t.Fatal("the expanded shelf still draws a collapse button")
	}
	if !view.HasText("最近放入") {
		t.Fatalf("the expanded shelf is not the overview: %v", view.Texts())
	}
}

// 刘海几何无效（notchW==0，NotchRect 未匹配到显示器）时悬停与拖拽触发区
// 都必须关闭，否则 (0,0) 附近会凭空出现探头/展开热区，托盘自行弹出。
func TestNoNotchGeometryDisablesTriggers(t *testing.T) {
	a := &shelf{}
	for _, p := range []mygo.Point{{X: 0, Y: 0}, {X: 100, Y: 20}, {X: 646, Y: 30}} {
		if a.overAnchor(p) {
			t.Fatalf("overAnchor(%v) true with no geometry", p)
		}
		if a.dropBand(p) {
			t.Fatalf("dropBand(%v) true with no notch geometry", p)
		}
	}
}

// 有效刘海几何时：悬停区紧贴刘海（含少量余量），投放带是刘海宽+左右余量
// × 菜单栏高的一条——菜单栏以下、远离刘海的位置都不得命中。
func TestNotchTriggersBoundToRealRect(t *testing.T) {
	a := &shelf{notchX: 645, notchY: 0, notchW: 180, notchH: 32}
	if !a.overAnchor(mygo.Point{X: 735, Y: 20}) {
		t.Fatal("cursor at notch center should hover")
	}
	if a.overAnchor(mygo.Point{X: 100, Y: 20}) {
		t.Fatal("cursor far left of notch must not hover")
	}
	if a.overAnchor(mygo.Point{X: 735, Y: 60}) {
		t.Fatal("cursor below hover pad must not hover")
	}
	if !a.dropBand(mygo.Point{X: 735, Y: 31}) {
		t.Fatal("cursor on the notch's bottom row should hit the drop band")
	}
	// The band's hold reach covers the guard's landing row below the
	// menu bar — the pushed cursor stays in-band — but those rows are
	// not an entry edge: arriving there from below does not enter.
	if !a.dropBandX(mygo.Point{X: 735, Y: dropBandH(a.notchH) - 1}) {
		t.Fatal("the guard's landing row left the band's hold range")
	}
	if a.dropBand(mygo.Point{X: 735, Y: dropBandH(a.notchH) - 1}) {
		t.Fatal("the landing row below the bar must not be an entry edge")
	}
	if a.dropBand(mygo.Point{X: 645 - dropBandPadX - 1, Y: 10}) {
		t.Fatal("cursor left of the band's pad must not hit it")
	}
	if !a.dropBand(mygo.Point{X: 645 - dropBandPadX, Y: 10}) {
		t.Fatal("cursor on the band's left edge should hit it")
	}
	if !a.dropBand(mygo.Point{X: 645 + 180 + dropBandPadX - 1, Y: 10}) {
		t.Fatal("cursor inside the band's right pad should hit it")
	}
	if a.dropBand(mygo.Point{X: 100, Y: 20}) {
		t.Fatal("cursor far from notch center must not hit the drop band")
	}
}

// 拖拽会话一旦激活，整块 overlay 都必须接收事件。空闲时 ignoringMouse 表示
// 「刘海以下穿透」，刘海条本身仍然命中，否则文件拖到 Y=0 会打开桌面栏。
func TestDragSessionDisablesPassThrough(t *testing.T) {
	a := &shelf{dragging: true, ignoringMouse: true}
	a.syncMousePassThrough()
	if a.ignoringMouse {
		t.Fatal("active drag must clear ignoringMouse unconditionally")
	}
	b := &shelf{peeking: true, ignoringMouse: true, notchX: 600, notchW: 180, notchH: 32}
	b.syncMousePassThrough()
	if b.ignoringMouse {
		t.Fatal("peeking shelf must accept mouse events so the pill is clickable")
	}
	d := &shelf{pointerDrag: true, ignoringMouse: true, notchX: 600, notchW: 180, notchH: 32}
	d.syncMousePassThrough()
	if d.ignoringMouse {
		t.Fatal("the first mouse-dragged event of a file drag must make the overlay a drop target")
	}
	c := &shelf{notchH: 32}
	c.syncMousePassThrough()
	if !c.ignoringMouse {
		t.Fatal("idle collapsed shelf must stay click-through below the notch")
	}
}

func TestPeekPillCoversExpandHint(t *testing.T) {
	a := &shelf{notchX: 645, notchY: 0, notchW: 180, notchH: 32, y: 0}
	// Hint is bottom-aligned in the capsule (padding 7, 11pt type).
	hint := mygo.Point{X: a.notchX + a.notchW/2, Y: max(32, a.notchH) + 18 - 8}
	if !a.overPeekPill(hint) {
		t.Fatalf("expand hint at %v is outside peek pill %+v", hint, a.peekPillRect())
	}
	if a.overPeekPill(mygo.Point{X: 80, Y: 20}) {
		t.Fatal("far-left cursor must not count as a pill click")
	}
}

func TestMouseDownOnPeekPillExpandsEvenIfEventWentElsewhere(t *testing.T) {
	a := &shelf{peeking: true, peekProgress: 1, notchX: 645, notchY: 0, notchW: 180, notchH: 32, y: 0}
	hint := mygo.Point{X: a.notchX + a.notchW/2, Y: max(32, a.notchH) + 10}
	a.mouseDown(false, 0, hint)
	if !a.visible {
		t.Fatal("clicking the visible peek pill must expand even when WindowServer gave the press to another app")
	}
}

func TestMouseDownAwayFromPeekDoesNotExpand(t *testing.T) {
	a := &shelf{peeking: true, peekProgress: 1, notchX: 645, notchY: 0, notchW: 180, notchH: 32, y: 0}
	a.mouseDown(false, 0, mygo.Point{X: 80, Y: 40})
	if a.visible {
		t.Fatal("a click far from the pill must not expand the shelf")
	}
}

// Hover must not move or resize the native surface; only the drawing changes.
// Keeping the strip anchored also prevents it from covering the Dock.
func TestHoverKeepsWindowAnchored(t *testing.T) {
	a := &shelf{x: 470, y: 0, notchX: 600, notchW: 180, notchH: 32}
	idle := a.desiredFrame()
	for _, progress := range []float32{0, 0.2, 0.8, 1, 0.4, 0} {
		a.peeking = progress > 0
		a.peekProgress = progress
		if got := a.desiredFrame(); got != idle {
			t.Fatalf("hover remapped the surface: %+v -> %+v", idle, got)
		}
	}
	if idle.Y != 0 || idle.Y+idle.Height >= 380 {
		t.Fatalf("collapsed surface must stay at the top, far above the Dock: %+v", idle)
	}
	// An incoming drag deliberately shrinks the surface to the notch's
	// menu-bar band (task D): ShelfU holds no sub-band real estate during a
	// drag, so apps under the old strip get their windows back.
	a.dragging = true
	drag := a.desiredFrame()
	bandW := max(180, a.notchW) + 2*dropBandPadX
	hintW := int(180+dropHintExtraW) + 2*earR
	if drag.Height > max(32, a.notchH) || drag.Width != max(bandW, hintW) {
		t.Fatalf("incoming-drag frame must be the menu-bar band (±ears), got %+v", drag)
	}
	a.visible = true
	if got := a.desiredFrame(); got.Y != idle.Y || got.Height <= idle.Height {
		t.Fatalf("opening must grow downwards without moving the top edge: %+v", got)
	}
}

// The event filter's corridor has to sit inside the collapsed drop
// strip: before the drag ever enters the band it is menu-bar high, so
// the clamp lands on the bar's last row (inside the entry range), and
// ordinary drags below the bar are never pushed there. Once the drag
// is in-band the corridor reaches the landing row below the bar, which
// the grown window covers.
func TestDragGuardLandsInsideCollapsedStrip(t *testing.T) {
	a := &shelf{x: 470, y: 0, notchX: 600, notchW: 180, notchH: 32}
	strip := a.desiredFrame()
	g := a.dragGuardRegion()
	if g.Y != strip.Y {
		t.Fatalf("guard top %+v must share the strip top %+v", g, strip)
	}
	if g.Y+g.Height > strip.Y+strip.Height {
		t.Fatalf("guard landing %+v misses drop strip %+v", g, strip)
	}
	if g.X < strip.X || g.X+g.Width > strip.X+strip.Width {
		t.Fatalf("guard corridor %+v is wider than drop strip %+v", g, strip)
	}
	// Unentered: the corridor is menu-bar high; its push lands on the
	// last row — inside the band's entry edge, so pushing enters.
	if g.Height != menuBandH(a.notchH)-1 {
		t.Fatalf("unentered corridor %dpt high, want menu-bar %d", g.Height, menuBandH(a.notchH)-1)
	}
	// In-band: the corridor reaches the landing row below the bar.
	a.inBand = true
	g = a.dragGuardRegion()
	if g.Height != dropBandH(a.notchH)-1 {
		t.Fatalf("in-band corridor %dpt high, want landing %d", g.Height, dropBandH(a.notchH)-1)
	}
}

// 探头/拖拽态的黑壳必须与窗口顶边（Y=0）齐平：光标落在窗口第一行时事件才
// 会打在托盘上，守卫放行段交出去的拖放才有地方落下。壳只在水平方向居中，
// 窗口顶边其余部分不属于托盘——那一段由守卫夹取，拖放交给桌面与下层应用。
// 测试环境没有真实窗口，tester 尺寸即窗口帧。
func TestPeekShellFlushWithWindowTop(t *testing.T) {
	a := &shelf{peeking: true, peekProgress: 1, notchW: 180, notchH: 32}
	view := ui.NewTester(a.view, sheetW, sheetH)
	img := view.Image()
	b := img.Bounds()
	first, last := -1, -1
	for x := range b.Dx() {
		if _, _, _, alpha := img.At(x, 0).RGBA(); alpha > 0 {
			if first < 0 {
				first = x
			}
			last = x
		}
	}
	if first < 0 {
		t.Fatal("peek shell does not touch the window's top edge: a drop at Y=0 would miss the shelf")
	}
	if first == 0 || last == b.Dx()-1 {
		t.Fatalf("peek shell spans the whole top edge (%d..%d): the shelf would swallow drops meant for the desktop", first, last)
	}
}

func TestPeekLeavesNoPaintWhenPointerLeaves(t *testing.T) {
	// A non-nil window selects the production rendering branch; the tester
	// supplies the surface and never invokes a native window operation here.
	a := &shelf{win: &mygo.Window{}, notchW: 180, notchH: 32}
	view := ui.NewTester(a.view, 280, 72)
	for range 12 {
		a.peeking, a.peekProgress = true, 1
		view.Frame()
		img := view.Image()
		if _, _, _, alpha := img.At(140, 2).RGBA(); alpha == 0 {
			t.Fatal("peek did not paint at the top")
		}
		for x := range img.Bounds().Dx() {
			if _, _, _, alpha := img.At(x, img.Bounds().Dy()-1).RGBA(); alpha != 0 {
				t.Fatal("peek painted beyond its bottom boundary")
			}
		}
		a.peeking, a.peekProgress = false, 0
		view.Frame()
		img = view.Image()
		for y := range img.Bounds().Dy() {
			for x := range img.Bounds().Dx() {
				if _, _, _, alpha := img.At(x, y).RGBA(); alpha != 0 {
					t.Fatalf("collapsed surface retained paint at %d,%d", x, y)
				}
			}
		}
		if a.visible {
			t.Fatal("hover must never expand the shelf")
		}
	}
}

// 置顶角标必须落在各自格子的右上角，而不是叠在网格层（回归：角标曾是
// 网格的 Absolute 子元素，全部挤在网格右上角）。
func TestPinBadgesStayInsideTheirTiles(t *testing.T) {
	s, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err := s.AddText("pinned one"); err != nil {
		t.Fatal(err)
	}
	if err := s.AddText("pinned two"); err != nil {
		t.Fatal(err)
	}
	for _, it := range s.Items {
		if err := s.SetPinned(it.ID, true); err != nil {
			t.Fatal(err)
		}
	}
	a := &shelf{store: s, thumbnails: map[string]*ui.Bitmap{}, thumbPending: map[string]bool{}}
	view := ui.NewTester(a.view, sheetW, sheetH)
	tile, ok := view.Find("pinned two") // 最新置顶项排第一格
	if !ok {
		t.Fatal("pinned tile missing")
	}
	badge, ok := view.Find("已置顶")
	if !ok {
		t.Fatal("pin badge missing")
	}
	if badge.X < tile.X || badge.X+badge.W > tile.X+tile.W ||
		badge.Y < tile.Y || badge.Y+badge.H > tile.Y+tile.H {
		t.Fatalf("pin badge %+v outside its tile %+v", badge, tile)
	}
}

// islandFrame and sheetFrame are the native window boxes the two levels
// need: the level's own shell, the ear corners the shell draws past its
// flanks and the opening spring's room. Each level has its own — an open
// island never holds the sheet's box (that 451pt transparent frame sat
// over the menu-bar icons beside it), and the mouse only reaches the shell
// drawn inside it (overlayAcceptsMouse).
func islandFrame() (int, int) { return openFrameRoom(overviewW, overviewH) }
func sheetFrame() (int, int)  { return openFrameRoom(sheetW, sheetH) }

// The shell has exactly two sizes, whatever the pages hold: the overview's
// 440 × 156 and a page's 440 × 250. Rows and live activities change the
// content inside the fixed box, never the frame.
func TestPanelSizeIsFixedPerLevel(t *testing.T) {
	s, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	a := &shelf{store: s, visible: true, level: 1, thumbnails: map[string]*ui.Bitmap{}, thumbPending: map[string]bool{}}
	// place() 需要真实显示器（AppKit）；无窗口测试直接给定几何。
	a.dispBounds = mygo.Rectangle{X: 0, Y: 0, Width: 1470, Height: 956}
	a.x, a.y = 455, 0
	add := func(n int) {
		for range n {
			if err := s.AddText(fmt.Sprintf("row %d", len(s.Items))); err != nil {
				t.Fatal(err)
			}
		}
	}
	short := a.desiredFrame()
	iw, _ := islandFrame()
	if short.Width != iw || short.Height != overviewH {
		t.Fatalf("empty overview frame = %d×%d, want %d×%d", short.Width, short.Height, iw, overviewH)
	}
	add(gridCols * 4)
	if got := a.desiredFrame(); got != short {
		t.Fatalf("items resized the overview: %+v -> %+v", short, got)
	}
	a.setLevel(2)
	sheet := a.desiredFrame()
	sw, _ := sheetFrame()
	if sheet.Width != sw || sheet.Height != sheetH {
		t.Fatalf("sheet frame = %d×%d, want %d×%d", sheet.Width, sheet.Height, sw, sheetH)
	}
	add(gridCols)
	a.settings.IslandNowPlaying = true
	a.np = &nowPlayingModule{host: a, have: true, last: mac.NowPlayingState{Title: "T", Artist: "A"}}
	a.modules = []module{a.np}
	if got := a.desiredFrame(); got != sheet {
		t.Fatalf("rows or a live activity resized the sheet: %+v -> %+v", sheet, got)
	}
	a.setLevel(1)
	if got := a.desiredFrame(); got != short {
		t.Fatalf("back on the overview: %+v, want %+v", got, short)
	}
	if short.Height >= sheet.Height {
		t.Fatalf("the overview's box %+v is not shorter than the page's %+v", short, sheet)
	}
	// A panel still opening keeps its own box: the page's frame here would
	// be a transparent strip over the menu-bar icons.
	a.fx.openMoving = true
	iw, ih := islandFrame()
	if got := a.desiredFrame(); got.Width != iw || got.Height != ih {
		t.Fatalf("opening overview frame = %+v, want the overview's in-flight %d×%d", got, iw, ih)
	}
	a.fx.openMoving = false
}

// Switching pages must not resize the shell: every page shares the same
// 420 × 250 sheet and scrolls inside its 396 × 174 content box.
func TestPageSwitchKeepsPanelSize(t *testing.T) {
	s, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err := s.AddText("托盘里一条"); err != nil {
		t.Fatal(err)
	}
	for i := range 12 {
		if err := s.AddClipText(fmt.Sprintf("剪贴板 %d", i), ""); err != nil {
			t.Fatal(err)
		}
	}
	a := &shelf{store: s, visible: true, level: 2, thumbnails: map[string]*ui.Bitmap{}, thumbPending: map[string]bool{}}
	a.dispBounds = mygo.Rectangle{X: 0, Y: 0, Width: 1470, Height: 956}
	a.x, a.y = 455, 0
	tray := a.desiredFrame()
	a.setTab(pageClipboard)
	if clip := a.desiredFrame(); clip != tray {
		t.Fatalf("switching to the clipboard resized the sheet: %+v -> %+v", tray, clip)
	}
	// A longer list scrolls inside the box instead of growing the shell.
	if err := s.AddClipText("再来一条", ""); err != nil {
		t.Fatal(err)
	}
	if got := a.desiredFrame(); got != tray {
		t.Fatalf("a longer clipboard list resized the sheet: %+v -> %+v", tray, got)
	}
}

// fakePage is a registered page used to prove the registry and the
// disabled-page fallback. on flips enabled().
type fakePage struct {
	on bool
}

func (p *fakePage) id() pageID    { return "fake" }
func (p *fakePage) label() string { return "假页面" }
func (p *fakePage) icon(c *ui.Context, _ bool) ui.Element {
	return ui.Icon(c, textSymbol)
}
func (p *fakePage) enabled() bool { return p.on }
func (p *fakePage) body(c *ui.Context, _ bool, _ bool) ui.Element {
	return ui.Column(c).Grow(1).MinHeight(0).Children(func() {
		ui.Text(c, "假页面内容").FontSize(13).TextColor(ink)
	})
}
func (p *fakePage) title() string { return "假页面" }
func (p *fakePage) actions(c *ui.Context) []ui.Element {
	return nil
}

// A page registered later joins the page order after the built-ins and
// draws inside the same fixed content box — the shell's size never
// depends on which page is current.
func TestRegisteredPageJoinsTheOrderAtFixedSize(t *testing.T) {
	a := &shelf{visible: true, level: 2, thumbnails: map[string]*ui.Bitmap{}, thumbPending: map[string]bool{}}
	a.dispBounds = mygo.Rectangle{X: 0, Y: 0, Width: 1470, Height: 956}
	a.x, a.y = 455, 0
	fake := &fakePage{on: true}
	a.registerPage(fake)
	if got := a.pageIndex("fake"); got != 5 {
		t.Fatalf("the fake page must register after the five built-ins, got index %d", got)
	}
	before := a.desiredFrame()
	a.setTab("fake")
	if got := a.desiredFrame(); got != before {
		t.Fatalf("a registered page resized the sheet: %+v -> %+v", before, got)
	}
	view := ui.NewTester(a.view, sheetW, sheetH)
	if !view.HasText("假页面内容") {
		t.Fatal("the fake page's content is missing")
	}
}

// Switching off the current page (e.g. AI 用量's setting) folds the panel
// back to the tray on the next frame; its ear icon also disappears.
func TestDisabledCurrentPageFallsBackToTray(t *testing.T) {
	a := &shelf{visible: true, thumbnails: map[string]*ui.Bitmap{}, thumbPending: map[string]bool{}}
	fake := &fakePage{on: true}
	a.registerPage(fake)
	view := ui.NewTester(a.view, sheetW, sheetH)
	a.setTab("fake")
	view.Frame()
	if !view.HasText("假页面内容") {
		t.Fatal("the fake page did not render")
	}
	fake.on = false
	view.Frame()
	if a.tab != pageTray || a.currentPage().id() != pageTray {
		t.Fatalf("a disabled current page must fall back to the tray, tab=%s", a.tab)
	}
	if view.HasText("假页面") {
		t.Fatal("a disabled page kept its ear icon and module bar")
	}
	// Re-enabling brings the icon back but the panel stays on the tray.
	fake.on = true
	view.Frame()
	if a.tab != pageTray {
		t.Fatal("the fallback must not be undone by re-enabling the page")
	}
	if _, ok := view.Find("假页面"); !ok {
		t.Fatal("the re-enabled page's ear icon did not come back")
	}
}

// The window follows the level the panel is on: an open island never holds
// the sheet's box (that 451pt transparent frame sat over the menu-bar icons
// beside it). A level spring still leaves the drawn shell wider than its
// target for a few frames, and that shell is never clipped.
func TestPanelFrameFollowsTheLevel(t *testing.T) {
	a := &shelf{visible: true, level: 1}
	a.x, a.y = 475, 0
	iw, _ := islandFrame()
	if f := a.desiredFrame(); f.Width != iw || f.Height != overviewH {
		t.Fatalf("open island frame = %d×%d, want the island's %d×%d", f.Width, f.Height, iw, overviewH)
	}
	// Mid-way back from the sheet the drawn shell is still wide.
	a.fx.w = overviewW + (sheetW-overviewW)*0.5
	if got, want := a.desiredFrame().Width, int(a.fx.w+0.5)+2*earR; got < want {
		t.Fatalf("frame %d wide clips the shrinking shell at %.1f, want at least %d", got, a.fx.w, want)
	}
	a.fx.w = 0
	a.setLevel(2)
	sw, _ := sheetFrame()
	if f := a.desiredFrame(); f.Width != sw || f.Height != sheetH {
		t.Fatalf("sheet frame = %d×%d, want the sheet's %d×%d", f.Width, f.Height, sw, sheetH)
	}
}

// 托盘页的「清空」对剪贴板收录只做隐藏：截图从托盘消失但留在剪贴板页，
// 受管 PNG 不删；被拖入的文件仍被真正删除。再到剪贴板页点「清空」才把
// 截图连图一起删掉。
func TestTrayClearHidesClipboardCaptureInsteadOfDeleting(t *testing.T) {
	s, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	dir := t.TempDir()
	note := filepath.Join(dir, "note.txt")
	if err := os.WriteFile(note, []byte("hi"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := s.AddFiles([]string{note}); err != nil {
		t.Fatal(err)
	}
	img := image.NewRGBA(image.Rect(0, 0, 4, 4))
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	if err := s.AddClipImage(buf.Bytes(), "com.example.Shot"); err != nil {
		t.Fatal(err)
	}
	var clip store.Item
	for _, it := range s.Items {
		if it.Kind == store.KindImage {
			clip = it
		}
	}
	managed := clip.Path
	a := &shelf{store: s, thumbnails: map[string]*ui.Bitmap{}, thumbPending: map[string]bool{}}
	view := ui.NewTester(a.view, sheetW, sheetH)

	if err := view.Click("清空"); err != nil {
		t.Fatalf("tray clear: %v", err)
	}
	// 文件引用真的被删了，但剪贴板截图只是移出托盘。
	if _, err := os.Stat(note); err != nil {
		t.Fatalf("original file deleted by tray clear: %v", err)
	}
	if _, err := os.Stat(managed); err != nil {
		t.Fatalf("managed png deleted by tray clear: %v", err)
	}
	for _, it := range s.Items {
		if it.Kind == store.KindFile {
			t.Fatal("tray clear kept the file item")
		}
	}
	if len(s.Items) != 1 || s.Items[0].ID != clip.ID {
		t.Fatalf("tray clear must keep the clipboard capture in the store: %+v", s.Items)
	}
	if !view.HasText("把文件拖到这里") {
		t.Fatal("tray still shows the hidden capture")
	}
	a.setTab(pageClipboard)
	view.Frame()
	if !view.HasText(itemTitle(clip)) {
		t.Fatal("hidden capture missing from the clipboard page")
	}
	// 剪贴板页的「清空」才是真正的删除。
	if err := view.Click("清空"); err != nil {
		t.Fatalf("clipboard clear: %v", err)
	}
	if len(s.Items) != 0 {
		t.Fatalf("clipboard clear left %d items", len(s.Items))
	}
	if _, err := os.Stat(managed); !os.IsNotExist(err) {
		t.Fatalf("managed png should be deleted by clipboard clear: %v", err)
	}
	if _, err := os.Stat(note); err != nil {
		t.Fatalf("original file deleted by clipboard clear: %v", err)
	}
}

// 托盘页右键「移除」剪贴板截图同样只是把它移出托盘：项目保留、PNG 保留、
// 剪贴板页仍可见；之后重新复制同一张图会把它带回托盘。
func TestTrayMenuRemoveHidesClipboardCapture(t *testing.T) {
	s, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	img := image.NewRGBA(image.Rect(0, 0, 4, 4))
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	pngA := buf.Bytes()
	if err := s.AddClipImage(pngA, "com.example.Shot"); err != nil {
		t.Fatal(err)
	}
	clip := s.Items[0]
	managed := clip.Path
	a := &shelf{store: s, thumbnails: map[string]*ui.Bitmap{}, thumbPending: map[string]bool{}}
	view := ui.NewTester(a.view, sheetW, sheetH)

	if err := view.RightClick(itemTitle(clip)); err != nil {
		t.Fatalf("right-click clip: %v", err)
	}
	if err := view.ChooseMenuItem("移除"); err != nil {
		t.Fatalf("choose 移除: %v (%v)", err, view.Menu())
	}
	if len(s.Items) != 1 || s.Items[0].ID != clip.ID {
		t.Fatalf("tray 移除 deleted the capture: %+v", s.Items)
	}
	if _, err := os.Stat(managed); err != nil {
		t.Fatalf("managed png deleted by tray 移除: %v", err)
	}
	if !view.HasText("把文件拖到这里") {
		t.Fatal("tray still shows the hidden capture")
	}
	a.setTab(pageClipboard)
	view.Frame()
	if !view.HasText(itemTitle(clip)) {
		t.Fatal("hidden capture missing from the clipboard page")
	}
	// 再次复制同一张图：去重命中，回到托盘最前。
	a.setTab(pageTray)
	if err := s.AddClipImage(pngA, "com.example.Shot"); err != nil {
		t.Fatal(err)
	}
	view.Frame()
	if len(s.Items) != 1 || !view.HasText(itemTitle(clip)) {
		t.Fatal("re-capture did not bring the item back to the tray")
	}
}

// TestScrollEdgesOnlyWhereThereIsMore: the edge bands appear only on an
// edge that still has content beyond it, and never for content that fits.
func TestScrollEdgesOnlyWhereThereIsMore(t *testing.T) {
	cases := []struct {
		name          string
		y, maxY, size float32
		top, bottom   bool
	}{
		{"fits", 0, 0, 200, false, false},
		{"start with more below", 0, 500, 200, false, true},
		{"scrolled", 120, 500, 200, true, true},
		{"at the end", 500, 500, 200, true, false},
		{"moved by a pixel", 0.2, 500, 200, false, true},
	}
	for _, c := range cases {
		st := &ui.ScrollState{Y: c.y, MaxY: c.maxY}
		top, bottom, viewH := scrollEdges(st, c.size)
		if top != c.top || bottom != c.bottom {
			t.Fatalf("%s: bands top=%v bottom=%v, want %v/%v", c.name, top, bottom, c.top, c.bottom)
		}
		if viewH <= 0 || viewH > c.size {
			t.Fatalf("%s: viewport height %.1f out of %.1f", c.name, viewH, c.size)
		}
	}
}

// TestEdgeBandIsAtMostHalfARow: the 174pt box is short, so the melt never
// reaches deeper than half a row and never past edgeBandMax.
func TestEdgeBandIsAtMostHalfARow(t *testing.T) {
	cases := []struct {
		rowH, want float32
	}{
		{clipRowH, clipRowH / 2}, // 30pt rows: a 15pt band
		{toolCellH, edgeBandMax}, // 84pt cards: capped at 24
		{tileH, edgeBandMax},     // 85pt tiles: capped at 24
		{20, 10},
	}
	for _, c := range cases {
		if got := edgeBandFor(c.rowH); got != c.want {
			t.Fatalf("edgeBandFor(%g) = %g, want %g", c.rowH, got, c.want)
		}
		if got := edgeBandFor(c.rowH); got > c.rowH/2+0.001 {
			t.Fatalf("edgeBandFor(%g) = %g is deeper than half a row", c.rowH, got)
		}
	}
}

// TestEdgeFadeIsProgressive: the blur weight rises smoothly to the edge,
// is full at it, and is gone past the band.
func TestEdgeFadeIsProgressive(t *testing.T) {
	band := edgeBandFor(clipRowH)
	if got := edgeFade(0, band); got != 1 {
		t.Fatalf("at the edge the fade is %.2f, want 1", got)
	}
	if got := edgeFade(band, band); got != 0 {
		t.Fatalf("at the band's end the fade is %.2f, want 0", got)
	}
	if got := edgeFade(band+10, band); got != 0 {
		t.Fatalf("past the band the fade is %.2f, want 0", got)
	}
	prev := float32(1)
	for d := float32(0); d <= band; d += 2 {
		got := edgeFade(d, band)
		if got > prev+1e-6 {
			t.Fatalf("the fade rose from %.3f to %.3f at %.0fpt", prev, got, d)
		}
		if got < 0 || got > 1 {
			t.Fatalf("fade %.2f out of range at %.0fpt", got, d)
		}
		prev = got
	}
	if mid := edgeFade(band/2, band); mid < 0.3 || mid > 0.7 {
		t.Fatalf("halfway into the band the fade is %.2f, want about half", mid)
	}
}

// TestRowsFadeOnlyInTheBand: only rows inside the viewport and reaching
// into an active band get a blur weight; a row in the open, or scrolled
// out, is left alone.
func TestRowsFadeOnlyInTheBand(t *testing.T) {
	const viewH = 174
	band := edgeBandFor(clipRowH)
	cases := []struct {
		name       string
		top        float32
		topBand    bool
		bottomBand bool
		want       float32
	}{
		{"first row under the top band", 0, true, false, 1},
		{"row inside the band", 6, true, false, 0.5},
		{"row past the band", 40, true, false, 0},
		{"row scrolled fully out", -40, true, true, 0},
		{"row straddling the top edge", -12, true, false, 1},
		{"last row at the bottom edge", viewH - clipRowH, false, true, 1},
		{"row above the bottom band", viewH - clipRowH - 40, false, true, 0},
		{"bands off", 0, false, false, 0},
	}
	for _, c := range cases {
		got := rowFade(c.top, clipRowH, viewH, c.topBand, c.bottomBand, band)
		switch {
		case c.want == 1 && got < 0.99:
			t.Fatalf("%s: weight %.2f, want the edge weight", c.name, got)
		case c.want == 0 && got != 0:
			t.Fatalf("%s: weight %.2f, want 0", c.name, got)
		case c.want > 0 && c.want < 1 && (got <= 0 || got >= 1):
			t.Fatalf("%s: weight %.2f, want a partial edge weight", c.name, got)
		}
	}
	// Progressive, not a step: a row further into the band is sharper than
	// one at its edge and blurrier than one past it.
	edge := rowFade(1, clipRowH, viewH, true, false, band)
	inner := rowFade(band-2, clipRowH, viewH, true, false, band)
	if !(inner < edge && edge < 1) {
		t.Fatalf("weights edge %.2f inner %.2f do not progress toward the edge", edge, inner)
	}
}

// TestBandKeepsTheSamePageIcons: the band carries one icon per page in
// registry order, the current one included — a switch never removes an
// icon and reflows the rest.
func TestBandKeepsTheSamePageIcons(t *testing.T) {
	fx := newUsageFixture(t, true)
	a := fx.a
	a.level, a.visible = 2, true
	a.setTab(pageTray)
	view := fx.view
	view.Frame()
	view.Frame()
	pages := a.tabPages()
	if len(pages) != 5 {
		t.Fatalf("the band has %d icons, want the design's 5", len(pages))
	}
	want := []pageID{pageOverview, pageTray, pageClipboard, pageTools, pageUsage}
	for i, p := range pages {
		if p.id() != want[i] {
			t.Fatalf("band icon %d is %s, want %s (registry order)", i, p.id(), want[i])
		}
	}
	for _, id := range []pageID{pageClipboard, pageTools, pageUsage, pageTray} {
		if err := view.Click(a.page(id).label()); err != nil {
			t.Fatalf("the %s icon is missing while on %s: %v", id, a.tab, err)
		}
		view.Frame()
		if a.tab != id {
			t.Fatalf("clicking the %s icon left the panel on %s", id, a.tab)
		}
		if got := a.tabPages(); len(got) != 5 {
			t.Fatalf("on %s the band has %d icons, want 5", id, len(got))
		}
	}
	// The overview icon steps back to the short size.
	if err := view.Click("总览"); err != nil {
		t.Fatal(err)
	}
	if a.currentLevel() != 1 || a.tab != pageOverview {
		t.Fatalf("the overview icon left level=%d tab=%s", a.currentLevel(), a.tab)
	}
}

// TestBandIconsKeepOneXAcrossPages: the band's icons keep their x when the
// panel switches pages; only the capsule moves.
func TestBandIconsKeepOneXAcrossPages(t *testing.T) {
	a, view := levelShelf(t)
	if err := a.store.AddClipImage(testPNG(t), "com.example.Shot"); err != nil {
		t.Fatal(err)
	}
	a.visible, a.level = true, 2
	a.setTab(pageTray)
	seen := map[pageID]float32{}
	for _, id := range []pageID{pageTray, pageClipboard, pageTools, pageTray} {
		if err := view.Click(a.page(id).label()); err != nil {
			t.Fatalf("the %s icon is missing while on %s: %v", id, a.tab, err)
		}
		view.Frame()
		view.Frame() // the switch settles
		if a.tab != id {
			t.Fatalf("the panel is on %s, want %s", a.tab, id)
		}
		for _, p := range a.tabPages() {
			r, ok := view.Find(p.label())
			if !ok {
				t.Fatalf("the %s icon is missing on %s; texts %v", p.id(), id, view.Texts())
			}
			if want, ok := seen[p.id()]; ok {
				if r.X != want {
					t.Fatalf("the %s icon moved from %.1f to %.1f when the panel switched to %s",
						p.id(), want, r.X, id)
				}
				continue
			}
			seen[p.id()] = r.X
		}
	}
	if len(seen) < 4 {
		t.Fatalf("measured %d icons", len(seen))
	}
}

// TestClipboardPageFitsItsControls: with a long history the search field
// and 清空 fit the 416pt action row, and the five band icons stay clear of
// each other.
func TestClipboardPageFitsItsControls(t *testing.T) {
	a, view := levelShelf(t)
	a.store.SetTextLimit(300) // the widest history setting
	a.settings.ClipboardLimit = 300
	for i := range 108 {
		if err := a.store.AddClipText(fmt.Sprintf("剪贴板内容 %d · 一段足够长的文字用于检查省略", i), "com.example.Source"); err != nil {
			t.Fatal(err)
		}
	}
	a.registerPage(&fakePage{on: true}) // a sixth page: the band keeps five
	a.visible, a.level = true, 2
	a.setTab(pageClipboard)
	view.Frame()
	view.Frame()
	if got := len(a.tabPages()); got != 5 {
		t.Fatalf("the band has %d icons, want 5", got)
	}
	if view.HasText("120 条") {
		t.Fatal("the page row still draws the clipboard count")
	}
	search, ok := view.Find(searchLabel)
	if !ok {
		t.Fatal("the clipboard search field is missing")
	}
	rowLeft := float32((sheetW - contentW) / 2)
	if search.X < rowLeft-0.5 || search.X+search.W > rowLeft+contentW+0.5 {
		t.Fatalf("the search field %+v leaves the action row", search)
	}
	var prev ui.Rect
	for _, p := range a.tabPages() {
		r, ok := view.Find(p.label())
		if !ok {
			t.Fatalf("the %s icon is missing", p.id())
		}
		if prev.W > 0 && r.X < prev.X+prev.W {
			t.Fatalf("the %s icon at %.1f overlaps the previous icon ending %.1f", p.id(), r.X, prev.X+prev.W)
		}
		prev = r
	}
}
