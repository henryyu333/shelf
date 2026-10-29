//go:build darwin

package main

// The tool cards' catalog, drawing and file-drop entry, shared by the
// tool page (cardPanel / cardEdit surfaces) and the desktop pin windows
// (cardDesktop, task B). The persisted arrangement lives in
// internal/layout (layout.json); this file is what a card id means and
// how it draws, accepts files and runs.
//
// For the pin windows, one card instance is exactly these pieces:
//
//	el := a.toolCard(c, layout.Card{ID: id, Span: span}, cardDesktop)
//	// el is the card: content, fill1 shell, file-drop target and hot
//	// state are already wired. The pin adds its own tap handling via
//	// el.Clicked() → a.runToolCard(id) and its own drag-to-move via
//	// HandleInput, as tools_page.go does.
//	// A file drop landing on it calls a.runToolCardDrop(id, drop.Data)
//	// — already wired inside toolCard when the card accepts the class.
//
// Card ids are stable: layout.json stores them.

import (
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/egoist/mygo/transfer"
	"github.com/egoist/mygo/ui"

	"shelf/internal/aiusage"
	"shelf/internal/layout"
	"shelf/internal/mac"
	"shelf/internal/store"
	"shelf/locales"
)

// cardKind separates one-shot actions from widgets with their own state.
type cardKind int

const (
	cardAction cardKind = iota
	cardWidget
)

// toolCardDef is one catalog entry (design draft §3's table): the card
// id stored in layout.json, its title and glyph, the column span it
// defaults to (variableSpan cards toggle 1/2 in edit mode), what file
// classes it accepts and how a drop maps to the drop zone's action.
// accepts answers for a classifyDrop result; available keeps a card out
// of the picker while its feature is off (AI 用量 follows its setting).
type toolCardDef struct {
	id           string
	kind         cardKind
	title        string
	icon         *ui.SVG
	span         int
	rows         int
	variableSpan bool
	accepts      func(class dropClass) bool
	available    func(a *shelf) bool
}

// Card glyphs added for the tool page; the action glyphs come from the
// drop zone's set (dropactions.go) so the same action reads alike.
var (
	toolsNavSymbol = symbol(`<rect x="4" y="4" width="7" height="7" rx="2"/><rect x="13" y="4" width="7" height="7" rx="2"/><rect x="4" y="13" width="16" height="7" rx="2"/>`)
	cupSymbol      = symbol(`<path d="M5 9h11v5a5 5 0 0 1-5 5h-1a5 5 0 0 1-5-5z"/><path d="M16 11h1.5a2.5 2.5 0 0 1 0 5H16M8 3v3M11 3v3M14 3v3"/>`)
	plusSymbol     = symbol(`<path d="M12 5v14M5 12h14"/>`)
	pencilSymbol   = symbol(`<path d="M4 20h4L19 9l-4-4L4 16z"/><path d="M13 7l4 4"/>`)
	// ocrSymbol 由 screenocr.go（任务 C）提供，这里直接用。
	// minusSymbol / spanSymbol are the edit-mode chrome: the ⊖ badge and
	// the variable-span handle.
	minusSymbol = symbol(`<path d="M5 12h14"/>`)
	spanSymbol  = symbol(`<path d="M4 9h7M4 15h10M17 5v14M20 8l-3-3-3 3M20 16l-3 3-3-3"/>`)
	awakeWarn   = warning // 保持唤醒的橙点（设计稿 var(--warn)）
)

// toolCatalog is every card in picker order: actions first, then
// widgets (design §3's rows in order). 框选文字 leads the actions and
// the default layout's first cell (design §1's first grid).
func toolCatalog() []toolCardDef {
	return []toolCardDef{
		{id: "ocr", kind: cardAction, title: "框选文字", icon: ocrSymbol, span: 1, accepts: imagesOnly, available: always},
		{id: "fit1m", kind: cardAction, title: "压缩到 1 MB", icon: shrinkSymbol, span: 1, accepts: imagesOnly, available: always},
		{id: "topdf", kind: cardAction, title: "转成 PDF", icon: pdfSymbol, span: 1, accepts: imagesOnly, available: always},
		{id: "tojpeg", kind: cardAction, title: "转 JPEG", icon: imgSymbol, span: 1, accepts: imagesOnly, available: always},
		{id: "toword", kind: cardAction, title: "转 Word", icon: docSymbol, span: 1, accepts: imagesOrPDFs, available: always},
		{id: "extract", kind: cardAction, title: "解压", icon: unzipSymbol, span: 1, accepts: archivesOnly, available: always},
		{id: "resize", kind: cardAction, title: "缩小图片", icon: shrinkSymbol, span: 1, accepts: imagesOnly, available: always},
		{id: "zip", kind: cardAction, title: "打成 zip", icon: zipSymbol, span: 1, accepts: anyClass, available: always},
		{id: "airdrop", kind: cardAction, title: "隔空投送", icon: airDropSymbol, span: 1, accepts: anyClass, available: always},
		{id: "playing", kind: cardWidget, title: "音乐", icon: musicSymbol, span: 2, rows: 2, accepts: never, available: always},
		{id: "awake", kind: cardWidget, title: "保持唤醒", icon: cupSymbol, span: 1, variableSpan: true, accepts: never, available: always},
		{id: "recent", kind: cardWidget, title: "最近文件", icon: docSymbol, span: 2, accepts: never, available: always},
		{id: "clip", kind: cardWidget, title: "剪贴板最新", icon: clipNavSymbol, span: 2, accepts: never, available: always},
		{id: "usage", kind: cardWidget, title: "AI 用量", icon: gaugeSymbol, span: 1, accepts: never, available: usageAvailable},
	}
}

