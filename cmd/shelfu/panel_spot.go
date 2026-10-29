//go:build darwin

package main

import (
	"math"
	"time"

	"github.com/egoist/mygo"
)

// Where the opened panel sits once the island may be dragged anywhere on
// the screen. The island itself only has to stay whole on its display, so
// near the bottom edge there is no room left for a 440 × 250 sheet below
// it: the panel then hangs above the island instead, its bottom edge on the
// island's own bottom edge — or, when a Dock lifts the visible frame off
// the island's rows, on the visible frame's bottom edge. Horizontally the
// panel is centred on the island and clamped into the visible frame — near
// a side edge it simply moves off the island's axis rather than leaving the
// screen.

// panelPlacement is the opened panel's box in screen coordinates, and which
// side of the island it grows from. Work is the visible frame; Up says the
// panel's bottom edge sits on the island's bottom edge (or on the visible
// frame's when the work area lifts it off) instead of its top.
type panelPlacement struct {
	X, Y, W, H int
	Up         bool
}

// panelSpot places a w × h panel for an island whose top edge is anchorTop
// (screen Y) and which is anchorH tall, centred on centre. work is the
// display's visible frame. The island's own box is not clamped here: the
// caller already keeps the island whole. Pure; unit-tested.
func panelSpot(centre, anchorTop, anchorH int, work mygo.Rectangle, w, h int) panelPlacement {
	if w <= 0 || h <= 0 {
		return panelPlacement{W: w, H: h}
	}
	return panelSpotSide(centre, anchorTop, anchorH, work, w, h, anchorTop+h > work.Y+work.Height)
}

// panelSpotSide places the panel on a named side instead of the one the
// island has room for: a drag keeps the side it started on, and the release
// animates to the other one. Pure; unit-tested.
func panelSpotSide(centre, anchorTop, anchorH int, work mygo.Rectangle, w, h int, up bool) panelPlacement {
	p := panelPlacement{W: w, H: h}
	if w <= 0 || h <= 0 {
		return p
	}
	centre = centre - w/2
	if centre+w > work.X+work.Width {
		centre = work.X + work.Width - w
	}
	if centre < work.X {
		centre = work.X
	}
	p.X = centre
	if !up {
		// Room below the island: the panel hangs from the island's top edge
		// as it always did.
		p.Y = anchorTop
		return p
	}
	// Grow upward with the bottom edge on the island's. An island in the
	// Dock's rows sits below the visible frame, so the panel is lifted off
	// it: the box stays whole inside the work area, bottom on the work
	// area's edge, as the plan allows.
	p.Up = true
	p.Y = anchorTop + anchorH - h
	if p.Y+h > work.Y+work.Height {
		p.Y = work.Y + work.Height - h
	}
	if p.Y < work.Y {
		p.Y = work.Y
	}
	return p
}

// panelDragSpot turns the panel box a drag asks for into the island spot
// that holds it on side up. The box is clamped whole into the work area
// first — that is the edge the panel stops at — and the island is then
// clamped onto its display with clampIslandSpot, exactly as a drag of the
// closed shell does. Deriving the island from the panel (instead of the
// other way round) is what keeps the grab point under the pointer; the
// panel is re-derived from the clamped island afterwards, so a clamp moves
// both together instead of sliding the box off the island it hangs from.
// bounds and work are display-relative or screen-relative together, as the
// caller passes them. Pure; unit-tested.
func panelDragSpot(x, y int, up bool, w, h int, work, bounds mygo.Rectangle, defTop int) islandSpot {
	if w <= 0 || h <= 0 {
		return islandSpot{}
	}
	// Horizontally the work area is the edge. A downward panel may sit on
	// the island's own top row, which lies above the visible frame (the
	// default row is 7, the menu bar 25), so its top stops at the island's
	// lowest row instead; clampIslandSpot then holds it there too. An
	// upward panel keeps wholly inside the work area.
	minY := bounds.Y + defTop
	if up {
		minY = work.Y
	}
	x = min(max(x, work.X), max(work.X+work.Width-w, work.X))
	y = min(max(y, minY), max(work.Y+work.Height-h, minY))
	top := y
	if up {
		top = y + h - islandIdleH
	}
	return clampIslandSpot(islandSpot{
		Centre: x + w/2 - bounds.X,
		Top:    top - bounds.Y,
	}, bounds, defTop)
}

// frameCoverX is the width a window centred on centre needs to hold a shell
// w wide drawn on axis, plus margin on both sides. The frame stays
// symmetric about the strip so the shell keeps its place in the canvas;
// only the sides grow.
func frameCoverX(centre, axis int, w float32, margin int) int {
	if centre == axis {
		return int(math.Ceil(float64(w))) + 2*margin
	}
	half := int(math.Ceil(float64(w)/2)) + margin
	return 2 * (abs(axis-centre) + half)
}

// anchorScreenTop is the island's top edge on screen; every other height in
// this file is measured from it.
func (a *shelf) anchorScreenTop() int { return a.y + a.islandTop }

