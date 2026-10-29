//go:build darwin

package main

import (
	"errors"
	"math"
	"sync/atomic"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"shelf/internal/mac"
)

// The notch shell: geometry, the three states (idle, peek, expanded), their
// animations and mouse pass-through. Content lives in panel.go and items.go.

func (a *shelf) place(p mygo.Point) {
	a.placeAt(p)
	if a.win != nil {
		a.syncWindowFrame()
		a.win.SetOpacity(1)
	}
	a.syncDragGuard()
	a.syncIdleIslands()
}

// displayNearest is the display lookup placeAt measures from. The app always
// asks WindowServer; tests replace it with a synthetic layout so placement,
// the saved island spot and the clamps can be driven without a screen.
var displayNearest = func(p mygo.Point) mygo.Display { return mygo.Screen.DisplayNearestPoint(p) }

// notchRect measures a display's camera housing; tests replace it with the
// synthetic layout displayNearest returns.
var notchRect = mac.NotchRect

func (a *shelf) placeAt(p mygo.Point) {
	d := displayNearest(p)
	a.dispBounds, a.dispWork = d.Bounds, d.WorkArea
	a.dispID = d.ID
	nx, ny, nw, nh, hasNotch := notchRect(d.ID)
	a.notchX, a.notchY, a.notchW, a.notchH, a.hasNotch = nx, ny, nw, nh, hasNotch
	a.topInset = nh
	a.applyShellStyle()
	// The panel, the peek pill and the strip share one axis: the anchor's
	// centre (the notch, or the display centre on a screen without one).
	// A different centre made the expansion jump sideways.
	a.x = a.anchorCenterX() - sheetW/2
	// The user's own place for this display, when the island was dragged:
	// showing, folding and a display change all replay it instead of the
	// default row.
	a.placeIslandAxis()
}

// overAnchor 只认实测几何：光标真正进入锚点热区（刘海，或者灵动岛的
// 空闲胶囊；四周留 hover 余量）才为真。显示器未匹配时 notchW 为 0，交互
// 整体禁用，不会在屏幕左上角凭空出现一个悬停/触发区。
func (a *shelf) overAnchor(p mygo.Point) bool {
	if a.island {
		return a.overIsland(p)
	}
	return a.notchW > 0 && a.notchH > 0 &&
		p.X >= a.notchX-notchHoverPadX && p.X <= a.notchX+a.notchW+notchHoverPadX &&
		p.Y >= a.notchY && p.Y <= a.notchY+a.notchH+notchHoverPadY
}

// Hover changes only the drawing, not the native window geometry. Keeping one
// stable transparent strip for idle/peek avoids resizing the backing surface
// every time the pointer crosses the notch. Only opening the panel changes it.
// The strip is always wide enough for the now-playing peek pill, so a track
// coming or going changes the drawing without a native resize. Every width
// includes the two ear corners (earR): the shell's top edge reaches past its
// flanks by that much on each side, and no frame may clip the arc.
func (a *shelf) desiredFrame() mygo.Rectangle {
	closing := !a.visible && a.fx.openK > 0
	top := a.sheetTop()
	ears := a.earWidth()
	if a.visible || closing {
		if closing {
			// Follow the drawn shell through its fold, with room for the
			// corners and two points below the bottom curve.
			w := int(math.Ceil(float64(a.fx.w))) + 2*ears
			h := int(a.fx.h+0.5) + top + 2
			if a.fb.up(time.Now()) {
				// Folding into the capsule: the shell may grow wider or
				// taller than the panel sliver it starts from. No glow
				// reaches past it anymore, so the room is the shell plus
				// its corners, shake and overshoot only.
				w = max(w, a.capsuleFrameW())
				h = max(h, top+a.capsuleBodyH()+2)
			}
			return mygo.Rectangle{X: a.stripCenter() - w/2, Y: a.y, Width: w, Height: h}
		}
		// The frame follows the level the panel is on, never the larger of
		// the two: an island opening on the sheet's box laid a 451pt
		// transparent frame — the shell is 420 — over the menu-bar icons
		// beside it. A level spring leaves the drawn shell wider or taller
		// than its target for a few frames and a spring on its way
		// overshoots, so the frame holds that shell and the level's
		// overshoot room. The width keeps the room at rest too: shrinking
		// it when the spring settles would shift the window under a shell
		// that has not moved, and the transparent columns beside the shell
		// no longer take clicks anyway (overlayAcceptsMouse).
		tw, th := a.panelBox()
		w, roomH := a.frameRoom(tw, float32(th+float32(top)))
		w = max(w, int(math.Ceil(float64(a.fx.w)))+2*ears)
		// Once settled the height is the shell's exact box, so its
		// transparent margin cannot swallow clicks below.
		h := max(int(math.Ceil(float64(th)))+top, int(math.Ceil(float64(a.fx.h)))+top)
		if a.fx.openMoving {
			h = max(h, roomH)
		}
		return mygo.Rectangle{X: a.stripCenter() - w/2, Y: a.y, Width: w, Height: h}
	}
	// An incoming system drag shrinks the resident strip to the anchor's
	// band: outside it ShelfU only shows a widened band-height hint, inside
	// it the shell grows 「松开放入」. The frame always covers the shape
	// being aimed at (plus corners) and the shell actually drawn, so the
	// morph, a fold remnant and the Spaces edge guard stay covered — and
	// the wings/capsule's taller frames are never held through a drag.
	if a.dragging && !a.ownDrag {
		w := a.bandW()
		h := top + a.menuBand()
		pw, ph := a.peekSize()
		w = max(w, int(math.Ceil(float64(pw)))+2*ears)
		h = max(h, top+int(math.Ceil(float64(ph))))
		if a.fx.peek {
			w = max(w, int(math.Ceil(float64(a.fx.w)))+2*ears)
			h = max(h, top+int(math.Ceil(float64(a.fx.h)))+1)
		}
		if a.inBand {
			// The cursor is inside the band (entered through the menu
			// bar): the frame must hold the guard's landing row even
			// while the shell is still the small hint — a suppressed
			// drag grows the window too, so the clamped cursor never
			// leaves its surface. Before entry the window stays
			// band-high; the guard's push lands on the band's last
			// row, which is already inside this frame.
			_, baseH := a.anchorSize()
			h = max(h, top+dropBandH(int(baseH)))
		}
		return mygo.Rectangle{X: a.stripCenter() - w/2, Y: a.y, Width: w, Height: h}
	}
	// The resident strip holds the largest peek shape for the current
	// state, so hovering only redraws; it resizes when a track comes or
	// goes, while nothing is drawn.
	pw, ph := a.peekSize()
	if a.np != nil && a.np.hasTrack() && a.settings.IslandNowPlaying {
		// The capsule and the wings can alternate on the same strip: keep
		// room for both so one hand-off never resizes the window.
		mw, mh := a.wingsSize()
		pw, ph = max(pw, mw), max(ph, mh)
	}
	if a.fx.peek {
		// The shell can still be the larger shape it is folding back from,
		// e.g. the capsule just expired or a track stopped playing.
		pw, ph = max(pw, a.fx.w), max(ph, a.fx.h)
	}
	if a.fx.sizeMoving {
		// A morph in flight overshoots its target by a couple of points
		// (Bounce 0.15); the strip keeps headroom so no frame is clipped.
		pw, ph = pw*(1+openOvershoot), ph*(1+openOvershoot)
	}
	w := int(math.Ceil(float64(pw))) + 2*ears
	h := top + int(math.Ceil(float64(ph)))
	if a.capsuleUp(time.Now()) || a.fb.drawn {
		// The completion capsule is another peek shape: while it shows, the
		// strip holds room for it at the widest the text can make it, plus
		// its corners, shake and grow-out overshoot. The shape grows out in
		// ~160ms, well inside the tick that resizes the strip, so no frame
		// is cut.
		w = max(w, a.capsuleFrameW())
		h = max(h, top+a.capsuleBodyH()+2)
	}
	return mygo.Rectangle{X: a.stripCenter() - w/2, Y: a.y, Width: w, Height: h}
}

