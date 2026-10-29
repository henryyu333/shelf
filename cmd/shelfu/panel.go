//go:build darwin

package main

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/transfer"
	"github.com/egoist/mygo/ui"

	"shelf/internal/store"
)

var (
	// 收进托盘：U 形托盘 + 向下箭头（空状态、探头「松开放入 / 放回」、拖放篮）。
	shelfSymbol = symbol(`<path d="M4.5 11v4.5A4.5 4.5 0 0 0 9 20h6a4.5 4.5 0 0 0 4.5-4.5V11"/><path d="M12 3.5v10m-3.5-3.5 3.5 3.5 3.5-3.5"/>`)
	textSymbol  = symbol(`<path d="M5 5h14M12 5v14M8 19h8"/>`)
	fileSymbol  = symbol(`<path d="M14 3H6v18h12V7zM14 3v5h4M9 12h6M9 16h6"/>`)
	closeSymbol = symbol(`<path d="m6 6 12 12M18 6 6 18"/>`)
	// 图钉：置顶标记（网格角标与列表行尾），填充的竖向 pin。
	pinSymbol = symbol(`<path d="M12 17v5M9 10.76a2 2 0 0 1-1.11 1.79l-1.78.9A2 2 0 0 0 5 15.24V16h14v-.76a2 2 0 0 0-1.11-1.79l-1.78-.9A2 2 0 0 1 15 10.76V7h1a2 2 0 0 0 0-4H8a2 2 0 0 0 0 4h1v3.76z"/>`)
	pinFill   = fillSymbol(`<path d="M9 10.76a2 2 0 0 1-1.11 1.79l-1.78.9A2 2 0 0 0 5 15.24V16h14v-.76a2 2 0 0 0-1.11-1.79l-1.78-.9A2 2 0 0 1 15 10.76V7h1a2 2 0 0 0 0-4H8a2 2 0 0 0 0 4h1v3.76z"/><path d="M12 17v5" fill="none" stroke="currentColor" stroke-width="2.4" stroke-linecap="round"/>`)
	// 页面图标在一级岛与二级返回行；全局按钮（设置、收起）在菜单栏行；清空在返回行。
	trayNavSymbol   = symbol(`<path d="M4 13h4.5l1.5 2.5h4l1.5-2.5H20"/><path d="M5.5 6h13L20 13v5a1 1 0 0 1-1 1H5a1 1 0 0 1-1-1v-5z"/>`)
	clipNavSymbol   = symbol(`<rect x="6" y="4.5" width="12" height="16" rx="2"/><path d="M9.5 4.5h5v2.5h-5zM9 11h6M9 14.5h6"/>`)
	trashSymbol     = symbol(`<path d="M4 7h16M9 7V4h6v3M6 7l1 13h10l1-13"/>`)
	gearSymbol      = symbol(`<path d="M10.49 5.27L10.72 2.69A9.4 9.4 0 0 1 13.28 2.69L13.51 5.27A6.9 6.9 0 0 1 17.08 7.33L19.43 6.24A9.4 9.4 0 0 1 20.7 8.45L18.58 9.94A6.9 6.9 0 0 1 18.58 14.06L20.7 15.55A9.4 9.4 0 0 1 19.43 17.76L17.08 16.67A6.9 6.9 0 0 1 13.51 18.73L13.28 21.31A9.4 9.4 0 0 1 10.72 21.31L10.49 18.73A6.9 6.9 0 0 1 6.92 16.67L4.57 17.76A9.4 9.4 0 0 1 3.3 15.55L5.42 14.06A6.9 6.9 0 0 1 5.42 9.94L3.3 8.45A9.4 9.4 0 0 1 4.57 6.24L6.92 7.33A6.9 6.9 0 0 1 10.49 5.27Z"/><circle cx="12" cy="12" r="2.8"/>`)
	chevronUpSymbol = symbol(`<path d="M6 15l6-6 6 6"/>`)
	chevronDownSym  = symbolWidth(`<path d="M6 9l6 6 6-6"/>`, 2.2)
	searchSymbol    = symbol(`<circle cx="11" cy="11" r="7"/><path d="M20 20l-4-4"/>`)
)

// The expanded panel's pages are registered in pages.go (pageID,
// panelPage); tray and clipboard are the built-in pages, further pages
// register themselves in ear order.

func symbol(paths string) *ui.SVG {
	return symbolWidth(paths, 1.7)
}

// symbolWidth is symbol with a custom stroke width (the peek chevron uses
// the mock's heavier 2.2).
func symbolWidth(paths string, w float64) *ui.SVG {
	return ui.MustParseSVG([]byte(`<svg viewBox="0 0 24 24" xmlns="http://www.w3.org/2000/svg" fill="none" stroke="currentColor" stroke-width="` + fmt.Sprintf("%g", w) + `" stroke-linecap="round" stroke-linejoin="round">` + paths + `</svg>`))
}

// fillSymbol is symbol for solid glyphs (pin badge, transport controls):
// the path fills with currentColor instead of stroking it.
func fillSymbol(paths string) *ui.SVG {
	return ui.MustParseSVG([]byte(`<svg viewBox="0 0 24 24" xmlns="http://www.w3.org/2000/svg" fill="currentColor" stroke="none">` + paths + `</svg>`))
}

// The palette and every other design token live in theme.go.

// panelTheme keeps the frame dark with the design's single accent: the
// default theme would take the user's accent color and a light appearance
// for scrollbars, focus rings and the search caret.
var panelTheme = func() *ui.Theme {
	t := ui.DarkTheme()
	t.Accent = accent
	t.AccentHover = accent.Alpha(0.8)
	t.AccentPressed = accent.Alpha(0.65)
	t.Selection = accent.Alpha(0.35)
	t.Focus = accent.Alpha(0.6)
	t.Background = ui.Transparent
	// Tooltips (the ear icons' names) take Inverse/InverseText, which
	// otherwise fall back to Text on Background — ink on transparent, a
	// blank white tip. A raised dark chip reads on the black panel.
	t.Inverse = ui.Hex("#2C2C2E")
	t.InverseText = ink
	t.FontSize = 13
	return t
}()

// panelLabel is the panel's label text: everything that is not user
// content — page names, counts, buttons, units, states. It draws one line,
// because single-line text never wraps, and while a test measures it
// (fitIssue, the same seam and the same MeasureText call the home pages'
// fitText uses) it reports the labels whose words do not fit: the natural
// width must fit the box the layout gave the element, so a label can no
// longer pass by ending in an ellipsis. key may be nil; it is the
// element's c.Key identity. Production pays one nil check.
func panelLabel(c *ui.Context, key any, text string, size float32, weight int) ui.Element {
	var t ui.Element
	if key != nil {
		t = ui.Text(c.Key(key), text)
	} else {
		t = ui.Text(c, text)
	}
	t.SingleLine().FontSize(size)
	if weight != 0 {
		t.FontWeight(weight)
	}
	if fitIssue != nil {
		// Bounds is the previous frame's box: the width this text was laid
		// out in. Zero means the element was not drawn yet.
		if b := t.Bounds(); b.W > 0 {
			w, h := c.MeasureText(0, ui.Span{Text: text, Size: size, Weight: weight})
			noteLine(text, h)
			noteBox(text, b)
			if w > b.W+0.5 {
				fitIssue(fmt.Sprintf("%q needs %.1fpt, box is %.1fpt (size %g): the label is cut",
					text, w, b.W, size))
			}
			if b.H > h*1.5 {
				fitIssue(fmt.Sprintf("%q is %.1fpt tall for a %.1fpt line (size %g): the label wrapped",
					text, b.H, h, size))
			}
		}
	}
	return t
}

// labelLines is the single-line height every measured label was drawn at,
// by text — filled only while a test measures (fitIssue != nil). The page
// audit applies a text's own font size as its one-line bound instead of one
// flat cap for the whole panel.
var labelLines = map[string]float32{}

// noteLine remembers how tall one line of text is at the size it was drawn
// with. Tests only.
func noteLine(text string, h float32) {
	if h > 0 {
		labelLines[text] = h
	}
}

// labelLineHeight returns how tall one line of text is at the size it was
// last drawn with, when a measuring test recorded it.
func labelLineHeight(text string) (float32, bool) {
	h, ok := labelLines[text]
	return h, ok
}

// labelBoxes is the box each measured label drew itself in, by text —
// filled only while a test measures. The page audit needs it because
// Tester.Find answers the first node carrying the text, which can be the
// 24pt pill or the 30pt row around the label rather than the label.
var labelBoxes = map[string]ui.Rect{}

// noteBox remembers the box a measured label drew in. Tests only.
func noteBox(text string, r ui.Rect) {
	if r.W > 0 && r.H > 0 {
		labelBoxes[text] = r
	}
}

// labelBox returns the box a label last drew in, when a measuring test
// recorded it.
func labelBox(text string) (ui.Rect, bool) {
	r, ok := labelBoxes[text]
	return r, ok
}

// panelLines is a fixed multi-line message: the newlines are part of the
// design (an empty state's advice, a quota card's two lines), so it wraps
// only where the text says to. While a test measures, every line must fit
// the box on its own and the drawn height must be the lines the text
// spells — a message that grew a third line is a layout bug.
func panelLines(c *ui.Context, text string, size float32, weight int) ui.Element {
	t := ui.Text(c, text).FontSize(size)
	if weight != 0 {
		t.FontWeight(weight)
	}
	if fitIssue != nil {
		if b := t.Bounds(); b.W > 0 {
			lines := strings.Split(text, "\n")
			var lineH float32
			for _, line := range lines {
				w, h := c.MeasureText(0, ui.Span{Text: line, Size: size, Weight: weight})
				noteLine(line, h)
				if w > b.W+0.5 {
					fitIssue(fmt.Sprintf("%q needs %.1fpt, box is %.1fpt (size %g): the line is cut",
						line, w, b.W, size))
				}
				lineH = max(lineH, h)
			}
			if b.H > lineH*float32(len(lines))*1.5 {
				fitIssue(fmt.Sprintf("%q is %.1fpt tall for %d line(s) of %.1fpt (size %g)",
					text, b.H, len(lines), lineH, size))
			}
		}
	}
	return t
}

