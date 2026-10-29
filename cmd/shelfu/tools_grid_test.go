//go:build darwin

package main

import (
	"testing"

	"shelf/internal/layout"
)

func TestPackToolCardsSpans(t *testing.T) {
	cards := []layout.Card{
		{ID: "a", Span: 1}, {ID: "b", Span: 2}, {ID: "c", Span: 1},
		{ID: "d", Span: 2}, {ID: "e", Span: 2},
	}
	cells := packToolCards(cards, nil, false)
	if len(cells) != 5 {
		t.Fatalf("cells = %d", len(cells))
	}
	// a(1) b(2) c(1) fills row 0; d(2)+e(2) fills row 1.
	colW := toolColW(1)
	spanW := toolColW(2)
	rowH := toolRowH(1)
	want := []toolCell{
		{id: "a", x: 0, y: 0, w: colW, h: rowH},
		{id: "b", x: colW + toolCellGap, y: 0, w: spanW, h: rowH},
		{id: "c", x: 3*colW + 3*toolCellGap, y: 0, w: colW, h: rowH},
		{id: "d", x: 0, y: toolCellH + toolCellGap, w: spanW, h: rowH},
		{id: "e", x: 2*colW + 2*toolCellGap, y: toolCellH + toolCellGap, w: spanW, h: rowH},
	}
	for i, cell := range cells {
		if cell != want[i] {
			t.Fatalf("cell %d = %+v, want %+v", i, cell, want[i])
		}
	}
}

func TestPackToolCardsPlus(t *testing.T) {
	// Edit mode appends the + slot after the last card, wrapping to the
	// next row when the row is full.
	cards := []layout.Card{{ID: "a", Span: 1}, {ID: "b", Span: 1}, {ID: "c", Span: 1}, {ID: "d", Span: 1}}
	cells := packToolCards(cards, nil, true)
	last := cells[len(cells)-1]
	if !last.plus || last.y != toolCellH+toolCellGap {
		t.Fatalf("+ = %+v, want it on row 1", last)
	}
	cells = packToolCards(cards[:3], nil, true)
	last = cells[len(cells)-1]
	if !last.plus || last.y != 0 || last.x != 3*(toolColW(1)+toolCellGap) {
		t.Fatalf("+ = %+v, want column 3 of row 0", last)
	}
	// Normal mode never has it.
	for _, cell := range packToolCards(cards, nil, false) {
		if cell.plus {
			t.Fatal("+ slot without edit mode")
		}
	}
}

func TestToolVirtualCards(t *testing.T) {
	cards := []layout.Card{{ID: "a"}, {ID: "b", Span: 2}, {ID: "c"}}
	got := toolVirtualCards(cards, "a", 2)
	ids := []string{got[0].ID, got[1].ID, got[2].ID}
	if ids[0] != "b" || ids[1] != "c" || ids[2] != "a" {
		t.Fatalf("virtual order = %v", ids)
	}
	if got[0].Span != 2 {
		t.Fatal("virtual move lost the span")
	}
	// Unknown id leaves the list alone.
	same := toolVirtualCards(cards, "zz", 0)
	if len(same) != len(cards) {
		t.Fatal("unknown id changed the list")
	}
}

func TestToolInsertIndex(t *testing.T) {
	cards := []layout.Card{{ID: "a"}, {ID: "b"}, {ID: "c"}, {ID: "d"}, {ID: "e"}}
	colW, rowH := toolColW(1), float32(toolCellH)
	// Pointer over cell 2's centre while dragging "a": index 2 — the
	// virtual order is b c a d e, and a lands where c was.
	if got := toolInsertIndex(cards, "a", -1, 2*colW+2*toolCellGap+colW/2, rowH/2); got != 2 {
		t.Fatalf("insert = %d, want 2", got)
	}
	// Over the first cell: 0.
	if got := toolInsertIndex(cards, "a", -1, colW/2, rowH/2); got != 0 {
		t.Fatalf("insert = %d, want 0", got)
	}
	// Past the last cell of a full row: 4 (before e).
	if got := toolInsertIndex(cards, "a", -1, 3*colW+3*toolCellGap+colW-1, rowH/2); got != 3 && got != 4 {
		t.Fatalf("row-end insert = %d", got)
	}
	// Unknown card: -1.
	if got := toolInsertIndex(cards, "zz", -1, 0, 0); got != -1 {
		t.Fatalf("unknown card insert = %d", got)
	}
}

func TestToolInsertIndexSpans(t *testing.T) {
	// Dragging the 2-span "b" over the row's second half should land it
	// after "a" rather than overlapping columns.
	cards := []layout.Card{{ID: "a"}, {ID: "b", Span: 2}, {ID: "c"}, {ID: "d"}}
	colW := toolColW(1)
	if got := toolInsertIndex(cards, "b", -1, 2*colW+2*toolCellGap+colW/2, toolCellH/2); got < 1 || got > 3 {
		t.Fatalf("span insert = %d", got)
	}
}

// TestDefaultLayoutFitsTwoRows: a fresh install's grid is exactly the
// page's two rows (4 × 2 = 174pt) with no scrolling, for either AI usage
// setting, and the right half holds 保持唤醒 over 框选文字 / 打成 zip.
func TestDefaultLayoutFitsTwoRows(t *testing.T) {
	viewport := float32(2*toolCellH + toolCellGap)
	for _, aiUsage := range []bool{true, false} {
		cells := packToolCards(layout.DefaultWith(aiUsage).Cards, nil, false)
		if len(cells) != 5 {
			t.Fatalf("aiUsage %v: cells = %d, want 5", aiUsage, len(cells))
		}
		var bottom float32
		for _, cell := range cells {
			bottom = max(bottom, cell.y+cell.h)
		}
		if bottom != viewport {
			t.Fatalf("aiUsage %v: grid bottom = %v, want the %v viewport", aiUsage, bottom, viewport)
		}
		fifth := map[string]toolCell{}
		for _, cell := range cells {
			fifth[cell.id] = cell
		}
		corner, hasUsage, hasFit := fifth["usage"], fifth["usage"].id == "usage", fifth["fit1m"].id == "fit1m"
		if aiUsage && !hasUsage {
			t.Fatalf("aiUsage on: usage missing from the fresh grid: %+v", cells)
		}
		if !aiUsage {
			if hasUsage || !hasFit {
				t.Fatalf("aiUsage off: want fit1m in usage's cell, got %+v", cells)
			}
			corner = fifth["fit1m"]
		}
		if corner.y != 0 || corner.x < 2*(toolColW(1)+toolCellGap) {
			t.Fatalf("aiUsage %v: corner card at %+v, want the right half of row 0", aiUsage, corner)
		}
		for _, id := range []string{"ocr", "zip"} {
			if c := fifth[id]; c.y != toolCellH+toolCellGap || c.x < 2*(toolColW(1)+toolCellGap) {
				t.Fatalf("aiUsage %v: %s at %+v, want the right half of row 1", aiUsage, id, c)
			}
		}
	}
}