// Use a single native setFrame plus a top-left pin so AppKit cannot leave
// the overlay a menu-bar-height below the hardware notch.
// materialCorner is the shell's bottom radius for the backdrop material:
// the same one the shell path draws — the overview's 22 through a page's
// 24 by the level progress, capped at half the height when the shell is the
// peek capsule.
func materialCorner(shellH, levelK float32) float64 {
	corner := float64(overviewRadius) + (float64(sheetRadius)-float64(overviewRadius))*float64(levelK)
	return math.Min(float64(shellH)/2, corner)
}

// materialKey is the box and visibility last applied to the backdrop
// material: an unchanged key skips the native calls while nothing moves.
type materialKey struct {
	win           uintptr
	x, y, w, h, r float64
	on            bool
}

// syncMaterial places the light appearance's backdrop material on the
// shell the window now holds: the frame was just applied, so the view
// lands exactly under the drawn shell in the same frame.
// onSyncMaterial runs at the top of every syncMaterial call; tests count
// the calls without a window server. onMaterialFrame reports each applied
// box with the shell and window it was computed for: evidence for the
// frames where the shell moves and the window does not.
var (
	onSyncMaterial  func()
	onMaterialFrame func(k materialKey, shellW, shellH float32, winW, winH int)
)

func (a *shelf) syncMaterial() {
	if onSyncMaterial != nil {
		onSyncMaterial()
	}
	if a.winH == 0 {
		return
	}
	// A window that was destroyed and rebuilt (a test's second surface)
	// left the material view dangling: it belongs to the old content view.
	if a.material != nil && a.materialWin != a.winH {
		a.material = nil
	}
	if !a.glassNow() {
		if a.material != nil {
			a.material.SetVisible(false)
		}
		a.materialAt = materialKey{}
		return
	}
	if a.material == nil {
		// The window's own vibrancy call installs the effect view (mygo
		// knows how); the material then gets the shell's frame below.
		a.win.SetVibrancy(mygo.VibrancyUnderWindow)
		a.material = mac.NewShellMaterial(a.winH)
		a.materialWin = a.winH
		if a.material == nil {
			// No material view: fall back to solid black rather than
			// pretending a translucent fill is a blur.
			a.win.SetVibrancy(mygo.VibrancyNone)
			a.reduceTrans = true
			return
		}
		a.material.SetVisible(false)
	}
	// The frame the window is about to have: view() calls this before the
	// native resize lands, so the material is placed for the same box.
	r := a.desiredFrame()
	fw, fh := float64(r.Width), float64(r.Height)
	sw, sh := float64(a.fx.w), float64(a.fx.h)
	if sw <= 0 || sh <= 0 {
		a.material.SetVisible(false)
		return
	}
	top := float64(a.sheetTop())
	radius := materialCorner(a.fx.h, a.fx.levelK)
	key := materialKey{win: a.winH, x: (fw - sw) / 2, y: fh - top - sh, w: sw, h: sh, r: radius, on: true}
	if key == a.materialAt {
		return
	}
	a.materialAt = key
	a.material.SetFrame(key.x, key.y, key.w, key.h, key.r)
	a.material.SetVisible(true)
	if onMaterialFrame != nil {
		onMaterialFrame(key, a.fx.w, a.fx.h, r.Width, r.Height)
	}
}