// 面板只有两种尺寸，与页面内容无关（原型 index.html?variant=B）：
// 一级岛 340 × 104，二级面板 420 × 250。二级面板的 250 由 32 菜单栏行、
// 26 返回行、6 间距、396 × 174 内容区和 12 底边拼成；一级岛的 104 由
// 32 菜单栏行、56 播放器行和 16 底边拼成。页面内容永远画在 396 × 174 里。
// 展开面板有两种尺寸：总览 440 × 156（点刘海就到），页面 440 × 250（点顶部
// 页面图标，同一个外壳向下长）。总览的 156 由 32 顶栏、6 间距、416 × 106、
// 12 底边拼成；页面 250 由 32 顶栏、6 间距、200 页面区、12 底边拼成，页面区
// 自己再分 22 动作行 + 4 间距 + 416 × 174 内容。宽度不变。
const (
	overviewW, overviewH = 440, 156
	sheetW, sheetH       = 440, 250
	contentW, contentH   = 416, 174
	overviewBodyH        = 106
	pageBodyH            = 200

	bandH          = 32 // 顶栏那一行
	sheetPadX      = padX
	sheetPadBottom = padBottom
	contentGap     = bodyGap
	overviewRadius = rOverview
	sheetRadius    = rPage
	tileRadius     = rTile

	bandBtn        = 22 // 顶栏行的按钮
	bandIcon       = 22 // 顶栏的页面图标
	bandIconGlyph  = 14
	bandIconGap    = 2
	pageActionH    = bodyActions // 页面动作行
	pageActionsGap = actionsGap
	chipH          = 22 // 活动小胶囊
	chipMax        = 2  // 左耳最多两个

	menuBarMinH  = 24 // 无刘海屏的菜单栏高
	headerTopGap = 8  // 键盘搜索时：菜单栏下沿到面板顶边

	gridCols    = 5  // 托盘网格列数
	tileH       = 85 // 格子高（两行正好 174）
	tileArt     = 40 // 缩略图边长
	clipRowH    = 30 // 剪贴板行高
	clipRowGap  = 0  // 行紧挨着，同原型 .cl
	clipSearchW = 150
	clipSearchH = 22
	// tabMaxPages is how many page icons the band carries; the current
	// page always has a seat (a sixth page replaces the last one while it
	// is current).
	tabMaxPages = 5

	toastLife = durToast     // 状态胶囊的展示时长
	toastFade = durToastFade // 淡出时长
	// earR 是壳顶边左右各伸出的耳角半径；窗口宽度必须包含两侧耳角，
	// 任何一帧耳角都是完整圆弧（desiredFrame 据此加宽）。
	earR = 12
)

// Page switch "fade through": the page being left fades out over the first
// tabOutShare of the switch, then the new page fades in, drifting tabShift
// points from its icon's side, all in tabAnimation (240ms). The two never
// overlap, so their different rows (a grid, a list) don't show through each
// other.
const (
	tabShift    = 8
	tabOutShare = 0.35
	// pageWarmOpacity is above the painter's 0.001 cull and far below what
	// an eye can see: the incoming page (and a page being warmed) is drawn
	// this faintly so its glyph atlas, pictures and text layouts are ready
	// before it shows at full strength.
	pageWarmOpacity = 0.006
)

// pageSwitchPhase overrides the page switch's progress for a headless
// view (a live window always reads a.tabPhase). Tests install it to render
// the fading layers and their hit behaviour; production leaves it nil.
var pageSwitchPhase func(a *shelf, now time.Time) float32

// pageMotion is how far the current page switch has run and which side the
// new page arrives from: the pages sit left to right as their icons do, so
// going right the new page comes from the right.
func (a *shelf) pageMotion(now time.Time) (t, dir float32) {
	t = 1
	switch {
	case pageSwitchPhase != nil:
		t = pageSwitchPhase(a, now)
	case a.win != nil:
		t = a.tabPhase(now)
	}
	dir = 1
	if a.pageIndex(a.tab) < a.pageIndex(a.tabFrom) {
		dir = -1
	}
	return t, dir
}

// tabPhase is how far a page switch has run, 0→1 over tabAnimation; 1 when
// no switch is under way.
func (a *shelf) tabPhase(now time.Time) float32 {
	if a.tabAt.IsZero() {
		return 1
	}
	t := float32(now.Sub(a.tabAt)) / float32(tabAnimation)
	return min(max(t, 0), 1)
}

// closeFade is how long the panel's content fades out after a hide; the
// shell folds on its own 220 ms spring at the same time.
const closeFade = 120 * time.Millisecond

// currentLevel is the panel's drawn level: 1 the island, 2 the sheet. A
// zero value on a headless surface (unit tests) reads as the sheet, which
// is the content those tests inspect; a live window always starts at the
// island.
func (a *shelf) currentLevel() int {
	if a.level >= 2 {
		return 2
	}
	if a.level == 0 && a.win == nil {
		return 2
	}
	return 1
}

// sheetTop is where the drawn shell starts inside the window: flush with
// the menu bar normally; while the clipboard search holds the keyboard the
// window sits below the menu bar's level, so the shell drops below it by
// the menu bar's height and keeps the sheet's 250 pt below that.
func (a *shelf) sheetTop() int {
	// The island floats below the screen's top edge; the hardware notch
	// path starts at it. Clipboard search drops the whole shell below the
	// menu bar, never above where the style already put it.
	top := a.islandTop
	if a.focusMode {
		top = max(top, max(a.topInset, menuBarMinH)+headerTopGap)
	}
	return top
}

