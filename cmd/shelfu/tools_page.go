//go:build darwin

package main

// The 工具 page: one grid holding the action cards and the widgets. Cards
// come from layout.json (internal/layout); drawing and per-card behaviour
// live in tools_cards.go. This file owns the four-column packing, the
// long-press / drag-to-reorder gestures, the editing chrome (⊖ badge, size
// handle, wobble, the + tile), the card catalogue, and the page's own
// actions (the pencil, 完成).

import (
	"fmt"
	"math"
	"slices"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"shelf/internal/layout"
)

const (
	pageTools pageID = "tools" // 工具：用户排版的卡片页

	toolCols      = 4  // 4 列网格
	toolCellH     = 84 // 单行高（两行正好填满 174）
	toolCellGap   = 6  // 卡片间距
	toolStageW    = contentW
	toolPickRowH  = 30 // 目录行高（design §3 .pi 30px）
	toolDragSlop  = 6  // 普通态拖动超过此距离作废点击
	toolJiggle    = 300 * time.Millisecond
	toolWobbleDeg = 0.7 // 编辑摆动 ±0.7°
)

// toolCell is one card's position in content coordinates.
type toolCell struct {
	id   string
	x, y float32
	w, h float32
	plus bool // the dashed + slot (edit mode only)
}

// toolPress is the pointer session on one card: from the down, through a
// possible long-press or reorder drag, to the settle spring after the
// release. All coordinates are stage content space.
type toolPress struct {
	id         string
	downAt     time.Time
	downX      float32 // pointer position at the down (content space)
	downY      float32
	px, py     float32 // latest pointer position
	grabDX     float32 // pointer offset inside the card at the down
	grabDY     float32
	cardX      float32 // the card element's rendered left/top this frame
	cardY      float32 //   (the animation's answer, so moves stay aligned)
	dragging   bool    // slop passed: the lifted copy follows the pointer
	suppress   bool    // moved without edit: the release is not a click
	settling   bool    // released: flying to the committed cell or back
	settleJump bool    // the settle channels were Jump'ed to the start
	settleX    float32
	settleY    float32
	index      int // current insertion index while dragging
}

// toolsPage is the card grid page.
type toolsPage struct {
	host     *shelf
	scroll   ui.ScrollState
	viewport ui.Rect     // previous frame's scroll viewport, in window coordinates
	preview  dragPreview // transient drag window; absent from layout and live pins

	editing   bool
	picking   bool
	editAt    time.Time // wobble phase origin
	press     *toolPress
	skipClick bool   // the last release was a suppressed press, not a tap
	jiggleID  string // card shaking for 无可执行项
	jiggleAt  time.Time
}

func (p *toolsPage) id() pageID    { return pageTools }
func (p *toolsPage) label() string { return p.host.appLoc().ToolsTitle() }
func (p *toolsPage) icon(c *ui.Context, _ bool) ui.Element {
	return ui.Icon(c, toolsNavSymbol)
}
func (p *toolsPage) enabled() bool { return true }

// toolsPageOf is the shelf's tools page, or nil for test shelves that
// never registered one.
func (a *shelf) toolsPage() *toolsPage {
	if p, ok := a.page(pageTools).(*toolsPage); ok {
		return p
	}
	return nil
}

// toolsLayout is the persisted arrangement. It loads lazily from
// layout.json, defaults first so the page never waits on the file. The
// copy carries the runtime answer for which widgets are switched off, so
// their entries take no cell without the file losing them.
func (a *shelf) toolsLayout() layout.Layout {
	if a.toolsLay == nil {
		l := layout.DefaultWith(a.settings.AIUsage.Enabled)
		if a.dataDir != "" {
			got, err := layout.LoadWith(a.dataDir, a.settings.AIUsage.Enabled)
			if err != nil {
				// Like settingsBroken: the page still draws, but the file
				// the user could repair stays untouched until restart.
				a.toolsLayBroken = true
				a.report(err)
			}
			l = got
		}
		a.toolsLay = &l
	}
	a.toolsLay.Inactive = a.inactiveWidget
	return *a.toolsLay
}

// inactiveWidget reports widgets that are in layout.json but take no cell
// right now: a widget whose feature is off (AI 用量), or an id this build
// cannot draw.
func (a *shelf) inactiveWidget(id string) bool {
	def := toolCardDefFor(id)
	return def == nil || (def.kind == cardWidget && !def.available(a))
}

