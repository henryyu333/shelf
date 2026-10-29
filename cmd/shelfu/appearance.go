//go:build darwin

package main

// The expanded panel follows the system appearance: dark shows pure black,
// light shows dark frosted glass (a pure black band joins the hardware
// notch, then a translucent dark material with the desktop blurred behind
// it). Reduce Transparency always means pure black. Hover shapes, the drop
// hint, the completion capsule and the live activities stay pure black at
// all times: they hang off the notch and must read as hardware.
//
// Only the panel's own tiling is affected. The material itself is a system
// NSVisualEffectView behind the window, framed to the shell every time the
// window frame is applied (internal/mac), so the blur follows the shape
// without a per-frame mask.

import (
	"time"

	"github.com/egoist/mygo/ui"

	"shelf/internal/mac"
	"shelf/internal/settings"
)

const appearancePoll = 250 * time.Millisecond

// refreshAppearance re-reads the system appearance and Reduce Transparency
// on a slow tick: the panel can change while it runs.
func (a *shelf) refreshAppearance(now time.Time) {
	if !a.appearanceAt.IsZero() && now.Sub(a.appearanceAt) < appearancePoll {
		return
	}
	if a.appearanceAt.IsZero() {
		// First read also seeds the frame's material decision.
		a.appearanceAt = now
	}
	a.appearanceAt = now
	dark, reduce := macSystemDark(), macReduceTransparency()
	if dark != a.systemDark || reduce != a.reduceTrans {
		a.systemDark, a.reduceTrans = dark, reduce
		// The change reaches the open panel at once: the material is
		// created, hidden or re-placed here, not on the next size change.
		a.syncMaterial()
		if a.win != nil {
			a.win.Invalidate()
		}
	}
}

// glassNow reports whether the expanded panel should draw as frosted glass
// instead of pure black. Only the open panel: the hover shapes, the drop
// hint, the completion capsule and the live activities hang off the notch
// and stay pure black, and a fold turns black on its first frame so the
// shrinking shell never shows the desktop through the peek form. The
// setting picks follow-the-system (black in Dark Mode, glass in Light
// Mode), always black, or always glass; the island style and Reduce
// Transparency are black whatever it says.
func (a *shelf) glassNow() bool {
	if a.win == nil || a.island || a.reduceTrans || !a.visible {
		return false
	}
	switch a.settings.PanelAppearance.Normalized() {
	case settings.PanelAppearanceBlack:
		return false
	case settings.PanelAppearanceGlass:
		return true
	}
	return !a.systemDark
}

// panelFill is the shell's own paint: solid black, or the light
// appearance's black band fading into translucent dark glass.
func (a *shelf) panelFill(p *ui.Painter, path *ui.Path, shellH float32) {
	if !a.glassNow() || shellH <= 0 {
		p.FillPath(path, surface)
		return
	}
	// The prototype's stops: solid black for the first 30pt (the band the
	// notch occupies), then rgba(20,20,26,.68) by 58pt and .72 at the
	// bottom.
	glass := ui.RGBA(20, 20, 26, 0.70)
	p.FillPathGradient(path, ui.LinearGradient{
		Angle: 180,
		Stops: []ui.GradientStop{
			{Color: surface, At: 0},
			{Color: surface, At: min(30/shellH, 0.5)},
			{Color: glass.Alpha(0.68), At: min(58/shellH, 0.8)},
			{Color: glass, At: 1},
		},
	})
}

// panelAppearanceIndex maps a saved appearance to the segmented control's
// segment: 0 follows the system, 1 is black, 2 is glass.
func panelAppearanceIndex(p settings.PanelAppearance) int {
	switch p.Normalized() {
	case settings.PanelAppearanceBlack:
		return 1
	case settings.PanelAppearanceGlass:
		return 2
	}
	return 0
}

// panelAppearanceAt maps the segmented control's segment to a saved
// appearance.
func panelAppearanceAt(idx int) settings.PanelAppearance {
	switch idx {
	case 1:
		return settings.PanelAppearanceBlack
	case 2:
		return settings.PanelAppearanceGlass
	}
	return settings.PanelAppearanceFollow
}

// setPanelAppearance saves the expanded panel's look and repaints an open
// panel at once: the fill reads the setting on the next build, and the
// material view is placed, hidden or shown here rather than on the next
// size change.
func (a *shelf) setPanelAppearance(p settings.PanelAppearance) {
	p = p.Normalized()
	if p == a.settings.PanelAppearance.Normalized() {
		return
	}
	next := a.settings
	next.PanelAppearance = p
	if !a.applySettings(next) {
		return
	}
	a.syncMaterial()
	if a.win != nil {
		a.win.Invalidate()
	}
}

// macSystemDark and macReduceTransparency are seams so headless tests never
// touch AppKit.
var (
	macSystemDark         = func() bool { return mac.SystemDark() }
	macReduceTransparency = func() bool { return mac.ReduceTransparency() }
)
