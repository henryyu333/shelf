//go:build darwin

package main

import (
	"math"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"shelf/internal/settings"
)

// The island shell: a floating black capsule with four round corners. A
// screen without a camera housing always draws it; a notched screen draws
// it only when the user picks 灵动岛 in 设置 › 通用 › 刘海样式. These numbers
// are the design preview's geom() island branch: 120 × 30 idle, 4pt below
// the wings' band, a 36pt capsule for the status line, and 7pt (or 6pt
// below the notch) from the screen's top edge.
const (
	islandIdleW    = 120
	islandIdleH    = 30
	islandTopGap   = 7 // no housing: the capsule floats below the top edge
	islandNotchGap = 6 // with a housing: it starts below the camera notch
	islandCapsuleH = 36
)

// islandCornerK turns a quarter circle into one cubic Bézier segment: the
// design preview's CSS border-radius is a circular arc, so the shell uses
// the standard kappa control points instead of the notch path's square
// quadratic corners.
const islandCornerK = 0.55228475

// islandPath appends the island's rounded rectangle to path.
func islandPath(path *ui.Path, x0, y0, x1, y1, r float32) {
	c := r * islandCornerK
	path.MoveTo(x0+r, y0)
	path.LineTo(x1-r, y0)
	path.CubeTo(x1-r+c, y0, x1, y0+r-c, x1, y0+r)
	path.LineTo(x1, y1-r)
	path.CubeTo(x1, y1-r+c, x1-r+c, y1, x1-r, y1)
	path.LineTo(x0+r, y1)
	path.CubeTo(x0+r-c, y1, x0, y1-r+c, x0, y1-r)
	path.LineTo(x0, y0+r)
	path.CubeTo(x0, y0+r-c, x0+r-c, y0, x0+r, y0)
	path.Close()
}

// islandShell decides this display's closed shape and where the shell
// starts inside the window. The window's top edge is the display's top
// edge; the shell hangs from islandTop (0 for the hardware notch).
func islandShell(hasNotch bool, notchH int, style settings.ShellStyle) (island bool, top int) {
	if hasNotch && style.Normalized() != settings.ShellStyleIsland {
		return false, 0
	}
	if hasNotch {
		return true, notchH + islandNotchGap
	}
	return true, islandTopGap
}

// applyShellStyle recomputes the current display's shell shape from the
// geometry placeAt already measured and the saved preference. It is the
// one place the style becomes pixels, so switching in 设置 is immediate.
func (a *shelf) applyShellStyle() {
	a.island, a.islandTop = islandShell(a.hasNotch, a.notchH, a.settings.ShellStyle)
	// A side change measured against the old shell no longer means
	// anything: the panel takes the new display and style at once.
	a.finishPanelFlip()
}

// setShellStyle saves the 刘海样式 choice and redraws at once: the same
// display's measured geometry decides whether the new style is even
// visible, and every dependent surface (window frame, pointer gate, drag
// corridor, the other screens' idle capsules) follows in one place.
func (a *shelf) setShellStyle(style settings.ShellStyle) {
	style = style.Normalized()
	if a.settings.ShellStyle.Normalized() == style {
		return
	}
	next := a.settings
	next.ShellStyle = style
	if !a.applySettings(next) {
		return
	}
	if a.win != nil {
		// Re-place on the display the shelf already lives on, not under a
		// pointer that may be on the settings window's screen.
		a.place(mygo.Point{X: a.dispBounds.X + a.dispBounds.Width/2, Y: a.dispBounds.Y + 1})
		a.syncMousePassThrough()
		a.win.Invalidate()
	} else {
		a.applyShellStyle()
		// Headless: the style still decides whether this display carries a
		// saved place at all.
		a.placeIslandAxis()
	}
	a.syncIdleIslands()
}

// anchorSize is the shell's resting size, the base every peek shape and
// the panel spring grow from: the hardware notch's measured box, or the
// island's 120 × 30 capsule.
func (a *shelf) anchorSize() (w, h float32) {
	if a.island {
		return islandIdleW, islandIdleH
	}
	return float32(max(180, a.notchW)), float32(max(32, a.notchH))
}

// islandRect is the idle capsule on screen, in the same coordinates the
// pointer uses. Only meaningful while a.island; a shelf without measured
// geometry keeps a zero width so no hot zone appears at (0,0).
func (a *shelf) islandRect() mygo.Rectangle {
	w, h := a.anchorSize()
	return mygo.Rectangle{
		X: a.stripCenter() - int(w/2), Y: a.y + a.islandTop,
		Width: int(w + 0.5), Height: int(h + 0.5),
	}
}