// upShellBottom is the upward shell's bottom edge on screen at openK: the
// island's own bottom while the shell is closed, easing onto the panel's
// placed bottom as it opens. The panel's bottom is the island's while the
// panel has room below it; a visible frame such as the Dock's lifts the
// panel off, and the drawn shell follows that clamped edge, so the settled
// shell is exactly the panel's box. The drawing, the window frame and the
// hit shape all read this one edge.
func (a *shelf) upShellBottom(openK float32) int {
	bottom := a.anchorScreenTop() + islandIdleH
	if !a.island {
		return bottom
	}
	p := a.panelPlacement()
	t := float64(min(max(openK, 0), 1))
	return bottom + int(math.Round(float64(p.Y+p.H-bottom)*t))
}

// upShellFloor is the lowest row an upward shell's window must hold: two
// points under the island's own bottom. The drawn shell's bottom eases up
// onto the clamped panel's, but the island it grows out of stays on screen
// below it, so the frame reaches under both.
func (a *shelf) upShellFloor() int {
	floor := a.anchorScreenTop() + islandIdleH + 2
	if a.flip.active {
		// A side change in flight slides the shell off that bottom, toward
		// the downward panel's; the frame reaches under the shell actually
		// drawn.
		floor = max(floor, a.upShellBottom(1))
	}
	return floor
}

// panelPlacement is the opened panel's box on screen at the current level.
// The hardware-notch shell has no island to move, so its panel keeps the
// old anchored box.
func (a *shelf) panelPlacement() panelPlacement {
	w, h := a.panelBox()
	if !a.island {
		return panelPlacement{X: a.stripCenter() - int(w)/2, Y: a.anchorScreenTop(), W: int(w), H: int(h)}
	}
	centre, top := a.stripCenter(), a.anchorScreenTop()
	work := a.workArea()
	if a.flip.active {
		// The side change in flight: the box slides between the two sides'
		// top edges, drawn from its bottom so the frame and the hit shape
		// follow it whole.
		p := panelSpot(centre, top, islandIdleH, work, int(w), int(h))
		k := float64(min(max(a.flip.k, 0), 1))
		p.Y = a.flip.from + int(math.Round(float64(a.flip.to-a.flip.from)*k))
		p.Up = true
		return p
	}
	if a.islandPress.panel {
		// A press on the band owns the side it started on: a panel dragged
		// with room below must not flip under the pointer mid-gesture.
		return panelSpotSide(centre, top, islandIdleH, work, int(w), int(h), a.islandPress.up)
	}
	return panelSpot(centre, top, islandIdleH, work, int(w), int(h))
}

// panelUp reports whether this island leaves no room below for its panel.
func (a *shelf) panelUp() bool { return a.island && a.panelPlacement().Up }

// shellUp reports whether the panel is drawn on the island's upward side
// right now: it grows up while open and keeps that side through the fold,
// until the shell is the capsule again.
func (a *shelf) shellUp() bool {
	return a.panelUp() && (a.visible || a.progress > 0.002 || a.fx.openK > 0.002)
}

// frameTop is the window's own top edge on screen: the island's top while
// the panel is closed or grows downward, the panel's top when it grows up.
// The drawing and every screen box measured from the window read this one
// value, so a window raised for an upward flight moves the shell with it.
func (a *shelf) frameTop() int {
	if a.shellUp() {
		return a.upFrameTop()
	}
	return a.y
}

// upFrameTop is the window's top edge while the shell hangs above the
// island: the highest of the settled panel's top, the shell actually drawn
// and the completion capsule's body, measured from the island's bottom
// edge. An open spring that overshoots its level, or a page folding back to
// the shorter overview, draws the shell above the panel's own top, and the
// frame has to reach up with it or the shell is clipped.
func (a *shelf) upFrameTop() int {
	top := a.panelPlacement().Y
	h := int(math.Ceil(float64(a.fx.h)))
	if !a.visible && a.fb.up(time.Now()) {
		// A fold in flight may end in the capsule, whose body is taller
		// than the sliver still drawn.
		h = max(h, a.capsuleBodyH())
	}
	if h > 0 {
		top = min(top, a.upShellBottom(a.fx.openK)-h)
	}
	return top
}

// shellTopLocal is the opened shell's top inside the window for height h at
// openK. Growing upward eases the bottom edge from the island's own onto
// the panel's placed one (upShellBottom), and the frame top already reaches
// up for the tallest shape drawn, so the local top is simply h above that
// bottom edge: it is never negative and settles exactly on the panel's box.
func (a *shelf) shellTopLocal(h, openK float32) float32 {
	if a.shellUp() {
		return float32(a.upShellBottom(openK)-a.frameTop()) - h
	}
	return float32(a.sheetTop())
}

// shellCentreX is the drawn shell's centre on screen: the island's axis
// while closed, easing onto the panel's own clamped axis as it opens.
func (a *shelf) shellCentreX(openK float32) int {
	if !a.island {
		return a.stripCenter()
	}
	p := a.panelPlacement()
	t := float64(min(max(openK, 0), 1))
	return a.stripCenter() + int(math.Round(float64(p.X+p.W/2-a.stripCenter())*t))
}
