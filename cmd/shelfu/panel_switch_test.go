//go:build darwin

package main

// The panel's page switch: every move between the overview and a page, or
// between two pages, is one timeline — the page being left fades out while
// the arriving one fades in from its icon's side. These tests hold the
// user-visible results: the overview arrives once, in its resting spot; a
// switch never shows neither page; both pages' action rows travel with
// their content; and the overview rests by warming the pages.

import (
	"testing"
	"time"

	"github.com/egoist/mygo"
)

// pageDraws counts the page blocks each frame drew, by page id: one group
// per pageBody build.
func pageDraws(t *testing.T, render func()) []map[pageID]int {
	t.Helper()
	var frames []map[pageID]int
	cur := map[pageID]int{}
	flush := func() {
		if len(cur) > 0 {
			frames = append(frames, cur)
			cur = map[pageID]int{}
		}
	}
	oldBlock, oldSwitch := onPageBlock, onPageSwitch
	onPageSwitch = func(*shelf) { flush() }
	onPageBlock = func(_ *shelf, id pageID, _ bool, _, _ float32) { cur[id]++ }
	t.Cleanup(func() { onPageBlock, onPageSwitch = oldBlock, oldSwitch })
	render()
	flush()
	onPageBlock, onPageSwitch = oldBlock, oldSwitch
	return frames
}

// oncePerFrame fails unless every recorded frame drew id exactly once.
func oncePerFrame(t *testing.T, frames []map[pageID]int, id pageID) {
	t.Helper()
	if len(frames) == 0 {
		t.Fatalf("%s: no frame recorded a page block", id)
	}
	for i, draws := range frames {
		if got := draws[id]; got != 1 {
			t.Fatalf("frame %d drew %s %d times, want 1 (all: %v)", i, id, got, draws)
		}
	}
}

// TestPageToOverviewDrawsOneOverview: leaving a page for the overview draws
// the page being left (fading) and the overview exactly once, and the
// overview sits where it rests — the otter and the status text do not slide
// from a second layout's offset.
func TestPageToOverviewDrawsOneOverview(t *testing.T) {
	a, view := levelShelf(t)
	a.visible = true
	view.Frame()
	rest, ok := view.Find("托盘 1 项")
	if !ok {
		t.Fatalf("the overview's status line is missing; texts %v", view.Texts())
	}

	old := pageSwitchPhase
	t.Cleanup(func() { pageSwitchPhase = old })
	pageSwitchPhase = func(*shelf, time.Time) float32 { return 1 }
	a.setTab(pageTray) // settle on a page, no fade
	a.tabAt = time.Time{}
	view.Frame()
	pageSwitchPhase = old

	a.setLevel(1) // the Esc move back to the overview
	if a.tab != pageOverview || a.tabFrom != pageTray {
		t.Fatalf("tab=%s from=%s, want the tray fading out", a.tab, a.tabFrom)
	}
	frames := pageDraws(t, func() {
		pageSwitchPhase = func(*shelf, time.Time) float32 { return 0.42 }
		view.Frame()
		pageSwitchPhase = old
	})
	oncePerFrame(t, frames, pageOverview)
	oncePerFrame(t, frames, pageTray)
	// The overview's own text is still there, on its resting spot.
	mid, ok := view.Find("托盘 1 项")
	if !ok {
		t.Fatalf("the arriving overview has no status line; texts %v", view.Texts())
	}
	if mid != rest {
		t.Fatalf("the overview arrived at %+v, its resting spot is %+v", mid, rest)
	}
}

// TestSameSizeSwitchNeverBlank: the timeline's two opacities never fall to
// zero together, and a mid-switch frame really draws both pages, their
// action rows included.
func TestSameSizeSwitchNeverBlank(t *testing.T) {
	for i := 0; i <= 200; i++ {
		tf := float32(i) / 200
		out, in := switchOpacities(tf)
		if out < 0 || in < 0 || out > 1 || in > 1 {
			t.Fatalf("t=%.3f: opacities out=%.3f in=%.3f out of range", tf, out, in)
		}
		if out == 0 && in == 0 {
			t.Fatalf("t=%.3f shows neither page", tf)
		}
	}
	if out, _ := switchOpacities(0); out != 1 {
		t.Fatalf("the switch starts with the old page at %.3f, want 1", out)
	}
	if out, _ := switchOpacities(tabOutShare); out != 0 {
		t.Fatalf("the old page has not finished fading at tabOutShare: %.3f", out)
	}
	if _, in := switchOpacities(1); in != 1 {
		t.Fatalf("the switch ends with the new page at %.3f, want 1", in)
	}

	a, view := levelShelf(t)
	a.visible = true
	old := pageSwitchPhase
	t.Cleanup(func() { pageSwitchPhase = old })
	pageSwitchPhase = func(*shelf, time.Time) float32 { return 1 }
	a.setTab(pageTray)
	a.tabAt = time.Time{}
	view.Frame()
	pageSwitchPhase = old

	a.setTab(pageClipboard)
	pageSwitchPhase = func(*shelf, time.Time) float32 { return 0.45 }
	frames := pageDraws(t, func() { view.Frame() })
	pageSwitchPhase = old
	oncePerFrame(t, frames, pageTray)
	oncePerFrame(t, frames, pageClipboard)
	// Both action rows ride their own block: the arriving page's search box
	// is in the frame while the page being left is still on screen.
	if !view.HasText("搜索剪贴板") {
		t.Fatalf("the arriving page's action row is missing mid-switch; texts %v", view.Texts())
	}
	if !view.HasText("⌘ / ⇧ 多选") {
		t.Fatalf("the leaving page's action row vanished before its content; texts %v", view.Texts())
	}
}