// The class predicates read classifyDrop's result, same as the drop
// zone's rules (dropactions.go): a card takes exactly the files its
// action could run on. zip and AirDrop take every non-empty class —
// folders, pictures, PDFs and mixes alike.
var (
	always         = func(*shelf) bool { return true }
	usageAvailable = func(a *shelf) bool { return a.settings.AIUsage.Enabled }
	imagesOnly     = func(class dropClass) bool { return class == classImages }
	imagesOrPDFs   = func(class dropClass) bool { return class == classImages || class == classPDFs }
	archivesOnly   = func(class dropClass) bool { return class == classArchives }
	anyClass       = func(class dropClass) bool { return true }
	never          = func(class dropClass) bool { return false }
)

// toolCardDef answers the catalog entry for id, or nil: a layout file
// can name a card this build does not know (a future card, or one that
// was removed), and such cells draw nothing.
func toolCardDefFor(id string) *toolCardDef {
	catalog := toolCatalog()
	for i := range catalog {
		if catalog[i].id == id {
			if catalog[i].rows < 1 {
				catalog[i].rows = 1
			}
			return &catalog[i]
		}
	}
	return nil
}

// name is the card's title in the language of l: the catalog keeps the
// source language's title for the shapes that read the table itself, and
// this switch says the id in the language the user reads.
func (d *toolCardDef) name(l locales.Messages) string {
	switch d.id {
	case "ocr":
		return l.CardOcr()
	case "fit1m":
		return l.CardFit1M()
	case "topdf":
		return l.CardToPDF()
	case "tojpeg":
		return l.CardToJPEG()
	case "toword":
		return l.CardToWord()
	case "extract":
		return l.CardExtract()
	case "resize":
		return l.CardResize()
	case "zip":
		return l.CardZip()
	case "airdrop":
		return l.CardAirDrop()
	case "playing":
		return l.Music()
	case "awake":
		return l.KeepAwake()
	case "recent":
		return l.RecentFiles()
	case "clip":
		return l.LatestClip()
	case "usage":
		return l.AiUsage()
	}
	return d.title
}

// toolCardSurface is where a card draws: the tool page's normal form,
// its name-only editing form, or a desktop pin window (task B). The
// panel and desktop draw identically except that the pin gets the
// border its floating-on-wallpaper needs (design §4) — the pin's own
// window adds that 0.5pt edge, the card itself stays the same.
type toolCardSurface int

const (
	cardPanel   toolCardSurface = iota // tool page, live form
	cardEdit                           // editing: glyph + name only, no live controls
	cardDesktop                        // pin window, live form
)

// toolCard draws one card: the fill1 14-radius shell with the card's
// content for its surface, the hot treatment while dragged files it
// accepts hover it, and its own drop target so a release on it runs the
// action instead of shelving the files. keySfx distinguishes a second
// copy of the same card among siblings (the lifted copy over the parked
// one mid-reorder): siblings sharing a key panic a Tester. The caller
// sets the card's size and adds its surface's pointer wiring.
func (a *shelf) toolCard(c *ui.Context, spec layout.Card, surf toolCardSurface, keySfx ...string) ui.Element {
	def := toolCardDefFor(spec.ID)
	title := spec.ID
	if def != nil && def.id == "usage" {
		// The 1 × 1 card carries no text, so its numbers live in the
		// accessible name and the hover tip (the same string).
		title = a.usageCardLabel(c)
	}
	key := "tc-" + spec.ID
	if len(keySfx) > 0 {
		key = "tc-" + keySfx[0] + "-" + spec.ID
	}
	card := ui.Column(c.Key(key)).Radius(14).MinWidth(0).MinHeight(0).Label(title)
	if def == nil {
		// Unknown id in layout.json: keep an empty cell so the grid
		// still shows where the card would be.
		return card.Background(fill1)
	}
	if def.id == "usage" {
		card.Tooltip(title)
	}
	// Dragged files hover: action cards that take this class are drop
	// targets with the accent treatment; the rest dim to 38% (design §1)
	// and stay out of the drop's way, so a release falls through to the
	// panel's plain 放入托盘 receive. Only a live drag dims: the snapshot
	// outlives the release by dropHoldGrace so the drop still finds its
	// cells, and a held set must not keep painting the page. Widgets never
	// dim — they take no files and stay as they are.
	class, n, live := a.toolCardDrag()
	hovering := surf != cardDesktop && n > 0
	dragPaint := toolCardDragPaint(surf, live)
	if !hovering {
		class = classMixed
	}
	accepts := hovering && def.accepts(class)
	var hot bool
	if surf != cardEdit && accepts {
		_, hot = ui.DataDragOver(card, dropCellOpts)
	}
	switch {
	case hot:
		card.Background(accent16).Border(1.5, accent.Alpha(0.9))
	default:
		card.Background(fill1)
	}
	if a.win != nil && !c.Preferences().ReduceMotion {
		target := float32(1)
		if hot {
			target = 1.03
		}
		scale := card.AnimateTo("hot", target, ui.Animation{Spring: ui.SpringOptions{Duration: 200 * time.Millisecond, Bounce: 0.15}})
		card.Scale(scale, scale).TransformOrigin(0.5, 0.5)
	} else if hot {
		card.Scale(1.03, 1.03).TransformOrigin(0.5, 0.5)
	}
	if dragPaint && toolCardDimmed(def, class) {
		card.Disabled(true).Opacity(0.38)
	}
	if surf == cardEdit {
		a.toolCardEditBody(c, card, def, spec)
	} else {
		sub := ""
		if dragPaint && def.kind == cardAction {
			sub = a.toolCardSub(loc(c), def, class)
		}
		a.toolCardBody(c, card, def, sub, spec, surf)
	}
	if accepts && surf != cardEdit {
		if drop, ok := ui.DropData(card, dropCellOpts); ok {
			a.runToolCardDrop(def.id, drop.Data)
		}
	}
	return card
}

