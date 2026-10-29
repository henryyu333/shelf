//go:build darwin

package main

import (
	"math"
	"slices"
	"sort"
	"strconv"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/transfer"
	"github.com/egoist/mygo/ui"

	"shelf/internal/layout"
	"shelf/internal/mac"
)

// Desktop pins: a tool card dragged out of the tools page becomes a small
// floating window on the desktop. Releasing it within pinSnapDist of a
// screen's left or right edge snaps it there as a pinStripW×pinStripH strip
// that expands on hover (or while a file drag comes within pinWakeDist),
// collapses pinHideDelay after the pointer leaves, and can be pinned open.
// Releasing the card top-centre on the notch hands it back to the tools
// page.
//
// The card the window shows comes from the tool catalog (tools_cards.go):
// a.toolCard(c, layout.Card{...}, cardDesktop) draws it, a.runToolCard(id)
// runs its tap and a.pinReceive(it, data) takes its file drop. The
// placement lives in layout.json's pins list (internal/layout), written
// through the tools page's mutateTools so Cards and Pins stay one file.
const (
	pinSnapDist  = 24 // release this close to a screen edge and it docks
	pinWakeDist  = 40 // a file drag this close to a strip wakes it
	pinStripW    = 8  // collapsed strip width, flush to the edge
	pinStripH    = 56 // collapsed strip height
	pinStripGap  = 8  // stacked strips on one edge stay this far apart
	pinHideDelay = 600 * time.Millisecond
	pinExpand    = 240 * time.Millisecond
	pinCardH     = 84
	pinReturnH   = 120                    // release this high, centred on the notch, returns the card
	pinHoldAfter = 900 * time.Millisecond // drop confirmation on the card
	pinDragStart = 4                      // a press that goes this far is a window move
)

// pinEdge is the screen side a pin docks to.
type pinEdge int

const (
	pinFree pinEdge = iota
	pinLeft
	pinRight
)

func (e pinEdge) layout() layout.Edge {
	switch e {
	case pinLeft:
		return layout.EdgeLeft
	case pinRight:
		return layout.EdgeRight
	}
	return ""
}

func pinEdgeOf(e layout.Edge) pinEdge {
	switch e {
	case layout.EdgeLeft:
		return pinLeft
	case layout.EdgeRight:
		return pinRight
	}
	return pinFree
}

// pinItem is one pinned window: the layout.Pin's placement plus its
// runtime state. frame is the applied screen rect (card size while free
// or expanded, strip size while collapsed).
type pinItem struct {
	owner     *shelf
	cardID    string
	span      int
	rows      int // row span the card had in the grid; 1 for action cards
	cardW     int // 136 or 278 from span; height is always pinCardH
	homeIndex int // tool-page grid slot it came from; -1 pins it back at the end
	display   int64
	edge      pinEdge
	always    bool // always expanded; the hide delay never collapses it
	expanded  bool // valid only while edge != pinFree
	cy        int  // the collapsed strip's centre-Y while docked — the slot it returns to
	frame     mygo.Rectangle
	leftAt    time.Time
	dropAt    time.Time // accent border lingers this long after a drop
	jiggleAt  time.Time // a tap that could not act gave the card a brief shake
	pressed   bool      // a press on the card is being held
	pressWas  bool      // it was already expanded when that press began
	moving    bool      // the press went far: it is a window move, not a click
	didMove   bool      // that press did move — its release is a drop, never a tap
	grabX     int
	grabY     int
	wantKill  bool // a menu asked to remove it; pinTick does the destroy
	putBack   bool // …and whether the card returns to the tools page
	win       *mygo.Window
}

// pinDisplays is the display list the geometry sees; tests replace it.
var pinDisplays = func() []mygo.Display { return mygo.Screen.Displays() }

// pinArea returns the work area of a display, or of the primary when the
// ID is gone (unplugged, or a test list that lacks it): the remembered
// edge survives the move.
func pinArea(display int64) mygo.Rectangle {
	var primary mygo.Rectangle
	for _, d := range pinDisplays() {
		if d.ID == display {
			return d.WorkArea
		}
		if d.Primary || primary == (mygo.Rectangle{}) {
			primary = d.WorkArea
		}
	}
	return primary
}

func (it *pinItem) displayRect() mygo.Rectangle { return pinArea(it.display) }

