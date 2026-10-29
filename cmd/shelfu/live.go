//go:build darwin

package main

// Realtime activities on the notch: charging, headphones and downloads.
// They share one queue of transient events (one at a time, arrival order)
// that take over the peek shape and hand it back afterwards. A download in
// progress is not an event: it holds the wings for as long as it runs and
// outranks playing music. The shell itself (notch, peek, expansion) is
// untouched; this file only decides which shape is shown and what it says.
//
// Queue rules, unit-tested:
//   - one event at a time, in arrival order;
//   - every event owns its display duration, and the clock starts when it
//     reaches the screen, so a notice queued behind another keeps its full
//     hold;
//   - a notice that waited longer than liveQueueMaxWait is stale and is
//     dropped, so a long queue cannot replay an old plug or low battery.
//     Download results are exempt: they are the outcome of the user's own
//     action, not a transient system state;
//   - a download result that arrives while another one still waits in the
//     queue joins it, so one scan's files are one notice;
//   - a status capsule already on screen owns the shape: the head event
//     waits for it instead of running its hold unseen;
//   - a capsule that comes up over a notice already running pauses its
//     remaining hold, so the words are still readable afterwards;
//   - opening the panel cancels what is queued, so nothing pops afterwards;
//   - a switch that is off drops its events at the door.

import (
	"path/filepath"
	"strings"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/i18n"

	"shelf/locales"
)

// The three highlight colours the activities use inside the black shell.
var (
	liveGreen = success
	liveBlue  = accent
	liveRed   = danger
)

const (
	// liveWingW is how far each side grows for charging and downloads: the
	// notch only gets wider, never taller than its band plus 4. The middle
	// of the shell sits behind the camera and has no pixels, so the whole
	// right-hand group (bar + 10pt gap + a three-digit 13pt number, about
	// 26pt) must sit inside the visible wing after its 14pt inset. With the
	// 36pt bar that is 14 + 36 + 10 + 26 = 86 of 112, leaving room before
	// the camera housing; the old 66pt bar filled the wing to the last point
	// and slid under the notch on a real MacBook (10-28 photo).
	liveWingW   = 112
	liveWingPad = 14
	// islandLiveWingW is the same side on the island: its whole shell has
	// pixels, so the content no longer has to fit inside one visible wing
	// beside a camera housing, and the design preview's 88pt is enough
	// (120 + 2×88 wide, 30 + 4 high).
	islandLiveWingW = 88
	// liveBarW/liveBarH are the charge bar's size: short enough that the
	// bar never reaches under the camera housing.
	liveBarW = 36
	liveBarH = 6

	liveHoldCharge   = 3 * time.Second
	liveHoldFull     = 3 * time.Second
	liveHoldLow      = 5 * time.Second
	liveHoldHeadset  = 3500 * time.Millisecond
	liveHoldUnplug   = 2 * time.Second
	liveHoldDownload = 4 * time.Second
	// liveHoldDownloadMax bounds a batch: every further file in the same
	// notice adds a second to read, up to this.
	liveHoldDownloadMax = 8 * time.Second

	// liveQueueMaxWait bounds how long a notice may wait behind others.
	// Every event's own hold now starts on screen; without a bound, a
	// burst (several downloads finishing at once) could replay a plug
	// from half a minute ago after the newer notices.
	liveQueueMaxWait = 15 * time.Second
)

// liveKind names one transient event.
type liveKind int

const (
	liveCharging liveKind = iota
	liveCharged
	liveLow
	liveHeadset
	liveHeadsetOff
	liveDownloadDone
)

// wings reports whether the event uses the wide two-wing shape instead of
// the capsule under the notch.
func (k liveKind) wings() bool {
	return k == liveCharging || k == liveCharged || k == liveLow
}

// liveEvent is one queued activity.
type liveEvent struct {
	kind   liveKind
	label  string   // 正在充电 / 已充满 / 电量低 / device or file name / 3 个文件
	levels []string // 左 80% / 右 78% / 盒 50%, when the device reports them
	pct    int
	// paths are the downloaded files, for the completion capsule's click:
	// one scan's completions share one event.
	paths []string
	token string // device UID: a late battery reading enriches this event
	width float32
	// hold is how long the event stays once it reaches the screen;
	// queuedAt is when it arrived, used only to drop a stale backlog.
	hold     time.Duration
	queuedAt time.Time
}

