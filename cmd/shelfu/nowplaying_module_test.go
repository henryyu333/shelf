//go:build darwin

package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"image"
	"image/color"
	"image/png"
	"io"
	"testing"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"shelf/internal/mac"
	"shelf/internal/settings"
)

// stubNowPlaying builds a module whose helper is a pair of pipes: the
// test writes helper lines into it and reads the commands sent back, so
// no perl and no real MediaRemote are involved.
type stubNowPlaying struct {
	mod   *nowPlayingModule
	lines io.WriteCloser // test → module: helper stdout
	cmds  chan string    // module → test: commands the module sent
}

func newStubNowPlaying(t *testing.T, on bool) *stubNowPlaying {
	t.Helper()
	// module → test (commands the module sends)
	cmdR, cmdW := io.Pipe()
	// test → module (helper stdout lines)
	lineR, lineW := io.Pipe()
	s := &stubNowPlaying{lines: lineW, cmds: make(chan string, 8)}
	// Drain the command pipe on its own goroutine: a click handler writes
	// synchronously, so a full pipe must never block the UI thread.
	go func() {
		sc := bufio.NewScanner(cmdR)
		for sc.Scan() {
			s.cmds <- sc.Text()
		}
		close(s.cmds)
	}()
	host := mac.NewNowPlayingHostWithSource(func(ctx context.Context) (io.WriteCloser, io.ReadCloser, func() error, error) {
		// The stub child "exits" when the host is stopped, so Stop does
		// not wait out its one-second grace period.
		wait := func() error { lineR.Close(); return nil }
		return cmdW, lineR, wait, nil
	})
	s.mod = &nowPlayingModule{
		host: &shelf{settings: settings.Settings{IslandNowPlaying: on}},
	}
	s.mod.np = host
	if err := host.Start(); err != nil {
		t.Fatal(err)
	}
	s.mod.started = true
	t.Cleanup(func() {
		host.Stop()
		lineW.Close()
		cmdW.Close()
	})
	return s
}

// feed pushes one helper line and steps refreshes until the module has
// folded it in (the helper runs on its own goroutine, so the line is
// never there instantly).
func (s *stubNowPlaying) feed(t *testing.T, line string) mac.NowPlayingState {
	t.Helper()
	s.mod.refresh(time.Now())
	before := s.mod.snap
	if _, err := io.WriteString(s.lines, line+"\n"); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for {
		s.mod.refresh(time.Now())
		if s.mod.snap != before {
			return s.mod.last
		}
		if time.Now().After(deadline) {
			t.Fatalf("line %s did not change the module state", line)
		}
		time.Sleep(2 * time.Millisecond)
	}
}

func TestNPSnapOf(t *testing.T) {
	now := time.Now()
	if got := npSnapOf(mac.NowPlayingState{}, false, true, now); got != (npSnap{}) {
		t.Fatalf("no state must project to the zero snapshot, got %+v", got)
	}
	st := mac.NowPlayingState{Playing: true, Title: "T", Artist: "A", ArtID: "id",
		Elapsed: 10, Rate: 1, Received: now.Add(-2 * time.Second)}
	got := npSnapOf(st, true, true, now)
	want := npSnap{have: true, playing: true, title: "T", artist: "A", artID: "id", position: 12}
	if got != want {
		t.Fatalf("snapshot = %+v, want %+v", got, want)
	}
	// Panel closed: the same state is still live but the moving position
	// is not compared, so the tick does not wake once a second.
	closed := npSnapOf(st, true, false, now)
	if !closed.have || closed.position != 0 {
		t.Fatalf("closed-panel snapshot = %+v", closed)
	}
	if closed == got {
		t.Fatal("a position-bearing snapshot must differ from a closed one")
	}
	// A paused track keeps its frozen position but reports not playing.
	paused := npSnapOf(mac.NowPlayingState{Title: "T", Elapsed: 10, Received: now.Add(-time.Hour)}, true, true, now)
	if paused.playing || paused.position != 10 {
		t.Fatalf("paused snapshot = %+v", paused)
	}
}

