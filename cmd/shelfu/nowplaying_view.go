//go:build darwin

package main

import (
	"image"
	"math"
	"time"

	"github.com/egoist/mygo/i18n"
	"github.com/egoist/mygo/ui"

	"shelf/internal/mac"
	"shelf/locales"
)

// The now-playing module's drawing lives here; nowplaying_module.go owns
// the helper's state. Two surfaces: the hover wings that grow out of the
// notch (a cover in the left wing, the equalizer in the right — nothing
// else) and the expanded panel's activity (36pt cover, title/「artist ·
// app」, ⏮ ⏯ ⏭, a hairline progress track). Transport stays in the panel:
// the wings only say what is playing.

// Hover wings geometry: the notch grows npWingsW to each side and
// npWingsBody below it. The hardware notch hides the middle, so both
// pieces sit in the notch's own band and never grow down into the screen.
const (
	npWingsW    = 52 // each wing beyond the notch
	npWingsBody = 4  // height below the notch: barely more than the band
	npWingsPad  = 14 // content inset from the shell's outer edge
	npWingsArt  = 22
	npWingsArtR = 5
	eqBars      = 4
	eqBarW      = 3
	eqBarGap    = 2
	eqBarR      = 1.5
	eqMaxH      = 14
	eqPausedH   = 4 // 暂停时静止为低矮等高
	eqStillH    = 8 // 减少动态效果时的静止高度
)

// eqPeriods are the equalizer bars' loop periods, inside the design's
// 0.9–1.4s band; spread phases keep them unrelated.
var eqPeriods = []time.Duration{1300 * time.Millisecond, 940 * time.Millisecond, 1120 * time.Millisecond, 1380 * time.Millisecond}

// Now-playing glyphs, filled where the controls are solid and stroked
// where panel symbols are.
var (
	musicSymbol = symbol(`<path d="M9 18V6l10-2v12M9 18a3 3 0 1 1-6 0 3 3 0 0 1 6 0zM19 16a3 3 0 1 1-6 0 3 3 0 0 1 6 0z"/>`)
	playFill    = fillSymbol(`<path d="M8 5.5 19 12 8 18.5z"/>`)
	pauseFill   = fillSymbol(`<rect x="6" y="5" width="4" height="14" rx="1"/><rect x="14" y="5" width="4" height="14" rx="1"/>`)
	prevFill    = fillSymbol(`<path d="M11 6v12l-8-6zM20 6v12l-8-6z"/>`)
	nextFill    = fillSymbol(`<path d="M13 6v12l8-6zM4 6v12l8-6z"/>`)
)

// showsPlayer reports whether hovering the notch shows the two wings rather
// than the plain bulge: a track is loaded, the setting is on and no file
// drag is asking for the notch's drop target.
func (m *nowPlayingModule) showsPlayer() bool {
	_, ok := m.track()
	return ok && m.host.settings.IslandNowPlaying && !m.host.dragging
}

// peekWings draws the hover wings at their final size w×h: the cover in
// the left wing, the equalizer in the right, both centered in the band the
// hardware notch occupies. The middle stays empty — the camera housing
// hides it. There are no controls: a press anywhere on the shell opens the
// shelf, and the sheet's left-ear chip keeps the transport. The
// caller clips the layout to the growing shell and passes the peek
// progress as k, so nothing squeezes while the notch opens. It reports
// whether it drew anything.
func (m *nowPlayingModule) peekWings(c *ui.Context, w, h, k float32) bool {
	st, ok := m.track()
	if !ok || !m.showsPlayer() {
		return false
	}
	l := loc(c)
	label := l.PanelExpand()
	if st.Title != "" {
		label = l.NpPlayingExpand(st.Title)
	}
	wings := ui.Row(c.Key("np-wings")).Width(w).Height(h).
		Padding(0, npWingsPad, npWingsBody, npWingsPad).AlignItems(ui.Center).
		Justify(ui.SpaceBetween).Opacity(min(max(k*k, 0), 1)).Label(label)
	wings.Children(func() {
		m.coverArt(c, "np-wings-art", npWingsArt, npWingsArtR)
		m.eqBars(c, st.Playing)
	})
	if wings.Clicked() {
		m.host.openPlaying()
	}
	return true
}

