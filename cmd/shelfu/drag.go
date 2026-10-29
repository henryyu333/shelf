//go:build darwin

package main

import (
	"log"
	"slices"
	"time"

	"github.com/egoist/mygo"

	"shelf/internal/mac"
)

// Incoming system drags: detection, opening the shelf near the notch, and the
// protection that keeps the Spaces bar from opening at the top edge.

// dragGuard owns the current filter, including a failed setup attempt so that
// the same error is not retried on every monitoring tick. A failure while
// untrusted is retried once Accessibility is granted, without a restart.
type dragGuard struct {
	stop      func()
	region    mac.DragGuardRegion
	attempted bool
	trusted   bool // Accessibility state at the last attempt
}

// dropBandH is the band's hold height: menu bar plus the guard's landing
// margin below it. The menu bar itself is max(32, notchH); the landing
// margin keeps the pre-shrink 16pt so the pushed cursor rests below the
// bar and inside the grown window.
func dropBandH(notchH int) int { return max(notchH+guardDropPad, 48) }

// menuBandH is the menu bar's height as the code knows it — the entry
// boundary of the band (the true menu bar is a point taller on some
// Macs, so the guard's first push still lands inside it).
func menuBandH(notchH int) int { return max(32, notchH) }

// dropBandX reports whether p lies within the band's horizontal reach:
// the notch plus dropBandPadX on each side, from the top down to the
// guard's landing row. Requires notch geometry: no notch, no band —
// without measured geometry nothing may invent a drop area.
func (a *shelf) dropBandX(p mygo.Point) bool {
	if a.notchW <= 0 || a.notchH <= 0 {
		return false
	}
	if a.island {
		// The band follows the floating capsule: the same horizontal
		// reach around it, from its top edge down to the guard's landing
		// row below it. Rows above the capsule stay the menu bar's.
		r := a.islandRect()
		_, baseH := a.anchorSize()
		return p.X >= r.X-dropBandPadX && p.X < r.X+r.Width+dropBandPadX &&
			p.Y >= r.Y && p.Y < r.Y+dropBandH(int(baseH))
	}
	return p.X >= a.notchX-dropBandPadX && p.X < a.notchX+a.notchW+dropBandPadX &&
		p.Y >= a.notchY && p.Y < a.notchY+dropBandH(a.notchH)
}

// dropBand is the band's ENTRY edge: inside the band's width, the cursor
// must be inside the menu bar itself. Reaching the landing rows below
// the bar from underneath never counts as entering — ordinary windows
// live there and a drag gliding past them must not wake the panel.
func (a *shelf) dropBand(p mygo.Point) bool {
	return a.dropBandX(p) && p.Y < a.sheetTop()+a.y+a.menuBand()
}

// updateDragBand applies the enter/hold state machine to a.inBand:
// entered only from the menu-bar rows, held anywhere in the band's width
// down to the guard's landing row. Callers reset a.inBand on a new drag
// serial before calling. Pure beyond the field write.
func (a *shelf) updateDragBand(p mygo.Point, active bool) {
	if a.inBand {
		a.inBand = active && !a.ownDrag && a.dropBandX(p)
	} else {
		a.inBand = active && !a.ownDrag && a.dropBand(p)
	}
	a.dropHot = a.inBand && !a.suppressed
}

// dragGuardRegion is the notch corridor the event filter clamps, in the
// same coordinates as the resident drop strip. While the drag has never
// entered the band the corridor is menu-bar high: its clamp lands on the
// bar's last row — inside the entry range, so the push itself enters the
// band. Once inside, the corridor reaches the landing row below the bar,
// inside the window that just grew for it. Drags that never entered are
// never pushed onto rows below the bar.
func (a *shelf) dragGuardRegion() mac.DragGuardRegion {
	_, baseH := a.anchorSize()
	h := a.menuBand() - 1
	if a.inBand {
		h = dropBandH(int(baseH)) - 1
	}
	// The corridor starts at the display's top edge so the Spaces gesture
	// stays blocked, but its landing row is inside the shell: with the
	// island that is its capsule (or the grown drop pill in-band), never a
	// row below it where the cursor could not reach the drop target.
	return mac.DragGuardRegion{
		X: a.stripCenter() - a.bandW()/2, Y: a.y,
		Width: a.bandW(), Height: a.sheetTop() + h,
	}
}

// dragSummonTypes are the drag pasteboard types whose shake may summon the
// basket: anything carrying files or image data. Text, link and app-window
// drags never summon it — they may still be dropped on the notch band.
var dragSummonTypes = []string{
	"public.file-url",
	"NSFilenamesPboardType",
	"com.apple.finder.node",
	"com.apple.NSFilePromiseItemMetaData",
	"com.apple.pasteboard.promised-file-url",
	"public.file-promise",
	"public.png",
	"public.tiff",
	"public.jpeg",
	"public.heic",
	"public.webp",
	"public.image",
}