func TestNowPlayingTrackAndSettingOff(t *testing.T) {
	s := newStubNowPlaying(t, true)
	s.feed(t, `{"ok":true,"isPlaying":true,"appState":1,"title":"T","artist":"A","duration":100,"elapsed":5}`)
	// Music never owns the collapsed notch: it feeds only the peek wings
	// and the panel card, so playing must report a track while paused does
	// too — the controls are most useful while paused.
	if !s.mod.hasTrack() {
		t.Fatal("playing music must still feed the peek wings and card")
	}
	// Paused: the peek wings and card stay for resuming.
	s.feed(t, `{"ok":true,"isPlaying":false,"appState":2,"title":"T","artist":"A","duration":100,"elapsed":5}`)
	if !s.mod.hasTrack() {
		t.Fatal("paused playback must still have a card")
	}
	// Turning the setting off hides both.
	s.mod.host.settings.IslandNowPlaying = false
	s.mod.tick(time.Now())
	if s.mod.hasTrack() {
		t.Fatal("the setting off must hide the card and the peek player")
	}
	if s.mod.np != nil || s.mod.started {
		t.Fatal("the setting off must stop the helper")
	}
}

func TestNowPlayingChipDrawsTrackAndToggle(t *testing.T) {
	s := newStubNowPlaying(t, true)
	s.feed(t, `{"ok":true,"isPlaying":true,"appState":1,"title":"夜曲","artist":"周杰伦","duration":100,"elapsed":5}`)
	a := &shelf{settings: settings.Settings{IslandNowPlaying: true}}
	a.modules = []module{s.mod, &retentionModule{host: a}}
	view := ui.NewTester(a.view, sheetW, sheetH)
	// The chip is a 22pt capsule: cover, bars and play/pause, named by the
	// track (the words themselves live in the tooltip).
	if _, ok := view.Find("夜曲"); !ok {
		t.Fatalf("the chip must carry the track's name; texts %v", view.Texts())
	}
	// The chip sits in the band above every page, not only the tray.
	a.tab = pageClipboard
	if _, ok := ui.NewTester(a.view, sheetW, sheetH).Find("夜曲"); !ok {
		t.Fatal("the clipboard page must keep the activity chip")
	}
	a.tab = pageTray
	// Clicking the play/pause button must send the toggle command to the
	// helper; the button is reachable by its accessibility label.
	if err := view.Click("暂停"); err != nil {
		t.Fatal(err)
	}
	s.expectCommand(t, "toggle")
	// The button flips on the press itself, before any helper line: a
	// paused source must never leave the glyph on "pause".
	if s.mod.last.Playing {
		t.Fatal("the press must optimistically pause the shown state")
	}
	next := ui.NewTester(a.view, sheetW, sheetH)
	if err := next.Click("播放"); err != nil {
		t.Fatalf("the button must now offer play: %v", err)
	}
	s.expectCommand(t, "toggle")
	// The second press optimistically resumes.
	if !s.mod.last.Playing {
		t.Fatal("the second press must optimistically resume the shown state")
	}
}

// TestPlayingPageDropsThePlayerChip: on the playing page the artwork and
// transport already show the track, so the band's right ear must not
// repeat it with a chip. Other activities keep their place and move up;
// leaving the page brings the chip back. A switch whose arrival page is
// the playing page counts too.
func TestPlayingPageDropsThePlayerChip(t *testing.T) {
	s := newStubNowPlaying(t, true)
	s.feed(t, `{"ok":true,"isPlaying":true,"appState":1,"title":"夜曲","artist":"周杰伦","duration":100,"elapsed":5}`)
	job := &convertJob{target: toJPEG, started: time.Now().Add(-time.Second)}
	a := &shelf{settings: settings.Settings{IslandNowPlaying: true}, level: 2, tab: pagePlaying}
	a.modules = []module{s.mod, &converter{host: a, job: job}}
	acts := a.liveActivities()
	if len(acts) != 1 {
		t.Fatalf("playing page activities = %d, want the conversion alone", len(acts))
	}
	if _, ok := acts[0].(*converter); !ok {
		t.Fatalf("the first activity is %T, want the conversion", acts[0])
	}
	// Leaving the page restores the chip, in module order.
	a.tab = pageTray
	acts = a.liveActivities()
	if len(acts) != 2 || acts[0] != liveActivity(s.mod) {
		t.Fatalf("tray-page activities = %v, want the player first", acts)
	}
	// The arrival page decides during a switch, not the page fading out.
	a.visible = true
	a.startPageSwitch(pageTray, pagePlaying)
	if acts := a.liveActivities(); len(acts) != 1 {
		t.Fatalf("a switch to the playing page kept %d chips", len(acts))
	}
}

