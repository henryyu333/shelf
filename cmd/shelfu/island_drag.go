//go:build darwin

package main

import (
	"math"
	"strconv"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"shelf/internal/settings"
)

// Dragging the floating island. A press on the closed shell is a tap until
// it travels islandDragStart; past that it becomes a move and the shell
// follows the pointer, clamped onto its display. A tap still opens the
// panel, as it always did. A press on the open panel's band, on its blank
// stretches, moves the island and the panel together by the same rule. The
// place is saved once, on the release.
//
// Every screen coordinate the shelf draws, hits or drops into comes from
// two anchors — stripCenter() and the island's top edge (a.y plus the
// style's row) — so moving those two moves the shell, the drop band, the
// snap hot zones, the completion capsule and the live activities together.
// The opened panel is placed from the same two anchors (panelSpot): below
// the island when the screen has room, above it when it does not.

const islandDragStart = 4 // a press that travels this far is a move, not a tap

// islandSpot is the floating island's place on one display, relative to
// that display's origin: the shell's centre X and its top Y. It is the
// runtime form of settings.IslandPos.
type islandSpot struct{ Centre, Top int }

// islandPress is one left press on the shell: where it landed, how far
// inside the panel (or capsule) it grabbed, and whether it has become a
// move. panel says the press started on the open panel's band; up is the
// side the panel grew on when it did, kept for the whole gesture.
type islandPress struct {
	active bool
	moved  bool
	panel  bool // the press started on the open panel's band
	up     bool // ...and the panel's side at that moment
	at     mygo.Point
	grabX  int // pointer offset inside the grabbed box
	grabY  int // ...and down from its top edge
}

// panelFlip is a side change in flight, after a panel drag was released on
// the other side of the island. from and to are the panel's top edges on
// screen; k runs 0→1 on the panel's open spring and is read by
// panelPlacement, so the box, its frame and its hit shape slide together.
// start marks the first drawn frame, which seeds the spring at zero.
type panelFlip struct {
	active bool
	start  bool
	k      float32
	from   int
	to     int
}

// islandHoverH is how tall the closed island can draw: the idle capsule
// plus the hover row below it (the peek chevron). The island keeps that
// much room below its top so a hover never hangs off the screen.
const islandHoverH = islandIdleH + peekBody

// clampIslandSpot holds a dragged island inside its display's whole box:
// the capsule and the hover row below it stay on the screen, and the top
// never rises above defTop (the row the style rests on — below the camera
// housing, or the 7pt gap under a bare screen's top edge). Neither the
// panel nor the visible frame bounds the island: the panel is what has to
// fit the work area, and it lifts off the island when the Dock covers the
// bottom rows. bounds is the display's screen rectangle; s and the result
// are display-relative. Pure; unit-tested.
func clampIslandSpot(s islandSpot, bounds mygo.Rectangle, defTop int) islandSpot {
	minX := islandIdleW / 2
	maxX := bounds.Width - islandIdleW/2
	minY := defTop
	maxY := bounds.Height - islandHoverH
	if maxX < minX {
		// A screen narrower than the capsule: one valid centre.
		minX = bounds.Width / 2
		maxX = minX
	}
	if maxY < minY {
		maxY = minY
	}
	s.Centre = min(max(s.Centre, minX), maxX)
	s.Top = min(max(s.Top, minY), maxY)
	return s
}

// islandSpotKey is the settings key one display's spot lives under.
func islandSpotKey(display int64) string { return strconv.FormatInt(display, 10) }

// defaultIslandSpot is where the island sits when the user never moved it:
// centred on the hardware notch (or the display), on the row the style puts
// it on. Display-relative.
func (a *shelf) defaultIslandSpot() islandSpot {
	return islandSpot{Centre: a.anchorCenterX() - a.dispBounds.X, Top: a.islandTop}
}

// islandSpotNow is the island's current place on this display.
func (a *shelf) islandSpotNow() islandSpot {
	return islandSpot{Centre: a.stripCenter() - a.dispBounds.X, Top: a.y + a.islandTop - a.dispBounds.Y}
}

