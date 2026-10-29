//go:build darwin

package main

// AI 用量页（原型 variant=B 的 AI 页）：左栏 203 宽、两张 84 高的套餐额度
// 双圈卡（外圈每周、内圈 5 小时，画「剩余」，悬停出 Synara 式明细）；
// 右栏 187 宽、本机 token 用量（分段范围、2 × 2 指标、堆叠柱图、时间轴、
// 图例）。整页在 416 × 174 里一屏看完，不滚动。取数调度在
// usage_module.go，这里只有绘制和格式化。
//
// 颜色例外：额度圈（每周 #30D158、5 小时 #FF9F0A、低余量 #FF453A）与
// 来源色（omp 蓝、Claude Code #D97757、Codex 灰白）是 DESIGN.md
// 「唯一强调色」规则的第二个例外，本文件是唯一使用它们的地方。

import (
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"

	"shelf/internal/aiusage"
	"shelf/internal/settings"
	"shelf/locales"
)

// Quota ring and source colours — the DESIGN.md single-accent rule's only
// other exception besides the now-playing cover tint (DESIGN.md「AI 用量」).
var (
	usageWeekly  = success                      // 外圈：每周；费用数值同色
	usageFive    = warning                      // 内圈：5 小时；节奏告警同色
	usageClaude  = ui.Hex("#D97757")            // Claude Code 的来源色与标志色
	usageCodex   = ui.Hex("#B8B8C0")            // Codex 的来源色
	usagePopBG   = surfaceRaise                 // 悬停明细底色
	usagePopEdge = ui.RGBA(255, 255, 255, 0.12) // 悬停明细描边
	usageGrid    = ui.RGBA(255, 255, 255, 0.08) // 柱图网格线
	usageBase    = ui.RGBA(255, 255, 255, 0.12) // 柱图基线
)

// The quota card's metrics from the mock (.qc{height:88}, .quota{gap:6}).
const (
	usageCardH    = 84 // two cards + 6 gap fill the 174 content height
	usageCardGap  = 6
	usageLeftW    = 213 // left column: quota cards
	usageRightW   = 197 // right column: local usage; 213+6+197 = 416
	usageRingSize = 50
)

// Symbols the page adds to panel.go's set: the refresh button and the ear
// icon's fallback gauge. The two tools' marks inside the ring are the
// official logos: OpenAI's Blossom (white) from the brand-page download
// https://cdn.openai.com/brand/openai-logos.zip (openai.com/brand) and
// Anthropic's Claude Spark (#D97757) from the press kit at
// https://anthropic.com/press-kit — both scaled only, per
// resources/THIRD_PARTY_NOTICES.txt.
var (
	refreshSymbol = symbol(`<path d="M20 12a8 8 0 1 1-2.34-5.66M20 3.5v4.5h-4.5"/>`)
	gaugeSymbol   = symbol(`<path d="M4 17a8 8 0 1 1 16 0"/><path d="M12 17l4.5-5"/><circle cx="12" cy="17" r="1.6"/>`)
	codexLogo     = ui.MustParseSVG([]byte(`<svg viewBox="0 0 716 716" xmlns="http://www.w3.org/2000/svg"><path fill="white" d="M508.749 317.399C516.777 287.314 508.991 253.884 485.389 230.282C461.788 206.681 428.36 198.895 398.273 206.923C376.231 184.928 343.39 174.956 311.148 183.596C278.906 192.234 255.45 217.292 247.36 247.361C217.291 255.451 192.233 278.91 183.595 311.149C174.957 343.391 184.927 376.232 206.924 398.274C198.896 428.359 206.683 461.789 230.284 485.391C253.885 508.992 287.313 516.779 317.401 508.75C339.442 530.745 372.286 540.717 404.525 532.079C436.767 523.441 460.223 498.384 468.313 468.315C498.383 460.224 523.44 436.766 532.078 404.526C540.716 372.285 530.747 339.443 508.749 317.402V317.399ZM470.899 244.776C486.892 260.77 493.488 282.601 490.687 303.412L415.577 260.046C412.411 258.218 408.509 258.218 405.345 260.046L317.401 310.82V277.526C317.401 275.191 318.652 273.005 320.676 271.837L387.644 233.174C414.178 218.353 448.346 222.223 470.901 244.776H470.899ZM357.837 311.144L398.275 334.491V381.185L357.837 404.532L317.398 381.185V334.491L357.837 311.144ZM264.776 269.693C265.207 239.305 285.644 211.649 316.453 203.393C338.3 197.54 360.505 202.744 377.127 215.573L302.014 258.937C298.848 260.764 296.898 264.144 296.898 267.798V369.346L268.065 352.699C266.043 351.531 264.776 349.353 264.776 347.017V269.691V269.693ZM203.391 316.454C209.244 294.608 224.854 277.978 244.276 269.999V356.73C244.276 360.384 246.226 363.763 249.392 365.591L337.337 416.365L308.503 433.013C306.481 434.181 303.961 434.188 301.939 433.02L234.971 394.357C208.868 378.789 195.138 347.261 203.391 316.454ZM244.775 470.9C228.781 454.906 222.186 433.075 224.986 412.264L300.096 455.63C303.263 457.457 307.164 457.457 310.328 455.63L398.273 404.856V438.149C398.273 440.485 397.022 442.671 394.997 443.839L328.029 482.502C301.495 497.322 267.327 493.452 244.772 470.9H244.775ZM450.897 445.982C450.466 476.371 430.029 504.027 399.22 512.283C377.373 518.136 355.168 512.932 338.547 500.102L413.659 456.738C416.826 454.911 418.775 451.532 418.775 447.877V346.329L447.609 362.977C449.631 364.145 450.897 366.323 450.897 368.659V445.985V445.982ZM512.282 399.221C506.429 421.068 490.819 437.697 471.397 445.676V358.946C471.397 355.292 469.448 351.912 466.281 350.085L378.336 299.311L407.17 282.663C409.192 281.495 411.712 281.487 413.734 282.655L480.702 321.318C506.805 336.887 520.536 368.415 512.282 399.221Z"/></svg>`))
	claudeLogo    = ui.MustParseSVG([]byte(`<svg viewBox="0 0 94 94" xmlns="http://www.w3.org/2000/svg"><path fill="#D97757" d="M18.7657 62.4437L37.1822 52.1167L37.4857 51.2122L37.1822 50.7085H36.2715L33.1852 50.5208L22.6615 50.2391L13.5545 49.8636L4.70044 49.3942L2.47428 48.9248L0.399902 46.1553L0.602281 44.794L2.47428 43.5266L5.15579 43.7613L11.0754 44.1837L19.98 44.794L26.4055 45.1695L35.9679 46.1553H37.4857L37.6881 45.545L37.1822 45.1695L36.7774 44.794L27.5692 38.5508L17.6021 31.9791L12.3908 28.1769L9.60812 26.2524L8.19147 24.4686L7.58433 20.5256L10.1141 17.7091L13.5545 17.9438L14.4146 18.1785L17.9056 20.8542L25.343 26.6279L35.0572 33.7629L36.4739 34.9364L37.0443 34.5514L37.1316 34.2792L36.4739 33.1996L31.212 23.6706L25.596 13.9539L23.0663 9.91695L22.4086 7.52296C22.1538 6.51831 22.0038 5.68714 22.0038 4.65957L24.8877 0.716544L26.5067 0.200195L30.4025 0.716544L32.0215 2.12477L34.4501 7.66379L38.3458 16.3478L44.4172 28.1769L46.188 31.6975L47.1493 34.9364L47.5035 35.9222H48.1106V35.3589L48.6166 28.6933L49.5273 20.5256L50.438 10.0108L50.7415 7.05356L52.2088 3.48605L55.1433 1.56148L57.42 2.64112L59.292 5.31674L59.039 7.05356L57.926 14.2824L55.7504 25.5952L54.3337 33.1996H55.1433L56.1046 32.2138L59.9497 27.1442L66.3752 19.0704L69.2085 15.8784L72.5478 12.3579L74.6728 10.668H78.7203L81.6548 15.0804L80.3394 19.6337L76.1906 24.8911L72.7502 29.3504L67.8172 35.9595L64.7562 41.2734L65.0307 41.7118L65.7681 41.6489L76.8989 39.255L82.9197 38.1753L90.1041 36.9549L93.3422 38.457L93.6963 40.006L92.4315 43.151L84.7411 45.0287L75.7353 46.8594L62.3244 50.0164L62.1759 50.1358L62.3512 50.3958L68.399 50.9432L70.9794 51.084H77.3037L89.0922 51.9759L92.1785 53.9944L93.9999 56.4822L93.6963 58.4068L88.9404 60.8008L82.5655 59.2987L67.6401 55.7312L62.5301 54.4638H61.8217V54.8862L66.0717 59.064L73.9139 66.1051L83.6786 75.2116L84.1845 77.4648L82.9197 79.2485L81.6042 79.0608L73.0032 72.5829L69.6639 69.6726L62.1759 63.3356H61.67V63.9928L63.3902 66.5276L72.5478 80.2812L73.0032 84.5059L72.3454 85.8672L69.9675 86.7121L67.3871 86.2427L61.9735 78.6852L56.4587 70.2359L52.0064 62.6315L51.4687 62.971L48.8189 91.2654L47.6047 92.7206L44.7714 93.8002L42.3934 92.0164L41.1286 89.1061L42.3934 83.3324L43.9113 75.8219L45.1255 69.8604L46.2386 62.4437L46.9184 59.9661L46.8583 59.8003L46.3153 59.8916L40.7238 67.5603L32.2239 79.0608L25.4948 86.2427L23.8758 86.8999L21.0931 85.4447L21.3461 82.863L22.9145 80.5629L32.2239 68.7338L37.8399 61.3641L41.4594 57.1337L41.4242 56.5218L41.2244 56.5048L16.489 72.6299L12.0873 73.1932L10.1647 71.4094L10.4176 68.4991L11.3283 67.5603L18.7657 62.4437Z"/></svg>`))
)

