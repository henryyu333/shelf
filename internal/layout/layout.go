// Package layout persists the tool page's card arrangement, the home
// canvas' placed widgets, and the desktop pins in layout.json under the
// data directory. The file is versioned, written atomically (temporary
// file + rename, like settings.json), and a corrupt or unknown-version
// file falls back to the default layout without being overwritten, so a
// damaged file never eats the user's arrangement. A card id appears at
// most once in the whole layout — on the home canvas, the tool page, or
// pinned to the desktop, never twice (design draft §6「固定项数量」).
package layout

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"slices"
)

// FileName is the file's name inside the data directory.
const FileName = "layout.json"

// Version is the file schema version Load writes and accepts; files
// stamped with an older version migrate in memory.
const Version = 2

const (
	// GridCols is the home canvas' width in cells.
	GridCols = 4
	// GridRows is the home canvas' height in cells.
	GridRows = 2
)

// Edge is the screen edge a pinned card sticks to; EdgeNone floats free
// and is drawn always expanded.
type Edge string

const (
	EdgeNone  Edge = "none"
	EdgeLeft  Edge = "left"
	EdgeRight Edge = "right"
)

// Card is one entry of the tool page grid: a catalog card id, the columns
// it spans (1 or 2) and the rows it spans (1 or 2). Action cards are 1 × 1;
// widgets take the rectangle they had on the old home canvas.
type Card struct {
	ID   string `json:"id"`
	Span int    `json:"span,omitempty"` // omitted means 1
	Rows int    `json:"rows,omitempty"` // omitted means 1
}

// Cols is the card's column span, clamped to 1 or 2.
func (c Card) Cols() int { return clampSpan(c.Span) }

// Height is the card's row span, clamped to 1 or 2.
func (c Card) Height() int { return clampRows(c.Rows) }

// Widget is one widget placed on the home canvas: the widget id and the
// rectangle it covers, in cells. X and Y address the top-left cell; W
// and H are the cell counts, each 1 or 2.
type Widget struct {
	ID string `json:"id"`
	X  int    `json:"x"`
	Y  int    `json:"y"`
	W  int    `json:"w"`
	H  int    `json:"h"`
}

// Pin is one card floating on the desktop (task B): the card, the
// column span it keeps on return to the grid, the display it lives
// on, the screen edge it sticks to (empty edge floats free), its
// position in the display's visible frame, and whether it stays
// expanded instead of folding into the edge strip.
//
// HomeX/HomeY/HomeW/HomeH remember the home-canvas rect a widget pin came
// from, so 放回刘海 puts it back in its cell after a restart. They are
// absent (HomeW 0) for action cards and for files written by builds that
// did not record the rect; both fall back to the catalogue placement.
type Pin struct {
	ID         string `json:"id"`
	Span       int    `json:"span,omitempty"`       // column span it keeps when put back on the tool page
	Display    string `json:"display,omitempty"`    // display identifier the pin was last seen on; empty = main
	Edge       Edge   `json:"edge,omitempty"`       // EdgeLeft / EdgeRight collapse at that edge
	X          int    `json:"x,omitempty"`          // left offset inside the display's visible frame
	Y          int    `json:"y,omitempty"`          // top offset inside the display's visible frame
	AlwaysOpen bool   `json:"alwaysOpen,omitempty"` // never folds into the edge strip
	Rows       int    `json:"rows,omitempty"`       // row span the card keeps when put back on the tool page

	// Legacy: the home canvas a widget pin came from (builds before the
	// tools page took the widgets). Read and ignored; never written.
	HomeX int `json:"homeX,omitempty"`
	HomeY int `json:"homeY,omitempty"`
	HomeW int `json:"homeW,omitempty"`
	HomeH int `json:"homeH,omitempty"`
}

// Layout is the persisted arrangement: the tools page's card grid (action
// cards and widgets together) and the pinned desktop cards.
type Layout struct {
	Version int      `json:"version"`
	Cards   []Card   `json:"cards,omitempty"`
	Home    []Widget `json:"home,omitempty"`
	Pins    []Pin    `json:"pins,omitempty"`

	// Inactive names widgets that are in the file but take no space right
	// now (a widget whose feature is switched off). Geometry skips them, so
	// their cells read as free; the entries themselves stay in the file.
	// Runtime only: never written, and nil means every widget is active.
	Inactive func(id string) bool `json:"-"`
}

