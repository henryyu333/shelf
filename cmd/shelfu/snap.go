//go:build darwin

package main

// 拖窗口到刘海分屏: a window dragged by its title bar toward the notch grows
// the 排列窗口 panel, the hovered tile previews its target on that display,
// and releasing the button arranges the window with Accessibility.

import (
	"errors"
	"fmt"
	"math"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"shelf/internal/mac"
	"shelf/locales"
)

const (
	snapW, snapH   = 360, 150 // 排列窗口面板，从刘海长出
	snapPadX       = 12
	snapTitleTop   = 9
	snapGridTop    = 40
	snapTileW      = 108
	snapTileH      = 46
	snapTileGap    = 6
	snapTileR      = 11
	snapMiniW      = 34
	snapMiniH      = 22
	snapGhostR     = 12
	snapGhostEdge  = 2
	snapZonePad    = 8  // 触发区相对刘海的水平、上沿外扩
	snapZoneBottom = 40 // 触发区相对刘海下沿再向下
	snapDragDist   = 4.0
	snapMovedDist  = 2.0 // AX 帧位置变化多少算窗口真的在动
	snapGhostOmega = 18  // 预览框弹簧角速度（rad/s，临界阻尼）
	snapConfirmAt  = 120 * time.Millisecond
	snapConfirmGap = 180 * time.Millisecond
	snapConfirmMax = 2
	snapConfirmTol = 2.0
)

// snapTarget is one arrangement: the horizontal span of the window it
// covers (as fractions of the visible area), drawn in the tile's small
// screen icon.
type snapTarget struct {
	From, To float32
}

// snapTargets is the design's order: 左半、全屏、右半、左 1/3、中 1/3、右 1/3;
// snapTargetName says each one.
var snapTargets = []snapTarget{
	{0, 0.5},
	{0, 1},
	{0.5, 1},
	{0, 1.0 / 3},
	{1.0 / 3, 2.0 / 3},
	{2.0 / 3, 1},
}

// snapTargetName is the tile's name in the language of l.
func snapTargetName(l locales.Messages, slot int) string {
	switch slot {
	case 0:
		return l.SnapLeftHalf()
	case 1:
		return l.SnapFull()
	case 2:
		return l.SnapRightHalf()
	case 3:
		return l.SnapLeftThird()
	case 4:
		return l.SnapMidThird()
	}
	return l.SnapRightThird()
}

// snapGhost is the transparent target preview: one floating window with an
// accent-tinted, accent-outlined rounded rect. It never takes the mouse.
type snapGhost struct {
	win            *mygo.Window
	frame          mygo.Rectangle // last applied screen rect
	target         mygo.Rectangle
	x, y, w, h     float64
	vx, vy, vw, vh float64
	at             time.Time
}

// snapConfirm is an arrangement applied at release that the shelf keeps
// checking for a moment. The window server may finish its own drag-time
// tiling after the AX frame is set; a second look and one re-apply makes
// ShelfU's target stick.
type snapConfirm struct {
	win    snapWindow
	target mac.AXRect
	resize bool
	at     time.Time
	tries  int
}

// snapWindow is the Accessible window a session follows. The production
// value is mac.AXWindowRef; tests supply a fake without touching AX.
type snapWindow interface {
	Valid() bool
	Frame() (mac.AXRect, bool)
	Settable() (move, resize bool)
	MoveResize(r mac.AXRect, resize bool) error
	Release()
}

// snapState is one window-drag session. Zero is idle.
type snapState struct {
	armed     bool
	press     mygo.Point
	win       snapWindow
	start     mac.AXRect   // window frame at the press
	frame     mac.AXRect   // latest frame read while dragging
	move      bool         // the window's position is settable
	resize    bool         // the window's size is settable
	trusted   bool         // Accessibility at the press
	dragging  bool         // the press turned out to be a window drag
	hot       int          // hovered tile, -1 for none
	panel     bool         // the 排列窗口 panel is (or is closing as) the open panel
	drawn     bool         // the shell currently drawn is the snap box
	wasOpen   bool         // the shelf panel was already open when the snap panel took over
	prevLevel int          // that panel's level, restored when the snap panel leaves
	confirm   *snapConfirm // the arrangement still being verified after the drag
	ghost     *snapGhost
}