// toolName is a tool's display name on cards and the legend.
func toolName(t aiusage.Tool) string {
	switch t {
	case aiusage.ToolClaude:
		return "Claude Code"
	case aiusage.ToolCodex:
		return "Codex"
	default:
		return "omp"
	}
}

// toolLogo is the tool's mark inside the double ring in its own colour,
// with the icon box size that shows the mark at ~18pt: both official
// SVGs pad the artwork inside their viewBox — the Blossom's ink spans
// the centre 51.1% of 716, the Spark ~99.8% of 94 (and rides slightly
// right of the viewBox centre, which balances the spark's leftward
// tail) — so the boxes are 35.2 and 18pt. Scaling is uniform: the
// viewBoxes are square and Icon keeps the aspect ratio.
func toolLogo(t aiusage.Tool) (*ui.SVG, ui.Color, float32) {
	if t == aiusage.ToolClaude {
		return claudeLogo, usageClaude, 18
	}
	return codexLogo, ink, 35.2
}

// toolColor is a tool's stacked-bar and legend colour.
func toolColor(t aiusage.Tool) ui.Color {
	switch t {
	case aiusage.ToolClaude:
		return usageClaude
	case aiusage.ToolCodex:
		return usageCodex
	default:
		return accent
	}
}

// localSourceOn reports whether the local-usage chip for tool is on.
func (f *usageFeature) localSourceOn(t aiusage.Tool) bool {
	cfg := f.cfg()
	switch t {
	case aiusage.ToolClaude:
		return cfg.LocalClaude
	case aiusage.ToolCodex:
		return cfg.LocalCodex
	default:
		return cfg.LocalOmp
	}
}

// --- panelPage ----------------------------------------------------------

func (f *usageFeature) id() pageID    { return pageUsage }
func (f *usageFeature) label() string { return f.host.appLoc().UsagePageTitle() }
func (f *usageFeature) enabled() bool { return f.host.settings.AIUsage.Enabled }

// icon is the left-ear's third icon: a miniature of the quota card's
// double ring showing the tool with the least 5-hour remaining (outer
// weekly, inner 5h); without any window data it falls back to a plain
// gauge. The ear recolours ordinary icons for hover, but the rings keep
// their own colours — the colours are the information.
func (f *usageFeature) icon(c *ui.Context, _ bool) ui.Element {
	if five, week, ok := f.leastWindow(); ok {
		e := ui.Box(c).Size(17, 17)
		e.Draw(func(p *ui.Painter, r ui.Rect) {
			lw := r.W * 2 / 17
			f.drawWindowRing(p, r, r.W*7.5/17, lw, week, usageWeekly, false, false)
			f.drawWindowRing(p, r, r.W*4/17, lw, five, usageFive, false, false)
		})
		return e
	}
	return ui.Icon(c, gaugeSymbol)
}

// leastWindow picks the tool with the least 5-hour remaining for the ear
// icon; its weekly window rides along on the outer ring. A tool without a
// 5-hour window never wins; with no 5-hour data at all, the least weekly
// remaining shows alone.
func (f *usageFeature) leastWindow() (five, week *aiusage.Window, ok bool) {
	best := math.Inf(1)
	for _, t := range quotaTools() {
		if !f.quotaOn(t) {
			continue
		}
		q, _ := f.quotaShown(t)
		for i := range q.Windows {
			w := &q.Windows[i]
			if w.Length >= 6*24*time.Hour || w.LeftPct() >= best {
				continue
			}
			best, five, week, ok = w.LeftPct(), w, weeklyWindow(q.Windows), true
		}
	}
	if ok {
		return five, week, true
	}
	best = math.Inf(1)
	for _, t := range quotaTools() {
		if !f.quotaOn(t) {
			continue
		}
		q, _ := f.quotaShown(t)
		if w := weeklyWindow(q.Windows); w != nil && w.LeftPct() < best {
			best, week, ok = w.LeftPct(), w, true
		}
	}
	return nil, week, ok
}

// weeklyWindow picks the window the outer ring draws: the unscoped weekly
// one, else the first weekly one a plan has.
func weeklyWindow(ws []aiusage.Window) *aiusage.Window {
	var fallback *aiusage.Window
	for i := range ws {
		if ws[i].Length < 6*24*time.Hour {
			continue
		}
		if ws[i].Model == "" {
			return &ws[i]
		}
		if fallback == nil {
			fallback = &ws[i]
		}
	}
	return fallback
}

