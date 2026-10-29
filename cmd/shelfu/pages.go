//go:build darwin

package main

import (
	"strings"

	"github.com/egoist/mygo/ui"

	"shelf/internal/store"
	"shelf/locales"
)

// pageID names one page of the expanded panel. Pages are self-contained
// (a panelPage holds its own host), so there is no registry map to keep in
// sync: a page joins by registerPage and leaves by returning false from
// enabled.
type pageID string

const (
	pageOverview  pageID = "overview"  // 总览：点刘海直接到的一页
	pageTray      pageID = "tray"      // 托盘：全部内容
	pageClipboard pageID = "clipboard" // 剪贴板：只显示剪贴板收录的文字与图片
	pagePlaying   pageID = "playing"   // 正在播放：音乐小组件的整页版
	pageUsage     pageID = "usage"     // AI 用量（由 AI 用量切片实现并登记）
)

// panelPage is one page of the expanded sheet. Every page draws into the
// same fixed 416 × 174 content box and scrolls inside it when it has more
// to show; the shell never changes size for a page. live is false while
// the page is only the outgoing picture of a fade — drawn, but never
// taking the pointer or the keyboard; over is true while a drag hovers the
// panel. icon's active means the button is highlighted (the current page
// or a hovered icon).
type panelPage interface {
	id() pageID
	label() string // tooltip and accessible name of the page icon
	icon(c *ui.Context, active bool) ui.Element
	enabled() bool                                       // false keeps the icon off the panel and falls the current page back to the tray
	body(c *ui.Context, over bool, live bool) ui.Element // the fixed 416 × 174 content
	// title is the back row's plain page name. Its slot is as wide as the
	// widest enabled page name, so the icons behind it never move.
	title() string
	actions(c *ui.Context) []ui.Element // the back row's right side, appended in order
}

// pageText is a page whose name it says in a given language. label and
// title carry no context, so the panel asks for the name in the language
// of its window here; a page without it falls back to its own label and
// title (see pageLabel and pageTitle in panel.go).
type pageText interface {
	labelIn(l locales.Messages) string
	titleIn(l locales.Messages) string
}

// ensurePages registers the built-in pages once; later pages are appended
// by registerPage in ear order. Pages assigned to a.pages before the first
// use (a test replacing the registry) keep their ids: a tray or clipboard
// page among them takes the built-in's seat.
func (a *shelf) ensurePages() {
	if a.pagesReady {
		return
	}
	a.pagesReady = true
	extra := a.pages
	a.pages = []panelPage{&overviewPage{host: a}, &trayPage{host: a}, &clipboardPage{host: a}, &toolsPage{host: a}, &playingPage{host: a}}
	for _, p := range extra {
		a.registerPage(p)
	}
}

// registerPage adds p to the panel's page list, after the pages already
// registered; a page registered under an existing id replaces it (the
// order and ear position are kept). Built-in pages are registered lazily
// on first use so tests that build a bare &shelf{} get the same registry
// as the app.
func (a *shelf) registerPage(p panelPage) {
	a.ensurePages()
	for i, q := range a.pages {
		if q.id() == p.id() {
			a.pages[i] = p
			return
		}
	}
	a.pages = append(a.pages, p)
}

// enabledPages are the registered pages whose icon is on the ear bar, in
// ear order.
func (a *shelf) enabledPages() []panelPage {
	a.ensurePages()
	var out []panelPage
	for _, p := range a.pages {
		if p.enabled() {
			out = append(out, p)
		}
	}
	return out
}

// page returns the registered page with id, nil when none is registered
// (a disabled page still answers — enabled is what drops it).
func (a *shelf) page(id pageID) panelPage {
	a.ensurePages()
	for _, p := range a.pages {
		if p.id() == id {
			return p
		}
	}
	return nil
}

// pageIndex is id's position in the registered order; the ear icons share
// that order. -1 when no such page is registered.
func (a *shelf) pageIndex(id pageID) int {
	a.ensurePages()
	for i, p := range a.pages {
		if p.id() == id {
			return i
		}
	}
	return -1
}