// liveQueue is the one-at-a-time event queue.
type liveQueue struct {
	queued []liveEvent
	cur    liveEvent
	until  time.Time
	shown  bool
	// coveredAt is when a status capsule first covered the shown event: its
	// remaining hold waits while it is set, so the words are still readable
	// (and, for a download result, clickable) once the capsule folds away.
	coveredAt time.Time
}

func (a *shelf) liveEnabled(kind liveKind) bool {
	s := a.settings.LiveActivities
	switch kind {
	case liveCharging, liveCharged, liveLow:
		return s.Charging
	case liveHeadset, liveHeadsetOff:
		return s.Headphones
	case liveDownloadDone:
		return s.Downloads
	}
	return false
}

// pushLive queues one activity. Events posted while the panel is open are
// dropped: the design shows nothing transient over an open panel, and a
// late popup after closing is not what the event meant.
func (a *shelf) pushLive(ev liveEvent, hold time.Duration, now time.Time) {
	if !a.liveEnabled(ev.kind) || a.visible {
		return
	}
	if ev.kind == liveDownloadDone && a.mergeDownloadDone(ev.paths) {
		return
	}
	ev.hold, ev.queuedAt = hold, now
	baseW, _ := a.anchorSize()
	ev.width = liveCapsuleWidth(a.appLoc(), ev, baseW)
	a.live.queued = append(a.live.queued, ev)
	if a.win != nil {
		a.win.Invalidate()
	}
}

// mergeDownloadDone joins paths into the download result already waiting in
// the queue, so a second completion arriving before the first is shown
// updates that notice instead of adding another. A result already on
// screen is not extended: its words are being read.
func (a *shelf) mergeDownloadDone(paths []string) bool {
	for i := range a.live.queued {
		ev := &a.live.queued[i]
		if ev.kind != liveDownloadDone {
			continue
		}
		ev.paths = mergePaths(ev.paths, paths)
		ev.label = downloadDoneLabel(a.appLoc(), ev.paths)
		ev.hold = downloadDoneHold(len(ev.paths))
		baseW, _ := a.anchorSize()
		ev.width = liveCapsuleWidth(a.appLoc(), *ev, baseW)
		if a.win != nil {
			a.win.Invalidate()
		}
		return true
	}
	return false
}

// pushDownloaded posts the completions of one scan as one notice: one file
// names itself, several say how many, and the hold grows a little per file.
func (a *shelf) pushDownloaded(paths []string, now time.Time) {
	paths = mergePaths(nil, paths)
	if len(paths) == 0 {
		return
	}
	a.pushLive(liveEvent{
		kind: liveDownloadDone, label: downloadDoneLabel(a.appLoc(), paths), paths: paths,
	}, downloadDoneHold(len(paths)), now)
}

// downloadDoneLabel is the completion capsule's noun: the file's own name,
// or the count when one scan finished several.
func downloadDoneLabel(l locales.Messages, paths []string) string {
	if len(paths) == 1 {
		return filepath.Base(paths[0])
	}
	return l.LiveFiles(len(paths))
}

// downloadDoneHold is the reading time for one completion notice: four
// seconds for the first file, a second per further file, capped so a big
// batch cannot hold the notch all afternoon.
func downloadDoneHold(n int) time.Duration {
	if n < 1 {
		n = 1
	}
	return min(liveHoldDownload+time.Duration(n-1)*time.Second, liveHoldDownloadMax)
}

// mergePaths appends the paths not already in list, keeping arrival order.
func mergePaths(list, add []string) []string {
	seen := make(map[string]bool, len(list)+len(add))
	for _, p := range list {
		seen[p] = true
	}
	for _, p := range add {
		if p == "" || seen[p] {
			continue
		}
		seen[p] = true
		list = append(list, p)
	}
	return list
}

// clearLive drops everything queued or showing; opening the panel calls it
// so no event survives behind it.
func (a *shelf) clearLive() {
	a.live = liveQueue{}
}