// Seams for the headless tests and the smoke run.
var (
	snapAXWindowAt = func(x, y float64) (snapWindow, int, bool) {
		w, pid, ok := mac.AXWindowAt(x, y)
		if !ok {
			return nil, 0, false
		}
		return w, pid, true
	}
	snapOwnPID    = mac.OwnPID
	snapOwnWindow = func(a *shelf, f mac.AXRect) bool { return a.snapOwnWindowFrame(f) }
	// snapInvalidate repaints the shelf when a tile becomes (or stops
	// being) the hovered one. A seam so the headless test can count it.
	snapInvalidate = func(a *shelf) {
		if a.win != nil {
			a.win.Invalidate()
		}
	}
	// snapWorkAreaAt picks the display a window lives on; a seam because
	// Display queries need the running app, headless tests do not have.
	snapWorkAreaAt = func(p mygo.Point) mygo.Rectangle {
		return mygo.Screen.DisplayNearestPoint(p).WorkArea
	}
)

// snapSlotRect is one arrangement's target in a display's visible frame
// (menu bar and Dock excluded). Needle edges share the boundary, so the six
// targets tile the frame exactly. Pure; unit-tested.
func snapSlotRect(work mygo.Rectangle, slot int) mygo.Rectangle {
	if slot < 0 || slot >= len(snapTargets) {
		return mygo.Rectangle{}
	}
	x0, x1 := 0, work.Width
	switch slot {
	case 0: // 左半
		x1 = work.Width / 2
	case 1: // 全屏
	case 2: // 右半
		x0 = work.Width / 2
	case 3, 4, 5: // 三等分
		i := slot - 3
		x0, x1 = work.Width*i/3, work.Width*(i+1)/3
	}
	return mygo.Rectangle{X: work.X + x0, Y: work.Y, Width: x1 - x0, Height: work.Height}
}

// snapAnchorRect is the resting anchor on screen: the measured hardware
// notch, or the floating island capsule. islandTop is where the shell
// starts inside the window (0 for the notch), so the trigger, the panel
// and the drawn shell all share one origin. Pure; unit-tested.
func snapAnchorRect(stripCenter, y, islandTop int, w, h float32) mygo.Rectangle {
	return mygo.Rectangle{
		X: stripCenter - int(w/2), Y: y + islandTop,
		Width: int(w + 0.5), Height: int(h + 0.5),
	}
}

// snapTriggerRect is the anchor's hot zone: its rectangle grown by
// snapZonePad on every side and another snapZoneBottom below its lower
// edge. Under the island style it follows the floating capsule. Pure;
// unit-tested.
func snapTriggerRect(anchor mygo.Rectangle) mygo.Rectangle {
	return mygo.Rectangle{
		X:      anchor.X - snapZonePad,
		Y:      anchor.Y - snapZonePad,
		Width:  anchor.Width + 2*snapZonePad,
		Height: anchor.Height + 2*snapZonePad + snapZoneBottom,
	}
}

// snapPanelRect is the panel's on-screen box: centred on the anchor, its
// top edge where the shell starts (the notch's top, or the island's
// floating top). Pure.
func snapPanelRect(stripCenter, top int) mygo.Rectangle {
	return mygo.Rectangle{X: stripCenter - snapW/2, Y: top, Width: snapW, Height: snapH}
}

// snapTileRect is one tile's box in screen coordinates. Pure.
func snapTileRect(panel mygo.Rectangle, slot int) mygo.Rectangle {
	col, row := slot%3, slot/3
	return mygo.Rectangle{
		X:      panel.X + snapPadX + col*(snapTileW+snapTileGap),
		Y:      panel.Y + snapGridTop + row*(snapTileH+snapTileGap),
		Width:  snapTileW,
		Height: snapTileH,
	}
}

// snapHotSlot is the tile under p, or -1 over a gap or outside the grid.
// Pure; unit-tested.
func snapHotSlot(panel mygo.Rectangle, p mygo.Point) int {
	for i := range snapTargets {
		if insideRect(p, snapTileRect(panel, i)) {
			return i
		}
	}
	return -1
}

