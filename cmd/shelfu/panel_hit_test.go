//go:build darwin

package main

// The page switch's unseen layers must never take the pointer: the warm
// layer, the arriving page before its fade starts and the outgoing picture
// are drawn for their pixels only. A click, a right-click, the wheel or a
// slider must act on the page the user can see (or on nothing while a
// switch has no visible page yet).

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"shelf/internal/mac"
	"shelf/internal/settings"
	"shelf/internal/store"
)

// hitShelf is a level-2 shelf with tray and clipboard content, a playing
// track (the home page's volume row), a volume seam and a copy writer that
// records what the clipboard would receive.
func hitShelf(t *testing.T) (*shelf, *ui.Tester, *[]string, *fakeVolume) {
	t.Helper()
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	for i := range 3 {
		if err := st.AddText(fmt.Sprintf("托盘条目 %d", i)); err != nil {
			t.Fatal(err)
		}
	}
	for i := range 6 {
		if err := st.AddClipText(fmt.Sprintf("剪贴板条目 %d · 一段用于命中的文字", i), "com.example.App"); err != nil {
			t.Fatal(err)
		}
	}
	a := &shelf{
		store: st, dataDir: t.TempDir(), level: 2, visible: true, settings: settings.Default(),
		thumbnails: map[string]*ui.Bitmap{}, thumbPending: map[string]bool{}, appNames: map[string]string{},
	}
	a.np = &nowPlayingModule{host: a, have: true, last: mac.NowPlayingState{
		Title: "歌名", Duration: 200, Elapsed: 20, Rate: 1, Received: time.Now(),
	}}
	vol := &fakeVolume{v: 0.4, ok: true}
	a.vol = vol
	var copied []string
	a.write = recordClipFunc(func(text string) error {
		copied = append(copied, text)
		return nil
	})
	a.setTab(pageTray)
	a.tabAt = time.Time{} // the sheet rests: no page switch is under way
	view := ui.NewTester(a.view, sheetW, sheetH)
	return a, view, &copied, vol
}

// TestWarmLayerTakesNoPointer draws the clipboard page only to warm it
// under the visible tray page: clicking the unseen row must copy the tray
// tile that is really there, right-clicking must open that tile's menu,
// and the wheel must leave the unseen list alone.
func TestWarmLayerTakesNoPointer(t *testing.T) {
	a, view, copied, vol := hitShelf(t)
	view.Frame()
	warm := func(id pageID) {
		t.Helper()
		a.warming = id
		view.Frame()
		if a.warming != "" {
			t.Fatalf("the %s warm layer was not drawn", id)
		}
	}
	warm(pageClipboard)

	// A click over the hidden clipboard row lands on the tray tile under it.
	if err := view.Click("剪贴板条目 5 · 一段用于命中的文字"); err != nil {
		t.Fatalf("clicking over the warm layer: %v", err)
	}
	if len(*copied) == 0 {
		t.Fatal("the click copied nothing: it did not reach the visible page")
	}
	if got := (*copied)[len(*copied)-1]; !strings.HasPrefix(got, "托盘条目") {
		t.Fatalf("a click over the warm clipboard page copied %q, want the visible tray's item", got)
	}

	// A right-click opens the visible tile's menu, and copying from it
	// takes the visible item. The click above repainted (a copy toast), so
	// the layer is warmed again for the next interaction.
	warm(pageClipboard)
	before := len(*copied)
	if err := view.RightClick("剪贴板条目 4 · 一段用于命中的文字"); err != nil {
		t.Fatalf("right-clicking over the warm layer: %v", err)
	}
	if len(view.Menu()) == 0 {
		t.Fatal("the right-click opened no menu: it missed the visible page")
	}
	if err := view.ChooseMenuItem("复制文字"); err != nil {
		t.Fatalf("copying from the visible page's menu: %v", err)
	}
	if len(*copied) != before+1 || !strings.HasPrefix((*copied)[len(*copied)-1], "托盘条目") {
		t.Fatalf("the menu copy took %q, want the visible tray's item", (*copied)[len(*copied)-1])
	}

	// The wheel must not scroll the unseen list.
	warm(pageClipboard)
	clip := a.page(pageClipboard).(*clipboardPage)
	view.Scroll(200, 120, 0, 60)
	view.Frame()
	if clip.scroll.Y != 0 {
		t.Fatalf("the wheel scrolled the unseen clipboard list to %.1f", clip.scroll.Y)
	}

	// The unseen playing page's volume bar writes nothing on a release.
	*vol = fakeVolume{v: 0.4, ok: true}
	warm(pagePlaying)
	bar, ok := view.Find("音量")
	if !ok {
		t.Fatal("the warm playing page drew no volume bar")
	}
	view.Press(bar.X+bar.W/2, bar.Y+bar.H/2)
	view.Move(bar.X+bar.W*3/4, bar.Y+bar.H/2)
	view.Release(bar.X+bar.W*3/4, bar.Y+bar.H/2)
	view.Frame()
	if len(vol.sets) != 0 {
		t.Fatalf("a press on the unseen home page's volume bar wrote %v", vol.sets)
	}
}