// view 画三种状态：空闲（完全不可见）、探头（悬停播放器、下拉箭头或拖拽
// 提示）、展开（纯黑面板从刘海里长出来）。单元测试里 a.win 为 nil，
// 直接按完整展开态渲染。
func (a *shelf) view(c *ui.Context) {
	a.buildingView = true
	defer func() { a.buildingView = false }()
	// A page that got switched off (or a stale id) must not stay current:
	// fold back to the tray before anything reads a.tab.
	a.normalizeTab()
	// The close fade has run its course: retire the view state now so the
	// next opening starts fresh.
	if !a.visible && !a.closeAt.IsZero() && c.Now().Sub(a.closeAt) >= closeFade {
		a.finishClose()
	}
	// Selection may change while building this frame.
	defer a.syncCopyKey()
	if c.Shortcut(0, ui.KeyEscape) {
		a.escape()
	}
	c.OnShortcut(ui.Cmd, ui.KeyQ, mygo.App.Quit)
	// 剪贴板标签的回车：焦点在搜索框时由输入框的 OnTextKey 处理（输入框先
	// 拿到方向键和回车），焦点在别处时在这里兜底。Esc 无此需要：输入框不
	// 消费它，窗口顶部注册的 Shortcut 已经覆盖所有焦点位置。
	if a.visible && a.tab == pageClipboard {
		if c.Shortcut(0, ui.KeyEnter) {
			a.copyClipSelection()
		}
	}
	// 托盘页选中一张图片时，⌘C 复制图片数据（文件项保持原有行为）。
	if a.visible && a.tab == pageTray {
		c.OnShortcut(ui.Cmd, ui.KeyC, a.copyTrayImage)
	}
	c.SetTheme(panelTheme)
	// 常驻窗口是 Transparent 的：mygo 根元素默认画满主题背景，必须改成
	// 全透明，否则整个窗口会一直盖着一块主题色灰板。
	c.Root().Background(ui.Transparent)
	opts := transfer.DropOptions{Formats: []transfer.Format{transfer.FileList, transfer.URIList, transfer.PNG, transfer.Text}, Operations: transfer.Copy}
	now := c.Now()
	// The welcome and new-version cards wait a moment after the opening:
	// ask for the frame that shows them.
	if a.visible {
		if a.welcomePending && !a.welcomeUp && !a.openedAt.IsZero() {
			c.After(max(time.Second-now.Sub(a.openedAt), 0) + 10*time.Millisecond)
		} else if a.whatsNewPending && !a.whatsNewUp && !a.openedAt.IsZero() {
			c.After(max(400*time.Millisecond-now.Sub(a.openedAt), 0) + 10*time.Millisecond)
		}
	}
	// What a closed panel has to say grows out of the notch (feedback.go);
	// the text is measured before the shell is sized around it.
	// 悬停或外来拖拽占用探头形状期间，胶囊根本没画出来：计时随让位整体
	// 滑动（含没画过的新提示——它的时钟本来就从第一帧算起），回来后按
	// 剩余时间继续，绝不会被静默吃掉。
	if f := &a.fb; f.text != "" && !f.live {
		yielded := !a.visible && a.progress <= 0.01 && (a.peeking || (a.dragging && !a.ownDrag))
		switch {
		case yielded && f.pausedAt.IsZero():
			f.pausedAt = now
		case yielded:
			d := now.Sub(f.pausedAt)
			f.until, f.shownAt, f.pausedAt = f.until.Add(d), f.shownAt.Add(d), now
		case !f.pausedAt.IsZero():
			d := now.Sub(f.pausedAt)
			f.until, f.shownAt = f.until.Add(d), f.shownAt.Add(d)
			f.pausedAt = time.Time{}
		}
	}
	a.fb.follow(a, now)
	if f := &a.fb; f.text != "" && f.pillW == 0 {
		f.pillW = f.measure(c)
	}
	capsule := a.capsuleUp(now)
	if capsule {
		a.fb.schedule(c, now)
	}
	// The realtime queue advances here too, so a notice grows the shell on
	// its own and folds it back when its hold ends.
	_, liveUp := a.liveNow(now, a.liveAllow())
	if liveUp {
		c.After(max(a.live.until.Sub(now), 0) + 10*time.Millisecond)
	}
	downloadUp := a.downloadRunning()
	openTarget, peekTarget := float32(0), float32(0)
	if a.visible {
		openTarget = 1
	}
	if a.peeking || capsule || liveUp || downloadUp {
		peekTarget = 1
	}
	// Keep one native drop target across all states. The transparent root does
	// not paint the theme background; clipping contains every animation frame.
	zone := ui.Column(c.Key("shelf")).Fill().AlignItems(ui.Center).Clip()
	// Draw springs at display cadence. Their last drawn values also drive
	// native frame size and mouse pass-through.
	openK, peekK, contentK := a.progress, a.peekProgress, float32(1)
	if a.win == nil && !a.peeking {
		openK = 1
	}
	sizeAnim := peekSpring
	if a.win != nil {
		openAnim := panelOpenSpring
		peekAnim := peekSpring
		if openTarget == 0 {
			openAnim = closeSpring
			// 展开中途关闭不得带着展开速度继续往外长：反向时重置为很小的
			// 向内初速度（临界阻尼下初速度 ≤ 0 保证壳尺寸单调变小）。
			openAnim.Spring.Velocity = -0.5
		} else if a.fx.openK < 1 && zone.Velocity("open") < 0 {
			// 收起中途重新打开也不得带着收起速度继续往回缩。
			zone.Jump("open", a.fx.openK)
			openAnim.Spring.Velocity = 0.5
		}
		switch {
		case capsule:
			// The capsule grows with the panel's own spring. A capsule that
			// is needed again ends any fold-back in flight.
			peekAnim, sizeAnim = openSpring, openSpring
			a.fb.retracting = false
		case peekTarget == 0 && (a.fb.drawn || a.fb.retracting):
			// The capsule leaves on a spring that never overshoots, for the
			// whole fold — not only the fold's first frame.
			peekAnim = retractSpring
			a.fb.retracting = true
		}
		if peekTarget == 0 {
			peekAnim.Spring.Velocity = -0.5 // 收起反向同样不带外冲速度
		}
		if reduceMotion(c) {
			openAnim = plainSpring
		}
		openK = zone.AnimateTo("open", openTarget, openAnim)
		if a.visible {
			// While the panel is opening, openK drives the shell alone:
			// freeze peek at its last drawn value so a second spring
			// cannot fight openK or reverse the shell on reopen.
			peekK = zone.Jump("peek", a.fx.peekK)
		} else {
			peekK = zone.AnimateTo("peek", peekTarget, peekAnim)
		}
		// The content follows its own channel, not openK: it springs in a
		// beat after the shell (contentSpring's 30ms delay), scales from
		// 0.88 and un-blurs as it fades in. While the panel is not expanded
		// the value rests at 0 — a Jump, not a spring — so every opening,
		// fresh or out of a close in flight, starts the content from
		// nothing instead of a stale fraction.
		if a.visible {
			contentK = zone.AnimateTo("content", 1, contentSpring)
		} else {
			contentK = zone.Jump("content", 0)
		}
		// AnimateTo first accounts for the time passed since the last frame
		// along the OLD target: a close issued mid-flight would otherwise
		// draw one more outward frame before turning. While closing, the
		// drawn interpolation may only shrink.
		if openTarget == 0 && openK > a.fx.openK {
			openK = a.fx.openK
		}
		if peekTarget == 0 && peekK > a.fx.peekK {
			peekK = a.fx.peekK
		}
		// A spring heading back to zero may overshoot its target; the shell
		// never shrinks past the hardware notch it grows from. The rest snap
		// stays under a point, so the fold visibly finishes instead of
		// jumping the last points.
		openK, peekK = max(openK, 0), max(peekK, 0)
		if openK < 0.002 {
			openK = 0
		}
		if peekK < 0.002 {
			peekK = 0
		}
		if a.peeking || peekK == 0 {
			a.fb.retracting = false
		}
	}
	// 真实窗口的彻底空闲态：刘海样式什么都不画，常驻窗口完全不可见；
	// 灵动岛空闲时一直画那颗 120 × 30 的胶囊，让用户知道往哪儿移鼠标。
	// 阈值与上面的静止吸附一致（亚点级），收起最后一段平滑落位、看不出跳变。
	idle := a.win != nil && !a.visible && a.progress <= 0.01 && openK <= 0.002 && !a.peeking && peekK <= 0.002
	// ShelfU's own drag-out never lands back on the shelf: the drop would
	// add a second item pointing at the same file. Without a target the
	// drag ends as if cancelled — nothing is added, the panel stays open
	// and 拖出后从托盘移除 does not fire.
	accepts := !a.ownDrag
	var over bool
	if accepts {
		_, over = ui.DataDragOver(zone, opts)
	}
	if idle {
		// 空闲态收到内容只入库不展开：刘海样式那时窗口本就不显示，也无从
		// 接受拖放。灵动岛的空闲胶囊一直可见，照常画下去。
		// The mesh is the anchor: nothing else is drawn, and the frame the
		// next state starts from is its own box.
		a.fx.w, a.fx.h = a.anchorSize()
		a.fx.peekW, a.fx.peekH = 0, 0
		a.fx.peek, a.fx.openMoving, a.fx.sizeMoving, a.fb.drawn = false, false, false, false
		a.fx.openK, a.fx.peekK = 0, 0
		a.snap.drawn = false // the fold out of the snap box is over
		a.fb.clearExpired(now, true)
		if a.island {
			zone.Label(loc(c).PanelExpand())
		} else {
			a.syncMousePassThrough()
			if accepts {
				if drop, ok := ui.DropData(zone, opts); ok {
					a.drop.done = true
					a.receive(drop.Data)
				}
			}
			return
		}
	}
	peeked := a.peeking || peekK > 0.01
	// A closing panel keeps the view it had for the content's 120 ms fade;
	// finishClose retires that state once the fade is over. Headless test
	// views always draw the expanded state.
	closeK := float32(0)
	switch {
	case a.win == nil:
		// Headless test surfaces draw the content unless a peek is being
		// inspected.
		if a.visible || !a.peeking {
			closeK = 1
		}
	case a.visible:
		closeK = 1
	case !a.closeAt.IsZero():
		if e := now.Sub(a.closeAt); e < closeFade {
			closeK = 1 - float32(e)/float32(closeFade)
		}
	}
	expanded := closeK > 0
	closing := !a.visible && openK > 0
	// The drop zone's file snapshot runs before anything is laid out so
	// the panel already shows the zone on the frame it appears.
	if a.visible {
		a.dropZoneSnap(over)
		// A file drag over the island opens the sheet on the tray page: the
		// drop zone lives there, and the island cannot show it.
		if a.drop.on && len(a.drop.files) > 0 && a.currentLevel() == 1 {
			a.tabFrom, a.tab = pageTray, pageTray
			a.setLevel(2)
		}
	} else {
		a.drop.on = false
	}
	// The two levels: levelK springs between the island (0) and the sheet
	// (1). A fresh opening at the sheet is a jump — the open spring alone
	// takes the notch to the sheet, so no island shape flashes on the way.
	levelTarget := float32(0)
	if a.currentLevel() == 2 {
		levelTarget = 1
	}
	levelK := levelTarget
	if a.win != nil {
		freshOpen := a.fx.openK == 0 && a.fx.levelK == 0 && levelTarget == 1
		if a.jumpLevel || freshOpen {
			levelK = zone.Jump("level", levelTarget)
			a.jumpLevel = false
		} else {
			levelAnim := levelSpring
			if reduceMotion(c) {
				levelAnim = plainSpring
			}
			levelK = zone.AnimateTo("level", levelTarget, levelAnim)
		}
	}
	levelK = min(max(levelK, 0), 1)
	a.fx.levelK = levelK
	// The hover shape springs between its forms (chevron, drop pill,
	// wings, capsule) when it changes while shown, e.g. a drag starting.
	// A fold is subtract-only: while the peek closes (peekTarget 0), the
	// shape freezes at what is actually drawn — re-aiming at the live
	// peekSize() answer would grow the folding shell toward a track's
	// wings before it could shrink.
	peekW, peekH := a.peekSize()
	if a.win != nil {
		if expanded {
			// Opening uses one fixed peek shape so its morph cannot fight
			// openK. Closing instead follows the current track/drag state.
			if a.fx.peekK > 0 && a.fx.peekW > 0 && a.fx.peekH > 0 {
				peekW, peekH = a.fx.peekW, a.fx.peekH
			}
			peekW = zone.Jump("peek-w", peekW)
			peekH = zone.Jump("peek-h", peekH)
		} else {
			switch {
			case closing:
				// Panel fold: the peek shape follows the live track/drag
				// state on the way down.
				peekW = zone.AnimateTo("peek-w", peekW, closeSpring)
				peekH = zone.AnimateTo("peek-h", peekH, closeSpring)
			case peekTarget == 0 && a.fx.peekW > 0 && a.fx.peekH > 0:
				// Peek folding to the notch is subtract-only: pin the
				// drawn shape. Jump, not AnimateTo — AnimateTo keeps an
				// outward velocity and grew the shell past its drawn
				// size when the pointer left mid-morph.
				peekW = zone.Jump("peek-w", a.fx.peekW)
				peekH = zone.Jump("peek-h", a.fx.peekH)
			case a.dragging && !a.ownDrag && !a.dropHot && (a.fx.peekW > peekW || a.fx.peekH > peekH):
				// An incoming drag outside the drop band owns the hint
				// shape. A fold remnant pinned mid-morph (e.g. the hover
				// wings still growing when the drag began) rides the
				// re-rising shell past the menu bar for the whole
				// morph — the "notch flashes" bug. The drag's shape
				// never morphs, so snap the channels: the drawn shell
				// may only shrink from here.
				peekW = zone.Jump("peek-w", peekW)
				peekH = zone.Jump("peek-h", peekH)
			default:
				peekW = zone.AnimateTo("peek-w", peekW, sizeAnim)
				peekH = zone.AnimateTo("peek-h", peekH, sizeAnim)
			}
		}
	}
	a.fx.peekW, a.fx.peekH = peekW, peekH
	a.fx.sizeMoving = a.win != nil && (!zone.Settled("peek-w") || !zone.Settled("peek-h"))
	// 刘海壳尺寸在三态间插值：硬件刘海 ←→ 探头胶囊 ←→ 一级岛 ←→ 二级面板
	// （shellSize 是几何的唯一来源）。
	shellW, shellH := a.shellSize(openK, levelK, peekK, peekW, peekH)
	shellW, shellH = max(shellW, 0), max(shellH, 0)
	// The frame follows the drawn shell while the springs are in flight (see
	// desiredFrame): the opening overshoots, and a shape morph can be larger
	// than the shape it heads for.
	a.fx.w, a.fx.h = shellW, shellH
	a.fx.openK, a.fx.peekK = openK, peekK
	a.fx.openMoving = a.win != nil && (!zone.Settled("open") || !zone.Settled("level"))
	a.fx.peek = !expanded && peeked && !closing
	// A failed job shakes the capsule, and only the capsule; the drawn
	// offset goes into fx before the frame and the mouse gate read it, so
	// both follow the shape in the same frame it is drawn.
	var shake float32
	if capsule {
		shake = a.fb.shake(now, c.Preferences().ReduceMotion)
	}
	a.fx.shakeX = shake
	a.syncMousePassThrough()
	// 窗口贴在屏幕 Y=0（展开=面板全幅，其余=刘海顶边窄条，见
	// syncWindowFrame）：壳水平居中、顶端贴齐窗口顶边，探头才能从硬件
	// 刘海里长出来，而不是浮在菜单栏下方。窗口宽度含两侧耳角（earR），
	// 这里画出的耳角任何一帧都不会被裁。
	top := float32(a.sheetTop())
	corner := float32(overviewRadius) + float32(sheetRadius-overviewRadius)*levelK
	if a.snap.panel || a.snap.drawn {
		corner = snapGhostR * 2 // 24: the design's snap panel corners
	}
	zone.Draw(func(p *ui.Painter, r ui.Rect) {
		x0 := (r.W - shellW) / 2
		x1 := x0 + shellW
		y0 := top
		y1 := top + shellH
		var path ui.Path
		if a.island {
			// 四角全圆的胶囊：空闲 15、两翼/提示条半圆、面板 22 → 24。
			islandPath(&path, x0+shake, y0, x1+shake, y1, min(shellH/2, corner))
			p.FillPath(&path, surface)
			return
		}
		ear := float32(earR)
		// 底部圆角：探头胶囊半圆 → 一级 22 → 二级 24。
		botR := min(shellH/2, corner)
		path.MoveTo(x0-ear+shake, y0)
		path.QuadTo(x0+shake, y0, x0+shake, y0+earR)
		path.LineTo(x0+shake, y1-botR)
		path.QuadTo(x0+shake, y1, x0+shake+botR, y1)
		path.LineTo(x1-botR+shake, y1)
		path.QuadTo(x1+shake, y1, x1+shake, y1-botR)
		path.LineTo(x1+shake, y0+earR)
		path.QuadTo(x1+shake, y0, x1+ear+shake, y0)
		path.Close()
		a.panelFill(p, &path, shellH)
	})
	zone.Children(func() {
		if !expanded {
			if closing || !peeked {
				// 收起途中只有壳缩回刘海，不画内容。
				return
			}
			a.peekView(c, shellW, shellH, openK, peekK, shake, over, peekTarget == 0)
			return
		}
		// Both levels are laid out at their final size and uncovered by the
		// growing shell, so nothing inside reflows while the panel opens or
		// changes level. levelK crossfades the two layers while the shell
		// springs between their sizes.
		clip := ui.Column(c.Key("panel-clip")).Width(shellW).Height(shellH).Clip().
			AlignItems(ui.Center).Margin(top, 0, 0, 0)
		reduce := c.Preferences().ReduceMotion
		reveal := contentK
		if !a.visible && closeK > 0 {
			reveal = closeK
		}
		scale := float32(1)
		if a.visible && !reduce {
			scale = 0.96 + 0.04*contentK
		}
		layer := func(level int, alpha float32) {
			if alpha <= 0.001 && level != a.currentLevel() {
				return
			}
			w, h := float32(overviewW), float32(overviewH)
			switch {
			case level == 2:
				w, h = sheetW, sheetH
			case a.snap.drawn:
				w, h = snapW, snapH
			}
			outer := ui.Column(c.Key(fmt.Sprintf("panel-l%d", level))).Absolute().Top(0).Left(0).Right(0).
				Height(h).AlignItems(ui.Center).Opacity(alpha)
			if level != a.currentLevel() {
				// The layer fading out is only a picture: it takes no
				// pointer and never the keyboard.
				outer.PassThrough()
			}
			outer.Children(func() {
				inner := ui.Column(c.Key("panel")).Width(w).Height(h)
				if !reduce && scale != 1 {
					inner.TransformOrigin(0.5, 0).Scale(scale, scale)
				}
				inner.Children(func() {
					switch {
					case level == 2:
						a.sheetView(c, over)
					case a.snap.drawn:
						a.snapView(c)
					default:
						a.overviewView(c, over)
					}
				})
			})
		}
		clip.Children(func() {
			layer(1, reveal*(1-levelK))
			layer(2, reveal*levelK)
		})
	})
	// What this frame drew decides the next one's retraction spring and
	// whether the capsule's pill takes clicks. Once the folded-back shell
	// has fully retracted, the expired text is dropped for good.
	a.fb.drawn = capsule && !expanded && !closing && peeked
	a.fb.clearExpired(now, peekK == 0 && !capsule)
	if !accepts {
		return
	}
	if drop, ok := ui.DropData(zone, opts); ok {
		a.drop.done = true
		a.receive(drop.Data)
		if !a.visible {
			a.show(false)
		}
	}
}