func pinDisplayID(display int64) string {
	if display == 0 {
		return ""
	}
	return strconv.FormatInt(display, 10)
}

func pinParseDisplay(s string) int64 {
	id, _ := strconv.ParseInt(s, 10, 64)
	return id
}

// pinFrameUnder puts a cardW × pinCardH pin card under the pointer, keeping
// the grab point the user took hold of. The grab is mapped into the card in
// proportion to the card it came from, so the pointer always sits on the
// card: a 2×2 widget is 195 × 174 but the pin card is 84pt tall, and an
// unscaled grab from its lower half would leave the pointer above the card.
func pinFrameUnder(cursor mygo.Point, grabDX, grabDY, srcW, srcH float32, cardW int) mygo.Rectangle {
	if srcW <= 0 {
		srcW = 1
	}
	if srcH <= 0 {
		srcH = 1
	}
	gx := float32(cardW) * min(max(grabDX, 0), srcW) / srcW
	gy := float32(pinCardH) * min(max(grabDY, 0), srcH) / srcH
	// A grab on the source's last point still lands on the card.
	gx = min(gx, float32(cardW)-1)
	gy = min(gy, float32(pinCardH)-1)
	return mygo.Rectangle{
		X: cursor.X - int(math.Round(float64(gx))), Y: cursor.Y - int(math.Round(float64(gy))),
		Width: cardW, Height: pinCardH,
	}
}

// pinClamp holds r inside area: a pinned card never sits under the menu
// bar or off its display.
func pinClamp(r mygo.Rectangle, area mygo.Rectangle) mygo.Rectangle {
	r.X = min(max(r.X, area.X), max(area.X, area.X+area.Width-r.Width))
	r.Y = min(max(r.Y, area.Y), max(area.Y, area.Y+area.Height-r.Height))
	return r
}

// pinExpandFrame is the card's frame while docked: flush to the docked
// edge, vertically centred on the strip it opened from, inside the work
// area.
func pinExpandFrame(edge pinEdge, area mygo.Rectangle, w, cy int) mygo.Rectangle {
	f := mygo.Rectangle{Y: cy - pinCardH/2, Width: w, Height: pinCardH}
	if edge == pinRight {
		f.X = area.X + area.Width - w
	} else {
		f.X = area.X
	}
	return pinClamp(f, area)
}

// pinStripFrame is the collapsed strip's frame: flush to the edge, kept
// inside the work area.
func pinStripFrame(edge pinEdge, area mygo.Rectangle, cy int) mygo.Rectangle {
	f := mygo.Rectangle{Y: cy - pinStripH/2, Width: pinStripW, Height: pinStripH}
	if edge == pinRight {
		f.X = area.X + area.Width - pinStripW
	} else {
		f.X = area.X
	}
	return pinClamp(f, area)
}

// pinReleaseEdge reports which edge a released card docks to: the release
// point within pinSnapDist of its display's left or right work-area edge.
func pinReleaseEdge(p mygo.Point, area mygo.Rectangle) pinEdge {
	switch {
	case p.X >= area.X-pinSnapDist && p.X <= area.X+pinSnapDist:
		return pinLeft
	case p.X >= area.X+area.Width-pinSnapDist && p.X <= area.X+area.Width+pinSnapDist:
		return pinRight
	}
	return pinFree
}

// pinReturnZone reports whether the dragged pin's centre lands on the
// shelf's own closed shell: the floating island's capsule wherever the user
// left it, else the hardware notch when the shelf tracks this display, else
// a release-band the same shape at the top of the display the centre is
// on. The same zone drives the 松开放回 bulge and the put-back release.
func (a *shelf) pinReturnZone(centre mygo.Point) bool {
	if a.island {
		// The floating island is its own put-back target: the card goes
		// back where the island is, which need not be the notch's column
		// or the top of the screen.
		return pinNear(centre, a.islandRect(), notchHoverPadX)
	}
	if centre.Y >= a.notchY+notchHoverPadY+pinReturnH {
		return false
	}
	// The shelf follows the cursor across displays (monitorDrag places
	// it on the cursor's): a pin dragged at the notch is by definition
	// on the shelf's display. The zone centres on the hardware notch,
	// widening like overAnchor does.
	nx, nw := a.notchX, a.notchW
	if nw <= 0 {
		nw = 200 // no measured notch: the strip's default centre
	}
	half := float64(nw)/2 + notchHoverPadX
	return abs(centre.X-(nx+nw/2)) <= int(half)
}