// orderedWindows puts the two meters a card shows first: the 5-hour
// session, the unscoped week, then any model-scoped weeks in the order
// Claude Code printed them.
func orderedWindows(ws []aiusage.Window) []aiusage.Window {
	var five, week, rest []aiusage.Window
	for _, w := range ws {
		switch {
		case w.Model == "" && w.Length < 6*24*time.Hour && len(five) == 0:
			five = append(five, w)
		case w.Model == "" && len(week) == 0:
			week = append(week, w)
		default:
			rest = append(rest, w)
		}
	}
	out := make([]aiusage.Window, 0, len(ws))
	out = append(out, five...)
	out = append(out, week...)
	return append(out, rest...)
}

// windowLabel is a window's card name in the language of l.
func windowLabel(l locales.Messages, w aiusage.Window) string {
	if w.Length >= 6*24*time.Hour {
		if _, scope, ok := strings.Cut(w.Name, "·"); ok && w.Model != "" {
			return l.UsageWindowScoped(strings.TrimSpace(scope))
		}
		return l.UsageWindowWeekly()
	}
	return l.UsageWindowFive()
}

// windowShown is the share a meter fills: how much is used by default, how
// much is left when the setting asks for it.
func (f *usageFeature) windowShown(w aiusage.Window) float64 {
	used := max(0, min(100, w.UsedPct))
	if f.cfg().QuotaLeft {
		return 100 - used
	}
	return used
}

// quotaPctText prints a share as Magpie does: whole numbers unchanged,
// anything else with one decimal. "41%" and "12.5%".
func quotaPctText(v float64) string {
	v = max(0, min(100, v))
	if v == math.Trunc(v) {
		return fmt.Sprintf("%.0f%%", v)
	}
	return fmt.Sprintf("%.1f%%", v)
}

// windowColor is a window's dot and arc colour; a window at or above 90%
// used is red whichever way the number is shown, as Magpie marks it.
func windowColor(w aiusage.Window) ui.Color {
	if max(0, min(100, w.UsedPct)) >= 90 {
		return danger
	}
	if w.Length >= 6*24*time.Hour {
		return usageWeekly
	}
	return usageFive
}

// title is the back row's page name; the data stamp is in actions.
func (f *usageFeature) title() string { return f.host.appLoc().UsagePageTitle() }

// stamp is the data age — or progress while any source is busy — shown
// just left of the refresh button; empty before the first answer.
func (f *usageFeature) stamp(c *ui.Context) string {
	l := tloc(c)
	if f.quota[0].busy || f.quota[1].busy || f.localBusy {
		return l.UsageUpdating()
	}
	at := f.lastUpdatedAt()
	if at.IsZero() {
		return ""
	}
	ago := usageAgo(l, at, f.deps.now())
	if f.offline() {
		return l.UsageUpdatedAgoOffline(ago)
	}
	return l.UsageUpdatedAgo(ago)
}

// actions is the data stamp and the refresh button; 「刷新」 is the
// button's tooltip and accessible name.
func (f *usageFeature) actions(c *ui.Context) []ui.Element {
	var out []ui.Element
	if s := f.stamp(c); s != "" {
		out = append(out, panelLabel(c, "usage-stamp", s, 10, 600).TextColor(ink3))
	}
	b := f.host.iconButton(c, "usage-refresh", refreshSymbol, tloc(c).UsageRefresh())
	if b.Clicked() && f.host.tab == pageUsage {
		f.requestRefresh()
	}
	return append(out, b)
}

// body is the fixed 416 × 174 two-column layout: quota cards on the left
// (203 wide, two 84-tall cards), local usage on the right (187 wide). No
// part of it scrolls — the user reads every state at once.
func (f *usageFeature) body(c *ui.Context, _ bool, _ bool) ui.Element {
	if !f.anySource() {
		l := tloc(c)
		return f.host.emptyState(c, gaugeSymbol, l.UsageEmptyTitle(), l.UsageEmptyDetail())
	}
	root := ui.Row(c.Key("usage")).FillWidth().FillHeight().Gap(usageCardGap).PassThrough()
	root.Children(func() {
		f.hoverTool = "" // rebuilt each frame by the cards below
		ui.Column(c.Key("usage-quota")).Width(usageLeftW).FillHeight().Gap(usageCardGap).Children(func() {
			f.quotaColumn(c)
		})
		if f.localOn() {
			ui.Column(c.Key("usage-local")).Width(usageRightW).FillHeight().Children(func() {
				f.localPanel(c)
			})
		} else {
			ui.Box(c).Width(usageRightW)
		}
		// The detail popover overlays the content while a card is hovered:
		// at 250pt tall the sheet has no room beside a card for a 300pt
		// card, and PassThrough hands the pointer back to the card so the
		// hover (and the popover) cannot flicker.
		if f.hoverTool != "" {
			pop := ui.Column(c.Key("usage-pop")).Absolute().Left(0).Top(6).Width(300).Radius(14).
				Background(usagePopBG).Border(0.5, usagePopEdge).
				Shadow(0, 12, 32, 0, ui.RGBA(0, 0, 0, 0.55)).
				Padding(12, 14, 12, 14).PassThrough()
			pop.Children(func() { f.popoverBody(c, f.hoverTool) })
		}
	})
	return root
}

// anySource reports whether at least one card or the local section shows.
func (f *usageFeature) anySource() bool {
	for _, t := range quotaTools() {
		if f.quotaOn(t) {
			return true
		}
	}
	return f.localOn()
}

// quotaColumn draws the enabled quota cards stacked, each 84 tall, and
// remembers which one the pointer is over for the hover detail.
func (f *usageFeature) quotaColumn(c *ui.Context) {
	for _, t := range quotaTools() {
		if !f.quotaOn(t) {
			continue
		}
		card := f.quotaCard(c, t)
		if _, _, over := card.PointerPosition(); over {
			f.hoverTool = t
		}
	}
}

// quotaCard draws one tool's card: the double ring plus name/plan, two
// window lines and a pace line — or a state message when the fetch could
// not produce numbers.
func (f *usageFeature) quotaCard(c *ui.Context, tool aiusage.Tool) ui.Element {
	st := &f.quota[quotaIndex(tool)]
	shown, stale := f.quotaShown(tool)
	card := ui.Row(c.Key("quota-"+string(tool))).Height(usageCardH).MinWidth(0).
		Radius(14).Padding(0, 10, 0, 10).AlignItems(ui.Center).Gap(9)
	if _, _, over := card.PointerPosition(); over {
		card.Background(fill2)
		f.hoverTool = tool
	} else {
		card.Background(fill1)
	}
	card.Children(func() {
		f.quotaRing(c, tool, shown, stale)
		ui.Column(c.Key("info")).Grow(1).MinWidth(0).Children(func() {
			f.quotaInfo(c, tool, st, shown, stale)
		})
	})
	return card
}