func TestPeekGrowsIntoTheWingsForATrack(t *testing.T) {
	a := &shelf{settings: settings.Settings{IslandNowPlaying: true}, notchW: 180, notchH: 32}
	if w, h := a.peekSize(); w != 220 || h != 32+peekBody {
		t.Fatalf("idle peek = %.0fx%.0f, want 220x%d (notch +40/+%d)", w, h, 32+peekBody, peekBody)
	}
	a.np = &nowPlayingModule{host: a, have: true, last: mac.NowPlayingState{Title: "T"}}
	// 180 + 52 + 52 wide, and only 4pt below the notch band.
	if w, h := a.peekSize(); w != 284 || h != 36 {
		t.Fatalf("with a track the notch must grow two 52pt wings, got %.0fx%.0f, want 284x36", w, h)
	}
	// The resident strip already holds the wings, so hovering never
	// resizes the native window.
	if f := a.desiredFrame(); f.Width < 284+2*earR || f.Height < 36 {
		t.Fatalf("idle strip %dx%d cannot hold the wings", f.Width, f.Height)
	}
	// A file drag inside the notch band needs the drop pill — its
	// 「松开放入」 line wholly below the hardware notch. Outside the band
	// the drag only gets the band-height hint. Switching the feature off
	// collapses the wings even before the next tick stops the helper.
	a.dragging = true
	if w, h := a.peekSize(); w != 180+dropHintExtraW || h != 32 {
		t.Fatalf("a drag outside the band must show only the hint, got %.0fx%.0f", w, h)
	}
	a.dropHot = true
	if w, h := a.peekSize(); w != 180+dropPillExtraW || h != 32+dropPillBody {
		t.Fatalf("a drag inside the band must show the drop pill, got %.0fx%.0f", w, h)
	}
	a.dragging = false
	a.settings.IslandNowPlaying = false
	if w, _ := a.peekSize(); w != 220 {
		t.Fatalf("the wings must close when now playing is off, got %.0f", w)
	}
}

// The chevron bulge and the band-high drag hint must fit the resident
// strip without a native resize. The drop pill no longer needs to fit:
// on an incoming drag the window shrinks to the notch band first, then
// grows only once the pointer is inside the band (task D).
func TestPeekShapesFitTheIdleStrip(t *testing.T) {
	a := &shelf{notchW: 185, notchH: 38}
	strip := a.desiredFrame()
	w, h := a.peekSize()
	if int(w+0.5) > strip.Width || int(h+0.5) > strip.Height {
		t.Fatalf("idle peek %.0fx%.0f exceeds the strip %dx%d", w, h, strip.Width, strip.Height)
	}
	// An incoming drag deliberately remaps the surface to the band.
	a.dragging = true
	drag := a.desiredFrame()
	if drag == strip {
		t.Fatal("incoming drag kept the full strip — it must shrink to the band")
	}
	if drag.Height > max(32, a.notchH) {
		t.Fatalf("drag band %dpt tall exceeds the menu bar", drag.Height)
	}
	// The hint stays inside the band it draws for.
	w, h = a.peekSize()
	if int(w+0.5)+2*earR > drag.Width || int(h+0.5) > drag.Height {
		t.Fatalf("drag hint %.0fx%.0f exceeds its frame %+v", w, h, drag)
	}
}