// pinStagger spaces collapsed strips on one edge of one display
// pinStripGap apart, top to bottom, holding the column inside the work
// area — strips that would overhang pull back up; a column taller than
// the area squeezes instead. Expanded cards keep their frame: the
// overlap is theirs while the pointer is on them. The pass also refreshes
// each strip's dock slot (cy).
func pinStagger(pins []*pinItem) {
	type edgeKey struct {
		disp int64
		edge pinEdge
	}
	groups := map[edgeKey][]*pinItem{}
	for _, it := range pins {
		if it.edge == pinFree || it.expanded {
			continue
		}
		k := edgeKey{it.display, it.edge}
		groups[k] = append(groups[k], it)
	}
	for _, g := range groups {
		if len(g) < 2 {
			continue
		}
		sort.SliceStable(g, func(i, j int) bool { return g[i].frame.Y < g[j].frame.Y })
		area := pinArea(g[0].display)
		// Forward pass: each strip sits at or below the previous one.
		y := g[0].frame.Y
		for _, it := range g {
			it.frame.Y = max(it.frame.Y, y)
			y = it.frame.Y + pinStripH + pinStripGap
		}
		if y-pinStripGap > area.Y+area.Height {
			// The column grew past the work area's bottom: pull it back
			// up from the bottom edge — anchoring the pull on the last
			// strip (which the forward pass had just spaced out) is a
			// no-op.
			y = area.Y + area.Height - pinStripH
			for i := len(g) - 1; i >= 0; i-- {
				g[i].frame.Y = min(g[i].frame.Y, y)
				y = g[i].frame.Y - pinStripH - pinStripGap
			}
		}
		if g[0].frame.Y < area.Y {
			// The head sits above the work area — an edge that only
			// overflowed its bottom, or a column saved before the bounds
			// tightened. A column that fits re-spaces down from the top;
			// one taller than the work area cannot fit at all: squeeze
			// the strips evenly top to bottom — they overlap, but every
			// strip stays on screen.
			if g[len(g)-1].frame.Y+pinStripH-g[0].frame.Y <= area.Height {
				y = area.Y
				for _, it := range g {
					it.frame.Y = max(it.frame.Y, y)
					y = it.frame.Y + pinStripH + pinStripGap
				}
			} else {
				span := area.Height - pinStripH
				for i, it := range g {
					it.frame.Y = area.Y
					if span > 0 {
						it.frame.Y += i * span / (len(g) - 1)
					}
				}
			}
		}
		for _, it := range g {
			it.cy = it.frame.Y + pinStripH/2
		}
	}
}

// pinApplyFrame pushes it.frame to the native window.
func (it *pinItem) applyFrame() {
	if it.win != nil {
		mac.SetWindowFrame(it.win.NativeHandle(), it.frame.X, it.frame.Y, it.frame.Width, it.frame.Height)
	}
}

// pinMoveFrame is the card frame while it is dragged: card-sized, top-left
// under the grab point, clamped into the work area so it can never be
// lost off-screen or parked under the menu bar.
func (it *pinItem) pinMoveFrame(p mygo.Point, area mygo.Rectangle) mygo.Rectangle {
	return pinClamp(mygo.Rectangle{X: p.X - it.grabX, Y: p.Y - it.grabY, Width: it.cardW, Height: pinCardH}, area)
}

// lockMove turns the press into a window move once it goes past
// pinDragStart from the grab point: the card shows the size the dragged
// frame is sized for, and the grab re-anchors to the top-left so a
// press that only woke the strip does not jump. dx and dy are the
// pointer's displacement from the grab point; a press already moving
// stays as it is.
func (it *pinItem) lockMove(dx, dy int) {
	if it.moving || (abs(dx) < pinDragStart && abs(dy) < pinDragStart) {
		return
	}
	it.moving, it.expanded, it.didMove = true, true, true
	it.grabX, it.grabY = 0, 0
}

// pinNear reports whether p is within dist of r, on any side.
func pinNear(p mygo.Point, r mygo.Rectangle, dist int) bool {
	return p.X >= r.X-dist && p.X < r.X+r.Width+dist && p.Y >= r.Y-dist && p.Y < r.Y+r.Height+dist
}

