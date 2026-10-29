//go:build darwin

package main

// 正在播放页：原主页音乐小组件的 2 × 2 版放大成整页（440 × 250 的页面区）。
// 封面、歌名 /「歌手 · 应用」、可拖的进度条与已播 / 剩余时间、上一首 / 播放
// 暂停 / 下一首、两端带喇叭的音量条。进度和音量都只是本地预览：松手才发一次
// 跳转 / 写一次系统音量；设备不支持调音量时整行变暗并说明。

import (
	"math"
	"time"

	"github.com/egoist/mygo/ui"

	"shelf/internal/mac"
)

// Speaker glyphs for the volume row.
var (
	volLoSymbol = symbol(`<path d="M4 10h3l4-3.5v11L7 14H4z"/>`)
	volHiSymbol = symbol(`<path d="M4 10h3l4-3.5v11L7 14H4z"/><path d="M15 9a4.5 4.5 0 0 1 0 6M17.5 6.5a8 8 0 0 1 0 11"/>`)
)

// volumeControl is the default output device's volume. Tests replace it
// with a fake so no test ever changes the machine's volume.
type volumeControl interface {
	level() (float32, bool)
	set(float32) error
}

type systemVolume struct{}

func (systemVolume) level() (float32, bool) { return mac.OutputVolume() }
func (systemVolume) set(v float32) error    { return mac.SetOutputVolume(v) }

// volumeControl is the seam: nil means the real CoreAudio device.
func (a *shelf) volumeControl() volumeControl {
	if a.vol != nil {
		return a.vol
	}
	return systemVolume{}
}

// playingPage is the now-playing page.
type playingPage struct {
	host *shelf

	// Progress/volume drag state: while dragging, the bar shows the
	// pointer's value; the command (seek / volume write) goes out once,
	// on release.
	dragPos bool
	posFrac float32
	dragVol bool
	volFrac float32

	// System output volume, re-read at volumePoll's pace while the page
	// draws. The row greys out when the device has no settable volume.
	volLoaded bool
	volOK     bool
	vol       float32
	volAt     time.Time
}

func (p *playingPage) id() pageID    { return pagePlaying }
func (p *playingPage) label() string { return p.host.appLoc().MusicTitle() }
func (p *playingPage) icon(c *ui.Context, _ bool) ui.Element {
	return ui.Icon(c, musicSymbol)
}
func (p *playingPage) enabled() bool                      { return true }
func (p *playingPage) title() string                      { return p.host.appLoc().MusicTitle() }
func (p *playingPage) actions(c *ui.Context) []ui.Element { return nil }