// TestSwitchFirstPhaseTakesNoPointer: while the old page is only a
// picture and the new one is still invisible, a click must not reach the
// arriving page's rows; once its fade starts, the same click does.
func TestSwitchFirstPhaseTakesNoPointer(t *testing.T) {
	a, view, copied, _ := hitShelf(t)
	view.Frame()
	old := pageSwitchPhase
	t.Cleanup(func() { pageSwitchPhase = old })
	a.setTab(pageClipboard)

	pageSwitchPhase = func(*shelf, time.Time) float32 { return 0.2 }
	view.Frame()
	if err := view.Click("剪贴板条目 0 · 一段用于命中的文字"); err != nil {
		t.Logf("the click hit no row (expected while the new page is invisible): %v", err)
	}
	if len(*copied) != 0 {
		t.Fatalf("a click before the fade-in reached the invisible page: copied %q", *copied)
	}

	pageSwitchPhase = func(*shelf, time.Time) float32 { return 0.6 }
	view.Frame()
	if err := view.Click("剪贴板条目 0 · 一段用于命中的文字"); err != nil {
		t.Fatalf("the fading-in page took no click: %v", err)
	}
	if len(*copied) != 1 {
		t.Fatalf("the fading-in page's row did not take the click: copied %q", *copied)
	}
}

// TestLevelChangeRetiresTheSwitchClock: Esc back to the island ends the
// page switch; a file dropped on the island right afterwards opens the
// tray page at full opacity instead of resuming the old fade.
func TestLevelChangeRetiresTheSwitchClock(t *testing.T) {
	a, _, _, _ := hitShelf(t)
	a.setTab(pageClipboard)
	if a.tabAt.IsZero() {
		t.Fatal("the visible switch left no clock")
	}
	a.setLevel(1)
	if !a.tabAt.IsZero() {
		t.Fatal("going back to the island kept the page switch clock")
	}
	if a.tabOutK != 1 || a.tabOutShift != 0 {
		t.Fatalf("the island kept the switch's picture state: k=%g shift=%g", a.tabOutK, a.tabOutShift)
	}
	// The drop-on-island path sets both pages and opens the sheet.
	a.tabFrom, a.tab = pageTray, pageTray
	a.setLevel(2)
	phase, _ := a.pageMotion(time.Now())
	if phase != 1 {
		t.Fatalf("reopening the sheet resumed a page switch at t=%.2f", phase)
	}
}

// TestRapidSwitchCarriesTheSettlingOffset: a switch that interrupts a
// fade-out continues from the offset the picture has reached, so the page
// does not jump back to x=0.
func TestRapidSwitchCarriesTheSettlingOffset(t *testing.T) {
	a, _, _, _ := hitShelf(t)
	// Put the panel 25% into a switch whose old page (tools) is still in
	// its 35% fade-out: the picture is part-way through its 8pt settle at
	// half opacity.
	a.tab, a.tabFrom = pageTray, pageTools
	a.tabAt = time.Now().Add(-tabAnimation / 4)
	a.tabOutK, a.tabOutShift = 0.5, tabShift
	a.setTab(pageClipboard)
	if a.tabFrom != pageTools {
		t.Fatalf("the interrupted fade lost its outgoing page: %s", a.tabFrom)
	}
	want := float64(tabShift) * (1 - 0.25/tabOutShare)
	if float64(a.tabOutShift) < want-0.5 || float64(a.tabOutShift) > want+0.5 {
		t.Fatalf("the interrupted fade's offset = %.2f, want about %.2f", a.tabOutShift, want)
	}
}