func (a *shelf) syncWindowFrame() {
	if a.win == nil || a.settingFrame {
		return
	}
	r := a.desiredFrame()
	if r == a.frame {
		return
	}
	if a.buildingView && a.winH != 0 {
		// MyGo captured the surface size before view() ran. Resizing here
		// would present this frame's old canvas in the new native frame.
		// Update posts to the next main-loop pass; resolve the latest target
		// there, including any close or reopen issued in the meantime.
		if !a.frameQueued {
			a.frameQueued = true
			a.win.Update(func() {
				a.frameQueued = false
				if a.win != nil && !a.win.IsDestroyed() {
					a.syncWindowFrame()
				}
			})
		}
		return
	}
	a.settingFrame = true
	defer func() { a.settingFrame = false }()
	mac.SetWindowFrame(a.winH, r.X, r.Y, r.Width, r.Height)
	a.frame = r
	a.syncMaterial()
	// The shell moved with the window, so the pointer may now be on it (a
	// display change recentres the overlay under a still pointer) or off
	// it. Re-ask before the frame that will draw the new position.
	a.syncPointerGate()
	a.win.Invalidate()
}

// Native window movement can invalidate our cached frame independently of a
// state transition. Never let the cached target hide a displaced window.
func (a *shelf) windowGeometryChanged() {
	if a.settingFrame || a.win == nil {
		return
	}
	if actual := a.win.Bounds(); actual == a.desiredFrame() {
		a.frame = actual
		return
	}
	a.frame = mygo.Rectangle{}
	a.syncWindowFrame()
}

// peekSize is the hover shape: the status capsule while the closed panel
// has something to report, the now-playing wings while a track is loaded
// (the notch grows 52pt to each side, barely below), the drop pill while a
// drag is over the notch band, the narrow drag hint while one is in
// flight, otherwise a bulge carrying a chevron. The glyphs sit wholly
// below the hardware notch: nothing drawn inside it is visible.
func (a *shelf) peekSize() (w, h float32) {
	baseW, baseH := a.anchorSize()
	if a.pinReturning {
		// A pinned card held over the notch wins over the capsule and
		// the wings: the release reads this same zone, so the affordance
		// must always be the drop-pill it acts on (design §4 松开放回).
		return baseW + dropPillExtraW, baseH + dropPillBody
	}
	now := time.Now()
	if a.capsuleUp(now) {
		// The completion capsule takes the peek shape while the panel is
		// closed: it is what the shell grows into, whether or not a track
		// is loaded.
		return a.capsuleSize()
	}
	if a.dragging && !a.ownDrag {
		if a.dropHot {
			return baseW + dropPillExtraW, baseH + dropPillBody
		}
		// Drag hint: the anchor's own height, a little wider — visible in
		// the menu bar, never over the windows below it.
		return baseW + dropHintExtraW, baseH
	}
	// Charging, headphones and a finished download take the notch while
	// their words are up; a running download holds the wings below them.
	if ev, ok := a.liveNow(now, a.liveAllow()); ok {
		return a.liveShape(ev)
	}
	if a.downloadRunning() {
		return a.downloadShape()
	}
	if a.np != nil && a.np.showsPlayer() {
		return a.wingsSize()
	}
	return baseW + 40, baseH + peekBody
}

// liveWingW is how far each side grows for charging and downloads: the
// notch's camera housing forces the right-hand content into one visible
// wing (112), while the island's continuous pill uses the preview's 88.
func (a *shelf) liveWingW() int {
	if a.island {
		return islandLiveWingW
	}
	return liveWingW
}

// wingsSize is the hover wings' size: the anchor plus npWingsW on each
// side, and npWingsBody below it. Wide notches keep their width and gain
// the same wings; the island's 120pt capsule grows to 224 × 34.
func (a *shelf) wingsSize() (w, h float32) {
	baseW, baseH := a.anchorSize()
	return baseW + 2*npWingsW, baseH + npWingsBody
}

// shellSize is the one interpolation the shell Path and the content layout
// share: hardware notch → peek shape peekW×peekH (peekK) → overview →
// page (levelK, then openK). The width is 440 at both levels, so a page
// only ever grows taller. The caller passes the drawn values: view()
// animates them per display frame and substitutes final ones in headless
// tests.
func (a *shelf) shellSize(openK, levelK, peekK, peekW, peekH float32) (w, h float32) {
	baseW, baseH := a.anchorSize()
	lvlW := float32(overviewW) + (sheetW-overviewW)*levelK
	lvlH := float32(overviewH) + (sheetH-overviewH)*levelK
	if a.snap.panel || a.snap.drawn {
		// The arrangement panel is its own box; it never morphs through
		// the sheet's size.
		lvlW, lvlH = snapW, snapH
	}
	w0 := baseW + (peekW-baseW)*peekK
	h0 := baseH + (peekH-baseH)*peekK
	return w0 + (lvlW-w0)*openK, h0 + (lvlH-h0)*openK
}

// capsuleFrameW is the window width the completion capsule needs once the
// glow margin is gone: the widest capsule, plus both ear corners, the
// failure shake's reach, and the grow-out spring's overshoot (Bounce 0.28
// widens the notch→capsule delta by about openOvershoot).
func capsuleFrameW() int {
	wide := int(math.Ceil(float64((fbMaxW - 180) * openOvershoot)))
	return fbMaxW + 2*(earR+fbShakeX+wide)
}

