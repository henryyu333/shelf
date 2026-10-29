//go:build darwin

package main

// Completion feedback: the panel's bottom toast only exists while the panel
// is open, so anything that finishes with the panel closed grows a capsule
// out of the hardware notch instead — a 16pt status dot and the toast's own
// words inside the same black shell. The capsule is a peek state of the
// resident window, not a window of its own: the shell, the spring and the
// pass-through rules are the ones the hover pill already uses.
//
// Status colours are the one exception to the panel's single accent: they
// appear only inside the capsule's black shell — the dot, never a border
// or a glow past its edge — and nowhere else.

import (
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"

	"shelf/locales"
)

// Status colours come from theme.go (success, warning, danger) and appear
// only inside the capsule's dot.
var (
	fbGreen  = success
	fbOrange = warning
	fbRed    = danger
)

const (
	fbHoldOK  = 1600 * time.Millisecond // a success stays this long after growing
	fbHoldErr = 2200 * time.Millisecond // a failure the same, after its shake
	fbShake   = 420 * time.Millisecond  // one decaying left-right shake
	fbShakeX  = 6                       // its first swing, in points
	fbBreathe = 1600 * time.Millisecond // one breath of a running job's status dot
	fbMaxW    = 380                     // the capsule ellipsizes past this width
	fbDot     = 16                      // the status dot
	fbGap     = 7                       // dot to text
	fbPadX    = 12                      // text to capsule edge
	fbBody    = 22                      // capsule height below the notch band
	fbExpand  = durCapsule              // the grow-in the hold is measured from
)

// The shell's springs live in theme.go with the rest of the tokens.

// openOvershoot is the room the frame keeps for a spring that is still on
// its way: the widest spring it must cover is the capsule's Bounce 0.28,
// which peaks near 4%, plus a frame's worth of catch-up. The panel itself
// opens on Bounce 0.18, which peaks near 1.1% (panelOpenOvershoot).
const (
	openOvershoot      = 0.045
	panelOpenOvershoot = 0.015
)

// peekKind is which content the peek shell is showing: the chevron or the
// 松开放入 pill, the hover wings, or the completion capsule. Recorded
// each revealed frame so a fold can keep drawing that same content while
// it shrinks away.
type peekKind int

const (
	peekChevron peekKind = iota
	peekDrop
	peekWings
	peekCapsule
	// peekHint is the drag-in-flight hint: a notch-high shell, a little
	// wider, with no glyph inside — it must not outgrow the menu bar.
	peekHint
	// peekReturn is a desktop-pinned card held over the notch: the same
	// pill as the drop shape, inviting 松开放回 (pin.go pinReturning).
	peekReturn
	// The realtime activities (live.go): the wide charging or download
	// wings, and the capsule for headphones or a finished download.
	peekLiveWings
	peekLiveCapsule
	peekDownload
)

// shellMotion is what view() drew last: the drawn shell's size, for the
// window frame to hold while the springs are in flight, and whether a
// spring is still on its way (the frame keeps overshoot room until then).
type shellMotion struct {
	w, h, peekW, peekH float32
	shakeX             float32 // the error shake's current offset: the capsule's hit box follows it
	openK              float32 // the drawn open interpolation: 0 = folded to the peek shape
	levelK             float32 // the drawn level interpolation: 0 = island, 1 = sheet
	peekK              float32 // the drawn peek interpolation, for the close clamp
	peekKind           peekKind
	openMoving         bool // the opening spring has not come to rest
	sizeMoving         bool // a peek-shape morph has not come to rest
	peek               bool // w and h are the peek shape's, not the panel's
}

// feedback is the capsule's state: what it says, for how long, and how wide
// the last frame measured that text.
type feedback struct {
	text       string
	kind       statusKind
	live       bool // the words are a running job's progress, refreshed per frame
	shownAt    time.Time
	until      time.Time
	hold       time.Duration // visible span retained until the first drawn frame
	played     string        // text that already ran its full span: never grows the capsule again
	pillW      float32
	drawn      bool      // drawn in the last frame (the window's command-line tests read it)
	retracting bool      // the shell is folding back from the capsule
	armed      bool      // its clock (hold, shake) has started on the first drawn frame
	pausedAt   time.Time // set while hover or a drag holds the peek shape: the hold rests
	liveToken  any       // the running job the live line belongs to: *convertJob or a screen-OCR round id
}

// up reports whether the capsule has something to say now.
func (f *feedback) up(now time.Time) bool {
	if f.text == "" {
		return false
	}
	return (!f.armed && f.hold > 0) || !f.pausedAt.IsZero() || f.until.IsZero() || now.Before(f.until)
}

// color is the status colour of what it says.
func (f *feedback) color() ui.Color {
	switch {
	case f.live:
		return fbOrange
	case f.kind == statusErr:
		return fbRed
	case f.kind == statusInfo:
		return ink2
	}
	return fbGreen
}