// sheetView is the 440 × 250 page shell: the band with the page tabs, then
// the page frame (actions row, 416 × 174 content, bottom pad).
func (a *shelf) sheetView(c *ui.Context, over bool) {
	ui.Column(c.Key("sheet")).Width(sheetW).Height(sheetH).Children(func() {
		a.bandRow(c)
		a.tabBody(c, over)
	})
}

// overviewView is the 440 × 156 first level: the same band, then the
// overview's 416 × 106 body.
func (a *shelf) overviewView(c *ui.Context, over bool) {
	ui.Column(c.Key("overview")).Width(overviewW).Height(overviewH).Children(func() {
		a.bandRow(c)
		ui.Box(c.Key("overview-gap")).Height(contentGap)
		ui.Column(c.Key("overview-body")).Width(contentW).Height(overviewBodyH).Children(func() {
			// The overview has no toast layer: its context line is the
			// status surface, so a pill never lands on top of it.
			a.overviewBody(c, over)
		})
	})
}

// tabMetrics is the page icons' size and gap inside the band's left ear.
// The camera housing may not hold pixels, so the five icons have to fit
// beside it; a wide notch shrinks them a little rather than letting one
// slide under the hardware.
func (a *shelf) tabMetrics() (size, gap float32) {
	size, gap = bandIcon, bandIconGap
	if a.notchW <= 0 || a.island || a.focusMode {
		return
	}
	avail := (float32(sheetW)-float32(a.notchW+2*6))/2 - 8
	for size > 16 && float32(tabMaxPages)*size+float32(tabMaxPages-1)*gap > avail {
		size -= 0.5
		if size <= 20 {
			gap = 1
		}
	}
	return
}

// bandRow is the 32pt row at the top of both sizes: the page icons in the
// left ear with a fill2 capsule under the current one, activity chips and
// the settings button in the right ear, split around the camera housing
// while the shell sits under the notch. There is no collapse button: the
// HIG popover rule — a click outside or Esc closes — is the way out.
// While the clipboard search holds the keyboard the window sits below the
// menu bar, so no housing gap is cut.
func (a *shelf) bandRow(c *ui.Context) {
	size, gap := a.tabMetrics()
	row := ui.Row(c.Key("band")).Height(bandH).FillWidth().
		Padding(0, sheetPadX, 0, sheetPadX).AlignItems(ui.Center)
	var tabs map[pageID]ui.Element
	row.Children(func() {
		ui.Row(c.Key("band-left")).Grow(1).MinWidth(0).Gap(gap).AlignItems(ui.Center).Children(func() {
			tabs = map[pageID]ui.Element{}
			for _, p := range a.tabPages() {
				b := a.tabIcon(c, p, size, size*0.64, p.id() == a.currentPage().id())
				tabs[p.id()] = b
				if b.Clicked() && p.id() != a.tab {
					a.openPage(p.id())
				}
			}
		})
		if a.notchW > 0 && !a.focusMode && !a.island {
			// The camera housing splits the band only when the shell is the
			// notch shape; the island floats below it with no gap to cut.
			ui.Box(c.Key("band-housing")).Width(float32(a.notchW + 2*6)).Shrink(0)
		}
		ui.Row(c.Key("band-right")).Grow(1).MinWidth(0).Gap(1).AlignItems(ui.Center).Justify(ui.End).Children(func() {
			if a.currentLevel() == 2 {
				// The overview's own context line already names the
				// running job (with its cancel); the band keeps only the
				// settings button there, as the design draws it.
				for _, act := range a.liveActivities() {
					act.chip(c)
				}
			}
			l := loc(c)
			if a.iconButton(c, "settings", gearSymbol, l.PanelSettings()).Clicked() {
				a.openSettings()
			}
		})
	})
	// The active icon's capsule glides to the icon on a switch and stays
	// glued to it on every other move.
	cur, ok := tabs[a.currentPage().id()]
	if !ok {
		return
	}
	r := cur.LayoutBounds()
	if r.W <= 0 {
		cur.Background(fill2)
		return
	}
	t := a.tabPhase(c.Now())
	x := r.X
	if t < 1 {
		x = row.AnimateTo("tab-x", r.X, ui.Animation{Duration: tabAnimation, Ease: ui.EaseInOut})
	} else {
		x = row.Jump("tab-x", r.X)
	}
	prev := row.LayoutBounds()
	row.Draw(func(p *ui.Painter, box ui.Rect) {
		dx, dy := float32(0), float32(0)
		if prev.W > 0 {
			dx, dy = box.X-prev.X, box.Y-prev.Y
		}
		p.Fill(ui.Rect{X: x + dx, Y: r.Y + dy, W: r.W, H: r.H}, fill2, r.H/2)
	})
}