// widgetCatalog lists the home widgets and the rectangle each starts at.
// Migration reads it to place a version 1 widget card, AddWidgetBest to
// try the natural spot first.
var widgetCatalog = []Widget{
	{ID: "playing", X: 0, Y: 0, W: 2, H: 2},
	{ID: "awake", X: 2, Y: 0, W: 1, H: 1},
	{ID: "usage", X: 3, Y: 0, W: 1, H: 1},
	{ID: "recent", X: 2, Y: 1, W: 2, H: 1},
	{ID: "clip", X: 2, Y: 1, W: 2, H: 1},
}

// defaultWidget returns id's catalogue rectangle.
func defaultWidget(id string) (Widget, bool) {
	for _, w := range widgetCatalog {
		if w.ID == id {
			return w, true
		}
	}
	return Widget{}, false
}

// defaultCards is the tool page's action cards, in the design's order.
func defaultCards() []Card {
	return []Card{
		{ID: "ocr", Span: 1},
		{ID: "fit1m", Span: 1},
		{ID: "topdf", Span: 1},
		{ID: "zip", Span: 1},
		{ID: "tojpeg", Span: 1},
		{ID: "toword", Span: 1},
		{ID: "extract", Span: 1},
		{ID: "resize", Span: 1},
	}
}

// Default returns the arrangement a missing or broken file starts from:
// the actions on the tool page and the play widget beside the awake,
// usage and recent widgets on the home canvas.
func Default() Layout {
	return DefaultWith(true)
}

// DefaultWith is Default for a build whose AI usage widget is available
// or not. The grid holds the widgets first (the music player's 2 × 2
// rectangle, then 1 × 1 cards) and the action cards after them; a widget
// whose feature is off takes no space at run time but keeps its entry.
func DefaultWith(aiUsage bool) Layout {
	l := Layout{
		Version: Version,
		Cards: []Card{
			{ID: "playing", Span: 2, Rows: 2},
			{ID: "awake", Span: 1, Rows: 1},
			{ID: "usage", Span: 1, Rows: 1},
			{ID: "recent", Span: 2, Rows: 1},
			{ID: "clip", Span: 2, Rows: 1},
		},
	}
	_ = aiUsage
	l.Cards = append(l.Cards, defaultCards()...)
	return l
}

// Load reads layout.json under dir, creating it with the default layout
// when missing. A corrupt file (bad JSON, an unknown version, or shape
// violations that validation cannot repair) yields the default layout and
// an error; the file is left alone for the user to inspect. A version 0
// or 1 file migrates in memory and is not written back.
func Load(dir string) (Layout, error) {
	return load(dir, Default)
}

// LoadWith is Load with DefaultWith's layout as the fallback: a missing
// file is created with it, a broken one falls back to it.
func LoadWith(dir string, aiUsage bool) (Layout, error) {
	return load(dir, func() Layout { return DefaultWith(aiUsage) })
}

func load(dir string, def func() Layout) (Layout, error) {
	data, err := os.ReadFile(filepath.Join(dir, FileName))
	switch {
	case errors.Is(err, fs.ErrNotExist):
		l := def()
		if werr := Write(dir, l); werr != nil {
			return l, fmt.Errorf("layout: creating %s: %w", FileName, werr)
		}
		return l, nil
	case err != nil:
		return def(), fmt.Errorf("layout: reading %s: %w", FileName, err)
	}
	var l Layout
	if err := json.Unmarshal(data, &l); err != nil {
		return def(), fmt.Errorf("layout: %s is not valid JSON, using defaults: %w", FileName, err)
	}
	if l.Version > Version || l.Version < 0 {
		// A future schema is rejected before any repair: the defaults
		// must not overwrite a file this build cannot read.
		return def(), fmt.Errorf("layout: %s version %d unknown, using defaults", FileName, l.Version)
	}
	if l.Version < Version {
		l.migrate()
	}
	l.normalize()
	// Version 2 files written before the tools page took the widgets
	// carry them under "home"; the canvas is gone, so they move into the
	// grid in memory. The file itself is written only on the next gesture.
	l.mergeHome()
	return l, nil
}

