//go:build darwin

package main

// fitText is the one-line text the home and tools pages draw. When a test
// installs fitIssue, every text is measured at its real font and recorded
// if it cannot fit the width its layout gives it — the permanent guard
// against clipped or wrapped text in the fixed 416 × 174 content area.
// Production leaves fitIssue nil and pays nothing.

import (
	"fmt"

	"github.com/egoist/mygo/ui"
)

var fitIssue func(string)

func fitText(c *ui.Context, text string, size float32, weight int, avail float32) ui.Element {
	if fitIssue != nil {
		w, _ := c.MeasureText(0, ui.Span{Text: text, Size: size, Weight: weight})
		if w > avail+0.5 {
			fitIssue(fmt.Sprintf("%q needs %.1fpt, has %.1fpt (size %g)", text, w, avail, size))
		}
	}
	t := ui.Text(c, text).SingleLine().FontSize(size)
	if weight != 0 {
		t.FontWeight(weight)
	}
	return t
}