// snapIsWindowDrag decides whether an armed press has become a window drag.
// A confirmed window whose position is changing wins even when the drag
// pasteboard changed: macOS starts a drag session for some window moves
// too. A file drag leaves the window still, so it never reaches that test.
// Without Accessibility the panel still appears, greyed out, so the user
// learns what is missing — movement alone is the only signal available
// then. Pure; unit-tested.
func snapIsWindowDrag(trusted, pressedWindow, movable, fileDrag bool, distance, frameMoved float64) bool {
	if trusted && pressedWindow && movable && frameMoved >= snapMovedDist {
		return true
	}
	if fileDrag {
		return false
	}
	if !trusted {
		return distance >= snapDragDist
	}
	return false
}

// snapPress starts (or replaces) a window-drag session at a left press.
// Presses on the shelf and its own interface windows never arm it.
func (a *shelf) snapPress(inside bool, button int, p mygo.Point) {
	if button != 0 || inside {
		return
	}
	a.cancelSnapConfirm()
	a.endSnapSession(false)
	if a.quitting || !a.settings.SnapWindows || a.notchW <= 0 || a.notchH <= 0 {
		return
	}
	s := &a.snap
	s.armed, s.press, s.hot = true, p, -1
	s.trusted = accessibilityProbe()
	if !s.trusted {
		return
	}
	w, pid, ok := snapAXWindowAt(float64(p.X), float64(p.Y))
	if !ok {
		s.armed = false
		return
	}
	move, resize := w.Settable()
	f, fok := w.Frame()
	if !move || !fok {
		w.Release()
		s.armed = false
		return
	}
	if pid == snapOwnPID() && snapOwnWindow(a, f) {
		w.Release()
		s.armed = false
		return
	}
	s.win, s.move, s.resize = w, move, resize
	s.start, s.frame = f, f
}

// snapOwnWindowFrame reports whether f is one of ShelfU's own interface
// windows: the notch overlay, a desktop pin, the basket, the preview, the
// Settings or 重新命名 window. AXWindowNumber is unsupported on macOS 27,
// so the frame is what identifies them. A normal window of the same
// process (the smoke test's, for one) is not ShelfU's interface and may be
// arranged.
func (a *shelf) snapOwnWindowFrame(f mac.AXRect) bool {
	if f.Width <= 0 || f.Height <= 0 {
		return false
	}
	var handles []uintptr
	if a.win != nil {
		handles = append(handles, a.win.NativeHandle())
	}
	if a.settingsWin != nil {
		handles = append(handles, a.settingsWin.NativeHandle())
	}
	if a.rn != nil && a.rn.win != nil {
		handles = append(handles, a.rn.win.NativeHandle())
	}
	if a.basket.win != nil {
		handles = append(handles, a.basket.win.NativeHandle())
	}
	if a.snap.ghost != nil && a.snap.ghost.win != nil {
		handles = append(handles, a.snap.ghost.win.NativeHandle())
	}
	for _, it := range a.pins {
		if it.win != nil {
			handles = append(handles, it.win.NativeHandle())
		}
	}
	for _, h := range handles {
		num := mac.WindowNumber(h)
		if num == 0 {
			continue
		}
		r, ok := mac.WindowScreenRect(num)
		if !ok {
			continue
		}
		if math.Abs(r.Origin.X-f.X) <= 1 && math.Abs(r.Origin.Y-f.Y) <= 1 &&
			math.Abs(r.Size.Width-f.Width) <= 1 && math.Abs(r.Size.Height-f.Height) <= 1 {
			return true
		}
	}
	return false
}

// snapTick follows an armed press while it is held and finishes the
// session on release. fileDrag is the system drag pasteboard state: a drag
// that wrote the pasteboard is a file drag, not a window move.
func (a *shelf) snapTick(now time.Time, p mygo.Point, fileDrag bool) {
	a.snapConfirmTick(now)
	s := &a.snap
	if s.armed {
		dx, dy := float64(p.X-s.press.X), float64(p.Y-s.press.Y)
		moved, window := 0.0, s.win != nil && s.win.Valid()
		if window {
			if f, ok := s.win.Frame(); ok {
				s.frame = f
				moved = math.Abs(f.X-s.start.X) + math.Abs(f.Y-s.start.Y)
			}
		}
		movingWindow := s.trusted && window && s.move && moved >= snapMovedDist
		if fileDrag && !movingWindow {
			// A drag that wrote the pasteboard and left the window still
			// is a file drag; the drop zone keeps that gesture.
			a.endSnapSession(true)
		} else if down := dragLeftDown(); !down {
			a.snapRelease(p)
		} else {
			s.dragging = snapIsWindowDrag(s.trusted, window, s.move, fileDrag,
				math.Hypot(dx, dy), moved)
			if !s.dragging {
				a.advanceGhost(now)
				return
			}
			trigger := snapTriggerRect(a.snapAnchor())
			panel := a.snapPanelBox()
			if !s.panel && insideRect(p, trigger) {
				a.openSnapPanel()
			}
			if s.panel {
				// Once open, the panel itself is part of the active area:
				// the pointer must be able to travel down to the tiles.
				if !insideRect(p, trigger) && !insideRect(p, panel) {
					a.endSnapSession(true)
					a.advanceGhost(now)
					return
				}
				a.snapHover(p)
			}
		}
	}
	a.advanceGhost(now)
}

