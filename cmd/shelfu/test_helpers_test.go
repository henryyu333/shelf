//go:build darwin

package main

// Shared test seams.

import (
	"testing"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"shelf/internal/layout"
	"shelf/internal/mac"
	"shelf/internal/store"
)

// fakeVolume is the replaceable volume seam: tests never touch the
// machine's real output volume.
type fakeVolume struct {
	v    float32
	ok   bool
	err  error
	sets []float32
}

func (f *fakeVolume) level() (float32, bool) { return f.v, f.ok }
func (f *fakeVolume) set(v float32) error {
	f.sets = append(f.sets, v)
	if f.err != nil {
		return f.err
	}
	f.v = v
	return nil
}

// musicShelf builds a shelf with a real track, a page tester and a volume
// seam: the now-playing page's controls live in a real layout.
func musicShelf(t *testing.T) (*shelf, *ui.Tester) {
	t.Helper()
	s, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	a := &shelf{store: s, dataDir: t.TempDir()}
	a.vol = &fakeVolume{v: 0.4, ok: true}
	a.np = &nowPlayingModule{
		host: a, have: true,
		last: mac.NowPlayingState{
			Title: "座右铭", Artist: "吴雨霏", Duration: 200, Elapsed: 20,
			Rate: 1, Received: time.Now(),
		},
	}
	a.setTab(pagePlaying)
	view := ui.NewTester(a.view, sheetW, sheetH)
	return a, view
}

// cardByID finds a card in the grid.
func cardByID(cards []layout.Card, id string) (layout.Card, bool) {
	for _, c := range cards {
		if c.ID == id {
			return c, true
		}
	}
	return layout.Card{}, false
}

// pinWindowRect is a convenient empty frame for pin seams in tests.
func pinWindowRect() mygo.Rectangle { return mygo.Rectangle{Width: 200, Height: 100} }