// peekPillRect is the on-screen box of the hovered notch capsule, matching
// the shell drawn in view(): the now-playing pill while a track is
// loaded, otherwise the chevron bulge. A press anywhere on it expands the
// shelf — the design has no in-pill controls. While the completion capsule
// or a fold in flight is on the shell the box is the shell actually drawn
// (a fold remnant still takes its own clicks), shifted by the error shake
// so the moving capsule keeps its own hit box.
func (a *shelf) peekPillRect() mygo.Rectangle {
	w, h := a.peekSize()
	if (a.peeking || a.fb.drawn || a.fb.retracting || a.peekProgress > 0.01) && a.fx.w > 0 {
		w, h = a.fx.w, a.fx.h
	}
	x := a.stripCenter() - int(w/2) + int(a.fx.shakeX)
	return mygo.Rectangle{X: x, Y: a.y + a.sheetTop(), Width: int(w + 0.5), Height: int(h + 0.5)}
}

func (a *shelf) overPeekPill(p mygo.Point) bool {
	r := a.peekPillRect()
	return r.Width > 0 && r.Height > 0 &&
		p.X >= r.X && p.X < r.X+r.Width &&
		p.Y >= r.Y && p.Y < r.Y+r.Height
}

// mouseDown is the AppKit mouse-down observer. inside means the event was
// delivered to our window; otherwise it went to another app or the menu bar.
// Every left press also reaches the drag tracker: the 30 ms board poll can
// miss a release between two drags, and the press is the edge that splits
// their serials (see dragTracker.press).
func (a *shelf) mouseDown(inside bool, button int, p mygo.Point) {
	if button == 0 {
		mac.NoteLeftPress()
	}
	if a.quitting {
		return
	}
	// A press on another screen's idle capsule moves the resident window
	// there first — every screen's island opens its own panel. It arms the
	// drag the same way, so an idle capsule anywhere can be moved.
	if !inside && button == 0 && !a.visible && a.idleIslandAt(p) {
		a.place(p)
		a.beginIslandPress(p)
		return
	}
	// The capsule is drawn over the menu bar's empty centre. WindowServer
	// often gives that press to the menu bar or the app underneath, so the
	// overlay paints a "click to expand" pill that never receives the click.
	// A press on the visible pill expands either way — except a press the
	// hover wings received itself: they have no controls, and their own
	// handler opens the shelf for a press anywhere on them. Only a press
	// inside the drawn shape acts: transparent window margin neither opens
	// the panel nor claims the event for it.
	// The idle island is a click target of its own: its window takes the
	// press only while the pointer is on the capsule, and a press that
	// slipped through to the window underneath still reaches the island —
	// the capsule is always visible, unlike the notch's hidden band. The
	// press only arms the move; the release either drops the panel open (a
	// tap) or ends the drag where it stopped. A finished download's capsule
	// is a button of its own; its view must receive the press, so it is
	// never swallowed here.
	if a.island && !a.visible && !a.liveClickable(time.Now()) && a.overShell(p) {
		a.beginIslandPress(p)
		return
	}
	if (a.peeking || a.fb.drawn || a.fb.retracting || a.peekProgress > 0.01) && !a.visible && a.overPeekPill(p) {
		// The completion capsule is a button of its own: its view adds the
		// file and opens the tray page. The shell must not swallow that
		// press by expanding first.
		if inside && a.liveClickable(time.Now()) {
			return
		}
		if inside && a.peeking && a.np != nil && a.np.showsPlayer() {
			return
		}
		a.show(false)
		return
	}
	if inside {
		// Esc and Space are taken only while the panel is open; a press
		// on the collapsed notch band or a closing panel must not hold them.
		if a.visible {
			a.holdKeys()
		}
		a.takeServiceOwnership()
		return
	}
	a.releaseKeys()
	// A left press delivered to another app may be the start of a window
	// drag; the arrangement panel follows it from here (snap.go).
	a.snapPress(false, button, p)
	menuBottom := a.notchY + max(36, a.notchH+4)
	if a.island {
		menuBottom = max(menuBottom, a.y+a.islandTop+islandIdleH)
	}
	inMenuBar := p.Y >= a.notchY && p.Y <= menuBottom
	if a.visible && !a.dragging && !inMenuBar {
		a.hide(hidePassive)
	}
}

// windowLevel is the overlay's one window-level policy, applied by
// syncMousePassThrough on every state change: a file drag sits above the
// menu bar so its strip cannot steal the drop; keyboard focus (clipboard
// search) must drop below kCGDockWindowLevel or the input method's
// candidate panel — drawn at 20 and anchored inside the opaque panel —
// stays completely hidden behind it (marked text still composes inline,
// which is why the bug reads as "IME types but no candidates").
func (a *shelf) windowLevel() int {
	switch {
	case a.dragging || a.pointerDrag:
		return mac.PopUpMenuWindowLevel
	case a.focusMode:
		return mac.SearchKeyboardLevel
	default:
		return mac.StatusWindowLevel
	}
}

// While idle the overlay ignores the mouse altogether, so clicks under and
// beside the notch reach whatever is below. An in-flight drag takes the
// whole window: file drags to the top of the notch must hit a window, or
// WindowServer treats Y=0 as the Spaces edge (pointerDrag flips this on
// the drag's first movement, long before the top). Every other state takes
// the mouse only over the shell the last frame drew: the window is a
// rectangle (the shell plus its transparent ear margins) sitting above the
// menu bar, and WindowServer routes a press to a window before any view is
// hit-tested, so a window-wide press would eat the menu-bar icons beside
// the shell. A closing panel follows the drawn shell (fx.openK), not the
// tick's eased progress: the moment the shell has folded back to the peek
// shape the window lets clicks through again.
func (a *shelf) syncMousePassThrough() {
	a.syncWindowFrame()
	setGatePolling(anyOverlayWanted())
	accept := a.overlayAcceptsMouse()
	a.ignoringMouse = !accept
	if a.win == nil {
		return
	}
	h := a.winH
	mac.SetOverlayAcceptsMouse(h, accept)
	mac.SetWindowLevel(h, a.windowLevel())
}