// tabPages are the page icons the band draws: five at most, in the
// registry's order, with the current page always among them (a page such
// as 正在播放 takes the last seat while it is current).
func (a *shelf) tabPages() []panelPage {
	// 正在播放 is a detail page: it takes one of the five seats only while
	// it is the page on screen, never in place of an ordinary page.
	var pages []panelPage
	cur := a.currentPage()
	for _, p := range a.enabledPages() {
		if p.id() == pagePlaying && cur.id() != pagePlaying {
			continue
		}
		pages = append(pages, p)
	}
	if len(pages) <= tabMaxPages {
		return pages
	}
	out := pages[:tabMaxPages]
	for _, p := range out {
		if p.id() == cur.id() {
			return out
		}
	}
	out[tabMaxPages-1] = cur
	return out
}

// tabIcon is one page icon in the band: ink when active, ink2 with a
// fill1 hover otherwise; the page's name is its tooltip and accessible
// label.
func (a *shelf) tabIcon(c *ui.Context, p panelPage, size, glyph float32, on bool) ui.Element {
	name := pageLabel(c, p)
	b := ui.ButtonBase(c.Key("page-"+string(p.id()))).Size(size, size).Radius(size / 2).Center().
		Label(name).Tooltip(name)
	if !on && b.Hovered() {
		b.Background(fill1)
	}
	b.Children(func() {
		g := p.icon(c, on || b.Hovered()).Size(glyph, glyph)
		if on || b.Hovered() {
			g.TextColor(ink)
		} else {
			g.TextColor(ink2)
		}
	})
	return b
}

// pageLabel is the page icon's tooltip and accessible name in the
// language of c's window; a page that names itself only through label
// (no pageText) keeps its own wording.
func pageLabel(c *ui.Context, p panelPage) string {
	if t, ok := p.(pageText); ok {
		return t.labelIn(loc(c))
	}
	return p.label()
}

// pageTitle is the back row's page name in the language of c's window.
func pageTitle(c *ui.Context, p panelPage) string {
	if t, ok := p.(pageText); ok {
		return t.titleIn(loc(c))
	}
	return p.title()
}

// tabBody holds the page under the band. A drag's drop zone and the
// screen-recording card replace the page whole — they are live gestures;
// everything else switches through the page transition (see pageBody).
func (a *shelf) tabBody(c *ui.Context, over bool) {
	ui.Column(c.Key("sheet-body")).Width(sheetW).Height(sheetH-bandH).
		Padding(0, sheetPadX, sheetPadBottom, sheetPadX).Children(func() {
		switch {
		case a.drop.on && len(a.drop.files) > 0:
			a.dropFrame(c, a.drop.files, a.drop.class)
		case a.ocrSheet:
			a.sheetFrame(c, func() { a.dropBar(c, loc(c).DropOcrTitle(), "", "") },
				func() { a.ocrSheetStage(c) })
		default:
			a.pageBody(c, over)
		}
		a.toastLayer(c, over)
	})
}

// pageBody is the page's own actions row and the 416 × 174 content box: the
// page being left fades out over the first tabOutShare, then the new page
// eases in and settles from its icon's side (see pageMotion). The band's
// icons stay put, so a switch never reflows them.
func (a *shelf) pageBody(c *ui.Context, over bool) {
	t, dir := a.pageMotion(c.Now())
	if c.Preferences().ReduceMotion {
		dir = 0 // 减少动态效果：只淡入淡出，不位移
	}
	a.pageActions(c, t)
	ui.Box(c.Key("page-gap")).Height(pageActionsGap)
	ui.Column(c.Key("page-content")).Width(contentW).Height(contentH).Clip().Children(func() {
		// The warm layer goes first, under the visible page: its rows are
		// drawn (their glyphs, thumbnails and layouts are the point) but
		// every hit lands on the page above it — the frame's own box and
		// its children come later in the hit tree, so a click, a
		// right-click, a drag or the wheel can never reach the unseen page.
		a.warmPage(c)
		if t < tabOutShare && a.tabFrom != a.tab {
			// The outgoing page is only a picture: it takes no pointer and
			// never the keyboard. It may already be disabled; an
			// unregistered one is skipped instead of drawn half-built.
			if out := a.page(a.tabFrom); out != nil && out.enabled() {
				k := a.tabOutK * (1 - ui.EaseIn(t/tabOutShare))
				// Same layout offset as the arriving page, for the same
				// reason: a paint transform here would put clip masks back
				// in the transition's way.
				old := ui.Column(c.Key("frame-out")).Absolute().Top(0).
					Left(a.tabOutShift * (1 - t/tabOutShare)).
					Width(contentW).Height(contentH).PassThrough().Opacity(k)
				old.Children(func() { out.body(c, over, false) })
			}
		}
		// The arrival offset is a layout offset, not a paint transform: a
		// transformed subtree marks every clip inside it as moving, and the
		// GPU builder then rasterises a fresh clip mask for each scrolled
		// box every frame — the whole transition missed frames on this
		// machine (measured 30-48ms frames). Positioned by layout, the same
		// 8pt settle costs nothing: the clips stay shader rectangles.
		frame := ui.Column(c.Key("frame")).Absolute().Top(0).Left(0).
			Width(contentW).Height(contentH)
		if t < 1 {
			in := ui.EaseOut(max(t-tabOutShare, 0) / (1 - tabOutShare))
			op := in
			if t < tabOutShare {
				// The page still hidden behind the fade is drawn, very
				// faintly, so its glyphs and pictures reach the GPU while
				// the frames are cheap instead of all at once on the
				// frame it becomes visible — that first frame used to
				// wait a full refresh or two on the glyph atlas upload.
				op = max(op, pageWarmOpacity*(1-t/tabOutShare))
			}
			frame.Opacity(op).Left(tabShift * dir * (1 - in))
			c.AnimationFrame()
		}
		frame.Children(func() { a.currentPage().body(c, over, true) })
		if t < tabOutShare {
			// Neither page is a target in a switch's first phase: the old
			// one is only a picture and the new one is still invisible
			// (pageWarmOpacity). A plain box with no handler takes the hit
			// chain, so nothing behind it — the fading pages included — can
			// copy, drag, open a menu or move a slider. Once the new page
			// starts to fade in (t >= tabOutShare) it takes the pointer.
			ui.Box(c.Key("page-shield")).Absolute().Left(0).Top(0).Right(0).Bottom(0)
		}
	})
}

// warmPage draws the page queued for warming (see shelf.warmPage) at an
// opacity nobody can see: building it here, while the panel rests, pays
// its first text layout, thumbnail texture and glyph atlas upload before
// the user switches to it.
func (a *shelf) warmPage(c *ui.Context) {
	id := a.warming
	if id == "" || a.tabPhase(c.Now()) < 1 {
		return
	}
	if a.warmed == nil {
		a.warmed = map[pageID]bool{}
	}
	if id == a.tab {
		// The user switched to it first: drawing it for real warms it.
		a.warmed[id], a.warming = true, ""
		a.warmDone()
		return
	}
	p := a.page(id)
	if p == nil || !p.enabled() {
		a.warming = ""
		a.warmDone()
		return
	}
	hidden := ui.Column(c.Key("warm-" + string(id))).Absolute().Left(0).Right(0).Top(0).Bottom(0).
		PassThrough().Opacity(pageWarmOpacity)
	hidden.Children(func() { p.body(c, false, false) })
	a.warmed[id] = true
	a.warming = ""
	a.warmDone()
}

// warmDone asks for the frame that drops the warm layer it just drew: the
// layer lives in the last built scene (and its hit tree) until a repaint,
// and nothing else may repaint a resting panel.
func (a *shelf) warmDone() {
	if a.win != nil {
		a.win.Invalidate()
	}
}

// nextWarmPage queues one page that was never drawn, for the next frame's
// warmPage. It runs from the monitor tick while the sheet rests, so the
// first switch to every page is as cheap as the ones after it.
func (a *shelf) nextWarmPage(now time.Time) {
	if a.win == nil || a.warming != "" || !a.visible || a.currentLevel() != 2 || a.tabPhase(now) < 1 {
		return
	}
	// Never warm while the shell itself is moving: the hidden page's first
	// layout would land in the middle of the open spring.
	if !a.animationStart.IsZero() || !a.closeAt.IsZero() || a.fx.openMoving || a.fx.sizeMoving {
		return
	}
	if a.warmed == nil {
		a.warmed = map[pageID]bool{}
	}
	for _, p := range a.enabledPages() {
		if a.warmed[p.id()] {
			continue
		}
		if p.id() == a.tab {
			// What the user is looking at is warm already.
			a.warmed[p.id()] = true
			continue
		}
		a.warming = p.id()
		a.win.Invalidate()
		return
	}
}