// mutateTools applies fn to a copy of the layout and saves it, reporting
// whether the change took. The in-memory copy is replaced only after the
// write succeeds, so a failed Write leaves both the file and the shown
// arrangement as they were — and the caller learns the card never moved;
// a load that failed (toolsLayBroken) latches writes off entirely — the
// page mutates for the session, the recoverable file stays put, which
// still counts as taken.
func (a *shelf) mutateTools(fn func(*layout.Layout)) bool {
	return a.mutateToolsChanged(func(l *layout.Layout) bool {
		fn(l)
		return true
	})
}

// mutateToolsChanged is mutateTools for an operation that knows whether it
// changed anything: fn reports it, and a false answer writes nothing — a
// refused gesture (a size that does not fit, a drop on an occupied cell)
// leaves the file untouched instead of rewriting the same layout.
func (a *shelf) mutateToolsChanged(fn func(*layout.Layout) bool) bool {
	l := a.toolsLayout()
	l.Cards = slices.Clone(l.Cards)
	l.Pins = slices.Clone(l.Pins)
	if !fn(&l) {
		return false
	}
	if a.dataDir != "" && !a.toolsLayBroken {
		if err := layout.Write(a.dataDir, l); err != nil {
			a.report(fmt.Errorf("tools layout: %w", err))
			return false // keep the pre-change layout and the file as they were
		}
	}
	a.toolsLay = &l
	if a.win != nil {
		a.win.Invalidate()
	}
	return true
}

// resetToolsEdit ends editing and the picker; called on hide and on
// leaving the page so re-entry starts clean.
func (a *shelf) resetToolsEdit() {
	if p := a.toolsPage(); p != nil {
		p.closeDragPreview()
		p.editing, p.picking, p.press = false, false, nil
		p.jiggleID = ""
		if a.win != nil {
			a.win.Invalidate()
		}
	}
}

func (p *toolsPage) title() string {
	if p.picking {
		return p.host.appLoc().ToolsCardCatalog()
	}
	return p.host.appLoc().ToolsTitle()
}

// actions is the page's own row: the pencil, or 完成 while editing or in
// the catalogue. When every card is placed the dashed + tile is the way
// into the catalogue; with no free cell it backs up with a + button here.
func (p *toolsPage) actions(c *ui.Context) []ui.Element {
	a := p.host
	live := a.tab == pageTools
	switch {
	case p.picking:
		return []ui.Element{a.toolDoneButton(c, live, "pick-done")}
	case p.editing:
		var out []ui.Element
		if p.pickable() {
			if !p.hasFreeCell() {
				b := a.iconButton(c, "tools-add", plusSymbol, loc(c).ToolsAddCard())
				if live && b.Clicked() {
					p.picking, p.press = true, nil
				}
				out = append(out, b)
			}
		}
		return append(out, a.toolDoneButton(c, live, "edit-done"))
	default:
		b := a.iconButton(c, "edit", pencilSymbol, loc(c).ToolsEdit())
		if live && b.Clicked() {
			p.editing, p.editAt = true, time.Now()
		}
		return []ui.Element{b}
	}
}

// toolDoneButton is the 完成 pill at the page's action row.
func (a *shelf) toolDoneButton(c *ui.Context, live bool, key string) ui.Element {
	b := ui.Row(c.Key(key)).Height(pageActionH).Padding(0, 10, 0, 10).Radius(pageActionH / 2).
		Background(accent).Center().Label(loc(c).Done())
	if live && b.Clicked() {
		if p := a.toolsPage(); p != nil {
			if p.picking {
				p.picking = false
			} else {
				p.closeDragPreview()
				p.editing, p.press = false, nil
			}
		}
	}
	b.Children(func() {
		panelLabel(c, nil, loc(c).Done(), 12, 600).TextColor(ink)
	})
	return b
}

// pickerGroups is the catalogue: action cards, then widgets, minus the
// ones already on the page or on the desktop.
func (p *toolsPage) pickerGroups() (groups [][]toolCardDef) {
	var actions, widgets []toolCardDef
	for _, def := range toolCatalog() {
		// The catalogue shows the rectangle a pick adds, so a def that
		// leaves rows at zero (the action cards' one cell) reads 1 × 1
		// rather than “N × 0”.
		if def.rows < 1 {
			def.rows = 1
		}
		if !def.available(p.host) || p.placed(def.id) {
			continue
		}
		if def.kind == cardWidget {
			widgets = append(widgets, def)
		} else {
			actions = append(actions, def)
		}
	}
	if len(actions) > 0 {
		groups = append(groups, actions)
	}
	if len(widgets) > 0 {
		groups = append(groups, widgets)
	}
	return
}