// pinWakeDrag expands the collapsed strips a file drag crosses, from the
// mouse-dragged event rather than the 30 ms pinTick: a release inside the
// wake distance in the same beat otherwise lands on a strip that had no
// chance to open and receive it.
func (a *shelf) pinWakeDrag(p mygo.Point) {
	for _, it := range a.pins {
		if !it.expanded && it.edge != pinFree && pinNear(p, it.frame, pinWakeDist) {
			it.expanded = true
			it.leftAt = time.Time{}
			if it.cy == 0 {
				it.cy = it.frame.Y + pinStripH/2
			}
			it.frame = pinExpandFrame(it.edge, it.displayRect(), it.cardW, it.cy)
			it.applyFrame()
			it.invalidate()
		}
	}
}

// addPin pins a card to the desktop: the card leaves the tools page grid
// (layout.AddPin enforces the one-card-one-place rule), its window opens
// at frame, and the placement lands in layout.json. cardID is a tool
// catalog id; frame is the release rect (a strip frame when docked,
// card-sized elsewhere). The tools page's drag-out calls this.
func (a *shelf) addPin(cardID string, span int, display int64, edge pinEdge, always bool, frame mygo.Rectangle) *pinItem {
	w := pinCardW
	if span == 2 {
		w = pinCardW2
	}
	it := &pinItem{
		owner: a, cardID: cardID, span: span, cardW: w,
		display: display, edge: edge, always: always,
		expanded: edge == pinFree, frame: frame,
	}
	if !a.commitPin(it) {
		return nil
	}
	return it
}

// commitPin publishes a placement only after its layout change succeeds.
// A drag preview can lend its existing native window to the committed pin.
func (a *shelf) commitPin(it *pinItem) bool {
	l := a.toolsLayout()
	it.homeIndex = slices.IndexFunc(l.Cards, func(c layout.Card) bool { return c.ID == it.cardID })
	if it.rows < 1 {
		// The card is still in the grid here: AddPin takes it out below.
		it.rows = a.cardRows(it.cardID)
	}
	it.frame = pinClamp(it.frame, pinArea(it.display))
	area := it.displayRect()
	stored := layout.Pin{
		ID: it.cardID, Span: it.span, Rows: max(it.rows, 1),
		Display: pinDisplayID(it.display), Edge: it.edge.layout(),
		X: it.frame.X - area.X, Y: it.frame.Y - area.Y, AlwaysOpen: it.always,
	}
	if !a.mutateTools(func(l *layout.Layout) { l.AddPin(stored) }) {
		return false
	}
	if it.edge != pinFree {
		it.cy = it.frame.Y + pinStripH/2
	}
	a.pins = append(a.pins, it)
	pinStagger(a.pins)
	for _, other := range a.pins {
		other.applyFrame() // restagger existing strips before the new window shows
	}
	if it.win == nil {
		pinOpenWindow(it)
	} else {
		it.win.OnClose(it.onClose)
		mac.ConfigurePinWindow(it.win.NativeHandle())
		it.invalidate()
	}
	a.savePins()
	return true
}

// pinOpenWindow opens a pin's native window; a seam so the drag-out and
// restore paths can be tested without a window server.
var pinOpenWindow = func(it *pinItem) { it.openWindow(false) }

// pinOpenPreview opens the transient window a drag follows the pointer in;
// the same seam, for the tools page and the home canvas.
var pinOpenPreview = func(it *pinItem) { it.openWindow(true) }

// openWindow creates a non-activating card panel. A preview ignores mouse
// events and sits above the source panel; a committed pin floats at level 3.
func (it *pinItem) openWindow(preview bool) {
	f := it.frame
	w := mygo.NewWindow(mygo.WindowOptions{
		Title: it.owner.appLoc().WindowPins(), Width: f.Width, Height: f.Height,
		X: f.X, Y: f.Y,
		Hidden: true, Frameless: true, Transparent: true, DisableShadow: true,
		AlwaysOnTop: true, SkipTaskbar: true, Panel: true,
		DisableMove: true, DisableResize: true, DisableMinimize: true, DisableMaximize: true, DisableFullScreen: true,
		Content: ui.View(func(c *ui.Context) { it.view(c) }),
	})
	it.win = w
	w.SetVisibleOnAllWorkspaces(true)
	if preview {
		mac.ConfigureDragPreview(w.NativeHandle(), it.owner.windowLevel()+1)
	} else {
		mac.ConfigurePinWindow(w.NativeHandle())
		w.OnClose(it.onClose)
	}
	mac.SetWindowFrame(w.NativeHandle(), f.X, f.Y, f.Width, f.Height)
	w.ShowInactive()
}