// overlayWanted reports whether the current state needs the overlay to take
// the mouse at all: hovering, opening, open, folding, or a capture capsule.
func (a *shelf) overlayWanted() bool {
	return a.visible || a.fx.openK > 0.02 || a.dragging || a.pointerDrag || a.islandDrag ||
		a.peeking || a.peekProgress > 0.01 || a.fb.drawn || a.fb.retracting ||
		a.liveUp()
}

// overlayAcceptsMouse answers whether the overlay takes the mouse at the
// current pointer, reading the pointer only when the answer depends on it:
// a headless surface has no window to gate, a drag owns the whole frame
// (the drop and the Spaces edge guard land on the window, not on the drawn
// shape), and an idle overlay ignores the mouse everywhere.
func (a *shelf) overlayAcceptsMouse() bool {
	wanted := a.overlayWanted()
	if a.winH == 0 {
		return wanted
	}
	if a.dragging || a.pointerDrag || a.islandDrag {
		return wanted
	}
	p := cursorPoint()
	if !wanted {
		// A notched shelf at rest draws nothing: every press belongs to
		// whatever is under the strip. The island's resting capsule is a
		// real, visible target and takes presses on its own shape.
		return a.island && a.overShell(p)
	}
	if a.overShell(p) {
		return true
	}
	// The drawn shell is not the only shape a press can belong to. An
	// opening island, the sheet springing out of the island and a capsule
	// growing out of the notch reach their box a frame or two later, and
	// the frame already covers it (desiredFrame follows the level), while a
	// still pointer produces no move event to re-ask with. So the shape the
	// state settles at counts too: a press that lands where the shell is
	// about to be must reach ShelfU, not the window underneath.
	tw, th, tl := a.settledShell()
	return a.overShellShape(p, tw, th, tl, a.fx.shakeX)
}

// settledShell is the shell the current state is heading for: the level's
// box while the panel is open, the peek shape (wings, drop pill, completion
// capsule, chevron) while it is closed. levelK is the drawn corner's level
// progress at rest, so the hit shape rounds exactly like the settled frame.
func (a *shelf) settledShell() (w, h, levelK float32) {
	var openK, peekK float32
	if a.visible {
		openK = 1
	}
	if a.level >= 2 {
		levelK = 1
	}
	if a.peeking || a.peekProgress > 0.01 || a.fb.drawn || a.fb.retracting {
		peekK = 1
	}
	pw, ph := a.peekSize()
	w, h = a.shellSize(openK, levelK, peekK, pw, ph)
	return w, h, levelK
}

// The pointer gate's sources are installed once per process and fan out to
// every overlay that follows the pointer: ShelfU owns one, and the smoke
// harness builds one per test. A watch bound to the first shelf left every
// other window's flag stale — its gate could only change on the next frame,
// never on a move.
var (
	pointerWatched bool
	pointerShelves []*shelf
)

// gatePollInterval is how often the gate re-reads the pointer while a shell
// is on screen. Move events alone are not enough: macOS coalesces
// mouse-moved events and an inactive overlay's event may be delivered tens
// of milliseconds late, so a press can arrive after the pointer reached the
// shell but before the monitor ran — WindowServer would hand that press to
// the window underneath. Polling bounds that window to one interval.
const gatePollInterval = 10 * time.Millisecond

// gatePolling mirrors "some overlay is on screen", set on the main thread so
// the poller never reads a shelf off it (the fields are main-thread only).
var gatePolling atomic.Bool

// gateWake carries "polling started" to the poller, which sleeps otherwise.
var gateWake = make(chan struct{}, 1)

// watchPointer registers the shelf with the pointer gate's sources. A
// settled panel draws no frames, so nothing else would notice the pointer
// leaving the drawn shell — and WindowServer decides which window a press
// reaches from ignoresMouseEvents, which only the pointer position keeps
// current. Both sources are mouse-only, so neither needs Accessibility.
// They live as long as the process.
func (a *shelf) watchPointer() {
	if a.watchingPointer {
		return
	}
	a.watchingPointer = true
	pointerShelves = append(pointerShelves, a)
	if pointerWatched {
		return
	}
	pointerWatched = true
	mac.ObserveMouseMoved(func() {
		for _, s := range pointerShelves {
			s.syncPointerGate()
		}
	})
	go pollPointerGate()
}

// pollPointerGate samples the pointer while a shell is on screen, so the
// flag is current without waiting for an event. The sample itself runs on
// the main thread: every field the gate reads belongs to it.
func pollPointerGate() {
	for range gateWake {
		for gatePolling.Load() {
			time.Sleep(gatePollInterval)
			mygo.RunOnMain(func() {
				for _, s := range pointerShelves {
					s.syncPointerGate()
				}
			})
		}
	}
}

// anyOverlayWanted reports whether any registered overlay is on screen, so
// one idle shelf cannot stop the poller another shelf relies on. Main
// thread only.
func anyOverlayWanted() bool {
	for _, s := range pointerShelves {
		if s.overlayWanted() {
			return true
		}
	}
	return false
}

// setGatePolling tells the poller that a shell is (or is no longer) on
// screen. On the main thread only.
func setGatePolling(on bool) {
	if gatePolling.Swap(on) == on {
		return
	}
	if on {
		select {
		case gateWake <- struct{}{}:
		default:
		}
	}
}

// syncPointerGate re-asks the shell-shaped hit test at the current pointer
// and applies the answer, touching the native flag only when it changed. It
// is both the pointer watch's entry and the refresh every change of the
// shell's shape or position goes through (window frame, show, level, page,
// fold, capsule, drag end), so no press waits for a move event.
func (a *shelf) syncPointerGate() {
	if a.win != nil && a.win.IsDestroyed() {
		return
	}
	accept := a.overlayAcceptsMouse()
	if accept == !a.ignoringMouse {
		return
	}
	a.ignoringMouse = !accept
	if a.win == nil {
		return // headless surface: the flag is all there is
	}
	mac.SetOverlayAcceptsMouse(a.winH, accept)
}