// eqBars draws the four equalizer bars: animated between 4 and 14pt while
// playing (each bar on its own period), a still low row while paused, and
// a still mid row when the desktop asks for less motion.
func (m *nowPlayingModule) eqBars(c *ui.Context, playing bool) {
	col := ink3
	if m.artColor.A > 0 {
		col = m.artColor
	}
	ui.Row(c.Key("np-eq")).Gap(eqBarGap).AlignItems(ui.Center).Height(eqMaxH).Children(func() {
		for i := range eqPeriods {
			bar := ui.Box(c.Key(i)).Width(eqBarW).Radius(eqBarR).Background(col).AlignSelf(ui.End)
			h := float32(eqPausedH)
			switch {
			case playing && !c.Preferences().ReduceMotion:
				k := bar.Loop("eq", eqPeriods[i], ui.Bounce(ui.EaseInOut))
				// 与循环进度混合一个固定相位，让四根条不在同一起伏。
				wave := math.Sin(float64(float32(i)*0.37+k) * math.Pi)
				h = 4 + float32(wave*wave)*float32(eqMaxH-4)
			case playing:
				h = eqStillH
			}
			bar.Height(h)
		}
	})
}

// live reports whether the sheet's left-ear chip shows the player: the setting is
// on and the source names a track, playing or paused (the controls are
// most useful while paused).
func (m *nowPlayingModule) live() bool { return m.hasTrack() }

// chip is the sheet's now-playing capsule: 16pt cover, equalizer and
// play/pause. The track's name is its tooltip.
func (m *nowPlayingModule) chip(c *ui.Context) ui.Element {
	st, ok := m.track()
	if !ok {
		return ui.Box(c).Size(0, 0)
	}
	l := loc(c)
	tip := l.NpPlayingPause(st.Title)
	if !st.Playing {
		tip = l.NpPlayingPlay(st.Title)
	}
	chip := ui.Row(c.Key("np-chip")).Height(chipH).Radius(chipH/2).Background(fill1).
		Padding(0, 2, 0, 2).Gap(3).AlignItems(ui.Center).Label(st.Title).Tooltip(tip)
	chip.Children(func() {
		m.coverArt(c, "np-chip-art", 16, 4)
		m.eqBarsCompact(c, st.Playing)
		m.chipToggle(c, "np-chip-toggle", st.Playing, 18, 10)
	})
	return chip
}

// chipToggle is the play/pause button used by the mini player and the
// chip; size is the hit box, glyph the icon's edge.
func (m *nowPlayingModule) chipToggle(c *ui.Context, key string, playing bool, size, glyph float32) {
	b := ui.ButtonBase(c.Key(key)).Size(size, size).Radius(size / 2).Center().Label(playPauseText(loc(c), playing))
	b.Children(func() {
		ui.Icon(c, toggleSymbol(playing)).Size(glyph, glyph).TextColor(ink)
	})
	if b.Clicked() {
		m.send(mac.NPPlayPause)
	}
}

// eqBarsCompact is the chip's small equalizer: three 8pt bars, animated
// only while playing and only without Reduce Motion.
func (m *nowPlayingModule) eqBarsCompact(c *ui.Context, playing bool) {
	col := ink3
	if m.artColor.A > 0 {
		col = m.artColor
	}
	ui.Row(c.Key("np-eq-s")).Gap(2).AlignItems(ui.Center).Height(10).Children(func() {
		for i := range 3 {
			bar := ui.Box(c.Key(i)).Width(2).Radius(1).Background(col).AlignSelf(ui.End)
			h := float32(3)
			if playing && !c.Preferences().ReduceMotion {
				k := bar.Loop("eq", eqPeriods[i], ui.Bounce(ui.EaseInOut))
				wave := math.Sin(float64(float32(i)*0.6+k) * math.Pi)
				h = 3 + float32(wave*wave)*7
			} else if playing {
				h = 6
			}
			bar.Height(h)
		}
	})
}