// placed reports whether a card is in the grid or pinned to the desktop.
func (p *toolsPage) placed(id string) bool {
	for _, c := range p.host.toolsLayout().Cards {
		if c.ID == id && !p.host.inactiveWidget(id) {
			return true
		}
	}
	for _, pin := range p.host.pins {
		if pin.cardID == id {
			return true
		}
	}
	return false
}

// pickable reports whether anything is left to add.
func (p *toolsPage) pickable() bool {
	for _, g := range p.pickerGroups() {
		if len(g) > 0 {
			return true
		}
	}
	return false
}

// hasFreeCell reports whether the dashed + tile has a cell on the grid.
func (p *toolsPage) hasFreeCell() bool {
	cards := p.host.toolsLayout().Cards
	return len(packToolCards(cards, p.host.inactiveWidget, true)) >
		len(packToolCards(cards, p.host.inactiveWidget, false))
}

// body is the fixed content box: the catalogue while picking, else the
// card grid.
func (p *toolsPage) body(c *ui.Context, _ bool, live bool) ui.Element {
	a := p.host
	if p.picking {
		return p.pickerBody(c, live)
	}
	l := a.toolsLayout()
	editing := p.editing
	d := p.press
	// The dragged card's slot is virtual: the list it would have if the
	// pointer released now.
	cards := l.Cards
	if d != nil && editing && (d.dragging || d.settling) {
		if ins := toolInsertIndex(l.Cards, d.id, d.index, d.px, d.py); ins >= 0 {
			d.index = ins
			cards = toolVirtualCards(l.Cards, d.id, ins)
		}
	}
	cells := packToolCards(cards, a.inactiveWidget, editing)
	contentH := float32(0)
	for _, cell := range cells {
		contentH = max(contentH, cell.y+cell.h)
	}
	return ui.Column(c.Key("tools-stage")).Grow(1).MinHeight(0).Children(func() {
		scroll := ui.Scroll(c.Key("cards")).Grow(1).MinHeight(0).TrackScroll(&p.scroll)
		p.viewport = scroll.Bounds()
		// Pointer tracking makes the stage interactive and reports the
		// viewport-local pointer for the press bookkeeping.
		vpx, vpy, _ := scroll.PointerPosition()
		if d != nil && !d.settling {
			d.px, d.py = vpx, vpy+p.scroll.Y
		}
		scroll.Children(func() {
			// A box in normal flow carries the grid's real height: an
			// absolute child of the scroll itself counts toward neither
			// the scrollable content nor the offset, so a grid past the
			// second row could neither scroll nor be reached.
			ui.Box(c.Key("cards-stack")).Width(toolStageW).Height(contentH).Children(func() {
				for _, cell := range cells {
					// The grid never blurs: the cards are the content, and
					// a row cut by the viewport stays as sharp as the rest
					// (the tray and clipboard pages keep the edge melt).
					p.toolCell(c, cell, editing, live)
				}
				if d != nil && d.dragging && p.preview.window() == nil {
					p.toolLiftedCard(c, *d)
				}
			})
		})
	})
}

