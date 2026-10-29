//go:build darwin

package main

// The one list of system permissions ShelfU needs and the one entry point
// that walks the user through granting them. The tray menu, the Settings
// window's 系统权限 group and the guided flow all read systemPermissions;
// none of them duplicates a title, an order or a probe.
//
// Nothing here changes a permission by itself: the enabled probes are
// read-only (a TCC.db open for Full Disk Access, AXIsProcessTrusted,
// CGPreflightScreenCaptureAccess), and only the user-initiated request
// functions open System Settings, reveal the app in Finder or reach the
// existing paths that clear a stale ad-hoc record while the grant is
// absent.

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"shelf/internal/mac"
	"shelf/locales"
)

const (
	fullDiskPermissionID      = "fulldisk"
	accessibilityPermissionID = "accessibility"
	screenCapturePermissionID = "screencapture"

	// permissionPollInterval is how often the flow re-reads the current
	// item's probe (Full Disk Access, Accessibility) while the user is in
	// System Settings.
	permissionPollInterval = time.Second
	// permissionScreenHandoff is how long the screen-recording step keeps
	// its 「第 n 项」 caption before it turns into 「还差最后一步」. The
	// request opens the pane first; the pause lets the user see which step
	// they are on while the pane comes forward.
	permissionScreenHandoff = 1500 * time.Millisecond
)

// Seams for the headless tests. The probes never prompt; the request
// functions are the user-initiated paths.
var (
	// Full Disk Access has no read-only query API: the probe opens the
	// TCC database, which only a granted process may read.
	fullDiskAccessProbe   = mac.FullDiskAccessGranted
	fullDiskAccessRequest = mac.RequestFullDiskAccess
	accessibilityProbe    = mac.AccessibilityTrusted
	// ScreenCapturePreflight, not ScreenCaptureAllowed: showing state must
	// never run the stale-record reset that belongs to the request path.
	screenCaptureProbe   = mac.ScreenCapturePreflight
	screenCaptureRequest = mac.RequestScreenCapture
	// openPrivacyPane is all a switched-off permission can do: macOS gives
	// no app the power to revoke a grant, only the user in System Settings.
	openPrivacyPane = mac.OpenPrivacySecurityPane

	permissionAfterFunc = func(d time.Duration, fn func()) permissionTimer {
		return time.AfterFunc(d, fn)
	}
	permissionDispatch = mygo.RunOnMain
)

type permissionTimer interface {
	Stop() bool
}

// systemPermission is one item of the unified entry: the probe that reports
// the live state and the user-initiated request for that one permission.
// short names it inside a sentence (the row's caption and the group's note),
// gloss is the one-line reason that note gives for it. l is the language the
// copy is in, so a sentence built from a list of them (the group's note, the
// row's caption) uses that language's separators without asking again.
type systemPermission struct {
	id      string
	title   string
	short   string
	gloss   string
	detail  string
	l       locales.Messages
	enabled func() bool
	request func() bool
}

// systemPermissions is the single source of the list, in the order the
// guided flow walks it: Full Disk Access first, because it is the one the
// user notices (every drag out of Downloads/Desktop/Documents asks until it
// is on). It says the source language; systemPermissionSet says it in the
// language a window or a menu shows.
func systemPermissions() []systemPermission {
	return systemPermissionSet(locales.In(""))
}