// onClose handles a system-initiated close (Space switch, Exposé
// tidy-up): the card returns to the tools page, same as 放回刘海. On a
// quit — OnBeforeQuit has already set quitting — the window goes away
// but the pin and its layout entry stay, so the next launch restores it.
// The window is already going away; unpin must not Destroy it again.
func (it *pinItem) onClose(ev *mygo.CloseEvent) {
	w := it.win
	it.win = nil // the native close owns destruction on success
	if it.owner.quitting {
		return
	}
	if !it.owner.unpin(it, true) {
		it.win = w
		if ev != nil {
			ev.PreventDefault()
		}
	}
}

// unpin removes it from the desktop and its pin from the layout.
// putBack returns the card to the tools page grid — its home slot, or
// the end when that is gone (放回刘海); otherwise the card leaves the
// desktop without rejoining the page and can be re-added from the "+"
// catalog later (从桌面移除, design §4).
//
// A widget returns to the cell it was pinned from, at the size it had; a
// pin that recorded none (saved by an older build) tries the catalogue
// placement first and then the first free cell. When the canvas has no
// room the pin and its window stay, with a hint to free a cell, instead of
// dropping the card while taking it off the desktop.
func (a *shelf) unpin(it *pinItem, putBack bool) bool {
	if !a.mutateToolsChanged(func(l *layout.Layout) bool {
		// A pin restored from layout.json has no remembered slot (-1):
		// 放回刘海 appends it to the page's end rather than the front.
		before := it.homeIndex
		if before < 0 {
			before = len(l.Cards)
		}
		l.RemovePin(it.cardID, putBack, before)
		return true
	}) {
		it.wantKill = false // do not retry a failed menu action every tick
		return false
	}
	for i, p := range a.pins {
		if p == it {
			a.pins = append(a.pins[:i], a.pins[i+1:]...)
			break
		}
	}
	w := it.win
	it.win = nil
	if w != nil {
		w.Destroy()
	}
	a.savePins()
	return true
}

// cardRows is the row span a card has in the grid (1 for cards that are
// not placed, and for every action card).
func (a *shelf) cardRows(id string) int {
	for _, c := range a.toolsLayout().Cards {
		if c.ID == id {
			return c.Height()
		}
	}
	return 1
}

// pinDisplaysChanged re-anchors pins whose display went away: docked pins
// keep their edge on the primary display, floating ones land inside it.
func (a *shelf) pinDisplaysChanged() {
	var primary int64
	seen := map[int64]bool{}
	for _, d := range pinDisplays() {
		seen[d.ID] = true
		if d.Primary || primary == 0 {
			primary = d.ID
		}
	}
	changed := false
	for _, it := range a.pins {
		if seen[it.display] {
			continue
		}
		it.display = primary
		area := it.displayRect()
		if it.edge != pinFree {
			// An expanded strip re-docks collapsed on the new display
			// (the stagger sorts it); a free card clamps inside it.
			it.expanded = false
			it.frame = pinStripFrame(it.edge, area, it.frame.Y+it.frame.Height/2)
			it.cy = it.frame.Y + pinStripH/2
		} else {
			it.frame = pinClamp(it.frame, area)
		}
		it.applyFrame()
		it.invalidate()
		changed = true
	}
	if changed {
		pinStagger(a.pins)
		for _, it := range a.pins {
			it.applyFrame()
		}
		a.savePins()
	}
}