// toolCell draws one packed cell: the + tile, a parked card (in-flow,
// possibly mid-reorder), or the card itself.
func (p *toolsPage) toolCell(c *ui.Context, cell toolCell, editing, live bool) {
	a := p.host
	if cell.plus {
		plus := ui.Column(c.Key("tc-plus")).Absolute().Left(cell.x).Top(cell.y).
			Width(cell.w).Height(cell.h).Radius(rCard).Center().
			BorderWidth(1.5).BorderColor(ui.RGBA(255, 255, 255, 0.25)).
			BorderStyle(ui.BorderDashed).Label(loc(c).ToolsAddCard())
		if live {
			plus.HandleInput(func(ev ui.InputEvent) bool {
				if ev.Kind == ui.InputPointerUp && p.editing {
					p.picking = true
					return true
				}
				return false
			})
			plus.PointerPosition()
		}
		plus.Children(func() {
			ui.Icon(c, plusSymbol).Size(22, 22).TextColor(ink3)
		})
		return
	}
	spec := layout.Card{ID: cell.id, Span: 1, Rows: 1}
	if def := toolCardDefFor(cell.id); def != nil {
		spec.Span, spec.Rows = def.span, def.rows
	}
	for _, cc := range a.toolsLayout().Cards {
		if cc.ID == cell.id {
			spec.Span, spec.Rows = cc.Cols(), cc.Height()
		}
	}
	d := p.press
	parked := d != nil && d.id == cell.id && (d.dragging || d.settling)
	card := a.toolCard(c, spec, surfFor(editing))
	switch {
	case parked && d.settling:
		p.toolSettle(card, d)
		card.Absolute().Width(cell.w).Height(cell.h)
	case parked:
		// The parked element stays the pointer's press target; its
		// animated slot position converts card-local events to stage
		// coordinates.
		cx := card.AnimateTo("cx", cell.x, ui.Snappy)
		cy := card.AnimateTo("cy", cell.y, ui.Snappy)
		d.cardX, d.cardY = cx, cy
		card.Absolute().Left(cx).Top(cy).Width(cell.w).Height(cell.h).Opacity(0)
	case editing:
		card.Absolute().Width(cell.w).Height(cell.h)
		card.Left(card.AnimateTo("cx", cell.x, ui.Snappy)).
			Top(card.AnimateTo("cy", cell.y, ui.Snappy))
		p.toolEditChrome(c, card, cell, spec, live)
	default:
		card.Absolute().Left(cell.x).Top(cell.y).Width(cell.w).Height(cell.h)
	}
	if live {
		p.toolCardInput(c, card, cell, editing, live)
	}
	if !parked {
		p.toolCardWobble(c, card, cell, editing)
	}
}

// surfFor picks the card surface: glyph-only while editing.
func surfFor(editing bool) toolCardSurface {
	if editing {
		return cardEdit
	}
	return cardPanel
}

// toolLiftedCard draws the dragged copy above the grid at the pointer.
// It takes no input: the parked card keeps the press so release always
// lands on the same element the down did.
func (p *toolsPage) toolLiftedCard(c *ui.Context, d toolPress) {
	a := p.host
	spec := p.dragSpec(d.id)
	card := a.toolCard(c, spec, cardEdit, "lift")
	card.Absolute().Left(d.px-d.grabDX).Top(d.py-d.grabDY).
		Width(toolColW(spec.Cols())).Height(toolRowH(spec.Height())).
		Scale(1.06, 1.06).TransformOrigin(0.5, 0.5).PassThrough()
}

// toolCardInput wires the pointer: down starts a press, move past the
// slop reorders in edit or kills the click outside it, up drops or lands,
// and the long-press deadline turns editing on.
func (p *toolsPage) toolCardInput(c *ui.Context, card ui.Element, cell toolCell, editing, live bool) {
	a := p.host
	card.PointerPosition() // interactive: a hit-test and tracking target
	card.HandleInput(func(ev ui.InputEvent) bool {
		switch ev.Kind {
		case ui.InputPointerDown:
			if p.press != nil {
				return false
			}
			d := &toolPress{
				id: cell.id, downAt: ev.Time,
				downX: cell.x + ev.X, downY: cell.y + ev.Y,
				px: cell.x + ev.X, py: cell.y + ev.Y,
				grabDX: ev.X, grabDY: ev.Y,
				cardX: cell.x, cardY: cell.y,
			}
			p.press = d
			c.AnimationFrame() // the long-press deadline needs frames
			return true
		case ui.InputPointerMove:
			defer p.syncDragPreview(p.toolPointer(ev))
			d := p.press
			if d == nil || d.id != cell.id || d.settling {
				return d != nil && d.id == cell.id && d.settling
			}
			d.px, d.py = d.cardX+ev.X, d.cardY+ev.Y
			if d.dragging {
				if ins := toolInsertIndex(a.toolsLayout().Cards, d.id, d.index, d.px, d.py); ins >= 0 {
					d.index = ins
				}
				return true
			}
			if hypot(d.px-d.downX, d.py-d.downY) <= toolDragSlop {
				return true
			}
			if p.editing {
				d.dragging = true
				d.index = toolInsertIndex(a.toolsLayout().Cards, d.id, -1, d.px, d.py)
				if d.index < 0 {
					d.index = len(a.toolsLayout().Cards) - 1
				}
			} else {
				d.suppress = true
			}
			return true
		case ui.InputPointerUp:
			d := p.press
			if d == nil || d.id != cell.id {
				return false
			}
			if d.settling {
				return true
			}
			if d.dragging {
				d.px, d.py = d.cardX+ev.X, d.cardY+ev.Y
				p.toolCardDrop(d, p.toolPointer(ev))
				return true
			}
			p.skipClick = d.suppress // a moved press never becomes a tap
			p.press = nil
			return true
		}
		return false
	})
	// The long-press deadline is checked at build time: a still press asks
	// for every frame until it crosses the slop or editing starts, so the
	// deadline fires on the frame clock even when nothing else animates.
	if d := p.press; d != nil && d.id == cell.id && !d.dragging && !d.settling && !d.suppress && !p.editing {
		c.AnimationFrame()
		if c.Now().Sub(d.downAt) >= durLongPress {
			p.editing, p.editAt = true, c.Now()
			d.suppress = true // this press only entered edit; no click, no drag yet
		}
	}
	if !editing && live && card.Clicked() {
		if p.skipClick {
			p.skipClick = false
		} else {
			p.toolCardTapped(cell.id)
		}
	}
}