// snapHover updates the hovered tile and the preview box. Without
// Accessibility there is nothing to arrange: no tile lights up and no
// preview appears, only the permission row.
func (a *shelf) snapHover(p mygo.Point) {
	s := &a.snap
	prev := s.hot
	s.hot = -1
	if s.trusted && s.win != nil && s.win.Valid() {
		s.hot = snapHotSlot(a.snapPanelBox(), p)
	}
	if s.hot != prev {
		// The hovered tile is the panel's only changing picture while the
		// user drags another app's window; nothing else redraws it.
		snapInvalidate(a)
	}
	if s.hot < 0 {
		a.closeGhost()
		return
	}
	target := snapSlotRect(snapWorkAreaAt(screenPointOf(s.frame)), s.hot)
	a.showGhost(target)
}

// snapRelease finishes the press: arrange on the hovered tile, or cancel.
// Without Accessibility a panel that appeared stays open with what it is
// waiting for; the user can click 开启 (or anywhere else to dismiss it).
func (a *shelf) snapRelease(p mygo.Point) {
	s := &a.snap
	hadPanel := s.panel
	target := -1
	// Only a panel that actually opened, and is still open at the release,
	// may arrange anything. A hidden 360 × 150 box is not a target: the
	// drag can end beside the notch without ever opening it.
	if s.panel && a.visible && s.dragging && s.trusted && s.win != nil && s.win.Valid() {
		// Read the window once: the tile is computed from the same panel
		// geometry that is logged and used for the move.
		if f, ok := s.win.Frame(); ok {
			s.frame = f
		}
		target = snapHotSlot(a.snapPanelBox(), p)
	}
	resizeErr, moveErr := false, false
	resize := s.resize
	if target >= 0 {
		r := snapSlotRect(snapWorkAreaAt(screenPointOf(s.frame)), target)
		want := mac.AXRect{
			X: float64(r.X), Y: float64(r.Y),
			Width: float64(r.Width), Height: float64(r.Height),
		}
		err := s.win.MoveResize(want, resize)
		switch {
		case errors.Is(err, mac.ErrAXResize):
			resizeErr = true
		case err != nil:
			moveErr = true
		}
		// The window server can finish its drag-time tiling a frame or two
		// after this call and overwrite the frame. Keep the reference and
		// look again; a mismatch is re-applied once or twice.
		s.confirm = &snapConfirm{win: s.win, target: want, resize: resize,
			at: time.Now().Add(snapConfirmAt)}
		s.win = nil
	}
	keepForPermission := hadPanel && !s.trusted
	a.endSnapSession(!keepForPermission)
	// The result is posted after the panel is gone (or the panel that was
	// underneath is back): with no shelf panel up it grows out of the notch
	// as a capsule instead of landing in a pill no one can see.
	switch {
	case resizeErr || (target >= 0 && !resize):
		a.toastStatus(a.appLoc().SnapCantResize(), time.Now().Add(4*time.Second), statusInfo)
	case moveErr:
		a.toastStatus(a.appLoc().SnapCantMove(), time.Now().Add(4*time.Second), statusErr)
	}
}

// snapConfirmTick checks the arrangement after the drag has settled. A
// frame the window server (or the app) changed is set once more; after
// snapConfirmMax re-applies the shelf accepts the window's own frame.
func (a *shelf) snapConfirmTick(now time.Time) {
	c := a.snap.confirm
	if c == nil || now.Before(c.at) {
		return
	}
	f, ok := c.win.Frame()
	if ok && snapFrameNear(f, c.target) {
		c.win.Release()
		a.snap.confirm = nil
		return
	}
	if c.tries >= snapConfirmMax {
		c.win.Release()
		a.snap.confirm = nil
		return
	}
	c.tries++
	c.at = now.Add(snapConfirmGap)
	c.win.MoveResize(c.target, c.resize)
}