// systemPermissionSet is the one list said in the language of l.
func systemPermissionSet(l locales.Messages) []systemPermission {
	return []systemPermission{
		{
			id:     fullDiskPermissionID,
			title:  l.PermFullDiskTitle(),
			short:  l.PermFullDiskShort(),
			gloss:  l.PermFullDiskGloss(),
			detail: l.PermFullDiskDetail(),
			l:      l,
			enabled: func() bool {
				return fullDiskAccessProbe()
			},
			request: func() bool {
				return fullDiskAccessRequest()
			},
		},
		{
			id:     accessibilityPermissionID,
			title:  l.PermAccessibilityTitle(),
			short:  l.PermAccessibilityShort(),
			gloss:  l.PermAccessibilityGloss(),
			detail: l.PermAccessibilityDetail(),
			l:      l,
			enabled: func() bool {
				return accessibilityProbe()
			},
			request: func() bool {
				return accessibilityRequest()
			},
		},
		{
			id:     screenCapturePermissionID,
			title:  l.PermScreenTitle(),
			short:  l.PermScreenShort(),
			gloss:  l.PermScreenGloss(),
			detail: l.PermScreenDetail(),
			l:      l,
			enabled: func() bool {
				return screenCaptureProbe()
			},
			request: func() bool {
				return screenCaptureRequest()
			},
		},
	}
}

func permissionByID(id string) (systemPermission, bool) {
	return permissionIn(systemPermissions(), id)
}

// permissionIn finds the item of list that has id.
func permissionIn(list []systemPermission, id string) (systemPermission, bool) {
	for _, p := range list {
		if p.id == id {
			return p, true
		}
	}
	return systemPermission{}, false
}

func missingSystemPermissions() []systemPermission {
	return missingPermissions(systemPermissions())
}

// missingPermissions filters list to the permissions the system has not
// granted.
func missingPermissions(list []systemPermission) []systemPermission {
	var missing []systemPermission
	for _, p := range list {
		if !p.enabled() {
			missing = append(missing, p)
		}
	}
	return missing
}

// permissionShortNames joins the given permissions' short names the way a
// sentence in their language lists them: 完整磁盘访问、辅助功能、屏幕录制,
// or Full Disk Access, Accessibility, Screen Recording.
func permissionShortNames(ps []systemPermission) string {
	names := make([]string, len(ps))
	for i, p := range ps {
		names[i] = p.short
	}
	l := locales.In("")
	if len(ps) > 0 {
		l = ps[0].l
	}
	return strings.Join(names, l.PermListSeparator())
}

// permissionMenuItem is the tray item that replaces the old single
// permission entry. It counts what is missing at menu-build time, because
// the menu is rebuilt on every right-click.
func (a *shelf) permissionMenuItem() *mygo.MenuItem {
	l := a.appLoc()
	missing := missingSystemPermissions()
	if len(missing) == 0 {
		return &mygo.MenuItem{Label: l.PermMenuAllOn(), Disabled: true}
	}
	return &mygo.MenuItem{
		Label: l.PermMenuMissing(len(missing)),
		Click: func(*mygo.MenuItem, *mygo.Window) { a.showPermissions() },
	}
}

// showPermissions opens (or focuses) the Settings window and immediately
// starts the guided flow.
func (a *shelf) showPermissions() {
	openSettingsWindow(a)
	// The guided walk lives on its own page: switching first means the
	// flow's captions are visible on an already-open window too.
	a.setSettingsPage(settingsPagePermissions)
	a.startPermissionFlow()
}

// openSettingsWindow is a seam: opening the window needs a running app loop,
// so the headless tests stub it and still exercise the click path.
var openSettingsWindow = func(a *shelf) { a.openSettings() }

// permissionStage is the guided flow's state. The zero value is idle.
type permissionStage int

const (
	permissionIdle permissionStage = iota
	// permissionWaiting: the current item's request has been made and the
	// flow is waiting for its grant. Accessibility is polled; the screen
	// recording step hands over to permissionRelaunch after a pause.
	permissionWaiting
	// permissionRelaunch: a grant that a running process may keep answering
	// stale for — Full Disk Access or screen recording — was made during the
	// walk, so ShelfU must be relaunched before it takes effect.
	permissionRelaunch
)

// permissionFlow is the state of the guided walk. ids is the snapshot of
// what was missing when the flow started, so a grant landing mid-flow
// cannot shift the step index. relaunch remembers that a Full Disk Access
// grant landed while the walk was running: the remaining items are still
// walked, and the walk then ends on the relaunch stage instead of the
// ordinary all-granted one.
type permissionFlow struct {
	stage    permissionStage
	ids      []string
	step     int
	relaunch bool
	timer    permissionTimer
}