// toolCardTapped runs the card's own action: an action card works on the
// tray's selection (design §3: 对托盘页当前选中的项目执行), a widget card
// does what its whole-card tap has always meant (keep-awake toggles, the
// clipboard card copies its newest capture, AI 用量 opens its page) —
// the music card opens the now-playing page: its transport buttons and
// rails own their presses, and a tap beside them is never a play toggle.
// A card with nothing to act on jiggles with the hint.
func (p *toolsPage) toolCardTapped(id string) {
	a := p.host
	if p.editing {
		return
	}
	if def := toolCardDefFor(id); def != nil && def.kind == cardWidget && def.id == "playing" {
		// The transport and the rails own their own presses; a tap on the
		// card itself opens the full player, the page's second entrance
		// beside the overview's track row.
		a.openPlaying()
		return
	}
	if a.runToolCard(id) {
		return
	}
	p.jiggleID, p.jiggleAt = id, time.Now()
	a.toolCardTapFeedback()
}

// toolCardTapFeedback is shared by tools-page and desktop-card taps.
func (a *shelf) toolCardTapFeedback() {
	if a.conv != nil && a.conv.busy() {
		a.toastStatus(a.appLoc().OneJobAtATime(), time.Now().Add(2*time.Second), statusInfo)
		return
	}
	a.toastStatus(a.appLoc().DropFilesHere(), time.Now().Add(2*time.Second), statusInfo)
}

func hypot(dx, dy float32) float64 {
	return math.Hypot(float64(dx), float64(dy))
}

// toolCardDrop ends a reorder: inside the stage it commits the index,
// outside the panel it pins the card to the desktop (design §2 把卡片拖出
// 面板边缘 = 固定到桌面), elsewhere it flies back.
func (p *toolsPage) toolCardDrop(d *toolPress, cursor mygo.Point) {
	defer p.closeDragPreview()
	a := p.host
	// The drop lands inside the stage only when the release is inside the
	// viewport the scroll shows right now: a card dragged below the visible
	// rows is on its way to the desktop, not to a slot further down the
	// grid it cannot see.
	viewH := p.viewport.H
	if viewH <= 0 {
		viewH = contentH
	}
	inside := d.px >= 0 && d.px <= toolStageW &&
		d.py >= p.scroll.Y-0.5 && d.py <= p.scroll.Y+viewH+0.5
	target := toolCell{id: d.id, x: d.cardX, y: d.cardY}
	if inside {
		if ins := d.index; ins >= 0 {
			a.mutateTools(func(l *layout.Layout) { l.MoveCard(d.id, ins) })
			// The committed cell is where the settle flight aims.
			for _, cell := range packToolCards(a.toolsLayout().Cards, a.inactiveWidget, true) {
				if cell.id == d.id {
					target = cell
				}
			}
		}
	} else {
		// A release beyond the panel's drawn body pins the card at the
		// cursor (grab point preserved so the window lands under it);
		// a release on another pane of the panel still flies back.
		panel := a.panelDropRect()
		if !insideRect(cursor, panel) {
			spec := p.dragSpec(d.id)
			span, w := spec.Cols(), pinCardWidth(d.id, spec.Cols())
			disp := displayIDAt(cursor, 0)
			area := pinArea(disp)
			edge := pinReleaseEdge(cursor, area)
			frame := pinFrameUnder(cursor, d.grabDX, d.grabDY, toolColW(spec.Cols()), toolRowH(spec.Height()), w)
			if edge != pinFree {
				frame = pinStripFrame(edge, area, cursor.Y) // the 24 pt edge rule, same as pin drags
			}
			var committed bool
			if it := p.preview.take(); it != nil {
				it.display, it.edge, it.expanded, it.frame = disp, edge, edge == pinFree, frame
				committed = a.commitPin(it)
				if !committed {
					if it.win != nil {
						it.win.Hide()
						it.win.Destroy()
						it.win = nil
					}
				}
			} else {
				committed = a.addPin(d.id, span, disp, edge, false, frame) != nil
			}
			if committed {
				p.press = nil // the parked cell is gone: no settle back
				return
			}
			// The failed write kept the grid unchanged; fly back.
		}
	}
	d.settling, d.dragging = true, false
	d.settleX, d.settleY = target.x, target.y
}