// Write saves l atomically: a temporary file in dir renamed over the
// target, so a crash never leaves a half-written layout.json.
func Write(dir string, l Layout) error {
	data, err := json.MarshalIndent(l, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	tmp, err := os.CreateTemp(dir, "."+FileName+".tmp-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName) // no-op after a successful rename
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, filepath.Join(dir, FileName))
}

// normalize repairs the recoverable parts of a loaded file (version 2):
// a card id that shows up more than once — in the grid, in the legacy home
// list, or among the pins — keeps only its first place (design §6: one
// card, one place). Card spans clamp to 1 or 2; pins for unknown edges
// reset to the main display floating free. The legacy home list is only an
// input for mergeHome: it keeps the entries as they came, with empty ids
// and duplicates dropped.
func (l *Layout) normalize() {
	seen := map[string]bool{}
	home := l.Home[:0]
	for _, w := range l.Home {
		if w.ID == "" || seen[w.ID] {
			continue
		}
		w.W = clampCells(w.W)
		w.H = clampCells(w.H)
		seen[w.ID] = true
		home = append(home, w)
	}
	l.Home = home
	cards := l.Cards[:0]
	for _, card := range l.Cards {
		if card.ID == "" || seen[card.ID] {
			continue
		}
		card.Span = clampSpan(card.Span)
		card.Rows = clampRows(card.Rows)
		seen[card.ID] = true
		cards = append(cards, card)
	}
	l.Cards = cards
	pins := l.Pins[:0]
	for _, pin := range l.Pins {
		if pin.ID == "" || seen[pin.ID] {
			continue
		}
		switch pin.Edge {
		case "", EdgeLeft, EdgeRight:
		default:
			pin.Edge = ""
		}
		seen[pin.ID] = true
		pins = append(pins, pin)
	}
	l.Pins = pins
}

// migrate upgrades a version 0 (pre-stamp) or 1 file in memory: a card
// whose id is a known widget keeps the rectangle the old home canvas gave
// it, as a grid size; every other card stays in its original order with
// its span clamped to 1 or 2. Pins are untouched here — normalize then
// applies the one-card-one-place rule. The file itself is not rewritten.
func (l *Layout) migrate() {
	cards := make([]Card, 0, len(l.Cards))
	var widgets []Card
	seen := map[string]bool{}
	for _, card := range l.Cards {
		if card.ID == "" || seen[card.ID] {
			continue
		}
		seen[card.ID] = true
		if w, ok := defaultWidget(card.ID); ok {
			widgets = append(widgets, Card{ID: w.ID, Span: clampSpan(w.W), Rows: clampRows(w.H)})
			continue
		}
		cards = append(cards, Card{ID: card.ID, Span: clampSpan(card.Span)})
	}
	// Widgets keep the canvas' reading order, ahead of the actions.
	slices.SortFunc(widgets, func(a, b Card) int {
		wa, _ := defaultWidget(a.ID)
		wb, _ := defaultWidget(b.ID)
		if wa.Y != wb.Y {
			return wa.Y - wb.Y
		}
		return wa.X - wb.X
	})
	l.Cards = append(widgets, cards...)
	l.Home = nil
	l.Version = Version
}

// index is the grid position of card id, or -1.
func (l *Layout) index(id string) int {
	return slices.IndexFunc(l.Cards, func(c Card) bool { return c.ID == id })
}

// card is the card with id, and whether the grid has it.
func (l *Layout) card(id string) (Card, bool) {
	if i := l.index(id); i >= 0 {
		return l.Cards[i], true
	}
	return Card{}, false
}

// hasCard reports whether id is placed anywhere: on the home canvas, on
// the tool page, or pinned to the desktop.
func (l *Layout) hasCard(id string) bool {
	if id == "" {
		return false
	}
	if l.index(id) >= 0 {
		return true
	}
	return slices.ContainsFunc(l.Pins, func(p Pin) bool { return p.ID == id })
}

// AddCard appends id to the end of the tool page grid with span clamped
// to 1 or 2; it is a no-op when the card is already placed anywhere
// (home canvas, grid, or desktop), the one-card-one-place rule.
func (l *Layout) AddCard(id string, span int) {
	l.AddCardSize(id, span, 1)
}

// AddCardSize places id at the end of the grid with a column and row span.
func (l *Layout) AddCardSize(id string, span, rows int) {
	if id == "" || l.hasCard(id) {
		return
	}
	l.Cards = append(l.Cards, Card{ID: id, Span: clampSpan(span), Rows: clampRows(rows)})
}

// SetCardSize switches id's rectangle; absent cards and out-of-range sizes
// do nothing.
func (l *Layout) SetCardSize(id string, span, rows int) {
	i := l.index(id)
	if i < 0 || span < 1 || span > 2 || rows < 1 || rows > 2 {
		return
	}
	l.Cards[i].Span, l.Cards[i].Rows = span, rows
}

// mergeHome folds a pre-unification file's home-canvas widgets into the
// grid, in their old reading order (row, then column), ahead of the action
// cards; an id already in the grid keeps its place there.
func (l *Layout) mergeHome() {
	if len(l.Home) == 0 {
		return
	}
	home := slices.Clone(l.Home)
	slices.SortFunc(home, func(a, b Widget) int {
		if a.Y != b.Y {
			return a.Y - b.Y
		}
		return a.X - b.X
	})
	cards := make([]Card, 0, len(home)+len(l.Cards))
	seen := make(map[string]bool, len(home)+len(l.Cards))
	for _, w := range home {
		if seen[w.ID] {
			continue
		}
		seen[w.ID] = true
		cards = append(cards, Card{ID: w.ID, Span: clampSpan(w.W), Rows: clampRows(w.H)})
	}
	for _, c := range l.Cards {
		if seen[c.ID] {
			continue
		}
		seen[c.ID] = true
		cards = append(cards, c)
	}
	l.Cards, l.Home = cards, nil
}

// RemoveCard takes id off the tool page grid; a home widget or desktop
// pin is not touched — use RemoveWidget or RemovePin for those.
func (l *Layout) RemoveCard(id string) {
	if i := l.index(id); i >= 0 {
		l.Cards = slices.Delete(l.Cards, i, i+1)
	}
}

// SetSpan switches id's column span between 1 and 2; absent cards and
// out-of-range spans do nothing.
func (l *Layout) SetSpan(id string, span int) {
	i := l.index(id)
	if i < 0 || span < 1 || span > 2 {
		return
	}
	l.Cards[i].Span = span
}

// MoveCard reorders id to before position to: the index it inserts at
// counting the list without it (n cards means to == n appends). A
// negative to clamps to 0, an over-the-end one to len.
func (l *Layout) MoveCard(id string, to int) {
	i := l.index(id)
	if i < 0 {
		return
	}
	card := l.Cards[i]
	l.Cards = slices.Delete(l.Cards, i, i+1)
	to = max(0, min(to, len(l.Cards)))
	l.Cards = slices.Insert(l.Cards, to, card)
}

// AddPin places id on the desktop as pin; it leaves the tools grid first
// (a card lives in exactly one place). A card already pinned keeps its
// placement.
func (l *Layout) AddPin(pin Pin) {
	if pin.ID == "" {
		return
	}
	if slices.ContainsFunc(l.Pins, func(p Pin) bool { return p.ID == pin.ID }) {
		return
	}
	l.RemoveCard(pin.ID)
	switch pin.Edge {
	case "", EdgeLeft, EdgeRight:
	default:
		pin.Edge = ""
	}
	l.Pins = append(l.Pins, pin)
}

// RemovePin takes id's pin off the desktop; when putBack is true the
// card goes back to the tool page grid — at before, or appended when
// before is out of range (design §4: 「放回刘海」回到原位置，被占就放末尾).
func (l *Layout) RemovePin(id string, putBack bool, before int) {
	i := slices.IndexFunc(l.Pins, func(p Pin) bool { return p.ID == id })
	if i < 0 {
		return
	}
	pin := l.Pins[i]
	l.Pins = slices.Delete(l.Pins, i, i+1)
	if !putBack || l.hasCard(pin.ID) {
		return
	}
	card := Card{ID: pin.ID, Span: clampSpan(pin.Span), Rows: clampRows(pin.Rows)}
	before = max(0, min(before, len(l.Cards)))
	l.Cards = slices.Insert(l.Cards, before, card)
}

func clampRows(rows int) int {
	if rows < 1 {
		return 1
	}
	if rows > 2 {
		return 2
	}
	return rows
}

func clampSpan(span int) int {
	if span < 1 || span > 2 {
		return 1
	}
	return span
}

// clampCells confines a widget side to the 1..2 cells a widget may span.
func clampCells(n int) int {
	return max(1, min(n, 2))
}

// LoadQuiet is Load with the error logged instead of returned, for the
// app shell's startup path: a broken file falls back to defaults and the
// user still gets a working page.
func LoadQuiet(dir string) Layout {
	l, err := Load(dir)
	if err != nil {
		log.Print(err)
	}
	return l
}

// LoadQuietWith is LoadWith with the error logged instead of returned.
func LoadQuietWith(dir string, aiUsage bool) Layout {
	l, err := LoadWith(dir, aiUsage)
	if err != nil {
		log.Print(err)
	}
	return l
}