// quotaInfo is the card's right side. Numbers show for a good fetch (or
// the last good one, dimmed, when the latest only failed to reach the
// endpoint); the account's own answers — 未登录, 已过期, 额度上限 — always
// replace numbers with their message.
func (f *usageFeature) quotaInfo(c *ui.Context, tool aiusage.Tool, st *usageQuotaState, q aiusage.Quota, stale bool) {
	now := f.deps.now()
	ui.Row(c.Key("name")).AlignItems(ui.Center).Gap(5).Children(func() {
		panelLabel(c, nil, toolName(tool), 12, 600).TextColor(ink)
		if q.Plan != "" {
			panelLabel(c, nil, q.Plan, 9.5, 600).TextColor(ink2).
				Background(fill2).Radius(5).Padding(1, 5, 1, 5)
		}
	})
	showNumbers := false
	switch {
	case st.latest.FetchedAt.IsZero():
		// First fetch not answered yet.
	case st.latest.Status == aiusage.QuotaOK:
		showNumbers = len(q.Windows) > 0
	case st.latest.Status.Transient():
		showNumbers = stale // last good numbers stay, dimmed
	}
	if showNumbers {
		ordered := orderedWindows(q.Windows)
		if len(ordered) > 2 {
			ordered = ordered[:2] // the rest are in the hover detail
		}
		for _, w := range ordered {
			f.windowLine(c, w, stale, now)
		}
		f.paceLine(c, q, stale, now)
		return
	}
	if st.latest.FetchedAt.IsZero() {
		panelLabel(c, nil, tloc(c).UsageQuerying(), 11, 0).TextColor(ink3).Margin(4, 0, 0, 0)
		return
	}
	f.quotaMessage(c, tool, st.latest, now)
}

// quotaMessage is the card's state text for the answers that produce no
// numbers: 未登录, 已过期, 额度上限, 查询限流, 查不到.
func (f *usageFeature) quotaMessage(c *ui.Context, tool aiusage.Tool, q aiusage.Quota, now time.Time) {
	// Each line must fit the 124pt info column at 10.5pt: a longer line
	// wraps inside the card and the third line falls out of it (the page
	// audit measures every line, so this cannot regress silently).
	l := tloc(c)
	var lines string
	switch q.Status {
	case aiusage.QuotaNotLoggedIn:
		if tool == aiusage.ToolClaude {
			lines = l.UsageSignedOutClaude()
		} else {
			lines = l.UsageSignedOutCodex()
		}
	case aiusage.QuotaExpired:
		lines = l.UsageSignInExpired(toolName(tool))
	case aiusage.QuotaLimitReached:
		// The vendor said the allowance is used up: say so, and when it
		// comes back if its message named the reset.
		lines = l.UsageLimitReached()
		if !q.LimitResetsAt.IsZero() {
			lines = l.UsageLimitReachedUntil(usageResetIn(l, q.LimitResetsAt, now))
		}
	case aiusage.QuotaRateLimited:
		lines = l.UsageRateLimited()
	case aiusage.QuotaNoCLI:
		lines = l.UsageNoCLI()
	default:
		lines = l.UsageUnavailable()
	}
	panelLines(c, lines, 10.5, 0).TextColor(ink3).Margin(4, 0, 0, 0)
}

// windowLine is one card row: coloured dot, window name, the shown share
// (used, or left when the setting asks) and its reset time.
func (f *usageFeature) windowLine(c *ui.Context, w aiusage.Window, stale bool, now time.Time) {
	l := tloc(c)
	name := windowLabel(l, w)
	col := windowColor(w)
	pctCol := ink
	if col == danger {
		pctCol = danger
	}
	if stale {
		col = col.Alpha(0.45)
		pctCol = ink3
	}
	ui.Row(c.Key("wl-"+name)).AlignItems(ui.Center).Gap(4).Margin(3, 0, 0, 0).Children(func() {
		panelLabel(c, nil, name, 10.5, 0).TextColor(ink2).Width(31)
		// The mode rides on the number, as Magpie's 「41% used」 does, so
		// the row states which share it shows without a second label.
		panelLabel(c, nil, f.shownShort(l, w), 10, 600).TextColor(pctCol).MinWidth(26)
		if !w.ResetsAt.IsZero() {
			panelLabel(c, nil, usageResetShort(l, w.ResetsAt, now), 9.5, 0).TextColor(ink3)
		}
	})
}

// paceLine is the card's last row: 「N 分钟前的数据」 for stale numbers,
// red 快用完了 under 10% left, the orange depletion estimate naming the
// window that runs out first, otherwise the quiet 正常 line.
func (f *usageFeature) paceLine(c *ui.Context, q aiusage.Quota, stale bool, now time.Time) {
	l := tloc(c)
	text, col := l.UsagePaceSteady(), ink3
	if stale {
		text = l.UsageStaleData(usageAgo(l, q.FetchedAt, now))
	} else {
		ordered := orderedWindows(q.Windows)
		if len(ordered) > 2 {
			ordered = ordered[:2]
		}
		lowest := 101.0
		for _, w := range ordered {
			if w.LeftPct() < lowest {
				lowest = w.LeftPct()
			}
		}
		runsOut, d := false, time.Duration(0)
		name := ""
		for _, w := range ordered {
			if pi, ok := f.paceOf(&w, now); ok && !pi.LastsUntilReset {
				if !runsOut || pi.RunsOutIn < d {
					d, name = pi.RunsOutIn, windowLabel(l, w)
				}
				runsOut = true
			}
		}
		switch {
		case lowest < 10:
			text, col = l.UsageRunningLow(), danger
		case runsOut:
			text, col = l.UsageWindowRunsOut(name, usageDeplete(l, d)), usageFive
		}
	}
	panelLabel(c, nil, text, 10, 0).TextColor(col).Margin(3, 0, 0, 0)
}

// quotaRing draws the card's 50pt double ring and centres the tool logo on
// the circle centre — the mock's .rg/.logo stack; Center on the ring box
// puts the child's geometric centre over the middle of the inner circle's
// hollow. Without window data it leaves the tracks and dims the logo.
func (f *usageFeature) quotaRing(c *ui.Context, tool aiusage.Tool, q aiusage.Quota, stale bool) {
	const ring = float32(usageRingSize)
	k := ring / 64
	e := ui.Box(c.Key("ring")).Size(ring, ring).Center()
	five, week := ringWindows(q.Windows)
	has := len(q.Windows) > 0
	e.Draw(func(p *ui.Painter, r ui.Rect) {
		f.drawWindowRing(p, r, 28*k, 5*k, week, usageWeekly, stale, true)
		f.drawWindowRing(p, r, 20*k, 5*k, five, usageFive, stale, true)
	})
	e.Children(func() {
		logo, col, size := toolLogo(tool)
		ic := ui.Icon(c, logo).Size(size*k, size*k).TextColor(col)
		if !has {
			ic.Opacity(0.4)
		}
	})
}

// ringWindows picks the two windows the double ring draws: the 5-hour
// session inside, the unscoped week outside.
func ringWindows(ws []aiusage.Window) (five, week *aiusage.Window) {
	for i := range ws {
		w := &ws[i]
		if w.Length < 6*24*time.Hour && five == nil {
			five = w
		}
		if w.Length >= 6*24*time.Hour && w.Model == "" && week == nil {
			week = w
		}
	}
	if week == nil {
		week = weeklyWindow(ws)
	}
	return five, week
}

// drawWindowRing paints one arc of the double ring inside r: the fill3
// track, the remaining fraction starting at 12 o'clock clockwise (round
// caps via StrokePath), and the white tick at the even-pace position. A
// nil window paints the bare track; dim fades the arc for stale data.
func (f *usageFeature) drawWindowRing(p *ui.Painter, r ui.Rect, rad, lw float32, w *aiusage.Window, col ui.Color, dim, tick bool) {
	cx, cy := r.X+r.W/2, r.Y+r.H/2
	var track ui.Path
	track.Circle(cx, cy, rad)
	p.StrokePath(&track, lw, fill3)
	if w == nil {
		return
	}
	if w.UsedPct >= 90 {
		col = danger
	}
	if dim {
		col = col.Alpha(0.45)
	}
	strokeRingArc(p, cx, cy, rad, lw, float32(f.windowShown(*w)/100), col)
	if tick {
		if pi, ok := f.paceOf(w, f.deps.now()); ok {
			strokeRingTick(p, cx, cy, rad, lw, float32(f.paceTickFraction(pi)))
		}
	}
}