// toolCardEditBody is the card's editing form (design §2): glyph and
// name, no live controls — the widget cards collapse to their name so a
// pause button can't fire while the card wobbles. The music card's 2 × 2
// rectangle is the exception: its player is the card's whole content, so
// it keeps the live rows (inset past the ⊖ badge and the size handle),
// and only a press outside those rows starts a reorder. Two-span widgets
// carry the 小组件 subtitle the mock shows.
func (a *shelf) toolCardEditBody(c *ui.Context, card ui.Element, def *toolCardDef, spec layout.Card) {
	if def.id == "playing" && spec.Height() > 1 {
		a.toolPlayingTall(c, card, cardEdit)
		return
	}
	card.Center().Gap(5).Children(func() {
		ui.Icon(c, def.icon).Size(20, 20).TextColor(ink)
		fitText(c, def.name(loc(c)), 12, 600, toolColW(def.span)-8).TextColor(ink)
		if def.span == 2 && def.kind == cardWidget {
			panelLabel(c, nil, loc(c).Widgets(), 10, 0).TextColor(ink3)
		}
	})
}

// toolCardBody is the live content: action cards centre glyph + name +
// subtitle, widgets lay out per card. While files hover the panel the
// action's subtitle becomes the dragged count (design §1: 副标题换成拖
// 入的数量).
func (a *shelf) toolCardBody(c *ui.Context, card ui.Element, def *toolCardDef, sub string, spec layout.Card, surf toolCardSurface) {
	span, rows := spec.Cols(), spec.Height()
	if def.kind == cardAction {
		card.Center().Gap(5).Children(func() {
			ui.Icon(c, def.icon).Size(20, 20).TextColor(ink)
			fitText(c, def.name(loc(c)), 12, 600, toolColW(span)-8).TextColor(ink)
			if sub != "" {
				fitText(c, sub, 10, 0, toolColW(span)-8).TextColor(ink3)
			}
		})
		return
	}
	switch def.id {
	case "playing":
		if rows > 1 {
			a.toolPlayingTall(c, card, surf)
			return
		}
		a.toolPlayingBody(c, card)
	case "awake":
		a.toolAwakeBody(c, card, span)
	case "recent":
		a.toolRecentBody(c, card)
	case "clip":
		a.toolClipBody(c, card)
	case "usage":
		a.toolUsageBody(c, card, span)
	default:
		card.Center().Gap(5).Children(func() {
			ui.Icon(c, def.icon).Size(20, 20).TextColor(ink)
			panelLabel(c, nil, def.name(loc(c)), 12, 600).TextColor(ink)
		})
	}
}

// toolCardDragPaint reports whether a card is painted by the panel's file
// drag at all: dimming and the count subtitle belong to the panel's tools
// page, never to a card floating on the desktop. A pin redraws whenever it
// is invalidated, and a drag over the panel must leave it exactly as it is
// (its drop target registration is separate — see toolCard's accepts).
func toolCardDragPaint(surf toolCardSurface, live bool) bool {
	return live && surf != cardDesktop
}

// toolCardDimmed reports whether a card fades to 38% during a live drag:
// only an action card whose class the dragged files do not fit. Widgets
// never dim — they take no files and stay as they are (design §1: only
// the action cards answer a file drag).
func toolCardDimmed(def *toolCardDef, class dropClass) bool {
	return def.kind == cardAction && !def.accepts(class)
}

// toolCardDrag answers the tools page's live drag picture: the class of
// the files a drag holds over the panel, their count, and whether that
// drag is in flight right now. live reads a.dragging — the 30 ms monitor's
// own flag — not the snapshot alone: the snapshot outlives the release by
// dropHoldGrace so the drop still finds its cells, and a held set must not
// keep painting the page. A drag of ShelfU's own items never paints it.
func (a *shelf) toolCardDrag() (dropClass, int, bool) {
	n := len(a.drop.files)
	return a.drop.class, n, a.dragging && !a.ownDrag && n > 0
}

// toolCardSub is the action card's subtitle: the dragged file count
// while files the card accepts hover, or empty otherwise. The caller asks
// only during a live drag.
func (a *shelf) toolCardSub(l locales.Messages, def *toolCardDef, class dropClass) string {
	if len(a.drop.files) > 0 && def.accepts(class) {
		return dropCountTextIn(l, len(a.drop.files), class)
	}
	return ""
}