func TestPeekWingsShowCoverAndBarsOnly(t *testing.T) {
	s := newStubNowPlaying(t, true)
	s.feed(t, `{"ok":true,"isPlaying":true,"appState":1,"title":"夜曲","artist":"周杰伦","duration":100,"elapsed":5}`)
	a := &shelf{
		settings:     settings.Settings{IslandNowPlaying: true},
		notchX:       500,
		notchW:       180,
		notchH:       32,
		peeking:      true,
		peekProgress: 1,
	}
	s.mod.host = a
	a.np = s.mod
	a.modules = []module{s.mod}
	view := ui.NewTester(a.view, sheetW, sheetH)
	// The wings carry no title, artist, clock or transport: the only
	// label is the accessibility one, which opens the shelf.
	for _, txt := range []string{"夜曲", "周杰伦", "暂停", "上一首", "下一首"} {
		for _, got := range view.Texts() {
			if got == txt {
				t.Fatalf("the hover wings drew %q; they show cover and bars only", txt)
			}
		}
	}
	const label = "正在播放：夜曲 · 展开 ShelfU"
	if _, ok := view.Find(label); !ok {
		t.Fatalf("the wings lack their accessibility label; texts %v", view.Texts())
	}
	// A press anywhere on the shell opens the shelf and sends nothing —
	// the empty middle, the cover and the bars alike (the wings have no
	// in-place controls).
	r, _ := view.Find(label)
	if err := view.Click(label); err != nil {
		t.Fatal(err)
	}
	if !a.visible {
		t.Fatal("a press on the wings must expand the panel")
	}
	select {
	case got := <-s.cmds:
		t.Fatalf("a wings press sent %q; the wings have no controls", got)
	case <-time.After(50 * time.Millisecond):
	}
	for _, p := range []struct {
		name string
		x, y float32
	}{
		{"cover", r.X + npWingsPad + npWingsArt/2, r.Y + r.H/2 - 2},
		{"bars", r.X + r.W - npWingsPad - eqBars*eqBarW/2 - (eqBars-1)*eqBarGap/2, r.Y + r.H/2 - 2},
	} {
		a.visible, a.progress = false, 0
		view.Frame()
		view.ClickAt(p.x, p.y)
		if !a.visible {
			t.Fatalf("a press on the %s must expand the panel too", p.name)
		}
		if a.tab != pageOverview || a.currentLevel() != 1 {
			t.Fatalf("a press on the %s landed on %s (level %d): every expand opens the overview",
				p.name, a.tab, a.currentLevel())
		}
	}
}

// TestExpandingWithMusicNeverOpensThePlayer: with a track loaded, every
// expand gesture — the hover wings, the shortcut's toggle and a plain open
// — lands on the overview. The player page is one tap further in, on the
// overview's track row.
func TestExpandingWithMusicNeverOpensThePlayer(t *testing.T) {
	s := newStubNowPlaying(t, true)
	s.feed(t, `{"ok":true,"isPlaying":true,"appState":1,"title":"夜曲","artist":"周杰伦","duration":100,"elapsed":5}`)
	a := &shelf{
		settings:     settings.Settings{IslandNowPlaying: true},
		notchX:       500,
		notchW:       180,
		notchH:       32,
		peeking:      true,
		peekProgress: 1,
	}
	s.mod.host = a
	a.np = s.mod
	a.modules = []module{s.mod}
	view := ui.NewTester(a.view, sheetW, sheetH)
	if err := view.Click("正在播放：夜曲 · 展开 ShelfU"); err != nil {
		t.Fatalf("the wings: %v", err)
	}
	if !a.visible || a.currentLevel() != 1 || a.tab != pageOverview {
		t.Fatalf("the wings opened %s (level %d, visible %v), want the overview",
			a.tab, a.currentLevel(), a.visible)
	}
	view.Frame()
	for _, txt := range []string{"播放进度", "音量"} {
		if _, ok := view.Find(txt); ok {
			t.Fatalf("the wings drew the player's %q: the overview must not carry the page", txt)
		}
	}
	// The shortcut closes and opens again: the second open is the overview
	// too, never the page the panel was last on.
	a.toggle()
	if a.visible {
		t.Fatal("the shortcut did not close the panel")
	}
	a.toggle()
	if !a.visible || a.tab != pageOverview || a.currentLevel() != 1 {
		t.Fatalf("the shortcut opened %s (level %d), want the overview", a.tab, a.currentLevel())
	}
	// From the overview the track row still opens the player.
	view.Frame()
	if err := view.Click("正在播放：夜曲"); err != nil {
		t.Fatalf("the overview's track row: %v", err)
	}
	if a.tab != pagePlaying || a.currentLevel() != 2 {
		t.Fatalf("the track row opened %s (level %d), want the player page", a.tab, a.currentLevel())
	}
}

// The wings' label drops the track when there is nothing to name, so the
// automation surface still reads 「展开 ShelfU」.
func TestPeekWingsLabelWithoutATitle(t *testing.T) {
	a := &shelf{
		settings:     settings.Settings{IslandNowPlaying: true},
		notchW:       180,
		notchH:       32,
		peeking:      true,
		peekProgress: 1,
	}
	a.np = &nowPlayingModule{host: a, have: true, last: mac.NowPlayingState{Playing: true}}
	view := ui.NewTester(a.view, sheetW, sheetH)
	if _, ok := view.Find("展开 ShelfU"); !ok {
		t.Fatalf("the untitled wings must still be labeled; texts %v", view.Texts())
	}
	if view.HasText("正在播放") {
		t.Fatal("a track without a title must not read 「正在播放：」")
	}
}