// paceTickFraction is where the even-pace tick sits on the 0–1 scale the
// meters fill, so the tick and the arc share one mode: the expected used
// share when the page shows 已用, the expected left share when it shows
// 剩余. The detail bar draws its tick from the same number.
func (f *usageFeature) paceTickFraction(pi aiusage.PaceInfo) float64 {
	expected := pi.ExpectedLeftPct
	if !f.cfg().QuotaLeft {
		expected = 100 - expected
	}
	return max(0, min(100, expected)) / 100
}

// strokeRingArc strokes the arc that is the fraction frac of a circle,
// starting at 12 o'clock clockwise (the Circle path starts at 3 o'clock
// and winds down, so 0.75 is the top). frac ≤ 0.25 stays inside the last
// quarter of the path; a larger fraction wraps around to its beginning.
func strokeRingArc(p *ui.Painter, cx, cy, rad, lw, frac float32, col ui.Color) {
	if frac <= 0 {
		return
	}
	var head ui.Path
	head.Circle(cx, cy, rad).Trim(0.75, min(1, 0.75+frac))
	p.StrokePath(&head, lw, col)
	if frac > 0.25 {
		var tail ui.Path
		tail.Circle(cx, cy, rad).Trim(0, frac-0.25)
		p.StrokePath(&tail, lw, col)
	}
}

// strokeRingTick draws the small white marker across the ring at the
// even-pace-remaining fraction (same clock as strokeRingArc).
func strokeRingTick(p *ui.Painter, cx, cy, rad, lw, frac float32) {
	a := float64(frac)*2*math.Pi - math.Pi/2
	ca, sa := float32(math.Cos(a)), float32(math.Sin(a))
	var tick ui.Path
	tick.MoveTo(cx+(rad-lw/2-1)*ca, cy+(rad-lw/2-1)*sa)
	tick.LineTo(cx+(rad+lw/2+1)*ca, cy+(rad+lw/2+1)*sa)
	p.StrokePath(&tick, 1.5, ui.RGB(255, 255, 255))
}

// --- hover detail --------------------------------------------------------

// popoverBody fills the Synara-style detail card: tool + plan, a legend,
// and per window a track bar with the even-pace tick and the pace delta.
func (f *usageFeature) popoverBody(c *ui.Context, tool aiusage.Tool) {
	q, _ := f.quotaShown(tool)
	now := f.deps.now()
	ui.Row(c.Key("ph")).AlignItems(ui.End).Children(func() {
		panelLabel(c, nil, toolName(tool), 14, 600).TextColor(ink)
		ui.Box(c).Grow(1)
		if q.Plan != "" {
			panelLabel(c, nil, q.Plan, 12, 500).TextColor(ink2)
		}
	})
	five, week := ringWindows(q.Windows)
	ui.Row(c.Key("leg")).Gap(14).Margin(6, 0, 0, 0).Children(func() {
		if week != nil {
			popLegend(c, usageWeekly, tloc(c).UsageLegendWeekly())
		}
		if five != nil {
			popLegend(c, usageFive, tloc(c).UsageLegendFive())
		}
	})
	ordered := orderedWindows(q.Windows)
	if len(ordered) == 0 {
		// No numbers to detail: the newest answer says why (a limit, a
		// lapsed sign-in, a query that did not get through).
		if st := &f.quota[quotaIndex(tool)]; !st.latest.FetchedAt.IsZero() {
			f.quotaMessage(c, tool, st.latest, now)
		}
		return
	}
	if len(ordered) > 2 {
		// More windows than the card holds (a plan with per-model weeks):
		// every window gets a compact meter so all of them fit the sheet.
		for _, w := range ordered {
			f.popCompact(c, w, now)
		}
		return
	}
	for _, w := range ordered {
		f.popWindow(c, w, now)
	}
}

func popLegend(c *ui.Context, col ui.Color, s string) {
	ui.Row(c).AlignItems(ui.Center).Children(func() {
		ui.Box(c).Size(7, 7).Radius(4).Background(col).Margin(0, 5, 0, 0)
		panelLabel(c, nil, s, 11, 0).TextColor(ink2)
	})
}

// popWindow is one detail block: name, the shown share and the long reset
// over the track (fill3 bar, coloured share, white/red even-pace tick),
// then the pace delta and the depletion estimate.
func (f *usageFeature) popWindow(c *ui.Context, w aiusage.Window, now time.Time) {
	l := tloc(c)
	name := windowLabel(l, w)
	shown := f.windowShown(w)
	col := windowColor(w)
	pi, hasPace := f.paceOf(&w, now)
	ui.Row(c.Key("pw-"+name)).AlignItems(ui.End).Margin(12, 0, 0, 0).Children(func() {
		panelLabel(c, nil, name, 13, 600).TextColor(ink)
		panelLabel(c, nil, f.shownLabel(l, shown), 11, 600).TextColor(ink2).Margin(0, 0, 0, 6)
		ui.Box(c).Grow(1)
		panelLabel(c, nil, usageResetLong(l, w.ResetsAt, now), 11, 0).TextColor(ink3)
	})
	bar := ui.Box(c.Key("trk-"+name)).FillWidth().Height(5).Radius(3).Margin(6, 0, 0, 0)
	bar.Draw(func(p *ui.Painter, r ui.Rect) {
		p.Fill(r, fill3, r.H/2)
		if wf := float32(shown) / 100; wf > 0 {
			p.Fill(ui.Rect{X: r.X, Y: r.Y, W: r.W * wf, H: r.H}, col, r.H/2)
		}
		if hasPace {
			tick := ui.RGB(255, 255, 255)
			if !pi.LastsUntilReset {
				tick = danger
			}
			x := r.X + r.W*float32(f.paceTickFraction(pi))
			p.Fill(ui.Rect{X: x - 1, Y: r.Y - 3, W: 2, H: 11}, tick, 1)
		}
	})
	ui.Row(c.Key("pf-"+name)).AlignItems(ui.End).Margin(5, 0, 0, 0).Children(func() {
		delta, dcol := "", ink3
		if hasPace {
			if pi.AheadPct > 0 {
				delta, dcol = l.UsageAhead(fmt.Sprintf("%.0f", pi.AheadPct)), col
			} else {
				delta = l.UsageBehind(fmt.Sprintf("%.0f", -pi.AheadPct))
			}
		}
		if delta != "" {
			panelLabel(c, nil, delta, 10.5, 0).TextColor(dcol)
		}
		ui.Box(c).Grow(1)
		right := l.UsageLastsToReset()
		if hasPace && !pi.LastsUntilReset {
			right = l.UsageAtRate(usageDeplete(l, pi.RunsOutIn))
		}
		panelLabel(c, nil, right, 10.5, 0).TextColor(ink3)
	})
}