// overShell reports whether p is on the shell the last frame drew — the
// black body plus the two ear corners its top edge reaches past its
// flanks, and not the corners its bottom rounds off. Screen coordinates,
// top-left origin. The shell is centred on stripCenter and hangs from
// a.y + sheetTop() in the window, exactly as view() draws it.
func (a *shelf) overShell(p mygo.Point) bool {
	return a.overShellShape(p, a.fx.w, a.fx.h, a.fx.levelK, a.fx.shakeX)
}

// overShellShape is overShell for an explicit shell: w×h, its bottom
// corner rounded by the island/sheet radius at levelK, shifted by shakeX.
func (a *shelf) overShellShape(p mygo.Point, w, h, levelK, shakeX float32) bool {
	if w <= 0 || h <= 0 {
		return false
	}
	top := float64(a.y + a.sheetTop())
	y := float64(p.Y) - top
	if y < 0 || y >= float64(h) {
		return false
	}
	cx := float64(a.stripCenter()) + float64(shakeX)
	x0, x1 := cx-float64(w)/2, cx+float64(w)/2
	corner := float64(overviewRadius) + float64(sheetRadius-overviewRadius)*float64(levelK)
	if a.island {
		// Four round corners, no top ears: the island's path is a rounded
		// rectangle (islandPath's circular arcs) whose radius shrinks to
		// half the height for a capsule. The hit edge follows the circle.
		r := math.Min(float64(h)/2, corner)
		inset := func(d float64) float64 {
			if r <= 0 || d >= r {
				return 0
			}
			return r - math.Sqrt(sq(r)-sq(r-d))
		}
		cut := math.Max(inset(y), inset(float64(h)-y))
		x := float64(p.X)
		return x >= x0+cut && x < x1-cut
	}
	left, right := x0, x1
	// inset is how far the path's corner cuts in d points from the edge it
	// starts on, for radius r: 0 at the flank, r at the outer edge. Both
	// top ears and bottom corners are quadratics with their control point
	// in the corner, which reduce to exactly this.
	inset := func(d, r float64) float64 {
		if r <= 0 || d >= r {
			return 0
		}
		return r * sq(1-math.Sqrt(d/r))
	}
	if ear := float64(earR); y < ear {
		// The ear: the top edge runs earR past the flank at y == 0 and
		// meets the flank at earR below.
		left, right = x0-inset(y, ear), x1+inset(y, ear)
	}
	if botR := math.Min(float64(h)/2, corner); y > float64(h)-botR {
		cut := inset(float64(h)-y, botR)
		left, right = x0+cut, x1-cut
	}
	x := float64(p.X)
	return x >= left && x < right
}

// sq is the square of v.
func sq(v float64) float64 { return v * v }

// setPeek animates the notch shell between its idle hardware shape and the
// peeked "I'm here" pill, and updates event pass-through to match.
func (a *shelf) setPeek(peek bool) {
	if a.peeking == peek {
		return
	}
	a.peeking = peek
	a.peekFrom, a.peekAnimStart = a.peekProgress, time.Now()
	a.syncMousePassThrough()
	if a.win != nil {
		a.win.Invalidate()
	}
}

// panelH is the open panel's fixed height: the arrangement panel while a
// window-drag session owns the shell, otherwise the sheet's 250. Both come
// from layout constants, never from page content.
func (a *shelf) panelH() int {
	if a.snap.panel || a.snap.drawn {
		return snapH
	}
	return sheetH
}

// panelBox is the open panel's target size. The arrangement panel replaces
// the island box; it is reached by the open spring out of the notch, never
// by a level change.
func (a *shelf) panelBox() (w, h float32) {
	if a.snap.panel || a.snap.drawn {
		return snapW, snapH
	}
	if a.currentLevel() == 2 {
		return sheetW, sheetH
	}
	return overviewW, overviewH
}

func (a *shelf) show(edge bool) {
	if a.visible {
		return
	}
	// 跟随系统时，系统语言可能已经变了（用户在系统设置里改过）：每次打开
	// 面板都按同一条规则重新解析；显式选择的语言不受影响。
	a.followSystemLanguage()
	// Transient notices never pop out after the panel closes.
	a.clearLive()
	if a.win != nil {
		a.placeAt(mygo.Screen.CursorScreenPoint())
	}
	if a.store != nil {
		a.store.Refresh()
	}
	// Opening after a close in flight keeps the old view until the fade is
	// over; cancel that pending reset so the fresh opening cannot be
	// clobbered a frame later.
	a.closeAt = time.Time{}
	// A drag or the clipboard hotkey opens straight on the page it needs; a
	// plain click on the notch opens the overview.
	level := 1
	a.tabAt = time.Time{}
	switch {
	case a.focusMode || a.ocrSheet:
		// The caller already chose the page (the clipboard search, the
		// recording card); it keeps it.
		level = 2
	case edge || (a.dragging && !a.ownDrag) || a.drop.on:
		level, a.tabFrom, a.tab = 2, pageTray, pageTray
	default:
		a.tabFrom, a.tab = pageOverview, pageOverview
	}
	a.level = level
	a.openedAt = time.Now()
	a.lastAct = time.Now()
	a.jumpLevel = true
	a.snap.drawn = a.snap.panel
	a.visible, a.edgeOpened, a.received = true, edge, false
	a.dropZoneSnap(edge)
	a.fx.openMoving = true
	a.animationFrom, a.animationStart = a.progress, time.Now()
	prevFrame := a.frame
	a.syncMousePassThrough()
	a.syncDragGuard()
	if a.win != nil {
		a.win.SetOpacity(1)
		a.win.ShowInactive()
		// ShowInactive 不夺焦，面板收不到按键，只能临时注册全局 Esc 与空格。
		a.holdKeys()
		if a.frame == prevFrame {
			a.win.Invalidate()
		}
	}
}