// stopPermissionFlow ends the walk and leaves no timer behind. Closing the
// Settings window calls it too.
func (a *shelf) stopPermissionFlow() {
	if a.perm.timer != nil {
		a.perm.timer.Stop()
		a.perm.timer = nil
	}
	a.perm = permissionFlow{}
}

// startPermissionFlow begins (or restarts) the walk over the permissions
// that are missing right now. Already-granted permissions are skipped by
// beginPermissionStep and never requested, so their TCC record is never
// cleared.
func (a *shelf) startPermissionFlow() {
	a.stopPermissionFlow()
	missing := missingSystemPermissions()
	if len(missing) == 0 {
		a.repaintSettings()
		return
	}
	ids := make([]string, len(missing))
	for i, p := range missing {
		ids[i] = p.id
	}
	a.perm = permissionFlow{stage: permissionWaiting, ids: ids}
	a.repaintSettings()
	a.beginPermissionStep()
}

func (a *shelf) permissionFlowCurrent() (systemPermission, bool) {
	if a.perm.step < 0 || a.perm.step >= len(a.perm.ids) {
		return systemPermission{}, false
	}
	return permissionByID(a.perm.ids[a.perm.step])
}

// beginPermissionStep skips everything already granted, then issues the
// current item's request and arms its wait.
func (a *shelf) beginPermissionStep() {
	for a.perm.step < len(a.perm.ids) {
		p, ok := a.permissionFlowCurrent()
		if !ok {
			a.perm.step++
			continue
		}
		if p.enabled() {
			a.perm.step++
			continue
		}
		p.request()
		a.repaintSettings()
		if p.id == screenCapturePermissionID {
			a.armPermissionTimer(permissionScreenHandoff)
		} else {
			a.armPermissionTimer(permissionPollInterval)
		}
		return
	}
	// Nothing left: the flow ends in the ordinary all-granted state, or on
	// the relaunch stage when a Full Disk Access grant landed during it.
	if a.perm.relaunch {
		a.perm.stage = permissionRelaunch
		a.repaintSettings()
		return
	}
	a.stopPermissionFlow()
	a.repaintSettings()
}

func (a *shelf) armPermissionTimer(d time.Duration) {
	if a.perm.timer != nil {
		a.perm.timer.Stop()
	}
	a.perm.timer = permissionAfterFunc(d, func() {
		permissionDispatch(func() { a.permissionTimerFired() })
	})
}

// permissionTimerFired advances the walk on the UI thread. A callback that
// lands after 停止 or after the window closed finds the flow idle and does
// nothing.
func (a *shelf) permissionTimerFired() {
	a.perm.timer = nil
	if a.perm.stage != permissionWaiting {
		return
	}
	p, ok := a.permissionFlowCurrent()
	if !ok {
		a.stopPermissionFlow()
		a.repaintSettings()
		return
	}
	if p.id == screenCapturePermissionID {
		// The request opened the pane; the grant itself is invisible to this
		// process's cached preflight, so the walk ends here either way.
		a.perm.stage = permissionRelaunch
		a.repaintSettings()
		return
	}
	if !p.enabled() {
		a.armPermissionTimer(permissionPollInterval)
		return
	}
	if p.id == fullDiskPermissionID {
		// The grant is live for new opens, but this process may keep its
		// old answer for file access; the walk goes on and ends on the
		// relaunch stage.
		a.perm.relaunch = true
	}
	a.perm.step++
	a.beginPermissionStep()
}

// relaunchPlan is how the Settings window restarts this exact app after the
// old process has released the data-directory lock.
type relaunchPlan struct {
	Exec string
	Args []string
}