// dragSummonsBasket reports whether this drag's declared types may wake
// the shake-to-summon basket. Pure; unit-tested.
func dragSummonsBasket(types []string) bool {
	for _, t := range types {
		if slices.Contains(dragSummonTypes, t) {
			return true
		}
	}
	return false
}

// notchGuardOn reports whether the drop-protection corridor belongs to the
// current shell: the notch, or the island while it still sits on its default
// row. A moved island is not the screen's top edge, and the corridor also
// pushes a dragged pointer down onto its landing row, so it must not exist
// there. (The user's own press on the island is a separate case: the filter
// is parked for it in syncDragGuard, so the corridor never acts on it.)
func (a *shelf) notchGuardOn() bool {
	return !(a.island && a.islandDY != 0)
}

// parkNotchFilter is the native filter's park switch. Tests replace it to
// observe what the monitor asks the guard to do without a real event tap.
var parkNotchFilter = mac.ParkDragGuard

// Protect the notch's approach corridor, including its centre. The lower edge
// is below the camera housing but still inside the shelf's drag trigger.
// The event filter (Accessibility only) is armed once and stays up for file
// drags and window moves alike; the cursor pin needs no permission and runs
// only while a file drag is in progress.
func (a *shelf) syncDragGuard() {
	// The user's own press on the island is not an incoming drag, and
	// neither is a press on any control in the panel: the corridor clamps
	// left-button drags onto its landing row, which would add vertical
	// travel the user never made — a 1–3pt wobble on a band icon under the
	// corridor would push the pointer off the target and cancel the press.
	// The filter is parked for the whole gesture instead — no teardown, no
	// re-arm — and normal file-drag protection comes back with the release
	// or as soon as the press becomes a drag of our own.
	parkNotchFilter((a.islandPress.active || a.ownPress) && !a.dragging && !a.ownDrag)
	// The guard exists to keep the Spaces edge gesture from firing at the
	// screen's top edge, under the notch. An island the user moved away from
	// that row is not there, so the corridor — which also pushes the cursor
	// down onto its landing row — must not exist either.
	if a.win == nil || !a.notchGuardOn() {
		a.stopDragGuard()
		return
	}
	region := a.dragGuardRegion()
	mac.SetDragPin(region, a.dragging && !a.quitting)
	if a.guard.stop != nil {
		if a.guard.region != region {
			mac.SetDragGuardRegion(region)
			a.guard.region = region
		}
		return
	}
	if a.guard.attempted && (a.guard.trusted || !mac.AccessibilityTrusted()) {
		return
	}
	stop, err := mac.StartDragGuard(region)
	a.guard = dragGuard{stop: stop, region: region, attempted: true, trusted: mac.AccessibilityTrusted()}
	if err != nil {
		log.Printf("刘海拖拽保护未启用：%v", err)
	}
}

// beginOwnDrag marks the drag now starting as ours (a file dragged out of
// the shelf) and puts the notch corridor back at once: dragging our own
// file to the screen's top edge must still be pushed onto the drop band.
func (a *shelf) beginOwnDrag() {
	a.ownDrag = true
	a.syncDragGuard()
}

func (a *shelf) stopDragGuard() {
	mac.SetDragPin(mac.DragGuardRegion{}, false)
	if a.guard.stop != nil {
		a.guard.stop()
	}
	a.guard = dragGuard{}
}

// pointerDragged is the NSEvent mouse-dragged callback. Finder has already
// declared the drag pasteboard by the first pixel of movement; waiting for
// the 30 ms poll lets the cursor reach Y=0 first.
func (a *shelf) pointerDragged() {
	if a.quitting {
		return
	}
	active, serial := dragState()
	if serial != a.dragSerial {
		// A new drag: remember whether the panel was already open (and on
		// which page) before show() flips visible — the drop zone is
		// suppressed only for drags that started over an open tools page.
		a.dragSerial = serial
		a.dragOpenTools = a.visible && a.tab == pageTools
		a.suppressed, a.inBand = false, false // a new drag re-enters
	}
	if !active {
		return
	}
	a.dragging, a.pointerDrag = true, true
	p := cursorPoint()
	if !a.visible && !insideRect(p, a.dispBounds) {
		a.place(p)
	}
	a.updateDragBand(p, true)
	if !a.suppressed {
		a.setPeek(true)
	}
	// Collapsed pin strips wake on the drag event itself, not the 30 ms
	// poll: a release inside the wake distance this same beat lands on an
	// already-open card.
	if !a.ownDrag && dragSummonsBasket(dragTypes()) {
		a.pinWakeDrag(p)
	}
	a.syncDragGuard()
	a.syncMousePassThrough()
	if a.dropHot && !a.visible {
		// 拖入首次触及刘海投放带时给一下力反馈，提示落点在顶部。
		mac.HapticTick()
		a.show(true)
	}
}