// Global hotkey seams: holdKeys/releaseKeys register and unregister the
// panel's Esc and Space, which needs the app running; tests stub them to
// check who owns the keys.
var (
	registerKey   = mygo.GlobalShortcut.Register
	unregisterKey = mygo.GlobalShortcut.Unregister
)

// holdKeys takes Esc (close Quick Look, then collapse) and Space (Quick
// Look the selection) while the shelf is in use; releaseKeys returns them
// as soon as the user clicks in another app.
func (a *shelf) holdKeys() {
	if !a.keysAreOurs() {
		// 设置窗口在前台时它自己处理 Esc 与空格（Esc/⌘W 关窗、控件收
		// 空格），全局注册会把按键从它手里抢走；剪贴板搜索模式的窗口同
		// 理。两条路径都先确保已注册的按键已经归还。
		a.releaseKeys()
		return
	}
	if !a.escHeld {
		if err := registerKey("Escape", a.escape); err != nil {
			a.report(errors.New(a.appLoc().StatusEscBusy(err.Error())))
		} else {
			a.escHeld = true
		}
	}
	if !a.spaceHeld {
		// Without Space, Quick Look stays available from the context menu.
		a.spaceHeld = registerKey("Space", a.toggleQuickLook) == nil
	}
	a.syncCopyKey()
}

// ShowInactive does not give the overlay keyboard focus. Hold ⌘C like
// Space only while a selected tray picture can actually be copied.
const copyKey = "Cmd+C"

// wantsCopyKey reports whether the tray would act on ⌘C right now: the
// panel is open with its keys, on the tray page, and exactly one selected
// item is a picture. Pure; unit-tested.
func (a *shelf) wantsCopyKey() bool {
	if !a.visible || !a.keysAreOurs() || a.tab != pageTray || a.selection.count() != 1 || a.store == nil {
		return false
	}
	for _, item := range a.store.Items {
		if a.selection.has(item.ID) {
			return !item.OffTray && !a.isLeaving(item) && convertible(item)
		}
	}
	return false
}

// syncCopyKey follows selection changes without reclaiming keys released
// by an outside click. Headless views have no native key to acquire.
func (a *shelf) syncCopyKey() {
	if a.win == nil {
		return // headless test shelf: no app to register with
	}
	switch want := a.wantsCopyKey() && (a.escHeld || a.spaceHeld); {
	case want && !a.cmdCHeld:
		if registerKey(copyKey, a.copyTrayImage) == nil {
			a.cmdCHeld = true
		}
	case !want && a.cmdCHeld:
		unregisterKey(copyKey)
		a.cmdCHeld = false
	}
}

// keysAreOurs reports whether the shelf may hold the global Esc and Space:
// never while the Settings or 重新命名 window is key, and never while the
// panel itself has the keyboard for clipboard search. Pure; unit-tested.
func (a *shelf) keysAreOurs() bool {
	if a.settingsWin != nil {
		return false
	}
	if a.rn != nil && a.rn.win != nil {
		return false
	}
	return !a.focusMode
}

func (a *shelf) releaseKeys() {
	if a.escHeld {
		unregisterKey("Escape")
		a.escHeld = false
	}
	if a.spaceHeld {
		unregisterKey("Space")
		a.spaceHeld = false
	}
	if a.cmdCHeld {
		unregisterKey(copyKey)
		a.cmdCHeld = false
	}
}

// escape closes Quick Look first, like Finder, then dismisses the
// screen-recording card or clears the clipboard
// search while it holds text; a sheet backs out to the island and the
// island folds away.
func (a *shelf) escape() {
	if mac.QuickLookVisible() {
		mac.CloseQuickLook()
		return
	}
	if a.ocrSheet {
		a.hide(hideExplicit)
		return
	}
	if a.tab == pageClipboard && a.search != "" {
		a.search = ""
		if a.win != nil {
			a.win.Invalidate()
		}
		return
	}
	if a.tab == pageTools {
		if p := a.toolsPage(); p != nil && (p.editing || p.picking) {
			// Esc in the 「+」 catalogue only backs out to the edit grid —
			// same as its 完成; outside it, Esc ends editing. Neither
			// hides the panel.
			if p.picking {
				p.picking = false
			} else {
				p.closeDragPreview()
				p.editing, p.press = false, nil
			}
			if a.win != nil {
				a.win.Invalidate()
			}
			return
		}
	}
	// Any page backs out to the overview first, the clipboard search's
	// keyboard mode included: leaving it hands the search back to the
	// normal panel and the overview to the screen; the next Esc closes.
	if a.currentLevel() == 2 {
		if a.focusMode {
			a.leaveFocusMode()
		}
		a.setLevel(1)
		return
	}
	a.hide(hideExplicit)
}

// leaveFocusMode gives up the keyboard the clipboard search took: the
// window drops back to the menu-bar level, the field stops being focused
// and the panel holds Esc/Space again like any other open page. The
// keyboard goes back to the app the user came from at the same moment —
// the panel stays open, so a later close has nothing left to hand back and
// must not lose the remembered app on the way out.
func (a *shelf) leaveFocusMode() {
	if !a.focusMode {
		return
	}
	a.focusMode = false
	prev := a.prevAppPID
	a.prevAppPID = 0
	a.syncWindowFrame()
	if a.win != nil {
		// Headless surfaces (tests) have no app to register keys with.
		a.holdKeys()
		a.win.Invalidate()
	}
	// The panel stays open, so the hand-back must not hide this app: the
	// close path's fallback (resignSelf) would take the notch window with it
	// when the remembered app is gone. Only the close may resign.
	a.handBackApp(prev)
}