// toolCardDropAction is the drop zone cell a release on the card runs
// (design: 规则和投放区动作格相同). Images onto 转成 PDF merge into one
// PDF past the first; the other targets map 1:1.
func (a *shelf) toolCardDropAction(id string, paths []string) dropAction {
	l := a.appLoc()
	def := toolCardDefFor(id)
	if def == nil {
		return zipAction(l, len(paths))
	}
	switch id {
	case "ocr":
		// 识别文字并复制：不走转换管线，走 isOCRCopy 通道
		return dropAction{id: id, icon: def.icon, label: l.DropRecognizeCopy(), sub: dropCountTextIn(l, len(paths), classImages), isOCRCopy: true}
	case "fit1m":
		return dropAction{id: id, icon: def.icon, label: def.name(l), sub: l.DropWithin(), target: toFit1M}
	case "tojpeg":
		return dropAction{id: id, icon: def.icon, label: def.name(l), sub: l.DropPerFile(), target: toJPEG}
	case "resize":
		return dropAction{id: id, icon: def.icon, label: def.name(l), sub: l.DropResizeSub(), target: toMedium}
	case "topdf":
		if len(paths) > 1 {
			return dropAction{id: id, icon: def.icon, label: l.DropPdfMerged(), sub: l.DropPdfMergedSub(), target: toMergedPDF}
		}
		return dropAction{id: id, icon: def.icon, label: def.name(l), sub: l.DropPdfPage(), target: toPDF}
	case "toword":
		return dropAction{id: id, icon: def.icon, label: def.name(l), sub: l.DropRecognizeSub(), target: toWord}
	case "extract":
		sub := filepath.Base(paths[0])
		if len(paths) > 1 {
			sub = l.DropCountItems(len(paths))
		}
		return dropAction{id: id, icon: def.icon, label: def.name(l), sub: sub, target: toExtract}
	case "airdrop":
		return dropAction{id: id, icon: def.icon, label: def.name(l), isAirDrop: true}
	default: // zip and any future any-class action
		return zipAction(l, len(paths))
	}
}

// runToolCardDrop executes a release on a tools-page card using the
// panel's frozen drop snapshot. Desktop pins receive their own transfer
// data through pinReceive instead.
func (a *shelf) runToolCardDrop(id string, data transfer.Data) {
	def := toolCardDefFor(id)
	if def == nil || def.kind != cardAction {
		return
	}
	// The card answered the release: clear the drag snapshot so the hot
	// state and subtitles do not linger through the hold grace.
	a.drop.done = true
	files, class := a.drop.files, a.drop.class
	if len(files) == 0 {
		f, err := dropFilesData(data)
		if err != nil || len(f) == 0 {
			return
		}
		files, class = f, classifyDrop(f)
	}
	if !def.accepts(class) {
		return
	}
	a.runDropAction(a.toolCardDropAction(id, files), data)
}

// runToolCard runs the card's tap action: an action card converts or
// AirDrops the tray's current selection, the widget cards do their own
// toggle/copy/jump. It reports whether the tap did something — the page
// uses the answer to jiggle a card that had nothing to act on (design:
// 没有可用的选中项时卡片轻摇一下，提示「把文件拖到这里」).
func (a *shelf) runToolCard(id string) bool {
	def := toolCardDefFor(id)
	if def == nil {
		return false
	}
	switch def.id {
	case "ocr":
		a.startScreenOCR() // 开始框选（设计 §5）；面板会先收起
		return true
	case "playing", "recent":
		return true // the card acts only through its own controls/thumbs
	case "awake":
		m := a.awake
		if m == nil {
			return false
		}
		if m.active() {
			m.stop(time.Now())
		} else {
			m.start(time.Now(), time.Hour)
		}
		return true
	case "clip":
		if it, ok := a.latestClipboardItem(); ok {
			return a.copyText(it.Text)
		}
		return false
	case "usage":
		if a.usage != nil {
			a.setTab(pageUsage)
			return true
		}
		return false
	}
	items := a.toolCardTargets(id)
	if len(items) == 0 {
		return false
	}
	if id == "airdrop" {
		paths, _ := usable(items)
		if len(paths) == 0 {
			return false
		}
		if err := airDrop(paths, nil); err != nil {
			a.report(err)
		}
		return true
	}
	target := a.toolCardTarget(id, len(items))
	if target < 0 {
		return false
	}
	if a.conv != nil && a.conv.busy() {
		return false // the tap was valid; the one running job is taken
	}
	a.startConvert(items, target)
	return true // consumed: the job runs, or its failure already reported
}

// toolCardTarget is the conversion a tap on the action card starts;
// -1 where the card does not convert.
func (a *shelf) toolCardTarget(id string, n int) convertTarget {
	switch id {
	case "fit1m":
		return toFit1M
	case "tojpeg":
		return toJPEG
	case "resize":
		return toMedium
	case "topdf":
		if n > 1 {
			return toMergedPDF
		}
		return toPDF
	case "toword":
		return toWord
	case "extract":
		return toExtract
	case "zip":
		return toZip
	}
	return -1
}

// toolCardTargets is the tray selection an action card applies to,
// filtered to what the card can run on: the same class rules as a file
// drop (convertible images, PDFs, archives, or any on-disk item for zip
// and AirDrop). Nothing fitting means no selection the card could use.
func (a *shelf) toolCardTargets(id string) []store.Item {
	def := toolCardDefFor(id)
	if def == nil || def.kind != cardAction {
		return nil
	}
	var out []store.Item
	for _, it := range a.selection.chosen(a.itemsFor(pageTray)) {
		switch id {
		case "ocr", "fit1m", "tojpeg", "resize", "topdf":
			if convertible(it) {
				out = append(out, it)
			}
		case "toword":
			if convertible(it) || isPDF(it) {
				out = append(out, it)
			}
		case "extract":
			if isArchive(it) {
				out = append(out, it)
			}
		default: // zip, airdrop: any file still on disk
			if !it.Missing && (it.Kind == store.KindFile || it.Kind == store.KindImage) {
				out = append(out, it)
			}
		}
	}
	return out
}

// clipboardHistory is the clipboard page's list with no search: every
// capture the page shows, pinned first. A capture hidden from the page
// (OffClip) lives on the tray only, so the card does not surface it.
func (a *shelf) clipboardHistory() []store.Item {
	if a.store == nil {
		return nil
	}
	visible := make([]store.Item, 0, len(a.store.Items))
	for _, it := range a.store.Items {
		if it.Source != store.SourceClipboard || it.OffClip {
			continue
		}
		visible = append(visible, it)
	}
	return clipboardOrder(visible)
}

