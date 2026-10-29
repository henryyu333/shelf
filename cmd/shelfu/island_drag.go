//go:build darwin

package main

import (
	"math"
	"strconv"

	"github.com/egoist/mygo"

	"shelf/internal/settings"
)

// Dragging the floating island. A press on the closed shell is a tap until
// it travels islandDragStart; past that it becomes a move and the shell
// follows the pointer, clamped onto its display. A tap still opens the
// panel, as it always did. Only the closed shell moves: the open panel is
// full of controls. The place is saved once, on the release.
//
// Every screen coordinate the shelf draws, hits or drops into comes from
// two anchors — stripCenter() and the window's top edge a.y — so moving
// those two moves the shell, the panel, the drop band, the snap hot zones,
// the completion capsule and the live activities together.

const islandDragStart = 4 // a press that travels this far is a move, not a tap

// islandSpot is the floating island's place on one display, relative to
// that display's origin: the shell's centre X and its top Y. It is the
// runtime form of settings.IslandPos.
type islandSpot struct{ Centre, Top int }

// islandPress is one left press on the closed island: where it landed, how
// far inside the capsule it grabbed, and whether it has become a move.
type islandPress struct {
	active bool
	moved  bool
	at     mygo.Point
	grabX  int // pointer offset inside the capsule box, 0..islandIdleW-1
	grabY  int // ...and down from the shell's top edge
}

// islandPanelRoom is the box the opened panel needs, the opening spring's
// overshoot included: the island must leave that much room wherever it is
// dropped, or the panel would open partly off screen.
func islandPanelRoom() (w, h int) {
	return sheetW + int(math.Ceil(sheetW*panelOpenOvershoot)),
		sheetH + int(math.Ceil(sheetH*panelOpenOvershoot))
}

// clampIslandSpot holds a dragged island on its display: the shell's top
// never rises above defTop (the row the style rests on — below the camera
// housing, or the 7pt gap under a bare screen's top edge), and the panel it
// opens stays whole inside work, horizontally as well as down. bounds and
// work are screen rectangles; s and the result are display-relative. Pure;
// unit-tested.
func clampIslandSpot(s islandSpot, bounds, work mygo.Rectangle, defTop int) islandSpot {
	if work.Width <= 0 || work.Height <= 0 {
		work = bounds
	}
	pw, ph := islandPanelRoom()
	// The panel is centred on the shell and hangs from the shell's top.
	minX := work.X - bounds.X + pw/2
	maxX := work.X + work.Width - bounds.X - pw/2
	minY := defTop
	maxY := work.Y + work.Height - bounds.Y - ph
	if maxX < minX {
		// A visible area narrower than the panel: one valid centre.
		minX = work.X + work.Width/2 - bounds.X
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
	spot := clampIslandSpot(islandSpot{Centre: pos.X, Top: pos.Y}, a.dispBounds, a.workArea(), a.islandTop)
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
		a.dispBounds, a.workArea(), a.islandTop)
	def := a.defaultIslandSpot()
	dx, dy := spot.Centre-def.Centre, spot.Top-def.Top
	if dx == a.islandDX && dy == a.islandDY {
		return
	}
	a.islandDX, a.islandDY = dx, dy
	a.y = a.dispBounds.Y + dy
	a.syncWindowFrame()
}

// beginIslandPress arms a press on the closed island: the panel opens on the
// release unless the press travels first (islandDragTick). The grab point is
// kept inside the capsule box, so a press on the wider hover/live pill still
// moves the shell without it jumping out from under the pointer.
func (a *shelf) beginIslandPress(p mygo.Point) {
	r := a.islandRect()
	top := a.y + a.sheetTop()
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
