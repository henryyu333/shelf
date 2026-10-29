//go:build darwin

package main

// The panel's design tokens, in one place: colours, corner radii, spacing,
// type sizes, springs and durations. Everything else references these; a
// number that describes the design belongs here, not in a page.
//
// Colours are white opacities on the black shell, so one palette serves both
// appearances. The only accent is the system blue; the state colours (green,
// orange, red) are the documented exception and appear only where the design
// puts them (status dots, quota rings, cost figures).

import (
	"time"

	"github.com/egoist/mygo/ui"
)

// Colours.
var (
	ink      = ui.Hex("#F5F5F7")            // 主文字、主图标
	ink2     = ui.RGBA(255, 255, 255, 0.62) // 次文字、非激活图标、歌手
	ink3     = ui.RGBA(255, 255, 255, 0.48) // 辅助说明、时间、计数（4.9:1，HIG 4.5:1）
	fill1    = ui.RGBA(255, 255, 255, 0.06) // 悬停底、搜索框、卡片底
	fill2    = ui.RGBA(255, 255, 255, 0.10) // 激活胶囊、按钮悬停、分隔线
	fill3    = ui.RGBA(255, 255, 255, 0.16) // 进度条与音量条轨道
	accent   = ui.Hex("#0A84FF")            // 唯一的强调色
	accent16 = accent.Alpha(0.16)           // 网格选中底
	accent24 = accent.Alpha(0.24)           // 列表选中底
	progress = ui.RGBA(255, 255, 255, 0.88) // 进度条已播部分
	danger   = ui.Hex("#FF453A")            // 错误、额度告急
	success  = ui.Hex("#30D158")            // 成功、每周额度、费用绿
	warning  = ui.Hex("#FF9F0A")            // 进行中、5 小时额度、节奏告警

	// Surfaces outside the pure-black shell: raised cards over it, the ⊖
	// badge, and the glyph knockout inside a coloured dot.
	surface      = ui.Hex("#000000") // 外壳、固定项、篮子
	surfaceRaise = ui.Hex("#1C1C1E") // 悬停明细、目录卡片、格式角标
	surfaceBadge = ui.Hex("#3A3A3C") // ⊖ 移除角标
	knockout     = ui.Hex("#000000") // 彩色圆点上的字
)

// The Settings window's own surfaces. Its sidebar is pure black like the
// shell; its page panel and the cards inside sit a step above it, and its
// controls have tints the panel's black-on-black cards never needed. Pages
// take every other colour (inks, accent, state colours) from the palette
// above.
var (
	setPanel     = ui.Hex("#0E0F11")             // 右侧页面板
	setCard      = ui.Hex("#18191C")             // 页内卡片
	setCardEdge  = ui.RGBA(255, 255, 255, 0.035) // 卡片描边
	setSep       = ui.RGBA(255, 255, 255, 0.06)  // 行分隔线
	setSelect    = ui.Hex("#1D1F23")             // 选中的侧栏项
	setCtl       = ui.Hex("#1F2024")             // 按钮、分段轨道、键帽
	setCtlHover  = ui.Hex("#2A2C31")             // 悬停
	setCtlActive = ui.Hex("#34363C")             // 分段选中
	setStatus    = ui.Hex("#141518")             // 侧栏底部的权限状态
	setLabel     = ui.RGBA(255, 255, 255, 0.40)  // 分组小标、表头
)

// The older pages' names for the inks.
var (
	hairline = fill2
)

// Corner radii, in points.
const (
	rOverview = 22 // 总览外壳底角
	rPage     = 24 // 页面外壳底角
	rCard     = 14 // 卡片、投放区、明细浮层
	rTile     = 12 // 托盘格子
	rRow      = 8  // 列表行、搜索框
	rPill     = 11 // 22pt 胶囊
)

// Spacing, in points.
const (
	padX        = 12 // 外壳左右内边距
	padBottom   = 12 // 外壳底边
	bodyGap     = 6  // 顶栏到内容区
	cardGap     = 6  // 卡片之间的间距
	gapGrid     = 4  // 托盘格子、页面图标之间的间距
	bodyActions = 22 // 页面在内容区顶部的动作行
	actionsGap  = 4  // 动作行到页面内容
)