// monitorDrag updates placement, drag protection and interaction on the main thread.
func (a *shelf) monitorDrag(now time.Time) {
	// A press in our own window ends with the button: the corridor comes
	// back for the next gesture (answered here because the app observes no
	// global mouse-up).
	if a.ownPress && !dragLeftDown() {
		a.ownPress = false
		a.syncDragGuard()
	}
	active, serial := dragState()
	if serial != a.dragSerial {
		a.dragSerial = serial
		a.dragOpenTools = a.visible && a.tab == pageTools // 同 pointerDragged
		a.suppressed, a.inBand = false, false             // a new drag re-enters
	}
	a.dragging = active
	if !active {
		a.suppressed = false
		if !dragLeftDown() {
			a.pointerDrag = false
		}
	}
	p := cursorPoint()
	if !a.visible && !insideRect(p, a.dispBounds) {
		a.place(p)
	}
	// A press held on the closed island owns the gesture: it is a move (or a
	// tap, on release) and nothing else may react to that pointer — no drop
	// band, no peek, no auto-hide, no window-snap session.
	if a.islandDragTick(p, dragLeftDown()) {
		return
	}
	// 拖窗口分屏：这一次按下如果是拖动别的应用的窗口，而不是系统拖拽
	// （拖文件会写 NSDragPboard），就跟着指针走并接管刘海面板。
	a.snapTick(now, p, active)
	// 摇晃召唤：只在系统拖拽进行中、且不是自己拖出或已放弃拖放的会话里
	// 采样；竖直拖动没有 x 采样变化，不会误触发篮子。只有文件或图片
	// 拖得动篮子——拖标签、链接、纯文字摇不响应（它们仍可放进刘海带）。
	summonable := active && !a.suppressed && !a.ownDrag && dragSummonsBasket(dragTypes())
	if summonable {
		if a.shake.add(now, float64(p.X)) {
			a.showBasket(p)
		}
	} else {
		a.shake.reset()
	}
	overAnchor := a.overAnchor(p)
	a.updateDragBand(p, active)
	// pinTick runs before the peek logic below so holdPeek and the fold
	// both see the pinReturning this tick computed; its fileDrag flag is
	// the summonable drag minus the panel-suppression — a drag the user
	// cancelled on the panel still wakes the desktop strips.
	a.pinTick(now, p, dragLeftDown(), active && !a.ownDrag && dragSummonsBasket(dragTypes()))
	// The pointer inside the notch drop band grows 「松开放入」 and takes
	// the drop; outside it the shell only shows the narrow hint.
	// The peek wings reach past the notch on both sides: the pointer must
	// be able to rest on them without the shell folding shut under it.
	// pinReturning holds the 松开放回 pill for the whole put-back zone,
	// which reaches past the pill's own body.
	holdPeek := overAnchor || a.pinReturning || (a.peeking && a.overPeekPill(p))
	// Dragging or hovering reveals the pill; only a click or a real incoming
	// drag in the trigger zone can open the full panel.
	if active || holdPeek {
		if !a.suppressed || overAnchor {
			a.setPeek(true)
		}
	}
	a.syncDragGuard()
	a.syncMousePassThrough()
	if a.inBand && !a.suppressed && !a.visible {
		// Incoming drags open immediately; empty-handed hover never does.
		mac.HapticTick()
		a.show(true)
	}
	if !active && !a.visible && !holdPeek {
		a.setPeek(false)
	}
	// 自动收起：仅拖拽唤出的托盘在未收到内容、光标离开面板与触发区
	// edgeLeaveDelay 后缩回刘海；点击探头/托盘唤出的托盘由用户显式收起。
	// 动画途中不判离开。
	if a.visible && a.edgeOpened && !a.received {
		panel := a.panelDropRect()
		inside := p.X >= panel.X-24 && p.X < panel.X+panel.Width+24 &&
			p.Y >= panel.Y && p.Y < panel.Y+panel.Height+24
		// Keep the panel alive for the whole card gesture, including
		// drag-out and settling; restart the leave delay afterward.
		tp := a.toolsPage()
		holdingCard := tp != nil && tp.press != nil
		if !inside && !a.inBand && !holdingCard && a.animationStart.IsZero() {
			if a.leftSince.IsZero() {
				a.leftSince = now
			}
			if now.Sub(a.leftSince) >= edgeLeaveDelay {
				a.hide(hidePassive)
			}
		} else {
			a.leftSince = time.Time{}
		}
	} else {
		a.leftSince = time.Time{}
	}

	a.basketTick(now, p)
}
func insideRect(p mygo.Point, r mygo.Rectangle) bool {
	return p.X >= r.X && p.X < r.X+r.Width && p.Y >= r.Y && p.Y < r.Y+r.Height
}