// currentPage is the page the panel shows now: the overview while the shell
// is at its short size, otherwise a.tab's page when it is registered and
// enabled, falling back to the tray — a page that gets switched off (e.g.
// AI 用量's setting) never stays current.
func (a *shelf) currentPage() panelPage {
	if a.currentLevel() == 1 && !a.snap.panel && !a.snap.drawn {
		if p := a.page(pageOverview); p != nil && p.enabled() {
			return p
		}
	}
	if p := a.page(a.tab); p != nil && p.enabled() {
		return p
	}
	if p := a.page(pageTray); p != nil {
		return p
	}
	return &trayPage{host: a}
}

// normalizeTab folds a stale a.tab (unset, or the id of a page that is now
// disabled or unregistered) back to the tray, and a.tab to the overview
// while the shell is at its short size. The switch is silent: no fade, no
// outgoing page.
func (a *shelf) normalizeTab() {
	// The arrangement panel borrows the short box but keeps the page
	// underneath: it never switches pages.
	if a.currentLevel() == 1 && !a.snap.panel && !a.snap.drawn {
		if p := a.page(pageOverview); p != nil && p.enabled() {
			a.tab, a.tabFrom = pageOverview, pageOverview
			return
		}
	}
	if p := a.page(a.tab); p != nil && p.enabled() {
		return
	}
	a.tab, a.tabFrom = pageTray, pageTray
	a.selection.keepExisting(a.visibleItems())
}

// trayPage is the panel's home page: the 5-column thumbnail grid of every
// stored item except ones hidden from the tray.
type trayPage struct {
	host   *shelf
	scroll ui.ScrollState // where the grid is scrolled to
}

func (p *trayPage) id() pageID                        { return pageTray }
func (p *trayPage) label() string                     { return locales.PageTray() }
func (p *trayPage) labelIn(l locales.Messages) string { return l.PageTray() }
func (p *trayPage) icon(c *ui.Context, _ bool) ui.Element {
	return ui.Icon(c, trayNavSymbol)
}
func (p *trayPage) enabled() bool { return true }

// items is the page's list: everything but captures hidden from the tray
// (OffTray, or a drag-out removal still in its grace); pinned first.
func (p *trayPage) items() []store.Item {
	a := p.host
	if a.store == nil {
		return nil
	}
	var pinned, rest []store.Item
	for _, it := range a.store.Items {
		// 托盘页的移除对剪贴板收录只做到「不在托盘显示」：标了 OffTray 的
		// 项目仍留在剪贴板页，托盘不再列出。
		if it.OffTray || a.isLeaving(it) {
			continue
		}
		// 设计决定：剪贴板收录的文字只属于剪贴板标签；托盘放文件、
		// 文件夹、图片（含剪贴板截图）以及拖入/服务收录/置顶的文字。
		if it.Source == store.SourceClipboard && it.Kind == store.KindText && !it.Pinned {
			continue
		}
		if it.Pinned {
			pinned = append(pinned, it)
		} else {
			rest = append(rest, it)
		}
	}
	return append(pinned, rest...)
}

func (p *trayPage) body(c *ui.Context, _ bool, _ bool) ui.Element {
	a := p.host
	list := p.items()
	if len(list) == 0 {
		l := loc(c)
		return a.emptyState(c, shelfSymbol, l.PageTrayEmptyTitle(), l.PageTrayEmptyHint())
	}
	rows := (len(list) + gridCols - 1) / gridCols
	listH := float32(rows*(tileH+gapGrid) - gapGrid)
	top, bottom, viewH := scrollEdges(&p.scroll, listH)
	band := edgeBandFor(tileH)
	return ui.Column(c.Key("tray-stage")).FillWidth().FillHeight().Children(func() {
		ui.Scroll(c.Key("items")).Grow(1).MinHeight(0).TrackScroll(&p.scroll).Children(func() {
			ui.Grid(c.Key("grid")).ColumnTracks(ui.Fr(1), ui.Fr(1), ui.Fr(1), ui.Fr(1), ui.Fr(1)).
				Gap(gapGrid).FillWidth().Children(func() {
				// 快照：右键菜单或拖入回调可改变原切片。
				for i, item := range list {
					rowTop := float32(i/gridCols*(tileH+gapGrid)) - p.scroll.Y
					a.fadedRow(c, item.ID, rowTop, tileH, viewH, top, bottom, band, func() {
						a.itemTile(c, item)
					})
				}
			})
		})
	})
}