// sheetFrame stacks a page's own top row, the gap and the fixed content box.
func (a *shelf) sheetFrame(c *ui.Context, back, body func()) {
	ui.Column(c.Key("page-frame")).FillWidth().Grow(1).MinHeight(0).Children(func() {
		back()
		ui.Box(c.Key("page-gap")).Height(pageActionsGap)
		ui.Column(c.Key("page-content")).Width(contentW).Height(contentH).Clip().Children(body)
	})
}

// pageActions is the 22pt row above the page content: whatever the current
// page puts at its right edge (the clipboard's search box, the tray's
// selection hint and clear, the tools page's pencil, the usage page's
// refresh). It fades in with the arriving page, so a switch never pops the
// controls ahead of the content.
func (a *shelf) pageActions(c *ui.Context, t float32) {
	in := float32(1)
	if t < 1 {
		in = ui.EaseOut(max(t-tabOutShare, 0) / (1 - tabOutShare))
	}
	row := ui.Row(c.Key("page-actions-row")).Height(pageActionH).FillWidth().Gap(4).
		AlignItems(ui.Center).Justify(ui.End)
	row.Children(func() {
		now := ui.Row(c.Key("page-actions")).Height(pageActionH).Gap(4).AlignItems(ui.Center).Opacity(in)
		if in <= 0 {
			now.PassThrough()
		}
		now.Children(func() {
			for _, e := range a.currentPage().actions(c) {
				_ = e
			}
		})
	})
	if t < 1 {
		c.AnimationFrame()
	}
}

// dropFrame is the page frame during a file drag: 投放 and its count in
// the back row, the release hint on the right, the zone in the content.
func (a *shelf) dropFrame(c *ui.Context, paths []string, class dropClass) {
	l := loc(c)
	a.sheetFrame(c, func() {
		a.dropBar(c, l.DropTitle(), dropCountTextIn(l, len(paths), class), l.DropReleaseTray())
	}, func() {
		a.dropStage(c, paths, class)
	})
}

// dropBar is a plain back row for the transient drop zone and OCR card.
func (a *shelf) dropBar(c *ui.Context, name, count, hint string) {
	ui.Row(c.Key("back-row")).Height(pageActionH).FillWidth().Gap(6).AlignItems(ui.Center).Children(func() {
		panelLabel(c, nil, name, 11.5, 600).TextColor(ink)
		if count != "" {
			panelLabel(c, nil, count, 10, 600).TextColor(ink3)
		}
		ui.Box(c).Grow(1)
		if hint != "" {
			panelLabel(c, nil, hint, 10.5, 600).TextColor(ink3)
		}
	})
}

// liveActivities are the modules with something under way, in module
// order, at most chipMax: the band's left ear has room for two chips.
func (a *shelf) liveActivities() []liveActivity {
	var acts []liveActivity
	for _, m := range a.modules {
		if act, ok := m.(liveActivity); ok && act.live() {
			acts = append(acts, act)
			if len(acts) == chipMax {
				break
			}
		}
	}
	return acts
}

// iconButton is one 22pt circular icon button in the band.
func (a *shelf) iconButton(c *ui.Context, key string, icon *ui.SVG, label string) ui.Element {
	b := ui.ButtonBase(c.Key(key)).Size(bandBtn, bandBtn).Radius(bandBtn / 2).Center().Label(label).Tooltip(label)
	if b.Hovered() {
		b.Background(fill2)
	}
	b.Children(func() {
		g := ui.Icon(c, icon).Size(13, 13)
		if b.Hovered() {
			g.TextColor(ink)
		} else {
			g.TextColor(ink2)
		}
	})
	return b
}

// clearable is what a page's 清空 removes: the unpinned rows the page
// shows, so the tray's button leaves the clipboard history alone and the
// other way round. While a search narrows the clipboard list there is
// nothing to clear — emptying only the matches would surprise.
func (a *shelf) clearable(list []store.Item, id pageID) []store.Item {
	if id == pageClipboard && strings.TrimSpace(a.search) != "" {
		return nil
	}
	var gone []store.Item
	for _, it := range list {
		if !it.Pinned {
			gone = append(gone, it)
		}
	}
	return gone
}

// searchField is the clipboard page's search box in the back row: a 150 ×
// 22 fill1 capsule with a magnifier, ink text and an accent caret. Typing
// filters the list as it goes; while the shelf was opened for search the
// box keeps the keyboard focus, so ↑/↓ walk the list and ⏎ copies and
// collapses without reaching for the mouse. Esc clears the search first,
// then closes the panel outright: the ⌃⌥V flow opens straight on the sheet,
// so there is no island state to back out to (see shelf.escape).
func (a *shelf) searchField(c *ui.Context, live bool) ui.Element {
	placeholder := loc(c).PanelSearch()
	wrap := ui.Row(c.Key("clip-search")).Width(clipSearchW).Height(clipSearchH).Padding(0, 8, 0, 8).Gap(5).
		AlignItems(ui.Center).Radius(7).Background(fill1)
	if !live {
		// The outgoing page's box is a picture of the field: an input here
		// would compete for the keyboard and the global keys.
		wrap.Children(func() {
			ui.Icon(c, searchSymbol).Size(11, 11).TextColor(ink3)
			if a.search != "" {
				ui.Text(c, a.search).SingleLine().FontSize(11).TextColor(ink)
			} else {
				panelLabel(c, nil, placeholder, 11, 0).TextColor(ink3)
			}
		})
		return wrap
	}
	wrap.Children(func() {
		ui.Icon(c, searchSymbol).Size(11, 11).TextColor(ink3)
		in := ui.TextInputBase(c.Key("clip-query"), &a.search).Grow(1).MinWidth(0).
			Placeholder(placeholder).Label(placeholder).FontSize(11).TextColor(ink)
		if a.focusMode {
			in.Focus()
		}
		// 焦点在搜索框期间归还全局 Esc/空格：全局热键会在按键到达窗口
		// 前吃掉它们（空格就打不进查询里）；焦点一离开就收回，
		// 空格恢复快速预览。
		if a.win != nil {
			if in.Focused() {
				a.releaseKeys()
			} else if a.visible && !a.focusMode {
				a.holdKeys()
			}
		}
		in.OnTextKey(func(k ui.TextKey) bool {
			switch k.Key {
			case ui.KeyUp:
				a.moveClipSelection(-1)
			case ui.KeyDown:
				a.moveClipSelection(1)
			case ui.KeyEnter:
				a.copyClipSelection()
			case ui.KeyEscape:
				a.escape()
			default:
				return false
			}
			return true
		})
	})
	return wrap
}

// emptyState is what a page shows when it has no rows: a centered ink3
// icon, a medium ink2 title and an ink3 note line. The page picks the
// contents; this draws the shared layout.
func (a *shelf) emptyState(c *ui.Context, icon *ui.SVG, title, hint string) ui.Element {
	return ui.Column(c).Grow(1).Center().Gap(8).Children(func() {
		ui.Icon(c, icon).Size(28, 28).TextColor(ink3)
		panelLabel(c, nil, title, 13, 500).TextColor(ink2)
		panelLabel(c, nil, hint, 11, 0).TextAlign(ui.Center).TextColor(ink3)
	})
}

// toastLayer stacks the panel's transient pills bottom-center: transient
// status (已复制/已放入/错误), the persistent update notice, the clipboard
// search hints and the drop hint while a drag hovers. PassThrough keeps
// the pills out of the pointer's way except for their own buttons.
func (a *shelf) toastLayer(c *ui.Context, over bool) {
	stack := ui.Column(c.Key("toasts")).Absolute().Left(16).Right(16).Bottom(10).
		AlignItems(ui.Center).Gap(6).PassThrough()
	stack.Children(func() {
		l := loc(c)
		if a.tab == pageClipboard && (a.focusMode || a.search != "") {
			a.pill(c, "hint", l.PanelSearchHint(), false)
		}
		if a.status != "" {
			a.statusToast(c)
		}
		if note := a.updateStatus(); note != "" {
			a.updatePill(c, note)
		}
		a.renameUndoPill(c)
		// The drop zone carries its own release hint in the back row;
		// the 松开放入 capsule stays hidden while it is up.
		if (over || (a.dragging && !a.ownDrag)) && !a.drop.on {
			a.pill(c, "drop", l.DropRelease(), false)
		}
	})
}

// pill is one bottom-center capsule: 11pt ink2 on fill2, 22pt tall.
func (a *shelf) pill(c *ui.Context, key, text string, isErr bool) {
	p := ui.Row(c.Key("pill-"+key)).Height(22).Padding(0, 12, 0, 12).Radius(11).
		AlignItems(ui.Center).Gap(6).Background(fill2)
	p.Children(func() {
		if isErr {
			ui.Box(c).Size(6, 6).Radius(3).Background(danger)
		}
		panelLabel(c, nil, text, 11, 0).TextColor(ink2)
	})
}