// relaunchPlanFor builds a detached /bin/sh wait-then-launch: the new
// process starts only after pid exits, so the single-instance flock is free
// and the user never sees the 「ShelfU 已在运行」 dialog. Inside an app
// bundle the launch goes through LaunchServices (`open -n`), which is what
// gives the restarted process the app's registered identity and TCC
// responsibility — the screen-recording grant this relaunch exists for
// would not follow an exec of Contents/MacOS. Outside a bundle (go test, a
// development run) the executable is started directly. The original
// arguments are preserved, including --data-dir.
func relaunchPlanFor(exe string, args []string, pid int) relaunchPlan {
	var b strings.Builder
	fmt.Fprintf(&b, "while kill -0 %d 2>/dev/null; do sleep 0.1; done; sleep 0.3; ", pid)
	if bundle, ok := appBundleForExecutable(exe); ok {
		fmt.Fprintf(&b, "exec /usr/bin/open -n %s", shellQuote(bundle))
		if len(args) > 0 {
			b.WriteString(" --args")
		}
	} else {
		b.WriteString("exec ")
		b.WriteString(shellQuote(exe))
	}
	for _, arg := range args {
		b.WriteByte(' ')
		b.WriteString(shellQuote(arg))
	}
	return relaunchPlan{Exec: "/bin/sh", Args: []string{"-c", b.String()}}
}

// appBundleForExecutable reports the enclosing .app when exe lives at
// <Bundle>.app/Contents/MacOS/<binary>. It is a pure path check; callers
// only use it to choose the launch route. The Full Disk Access reveal needs
// the same answer, so the check itself lives in internal/mac.
func appBundleForExecutable(exe string) (string, bool) {
	return mac.AppBundleForExecutable(exe)
}

func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// Seams for the relaunch tests: nothing in a test may start a process or
// quit the app.
var (
	relaunchExecutable = os.Executable
	relaunchStart      = func(p relaunchPlan) error {
		cmd := exec.Command(p.Exec, p.Args...)
		// Detach from this process so quitting the app does not take the
		// waiter down with it.
		cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
		return cmd.Start()
	}
	relaunchQuit = mygo.App.Quit
)

// relaunchShelf restarts ShelfU with the same arguments so the cached
// screen-recording preflight and the process's Full Disk Access answer are
// re-read. It starts the waiter first and quits only once that succeeded; a
// failure stays visible in the window.
func (a *shelf) relaunchShelf() {
	exe, err := relaunchExecutable()
	if err != nil {
		a.report(err)
		return
	}
	plan := relaunchPlanFor(exe, os.Args[1:], os.Getpid())
	if err := relaunchStart(plan); err != nil {
		a.report(err)
		return
	}
	a.stopPermissionFlow()
	relaunchQuit()
}

// permissionCaption is the row's caption: what is left, or which step of the
// walk the user is on. Pure apart from the flow's own state. permissions
// carries the names in the language of l, so a name said here follows the
// window rather than the source language.
func (a *shelf) permissionCaption(l locales.Messages, permissions, missing []systemPermission) string {
	switch a.perm.stage {
	case permissionWaiting:
		if a.perm.step < 0 || a.perm.step >= len(a.perm.ids) {
			break
		}
		id := a.perm.ids[a.perm.step]
		name := id
		if p, ok := permissionIn(permissions, id); ok {
			name = p.short
		}
		if id == fullDiskPermissionID {
			// Full Disk Access has no list entry for an app that never asked:
			// the request reveals the bundle in Finder for exactly this.
			return l.PermStepFullDisk(a.perm.step+1, len(a.perm.ids), name)
		}
		return l.PermStep(a.perm.step+1, len(a.perm.ids), name)
	case permissionRelaunch:
		return l.PermRelaunchStep()
	}
	if len(missing) == 0 {
		return l.PermAllGranted(len(permissions), len(permissions))
	}
	return l.PermMissing(len(missing), permissionShortNames(missing))
}