// liveNow advances the queue and returns the event to draw. allow is false
// while the panel is open or a file drag owns the notch; nothing is
// started then, and the shown event is hidden (not cancelled: its hold
// still runs). A status capsule that is up owns the shape in the same way:
// the head event is kept for later rather than spending its hold unseen
// behind the capsule's words. A capsule that comes up over a notice already
// running pauses that notice's remaining hold instead of eating it: the
// download result a 已复制 covers is still on screen when the capsule ends.
func (a *shelf) liveNow(now time.Time, allow bool) (liveEvent, bool) {
	q := &a.live
	switch {
	case q.shown && a.capsuleUp(now) && (!q.coveredAt.IsZero() || now.Before(q.until)):
		// Covered: the clock rests until the capsule is gone. Once the
		// notice is already covered it stays paused even past its frozen
		// deadline: the capsule outlasting the hold must not hand the
		// notice back while its words are still hidden.
		if q.coveredAt.IsZero() {
			q.coveredAt = now
		}
		return liveEvent{}, false
	case q.shown && !q.coveredAt.IsZero():
		// The capsule is gone: give back the time it held the shape.
		q.until = q.until.Add(now.Sub(q.coveredAt))
		q.coveredAt = time.Time{}
	}
	if q.shown {
		if allow && now.Before(q.until) && a.liveEnabled(q.cur.kind) {
			return q.cur, true
		}
		q.shown = false
		if !now.Before(q.until) {
			q.cur = liveEvent{}
		}
	}
	if !allow || a.capsuleUp(now) {
		return liveEvent{}, false
	}
	for len(q.queued) > 0 {
		ev := q.queued[0]
		q.queued = q.queued[1:]
		if !a.liveEnabled(ev.kind) {
			continue // its switch went off after it was queued
		}
		// A download result is not a transient state: the file the user
		// asked for finished, whether or not a busy queue delayed the
		// words. Every other kind is stale after the wait bound.
		if ev.kind != liveDownloadDone && now.Sub(ev.queuedAt) > liveQueueMaxWait {
			continue
		}
		// The hold starts now, not at arrival, so waiting behind another
		// notice never eats this one's own duration.
		q.cur, q.until, q.shown = ev, now.Add(ev.hold), true
		return ev, true
	}
	return liveEvent{}, false
}

// liveEnrich attaches a late battery reading to the event that announced
// the same device, whether it is on screen or still queued.
func (a *shelf) liveEnrich(token string, levels []string) {
	if token == "" {
		return
	}
	apply := func(ev *liveEvent) bool {
		if ev.token != token || ev.kind != liveHeadset {
			return false
		}
		ev.levels = levels
		baseW, _ := a.anchorSize()
		ev.width = liveCapsuleWidth(a.appLoc(), *ev, baseW)
		return true
	}
	changed := false
	if a.live.shown && apply(&a.live.cur) {
		changed = true
	}
	for i := range a.live.queued {
		if apply(&a.live.queued[i]) {
			changed = true
		}
	}
	if changed && a.win != nil {
		a.win.Invalidate()
	}
}

// liveShape is the shell the event needs: the wide wings or a capsule.
func (a *shelf) liveShape(ev liveEvent) (w, h float32) {
	baseW, baseH := a.anchorSize()
	if ev.kind.wings() {
		return baseW + 2*float32(a.liveWingW()), baseH + npWingsBody
	}
	w = ev.width
	if w <= 0 {
		w = liveCapsuleWidth(a.appLoc(), ev, baseW)
	}
	return w, float32(a.capsuleBodyH())
}

// liveDownloadDoneShape is the completion capsule's box, computed from the
// event exactly as liveShape does (used by the click test).
func liveCapsuleWidth(l locales.Messages, ev liveEvent, baseW float32) float32 {
	minW := float32(280)
	switch ev.kind {
	case liveHeadset:
		minW = 290
		if len(ev.levels) > 0 {
			minW = 330
		}
	case liveHeadsetOff:
		minW = 250
	}
	w := liveIconWidth(ev.kind) + 2*fbPadX
	first := false
	add := func(text string, size float32) {
		if text == "" {
			return
		}
		if !first {
			w += fbGap
		}
		first = false
		w += liveTextWidth(text, size)
	}
	switch ev.kind {
	case liveHeadset, liveHeadsetOff:
		add(ev.label, 12)
		for _, lv := range ev.levels {
			add(lv, 12)
		}
		if ev.kind == liveHeadsetOff {
			add(l.LiveDisconnected(), 12)
		} else if len(ev.levels) == 0 {
			add(l.LiveConnected(), 12)
		}
	case liveDownloadDone:
		add(l.LiveDownloaded(), 12)
		add(ev.label, 12)
		w += fbGap + 7 // the green dot
	}
	w = max(w, minW, baseW+60)
	return min(w, fbMaxW)
}