func TestPeekPressExpandsAndSendsNoCommand(t *testing.T) {
	s := newStubNowPlaying(t, true)
	s.feed(t, `{"ok":true,"isPlaying":true,"appState":1,"title":"T","artist":"A","duration":100,"elapsed":5}`)
	a := &shelf{
		settings:     settings.Settings{IslandNowPlaying: true},
		notchX:       500,
		notchW:       180,
		notchH:       32,
		peeking:      true,
		peekProgress: 1,
	}
	s.mod.host = a
	a.np = s.mod
	a.modules = []module{s.mod}
	_ = ui.NewTester(a.view, sheetW, sheetH) // draws the pill
	origReg, origUnreg := registerKey, unregisterKey
	registerKey = func(string, func()) error { return nil }
	unregisterKey = func(string) {}
	defer func() { registerKey, unregisterKey = origReg, origUnreg }()
	// WindowServer delivered the press to the menu bar (inside=false): the
	// player never saw it, so the observer expands and sends nothing.
	pill := a.peekPillRect()
	a.mouseDown(false, 0, mygo.Point{X: pill.X + pill.Width - 12, Y: pill.Y + pill.Height/2})
	if !a.visible {
		t.Fatal("a press on the pill must expand the panel")
	}
	select {
	case got := <-s.cmds:
		t.Fatalf("a pill press sent %q; the pill has no controls", got)
	case <-time.After(50 * time.Millisecond):
	}
}

// push writes one helper line and refreshes once it has been read. Unlike
// feed it does not require the visible snapshot to change: a contradicted
// optimistic state is folded in and then overridden, which is exactly the
// case under test.
func (s *stubNowPlaying) push(t *testing.T, line string) {
	t.Helper()
	if _, err := io.WriteString(s.lines, line+"\n"); err != nil {
		t.Fatal(err)
	}
	time.Sleep(40 * time.Millisecond)
	s.mod.refresh(time.Now())
}

func TestPlayPausePressHoldsUntilHelperAgrees(t *testing.T) {
	s := newStubNowPlaying(t, true)
	s.feed(t, `{"ok":true,"isPlaying":true,"appState":1,"title":"T","duration":100,"elapsed":5,"hasRate":true,"rate":1}`)
	s.mod.send(mac.NPPlayPause)
	s.expectCommand(t, "toggle")
	if s.mod.last.Playing {
		t.Fatal("the press must flip the shown state at once")
	}
	// A line the app emitted before processing the command still says
	// playing: it must not flip the button back to "pause".
	s.push(t, `{"ok":true,"isPlaying":true,"appState":1,"title":"T","duration":100,"elapsed":6,"hasRate":true,"rate":1}`)
	if s.mod.last.Playing {
		t.Fatal("a stale helper line overwrote the press")
	}
	// The helper catches up. Agreement clears the override.
	s.push(t, `{"ok":true,"isPlaying":false,"appState":2,"title":"T","duration":100,"elapsed":6,"hasRate":true,"rate":0}`)
	if s.mod.last.Playing {
		t.Fatal("the helper's paused state was not adopted")
	}
	// With the override settled, the next real state is trusted, so the
	// user's later play (from the app) is not masked.
	s.push(t, `{"ok":true,"isPlaying":true,"appState":1,"title":"T","duration":100,"elapsed":6,"hasRate":true,"rate":1}`)
	if !s.mod.last.Playing {
		t.Fatal("a settled override must not mask real playback")
	}
}

func TestNowPlayingActivityHiddenWithoutTrack(t *testing.T) {
	s := newStubNowPlaying(t, true)
	if s.mod.live() {
		t.Fatal("no track: the activity must not be live")
	}
	// A helper line with nothing playing is not a track either.
	s.feed(t, `{"ok":true,"isPlaying":false,"appState":2,"title":"","artist":""}`)
	if s.mod.live() {
		t.Fatal("an empty state must not show an activity")
	}
}

