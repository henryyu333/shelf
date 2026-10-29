//go:build darwin

package main

import (
	"math"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/transfer"
	"github.com/egoist/mygo/ui"

	"shelf/internal/layout"
)

// The pinned card's look (design/tools-v1.html §4): pure black, radius 18,
// a 0.5pt white 14% edge so it reads on any wallpaper — the one difference
// from the same card inside the notch panel. The card itself draws as
// toolCard draws it on the tools page (cardDesktop surface).
const (
	pinCardW  = 136 // one-column card; two-column cards are 278
	pinCardW2 = 278
)

var (
	pinEdgeColor  = ui.RGBA(255, 255, 255, 0.14)
	pinStripMark  = ui.RGBA(255, 255, 255, 0.40)
	pinEdgeStrip  = ui.RGBA(255, 255, 255, 0.22)
	pinHotFill    = ui.Hex("#06192E")
	pinExpandAnim = ui.Animation{Spring: ui.SpringOptions{Duration: pinExpand, Bounce: 0.12}}
)

// view draws the pinned window: the tool card while free or expanded,
// the narrow edge strip while collapsed. The window frame matches the
// content exactly; the shadow is the window's own.
func (it *pinItem) view(c *ui.Context) {
	c.Root().Background(ui.Transparent)
	a := it.owner
	if !it.expanded {
		it.viewStrip(c)
		return
	}
	// Desktop drops belong to this zone, not the tools-page snapshot.
	opts := transfer.DropOptions{Formats: []transfer.Format{transfer.FileList, transfer.URIList, transfer.PNG, transfer.Text}, Operations: transfer.Copy}
	zone := ui.Column(c.Key("pin")).Fill().Radius(18).Clip()
	zone.Background(surface).Border(0.5, pinEdgeColor)
	slide := zone.AnimateTo(pinRevealKey{it}, 1, pinExpandAnim)
	_, over := ui.DataDragOver(zone, opts)
	zone.Children(func() {
		card := a.toolCard(c, layout.Card{ID: it.cardID, Span: it.span}, cardDesktop).Fill()
		if !it.jiggleAt.IsZero() && !c.Preferences().ReduceMotion {
			k := float64(c.Now().Sub(it.jiggleAt)) / float64(toolJiggle)
			if k < 1 {
				card.Rotate(float32(math.Sin(k*3*math.Pi)*3*(1-k))).TransformOrigin(0.5, 0.5)
				c.AnimationFrame()
			}
		}
		switch it.edge {
		case pinLeft:
			card.Translate(-float32(it.cardW-pinStripW)*(1-slide), 0)
		case pinRight:
			card.Translate(float32(it.cardW-pinStripW)*(1-slide), 0)
		}
	})
	if over || time.Now().Before(it.dropAt) {
		zone.Border(1.5, c.Theme().Accent)
	}
	it.trackPress(zone)
	if it.pressWas && !it.didMove && zone.Clicked() {
		// A press that never went far is a tap on the card; a press that
		// moved the window or only woke the strip is not.
		if !a.runToolCard(it.cardID) {
			it.jiggleAt = time.Now()
			a.toolCardTapFeedback()
		}
	}
	it.pinMenu(zone)
	if drop, ok := ui.DropData(zone, opts); ok {
		a.pinReceive(it, drop.Data)
	}
}

// viewStrip draws the collapsed edge strip: an 8×56 black pill flush to
// the screen edge with the 2×16 grab mark the mock gives it.
func (it *pinItem) viewStrip(c *ui.Context) {
	root := ui.Column(c.Key("pin")).Fill()
	it.trackPress(root)
	it.pinMenu(root)
	root.Children(func() {
		mark := ui.Column(c.Key("strip")).Fill().Radius(4).Center()
		mark.Background(surface).Border(0.5, pinEdgeStrip)
		mark.Children(func() {
			ui.Box(c.Key("mark")).Size(2, 16).Radius(1).Background(pinStripMark)
		})
	})
}

// trackPress wires the pin's pointer session to the real pointer
// events: a primary Down arms a fresh session (the previous gesture's
// move must not poison this tap), a Move past pinDragStart locks the
// press as a window move, and the Up ends the session — leaving the
// card's tap to the view's Clicked check, or settling a moved release
// from the release point itself. The 30 ms poll only follows the cursor
// with the window; it no longer decides when a press began or ended.
// The secondary button passes through: its press belongs to the
// context menu (pinMenu).
func (it *pinItem) trackPress(e ui.Element) {
	e.PointerPosition()
	e.HandleInput(func(ev ui.InputEvent) bool {
		switch ev.Kind {
		case ui.InputPointerDown:
			if ev.Button != 0 {
				return false // the context menu takes the secondary press
			}
			it.pressAt(float64(ev.X), float64(ev.Y))
			return true
		case ui.InputPointerMove:
			if !it.pressed {
				return false
			}
			it.lockMove(int(ev.X)-it.grabX, int(ev.Y)-it.grabY)
			return true
		case ui.InputPointerUp:
			if !it.pressed || ev.Button != 0 {
				return false
			}
			// A fast drag's release still locks as a move before it ends.
			it.lockMove(int(ev.X)-it.grabX, int(ev.Y)-it.grabY)
			it.pressed, it.moving = false, false
			if it.didMove {
				// Local to the window the last poll applied — it has not
				// moved since — so this is the release point in screen
				// coordinates.
				p := mygo.Point{X: it.frame.X + int(ev.X), Y: it.frame.Y + int(ev.Y)}
				it.owner.pinRelease(it, p)
			}
			return true
		}
		return false
	})
}

// pressAt arms the session a Down began: the grab point is where it
// landed, and the previous gesture's move must not poison this tap,
// nor may this tap see its own drag.
func (it *pinItem) pressAt(px, py float64) {
	it.grabX, it.grabY = int(px+0.5), int(py+0.5)
	it.pressWas = it.expanded
	it.moving, it.didMove = false, false
	it.pressed = true
}

// pinMenu is the right-click menu (design §4): 始终展开 toggles the
// auto-collapse, 放回刘海 hands the card back to the tools page, 从桌面移除
// takes it off the desktop without putting it back on the page.
func (it *pinItem) pinMenu(e ui.Element) {
	a := it.owner
	l := a.appLoc()
	e.ContextMenu(func(m *ui.Menu) {
		if it.edge != pinFree {
			if m.Item(l.PinAlways()).Checked(it.always).Chosen() {
				it.always = !it.always
				// Clearing it restarts the leave-delay rather than
				// collapsing the card under the cursor mid-hover.
				it.leftAt = time.Time{}
				a.savePins()
			}
			m.Separator()
		}
		if m.Item(l.PinPutBack()).Chosen() {
			it.wantKill, it.putBack = true, true
		}
		if m.Item(l.PinRemoveDesktop()).Chosen() {
			it.wantKill, it.putBack = true, false
		}
	})
}

// pinRevealKey keys the strip-to-card slide to this pin's window.
type pinRevealKey struct{ it *pinItem }
