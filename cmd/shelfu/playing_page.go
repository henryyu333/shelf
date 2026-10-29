//go:build darwin

package main

// 正在播放页：原主页音乐小组件的 2 × 2 版放大成整页（440 × 250 的页面区）。
// 封面、歌名 /「歌手 · 应用」、可拖的进度条与已播 / 剩余时间、上一首 / 播放
// 暂停 / 下一首、两端带喇叭的音量条。进度和音量都只是本地预览：松手才发一次
// 跳转 / 写一次系统音量；设备不支持调音量时整行变暗并说明。

import (
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

// playingPage is the now-playing page. Its seek and volume rails are the
// shelf's shared player controls (music_controls.go), the same ones the
// tools page's 2 × 2 music card draws.
type playingPage struct {
	host *shelf
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
	if p.host.music.seek.drag && dur > 0 {
		frac = p.host.music.seek.frac
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
			nw, rw := clockLabelW(c, nowLabel, typeFoot), clockLabelW(c, remLabel, typeFoot)
			panelLabel(c, nil, nowLabel, typeFoot, 500).TextColor(ink3).Width(nw)
			p.host.seekRail(c, "playing-progress", frac, dur, 14, 4).Width(contentW - 20 - 16 - 6 - nw - rw)
			panelLabel(c, nil, remLabel, typeFoot, 500).TextColor(ink3).Width(rw)
		})
		ui.Row(c.Key("playing-ctrl")).Justify(ui.Center).AlignItems(ui.Center).Gap(26).FillWidth().Children(func() {
			m.controlButton(c, "playing-prev", prevFill, l.PreviousTrack(), mac.NPPrevious, 30, 17)
			m.controlButton(c, "playing-toggle", toggleSymbol(st.Playing), playPauseLabel(st.Playing), mac.NPPlayPause, 38, 22)
			m.controlButton(c, "playing-next", nextFill, l.NextTrack(), mac.NPNext, 30, 17)
		})
		p.host.volumeRow(c, "playing-vol", 13, 8)
	})
	return ui.Box(c).Size(0, 0)
}

// openPlaying opens the now-playing page: from the overview's track row
// and from the tools page's music card (a tap on the card itself). The
// notch's expand gestures never land here — they open the overview.
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