// latestClipboardItem is the newest clipboard capture (pinned first,
// like the clipboard page): the 剪贴板最新 card's payload.
func (a *shelf) latestClipboardItem() (store.Item, bool) {
	items := a.clipboardHistory()
	if len(items) == 0 {
		return store.Item{}, false
	}
	return items[0], true
}

// awakeRemain is the 保持唤醒 card's subtitle: the countdown for timed
// holds, 一直 for the open-ended one, 关 while off.
func (a *shelf) awakeRemain(l locales.Messages) string {
	m := a.awake
	if m == nil || !m.active() {
		return l.AwakeOff()
	}
	if m.duration == 0 {
		return l.AwakeAlways()
	}
	return l.ToolsAwakeRemain(m.remainText(time.Now()))
}

// toolAwakeBody is the 保持唤醒 widget: the 1-column form centres
// glyph + name + state with the on-dot in the corner; the 2-column form
// goes horizontal and adds the duration pills (design §3: 2 列时多出
// 30 分钟 / 1 / 2 / 4 小时 / 一直 选择).
func (a *shelf) toolAwakeBody(c *ui.Context, card ui.Element, span int) {
	if span == 1 {
		a.toolAwakeBodyNarrow(c, card)
		return
	}
	a.toolAwakeBodyWide(c, card)
}

// toolAwakeBodyNarrow is the 1-column form.
func (a *shelf) toolAwakeBodyNarrow(c *ui.Context, card ui.Element) {
	m := a.awake
	on := m != nil && m.active()
	card.Center().Gap(5).Children(func() {
		ui.Icon(c, cupSymbol).Size(20, 20).TextColor(ink)
		panelLabel(c, nil, loc(c).KeepAwake(), 12, 600).TextColor(ink)
		panelLabel(c, nil, a.awakeRemain(loc(c)), 10, 0).TextColor(ink3)
		if on {
			ui.Box(c.Key("on-dot")).Absolute().Top(8).Right(8).Size(6, 6).Radius(3).Background(awakeWarn)
		}
	})
}

// toolAwakeBodyWide is the 2-column form, adding the duration pills.
func (a *shelf) toolAwakeBodyWide(c *ui.Context, card ui.Element) {
	m := a.awake
	on := m != nil && m.active()
	card.Padding(0, 12, 0, 12).Justify(ui.Center).Children(func() {
		ui.Column(c).FillWidth().Children(func() {
			ui.Row(c).AlignItems(ui.Center).Gap(10).FillWidth().Children(func() {
				ui.Icon(c, cupSymbol).Size(20, 20).TextColor(ink)
				ui.Column(c).Grow(1).MinWidth(0).Gap(2).Children(func() {
					panelLabel(c, nil, loc(c).KeepAwake(), 12, 600).TextColor(ink)
					panelLabel(c, nil, a.awakeRemain(loc(c)), 10, 0).TextColor(ink3)
				})
				if on {
					ui.Box(c.Key("on-dot")).Size(6, 6).Radius(3).Background(awakeWarn)
				}
			})
			ui.Row(c.Key("awake-chips")).Gap(6).Margin(8, 0, 0, 0).Children(func() {
				for _, d := range awakeChipSpans {
					a.awakeChip(c, fmt.Sprintf("chip-%d", d/time.Minute), awakeChipLabel(loc(c), d), d)
				}
			})
		})
	})
}

// awakeChipSpans are the 保持唤醒 cards' duration pills, shortest first;
// 0 is 一直. The tray menu lists the same choices in its own file.
var awakeChipSpans = []time.Duration{30 * time.Minute, time.Hour, 2 * time.Hour, 4 * time.Hour, 0}

// awakeChipLabel is the pill name of span d.
func awakeChipLabel(l locales.Messages, d time.Duration) string {
	switch d {
	case 30 * time.Minute:
		return l.Awake30m()
	case time.Hour:
		return l.Awake1h()
	case 2 * time.Hour:
		return l.Awake2h()
	case 4 * time.Hour:
		return l.Awake4h()
	}
	return l.AwakeAlways()
}

// awakeChip is one duration pill of the 2-column 保持唤醒 card; the held
// span (or 一直) gets the accent fill.
func (a *shelf) awakeChip(c *ui.Context, key, label string, d time.Duration) {
	m := a.awake
	on := m != nil && m.active() && m.duration == d
	chip := ui.Row(c.Key(key)).Height(20).Padding(0, 9, 0, 9).Radius(10).AlignItems(ui.Center).Label(label)
	if on {
		chip.Background(accent)
	} else {
		chip.Background(fill2)
	}
	chip.Children(func() {
		t := panelLabel(c, nil, label, 10, 600)
		if on {
			t.TextColor(ink)
		} else {
			t.TextColor(ink2)
		}
	})
	chip.HandleInput(func(ev ui.InputEvent) bool {
		// The chip answers the press, not the card: the card's
		// HandleInput is the innermost otherwise and swallows it,
		// so a tap on 30 分钟 would toggle one hour instead.
		return ev.Button == 0 && ev.Kind >= ui.InputPointerDown && ev.Kind <= ui.InputPointerMove
	})
	if chip.Clicked() && m != nil {
		if on {
			m.stop(time.Now())
		} else {
			m.start(time.Now(), d)
		}
	}
}