// overIsland is the idle capsule's hover zone: the drawn capsule plus the
// same small margin the notch uses.
func (a *shelf) overIsland(p mygo.Point) bool {
	if a.notchW <= 0 || a.notchH <= 0 {
		return false
	}
	r := a.islandRect()
	return p.X >= r.X-notchHoverPadX && p.X <= r.X+r.Width+notchHoverPadX &&
		p.Y >= r.Y-notchHoverPadY && p.Y <= r.Y+r.Height+notchHoverPadY
}

// earWidth is the horizontal room the shell's top corners add on each
// side. The hardware-notch path reaches past its flanks by earR; the
// island's four round corners stay inside the box.
func (a *shelf) earWidth() int {
	if a.island {
		return 0
	}
	return earR
}

// bandW is the drag band's width: the anchor plus dropBandPadX per side.
func (a *shelf) bandW() int {
	w, _ := a.anchorSize()
	return int(w) + 2*dropBandPadX
}

// menuBand is the band's entry height: the menu bar over the hardware
// notch, the capsule's own body over the island (the island floats below
// the bar, so its drawn body is the entry edge).
func (a *shelf) menuBand() int {
	if a.island {
		return islandIdleH
	}
	return max(32, a.notchH)
}

// capsuleSize is the completion capsule's shell under the current style.
// The island has no camera housing to clear, so its capsule is one 36pt
// pill with the contents vertically centred instead of a 22pt band below
// the notch.
func (a *shelf) capsuleSize() (w, h float32) {
	baseW, baseH := a.anchorSize()
	w, h = a.fb.capsule(baseW, baseH)
	if a.island {
		h = islandCapsuleH
	}
	return w, h
}

// capsuleBodyH is the completion capsule's height under the current style:
// the notch band plus its 22pt line, or the island's one 36pt pill.
func (a *shelf) capsuleBodyH() int {
	if a.island {
		return islandCapsuleH
	}
	return max(32, a.notchH) + fbBody
}

// capsuleFrameW is the window width a completion capsule needs: the
// widest capsule, both corners, the failure shake's reach and the
// grow-out spring's overshoot.
func (a *shelf) capsuleFrameW() int {
	wide := int(math.Ceil(float64((fbMaxW - 180) * openOvershoot)))
	return fbMaxW + 2*(a.earWidth()+fbShakeX+wide)
}

// frameRoom is the box the window needs for a shell of expW × expH while
// the opening spring is on its way, style-aware: the notch path adds the
// ear margins, the island keeps its corners inside the box.
func (a *shelf) frameRoom(expW, expH float32) (w, h int) {
	if a.island {
		return int(math.Ceil(float64(expW * (1 + panelOpenOvershoot)))),
			int(math.Ceil(float64(expH*(1+panelOpenOvershoot)))) + 2
	}
	return openFrameRoom(expW, expH)
}

// islandDisplay is the geometry idleIslandPlacements needs from a display;
// HasNotch comes from mac.NotchRect, the same source place() uses.
type islandDisplay struct {
	ID       int64
	Bounds   mygo.Rectangle
	HasNotch bool
}

// idlePlacement is one extra display's idle capsule.
type idlePlacement struct {
	DisplayID int64
	Frame     mygo.Rectangle
}

// idleIslandPlacements lists the idle capsules every screen without a
// camera housing shows while the main shelf window lives on another
// display: each at the place the user left it there, or centred on the
// top edge when that screen was never dragged. style is accepted so callers
// state the preference they are rendering — a screen without a housing is
// always the island, even when the setting still says 刘海. saved is
// settings.IslandPos keyed by display id. Pure; unit-tested.
func idleIslandPlacements(displays []islandDisplay, current int64, style settings.ShellStyle, saved map[string]settings.IslandPos) []idlePlacement {
	_ = style
	var out []idlePlacement
	for _, d := range displays {
		if d.HasNotch || d.ID == current {
			continue
		}
		frame := mygo.Rectangle{
			X:      d.Bounds.X + (d.Bounds.Width-islandIdleW)/2,
			Y:      d.Bounds.Y + islandTopGap,
			Width:  islandIdleW,
			Height: islandIdleH,
		}
		if pos, ok := saved[islandSpotKey(d.ID)]; ok {
			// The same clamp the resident island gets, so a capsule can
			// never be restored off the screen it belongs to.
			spot := clampIslandSpot(islandSpot{Centre: pos.X, Top: pos.Y}, d.Bounds, islandTopGap)
			frame.X = d.Bounds.X + spot.Centre - islandIdleW/2
			frame.Y = d.Bounds.Y + spot.Top
		}
		out = append(out, idlePlacement{DisplayID: d.ID, Frame: frame})
	}
	return out
}