// body draws the player into the page's 416 × 174 content box.
func (p *playingPage) body(c *ui.Context, _ bool, _ bool) ui.Element {
	l := loc(c)
	m := p.host.np
	stage := ui.Column(c.Key("playing-stage")).FillWidth().FillHeight().Padding(0, 10, 0, 10)
	var st mac.NowPlayingState
	ok := false
	if m != nil {
		st, ok = m.track()
	}
	if !ok {
		return stage.Children(func() {
			ui.Column(c.Key("playing-empty")).FillWidth().FillHeight().Center().Gap(8).Children(func() {
				ui.Row(c.Key("playing-empty-art")).Size(52, 52).Radius(10).Background(fill2).
					Opacity(0.6).Center().Children(func() {
					ui.Icon(c, musicSymbol).Size(22, 22).TextColor(ink3)
				})
				panelLabel(c, nil, l.MusicEmpty(), typeCallout, 0).TextColor(ink3)
			})
		})
	}
	dur := st.Duration
	now := time.Now()
	pos := st.Position(now)
	if dur > 0 {
		pos = min(max(pos, 0), dur)
	}
	frac := float32(0)
	if dur > 0 {
		frac = float32(pos / dur)
	}
	if p.dragPos && dur > 0 {
		frac = p.posFrac
		pos = float64(frac) * dur
	}
	stage.Justify(ui.SpaceBetween).Children(func() {
		ui.Row(c.Key("playing-top")).Gap(12).AlignItems(ui.Center).FillWidth().Children(func() {
			m.coverArt(c, "playing-art", 52, 10)
			ui.Column(c).Grow(1).MinWidth(0).Gap(2).Children(func() {
				ui.Text(c.Key("playing-title"), st.Title).SingleLine().FontSize(typeTitle).
					FontWeight(600).TextColor(ink)
				if meta := m.cardMeta(st); meta != "" {
					ui.Text(c.Key("playing-meta"), meta).SingleLine().FontSize(typeCallout).TextColor(ink2)
				}
			})
		})
		ui.Row(c.Key("playing-track")).AlignItems(ui.Center).Gap(8).FillWidth().Children(func() {
			rem := float64(0)
			if dur > 0 {
				rem = max(0, dur-pos)
			}
			nowLabel, remLabel := mac.FormatClock(pos), "-"+mac.FormatClock(rem)
			nw, rw := clockLabelW(c, nowLabel), clockLabelW(c, remLabel)
			panelLabel(c, nil, nowLabel, typeFoot, 500).TextColor(ink3).Width(nw)
			p.progressBar(c, "playing-progress", frac, dur > 0, dur, contentW-20-16-6-nw-rw)
			panelLabel(c, nil, remLabel, typeFoot, 500).TextColor(ink3).Width(rw)
		})
		ui.Row(c.Key("playing-ctrl")).Justify(ui.Center).AlignItems(ui.Center).Gap(26).FillWidth().Children(func() {
			m.controlButton(c, "playing-prev", prevFill, l.PreviousTrack(), mac.NPPrevious, 30, 17)
			m.controlButton(c, "playing-toggle", toggleSymbol(st.Playing), playPauseLabel(st.Playing), mac.NPPlayPause, 38, 22)
			m.controlButton(c, "playing-next", nextFill, l.NextTrack(), mac.NPNext, 30, 17)
		})
		p.volumeRow(c)
	})
	return ui.Box(c).Size(0, 0)
}

// clockLabelW is the width a 10.5pt time label needs, rounded up with a
// point of slack: the row spends exactly this much on the label.
func clockLabelW(c *ui.Context, s string) float32 {
	w, _ := c.MeasureText(0, ui.Span{Text: s, Size: typeFoot, Weight: 500})
	return float32(math.Ceil(float64(w))) + 1
}

// progressBar is the seekable track: a 4pt rail with a knob on hover or
// while dragged. The bar only reports a press to the page; the seek goes
// out once on release.
func (p *playingPage) progressBar(c *ui.Context, key string, frac float32, enabled bool, dur float64, width float32) ui.Element {
	if frac < 0 || frac != frac {
		frac = 0
	}
	frac = min(max(frac, 0), 1)
	width = max(width, 40)
	bar := ui.Row(c.Key(key)).Width(width).Height(14).Label(loc(c).PlaybackPosition())
	bar.PointerPosition()
	if enabled {
		bar.HandleInput(func(ev ui.InputEvent) bool {
			f := min(max(ev.X/width, 0), 1)
			switch ev.Kind {
			case ui.InputPointerDown:
				p.dragPos, p.posFrac = true, f
				return true
			case ui.InputPointerMove:
				if p.dragPos {
					p.posFrac = f
				}
				return true
			case ui.InputPointerUp:
				if p.dragPos {
					p.dragPos = false
					p.posFrac = f
					if p.host.np != nil && dur > 0 {
						p.host.np.seekTo(float64(f) * dur)
					}
				}
				return true
			}
			return false
		})
	}
	active := p.dragPos || bar.Hovered()
	bar.Draw(func(pt *ui.Painter, r ui.Rect) {
		y := r.Y + (r.H-4)/2
		pt.Fill(ui.Rect{X: r.X, Y: y, W: r.W, H: 4}, fill3, 2)
		if frac > 0 {
			pt.Fill(ui.Rect{X: r.X, Y: y, W: r.W * frac, H: 4}, progress, 2)
		}
		if active {
			pt.Fill(ui.Rect{X: r.X + r.W*frac - 5, Y: r.Y + r.H/2 - 5, W: 10, H: 10}, ink, 5)
		}
	})
	return bar
}