// cancelSnapConfirm drops a pending verification without touching the
// window (quit path).
func (a *shelf) cancelSnapConfirm() {
	if c := a.snap.confirm; c != nil {
		c.win.Release()
		a.snap.confirm = nil
	}
}

// snapFrameNear reports whether two frames are the same arrangement
// within the one-point rounding the window server applies.
func snapFrameNear(a, b mac.AXRect) bool {
	return math.Abs(a.X-b.X) <= snapConfirmTol && math.Abs(a.Y-b.Y) <= snapConfirmTol &&
		math.Abs(a.Width-b.Width) <= snapConfirmTol && math.Abs(a.Height-b.Height) <= snapConfirmTol
}

// endSnapSession drops the AX reference and the preview; hidePanel also
// folds a panel the session opened. The panel's shell geometry is retired
// by view() once the fold is over.
func (a *shelf) endSnapSession(hidePanel bool) {
	s := &a.snap
	if s.win != nil {
		s.win.Release()
	}
	s.win = nil
	s.armed, s.dragging, s.move, s.resize = false, false, false, false
	s.press = mygo.Point{}
	s.hot = -1
	a.closeGhost()
	if hidePanel {
		a.removeSnapPanel()
	}
}

// openSnapPanel shows the shelf as the 360 × 150 arrangement panel. When a
// panel is already open the snap panel takes it over in place: the level
// returns to the island and the content becomes the six tiles, with the
// shell morphing to its own box. show() would refuse (already visible), so
// this path sets the drawn state and the native frame itself.
func (a *shelf) openSnapPanel() {
	s := &a.snap
	if s.panel {
		return
	}
	s.panel, s.hot = true, -1
	if a.visible {
		s.wasOpen = true
		s.prevLevel = a.currentLevel()
		s.drawn = true
		a.level = 1
		a.jumpLevel = false
		a.syncMousePassThrough()
		if a.win != nil {
			a.win.Invalidate()
		}
		return
	}
	s.wasOpen, s.prevLevel = false, 0
	a.show(false)
}

// removeSnapPanel leaves the arrangement panel. A panel the drag opened
// folds away; one that was already open underneath returns to the level it
// had (its page is still current, because the snap panel never switched
// pages). drawn stays true for a fold so the shell animates from its own
// box, and clears at once when restoring a panel.
func (a *shelf) removeSnapPanel() {
	s := &a.snap
	if !s.panel {
		return
	}
	s.panel = false
	if !a.visible {
		s.wasOpen, s.prevLevel = false, 0
		return
	}
	if s.wasOpen {
		level := s.prevLevel
		s.wasOpen, s.prevLevel = false, 0
		s.drawn = false
		a.level = level
		if a.level < 1 {
			a.level = 1
		}
		// Put the old page back at once; only the shell has to spring
		// from the 360 × 150 box to its own.
		a.jumpLevel = true
		a.syncMousePassThrough()
		if a.win != nil {
			a.win.Invalidate()
		}
		return
	}
	a.hide(hidePassive)
}

// openSnapPermissions is the panel's 开启 button: the existing single
// permission flow, which opens Settings and walks the missing items.
func (a *shelf) openSnapPermissions() {
	a.removeSnapPanel()
	a.showPermissions()
}

// snapAnchor is the current display's resting shell box on screen: the
// measured notch, or the island's floating capsule.
func (a *shelf) snapAnchor() mygo.Rectangle {
	w, h := a.anchorSize()
	return snapAnchorRect(a.stripCenter(), a.y, a.islandTop, w, h)
}

// screenPointOf is the AX frame's centre as the point that picks a display.
func screenPointOf(r mac.AXRect) mygo.Point {
	return mygo.Point{X: int(math.Round(r.X + r.Width/2)), Y: int(math.Round(r.Y + r.Height/2))}
}