// savedIslandSpot reads this display's stored place.
func (a *shelf) savedIslandSpot() (settings.IslandPos, bool) {
	pos, ok := a.settings.IslandPos[islandSpotKey(a.dispID)]
	return pos, ok
}

// workArea is the display's visible frame, or its bounds when placeAt has
// not measured one (a headless test shelf).
func (a *shelf) workArea() mygo.Rectangle {
	if a.dispWork.Width > 0 && a.dispWork.Height > 0 {
		return a.dispWork
	}
	return a.dispBounds
}

// placeIslandAxis puts the window's top back on its display and applies the
// display's saved place, clamped. The notch style has no place to apply: it
// stays on the display's top edge and axis. placeAt and a style switch both
// go through it, so nothing ever replays the island at the default spot
// behind the user's back.
func (a *shelf) placeIslandAxis() {
	a.islandDX, a.islandDY = 0, 0
	a.y = a.dispBounds.Y
	if !a.island {
		return
	}
	pos, ok := a.savedIslandSpot()
	if !ok {
		return
	}
	spot := clampIslandSpot(islandSpot{Centre: pos.X, Top: pos.Y}, a.dispBounds, a.islandTop)
	def := a.defaultIslandSpot()
	a.islandDX, a.islandDY = spot.Centre-def.Centre, spot.Top-def.Top
	a.y += a.islandDY
}

// moveIslandTo puts the shell's centre and top at a screen coordinate,
// clamped onto the display, and moves the one window that draws it. Only the
// island shape has a place of its own: a re-place that landed on the notch
// shell must never hand it an island offset.
func (a *shelf) moveIslandTo(centre, top int) {
	if !a.island {
		return
	}
	spot := clampIslandSpot(islandSpot{Centre: centre - a.dispBounds.X, Top: top - a.dispBounds.Y},
		a.dispBounds, a.islandTop)
	def := a.defaultIslandSpot()
	dx, dy := spot.Centre-def.Centre, spot.Top-def.Top
	if dx == a.islandDX && dy == a.islandDY {
		return
	}
	a.islandDX, a.islandDY = dx, dy
	a.y = a.dispBounds.Y + dy
	a.syncWindowFrame()
}

// beginPanelPress arms a press on the open panel's band. The panel itself
// is what follows the pointer: the grab offset is kept inside the panel's
// box, the side it grew on is locked for the gesture, and a new press
// settles any side change still in flight so the box cannot slide out from
// under it.
func (a *shelf) beginPanelPress(p mygo.Point) {
	a.finishPanelFlip()
	place := a.panelPlacement()
	a.islandPress = islandPress{
		active: true,
		panel:  true,
		up:     place.Up,
		at:     p,
		grabX:  p.X - place.X,
		grabY:  p.Y - place.Y,
	}
	a.islandDrag = false
	a.syncDragGuard()
}

// bandBlank reports whether p is on the open panel's band and on none of
// its controls. Only that stretch starts a move: a press on a page icon,
// a live activity chip or the settings button keeps its own click, and a
// press below the band belongs to the page. The boxes were measured on the
// last drawn frame; a frame that has not drawn the band yet has none, so
// no press can be taken for blank there.
func (a *shelf) bandBlank(p mygo.Point) bool {
	if a.bandBox.Width <= 0 || a.bandBox.Height <= 0 {
		return false
	}
	place := a.panelPlacement()
	x, y := p.X-place.X, p.Y-place.Y
	if x < 0 || y < 0 || x >= a.bandBox.Width || y >= a.bandBox.Height {
		return false
	}
	for _, r := range a.bandHits {
		if x >= r.X && y >= r.Y && x < r.X+r.Width && y < r.Y+r.Height {
			return false
		}
	}
	return true
}

// bandBegin starts the band's press test for this frame: the band's box
// comes from the last drawn frame, and its controls are added as they are
// built (bandAdd). A frame that has not drawn the band yet measures
// nothing, so no press can be taken for blank there.
func (a *shelf) bandBegin(box ui.Rect) {
	a.bandHits = a.bandHits[:0]
	a.bandBox = mygo.Rectangle{}
	if box.W <= 0 || box.H <= 0 {
		return
	}
	a.bandBox = mygo.Rectangle{
		X:      int(math.Round(float64(box.X))),
		Y:      int(math.Round(float64(box.Y))),
		Width:  int(math.Round(float64(box.W))),
		Height: int(math.Round(float64(box.H))),
	}
}