// NetEase publishes its covers as WebP; they must show as the cover, not
// fall through to a glyph.
func TestNowPlayingDecodesWebPArtwork(t *testing.T) {
	webp, err := base64.StdEncoding.DecodeString("UklGRhoAAABXRUJQVlA4TA0AAAAvAAAAEAcQERGIiP4HAA==")
	if err != nil {
		t.Fatal(err)
	}
	m := &nowPlayingModule{}
	m.decodeArt(mac.NowPlayingState{ArtID: "w", Art: webp, App: "com.netease.163music"})
	if m.art == nil || m.artIsIcon || m.artDone != "w" {
		t.Fatalf("WebP artwork: art=%v icon=%v done=%q, want the decoded cover", m.art, m.artIsIcon, m.artDone)
	}
}

func TestNowPlayingDecodeArtOncePerTrack(t *testing.T) {
	m := &nowPlayingModule{}
	pngBytes := testPNG(t)
	m.decodeArt(mac.NowPlayingState{ArtID: "a", Art: pngBytes})
	if m.art == nil || m.artIsIcon {
		t.Fatal("a valid PNG must decode as the cover")
	}
	if m.artDone != "a" || m.artState() != npArtCover {
		t.Fatalf("artDone = %q state = %d, want a/cover", m.artDone, m.artState())
	}
	// The same track never re-decodes.
	bitmap := m.art
	m.decodeArt(mac.NowPlayingState{ArtID: "a", Art: pngBytes})
	if m.art != bitmap {
		t.Fatal("the same track must reuse the decoded bitmap")
	}
	// A new track whose blob cannot be decoded drops the old artwork. Until
	// a line names the app it is retried rather than latched.
	m.decodeArt(mac.NowPlayingState{ArtID: "b", Art: []byte("not an image")})
	if m.art != nil || m.artDone == "b" {
		t.Fatalf("an undecodable blob without an app must clear the art and stay retryable: art=%v tried=%q", m.art, m.artDone)
	}
	// The helper declares the artwork missing (after its grace window) but
	// no line has named an app yet: nothing to draw and nothing is latched,
	// so a later line naming the bundle still gets its chance.
	m.decodeArt(mac.NowPlayingState{ArtID: "c", ArtMissing: true})
	if m.art != nil || m.iconFor != "" || m.artDone == "c" {
		t.Fatalf("artwork-less track without an app: art=%v iconFor=%q done=%q", m.art, m.iconFor, m.artDone)
	}
	var lookups []string
	m.newIcon = func(bundle string) *ui.Bitmap {
		lookups = append(lookups, bundle)
		return nil
	}
	m.decodeArt(mac.NowPlayingState{ArtID: "c", ArtMissing: true})
	if m.iconFor != "" || len(lookups) != 0 {
		t.Fatal("an app-less line must not look up or latch an icon")
	}
	m.decodeArt(mac.NowPlayingState{ArtID: "c", ArtMissing: true, App: "com.example.player"})
	if m.iconFor != "c" || len(lookups) != 1 || lookups[0] != "com.example.player" {
		t.Fatalf("the named app must be looked up once: iconFor=%q lookups=%v", m.iconFor, lookups)
	}
	m.decodeArt(mac.NowPlayingState{ArtID: "c", ArtMissing: true, App: "com.example.player"})
	if len(lookups) != 1 {
		t.Fatal("the fallback must be looked up once per track")
	}
	// Artwork that arrives late for a track still decodes (the helper's
	// artMissing deadline is not the reader's last word).
	m.decodeArt(mac.NowPlayingState{ArtID: "c", ArtMissing: true, Art: pngBytes})
	if m.art == nil || m.artDone != "c" {
		t.Fatal("late artwork must still decode and replace the fallback")
	}
	if m.artIsIcon || m.artState() != npArtCover {
		t.Fatal("late artwork must be shown as the cover, not an icon")
	}
	// A later missing line for the same track must not clear the cover.
	m.decodeArt(mac.NowPlayingState{ArtID: "c", ArtMissing: true})
	if m.art == nil {
		t.Fatal("a repeated missing line must not drop a decoded cover")
	}
	// An empty artwork without artMissing (title-first sample) is not
	// remembered as tried, so its own late blob decodes.
	m.decodeArt(mac.NowPlayingState{ArtID: "d"})
	if m.artDone == "d" {
		t.Fatal("an empty artwork without artMissing must not be remembered as done")
	}
	m.decodeArt(mac.NowPlayingState{ArtID: "d", Art: pngBytes})
	if m.art == nil {
		t.Fatal("late artwork must still decode")
	}
}