func (p *trayPage) title() string                     { return locales.PageTray() }
func (p *trayPage) titleIn(l locales.Messages) string { return l.PageTray() }

func (p *trayPage) actions(c *ui.Context) []ui.Element {
	a := p.host
	l := loc(c)
	live := a.tab == pageTray
	list := p.items()
	var out []ui.Element
	if len(list) > 0 {
		hint := l.PageMultiHint()
		if n := a.selection.count(); n > 1 {
			hint = l.PageSelectedCount(n)
		}
		out = append(out, ui.Text(c.Key("multi-hint"), hint).SingleLine().FontSize(10.5).FontWeight(600).TextColor(ink3))
	}
	if gone := a.clearable(list, p.id()); len(gone) > 0 {
		b := a.iconButton(c, "clear", trashSymbol, l.PageClear())
		if b.Clicked() && live {
			a.removeFrom(gone, p.id())
		}
		out = append(out, b)
	}
	return out
}

// clipboardPage is the capture history: 30pt rows (16pt source-app icon,
// text, 「应用 · 时间」 on the right). Only clipboard
// captures matching the search text are listed.
type clipboardPage struct {
	host   *shelf
	scroll ui.ScrollState // where the list is scrolled to
}

func (p *clipboardPage) id() pageID                        { return pageClipboard }
func (p *clipboardPage) label() string                     { return locales.PageClipboard() }
func (p *clipboardPage) labelIn(l locales.Messages) string { return l.PageClipboard() }
func (p *clipboardPage) icon(c *ui.Context, _ bool) ui.Element {
	return ui.Icon(c, clipNavSymbol)
}
func (p *clipboardPage) enabled() bool { return true }

// items is the page's list: clipboard captures matching the (already
// trimmed) search text; pinned first. Captures removed from this page
// (OffClip) are gone from it while they stay on the tray.
func (p *clipboardPage) items() []store.Item {
	a := p.host
	if a.store == nil {
		return nil
	}
	query := strings.ToLower(strings.TrimSpace(a.search))
	visible := make([]store.Item, 0, len(a.store.Items))
	for _, it := range a.store.Items {
		if it.Source != store.SourceClipboard || it.OffClip || !clipboardItemMatches(it, query) {
			continue
		}
		visible = append(visible, it)
	}
	return clipboardOrder(visible)
}

// clipboardOrder is the capture list's order: pinned first, newest first
// within each group. The page and the 剪贴板最新 card share it, so the
// card shows what the page shows.
func clipboardOrder(items []store.Item) []store.Item {
	out := make([]store.Item, 0, len(items))
	for _, it := range items {
		if it.Pinned {
			out = append(out, it)
		}
	}
	for _, it := range items {
		if !it.Pinned {
			out = append(out, it)
		}
	}
	return out
}

func (p *clipboardPage) body(c *ui.Context, _ bool, _ bool) ui.Element {
	a := p.host
	list := p.items()
	if len(list) == 0 {
		l := loc(c)
		icon, title, hint := textSymbol, l.PageClipEmptyTitle(), l.PageClipEmptyHint()
		if a.search != "" {
			icon, title, hint = searchSymbol, l.PageClipNoMatchTitle(), l.PageClipNoMatchHint()
		}
		return a.emptyState(c, icon, title, hint)
	}
	listH := float32(len(list)*(clipRowH+clipRowGap) - clipRowGap)
	top, bottom, viewH := scrollEdges(&p.scroll, listH)
	band := edgeBandFor(clipRowH)
	return ui.Column(c.Key("clip-stage")).FillWidth().FillHeight().Children(func() {
		ui.Scroll(c.Key("items")).Grow(1).MinHeight(0).TrackScroll(&p.scroll).Children(func() {
			ui.Column(c.Key("clip-list")).Gap(clipRowGap).FillWidth().Children(func() {
				for i, item := range list {
					rowTop := float32(i*(clipRowH+clipRowGap)) - p.scroll.Y
					a.fadedRow(c, item.ID, rowTop, clipRowH, viewH, top, bottom, band, func() {
						a.clipRow(c, item)
					})
				}
			})
		})
	})
}

