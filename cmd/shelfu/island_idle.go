//go:build darwin

package main

import (
	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"shelf/internal/mac"
)

// Idle islands on the other screens: every display without a camera
// housing shows its own resting capsule, even while the resident window
// (the one that grows into the panel) follows the pointer elsewhere. They
// are display-only lightweight windows that never take a click; hovering
// or pressing one moves the resident window to that screen, which then
// draws the same capsule itself with all the live states attached.

// idleIslandWindow is one extra display's resting capsule. Windows are
// kept per display id and only moved when the display layout changes.
type idleIslandWindow struct {
	dispID int64
	win    *mygo.Window
	frame  mygo.Rectangle
}

// idleIslandView draws the 120 × 30 black capsule that fills the tiny
// window; the transparent corners are what makes it read as an island.
func idleIslandView(c *ui.Context) {
	c.Root().Background(ui.Transparent)
	ui.Column(c.Key("idle-island")).Fill().Label(loc(c).IslandLabel()).Draw(
		func(p *ui.Painter, r ui.Rect) {
			rad := min(r.W/2, r.H/2)
			var path ui.Path
			path.MoveTo(r.X+rad, r.Y)
			path.LineTo(r.X+r.W-rad, r.Y)
			path.QuadTo(r.X+r.W, r.Y, r.X+r.W, r.Y+rad)
			path.LineTo(r.X+r.W, r.Y+r.H-rad)
			path.QuadTo(r.X+r.W, r.Y+r.H, r.X+r.W-rad, r.Y+r.H)
			path.LineTo(r.X+rad, r.Y+r.H)
			path.QuadTo(r.X, r.Y+r.H, r.X, r.Y+r.H-rad)
			path.LineTo(r.X, r.Y+rad)
			path.QuadTo(r.X, r.Y, r.X+rad, r.Y)
			path.Close()
			p.FillPath(&path, surface)
		})
}

// syncIdleIslands reconciles the extra capsules with the current display
// layout and the resident window's display. Main thread only; a headless
// test shelf (no native window) owns none.
func (a *shelf) syncIdleIslands() {
	if a.win == nil || a.quitting {
		a.closeIdleIslands()
		return
	}
	displays := mygo.Screen.Displays()
	ds := make([]islandDisplay, 0, len(displays))
	for _, d := range displays {
		_, _, _, _, hasNotch := mac.NotchRect(d.ID)
		ds = append(ds, islandDisplay{ID: d.ID, Bounds: d.Bounds, Work: d.WorkArea, HasNotch: hasNotch})
	}
	want := idleIslandPlacements(ds, a.dispID, a.settings.ShellStyle, a.settings.IslandPos)
	have := make(map[int64]*idleIslandWindow, len(a.idleIslands))
	for _, w := range a.idleIslands {
		have[w.dispID] = w
	}
	next := make([]*idleIslandWindow, 0, len(want))
	for _, pl := range want {
		w := have[pl.DisplayID]
		delete(have, pl.DisplayID)
		if w == nil {
			w = a.openIdleIsland(pl)
			if w == nil {
				continue
			}
		} else if w.frame != pl.Frame {
			w.frame = pl.Frame
			mac.SetWindowFrame(w.win.NativeHandle(), pl.Frame.X, pl.Frame.Y, pl.Frame.Width, pl.Frame.Height)
		}
		next = append(next, w)
	}
	for _, w := range have {
		w.win.Destroy()
	}
	a.idleIslands = next
}

// openIdleIsland creates one display-only capsule window for pl.
func (a *shelf) openIdleIsland(pl idlePlacement) *idleIslandWindow {
	f := pl.Frame
	w := mygo.NewWindow(mygo.WindowOptions{
		Title: a.appLoc().WindowIsland(), Width: f.Width, Height: f.Height,
		X: f.X, Y: f.Y,
		Hidden: true, Frameless: true, Transparent: true, DisableShadow: true,
		AlwaysOnTop: true, SkipTaskbar: true, Panel: true,
		DisableMove: true, DisableResize: true, DisableMinimize: true, DisableMaximize: true, DisableFullScreen: true,
		Content: ui.View(idleIslandView),
	})
	if w == nil {
		return nil
	}
	w.SetVisibleOnAllWorkspaces(true)
	h := w.NativeHandle()
	mac.ConfigureNotchWindow(h)
	// The capsule is a picture: the resident window handles the pointer
	// and the global mouse-down observer moves it here on a press.
	mac.SetOverlayAcceptsMouse(h, false)
	mac.SetWindowFrame(h, f.X, f.Y, f.Width, f.Height)
	w.ShowInactive()
	return &idleIslandWindow{dispID: pl.DisplayID, win: w, frame: f}
}

// closeIdleIslands destroys every extra capsule; quit and a headless
// shelf both go through it.
func (a *shelf) closeIdleIslands() {
	for _, w := range a.idleIslands {
		w.win.Destroy()
	}
	a.idleIslands = nil
}

// idleIslandAt reports whether p is on one of the display-only capsules,
// so a press that reached another app can still bring the shelf there.
func (a *shelf) idleIslandAt(p mygo.Point) bool {
	for _, w := range a.idleIslands {
		if p.X >= w.frame.X && p.X < w.frame.X+w.frame.Width &&
			p.Y >= w.frame.Y && p.Y < w.frame.Y+w.frame.Height {
			return true
		}
	}
	return false
}