// bandAdd keeps one control's box for the press test, relative to the
// band's own top-left — the shell's when the panel rests. The box is the
// previous frame's, which is the frame the pointer was over.
func (a *shelf) bandAdd(e ui.Element) {
	r := e.Bounds()
	if r.W <= 0 || r.H <= 0 {
		return
	}
	a.bandHits = append(a.bandHits, mygo.Rectangle{
		X:      int(math.Round(float64(r.X))) - a.bandBox.X,
		Y:      int(math.Round(float64(r.Y))) - a.bandBox.Y,
		Width:  int(math.Round(float64(r.W))),
		Height: int(math.Round(float64(r.H))),
	})
}

// startPanelFlip sends the panel to the side its new place calls for, from
// the side it was dragged on. Only the top edge differs between the two
// boxes, so the flight is a slide on the panel's own spring, read back by
// panelPlacement on every drawn frame.
func (a *shelf) startPanelFlip(from, to panelPlacement) {
	if from.Y == to.Y && from.Up == to.Up {
		return
	}
	a.flip = panelFlip{active: true, start: true, from: from.Y, to: to.Y}
	if a.win != nil {
		a.win.Invalidate()
	}
}

// finishPanelFlip settles a side change at once.
func (a *shelf) finishPanelFlip() { a.flip = panelFlip{} }

// panelFlipStep advances a side change in the frame being drawn, on the
// same spring the panel opens with; the value is the one panelPlacement
// reads. Reduce Motion takes the new side at once, as every other spring
// does. The flight ends when the spring has settled.
func (a *shelf) panelFlipStep(zone ui.Element) {
	if !a.flip.active {
		return
	}
	if a.flip.start {
		zone.Jump("flip", 0)
		a.flip.start = false
	}
	k := min(max(zone.AnimateTo("flip", 1, panelOpenSpring), 0), 1)
	a.flip.k = k
	if k >= 1 || zone.Settled("flip") {
		a.flip = panelFlip{}
	}
}

// beginIslandPress arms a press on the closed island: the panel opens on the
// release unless the press travels first (islandDragTick). The grab point is
// kept inside the capsule box, so a press on the wider hover/live pill still
// moves the shell without it jumping out from under the pointer.
func (a *shelf) beginIslandPress(p mygo.Point) {
	r := a.islandRect()
	top := a.frameTop() + a.sheetTop()
	a.islandPress = islandPress{
		active: true,
		at:     p,
		grabX:  min(max(p.X-r.X, 0), islandIdleW-1),
		grabY:  min(max(p.Y-top, 0), islandIdleH-1),
	}
	a.islandDrag = false
	// The notch's corridor must not clamp the user's own gesture: this parks
	// the drag filter for the whole press and the release unparks it.
	a.syncDragGuard()
}

// islandDragTick follows a press on the closed island and reports whether the
// gesture belongs to it. While it does, the monitor skips the drop band, the
// auto-hide and the snap session: a moving island opens nothing under
// itself. Releasing a tap opens the panel exactly as before.
func (a *shelf) islandDragTick(p mygo.Point, down bool) bool {
	press := &a.islandPress
	if !press.active {
		return false
	}
	if !a.island {
		// The gesture crossed onto a display that draws the notch shell:
		// there is nothing here to carry, and that shell has no place to
		// apply. End the gesture on this frame — no move, no save, so the
		// notch keeps its offset of zero — and let it die with the press
		// that carried it.
		*press = islandPress{}
		a.islandDrag = false
		if !down {
			a.ownPress = false
		}
		a.syncDragGuard()
		a.syncMousePassThrough()
		return true
	}
	if press.panel {
		return a.panelDragTick(p, down)
	}
	// The release frame carries the pointer's final place, so the tap/move
	// threshold is judged there too: a press that only travels as it lifts
	// is a move, not a tap, and a drag ends where the button came up rather
	// than on the last tick's sample.
	moved := press.moved ||
		abs(p.X-press.at.X) > islandDragStart || abs(p.Y-press.at.Y) > islandDragStart
	if down && !moved {
		return true
	}
	if !press.moved {
		press.moved, a.islandDrag = true, true
		a.setPeek(false) // the shell stays its 120 × 30 capsule while it moves
		a.syncDragGuard()
	}
	centre, top := p.X-press.grabX+islandIdleW/2, p.Y-press.grabY
	if down {
		a.moveIslandTo(centre, top)
		return true
	}
	*press = islandPress{}
	a.islandDrag = false
	a.ownPress = false
	if moved {
		a.moveIslandTo(centre, top)
		a.saveIslandSpot()
	} else {
		a.show(false)
	}
	a.syncDragGuard()
	a.syncMousePassThrough()
	return true
}