// toolRecentBody is the 最近文件 widget: title + hint on the left, up to
// three newest tray thumbnails on the right; each thumb drags out like
// its tray tile (design §3).
func (a *shelf) toolRecentBody(c *ui.Context, card ui.Element) {
	card.Padding(0, 10, 0, 10).Justify(ui.Center).Children(func() {
		ui.Row(c).AlignItems(ui.Center).Gap(8).FillWidth().Children(func() {
			ui.Column(c).Grow(1).MinWidth(0).Gap(2).Children(func() {
				panelLabel(c, nil, loc(c).RecentFiles(), 12, 600).TextColor(ink)
				panelLabel(c, nil, loc(c).DragOut(), 10, 0).TextColor(ink3)
			})
			ui.Row(c.Key("recent-thumbs")).Gap(4).AlignItems(ui.Center).Children(func() {
				items := a.itemsFor(pageTray)
				show := items[:min(3, len(items))]
				for _, it := range show {
					a.recentThumbAt(c, it, 30)
				}
				if len(show) == 0 {
					ui.Icon(c, docSymbol).Size(20, 20).TextColor(ink3)
				}
			})
		})
	})
}

// recentThumb is one 40pt tray file thumbnail on the card: it drags out
// exactly like the tray tile (the copy lands in drag-out/, original
// untouched); 拖出后从托盘移除 does not apply — the card is not the tray
// page.
func (a *shelf) recentThumb(c *ui.Context, item store.Item) {
	a.recentThumbAt(c, item, 40)
}

// recentThumbAt is recentThumb for a widget-driven size (the home canvas
// uses 24/26pt thumbs).
func (a *shelf) recentThumbAt(c *ui.Context, item store.Item, size float32) {
	thumb := ui.Row(c.Key(fmt.Sprintf("recent-%s-%g", item.ID, size))).Size(size, size).Radius(8).Center().Clip().
		Label(itemTitle(item))
	if !item.Missing {
		thumb.HandleInput(func(ev ui.InputEvent) bool {
			// The drag starts on the thumbnail itself: let the card's
			// press handler take the down and dragMove never sees this
			// DragDataFrom.
			return ev.Button == 0 && ev.Kind >= ui.InputPointerDown && ev.Kind <= ui.InputPointerMove
		})
		thumb.DragDataFrom(func() transfer.Data {
			a.beginOwnDrag()
			a.takeServiceOwnership()
			if a.win != nil {
				a.win.Invalidate()
			}
			a.dragOutIDs = nil // a widget card is not the tray page: no removal
			return a.dragOut(item)
		}, transfer.DragOptions{Operations: transfer.Copy, Done: a.completed})
	}
	if b := a.thumbnail(item); b != nil {
		thumb.Children(func() {
			ui.Image(c.Key("bmp"), b).Size(size, size).Fit(ui.Cover)
		})
	} else {
		thumb.Background(fill2).Children(func() {
			ui.Icon(c, fileSymbol).Size(size*0.45, size*0.45).TextColor(ink3)
		})
	}
}

// toolClipBody is the 剪贴板最新 widget: the newest capture's text (or
// the screenshot title), with 点按复制 as the hint. The tap is wired by
// the surface (runToolCard).
func (a *shelf) toolClipBody(c *ui.Context, card ui.Element) {
	card.Padding(0, 12, 0, 12).Justify(ui.Center).Children(func() {
		ui.Row(c).AlignItems(ui.Center).Gap(10).FillWidth().Children(func() {
			ui.Row(c.Key("clip-ic")).Size(36, 36).Radius(10).Background(fill2).Center().Children(func() {
				ui.Icon(c, clipNavSymbol).Size(16, 16).TextColor(ink2)
			})
			ui.Column(c).Grow(1).MinWidth(0).Gap(2).Children(func() {
				if it, ok := a.latestClipboardItem(); ok {
					ui.Text(c, itemTitle(it)).SingleLine().FontSize(12).FontWeight(600).TextColor(ink)
					panelLabel(c, nil, loc(c).TapToCopy(), 10, 0).TextColor(ink3)
				} else {
					panelLabel(c, nil, loc(c).LatestClip(), 12, 600).TextColor(ink)
					panelLabel(c, nil, loc(c).NoClipItems(), 10, 0).TextColor(ink3)
				}
			})
		})
	})
}

// toolUsageBody is the AI 用量 widget. The 1 × 1 card is only the
// least-remaining tool's double ring — the page's quota ring at 64pt,
// centred, no text at all. The 2 × 1 card keeps the ring beside the name
// and the remaining percent, as it always has. With no quota data the
// rings are bare and the mark is the gauge (design §3).
func (a *shelf) toolUsageBody(c *ui.Context, card ui.Element, span int) {
	var five, week *aiusage.Window
	var tool aiusage.Tool
	stale, ok := false, false
	if a.usage != nil {
		five, week, tool, ok = a.usage.leastWindow()
		if ok {
			_, stale = a.usage.quotaShown(tool)
		}
	}
	if span < 2 {
		card.Center().Children(func() {
			a.toolUsageRing(c, toolUsageRingSize, tool, five, week, stale, ok)
		})
		return
	}
	card.Padding(0, 12, 0, 12).Justify(ui.Center).Children(func() {
		ui.Row(c).AlignItems(ui.Center).Gap(10).FillWidth().Children(func() {
			a.toolUsageRing(c, toolUsageRingSizeWide, tool, five, week, false, false)
			ui.Column(c).Grow(1).MinWidth(0).Gap(2).Children(func() {
				panelLabel(c, nil, loc(c).AiUsage(), 12, 600).TextColor(ink)
				sub := loc(c).NoData()
				if ok {
					w := five
					if w == nil {
						w = week
					}
					sub = loc(c).PercentLeft(fmt.Sprintf("%.0f", max(0, 100-w.UsedPct)))
				}
				panelLabel(c, nil, sub, 10, 0).TextColor(ink3)
			})
		})
	})
}