// popCompact is one line plus a thin meter: the shape a plan with more
// than two windows uses so every window stays visible in the sheet.
func (f *usageFeature) popCompact(c *ui.Context, w aiusage.Window, now time.Time) {
	l := tloc(c)
	name := windowLabel(l, w)
	shown := f.windowShown(w)
	col := windowColor(w)
	ui.Row(c.Key("pc-"+name)).AlignItems(ui.Center).Margin(4, 0, 0, 0).Gap(6).Children(func() {
		panelLabel(c, nil, name, 12, 600).TextColor(ink)
		ui.Box(c).Grow(1)
		panelLabel(c, nil, f.shownLabel(l, shown), 11, 600).TextColor(ink2)
		if !w.ResetsAt.IsZero() {
			panelLabel(c, nil, usageResetShort(l, w.ResetsAt, now), 10.5, 0).TextColor(ink3)
		}
	})
	ui.Box(c.Key("tk-"+name)).FillWidth().Height(3).Radius(2).Margin(3, 12, 1, 0).Draw(func(p *ui.Painter, r ui.Rect) {
		p.Fill(r, fill3, r.H/2)
		if wf := float32(shown) / 100; wf > 0 {
			p.Fill(ui.Rect{X: r.X, Y: r.Y, W: r.W * wf, H: r.H}, col, r.H/2)
		}
	})
}

// shownShort is the card row's share and mode: 「41% 已用」 or 「59%
// 剩余」 at 11pt, sized to fit beside the window name.
func (f *usageFeature) shownShort(l locales.Messages, w aiusage.Window) string {
	v := quotaPctText(f.windowShown(w))
	if f.cfg().QuotaLeft {
		return l.UsageShownLeft(v)
	}
	return l.UsageShownUsed(v)
}

// shownLabel names the share the meters fill: 「41% 已用」 or 「59% 剩余」.
func (f *usageFeature) shownLabel(l locales.Messages, v float64) string {
	if f.cfg().QuotaLeft {
		return l.UsageShownLeft(quotaPctText(v))
	}
	return l.UsageShownUsed(quotaPctText(v))
}

// --- local usage ---------------------------------------------------------

// localPanel is 本机用量: header with range segments, 2 × 2 metrics, the
// stacked chart filling the rest, axis and legend — or the scan/progress
// card while there is no summary yet. Everything fits 197 × 174.
func (f *usageFeature) localPanel(c *ui.Context) {
	card := ui.Column(c.Key("local-card")).FillWidth().FillHeight().Radius(14).Background(fill1).
		Padding(8, 10, 7, 10).Gap(4)
	card.Children(func() {
		ui.Row(c.Key("uh")).Height(15).AlignItems(ui.Center).Children(func() {
			panelLabel(c, nil, tloc(c).UsageLocalTitle(), 10.5, 600).TextColor(ink2)
			ui.Box(c).Grow(1)
			f.usageSeg(c)
		})
		if !f.hasSummary || f.summary.Range != f.rangeSel {
			f.scanCard(c)
			return
		}
		s := f.shownSummary()
		f.kpiGrid(c, s)
		f.usageChart(c, s)
		f.usageLegend(c, s)
	})
}

// shownSummary is the summary as it must draw right now: KPI totals, the
// chart and the legend all follow the live aiUsage.local* chips, so a chip
// flip repaints the numbers at once instead of waiting for the next scan.
// With every source on (the default) it returns the published summary
// unchanged; otherwise it rebuilds Total from the kept per-tool totals
// and drops the dropped tools' tokens from each bucket.
func (f *usageFeature) shownSummary() aiusage.Summary {
	s := f.summary
	var kept []aiusage.Tool
	for _, t := range localTools() {
		if f.localSourceOn(t) {
			kept = append(kept, t)
		}
	}
	if len(kept) == len(localTools()) {
		return s
	}
	s.ByTool = make(map[aiusage.Tool]aiusage.Totals, len(kept))
	s.Total = aiusage.Totals{}
	for _, t := range kept {
		bt := f.summary.ByTool[t]
		s.ByTool[t] = bt
		s.Total.Input += bt.Input
		s.Total.Output += bt.Output
		s.Total.CacheRead += bt.CacheRead
		s.Total.CacheWrite += bt.CacheWrite
		s.Total.Requests += bt.Requests
		s.Total.Unpriced += bt.Unpriced
		s.Total.Cost += bt.Cost
	}
	s.Buckets = make([]aiusage.Bucket, len(f.summary.Buckets))
	for i, b := range f.summary.Buckets {
		tokens := make(map[aiusage.Tool]int64, len(kept))
		for _, t := range kept {
			if v := b.Tokens[t]; v > 0 {
				tokens[t] = v
			}
		}
		s.Buckets[i] = aiusage.Bucket{Start: b.Start, Tokens: tokens}
	}
	return s
}

// usageSeg is the 今天/7 天/30 天/全部 picker: a fill1 track of small
// buttons; the active segment gets the fill2 capsule and ink text.
func (f *usageFeature) usageSeg(c *ui.Context) {
	l := tloc(c)
	labels := []string{l.UsageRangeToday(), l.UsageRange7d(), l.UsageRange30d(), l.UsageRangeAll()}
	ranges := []aiusage.Range{aiusage.RangeToday, aiusage.Range7d, aiusage.Range30d, aiusage.RangeAll}
	ui.Row(c.Key("seg")).Background(fill1).Radius(6).Padding(1, 1, 1, 1).Gap(1).Children(func() {
		for i, s := range labels {
			r := ranges[i]
			b := ui.ButtonBase(c.Key("seg-"+s)).Radius(4).Padding(1, 4, 1, 4).Label(s)
			on := f.rangeSel == r
			if on {
				b.Background(fill2)
			}
			b.Children(func() {
				tc := ink3
				if on {
					tc = ink
				}
				panelLabel(c, nil, s, 9.5, 600).TextColor(tc)
			})
			if b.Clicked() && f.rangeSel != r {
				f.rangeSel = r
				if f.localOn() && !f.localBusy {
					f.startLocal(f.deps.now())
				}
				f.invalidate()
			}
		}
	})
}

// scanCard fills the local section while there is no summary to show: the
// first scan's progress, the pre-scan 「正在统计…」, or a read error.
func (f *usageFeature) scanCard(c *ui.Context) {
	l := tloc(c)
	ui.Column(c.Key("scan")).Grow(1).MinHeight(0).AlignItems(ui.Center).Justify(ui.Center).Gap(6).Children(func() {
		switch {
		case f.localErr != nil && !f.localBusy:
			panelLines(c, l.UsageScanFailed(), 10.5, 0).TextAlign(ui.Center).TextColor(ink2)
		case f.localBusy:
			done, total := f.scanDone.Load(), f.scanTotal.Load()
			text := l.UsageScanFirst()
			var frac float32
			if total > 0 {
				n := min(100, done*100/total)
				text = l.UsageScanFirstPct(int(n))
				frac = float32(n) / 100
			}
			panelLabel(c, nil, text, 10.5, 0).TextColor(ink2)
			ui.Box(c.Key("sbar")).Width(130).Height(3).Radius(2).Background(fill3).Draw(func(p *ui.Painter, r ui.Rect) {
				if frac > 0 {
					p.Fill(ui.Rect{X: r.X, Y: r.Y, W: r.W * frac, H: r.H}, progress, 2)
				}
			})
			ui.Text(c, f.scanSources(c)).MaxLines(2).TextAlign(ui.Center).FontSize(9.5).TextColor(ink3)
		default:
			panelLabel(c, nil, l.UsageScanCounting(), 10.5, 0).TextColor(ink2)
		}
	})
}