func (p *clipboardPage) title() string                     { return locales.PageClipboard() }
func (p *clipboardPage) titleIn(l locales.Messages) string { return l.PageClipboard() }

// actions is the back row's right side: the compact search box and, when
// there is something to clear, the trash button. While the page is the
// outgoing picture the box is drawn without an input.
func (p *clipboardPage) actions(c *ui.Context) []ui.Element {
	a := p.host
	out := []ui.Element{p.host.searchField(c, p.host.tab == pageClipboard)}
	if gone := a.clearable(p.items(), p.id()); len(gone) > 0 {
		b := a.iconButton(c, "clear", trashSymbol, loc(c).PageClear())
		if b.Clicked() && a.tab == pageClipboard {
			a.removeFrom(gone, p.id())
		}
		out = append(out, b)
	}
	return out
}

// Progressive edge blur: a scrolling page does not cut its rows off at a
// hard line. The last edgeBand points toward an edge that still has
// content beyond it blur, most at the edge itself and less the further in
// a row sits, so rows melt into the shell as they scroll out. There is no
// overlay and no dimming: a row fully in the open stays exactly as it is.
const (
	// edgeBandMax caps the band at 24pt, and no band is deeper than half
	// a row: the 174pt content box is short, and the old 34pt band could
	// swallow a whole 30pt clipboard row at once.
	edgeBandMax = 24
	// edgeBlurPerPT is the blur sigma at the very edge per point of band:
	// 2.88 at a 24pt band, 1.8 at the clipboard's 15pt.
	edgeBlurPerPT = 0.12
)

// edgeBandFor is how deep the melt reaches for rows rowH tall.
func edgeBandFor(rowH float32) float32 { return min(edgeBandMax, rowH/2) }

// scrollEdges reports which melts a scroll container shows now — the top
// while its content has scrolled down, the bottom while more is below —
// and the viewport height both are measured in. MaxY is what the last
// layout made scrollable, so a list that fits shows neither. contentH is
// the list's full height, not the viewport's.
func scrollEdges(st *ui.ScrollState, contentH float32) (top, bottom bool, viewH float32) {
	viewH = contentH - st.MaxY
	if viewH <= 0 {
		viewH = contentH
	}
	return st.Y > 0.5, st.MaxY > 0.5 && st.Y < st.MaxY-0.5, viewH
}

// edgeFade is how strong the blur is d points into the band: 1 at the
// edge itself, 0 at its inner end, smooth in between (and 1 outside the
// viewport, for a row that is only partly visible).
func edgeFade(d, band float32) float32 {
	if d <= 0 {
		return 1
	}
	if band <= 0 || d >= band {
		return 0
	}
	t := 1 - d/band
	return t * t * (3 - 2*t)
}

// rowFade is the blur weight of one row: 0 unless it is inside the
// viewport and reaches into an edge that still has content beyond it.
func rowFade(rowTop, rowH, viewH float32, top, bottom bool, band float32) float32 {
	if rowTop+rowH <= 0 || rowTop >= viewH {
		return 0
	}
	var t float32
	if top {
		t = max(t, edgeFade(rowTop, band))
	}
	if bottom {
		t = max(t, edgeFade(viewH-(rowTop+rowH), band))
	}
	return t
}

// fadedRow wraps one scrolling row (a tile's cell, a clipboard row) so the
// edge blur follows it as it scrolls; the row itself keeps its key and
// behaviour. A page switch pauses the blur: both pages are moving and
// re-rasterising blurred rows for 240ms would cost more than the fade
// shows.
func (a *shelf) fadedRow(c *ui.Context, id string, rowTop, rowH, viewH float32, top, bottom bool, band float32, children func()) {
	row := ui.Column(c.Key(id + "-fade")).Height(rowH)
	// A row that covers the whole viewport (a 2 × 2 widget card, say) is
	// never an edge row: it is the page, not content melting into it.
	edgeRow := rowH < viewH*0.75
	if a.win == nil || a.tabPhase(c.Now()) >= 1 {
		if t := rowFade(rowTop, rowH, viewH, top, bottom, band); edgeRow && t > 0 {
			row.Blur(band * edgeBlurPerPT * t)
		}
	}
	row.Children(children)
}