// glyph is the sign inside the dot. A running job has no verdict yet: the
// dot holds an ellipsis, as the design's waiting state does. A plain
// information dot carries no sign.
func (f *feedback) glyph() string {
	switch {
	case f.live:
		return "…"
	case f.kind == statusErr:
		return "!"
	case f.kind == statusInfo:
		return ""
	}
	return "✓"
}

// holdFloor is the shortest hold the capsule gives a toast after growing
// out, so even a deadline-free or short-deadline status stays readable.
func (k statusKind) holdFloor() time.Duration {
	if k == statusErr {
		return fbHoldErr
	}
	return fbHoldOK
}

// fbErrMinHold is the floor for a deadline-free error (report()): long
// enough to read and to finish the shake.
const fbErrMinHold = 4 * time.Second

// show turns a toast into a capsule. The caller's deadline wins: the
// capsule only floors it so the grow-out plus a readable hold always fit
// (and a deadline-free error stays at least fbErrMinHold). The hold is not
// counted from here: the panel may still be folding away, so the capsule
// arms its own clock the first frame it actually draws (see arm).
func (f *feedback) show(text string, kind statusKind, until time.Time, now time.Time) {
	// The capsule is one glanceable line: a multi-line toast (stacked
	// notices) shows its first line, never a mid-word cut.
	if i := strings.IndexByte(text, '\n'); i >= 0 {
		text = text[:i]
	}
	if f.text != text {
		f.pillW = 0
		f.played = "" // new words are unplayed
	}
	f.text, f.kind, f.live = text, kind, false
	f.shownAt = now
	f.hold = max(until.Sub(now), fbExpand+kind.holdFloor())
	if until.IsZero() && kind == statusErr {
		f.hold = max(f.hold, fbErrMinHold)
	}
	f.until = now.Add(f.hold)
	f.armed = false
	f.pausedAt = time.Time{}
}

// arm starts the capsule's own clock when its first frame draws — the hold
// and the shake are measured from the grow-out the user can see, not from
// the post, which can precede the end of the panel's close or a long hover.
// The caller's deadline still wins when it reaches further.
func (f *feedback) arm(now time.Time) {
	if f.armed || f.live {
		return
	}
	f.shownAt, f.until = now, now.Add(f.hold)
	f.armed = true
}

// follow points the capsule at the running job, so a panel that is
// closed while a job runs keeps reporting it — the conversion job, or the
// capture+recognition round of 框选文字 once it runs past its quiet
// window. A toast posted meanwhile keeps the capsule for its whole span —
// a job never stomps a verdict the user has not seen; the progress
// returns after it.
func (f *feedback) follow(a *shelf, now time.Time) {
	text, token, live := a.liveStatus(now)
	if !live {
		if f.liveToken != nil {
			f.liveToken = nil
			if f.live {
				f.live, f.text = false, ""
			}
		}
		return
	}
	if !f.live && f.up(now) {
		return // a posted toast holds the capsule for its whole span
	}
	if f.live && f.liveToken == token && f.text == text {
		return // the step is unchanged: keep the measured width, redraw nothing
	}
	// Re-entering after a transient toast has expired keeps the same
	// job but must drop the toast's stale deadline — live has none.
	f.until = time.Time{}
	if f.liveToken != token {
		f.shownAt = now
	}
	f.liveToken = token
	if f.text != text {
		f.pillW = 0
	}
	f.text, f.kind, f.live = text, statusOK, true
	f.pausedAt = time.Time{}
}

// liveStatus is the running job the capsule should show now: a conversion
// job while one is live, else a screen-OCR round that outlived its quiet
// window. Pure apart from the job reads; unit-tested.
func (a *shelf) liveStatus(now time.Time) (text string, token any, live bool) {
	if a.conv != nil && a.conv.job != nil && a.conv.live() {
		if text, ok := convertProgress(a.appLoc(), a.conv.job); ok {
			return text, a.conv.job, true
		}
	}
	return a.ocrLiveStatus(now)
}

// capsule is the shape the notch grows into: as wide as the text needs, but
// never narrower than the notch plus 60, and no wider than fbMaxW.
func (f *feedback) capsule(baseW, baseH float32) (w, h float32) {
	text := f.pillW
	if text == 0 {
		text = measureFallback(f.text)
	}
	w = text + fbDot + fbGap + 2*fbPadX
	w = min(max(w, baseW+60), fbMaxW)
	return w, baseH + fbBody
}

// measure is the width the text needs at the capsule's 12pt semibold.
func (f *feedback) measure(c *ui.Context) float32 {
	if f.text == "" {
		return 0
	}
	w, _ := c.MeasureText(0, ui.Span{Text: f.text, Size: 12, Weight: 600})
	return w
}

// measureFallback keeps the capsule sane before any frame has measured the
// text, and in tests that build a shelf without a view.
func measureFallback(text string) float32 {
	var w float32
	for _, r := range text {
		switch {
		case r > 0x2E80: // CJK and full-width punctuation
			w += 12
		default:
			w += 6.5
		}
	}
	return w
}