// scanSources names the enabled local sources in the scan card's caption.
func (f *usageFeature) scanSources(c *ui.Context) string {
	l := tloc(c)
	var names []string
	for _, t := range localTools() {
		if f.localSourceOn(t) {
			names = append(names, toolName(t))
		}
	}
	if len(names) == 0 {
		return l.UsageNoLocalSources()
	}
	return l.UsageReadOnly(strings.Join(names, l.UsageListSeparator()))
}

// kpiGrid is the 2 × 2 metric block: Token, requests, cost and cache-hit
// rate, each a 9pt label over a 12pt value. The two cells whose number is
// a sum take a hover tip: the Token total splits into input, output and
// cache, and the cost line says how much of it is priced.
func (f *usageFeature) kpiGrid(c *ui.Context, s aiusage.Summary) {
	tot := s.Total
	l := tloc(c)
	ui.Grid(c.Key("kpi")).ColumnTracks(ui.Fr(1), ui.Fr(1)).GapY(1).GapX(8).FillWidth().
		Children(func() {
			f.kpi(c, "Token", usageTokens(totalTokens(tot)), false, usageTokenSub(l, tot))
			f.kpi(c, l.UsageKpiRequests(), fmt.Sprintf("%d", tot.Requests), false, "")
			f.kpi(c, l.UsageKpiCost(), usageCost(tot), true, usageCostSub(l, tot))
			f.kpi(c, l.UsageKpiCacheHit(), usageHitRate(tot), false, "")
		})
}

// kpi is one metric cell: label over value. money turns the value green
// only while a real price shows. tip, when not empty, explains the value
// on hover — the 2 × 2 grid is too tight to spell the parts out inline.
func (f *usageFeature) kpi(c *ui.Context, k, v string, money bool, tip string) {
	cell := ui.Column(c.Key("kpi-" + k)).Height(25).MinWidth(0)
	if tip != "" {
		cell.Tooltip(tip)
	}
	cell.Children(func() {
		panelLabel(c, nil, k, 9, 0).TextColor(ink3)
		col := ink
		if money && v != "—" {
			col = usageWeekly
		}
		panelLabel(c, nil, v, 11.5, 600).TextColor(col)
	})
}

// usageTokenSub splits the Token KPI into the parts it adds up: input,
// output and cache (read plus write) — the same three the local scan
// records separately.
func usageTokenSub(l locales.Messages, t aiusage.Totals) string {
	return l.UsageTokenBreakdown(
		usageTokens(t.Input),
		usageTokens(t.Output),
		usageTokens(t.CacheRead+t.CacheWrite))
}

// usageChart is the stacked bar chart: per bucket a column of per-tool
// segments (omp bottom, Codex middle, Claude Code top, as the mock's
// legend orders them), gridlines at half and top, the baseline, and the
// x labels under it.
func (f *usageFeature) usageChart(c *ui.Context, s aiusage.Summary) {
	buckets := s.Buckets
	var maxTokens int64 = 1
	for _, b := range buckets {
		var v int64
		for _, t := range localTools() {
			v += b.Tokens[t]
		}
		if v > maxTokens {
			maxTokens = v
		}
	}
	ui.Box(c.Key("chart")).FillWidth().Grow(1).MinHeight(0).Draw(func(p *ui.Painter, r ui.Rect) {
		p.Line(r.X, r.Y+0.25, r.X+r.W, r.Y+0.25, 0.5, usageGrid)
		p.Line(r.X, r.Y+r.H/2, r.X+r.W, r.Y+r.H/2, 0.5, usageGrid)
		p.Line(r.X, r.Y+r.H-0.25, r.X+r.W, r.Y+r.H-0.25, 0.5, usageBase)
		n := len(buckets)
		if n == 0 {
			return
		}
		slot := r.W / float32(n)
		gap := float32(3)
		if n > 40 {
			gap = 1
		}
		w := max(1, slot-gap)
		for i, b := range buckets {
			var tot int64
			for _, t := range localTools() {
				tot += b.Tokens[t]
			}
			if tot == 0 {
				continue
			}
			x0 := r.X + float32(i)*slot + (slot-w)/2
			y := r.Y + r.H
			topDone := false
			// Stacked bottom-up in localTools order: omp, Codex, Claude Code.
			for j := len(localTools()) - 1; j >= 0; j-- {
				t := localTools()[j]
				v := b.Tokens[t]
				if v <= 0 {
					continue
				}
				sh := max(0, float32(v)/float32(maxTokens)*(r.H-1))
				col := toolColor(t)
				if !topDone {
					var path ui.Path
					topRoundedRect(&path, x0, y-sh, w, sh, 2)
					p.FillPath(&path, col)
					topDone = true
				} else {
					p.Fill(ui.Rect{X: x0, Y: y - sh, W: w, H: sh}, col, 0)
				}
				y -= sh
			}
		}
	})
	ui.Row(c.Key("xl")).Justify(ui.SpaceBetween).Padding(0, 2, 0, 2).Children(func() {
		labels := f.xLabels(tloc(c), buckets)
		for _, i := range []int{0, 2, 4} {
			if i < len(labels) {
				panelLabel(c, nil, labels[i], 9, 0).TextColor(ink3)
			}
		}
	})
}

// topRoundedRect draws a bar-top path: a rectangle with only its top
// corners rounded.
func topRoundedRect(path *ui.Path, x, y, w, h, r float32) {
	r = min(r, min(w, h)/2)
	path.MoveTo(x, y+h)
	path.LineTo(x, y+r)
	path.QuadTo(x, y, x+r, y)
	path.LineTo(x+w-r, y)
	path.QuadTo(x+w, y, x+w, y+r)
	path.LineTo(x+w, y+h)
	path.Close()
}

// xLabels is the axis under the chart: clock marks for today, dates for
// the other ranges. The clock marks stay 24-hour numerals: five ticks
// share 167pt, which a 12-hour form with AM/PM cannot fit.
func (f *usageFeature) xLabels(l locales.Messages, buckets []aiusage.Bucket) []string {
	if f.rangeSel == aiusage.RangeToday {
		return []string{"00:00", "06:00", "12:00", "18:00", "24:00"}
	}
	labels := []string{"", "", "", "", ""}
	if len(buckets) == 0 {
		// No data in the range yet: the axis still names its span.
		now := f.deps.now()
		switch f.rangeSel {
		case aiusage.Range7d:
			return []string{l.UsageDayMonth(now.AddDate(0, 0, -6)), l.UsageDayMonth(now.AddDate(0, 0, -3)), l.UsageRangeToday()}
		case aiusage.Range30d:
			return []string{l.UsageDayMonth(now.AddDate(0, 0, -29)), l.UsageDayMonth(now.AddDate(0, 0, -14)), l.UsageRangeToday()}
		default:
			return []string{l.UsageMonth(now.AddDate(0, -12, 0)), l.UsageMonth(now.AddDate(0, -6, 0)), l.UsageRangeToday()}
		}
	}
	for i, frac := range []float64{0, 0.25, 0.5, 0.75, 1} {
		b := buckets[min(len(buckets)-1, int(frac*float64(len(buckets)-1)+0.5))]
		labels[i] = l.UsageDayMonth(b.Start)
	}
	return labels
}