// statusToast renders a.status as a fading pill: an explicit statusUntil
// wins; a status without one (notices, errors) fades toastLife of seen
// time — the same clock dismissReadFeedback reads, so panel-closed time
// never eats a deadline-free toast. report()ed errors carry a red dot.
func (a *shelf) statusToast(c *ui.Context) {
	now := c.Now()
	seen := a.statusSeen
	if !a.statusSeenAt.IsZero() {
		seen += now.Sub(a.statusSeenAt)
	}
	until := a.statusUntil
	if until.IsZero() {
		until = now.Add(toastLife - seen)
	}
	remaining := until.Sub(now)
	if remaining <= 0 {
		// The monitor tick clears the text; this frame already shows
		// nothing rather than a stale pill.
		return
	}
	p := ui.Row(c.Key("pill-status")).Height(22).Padding(0, 12, 0, 12).Radius(11).
		AlignItems(ui.Center).Gap(6).Background(fill2)
	// 末段淡出：胶囊本身是透明 pill，不抢指针（toastLayer PassThrough）。
	k := p.AnimateTo("statusFade", float32(1), ui.Animation{Duration: toastFade})
	if remaining < toastFade {
		k = p.AnimateTo("statusFade", float32(0), ui.Animation{Duration: toastFade})
	}
	p.Opacity(k)
	p.Children(func() {
		if a.statusKind == statusErr {
			ui.Box(c).Size(6, 6).Radius(3).Background(danger)
		}
		panelLabel(c, nil, a.status, 11, 0).TextColor(ink)
	})
	// 让引擎在过期时刻再起一帧，tick 趁机清掉状态。
	c.After(remaining + 30*time.Millisecond)
}

// updatePill is the persistent update notice, transplanted from the old
// footer into a bottom pill; installing and relaunching stay user-chosen
// buttons (hence no PassThrough needed on the pill itself — its siblings
// still let clicks through).
func (a *shelf) updatePill(c *ui.Context, note string) {
	p := ui.Row(c.Key("pill-update")).Height(30).Padding(0, 8, 0, 14).Radius(15).
		AlignItems(ui.Center).Gap(8).Background(fill2)
	l := loc(c)
	p.Children(func() {
		panelLabel(c, nil, note, 11, 0).TextColor(ink)
		switch {
		case a.updateInstalled:
			if ui.Button(c, l.PanelRestart()).Label(l.PanelRestartLabel()).Clicked() {
				relaunchApp()
			}
		case a.update != nil && !a.updateInstalling:
			if ui.Button(c, l.PanelInstallUpdate(a.update.Version)).Label(l.PanelInstallUpdate(a.update.Version)).Clicked() {
				a.installUpdate()
			}
		}
	})
}

// peekView draws the pill under the hardware notch while hovering: the
// now-playing wings while a track is loaded, 「松开放入」 while a drag is
// on, otherwise the small downward chevron. A closed panel with something
// to report draws the status capsule instead (feedback.go). Like the
// wings, the pill's content is laid out at its final size and uncovered
// by the growing shell; the glyphs sit in the band below the hardware
// notch, where the screen has pixels.
func (a *shelf) peekView(c *ui.Context, shellW, shellH, openK, peekK, shake float32, over, folding bool) {
	now := c.Now()
	if a.fb.retracting && !a.peeking && !(a.dragging && !a.ownDrag) {
		// The shell is still folding back from the capsule: only the frame
		// moves, as when the panel closes. The hover wings are not drawn
		// over a capsule on its way out.
		return
	}
	// While the peek is folding (peek target 0) the shell keeps showing
	// the content it already had — folding is subtract-only. Re-evaluating
	// live state mid-fold would flash a loaded track's title inside the
	// shrinking shell.
	kind := a.fx.peekKind
	var liveEv liveEvent
	liveOK := false
	if !folding {
		// The queue advances once per drawn frame, so the kind chosen here
		// and the shape peekSize() returns agree.
		if ev, ok := a.liveNow(now, a.liveAllow()); ok {
			liveEv, liveOK = ev, true
		}
		switch {
		case a.pinReturning:
			// A pinned card held over the notch wins over the capsule
			// and the wings: the release reads this same zone, so the
			// affordance must always be the pill it acts on
			// (design §4 松开放回).
			kind = peekReturn
		case a.capsuleUp(now):
			// The completion capsule outranks the wings: a status that is
			// up is what the shell grew for (peekSize and desiredFrame
			// agree), and the wings take the shape back when the toast
			// is done.
			kind = peekCapsule
		case liveOK:
			// A charging / headphone / download notice covers the wings
			// for its own hold; the persistent download owns them after.
			if liveEv.kind.wings() {
				kind = peekLiveWings
			} else {
				kind = peekLiveCapsule
			}
		case a.downloadRunning():
			kind = peekDownload
		case !over && a.np != nil && a.np.showsPlayer():
			kind = peekWings
		case a.dragging && !a.ownDrag:
			// Inside the notch band the drop pill invites the release;
			// outside it only the widened hint shows — never a glyph.
			if over || a.dropHot {
				kind = peekDrop
			} else {
				kind = peekHint
			}
		case over:
			kind = peekDrop
		default:
			kind = peekChevron
		}
		a.fx.peekKind = kind
	}
	switch kind {
	case peekCapsule:
		a.capsuleView(c, shellW, shellH, openK, peekK, shake, now)
		return
	case peekLiveWings:
		if !liveOK {
			return // the event ended: the fold shows only the shrinking shell
		}
		pw, ph := a.liveShape(liveEv)
		clip := ui.Column(c.Key("live-clip")).Width(shellW).Height(shellH).Clip().AlignItems(ui.Center).
			Margin(float32(a.sheetTop()), 0, 0, 0)
		clip.Children(func() { a.liveView(c, pw, ph, peekK, liveEv) })
		return
	case peekLiveCapsule:
		if !liveOK {
			return
		}
		a.liveView(c, shellW, shellH, peekK, liveEv)
		return
	case peekDownload:
		if !a.downloadRunning() {
			return
		}
		pw, ph := a.downloadShape()
		clip := ui.Column(c.Key("dl-clip")).Width(shellW).Height(shellH).Clip().AlignItems(ui.Center).
			Margin(float32(a.sheetTop()), 0, 0, 0)
		clip.Children(func() { a.downloadWings(c, pw, ph, peekK, a.dl.progress()) })
		return
	case peekWings:
		if a.np != nil {
			pw, ph := a.wingsSize()
			// The shell hangs from sheetTop inside the window (the island
			// floats below the top edge); its content starts there too.
			clip := ui.Column(c.Key("peek-clip")).Width(shellW).Height(shellH).Clip().AlignItems(ui.Center).
				Margin(float32(a.sheetTop()), 0, 0, 0)
			drew := false
			clip.Children(func() { drew = a.np.peekWings(c, pw, ph, peekK) })
			if drew {
				return
			}
		}
		// The wings' content is gone mid-fold (track ended): fold the
		// shell silently rather than swapping in another glyph.
		return
	case peekHint:
		// The drag hint draws nothing inside the shell: it only widens,
		// band-height, never a glyph below the notch.
		return
	}
	pw, ph := a.peekSize()
	if folding {
		// Folding keeps the content kind frozen but lays it out at the
		// shrinking shell's size: the frozen drop-pill size (a.fx.peekW/H)
		// would center the words below the clipping shell mid-fold, while
		// the live peekSize() answer (a loaded track's wings) pushes
		// them outside even sooner.
		pw, ph = shellW, shellH
	}
	// 内容从壳顶内侧开始：刘海样式空出刘海那一条，灵动岛空出胶囊本身
	// 的高度，文字落在它下面长出的那一截。
	_, notchH := a.anchorSize()
	l := loc(c)
	pill := ui.Column(c.Key("peek")).Width(shellW).Height(shellH).Clip().
		AlignItems(ui.Center).Label(l.PanelExpand()).Margin(float32(a.sheetTop()), 0, 0, 0)
	pill.Children(func() {
		ui.Column(c.Key("peek-body")).Width(pw).Height(ph).Opacity(min(max(peekK, 0), 1)).Children(func() {
			ui.Box(c.Key("peek-notch")).Height(notchH)
			band := ui.Row(c.Key("peek-band")).FillWidth().Grow(1).Justify(ui.Center).AlignItems(ui.Center).Gap(6)
			band.Children(func() {
				if kind == peekDrop {
					ui.Icon(c, shelfSymbol).Size(14, 14).TextColor(ink)
					panelLabel(c, nil, l.DropRelease(), 12, 600).TextColor(ink)
				} else if kind == peekReturn {
					ui.Icon(c, shelfSymbol).Size(14, 14).TextColor(ink)
					panelLabel(c, nil, l.DropReturn(), 12, 600).TextColor(ink)
				} else {
					ui.Icon(c, chevronDownSym).Size(12, 12).TextColor(ink2)
				}
			})
		})
	})
	if pill.Clicked() {
		a.show(false)
	}
}