// panelDragTick follows a press that started on the open panel's band. The
// panel keeps its grab point under the pointer and stays whole inside the
// work area; the island is derived from wherever the panel then is, on the
// side the press started on, and saved on the release like any island
// move. A press that never travels islandDragStart is nothing at all: the
// panel stays open on its page, and no click is synthesised.
func (a *shelf) panelDragTick(p mygo.Point, down bool) bool {
	press := &a.islandPress
	moved := press.moved ||
		abs(p.X-press.at.X) > islandDragStart || abs(p.Y-press.at.Y) > islandDragStart
	if down && !moved {
		return true
	}
	w, h := a.panelBox()
	up := press.up
	spot := panelDragSpot(p.X-press.grabX, p.Y-press.grabY, up, int(w), int(h),
		a.workArea(), a.dispBounds, a.islandTop)
	centre, top := a.dispBounds.X+spot.Centre, a.dispBounds.Y+spot.Top
	if down {
		if !press.moved {
			press.moved, a.islandDrag = true, true
			a.syncDragGuard()
		}
		a.moveIslandTo(centre, top)
		return true
	}
	*press = islandPress{}
	a.islandDrag = false
	a.ownPress = false
	if moved {
		a.moveIslandTo(centre, top)
		a.saveIslandSpot()
		// The place the release leaves may have room below the island now:
		// the panel is then due on the other side. It slides there on the
		// open spring, never jumping with the box it was dragged on.
		from := panelSpotSide(a.stripCenter(), a.anchorScreenTop(), islandIdleH,
			a.workArea(), int(w), int(h), up)
		to := panelSpot(a.stripCenter(), a.anchorScreenTop(), islandIdleH,
			a.workArea(), int(w), int(h))
		if to.Up != up {
			a.startPanelFlip(from, to)
		}
	}
	a.syncDragGuard()
	a.syncMousePassThrough()
	return true
}

// saveIslandSpot writes this display's place to settings.json after a move.
// Only the release saves: a drag is not a stream of writes, and a broken
// settings file is never overwritten.
func (a *shelf) saveIslandSpot() {
	if a.settingsBroken || !a.island {
		return
	}
	spot := a.islandSpotNow()
	pos := settings.IslandPos{X: spot.Centre, Y: spot.Top}
	key := islandSpotKey(a.dispID)
	if a.settings.IslandPos[key] == pos {
		return
	}
	next := a.settings
	next.IslandPos = make(map[string]settings.IslandPos, len(a.settings.IslandPos)+1)
	for k, v := range a.settings.IslandPos {
		next.IslandPos[k] = v
	}
	next.IslandPos[key] = pos
	a.applySettings(next)
}

// resetIslandPosition forgets this display's place and replays the default
// one: 设置 › 通用 › 恢复默认位置.
func (a *shelf) resetIslandPosition() {
	key := islandSpotKey(a.dispID)
	if _, ok := a.settings.IslandPos[key]; ok {
		next := a.settings
		next.IslandPos = make(map[string]settings.IslandPos, len(a.settings.IslandPos))
		for k, v := range a.settings.IslandPos {
			if k != key {
				next.IslandPos[k] = v
			}
		}
		if !a.applySettings(next) {
			return
		}
	}
	a.place(mygo.Point{X: a.dispBounds.X + a.dispBounds.Width/2, Y: a.dispBounds.Y + 1})
	a.syncMousePassThrough()
	if a.win != nil {
		a.win.Invalidate()
	}
	a.syncIdleIslands()
}