// usageLegend is the chart's footnote: one chip per enabled local source.
func (f *usageFeature) usageLegend(c *ui.Context, s aiusage.Summary) {
	ui.Row(c.Key("legend")).Gap(10).Children(func() {
		for _, t := range []aiusage.Tool{aiusage.ToolOmp, aiusage.ToolClaude, aiusage.ToolCodex} {
			if !f.localSourceOn(t) {
				continue
			}
			ui.Row(c.Key("lg-" + string(t))).AlignItems(ui.Center).Children(func() {
				ui.Box(c).Size(6, 6).Radius(2).Background(toolColor(t)).Margin(0, 4, 0, 0)
				panelLabel(c, nil, toolName(t), 9.5, 0).TextColor(ink2)
			})
		}
	})
}

// --- formatting ------------------------------------------------------------

// totalTokens is a range total's full token count (input + output + both
// cache kinds), matching Bucket.Tokens' definition.
func totalTokens(t aiusage.Totals) int64 {
	return t.Input + t.Output + t.CacheRead + t.CacheWrite
}

// usageTokens compacts a token count with at most 3 significant digits:
// one decimal under 100 of the unit (4.7M / 39.3K), none at ≥100 (291M /
// 117K) — real-scale KPI lines must stay short enough to fit their cell.
func usageTokens(n int64) string {
	switch {
	case n <= 0:
		return "0"
	case n < 1_000:
		return fmt.Sprintf("%d", n)
	case n < 1_000_000:
		return compact(float64(n)/1e3, "K")
	case n < 1_000_000_000:
		return compact(float64(n)/1e6, "M")
	default:
		return compact(float64(n)/1e9, "B")
	}
}

// compact renders v with one decimal when v < 100 (dropping a trailing
// .0), none at ≥100: 2.5M and 4.7M keep the digit, 164M and 291M don't.
func compact(v float64, unit string) string {
	if v >= 100 {
		return fmt.Sprintf("%.0f%s", v, unit)
	}
	return strings.TrimSuffix(fmt.Sprintf("%.1f", v), ".0") + unit
}

// usageCost is the KPI's money line: ≈$42.96, + appended when some records
// had no price; — when nothing was priced at all.
func usageCost(t aiusage.Totals) string {
	if t.Cost <= 0 {
		return "—"
	}
	s := fmt.Sprintf("≈$%.2f", t.Cost)
	if t.Unpriced > 0 {
		s += "+"
	}
	return s
}

// usageCostSub explains the money line: only the tools' own prices count,
// and how many records carried none.
func usageCostSub(l locales.Messages, t aiusage.Totals) string {
	if t.Unpriced > 0 {
		return l.UsageCostUnpriced(int(t.Unpriced))
	}
	return l.UsageCostBuiltIn()
}

// usageHitRate is the cache-read share of everything read (input + cache).
func usageHitRate(t aiusage.Totals) string {
	den := t.Input + t.CacheRead
	if den <= 0 {
		return "—"
	}
	return fmt.Sprintf("%.0f%%", float64(t.CacheRead)/float64(den)*100)
}

// usageAgo renders the age of data in the language of l for the update
// stamp and the stale-data line: 刚刚 / N 分钟前 / N 小时前 / N 天前 (the
// caller's message adds 更新 or 的数据).
func usageAgo(l locales.Messages, t, now time.Time) string {
	d := now.Sub(t)
	switch {
	case d < time.Minute:
		return l.UsageAgoNow()
	case d < time.Hour:
		return l.UsageAgoMinutes(int(d.Minutes()))
	case d < 24*time.Hour:
		return l.UsageAgoHours(int(d.Hours()))
	default:
		return l.UsageAgoDays(int(d.Hours() / 24))
	}
}

// usageWait renders how long a held-back refresh must wait: whole seconds
// under a minute, whole minutes (rounded up) above it.
func usageWait(l locales.Messages, d time.Duration) string {
	if d < time.Minute {
		return l.UsageWaitSeconds(max(1, int(math.Ceil(d.Seconds()))))
	}
	return l.UsageWaitMinutes(int(math.Ceil(d.Minutes())))
}

// usageClockText writes a span under a day as hours and zero-padded
// minutes, 「1:24」, whichever language reads it: both countdown forms
// stay numeric so they fit the card.
func usageClockText(d time.Duration) string {
	return fmt.Sprintf("%d:%02d", int(d.Hours()), int(d.Minutes())%60)
}

// usageResetShort is the quota card's compact reset: 「1:24 后」 under a
// day, the weekday otherwise — the 203-wide card cannot hold the long
// form beside the numbers.
func usageResetShort(l locales.Messages, at, now time.Time) string {
	d := at.Sub(now)
	if d <= 0 {
		return l.UsageResetsSoon()
	}
	if d < 24*time.Hour {
		return l.UsageResetsInShort(usageClockText(d))
	}
	return l.UsageWeekday(at.Local())
}

// usageReset is usageResetIn in the source language, for the tests that
// pin the Chinese wording.
func usageReset(at, now time.Time) string {
	return usageResetIn(locales.In(""), at, now)
}

// usageResetIn is the card's reset time in the language of l: 「4:25
// 后重置」 under a day, else 「周六 02:00 重置」.
func usageResetIn(l locales.Messages, at, now time.Time) string {
	d := at.Sub(now)
	if d <= 0 {
		return l.UsageResetsSoon()
	}
	if d < 24*time.Hour {
		return l.UsageResetsClock(usageClockText(d))
	}
	local := at.Local()
	return l.UsageResetsAt(l.UsageWeekday(local), l.UsageClock(local))
}

// usageResetLong is the popover's reset time: 「3 小时 13 分后重置」 under
// a day (minutes under an hour), else 「4 天 18 小时后重置」.
func usageResetLong(l locales.Messages, at, now time.Time) string {
	d := at.Sub(now)
	if d <= 0 {
		return l.UsageResetsSoon()
	}
	if d >= 24*time.Hour {
		return l.UsageResetsInDH(int(d.Hours()/24), int(d.Hours())%24)
	}
	if d >= time.Hour {
		return l.UsageResetsInHM(int(d.Hours()), int(d.Minutes())%60)
	}
	return l.UsageResetsInMin(max(1, int(d.Minutes())))
}

// usageDeplete formats the depletion estimate as a clause the pace lines
// complete: 「1:42 后」 under a day, else 「3 天 2 小时后」.
func usageDeplete(l locales.Messages, d time.Duration) string {
	if d <= 0 {
		return l.UsageRunsOutNow()
	}
	if d >= 24*time.Hour {
		return l.UsageRunsOutDays(int(d.Hours()/24), int(d.Hours())%24)
	}
	return l.UsageRunsOutClock(usageClockText(d))
}

// --- settings ------------------------------------------------------------

// setAIUsage saves an aiUsage mutation through applySettings (the single
// path), like setNowPlaying; the page reads the settings every frame and
// tick, so a change applies without a restart.
func (a *shelf) setAIUsage(mutate func(*settings.Settings)) {
	wasOn := a.settings.AIUsage.Enabled
	next := a.settings
	mutate(&next)
	if !a.applySettings(next) {
		return
	}
	// Switching the feature off retires its in-flight work right away; the
	// next monitor tick must not find quota requests or scans still running.
	if wasOn && !next.AIUsage.Enabled && a.usage != nil {
		a.usage.stopWork()
	}
}