// toolSettle animates the released card from the pointer to its cell.
// The press clears once the flight rests: Settled, not Arrived — Arrived
// reports only the frame a moving value comes to rest, so a flight that
// starts at its goal (an edit drag released back where it began, index
// unchanged) never moves and never arrives, and the press would stick.
func (p *toolsPage) toolSettle(card ui.Element, d *toolPress) {
	if !d.settleJump {
		card.Jump("cx", d.px-d.grabDX)
		card.Jump("cy", d.py-d.grabDY)
		card.Jump("sc", 1.06)
		d.settleJump = true
	}
	cx := card.AnimateTo("cx", d.settleX, ui.Animation{Spring: ui.SpringOptions{Duration: durSettle, Bounce: 0.12}})
	cy := card.AnimateTo("cy", d.settleY, ui.Animation{Spring: ui.SpringOptions{Duration: durSettle, Bounce: 0.12}})
	sc := card.AnimateTo("sc", 1, ui.Animation{Spring: ui.SpringOptions{Duration: durSettle, Bounce: 0.12}})
	card.Left(cx).Top(cy).Scale(sc, sc).TransformOrigin(0.5, 0.5)
	d.cardX, d.cardY = cx, cy
	if card.Settled("cx") && card.Settled("cy") && card.Settled("sc") {
		p.press = nil
	}
}

// toolEditChrome is the editing overlay on one card: the ⊖ badge at the
// top left, and — for a card that has sizes to choose from — the handle at
// the bottom right.
func (p *toolsPage) toolEditChrome(c *ui.Context, card ui.Element, cell toolCell, spec layout.Card, live bool) {
	a := p.host
	card.Children(func() {
		minus := ui.ButtonBase(c.Key("minus-"+cell.id)).Absolute().Left(2).Top(2).Size(18, 18).
			Radius(9).Center().Background(surfaceBadge).Border(1.5, surface).
			Label(loc(c).Remove()).Tooltip(loc(c).Remove())
		minus.Children(func() {
			ui.Icon(c, minusSymbol).Size(8, 8).TextColor(ink)
		})
		if live {
			minus.HandleInput(func(ev ui.InputEvent) bool {
				if ev.Kind == ui.InputPointerUp {
					if a.mutateToolsChanged(func(l *layout.Layout) bool {
						l.RemoveCard(cell.id)
						return true
					}) {
						p.press = nil
					}
				}
				return true // swallow the down too, so no card press starts
			})
			minus.PointerPosition()
		}
		if len(cardSizes(cell.id)) > 1 {
			next := cardSizes(cell.id)[0]
			handle := ui.ButtonBase(c.Key("size-"+cell.id)).Absolute().Right(5).Bottom(5).Size(12, 12).
				Center().Label(loc(c).CellSize(next[0], next[1])).Tooltip(loc(c).CellSize(next[0], next[1]))
			handle.Children(func() {
				ui.Icon(c, spanSymbol).Size(10, 10).TextColor(ink2)
			})
			if live {
				handle.HandleInput(func(ev ui.InputEvent) bool {
					if ev.Kind == ui.InputPointerUp {
						p.cycleSize(spec)
					}
					return true
				})
				handle.PointerPosition()
			}
		}
	})
}

