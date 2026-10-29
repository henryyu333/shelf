//go:build darwin

package main

// The player's seek and volume rails, shared by every surface that draws
// them: the 正在播放 page, the tools page's 2 × 2 music card and that card
// pinned to the desktop. One state on the shelf means the three draw the
// same knob, and a drag begun on one is the drag the others would show;
// the command still goes out once, on release — one seek, one volume
// write.

import (
	"math"
	"time"

	"github.com/egoist/mygo/ui"
)

// volumePoll is how often a drawn rail re-reads the output device: the
// monitor tick's pace, not every frame, so switching the system output
// while the panel is open reaches the bar without a read per repaint.
const volumePoll = 500 * time.Millisecond

// railDrag is one rail's pointer state. While a drag is on, frac is the
// value under the pointer: the knob follows it without the track moving
// under it, and the release commits it once.
type railDrag struct {
	drag bool
	frac float32
}

// shown is the value the rail draws: the drag's own while one is on, the
// track's otherwise.
func (d *railDrag) shown(idle float32) float32 {
	if d.drag {
		return d.frac
	}
	return idle
}

// volumeState is the output device's last read and whether it accepts
// writes: a device that refuses greys the row and says why.
type volumeState struct {
	loaded bool
	ok     bool
	v      float32
	at     time.Time
}

// musicControls is the shelf's player pointer state: both rails and the
// output volume they read and write.
type musicControls struct {
	seek railDrag
	vol  railDrag
	out  volumeState
}

// rail draws one draggable bar: a trackH-thick rail the full width of the
// element, a 10pt knob while the pointer is on it or dragging, and the
// height height as the hit area. enabled false draws the bar but takes no
// pointer, so a disabled rail never starts a drag. commit runs once, on
// release, with the fraction the pointer let go at.
func rail(c *ui.Context, key, label string, frac float32, height, trackH float32, fill ui.Color, enabled bool, d *railDrag, commit func(float32)) ui.Element {
	if frac < 0 || frac != frac {
		frac = 0
	}
	frac = min(max(frac, 0), 1)
	bar := ui.Row(c.Key(key)).Height(height).Label(label)
	bar.PointerPosition()
	// Bounds is the box the last frame laid out, the one the user is
	// pressing: reading it here, in the build, keeps the handler off the
	// element it would leave behind for the next pass.
	w := bar.Bounds().W
	if enabled {
		bar.HandleInput(func(ev ui.InputEvent) bool {
			if w <= 0 {
				return false
			}
			f := min(max(ev.X/w, 0), 1)
			switch ev.Kind {
			case ui.InputPointerDown:
				d.drag, d.frac = true, f
				return true
			case ui.InputPointerMove:
				if d.drag {
					d.frac = f
				}
				return true
			case ui.InputPointerUp:
				if d.drag {
					d.drag, d.frac = false, f
					commit(f)
				}
				return true
			}
			return false
		})
	}
	active := d.drag || bar.Hovered()
	bar.Draw(func(pt *ui.Painter, r ui.Rect) {
		y := r.Y + (r.H-trackH)/2
		pt.Fill(ui.Rect{X: r.X, Y: y, W: r.W, H: trackH}, fill3, trackH/2)
		if frac > 0 {
			pt.Fill(ui.Rect{X: r.X, Y: y, W: r.W * frac, H: trackH}, fill, trackH/2)
		}
		if active {
			pt.Fill(ui.Rect{X: r.X + r.W*frac - 5, Y: r.Y + r.H/2 - 5, W: 10, H: 10}, ink, 5)
		}
	})
	return bar
}

// seekRail is the draggable timeline: a release seeks the loaded track
// once. dur <= 0 draws an inert rail (a stream without a duration).
func (a *shelf) seekRail(c *ui.Context, key string, frac float32, dur float64, height, trackH float32) ui.Element {
	return rail(c, key, loc(c).PlaybackPosition(), frac, height, trackH, progress, dur > 0, &a.music.seek, func(f float32) {
		if a.np != nil && dur > 0 {
			a.np.seekTo(float64(f) * dur)
		}
	})
}

// volumeRail is the draggable output volume: a release writes the system
// volume once.
func (a *shelf) volumeRail(c *ui.Context, key string, frac float32, enabled bool, height float32) ui.Element {
	return rail(c, key, loc(c).MusicVolume(), frac, height, 3, ui.RGBA(255, 255, 255, 0.75), enabled, &a.music.vol, func(f float32) {
		a.writeVolume(f, c.Now())
	})
}

// volumeRow is the speaker pair and the volume rail; the row greys out
// and says why on a device with no settable volume. icon is the speaker
// glyph's size and gap the space around it.
func (a *shelf) volumeRow(c *ui.Context, key string, icon, gap float32) {
	// The row is the only place that knows about the output device, so
	// while a player surface is on screen it asks for the next frame at
	// the poll's pace: a paused panel then still notices a device switch.
	if a.visible && (a.tab == pagePlaying || a.tab == pageTools) {
		c.After(volumePoll)
	}
	v, ok := a.outputVolume(c.Now())
	row := ui.Row(c.Key(key)).AlignItems(ui.Center).Gap(gap).FillWidth()
	if !ok {
		row.Opacity(0.4).Tooltip(loc(c).MusicVolumeUnsupported())
	}
	row.Children(func() {
		ui.Icon(c, volLoSymbol).Size(icon, icon).TextColor(ink3)
		a.volumeRail(c, key+"-bar", a.music.vol.shown(v), ok, 14).Grow(1).MinWidth(0)
		ui.Icon(c, volHiSymbol).Size(icon, icon).TextColor(ink3)
	})
}

// outputVolume reads the system volume: once, then on volumePoll's pace.
// A drag owns the shown value, so a poll never moves the knob under the
// pointer.
func (a *shelf) outputVolume(now time.Time) (float32, bool) {
	s := &a.music.out
	if a.music.vol.drag {
		return s.v, s.ok
	}
	if s.loaded && now.Sub(s.at) < volumePoll {
		return s.v, s.ok
	}
	s.at = now
	if v, ok := a.volumeControl().level(); ok {
		s.v, s.ok = v, true
	} else {
		s.ok = false
	}
	s.loaded = true
	return s.v, s.ok
}

// writeVolume writes the chosen volume; a device that refuses greys the
// row and says why.
func (a *shelf) writeVolume(v float32, now time.Time) {
	s := &a.music.out
	if err := a.volumeControl().set(v); err != nil {
		s.ok, s.at, s.loaded = false, now, true
		a.toastStatus(a.appLoc().MusicVolumeUnsupported(), now.Add(2*time.Second), statusInfo)
		return
	}
	s.v, s.ok, s.at, s.loaded = v, true, now, true
}

// clockLabelW is the width a time label of size pt needs, rounded up with
// a point of slack: the row spends exactly this much on the label.
func clockLabelW(c *ui.Context, s string, size float32) float32 {
	w, _ := c.MeasureText(0, ui.Span{Text: s, Size: size, Weight: 500})
	return float32(math.Ceil(float64(w))) + 1
}