// capsuleView draws the completion capsule: the 16pt status dot over the
// toast's words, hung below the notch band. The capsule's outline is the
// shell path itself — the same black the hardware draws — so nothing here
// paints a second shape, a border, or a glow; the status colour stays
// inside the shell as the dot, which breathes while a job runs. The
// content is clipped to the shell while it grows.
func (a *shelf) capsuleView(c *ui.Context, shellW, shellH, _ /* openK */, peekK, shake float32, now time.Time) {
	f := &a.fb
	// The first drawn frame starts the capsule's clock: a status posted while
	// the panel was still closing is held for its full span from here.
	f.arm(now)

	cw, ch := a.capsuleSize()
	col := f.color()
	textW := cw - fbDot - fbGap - 2*fbPadX
	box := ui.Column(c.Key("fb-capsule")).Width(shellW).Height(shellH).AlignItems(ui.Center).
		Margin(float32(a.sheetTop()), 0, 0, 0).Label(loc(c).PanelExpandText(f.text))
	if shake != 0 {
		box.Translate(shake, 0)
	}
	box.Children(func() {
		// Everything inside the capsule is clipped to the shell actually
		// drawn this frame: the sliver grows out of the notch and uncovers
		// the words the same way the panel's shell uncovers its content.
		clip := ui.Column(c.Key("fb-clip")).Absolute().Top(0).Left(0).
			Width(shellW).Height(shellH).Clip().Justify(ui.End).AlignItems(ui.Center).PassThrough()
		clip.Children(func() {
			// 刘海的提示条长在刘海下沿那 22pt 的带里；灵动岛是一颗 36
			// 高的完整胶囊，文字垂直居中。
			rowH, justify := float32(fbBody), ui.End
			if a.island {
				rowH, justify = ch, ui.Center
			}
			body := ui.Column(c.Key("fb-body")).Width(cw).Height(ch).Justify(justify).
				Opacity(min(max(peekK, 0), 1))
			body.Children(func() {
				ui.Row(c.Key("fb-msg")).Height(rowH).FillWidth().Justify(ui.Center).AlignItems(ui.Center).Gap(fbGap).Children(func() {
					dot := ui.Row(c.Key("fb-dot")).Size(fbDot, fbDot).Radius(fbDot / 2).Background(col).Center()
					if f.live {
						// A running job has no verdict yet: the dot
						// breathes instead of a light the hardware
						// would never show.
						dot.Opacity(f.accent(now, c.Preferences().ReduceMotion))
					}
					if g := f.glyph(); g != "" {
						dot.Children(func() { ui.Text(c, g).FontSize(10).FontWeight(800).TextColor(surface) })
					}
					ui.Text(c.Key("fb-text"), f.text).SingleLine().FontSize(12).FontWeight(600).
						TextColor(ink).MaxWidth(textW)
				})
			})
		})
	})
	if box.Clicked() {
		a.show(false)
	}
}

// openPage is a page icon's click (or a card's 全部): the panel shows that
// page. The overview is the short size; every other page grows the same
// shell to 440 × 250. Switching between two pages at the same size is the
// ordinary fade; crossing sizes springs the shell.
func (a *shelf) openPage(id pageID) {
	p := a.page(id)
	if p == nil || !p.enabled() {
		return
	}
	want := 2
	if id == pageOverview {
		want = 1
	}
	if a.currentLevel() == want {
		if a.tab != id {
			a.setTab(id)
		}
		return
	}
	if a.tab != id {
		a.tabFrom, a.tab = id, id
		a.tabAt = time.Time{}
		a.tabOutK, a.tabOutShift = 1, 0
	}
	a.setLevel(want)
}

// setLevel moves between the overview (1) and a page (2). The shell's size
// springs to the new level; the content crossfades through levelK.
func (a *shelf) setLevel(level int) {
	level = min(max(level, 1), 2)
	if a.level == level {
		return
	}
	a.level = level
	if level == 1 {
		// Backing out to the overview: the short layer is a fixed page,
		// so the tab follows it (the level crossfade is the transition).
		a.tab = pageOverview
	} else if a.tab == pageOverview {
		// Only a page has the tall size: a caller that grows the shell
		// (the arrangement panel's restore, a drag) lands on the tray.
		a.tab = pageTray
	}
	a.tabFrom = a.tab
	a.tabAt = time.Time{}
	a.tabOutK, a.tabOutShift = 1, 0
	if level == 2 {
		// A fresh opening warms every page again: textures and the glyph
		// atlas may have been trimmed while the panel was closed.
		a.warmed, a.warming = nil, ""
	}
	a.reveal = ""
	a.selection.keepExisting(a.visibleItems())
	a.syncWindowFrame()
	// The level's box is the shell's target shape, and the frame jumps to
	// it now. A still pointer inside the sheet (but outside the island it
	// grew from) must take presses from this moment, not from the frame
	// that draws the sheet.
	a.syncPointerGate()
	if a.win != nil {
		a.win.Invalidate()
	}
	a.syncCopyKey()
}

// visibleItems is the item list the expanded panel shows and acts on: the
// current page's rows. The tray lists every item except ones hidden from
// it, the clipboard page the captures matching the search text; on both
// pinned items come first, stable within each group (otherwise newest
// first). Rows, selection ranges, menus and drag-outs all use it, so no
// action can reach a row the page is not showing and the display order is
// the selection order. Pages without an item list answer nil.
func (a *shelf) visibleItems() []store.Item { return a.itemsFor(a.currentPage().id()) }

// itemsFor is visibleItems for the given page (the page fading out after
// a switch draws its own rows). Each store-backed page owns its list
// (see pages.go); a page without one answers nil.
func (a *shelf) itemsFor(id pageID) []store.Item {
	switch p := a.page(id).(type) {
	case *trayPage:
		return p.items()
	case *clipboardPage:
		return p.items()
	case *toolsPage:
		// 工具页的动作卡作用于托盘选中项：它报托盘的列表，选择不随翻页丢。
		return a.itemsFor(pageTray)
	default:
		return nil
	}
}

// clipboardItemMatches reports whether a clipboard-list row matches the
// (already lowercased and trimmed) search text: text items by
// case-insensitive substring, images while the search is empty or by
// case-insensitive substring of their recognized text once recognition
// ran. The image text and the query go through the same whitespace fold
// the snippet uses, so a hit always renders a matching snippet.
func clipboardItemMatches(it store.Item, query string) bool {
	if query == "" {
		return true
	}
	if it.Kind == store.KindImage && it.OCRDone && it.OCRText != "" {
		return strings.Contains(strings.ToLower(ocrFold(it.OCRText)), ocrFold(query))
	}
	if it.Kind != store.KindText {
		return false
	}
	return strings.Contains(strings.ToLower(it.Text), query)
}

// setTab switches the panel's page. The selection follows: ids the new
// list does not show are dropped, so a ⇧-range never spans rows the user
// cannot see. Disabled or unregistered pages cannot be switched to.
func (a *shelf) setTab(id pageID) {
	p := a.page(id)
	if p == nil || !p.enabled() {
		return
	}
	cur := a.currentPage().id()
	if cur == id {
		a.tab = id // a.tab was stale; nothing to fade from
		return
	}
	now := time.Now()
	outK, outShift, from := float32(1), float32(0), cur
	if !a.tabAt.IsZero() {
		// A switch is still running: carry the picture on screen into this
		// one, so rapid switching continues instead of flashing the page
		// that was arriving.
		if ph := a.tabPhase(now); ph < 1 {
			if ph < tabOutShare && a.tabFrom != cur {
				// The switch that is running is still fading a page out
				// (its incoming one has not started): carry that page and
				// the opacity it has reached, so the fade continues where
				// it is instead of rising again.
				from = a.tabFrom
				outK = a.tabOutK * (1 - ui.EaseIn(ph/tabOutShare))
				// The picture is also part-way through its 8pt settle:
				// start this fade at the offset it has reached.
				outShift = a.tabOutShift * (1 - ph/tabOutShare)
			} else {
				in := ui.EaseOut((ph - tabOutShare) / (1 - tabOutShare))
				outK = in
				dir := float32(1)
				if a.pageIndex(cur) < a.pageIndex(a.tabFrom) {
					dir = -1
				}
				outShift = tabShift * dir * (1 - in)
			}
		}
	}
	a.tabFrom, a.tab, a.tabOutK, a.tabOutShift = from, id, outK, outShift
	// The tab and the shell size stay one state: a page is the tall shell,
	// the overview the short one. A caller that names a page without
	// switching levels (a test seam, a shortcut) gets the size that page
	// draws in; the arrangement panel borrows the short box and is left
	// alone.
	if !a.snap.panel && !a.snap.drawn {
		want := 2
		if id == pageOverview {
			want = 1
		}
		if a.level != want {
			a.level = want
			a.jumpLevel = a.win == nil || !a.visible
		}
	}
	if cur == pageUsage && a.usage != nil { // 离开用量页即取消其后台工作
		a.usage.stopWork()
	}
	if cur == pageTools { // 离开工具页即退出编辑与目录
		a.resetToolsEdit()
	}
	if a.visible {
		a.tabAt = now
	}
	a.reveal = ""
	a.selection.keepExisting(a.visibleItems())
	a.syncWindowFrame()
	if a.win != nil {
		a.win.Invalidate()
	}
	a.syncCopyKey()
}

// moveClipSelection moves the clipboard tab's selection by delta rows
// (↑/↓ in the search field). With nothing selected yet, ↓ starts at the
// first row and ↑ at the last; the row is scrolled into view.
func (a *shelf) moveClipSelection(delta int) {
	items := a.visibleItems()
	if len(items) == 0 {
		return
	}
	next := 0
	if cur := indexOf(items, a.selection.anchor); cur >= 0 {
		next = min(max(cur+delta, 0), len(items)-1)
	} else if delta < 0 {
		next = len(items) - 1
	}
	a.selection.only(items[next].ID)
	a.reveal = items[next].ID
	if a.win != nil {
		a.win.Invalidate()
	}
}

// copyClipSelection is ⏎ on the clipboard tab: the selected item goes on
// the clipboard — text as text, an image as PNG — and the shelf collapses,
// so ⌘V pastes it into the app the user came from. Several selected texts
// are copied one per line.
func (a *shelf) copyClipSelection() {
	items := a.selection.chosen(a.visibleItems())
	if len(items) == 0 {
		return
	}
	if len(items) == 1 {
		if a.copyItem(items[0]) {
			a.hide(hideExplicit)
		}
		return
	}
	var texts []string
	for _, it := range items {
		if it.Kind != store.KindText {
			a.report(errors.New(a.appLoc().PanelCopyManyFailed()))
			return
		}
		texts = append(texts, it.Text)
	}
	if a.copyText(strings.Join(texts, "\n")) {
		a.hide(hideExplicit)
	}
}