// volumeRow is the speaker pair and the volume bar; the row greys out and
// says why on a device with no settable volume.
func (p *playingPage) volumeRow(c *ui.Context) {
	// The row is the only place that knows about the output device, so
	// while it is on screen it asks for the next frame at the poll's pace:
	// a paused panel then still notices a device switch.
	if p.host.visible && p.host.tab == pagePlaying {
		c.After(volumePoll)
	}
	v, ok := p.volume(c.Now())
	frac := v
	if p.dragVol {
		frac = p.volFrac
	}
	row := ui.Row(c.Key("playing-vol")).AlignItems(ui.Center).Gap(8).FillWidth()
	if !ok {
		row.Opacity(0.4).Tooltip(loc(c).MusicVolumeUnsupported())
	}
	row.Children(func() {
		ui.Icon(c, volLoSymbol).Size(13, 13).TextColor(ink3)
		p.volumeBar(c, frac, ok)
		ui.Icon(c, volHiSymbol).Size(13, 13).TextColor(ink3)
	})
}

// volumeBar is the 3pt volume rail; a release writes the system volume
// once through the replaceable control.
func (p *playingPage) volumeBar(c *ui.Context, frac float32, enabled bool) ui.Element {
	bar := ui.Row(c.Key("playing-volbar")).Grow(1).MinWidth(0).Height(14).Label(loc(c).MusicVolume())
	bar.PointerPosition()
	if enabled {
		vx, _, _ := bar.PointerPosition()
		_ = vx
		bar.HandleInput(func(ev ui.InputEvent) bool {
			w := bar.Bounds().W
			if w <= 0 {
				return false
			}
			f := min(max(ev.X/w, 0), 1)
			switch ev.Kind {
			case ui.InputPointerDown:
				p.dragVol, p.volFrac = true, f
				return true
			case ui.InputPointerMove:
				if p.dragVol {
					p.volFrac = f
				}
				return true
			case ui.InputPointerUp:
				if p.dragVol {
					p.dragVol = false
					p.volFrac = f
					p.commitVolume(f, c.Now())
				}
				return true
			}
			return false
		})
	}
	active := p.dragVol || bar.Hovered()
	bar.Draw(func(pt *ui.Painter, r ui.Rect) {
		y := r.Y + (r.H-3)/2
		pt.Fill(ui.Rect{X: r.X, Y: y, W: r.W, H: 3}, fill3, 1.5)
		if frac > 0 {
			pt.Fill(ui.Rect{X: r.X, Y: y, W: r.W * frac, H: 3}, ui.RGBA(255, 255, 255, 0.75), 1.5)
		}
		if active {
			pt.Fill(ui.Rect{X: r.X + r.W*frac - 5, Y: r.Y + r.H/2 - 5, W: 10, H: 10}, ink, 5)
		}
	})
	return bar
}

// volumePoll is how often the drawn row re-reads the output device.
const volumePoll = 500 * time.Millisecond

// volume reads the system volume: once, then on volumePoll's pace while
// the page draws. A drag owns the shown value, so a poll never moves the
// knob under the pointer.
func (p *playingPage) volume(now time.Time) (float32, bool) {
	if p.dragVol {
		return p.vol, p.volOK
	}
	if p.volLoaded && now.Sub(p.volAt) < volumePoll {
		return p.vol, p.volOK
	}
	p.volAt = now
	if v, ok := p.host.volumeControl().level(); ok {
		p.vol, p.volOK = v, true
	} else {
		p.volOK = false
	}
	p.volLoaded = true
	return p.vol, p.volOK
}

// commitVolume writes the chosen volume; a device that refuses greys the
// row and says why.
func (p *playingPage) commitVolume(v float32, now time.Time) {
	if err := p.host.volumeControl().set(v); err != nil {
		p.volOK = false
		p.volAt = now
		p.volLoaded = true
		p.host.toastStatus(p.host.appLoc().MusicVolumeUnsupported(), time.Now().Add(2*time.Second), statusInfo)
		return
	}
	p.vol, p.volOK, p.volAt, p.volLoaded = v, true, now, true
}

// playingPageOf is the shelf's now-playing page, nil for test shelves
// that never registered one.
func (a *shelf) playingPageOf() *playingPage {
	if p, ok := a.page(pagePlaying).(*playingPage); ok {
		return p
	}
	return nil
}

// openPlaying opens the now-playing page from the overview or the hover
// wings: the panel expands on that page.
func (a *shelf) openPlaying() {
	p := a.page(pagePlaying)
	if p == nil {
		return
	}
	if !a.visible {
		a.show(false)
	}
	a.openPage(pagePlaying)
}