// Type sizes, in points (HIG: macOS body 13, small 11, footnote 10; no thin
// weights, 9pt floor).
const (
	typeTitle   = 15   // 正在播放页的歌名
	typeBody    = 13   // 正文、行文字
	typeCallout = 12   // 动作卡标题、剪贴板正文
	typeSub     = 11   // 小标题、胶囊、返回行
	typeFoot    = 10.5 // 格子文件名、来源 · 时间
	typeMini    = 10   // 计数、单位
	typeMicro   = 9    // 图表标签
)

// Durations, in seconds of wall clock.
const (
	durTab       = 240 * time.Millisecond // 切页：胶囊滑动、旧页淡出后新页淡入
	durClose     = 220 * time.Millisecond // 收起外壳
	durCloseFade = 120 * time.Millisecond // 收起时内容先淡出
	durPeek      = 160 * time.Millisecond // 探头形态之间
	durCapsule   = 380 * time.Millisecond // 完成胶囊长出
	durWobble    = 520 * time.Millisecond // 编辑摆动一圈
	durSettle    = 240 * time.Millisecond // 松手吸附
	durLongPress = 500 * time.Millisecond // 长按进入编辑
	durFade      = 150 * time.Millisecond // 减少动态效果时唯一保留的淡入淡出
	durToast     = 2500 * time.Millisecond
	durToastFade = 220 * time.Millisecond
)

// Springs: the shell's motion is one vocabulary of overshoot.
var (
	panelOpenSpring = ui.Animation{Spring: ui.SpringOptions{Duration: 360 * time.Millisecond, Bounce: 0.18}}
	levelSpring     = ui.Animation{Spring: ui.SpringOptions{Duration: 360 * time.Millisecond, Bounce: 0.18}}
	contentSpring   = ui.Animation{Spring: ui.SpringOptions{Duration: 300 * time.Millisecond, Bounce: 0}, Delay: 40 * time.Millisecond}
	openSpring      = ui.Animation{Spring: ui.SpringOptions{Duration: durCapsule, Bounce: 0.28}}
	closeSpring     = ui.Animation{Spring: ui.SpringOptions{Duration: durClose, Bounce: 0}}
	peekSpring      = ui.Animation{Spring: ui.SpringOptions{Duration: durPeek, Bounce: 0.15}}
	retractSpring   = ui.Animation{Spring: ui.SpringOptions{Duration: 300 * time.Millisecond, Bounce: 0}}
	shakeSpring     = ui.Animation{Spring: ui.SpringOptions{Duration: 300 * time.Millisecond, Bounce: 0}}
	fadeAnimation   = ui.Animation{Duration: durFade, Ease: ui.EaseOut}
)

// reduceMotion reports whether motion should collapse to fades only.
func reduceMotion(c *ui.Context) bool { return c.Preferences().ReduceMotion }

// fadeOr returns the spring-backed animation normally and the plain fade
// when the desktop asks for less motion: no scale, no travel, no bounce.
func fadeOr(c *ui.Context, a ui.Animation) ui.Animation {
	if reduceMotion(c) {
		return fadeAnimation
	}
	return a
}

// Symbols the overview and the pages share.
var (
	overviewNavSymbol = symbol(`<rect x="4" y="4" width="7" height="7" rx="2"/><rect x="13" y="4" width="7" height="7" rx="2"/><rect x="4" y="13" width="16" height="7" rx="2"/>`)
	checkSymbol       = symbol(`<path d="M5 12.5l4.5 4.5L19 7.5"/>`)
	warnSymbol        = symbol(`<circle cx="12" cy="12" r="8.5"/><path d="M12 7.5v5.5M12 16.5v.1"/>`)
	downloadSymbol    = symbol(`<path d="M12 4v10m-4-4 4 4 4-4M5 19h14"/>`)
)