// toolUsageRingSize is the 1 × 1 card's ring diameter: the page's 64pt
// quota ring at k = 1, which leaves 10pt above and below in the 84pt cell.
// The 2 × 1 card keeps its shipped 44pt ring, metrics rounded to halves.
const (
	toolUsageRingSize     = 64
	toolUsageRingSizeWide = 44
)

// toolUsageRing draws the card's double ring at d, scaled from the page's
// 64pt one (28 / 20pt radii, 5pt stroke, white even-pace tick): weekly
// outside, 5-hour inside. mark puts the selected tool's own mark in the
// middle; without it — and whenever there is no quota data — the gauge
// does. A stale fetch dims the arcs as the quota card does.
func (a *shelf) toolUsageRing(c *ui.Context, d float32, tool aiusage.Tool, five, week *aiusage.Window, stale, mark bool) {
	k := d / 64
	outer, inner, lw, gauge := 28*k, 20*k, 5*k, 20*k
	if d != toolUsageRingSize {
		outer, inner, lw, gauge = 19, 13.5, 3.5, 14 // the 2 × 1 card's ring, unchanged
	}
	e := ui.Box(c.Key("tc-ring")).Size(d, d).Center()
	e.Draw(func(p *ui.Painter, r ui.Rect) {
		cx, cy := r.X+r.W/2, r.Y+r.H/2
		if a.usage == nil {
			// No usage feature at all: the 1 × 1 card still shows its
			// tracks under the gauge (design §3); the 2 × 1 kept none.
			if d == toolUsageRingSize {
				strokeRingTrack(p, cx, cy, outer, lw)
				strokeRingTrack(p, cx, cy, inner, lw)
			}
			return
		}
		a.usage.drawWindowRing(p, r, outer, lw, week, usageWeekly, stale, true)
		a.usage.drawWindowRing(p, r, inner, lw, five, usageFive, stale, true)
	})
	e.Children(func() {
		if !mark {
			ui.Icon(c, gaugeSymbol).Size(gauge, gauge).TextColor(ink3)
			return
		}
		logo, col, box := toolLogo(tool)
		ui.Icon(c, logo).Size(box*k, box*k).TextColor(col)
	})
}

// usageCardLabel is the AI 用量 card's accessible name and hover tip: the
// least-remaining tool, then each window's shown share — used, or left
// when the setting asks — or 暂无数据 before any quota answer.
func (a *shelf) usageCardLabel(c *ui.Context) string {
	l := loc(c)
	if a.usage == nil {
		return l.AiUsage() + " · " + l.NoData()
	}
	five, week, tool, ok := a.usage.leastWindow()
	if !ok {
		return l.AiUsage() + " · " + l.NoData()
	}
	parts := []string{toolName(tool)}
	if five != nil {
		parts = append(parts, l.UsageWindowFive()+" "+a.usage.shownShort(l, *five))
	}
	if week != nil {
		parts = append(parts, l.UsageWindowWeekly()+" "+a.usage.shownShort(l, *week))
	}
	return strings.Join(parts, " · ")
}

// toolPlayingTall is the music card in its 2 × 2 form: the full player —
// cover and two lines, the seekable progress row, the transport and the
// volume row (design 主页画布 · 音乐 2 × 2). The grid's rectangle is 205 ×
// 174, so the rows stack down the card; the pinned window is 278 × 84,
// so they sit in a column beside the cover. Both rows take the pointer
// themselves, so a drag on a rail never becomes a reorder or a drag out.
// The editing form draws the same player, inset past its ⊖ badge and
// size handle.
func (a *shelf) toolPlayingTall(c *ui.Context, card ui.Element, surf toolCardSurface) {
	m := a.np
	st, ok := mac.NowPlayingState{}, false
	if m != nil {
		st, ok = m.track()
	}
	if !ok {
		card.Center().Gap(6).Children(func() {
			ui.Icon(c, musicSymbol).Size(24, 24).TextColor(ink3)
			panelLabel(c, nil, loc(c).NowPlaying(), 12, 600).TextColor(ink3)
		})
		return
	}
	if surf == cardDesktop {
		a.toolPlayingWide(c, card, st)
		return
	}
	// The editing chrome sits in the card's corners: the ⊖ badge at 2,2
	// and the size handle at the bottom right. The player keeps clear of
	// both, or the badge would sit on the cover and the handle on the
	// speaker.
	const padTop, padRight, padBottom, padLeft = 10, 12, 10, 12
	if surf == cardEdit {
		card.Padding(22, 20, 14, 20)
	} else {
		card.Padding(padTop, padRight, padBottom, padLeft)
	}
	card.Justify(ui.SpaceBetween).Children(func() {
		ui.Row(c.Key("tc-np-top")).AlignItems(ui.Center).Gap(9).FillWidth().Children(func() {
			m.coverArt(c, "tc-np-art", 44, 10)
			ui.Column(c).Grow(1).MinWidth(0).Gap(2).Justify(ui.Center).Children(func() {
				ui.Text(c, st.Title).SingleLine().FontSize(12.5).FontWeight(600).TextColor(ink)
				if meta := m.cardMeta(st); meta != "" {
					ui.Text(c, meta).SingleLine().FontSize(10.5).TextColor(ink3)
				}
			})
		})
		a.toolPlayingTallBars(c, st, 18)
	})
}