// pinTick drives the pinned windows from the monitor: window moves while
// the press is a drag, hover expand, the delayed collapse and the
// always-open state. p is the cursor, down the left button, fileDrag a
// system drag the cards can receive.
func (a *shelf) pinTick(now time.Time, p mygo.Point, down, fileDrag bool) {
	returning := false
	for _, it := range append([]*pinItem{}, a.pins...) {
		if it.wantKill {
			a.unpin(it, it.putBack)
			continue
		}
		area := it.displayRect()
		if it.pressed {
			if down && it.moving {
				// Only follow an event-owned gesture. Sampling the
				// button here must not create or finish a session.
				it.display = displayIDAt(p, it.display)
				area = it.displayRect()
				it.frame = it.pinMoveFrame(p, area)
				it.applyFrame()
				centre := mygo.Point{X: it.frame.X + it.frame.Width/2, Y: it.frame.Y + it.frame.Height/2}
				returning = returning || a.pinReturnZone(centre)
			}
			continue
		}
		if it.edge == pinFree {
			continue
		}
		if it.always {
			if !it.expanded {
				it.expanded = true
				if it.cy == 0 {
					it.cy = it.frame.Y + pinStripH/2
				}
				it.frame = pinExpandFrame(it.edge, area, it.cardW, it.cy)
				it.applyFrame()
				it.invalidate()
			}
			continue // always expanded: the hide delay never runs
		}
		over := insideRect(p, it.frame) || (fileDrag && pinNear(p, it.frame, pinWakeDist))
		if it.expanded {
			if over {
				it.leftAt = time.Time{}
			} else if it.leftAt.IsZero() {
				it.leftAt = now
			} else if now.Sub(it.leftAt) >= pinHideDelay {
				it.expanded = false
				it.leftAt = time.Time{}
				// The card returns to its own dock slot — it may have
				// been clamped while open, so its centre is not the
				// strip's. The edge's column then re-spaces.
				cy := it.cy
				if cy == 0 {
					cy = it.frame.Y + pinCardH/2
				}
				it.frame = pinStripFrame(it.edge, area, cy)
				it.cy = it.frame.Y + pinStripH/2
				pinStagger(a.pins)
				for _, other := range a.pins {
					other.applyFrame()
				}
				it.invalidate()
			}
		} else if over {
			it.expanded = true
			it.leftAt = time.Time{}
			if it.cy == 0 {
				it.cy = it.frame.Y + pinStripH/2
			}
			it.frame = pinExpandFrame(it.edge, area, it.cardW, it.cy)
			it.applyFrame()
			it.invalidate()
		}
	}
	// A pin held over the notch bulges 松开放回: peekView reads
	// a.pinReturning for the shape, the release in pinRelease reads the
	// same zone. The flag clears the tick the drag ends; monitorDrag's
	// own fold takes the shell down on the next pass.
	if a.pinReturning != returning {
		a.pinReturning = returning
		if returning && !a.visible {
			a.setPeek(true)
		}
	}
}

func (it *pinItem) invalidate() {
	if it.win != nil {
		it.win.Invalidate()
	}
}

// pinRelease settles a moved card on its final position: back to the
// tools page on the notch, docked to an edge, or floating where it
// landed. The verdict and the landing come from the release point
// itself — the frame the last poll applied can be a tick behind the
// cursor, so a release that left the notch after the last sample must
// not read that sample's centre, and a free release lands under the
// release, not where the poll stopped.
func (a *shelf) pinRelease(it *pinItem, p mygo.Point) {
	// The release's own point picks the display — same point
	// pinReleaseEdge reads — so a release just past a seam still lands on
	// the display the cursor reached between two move ticks.
	it.display = displayIDAt(p, it.display)
	area := it.displayRect()
	// The grab re-anchored to the top-left when the move began, so the
	// move frame under the release is the release's own rectangle.
	frame := it.pinMoveFrame(p, area)
	centre := mygo.Point{X: frame.X + frame.Width/2, Y: frame.Y + frame.Height/2}
	if a.pinReturnZone(centre) {
		a.unpin(it, true)
		return
	}
	if edge := pinReleaseEdge(p, area); edge != pinFree {
		it.edge, it.expanded = edge, false
		it.frame = pinStripFrame(edge, area, p.Y)
		it.cy = it.frame.Y + pinStripH/2
		pinStagger(a.pins)
		for _, other := range a.pins {
			other.applyFrame() // the stagger may move strips it did not release
		}
	} else {
		it.edge, it.expanded, it.cy = pinFree, true, 0
		it.frame = frame
	}
	it.applyFrame()
	a.savePins()
}

// displayIDAt resolves the display a released card belongs to: the one
// containing the point, else the nearest, else the remembered one.
func displayIDAt(p mygo.Point, keep int64) int64 {
	var nearest int64
	best := -1
	for _, d := range pinDisplays() {
		if insideRect(p, d.Bounds) {
			return d.ID
		}
		dx := max(0, max(d.Bounds.X-p.X, p.X-d.Bounds.X-d.Bounds.Width))
		dy := max(0, max(d.Bounds.Y-p.Y, p.Y-d.Bounds.Y-d.Bounds.Height))
		if best < 0 || dx*dx+dy*dy < best {
			best, nearest = dx*dx+dy*dy, d.ID
		}
	}
	if nearest != 0 {
		return nearest
	}
	return keep
}