// liveTextWidth estimates a capsule line at the shell's 12pt weight; the
// layout then fits the real font inside the width this reserved.
func liveTextWidth(s string, size float32) float32 {
	var w float32
	for _, r := range s {
		switch {
		case r > 0x2E80: // CJK and full-width punctuation
			w += size
		default:
			w += size * 0.56
		}
	}
	return w
}

// liveIconWidth is the leading glyph's width in the capsule.
func liveIconWidth(k liveKind) float32 {
	switch k {
	case liveHeadset:
		return 18
	case liveHeadsetOff:
		return 7
	case liveDownloadDone:
		return 13
	}
	return 13
}

// openDownloaded puts the finished files into the tray and opens its page —
// the completion capsule's click. A merged notice adds every file it names.
func (a *shelf) openDownloaded(ev liveEvent) {
	if len(ev.paths) == 0 {
		return
	}
	if a.store == nil {
		return
	}
	if err := a.store.AddFiles(ev.paths); err != nil {
		a.report(err)
		return
	}
	a.clearLive()
	a.show(false)
	a.setTab(pageTray)
	if a.currentLevel() == 1 {
		a.setLevel(2)
	}
}

// liveAllow is when the notch may show a transient activity: the panel is
// closed (or fully folded) and no incoming file drag owns the shape.
func (a *shelf) liveAllow() bool {
	return !a.visible && a.progress <= 0.01 && !(a.dragging && !a.ownDrag)
}

// liveUp reports whether a live activity currently occupies the peek
// shape, for the mouse gate. It does not advance the queue.
func (a *shelf) liveUp() bool {
	return a.live.shown || a.downloadRunning()
}

// liveClickable reports whether the drawn shape is the completion capsule,
// whose click belongs to the view and not to the shell's open handler.
func (a *shelf) liveClickable(now time.Time) bool {
	if !a.live.shown || now.After(a.live.until) {
		return false
	}
	return a.live.cur.kind == liveDownloadDone
}

// downloadFile is one browser download that is still in flight.
type downloadFile struct {
	temp   string // the temporary file/bundle
	name   string // final file name
	target string // final path
	done   int64
	total  int64 // 0 when the browser does not say
	mod    time.Time
}

// downloadProgress is the persistent activity's snapshot.
type downloadProgress struct {
	file  downloadFile
	count int // further downloads beyond the newest
}

// downloadRunning reports whether a download currently owns the wings.
func (a *shelf) downloadRunning() bool {
	return a.dl != nil && a.dl.active() && !a.visible && a.settings.LiveActivities.Downloads
}

// liveShapeForDownload is the wings box for a running download.
func (a *shelf) downloadShape() (w, h float32) {
	baseW, baseH := a.anchorSize()
	return baseW + 2*float32(a.liveWingW()), baseH + npWingsBody
}

// liveStatusLine is the one-line description used for accessibility and
// the report; it keeps the event's wording in one place.
func (ev liveEvent) statusLine() string {
	return ev.statusLineIn(locales.In(i18n.Locale()))
}

// statusLineIn is statusLine in the language of l.
func (ev liveEvent) statusLineIn(l locales.Messages) string {
	switch ev.kind {
	case liveDownloadDone:
		return l.LiveStatusDownloaded(ev.label)
	case liveHeadsetOff:
		return l.LiveStatusOff(ev.label)
	case liveHeadset:
		return strings.Join(append([]string{ev.label}, ev.levels...), " ")
	case liveCharging:
		return l.LiveStatusCharging(ev.pct)
	case liveCharged:
		return l.LiveStatusCharged(ev.pct)
	case liveLow:
		return l.LiveStatusLow(ev.pct)
	}
	return ev.label
}

// announceLive posts one of the power/headset events. Small helper so the
// modules share the timestamp and the sweep.
func (a *shelf) announceLive(ev liveEvent, hold time.Duration) {
	a.pushLive(ev, hold, time.Now())
}

// mygoRunOnMain is the seam the battery lookup uses to deliver its result.
var mygoRunOnMain = mygo.RunOnMain