// setSnapWindows saves the setting; switching it off ends any session and
// folds a panel the session opened.
func (a *shelf) setSnapWindows(on bool) {
	if a.settings.SnapWindows == on {
		return
	}
	next := a.settings
	next.SnapWindows = on
	if !a.applySettings(next) {
		return
	}
	if !on {
		a.endSnapSession(true)
		a.cancelSnapConfirm()
	}
}

// showGhost points the preview box at target, creating its window on the
// first hover. Moving the window to the next tile is the spring in
// advanceGhost, not a jump.
func (a *shelf) showGhost(target mygo.Rectangle) {
	s := &a.snap
	if target.Width <= 0 || target.Height <= 0 {
		return
	}
	g := s.ghost
	if g == nil {
		g = &snapGhost{}
		s.ghost = g
		g.target = target
		g.frame = target
		g.x, g.y, g.w, g.h = float64(target.X), float64(target.Y), float64(target.Width), float64(target.Height)
		if a.win != nil {
			a.openGhostWindow(g)
		}
		return
	}
	g.target = target
}

// advanceGhost steps the preview's spring and applies the new frame. The
// window never takes the mouse; a headless shelf only keeps the target so
// the hover logic stays testable.
func (a *shelf) advanceGhost(now time.Time) {
	g := a.snap.ghost
	if g == nil {
		return
	}
	if g.at.IsZero() {
		g.at = now
		return
	}
	dt := min(max(now.Sub(g.at).Seconds(), 0), 0.1)
	g.at = now
	if dt > 0 {
		step := func(cur, vel *float64, target float64) {
			acc := snapGhostOmega*snapGhostOmega*(target-*cur) - 2*snapGhostOmega**vel
			*vel += acc * dt
			*cur += *vel * dt
		}
		step(&g.x, &g.vx, float64(g.target.X))
		step(&g.y, &g.vy, float64(g.target.Y))
		step(&g.w, &g.vw, float64(g.target.Width))
		step(&g.h, &g.vh, float64(g.target.Height))
	}
	r := mygo.Rectangle{
		X: int(math.Round(g.x)), Y: int(math.Round(g.y)),
		Width: max(1, int(math.Round(g.w))), Height: max(1, int(math.Round(g.h))),
	}
	if r == g.frame {
		return
	}
	g.frame = r
	if g.win != nil {
		mac.SetWindowFrame(g.win.NativeHandle(), r.X, r.Y, r.Width, r.Height)
		g.win.Invalidate()
	}
}

// openGhostWindow creates the preview's native window at its target.
func (a *shelf) openGhostWindow(g *snapGhost) {
	f := g.frame
	w := mygo.NewWindow(mygo.WindowOptions{
		Title: a.appLoc().WindowSnap(), Width: f.Width, Height: f.Height,
		X: f.X, Y: f.Y,
		Hidden: true, Frameless: true, Transparent: true, DisableShadow: true,
		AlwaysOnTop: true, SkipTaskbar: true, Panel: true,
		DisableMove: true, DisableResize: true, DisableMinimize: true, DisableMaximize: true, DisableFullScreen: true,
		Content: ui.View(snapGhostView),
	})
	g.win = w
	w.SetVisibleOnAllWorkspaces(true)
	mac.ConfigureDragPreview(w.NativeHandle(), a.windowLevel())
	mac.SetWindowFrame(w.NativeHandle(), f.X, f.Y, f.Width, f.Height)
	w.ShowInactive()
}

// closeGhost destroys the preview window. Idempotent.
func (a *shelf) closeGhost() {
	g := a.snap.ghost
	a.snap.ghost = nil
	if g != nil && g.win != nil {
		g.win.Destroy()
	}
}

// snapGhostView paints the preview: accent 16% fill, 2pt accent edge,
// 12pt corners, nothing else.
func snapGhostView(c *ui.Context) {
	c.Root().Background(ui.Transparent)
	ui.Column(c.Key("ghost")).Fill().Radius(snapGhostR).
		Background(accent16).Border(snapGhostEdge, accent)
}

