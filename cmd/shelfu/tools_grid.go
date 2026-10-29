//go:build darwin

package main

// Pure grid math for the tools page: pack the card list into 4 columns,
// find the insertion index a dragged card's pointer means, and build the
// virtual list a drop previews. Split from tools_page.go so tests can
// exercise it without a view.
//
// Cards carry a column span and a row span: action cards take one cell,
// the widgets keep the rectangles they had on the old home canvas (the
// music player is 2 × 2). Packing is row-major first-fit over a grid that
// grows downward, so the page scrolls once the cards pass two rows.

import (
	"math"

	"shelf/internal/layout"
)

// toolColW is a card's pixel width for its column span.
func toolColW(span int) float32 {
	span = max(1, min(span, toolCols))
	return float32(span)*(toolStageW-float32(toolCols-1)*toolCellGap)/float32(toolCols) +
		float32(span-1)*toolCellGap
}

// toolRowH is a card's pixel height for its row span.
func toolRowH(rows int) float32 {
	rows = max(1, min(rows, 2))
	return float32(rows)*toolCellH + float32(rows-1)*toolCellGap
}

// packToolCards lays cards into 4 columns, first fit in row-major order:
// each card takes the first w × h block of free cells, scanning rows top
// to bottom. skip names cards that take no space right now (a widget whose
// feature is off): their cells read as free, their entries stay. editing
// appends the dashed + slot.
func packToolCards(cards []layout.Card, skip func(string) bool, editing bool) []toolCell {
	taken := map[[2]int]bool{}
	var cells []toolCell
	place := func(id string, span, rows int, plus bool) {
		span, rows = max(1, min(span, toolCols)), max(1, min(rows, 2))
		for y := 0; ; y++ {
			for x := 0; x+span <= toolCols; x++ {
				free := true
				for dy := range rows {
					for dx := range span {
						if taken[[2]int{x + dx, y + dy}] {
							free = false
						}
					}
				}
				if !free {
					continue
				}
				for dy := range rows {
					for dx := range span {
						taken[[2]int{x + dx, y + dy}] = true
					}
				}
				cells = append(cells, toolCell{
					id: id, plus: plus,
					x: float32(x) * (toolColW(1) + toolCellGap),
					y: float32(y) * (toolCellH + toolCellGap),
					w: toolColW(span), h: toolRowH(rows),
				})
				return
			}
		}
	}
	for _, card := range cards {
		if skip != nil && skip(card.ID) {
			continue
		}
		place(card.ID, card.Cols(), card.Height(), false)
	}
	if editing {
		place("", 1, 1, true)
	}
	return cells
}

// toolVirtualCards is the list the grid would have if the dragged card
// dropped at index now: cards minus it, with it re-inserted there. The
// parked copy keeps its own size.
func toolVirtualCards(cards []layout.Card, dragID string, index int) []layout.Card {
	var (
		rest  []layout.Card
		drag  layout.Card
		found bool
	)
	for _, card := range cards {
		if card.ID == dragID {
			drag, found = card, true
			continue
		}
		rest = append(rest, card)
	}
	if !found {
		return cards
	}
	index = max(0, min(index, len(rest)))
	out := make([]layout.Card, 0, len(rest)+1)
	out = append(out, rest[:index]...)
	out = append(out, drag)
	return append(out, rest[index:]...)
}

// toolInsertIndex is where the pointer means to drop the dragged card:
// the index inside cards (without it) whose packed cell's centre is
// nearest, in row-major scan order. It returns -1 when the drag id is
// unknown. px, py are stage content coordinates.
//
// current is the index last reported; it wins ties so the target does
// not flicker between two cells while the pointer hovers a boundary.
func toolInsertIndex(cards []layout.Card, dragID string, current int, px, py float32) int {
	drag := layout.Card{ID: dragID}
	for _, card := range cards {
		if card.ID == dragID {
			drag = card
		}
	}
	if drag.ID == "" {
		return -1
	}
	best, bestD := -1, float64(math.MaxFloat64)
	for i := 0; i <= len(cards)-1; i++ {
		virtual := toolVirtualCards(cards, dragID, i)
		for _, cell := range packToolCards(virtual, nil, false) {
			if cell.id != dragID {
				continue
			}
			dx := px - (cell.x + cell.w/2)
			dy := py - (cell.y + cell.h/2)
			d := float64(dx*dx + dy*dy)
			if d < bestD || (d == bestD && i == current) {
				best, bestD = i, d
			}
		}
	}
	return best
}