// settingsPermissionSwitch is the group's one row: the title and the state
// caption on the left, one switch on the right. The switch is on only when
// every permission is granted; while the walk waits it shows on, because the
// walk is what is running, and a quiet 停止 sits next to it. The relaunch
// stage replaces the switch with the accent button: nothing is left to
// switch until the app has restarted.
func (a *shelf) settingsPermissionSwitch(c *ui.Context, permissions, missing []systemPermission) {
	l := loc(c)
	title := l.PermTitle()
	caption := a.permissionCaption(l, permissions, missing)
	if a.perm.stage == permissionRelaunch {
		ui.Row(c.Key("perm-row")).AlignItems(ui.Center).Gap(10).MinHeight(48).Padding(settingsRowPadY, 12).FillWidth().Children(func() {
			permissionRowLabel(c, title, caption)
			settingsAccentButton(c, "relaunch", l.PermRelaunchButton(), func() { a.relaunchShelf() })
		})
		return
	}
	on := len(missing) == 0 || a.perm.stage == permissionWaiting
	row := ui.ToggleBase(c.Key("toggle-permissions"), &on).FillWidth().AlignItems(ui.Center).
		Gap(10).MinHeight(48).Padding(settingsRowPadY, 12).Label(title)
	var sw ui.Element
	row.Children(func() {
		permissionRowLabel(c, title, caption)
		ui.Row(c.Key("perm-ctl")).AlignItems(ui.Center).Gap(8).Children(func() {
			if a.perm.stage == permissionWaiting {
				settingsQuietButton(c, "stop", l.PermStop(), func() {
					a.stopPermissionFlow()
					a.repaintSettings()
				})
			}
			sw = ui.Switch(c, &on).Label(title)
		})
	})
	if row.Changed() || sw.Changed() {
		a.applyPermissionSwitch(on)
	}
}

// permissionRowLabel is the switch row's left column.
func permissionRowLabel(c *ui.Context, title, caption string) {
	ui.Column(c.Key("perm-k")).Gap(2).Grow(1).MinWidth(0).Children(func() {
		settingsLabel(c, title, 13, 0).TextColor(ink)
		ui.Text(c, caption).FontSize(11).TextColor(ink3)
	})
}

// applyPermissionSwitch is what the one switch does. On starts the walk over
// whatever is missing. Off while the walk waits stops it. Off when everything
// is granted cannot revoke anything — macOS gives no app that power — so it
// opens 隐私与安全性 and the row's probe keeps the switch on.
func (a *shelf) applyPermissionSwitch(on bool) {
	switch {
	case on:
		a.startPermissionFlow()
	case a.perm.stage == permissionWaiting:
		a.stopPermissionFlow()
		a.repaintSettings()
	default:
		openPrivacyPane()
		a.repaintSettings()
	}
}

// settingsAccentButton is the mock's compact accent pill used by the
// permission header.
func settingsAccentButton(c *ui.Context, key, label string, fn func()) {
	b := ui.ButtonBase(c.Key("perm-"+key)).Height(24).Radius(12).Padding(0, 10, 0, 10).
		Background(settingsTheme.Accent).Center().Label(label)
	if b.Pressed() {
		b.Background(settingsTheme.AccentPressed)
	} else if b.Hovered() {
		b.Background(settingsTheme.AccentHover)
	}
	b.Children(func() {
		ui.Text(c, label).SingleLine().FontSize(12).FontWeight(600).TextColor(ui.RGB(255, 255, 255))
	})
	if b.Clicked() {
		fn()
	}
}

// settingsQuietButton is the same pill without the accent fill, for 停止.
func settingsQuietButton(c *ui.Context, key, label string, fn func()) {
	b := ui.ButtonBase(c.Key("perm-"+key)).Height(24).Radius(12).Padding(0, 10, 0, 10).
		Background(ui.RGBA(255, 255, 255, 0.10)).Center().Label(label)
	if b.Hovered() {
		b.Background(ui.RGBA(255, 255, 255, 0.16))
	}
	b.Children(func() {
		ui.Text(c, label).SingleLine().FontSize(12).FontWeight(600).TextColor(ink)
	})
	if b.Clicked() {
		fn()
	}
}