func TestNowPlayingFallsBackToTheAppIcon(t *testing.T) {
	icon := ui.NewBitmap(image.NewRGBA(image.Rect(0, 0, 8, 8)))
	m := &nowPlayingModule{newIcon: func(bundle string) *ui.Bitmap {
		if bundle != "com.example.player" {
			t.Fatalf("looked up %q", bundle)
		}
		return icon
	}}
	m.decodeArt(mac.NowPlayingState{ArtID: "x", ArtMissing: true, App: "com.example.player"})
	if m.art != icon || !m.artIsIcon || m.artState() != npArtIcon {
		t.Fatalf("app icon fallback: art=%p isIcon=%v state=%d", m.art, m.artIsIcon, m.artState())
	}
}

func TestNowPlayingStopsWhenHelperDies(t *testing.T) {
	s := newStubNowPlaying(t, true)
	s.feed(t, `{"ok":true,"isPlaying":true,"appState":1,"title":"T","elapsed":1}`)
	if !s.mod.playing() {
		t.Fatal("state must be live")
	}
	// The helper's stdout ends: the module must notice, drop the state
	// and schedule a restart rather than showing a frozen track.
	if err := s.lines.Close(); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for !s.mod.np.Dead() {
		if time.Now().After(deadline) {
			t.Fatal("host did not notice the helper exit")
		}
		time.Sleep(2 * time.Millisecond)
	}
	s.mod.tick(time.Now())
	if s.mod.playing() || s.mod.np != nil || s.mod.started {
		t.Fatal("a dead helper must clear the state and stop the module")
	}
	// The restart is delayed, not immediate (no process churn).
	if s.mod.retryAt.IsZero() {
		t.Fatal("a restart must be scheduled with a delay")
	}
}

func TestNowPlayingStartFailureIsRetriedLater(t *testing.T) {
	m := &nowPlayingModule{host: &shelf{settings: settings.Settings{IslandNowPlaying: true}}}
	m.newHost = func() *mac.NowPlayingHost {
		return mac.NewNowPlayingHostWithSource(func(context.Context) (io.WriteCloser, io.ReadCloser, func() error, error) {
			return nil, nil, nil, errors.New("no helper here")
		})
	}
	now := time.Now()
	m.tick(now)
	if m.started || m.np != nil {
		t.Fatal("a failed start must not leave a host behind")
	}
	if m.retryAt.IsZero() || !m.retryAt.After(now) {
		t.Fatal("a failed start must schedule a retry")
	}
	// The next ticks inside the delay must not even try again.
	tries := 0
	m.newHost = func() *mac.NowPlayingHost {
		tries++
		return mac.NewNowPlayingHostWithSource(func(context.Context) (io.WriteCloser, io.ReadCloser, func() error, error) {
			return nil, nil, nil, errors.New("still nothing")
		})
	}
	m.tick(now.Add(npRetryDelay / 2))
	if tries != 0 {
		t.Fatal("a tick inside the retry delay must not restart the helper")
	}
	m.tick(now.Add(npRetryDelay + time.Second))
	if tries != 1 {
		t.Fatal("a tick past the delay must retry once")
	}
	if m.hasTrack() {
		t.Fatal("a module that never started must stay silent")
	}
}

func TestPlayPauseAffordances(t *testing.T) {
	if toggleSymbol(true) != pauseFill || toggleSymbol(false) != playFill {
		t.Fatal("the toggle glyph must match the playing state")
	}
	if playPauseLabel(true) != "暂停" || playPauseLabel(false) != "播放" {
		t.Fatal("the toggle label must name the action it will take")
	}
}

// expectCommand checks the next command the module sent to the helper.
func (s *stubNowPlaying) expectCommand(t *testing.T, want string) {
	t.Helper()
	select {
	case got, ok := <-s.cmds:
		if !ok {
			t.Fatalf("the command pipe closed before %q arrived", want)
		}
		if got != want {
			t.Fatalf("the module sent %q, want %q", got, want)
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("no %q command arrived", want)
	}
}

// testPNG is a tiny valid PNG for the artwork decode path.
func testPNG(t *testing.T) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 4, 4))
	for x := range 4 {
		for y := range 4 {
			img.Set(x, y, color.RGBA{R: 200, G: 30, B: 40, A: 255})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}