// TestOverviewPrewarmsPages: resting on the overview queues and draws the
// pages nobody has seen, so the first switch to any page is as cheap as the
// ones after it.
func TestOverviewPrewarmsPages(t *testing.T) {
	a, view := levelShelf(t)
	a.visible = true
	view.Frame()
	if a.currentLevel() != 1 {
		t.Fatalf("level = %d, want the resting overview", a.currentLevel())
	}
	// nextWarmPage needs a native surface to ask for the frame; the tests'
	// shelf has none, so it is lent one for that call only.
	a.win, a.winH = &mygo.Window{}, 0
	a.nextWarmPage(time.Now())
	a.win = nil
	if a.warming == "" {
		t.Fatal("resting on the overview queued no page to warm")
	}
	id := a.warming
	view.Frame()
	if !a.warmed[id] {
		t.Fatalf("the overview never drew the %s warm layer", id)
	}
	// Warming continues one page per tick until every page has been seen
	// once; the page on screen counts as warm by being drawn.
	for range len(a.enabledPages()) + 1 {
		a.win = &mygo.Window{}
		a.nextWarmPage(time.Now())
		a.win = nil
		if a.warming == "" {
			break
		}
		view.Frame()
	}
	for _, p := range a.enabledPages() {
		if !a.warmed[p.id()] {
			t.Fatalf("%s was never warmed", p.id())
		}
	}
}

// TestRapidSwitchKeepsTheDrawnFade: a second switch that interrupts a
// running one continues the picture on screen. The page being left keeps
// the opacity it had reached — a page part-way through its fade must not
// jump back to full brightness — and its settle offset carries on from
// where it was instead of snapping to the final position.
func TestRapidSwitchKeepsTheDrawnFade(t *testing.T) {
	cases := []struct {
		name string
		// A (left) -> B (arriving) at phase, then a click on C.
		from, page, next pageID
		phase            float32
		outK, outShift   float32
		wantOut          pageID
	}{
		{"still fading out", pageTray, pageTools, pageClipboard, 0.2, 0.9, 3, pageTray},
		{"already fading in", pageTray, pageClipboard, pageTools, 0.6, 1, 0, pageClipboard},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			a, view := levelShelf(t)
			a.visible, a.level = true, 2
			view.Frame()
			// Put the panel mid-switch A -> B.
			a.tab, a.tabFrom = tc.page, tc.from
			a.tabAt = time.Now().Add(-time.Duration(float32(tabAnimation) * tc.phase))
			a.tabOutK, a.tabOutShift = tc.outK, tc.outShift

			a.setTab(tc.next)
			if a.tabOutK >= 1 {
				t.Fatalf("the interrupted switch did not carry an opacity: %.3f", a.tabOutK)
			}
			wantOp, wantLeft := a.tabOutK, a.tabOutShift

			var out *struct {
				id       pageID
				op, left float32
			}
			old := onPageBlock
			onPageBlock = func(_ *shelf, id pageID, live bool, op, left float32) {
				if !live && out == nil {
					out = &struct {
						id       pageID
						op, left float32
					}{id, op, left}
				}
			}
			oldPhase := pageSwitchPhase
			pageSwitchPhase = func(*shelf, time.Time) float32 { return 0 }
			view.Frame()
			onPageBlock, pageSwitchPhase = old, oldPhase
			if out == nil {
				t.Fatal("the switch drew no page to fade out")
			}
			if out.id != tc.wantOut {
				t.Fatalf("the fading page is %s, want %s", out.id, tc.wantOut)
			}
			if d := out.op - wantOp; d > 0.01 || d < -0.01 {
				t.Fatalf("the fading page is drawn at %.3f, not the %.3f it had reached", out.op, wantOp)
			}
			if d := out.left - wantLeft; d > 0.01 || d < -0.01 {
				t.Fatalf("the fading page is drawn at x=%.2f, not the %.2f it had reached", out.left, wantLeft)
			}
		})
	}
}