// shake is the capsule's horizontal offset now: one decaying left-right
// pass, left then right, that starts once it has grown out.
func (f *feedback) shake(now time.Time, reduceMotion bool) float32 {
	if f.kind != statusErr || f.live || reduceMotion {
		return 0
	}
	d := now.Sub(f.shownAt.Add(fbExpand))
	if d <= 0 || d >= fbShake {
		return 0
	}
	t := float32(d) / float32(fbShake)
	// One cycle, left first, its second swing at about half height —
	// the design's decaying keyframes in one pass.
	return -fbShakeX * (1 - 0.6*t) * float32(math.Sin(2*math.Pi*float64(t)))
}

// accent is the strength of the status dot's colour inside the shell: a
// steady 0.9 for a verdict, a slow breath (0.3–0.9) for a job that is
// still running. Plain information carries no accent: the neutral dot
// speaks for itself.
func (f *feedback) accent(now time.Time, reduceMotion bool) float32 {
	if !f.live && f.kind == statusInfo {
		return 0
	}
	if !f.live || reduceMotion {
		return 0.9
	}
	k := float32((now.UnixNano() % int64(fbBreathe))) / float32(fbBreathe)
	return 0.3 + 0.6*(0.5-0.5*float32(math.Cos(2*math.Pi*float64(k))))
}

// animating reports whether the capsule still moves by itself — only the
// failure shake needs per-frame redraws. A running job's dot instead
// breathes on the timed beats settle() hands to After, so progress costs
// a frame four times per breath, not one per display frame.
func (f *feedback) animating(now time.Time, reduceMotion bool) bool {
	if reduceMotion {
		return false
	}
	d := now.Sub(f.shownAt.Add(fbExpand))
	return !f.live && f.kind == statusErr && d >= 0 && d < fbShake
}

// settle is the time the capsule's fate changes by itself: the end of its
// hold, or the next breath of a running job's dot.
func (f *feedback) settle(now time.Time) time.Time {
	if f.live {
		return now.Add(fbBreathe / 4)
	}
	return f.until
}

// capsuleUp reports whether the notch should show the capsule: the panel is
// fully closed, no hover or file drag has claimed the peek shape, and there
// is something to say. A capsule whose hold just ended is stamped played —
// the fold may still be drawing, but nothing may ever re-show the words.
func (a *shelf) capsuleUp(now time.Time) bool {
	up := a.fb.up(now)
	if !up && a.fb.armed && a.fb.text != "" {
		a.fb.played = a.fb.text
	}
	return !a.visible && a.progress <= 0.01 && !a.peeking &&
		!(a.dragging && !a.ownDrag) && up
}
func (f *feedback) schedule(c *ui.Context, now time.Time) {
	if t := f.settle(now); !t.IsZero() {
		c.After(max(t.Sub(now), 0) + 10*time.Millisecond)
	}
	if f.animating(now, c.Preferences().ReduceMotion) {
		c.AnimationFrame()
	}
}

// clearExpired drops a toast whose time ran out once the shell has folded
// all the way back, so nothing is measured or said about it afterwards.
// The played stamp remembers what already ran: a status still carrying
// that text must not grow the capsule again.
func (f *feedback) clearExpired(now time.Time, folded bool) {
	if folded && !f.up(now) {
		if f.text != "" {
			f.played = f.text
		}
		f.text, f.liveToken = "", nil
	}
}

// openFrameRoom is the box the window needs for a shell of expW × expH
// while the opening or level spring is still in flight: room for the
// overshoot on both axes, plus a tick of slack so the frame never trails
// the shell. The width also holds the two ear corners (earR) the shell's
// top edge draws past its flanks.
func openFrameRoom(expW, expH float32) (w, h int) {
	w = int(math.Ceil(float64(expW*(1+panelOpenOvershoot)))) + 2*earR
	return w, int(math.Ceil(float64(expH*(1+panelOpenOvershoot)))) + 2
}

// convertProgress is the activity strip's title and step for a running job,
// the words the capsule shows while the panel is closed. The strip in
// converter.go draws the same lines in its own row.
func convertProgress(l locales.Messages, j *convertJob) (string, bool) {
	if j == nil {
		return "", false
	}
	done, total := int(j.done.Load()), max(int(j.total.Load()), 1)
	title := l.StatusConvertTo(strings.ToUpper(convertExt(j.target)))
	switch j.target {
	case toJPEG:
		title = l.StatusConvertTo("JPEG")
	case toWord:
		title = l.StatusConvertTo("Word")
	case toMergedPDF:
		title = l.StatusConvertMergePDF()
	case toZip:
		title = l.StatusConvertZip()
	case toLarge, toMedium, toSmall:
		title = l.StatusConvertShrink()
	case toFit500, toFit1M, toFit2M, toFit5M:
		title = l.StatusConvertCompress()
	case toExtract:
		title = l.StatusConvertExtract()
	}
	step := fmt.Sprintf("%d/%d", min(done+1, total), total)
	if j.ocr.Load() {
		step = l.StatusConvertOCR(strconv.Itoa(min(done+1, total)), strconv.Itoa(total))
	}
	if j.cancel.Load() {
		step = l.StatusConvertCancel()
	}
	return l.StatusConvertLine(title, step), true
}