// snapView lays out the 360 × 150 panel: the title at the top left, the
// 3 × 2 tile grid, and — without Accessibility — the permission row over
// the greyed out tiles.
func (a *shelf) snapView(c *ui.Context) {
	trusted := a.snap.trusted
	ui.Column(c.Key("snap")).Width(snapW).Height(snapH).Children(func() {
		panelLabel(c, "snap-title", loc(c).SnapTitle(), 11, 600).
			Absolute().Left(14).Top(snapTitleTop).TextColor(ink3)
		grid := ui.Column(c.Key("snap-grid")).Absolute().Left(snapPadX).Top(snapGridTop).
			Width(3*snapTileW + 2*snapTileGap).Gap(snapTileGap)
		grid.Disabled(!trusted)
		if !trusted {
			grid = grid.Opacity(0.35)
		}
		grid.Children(func() {
			for row := range 2 {
				ui.Row(c.Key(fmt.Sprintf("snap-row-%d", row))).FillWidth().Gap(snapTileGap).Children(func() {
					for col := range 3 {
						a.snapTile(c, row*3+col, trusted)
					}
				})
			}
		})
		if !trusted {
			a.snapPermissionRow(c)
		}
	})
}

// snapTile draws one arrangement: a small screen icon with the target
// region, then its name. The hovered tile takes the accent fill and edge.
func (a *shelf) snapTile(c *ui.Context, slot int, trusted bool) {
	t := snapTargets[slot]
	name := snapTargetName(loc(c), slot)
	hot := trusted && a.snap.hot == slot
	tile := ui.Row(c.Key(fmt.Sprintf("snap-tile-%d", slot))).Width(snapTileW).Height(snapTileH).
		Radius(snapTileR).Padding(0, 10).Gap(8).AlignItems(ui.Center).
		Label(name)
	if hot {
		tile.Background(accent.Alpha(0.28)).Border(1.5, accent)
	} else {
		tile.Background(fill1)
	}
	tile.Children(func() {
		mini := ui.Box(c.Key(fmt.Sprintf("snap-mini-%d", slot))).Width(snapMiniW).Height(snapMiniH)
		mini.Draw(func(p *ui.Painter, r ui.Rect) {
			edge, inner := ui.RGBA(255, 255, 255, 0.45), ui.RGBA(255, 255, 255, 0.55)
			if hot {
				edge, inner = ui.Hex("#FFFFFF"), accent
			}
			p.Stroke(r, edge, 4, 1.4)
			x := r.X + r.W*t.From
			p.Fill(ui.Rect{X: x, Y: r.Y + 1.4, W: r.W * (t.To - t.From), H: r.H - 2.8}, inner, 1)
		})
		n := panelLabel(c, fmt.Sprintf("snap-name-%d", slot), name, 11.5, 600)
		if hot {
			n.TextColor(ink)
		} else {
			n.TextColor(ink2)
		}
	})
}

// snapPermissionRow is the panel's one line without Accessibility: what is
// missing and the button that starts the existing permission flow.
func (a *shelf) snapPermissionRow(c *ui.Context) {
	ui.Row(c.Key("snap-perm")).Absolute().Left(0).Right(0).Top(58).Height(34).
		Justify(ui.Center).Children(func() {
		row := ui.Row(c.Key("snap-perm-pill")).Height(26).Radius(13).
			Background(ui.RGBA(0, 0, 0, 0.9)).Border(0.5, fill2).
			Padding(0, 10).Gap(8).AlignItems(ui.Center)
		row.Children(func() {
			ui.Text(c, loc(c).SnapNeedsAccess()).SingleLine().FontSize(11.5).TextColor(ink2)
			b := ui.ButtonBase(c.Key("snap-perm-open")).Height(20).Radius(10).
				Padding(0, 10).Center().Background(accent).Label(loc(c).SnapEnable())
			if b.Hovered() {
				b.Background(accent.Alpha(0.8))
			}
			b.Children(func() {
				ui.Text(c, loc(c).SnapEnable()).SingleLine().FontSize(11).FontWeight(600).TextColor(ink)
			})
			if b.Clicked() {
				a.openSnapPermissions()
			}
		})
	})
}

// snapPanelBox is the arrangement panel's on-screen box: centred on the
// island's axis when it fits, clamped into the visible frame otherwise,
// and growing upward when the island is too low for it.
func (a *shelf) snapPanelBox() mygo.Rectangle {
	if !a.island {
		return snapPanelRect(a.stripCenter(), a.frameTop()+a.sheetTop())
	}
	p := panelSpot(a.stripCenter(), a.anchorScreenTop(), islandIdleH, a.workArea(), snapW, snapH)
	return mygo.Rectangle{X: p.X, Y: p.Y, Width: p.W, Height: p.H}
}