// toolPlayingWide is the same player in the pinned window: the cover and
// its two lines on the left, the seek / transport / volume column beside
// them, in the 84pt the pin gives every card.
func (a *shelf) toolPlayingWide(c *ui.Context, card ui.Element, st mac.NowPlayingState) {
	m := a.np
	card.Padding(8, 10, 8, 10).Justify(ui.Center).Children(func() {
		ui.Row(c.Key("tc-np-wide")).AlignItems(ui.Center).Gap(8).FillWidth().Children(func() {
			m.coverArt(c, "tc-np-w-art", 40, 9)
			ui.Column(c).Grow(1).MinWidth(0).Gap(2).Justify(ui.Center).Children(func() {
				// The pin's window is the width the 2 × 1 card uses, so its
				// two lines keep that card's sizes: 12.5 would push 「歌手 ·
				// 应用」 into an ellipsis.
				ui.Text(c, st.Title).SingleLine().FontSize(12).FontWeight(600).TextColor(ink)
				if meta := m.cardMeta(st); meta != "" {
					ui.Text(c, meta).SingleLine().FontSize(10).TextColor(ink3)
				}
			})
			// 98pt of column keeps the two clocks and the transport, and
			// leaves the two lines the 104pt 「歌手 · 应用」 needs.
			ui.Column(c.Key("tc-np-w-bars")).Width(98).Gap(6).Children(func() {
				a.toolPlayingTallBars(c, st, 8)
			})
		})
	})
}

// toolPlayingTallBars draws the player's three control rows — the
// progress row with its two clocks, the transport and the volume row —
// which the 2 × 2 card stacks and the pinned window's column holds. The
// row sits at the width its parent gives it; ctrlGap is the transport's
// spacing, tighter in the pin's column.
func (a *shelf) toolPlayingTallBars(c *ui.Context, st mac.NowPlayingState, ctrlGap float32) {
	m := a.np
	now := c.Now()
	dur := st.Duration
	pos := st.Position(now)
	if dur > 0 {
		pos = min(max(pos, 0), dur)
	}
	frac := float32(0)
	if dur > 0 {
		frac = float32(pos / dur)
	}
	if a.music.seek.drag && dur > 0 {
		frac = a.music.seek.frac
		pos = float64(frac) * dur
	}
	ui.Row(c.Key("tc-np-track")).AlignItems(ui.Center).Gap(6).FillWidth().Children(func() {
		rem := float64(0)
		if dur > 0 {
			rem = max(0, dur-pos)
		}
		// The labels are as wide as their own text at this font: a fixed
		// width would cut -10:00 and 1:02:03.
		nowLabel, remLabel := mac.FormatClock(pos), "-"+mac.FormatClock(rem)
		nw, rw := clockLabelW(c, nowLabel, 9.5), clockLabelW(c, remLabel, 9.5)
		panelLabel(c, nil, nowLabel, 9.5, 500).TextColor(ink3).Width(nw)
		a.seekRail(c, "tc-np-progress", frac, dur, 12, 4).Grow(1).MinWidth(0)
		panelLabel(c, nil, remLabel, 9.5, 500).TextColor(ink3).Width(rw)
	})
	ui.Row(c.Key("tc-np-ctl-tall")).Justify(ui.Center).AlignItems(ui.Center).Gap(ctrlGap).FillWidth().Children(func() {
		m.controlButton(c, "tc-np-prev", prevFill, loc(c).PreviousTrack(), mac.NPPrevious, 26, 15)
		m.controlButton(c, "tc-np-toggle", toggleSymbol(st.Playing), playPauseLabel(st.Playing), mac.NPPlayPause, 30, 19)
		m.controlButton(c, "tc-np-next", nextFill, loc(c).NextTrack(), mac.NPNext, 26, 15)
	})
	a.volumeRow(c, "tc-np-vol", 12, 6)
}

// toolPlayingBody is the 正在播放 widget: 52pt cover, title and
// 歌手 · 应用 meta, and the transport buttons — the activity strip's
// parts in card form (design §3: 按钮就地控制). No track collapses to a
// quiet name-only card.
func (a *shelf) toolPlayingBody(c *ui.Context, card ui.Element) {
	m := a.np
	if m == nil {
		card.Center().Gap(5).Children(func() {
			ui.Icon(c, musicSymbol).Size(20, 20).TextColor(ink3)
			panelLabel(c, nil, loc(c).NowPlaying(), 12, 600).TextColor(ink3)
		})
		return
	}
	st, ok := m.track()
	if !ok {
		card.Center().Gap(5).Children(func() {
			ui.Icon(c, musicSymbol).Size(20, 20).TextColor(ink3)
			panelLabel(c, nil, loc(c).NowPlaying(), 12, 600).TextColor(ink)
			panelLabel(c, nil, loc(c).NothingPlaying(), 10, 0).TextColor(ink3)
		})
		return
	}
	card.Padding(0, 12, 0, 12).Justify(ui.Center).Children(func() {
		ui.Row(c).AlignItems(ui.Center).Gap(10).FillWidth().Children(func() {
			m.coverArt(c, "tc-np-art", 52, 10)
			ui.Column(c).Grow(1).MinWidth(0).Gap(2).Justify(ui.Center).Children(func() {
				ui.Text(c, st.Title).SingleLine().FontSize(12).FontWeight(600).TextColor(ink)
				if meta := m.cardMeta(st); meta != "" {
					ui.Text(c, meta).SingleLine().FontSize(10).TextColor(ink3)
				}
			})
			ui.Row(c.Key("tc-np-ctl")).Gap(10).AlignItems(ui.Center).Children(func() {
				m.controlButton(c, "tc-np-prev", prevFill, loc(c).PreviousTrack(), mac.NPPrevious, 15, 15)
				m.controlButton(c, "tc-np-toggle", toggleSymbol(st.Playing), playPauseLabel(st.Playing), mac.NPPlayPause, 15, 15)
				m.controlButton(c, "tc-np-next", nextFill, loc(c).NextTrack(), mac.NPNext, 15, 15)
			})
		})
	})
}