// hide collapses the shelf. exit decides whether the remembered app gets
// the keyboard back (see shouldRestoreApp); the remembered pid is always
// cleared so a later explicit hide cannot reactivate it a second time.
// The view state (page, search, keyboard mode) survives until the 120 ms
// content fade ends, so the closing panel fades the page it was showing
// instead of flashing the default one; finishClose retires it.
func (a *shelf) hide(exit hideExit) {
	if !a.visible && !a.peeking {
		return
	}
	a.handoffStatus(time.Now())
	// Any hide — manual or the flash's own timer — ends service ownership
	// so a queued timer can never reclaim a panel the user reopened.
	a.takeServiceOwnership()
	restore := a.shouldRestoreApp(exit)
	prev := a.prevAppPID
	a.visible, a.edgeOpened, a.peeking = false, false, false
	a.overviewDue = time.Time{} // a closed panel keeps no deadline
	// Any hide ends the arrangement panel's claim on the shell; its box
	// stays drawn (snap.drawn) until the fold completes.
	a.snap.panel = false
	a.snap.wasOpen, a.snap.prevLevel = false, 0
	a.closeAt = time.Now()
	// 收起也复位层级进度：二级收起后 fx.levelK 若停在 1，下一次打开一级
	// 的第一帧仍会按二级壳开窗（451pt），在菜单栏图标上方压一层透明窗。
	// 绘制通道由 jumpLevel 在下一帧直接落位，这里同步帧几何读到的值。
	a.fx.levelK = 0
	a.jumpLevel = true
	if a.usage != nil { // 隐藏即取消 AI 用量在途取数和扫描
		a.usage.stopWork()
	}
	a.animationFrom, a.animationStart = a.progress, time.Now()
	a.peekFrom, a.peekAnimStart = a.peekProgress, time.Now()
	a.prevAppPID = 0
	a.releaseKeys()
	// 当前拖拽不再次触发；松开鼠标或出现新会话后解除。
	a.suppressed = true
	a.dropHot = false
	a.syncMousePassThrough()
	if restore {
		a.activatePreviousApp(prev)
	}
	if a.win != nil {
		a.win.Invalidate()
	}
}

// finishClose retires the view state after the content fade: the next
// opening starts on the island's default face with no search or edit.
func (a *shelf) finishClose() {
	a.closeAt = time.Time{}
	a.focusMode, a.tab, a.tabFrom, a.search, a.reveal = false, pageTray, pageTray, "", ""
	a.tabAt = time.Time{}
	a.tabOutK, a.tabOutShift = 1, 0
	a.warming = ""
	a.level = 1
	a.tab = pageOverview
	a.overviewDue = time.Time{} // the retired view keeps no deadline
	// 收起即放弃权限说明卡：面板再开时它要么还在等授权（重新触发再出），
	// 要么授权已完成（预检通过直接框选），都不该回到一张旧卡。
	a.ocrSheet = false
	a.resetToolsEdit() // 工具页的编辑与目录不跨收起保留
	if p := a.playingPageOf(); p != nil {
		p.volLoaded = false // the output device may have changed while closed
	}
	a.selection.clear()
}

// animate updates the shell drawing; idle/peek retain one native surface.
func (a *shelf) animate(now time.Time) {
	finished := false
	if !a.animationStart.IsZero() {
		k := min(float32(now.Sub(a.animationStart))/float32(panelAnimation), 1)
		target := float32(0)
		if a.visible {
			target = 1
		}
		a.progress = a.animationFrom + (target-a.animationFrom)*ui.EaseOut(k)
		if k >= 1 {
			a.animationStart = time.Time{}
			finished = true
		}
	}
	if !a.peekAnimStart.IsZero() {
		k := min(float32(now.Sub(a.peekAnimStart))/float32(peekAnimation), 1)
		target := float32(0)
		if a.peeking || a.visible {
			target = 1
		}
		a.peekProgress = a.peekFrom + (target-a.peekFrom)*ui.EaseOut(k)
		if k >= 1 {
			a.peekAnimStart = time.Time{}
			finished = true
		}
	}
	if a.win == nil {
		return
	}
	// 动画途中由 zone.AnimateTo 以显示器帧率自驱动重绘；结束时补一次穿透状态同步。
	if !a.animationStart.IsZero() || !a.peekAnimStart.IsZero() || finished {
		if finished {
			a.syncMousePassThrough()
		}
		if !a.fx.openMoving && !a.fx.sizeMoving {
			a.win.Invalidate()
		}
	}
}

// anchorCenterX is the shell's default horizontal axis on the current
// display: the hardware notch's centre, or the display's centre without one.
// a.x is the fallback for a shelf with no measured geometry (a headless
// test shelf that only knows its own window).
func (a *shelf) anchorCenterX() int {
	switch {
	case a.notchW > 0:
		return a.notchX + a.notchW/2
	case a.dispBounds.Width > 0:
		return a.dispBounds.X + a.dispBounds.Width/2
	}
	return a.x + sheetW/2
}

// stripCenter is shared by the drop surface and its protected approach
// region: the default axis, moved by wherever the user dragged the island.
func (a *shelf) stripCenter() int {
	return a.anchorCenterX() + a.islandDX
}

// panelDropRect is the open panel's box as the drop and release checks read
// it: the sheet's width on the strip's axis, hanging from the window's top
// edge. A dragged island moves the panel, so these checks must not read the
// window's default left edge.
func (a *shelf) panelDropRect() mygo.Rectangle {
	return mygo.Rectangle{X: a.stripCenter() - sheetW/2, Y: a.y, Width: sheetW, Height: a.panelH()}
}