// coverArt draws the artwork rounded and shadowed, the app icon as-is when
// no cover exists, or a music glyph.
func (m *nowPlayingModule) coverArt(c *ui.Context, key string, size, radius float32) {
	switch {
	case m.art != nil && !m.artIsIcon:
		ui.Image(c.Key(key), m.art).Size(size, size).Radius(radius).Fit(ui.Cover).
			Shadow(0, 6, 18, 0, ui.RGBA(0, 0, 0, 0.5))
	case m.art != nil:
		// The app icon already carries its own rounded shape.
		ui.Image(c.Key(key), m.art).Size(size, size)
	default:
		ui.Row(c.Key(key+"-icon")).Size(size, size).Radius(radius).Center().Background(fill1).Children(func() {
			ui.Icon(c, musicSymbol).Size(size*0.43, size*0.43).TextColor(ink3)
		})
	}
}

// cardMeta is the card's second line 「歌手 · 应用名」, either part alone
// when the other is unknown.
func (m *nowPlayingModule) cardMeta(st mac.NowPlayingState) string {
	app := ""
	if st.App != "" {
		app = m.host.appName(st.App)
	}
	switch {
	case st.Artist != "" && app != "":
		return st.Artist + " · " + app
	case app != "":
		return app
	default:
		return st.Artist
	}
}

// controlButton is one transport button: a white glyph, no fill, fill2
// while hovered; size 32 for prev/next, 38 for play/pause.
func (m *nowPlayingModule) controlButton(c *ui.Context, key string, icon *ui.SVG, label string, command mac.NowPlayingCommand, size, glyph float32) {
	b := ui.ButtonBase(c.Key(key)).Size(size, size).Center().Radius(size / 2).Label(label)
	b.HandleInput(func(ev ui.InputEvent) bool {
		// Claim the button's own pointer events: a card-level
		// HandleInput (tools page's press/reorder) would otherwise be
		// the innermost handler and swallow the press, so Clicked
		// would never fire. Keys stay free for ButtonBase's own
		// Enter/Space activation.
		return ev.Button == 0 && ev.Kind >= ui.InputPointerDown && ev.Kind <= ui.InputPointerMove
	})
	b.Children(func() {
		ui.Icon(c, icon).Size(glyph, glyph).TextColor(ink)
	})
	if b.Clicked() {
		m.send(command)
	}
}

// toggleSymbol is the play/pause glyph matching the current state.
func toggleSymbol(playing bool) *ui.SVG {
	if playing {
		return pauseFill
	}
	return playFill
}

// playPauseLabel names the action the button will take, in the language
// the app shows; the home widgets and tool cards, built outside a view,
// use it.
func playPauseLabel(playing bool) string {
	return playPauseText(locales.In(i18n.Locale()), playing)
}

// playPauseText is playPauseLabel in the language of l.
func playPauseText(l locales.Messages, playing bool) string {
	if playing {
		return l.NpPause()
	}
	return l.NpPlay()
}

// rememberArt stores a freshly decoded cover (or app icon) along with its
// average colour (the equalizer bars' tint), computed once per artwork,
// never per frame. img is the decoded source; isIcon says the picture is
// the app's icon rather than album art.
func (m *nowPlayingModule) rememberArt(img image.Image, isIcon bool) {
	m.art = ui.NewBitmap(img)
	m.artIsIcon = isIcon
	m.artColor = averageColor(img)
}

// forgetArt drops the current track's picture and colour; a new ArtID
// starts art-less until its cover decodes.
func (m *nowPlayingModule) forgetArt() {
	m.art, m.artIsIcon = nil, false
	m.artColor = ui.Color{}
}

// averageColor is the mean of the image's opaque pixels, premultiplied;
// a fully transparent image reports zero.
func averageColor(img image.Image) ui.Color {
	b := img.Bounds()
	var r, g, bl, n uint64
	step := max(1, min(b.Dx(), b.Dy())/24)
	for y := b.Min.Y; y < b.Max.Y; y += step {
		for x := b.Min.X; x < b.Max.X; x += step {
			cr, cg, cb, ca := img.At(x, y).RGBA()
			if ca == 0 {
				continue
			}
			// 未乘 alpha 的通道平均，半透明像素按其不透明度加权。
			r += uint64(cr) * uint64(ca)
			g += uint64(cg) * uint64(ca)
			bl += uint64(cb) * uint64(ca)
			n += uint64(ca)
		}
	}
	if n == 0 {
		return ui.Color{}
	}
	return ui.Color{
		R: uint8(r / n >> 8),
		G: uint8(g / n >> 8),
		B: uint8(bl / n >> 8),
		A: 255,
	}
}