// pinReceive is the drop on a pinned card: files the card accepts run
// its action on this drop's own list — never a.drop.files, the tools-page
// zone's snapshot, which would act on a previous drag's paths. Anything
// else lands in the tray like a drop on the notch, and so does an action
// the pipeline cannot start (runDropActionFiles shelves on a false
// startConvert): a release on a pin never loses the files. dropAt keeps
// the accent border up a moment as the confirmation the notch capsule
// gives elsewhere.
func (a *shelf) pinReceive(it *pinItem, data transfer.Data) {
	def := toolCardDefFor(it.cardID)
	if def != nil && def.kind == cardAction {
		if files, err := dropFilesData(data); err == nil && len(files) > 0 && def.accepts(classifyDrop(files)) {
			a.runDropActionFiles(a.toolCardDropAction(it.cardID, files), files, data)
			it.dropAt = time.Now().Add(pinHoldAfter)
			it.invalidate()
			return
		}
	}
	if err := a.receive(data); err != nil {
		a.toastStatus(a.appLoc().PinDropFailed(err.Error()), time.Time{}, statusErr)
	}
	it.dropAt = time.Now().Add(pinHoldAfter)
	it.invalidate()
}

func abs(v int) int {
	if v < 0 {
		return -v
	}
	return v
}

// savePins mirrors the live pins into layout.json's pin list: positions
// are stored inside each display's visible frame, as layout.Pin wants.
// mutateTools keeps the tools page's Cards untouched and writes the file.
func (a *shelf) savePins() {
	pins := make([]layout.Pin, 0, len(a.pins))
	for _, it := range a.pins {
		area := it.displayRect()
		f := it.frame
		if it.edge != pinFree {
			// A docked pin's stored rect is always its strip — the slot
			// it collapses back to. An open card's frame is bigger and,
			// clamped to the work area, not even centred on the strip;
			// saving it would drift the pin every cycle.
			cy := it.cy
			if cy == 0 {
				cy = f.Y + f.Height/2
			}
			f = pinStripFrame(it.edge, area, cy)
		}
		stored := layout.Pin{
			ID: it.cardID, Span: it.span, Rows: max(it.rows, 1),
			Display: pinDisplayID(it.display),
			Edge:    it.edge.layout(), X: f.X - area.X, Y: f.Y - area.Y,
			AlwaysOpen: it.always,
		}
		pins = append(pins, stored)
	}
	a.mutateTools(func(l *layout.Layout) { l.Pins = pins })
}

// pinRestore opens the pinned cards' windows from the shared layout (the
// tools page reads the same toolsLayout copy). A card the catalog does
// not know yet still pins — an unknown id draws the empty cell toolCard
// reserves.
func (a *shelf) pinRestore() {
	for _, p := range a.toolsLayout().Pins {
		disp := pinParseDisplay(p.Display)
		edge := pinEdgeOf(p.Edge)
		w := pinCardW
		if p.Span == 2 {
			w = pinCardW2
		}
		area := pinArea(disp)
		var frame mygo.Rectangle
		if edge == pinFree {
			frame = mygo.Rectangle{X: area.X + p.X, Y: area.Y + p.Y, Width: w, Height: pinCardH}
		} else {
			frame = pinClamp(mygo.Rectangle{X: area.X + p.X, Y: area.Y + p.Y, Width: pinStripW, Height: pinStripH}, area)
		}
		it := &pinItem{
			owner: a, cardID: p.ID, span: p.Span, rows: max(p.Rows, 1), cardW: w, homeIndex: -1,
			display: disp, edge: edge, always: p.AlwaysOpen,
			expanded: edge == pinFree, frame: frame,
		}
		if edge != pinFree {
			it.cy = it.frame.Y + pinStripH/2
		}
		a.pins = append(a.pins, it)
		pinOpenWindow(it)
	}
	// Pins saved on a display that is gone now (unplugged while the app
	// was not running) land on the primary; the pass rewrites the stored
	// ID, so layout.json stops pointing at a missing screen.
	a.pinDisplaysChanged()
}