// cardSizes are the rectangles a card cycles through with its handle, the
// card's current one first.
func cardSizes(id string) [][2]int {
	if def := toolCardDefFor(id); def != nil && def.kind == cardWidget {
		switch id {
		case "playing":
			return [][2]int{{2, 2}, {2, 1}, {1, 1}}
		case "awake":
			return [][2]int{{1, 1}, {2, 1}, {2, 2}}
		case "usage":
			return [][2]int{{1, 1}, {2, 1}}
		case "recent", "clip":
			return [][2]int{{2, 1}, {2, 2}, {1, 1}}
		}
	}
	return [][2]int{{1, 1}}
}

// cycleSize moves a card to the next rectangle after its current one.
func (p *toolsPage) cycleSize(spec layout.Card) {
	sizes := cardSizes(spec.ID)
	next := sizes[0]
	for i, s := range sizes {
		if s[0] == spec.Cols() && s[1] == spec.Height() {
			next = sizes[(i+1)%len(sizes)]
			break
		}
	}
	p.host.mutateToolsChanged(func(l *layout.Layout) bool {
		l.SetCardSize(spec.ID, next[0], next[1])
		return true
	})
}

// toolCardWobble is the editing wobble: ±0.7° on a 520ms period, odd
// columns in counter-phase; still under Reduce Motion.
func (p *toolsPage) toolCardWobble(c *ui.Context, card ui.Element, cell toolCell, editing bool) {
	if !editing || reduceMotion(c) {
		return
	}
	col := int(math.Round(float64(cell.x / (toolColW(1) + toolCellGap))))
	phase := 0.0
	if col%2 == 1 {
		phase = math.Pi
	}
	k := card.Loop("wobble", durWobble, ui.Linear)
	angle := toolWobbleDeg * math.Sin(2*math.Pi*float64(k)+phase)
	card.Rotate(float32(angle)).TransformOrigin(0.5, 0.5)
	c.AnimationFrame()
}

// pickerBody is the catalogue (design §3): a raised card holding two
// columns of rows — action cards, then widgets — with the default size on
// the right. Picking a row places it at the first free rectangle.
func (p *toolsPage) pickerBody(c *ui.Context, live bool) ui.Element {
	groups := p.pickerGroups()
	body := ui.Column(c.Key("picker")).FillWidth().FillHeight()
	body.Children(func() {
		// The list scrolls inside the card: with every card removed the
		// catalogue is taller than the 174pt box, and a clipped row could
		// never be picked.
		scroll := ui.Scroll(c.Key("picker-scroll")).FillWidth().Grow(1).MinHeight(0)
		scroll.Children(func() {
			card := ui.Column(c.Key("picker-card")).FillWidth().Background(surfaceRaise).
				Radius(rCard).Padding(10, 12, 12, 12).Gap(4)
			card.Children(func() {
				panelLabel(c, nil, loc(c).ToolsCardCatalog(), typeSub, 600).TextColor(ink3)
				if len(groups) == 0 {
					panelLabel(c, nil, loc(c).ToolsAllPlaced(), typeCallout, 0).TextColor(ink3)
					return
				}
				for i, group := range groups {
					if i > 0 {
						ui.Box(c).Height(4)
					}
					ui.Grid(c.Key(fmt.Sprintf("pick-group-%d", i))).
						ColumnTracks(ui.Fr(1), ui.Fr(1)).Gap(4).FillWidth().Children(func() {
						for _, def := range group {
							p.pickRow(c, def, live)
						}
					})
				}
			})
		})
	})
	return body
}

// pickRow is one catalogue row: glyph, name and the default size.
func (p *toolsPage) pickRow(c *ui.Context, def toolCardDef, live bool) ui.Element {
	a := p.host
	row := ui.Row(c.Key("pick-"+def.id)).Height(toolPickRowH).FillWidth().Gap(8).
		Padding(0, 6, 0, 6).Radius(rRow).AlignItems(ui.Center).Label(def.name(loc(c)))
	if row.Hovered() {
		row.Background(fill1)
	}
	row.Children(func() {
		ui.Icon(c, def.icon).Size(15, 15).TextColor(ink2)
		panelLabel(c, nil, def.name(loc(c)), typeCallout, 0).TextColor(ink).Grow(1).MinWidth(0)
		ui.Text(c, loc(c).CellSize(def.span, def.rows)).SingleLine().FontSize(typeMini).TextColor(ink3)
	})
	if live && row.Clicked() {
		a.mutateToolsChanged(func(l *layout.Layout) bool {
			l.AddCardSize(def.id, def.span, def.rows)
			return true
		})
		p.picking = false
	}
	return row
}
