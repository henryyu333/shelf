//go:build darwin

package main

// Tests for the 系统权限 unified entry: the tray item's count, the Settings
// group's one row and one switch (all granted, partly missing, walking,
// relaunching), the guided walk (skipping granted items, Full Disk Access
// first, the relaunch stage), and the relaunch command's arguments. Every
// probe, request and System Settings open is a seam; no test reads or
// changes a real permission.

import (
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"shelf/internal/settings"
	"shelf/locales"
)

type permStubs struct {
	fda, ax, screen bool
	fdaRequests     int
	axRequests      int
	scRequests      int
	paneOpens       int
}

type fakePermissionTimer struct{ stopped bool }

func (f *fakePermissionTimer) Stop() bool {
	was := !f.stopped
	f.stopped = true
	return was
}

type permissionClock struct {
	timers []*fakePermissionTimer
	fns    []func()
	delays []time.Duration
}

func (c *permissionClock) after(d time.Duration, fn func()) permissionTimer {
	t := &fakePermissionTimer{}
	c.timers = append(c.timers, t)
	c.fns = append(c.fns, fn)
	c.delays = append(c.delays, d)
	return t
}

func (c *permissionClock) fireLast(t *testing.T) {
	t.Helper()
	if len(c.fns) == 0 {
		t.Fatal("no permission timer was armed")
	}
	c.fns[len(c.fns)-1]()
}

func (c *permissionClock) last() *fakePermissionTimer {
	if len(c.timers) == 0 {
		return nil
	}
	return c.timers[len(c.timers)-1]
}

func stubPermissions(t *testing.T) (*permStubs, *permissionClock) {
	t.Helper()
	st := &permStubs{}
	clk := &permissionClock{}
	origFDAProbe, origAXProbe, origSCProbe := fullDiskAccessProbe, accessibilityProbe, screenCaptureProbe
	origFDAReq, origAXReq, origSCReq := fullDiskAccessRequest, accessibilityRequest, screenCaptureRequest
	origPane, origAfter, origDispatch := openPrivacyPane, permissionAfterFunc, permissionDispatch
	fullDiskAccessProbe = func() bool { return st.fda }
	accessibilityProbe = func() bool { return st.ax }
	screenCaptureProbe = func() bool { return st.screen }
	fullDiskAccessRequest = func() bool { st.fdaRequests++; return st.fda }
	accessibilityRequest = func() bool { st.axRequests++; return st.ax }
	screenCaptureRequest = func() bool { st.scRequests++; return st.screen }
	openPrivacyPane = func() { st.paneOpens++ }
	permissionAfterFunc = clk.after
	permissionDispatch = func(fn func()) { fn() }
	t.Cleanup(func() {
		fullDiskAccessProbe, accessibilityProbe, screenCaptureProbe = origFDAProbe, origAXProbe, origSCProbe
		fullDiskAccessRequest, accessibilityRequest, screenCaptureRequest = origFDAReq, origAXReq, origSCReq
		openPrivacyPane = origPane
		permissionAfterFunc, permissionDispatch = origAfter, origDispatch
	})
	return st, clk
}

func permissionTrayLabels(t *testing.T) []string {
	t.Helper()
	labels := []string{}
	for _, it := range (&shelf{settings: settings.Default()}).trayMenu().Items() {
		labels = append(labels, it.Label)
	}
	return labels
}

// permissionRowCaption is the row's state caption in the last frame.
func permissionRowCaption(t *testing.T, view *ui.Tester) string {
	t.Helper()
	// The caption lives on the 系统权限 page; open it when the test has not.
	isCaption := func(s string) bool {
		switch {
		case strings.HasPrefix(s, "还差 "):
			// The sidebar's status is 还差 N 项权限; the row's caption names
			// the permissions after 项：.
			return strings.Contains(s, "项：")
		}
		return strings.HasPrefix(s, "已全部开启（") || strings.HasPrefix(s, "第 ") ||
			strings.HasPrefix(s, "还差最后一步")
	}
	found := false
	for _, text := range view.Texts() {
		if isCaption(text) {
			found = true
		}
	}
	if !found {
		openSettingsPage(t, view, "系统权限")
	}
	for _, text := range view.Texts() {
		if isCaption(text) {
			return text
		}
	}
	t.Fatalf("no permission caption in the frame; texts=%v", view.Texts())
	return ""
}

func TestPermissionTrayItemCountsMissing(t *testing.T) {
	for _, tt := range []struct {
		name          string
		fda, ax, scre bool
		want          string
		wantDisable   bool
	}{
		{"all missing", false, false, false, "开启系统权限（还差 3 项）…", false},
		{"screen missing", true, true, false, "开启系统权限（还差 1 项）…", false},
		{"full disk missing", false, true, true, "开启系统权限（还差 1 项）…", false},
		{"two missing", true, false, false, "开启系统权限（还差 2 项）…", false},
		{"all granted", true, true, true, "系统权限已全部开启", true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			st, _ := stubPermissions(t)
			st.fda, st.ax, st.screen = tt.fda, tt.ax, tt.scre
			a := &shelf{settings: settings.Default()}
			item := a.permissionMenuItem()
			if item.Label != tt.want {
				t.Fatalf("tray label = %q, want %q", item.Label, tt.want)
			}
			if item.Disabled != tt.wantDisable {
				t.Fatalf("tray disabled = %v, want %v", item.Disabled, tt.wantDisable)
			}
			// The item replaces the old guard entry in the same position.
			labels := permissionTrayLabels(t)
			if len(labels) < 2 || labels[1] != tt.want {
				t.Fatalf("permission item is not second in the tray: %v", labels)
			}
		})
	}
}

// The tray item starts the same walk the switch does, over what is missing.
func TestPermissionTrayItemStartsTheWalk(t *testing.T) {
	st, _ := stubPermissions(t)
	oldOpen := openSettingsWindow
	opened := 0
	openSettingsWindow = func(*shelf) { opened++ }
	t.Cleanup(func() { openSettingsWindow = oldOpen })
	a := &shelf{settings: settings.Default()}
	item := a.permissionMenuItem()
	if item.Click == nil {
		t.Fatal("the tray item must start the walk")
	}
	item.Click(nil, nil)
	if opened != 1 {
		t.Fatalf("the tray item did not open the Settings window: %d", opened)
	}
	if a.perm.stage != permissionWaiting {
		t.Fatalf("the tray item did not start the walk: %v", a.perm.stage)
	}
	if a.settingsPage != settingsPagePermissions {
		t.Fatalf("the tray item left the window on %v", a.settingsPage)
	}
	if st.fdaRequests != 1 || st.axRequests != 0 || st.scRequests != 0 {
		t.Fatalf("the walk did not start with Full Disk Access: fda=%d ax=%d screen=%d",
			st.fdaRequests, st.axRequests, st.scRequests)
	}
}

// 开启系统权限… shows the 系统权限 page even when the window is already open
// on another one.
func TestShowPermissionsSwitchesToItsPage(t *testing.T) {
	_, _ = stubPermissions(t)
	oldOpen := openSettingsWindow
	openSettingsWindow = func(*shelf) {}
	t.Cleanup(func() { openSettingsWindow = oldOpen })
	a := &shelf{settings: settings.Default(), settingsPage: settingsPageClipboard}
	a.showPermissions()
	if a.settingsPage != settingsPagePermissions {
		t.Fatalf("showPermissions left the window on %v", a.settingsPage)
	}
	if a.perm.stage != permissionWaiting {
		t.Fatalf("showPermissions did not start the walk: %v", a.perm.stage)
	}
}

// The 系统权限 page lists every permission with its live state above the one
// switch that walks the user through what is missing: the old window's two
// design goals (one entry point, one place to see the state) on one page.
func TestSettingsPermissionsPageListsEveryPermission(t *testing.T) {
	_, _ = stubPermissions(t)
	_, view := newSettingsShelf(t)
	openSettingsPage(t, view, "系统权限")
	for _, gone := range []string{"文件访问 · 完整磁盘访问", "全部开启…", "ShelfU 需要"} {
		if view.HasText(gone) {
			t.Fatalf("the removed per-permission UI still shows %q; texts=%v", gone, view.Texts())
		}
	}
	ps := systemPermissionSet(locales.In(""))
	for _, p := range ps {
		if !view.HasText(p.short) || !view.HasText(p.detail) {
			t.Fatalf("permission %q lacks its row; texts=%v", p.id, view.Texts())
		}
	}
	for _, want := range []string{
		"系统权限",
		"需要 3 项",
		"未开启",
		"还差 3 项：完整磁盘访问、辅助功能、屏幕录制",
	} {
		if !view.HasText(want) {
			t.Fatalf("the page lacks %q; texts=%v", want, view.Texts())
		}
	}
}

// The switch state follows the probes: on only when everything is granted,
// off while anything is missing, and the caption names what is left.
func TestPermissionSwitchStates(t *testing.T) {
	st, _ := stubPermissions(t)
	a, view := newSettingsShelf(t)

	// All missing: off, and turning it on starts the walk.
	if got := permissionRowCaption(t, view); got != "还差 3 项：完整磁盘访问、辅助功能、屏幕录制" {
		t.Fatalf("none-granted caption = %q", got)
	}
	if err := view.Click(permissionRowCaption(t, view)); err != nil {
		t.Fatal(err)
	}
	view.Frame()
	if st.fdaRequests != 1 || a.perm.stage != permissionWaiting {
		t.Fatalf("turning the switch on did not start the walk: stage=%v fda=%d", a.perm.stage, st.fdaRequests)
	}
	if got := permissionRowCaption(t, view); !strings.HasPrefix(got, "第 1 项，共 3 项：完整磁盘访问 · ") {
		t.Fatalf("walking caption = %q", got)
	}
	a.stopPermissionFlow()
	view.Frame()

	// Two missing: still off, and the caption names them in list order.
	st.fda, st.ax, st.screen = true, false, false
	view.Frame()
	if got := permissionRowCaption(t, view); got != "还差 2 项：辅助功能、屏幕录制" {
		t.Fatalf("partial caption = %q", got)
	}

	// All granted: on, and the caption says so.
	st.ax, st.screen = true, true
	view.Frame()
	if got := permissionRowCaption(t, view); got != "已全部开启（3/3）" {
		t.Fatalf("all-granted caption = %q", got)
	}
}

// The switch is on only when everything is granted: turning it off in that
// state cannot revoke anything, so it opens 隐私与安全性 and the row's probe
// keeps the switch on.
func TestPermissionSwitchOffOpensPrivacyPane(t *testing.T) {
	st, _ := stubPermissions(t)
	st.fda, st.ax, st.screen = true, true, true
	a, view := newSettingsShelf(t)
	if err := view.Click(permissionRowCaption(t, view)); err != nil {
		t.Fatal(err)
	}
	view.Frame()
	if st.paneOpens != 1 {
		t.Fatalf("turning the switch off opened the pane %d times, want 1", st.paneOpens)
	}
	if a.perm.stage != permissionIdle {
		t.Fatalf("turning the switch off started a walk: %v", a.perm.stage)
	}
	if got := permissionRowCaption(t, view); got != "已全部开启（3/3）" {
		t.Fatalf("the switch did not stay on: caption=%q", got)
	}
	if st.fdaRequests+st.axRequests+st.scRequests != 0 {
		t.Fatal("turning the switch off requested a permission")
	}
}

// The walk covers the missing items only, in list order: Full Disk Access,
// then Accessibility, then screen recording.
func TestPermissionFlowOrderSkipsGranted(t *testing.T) {
	st, clk := stubPermissions(t)
	st.fda, st.ax = true, false
	a, view := newSettingsShelf(t)
	if err := view.Click(permissionRowCaption(t, view)); err != nil {
		t.Fatal(err)
	}
	view.Frame()
	if st.fdaRequests != 0 {
		t.Fatalf("a granted Full Disk Access was requested: %d", st.fdaRequests)
	}
	if st.axRequests != 1 || st.scRequests != 0 {
		t.Fatalf("the walk did not start at Accessibility: ax=%d screen=%d", st.axRequests, st.scRequests)
	}
	if got := permissionRowCaption(t, view); !strings.HasPrefix(got, "第 1 项，共 2 项：辅助功能 · ") {
		t.Fatalf("walking caption = %q", got)
	}
	if !view.HasText("停止") {
		t.Fatal("the walk must offer 停止")
	}
	// A poll while the switch is still off stays on the same step.
	clk.fireLast(t)
	view.Frame()
	if st.scRequests != 0 || !strings.HasPrefix(permissionRowCaption(t, view), "第 1 项，共 2 项：辅助功能") {
		t.Fatalf("an ungranted poll advanced the walk: screen=%d", st.scRequests)
	}
	// The grant lands in System Settings; the poll sees it and moves on.
	st.ax = true
	clk.fireLast(t)
	view.Frame()
	if st.scRequests != 1 {
		t.Fatalf("screen recording not requested after Accessibility: %d", st.scRequests)
	}
	if got := permissionRowCaption(t, view); !strings.HasPrefix(got, "第 2 项，共 2 项：屏幕录制 · ") {
		t.Fatalf("second step caption = %q", got)
	}
	// The screen step hands over to the relaunch step.
	clk.fireLast(t)
	if a.perm.stage != permissionRelaunch {
		t.Fatalf("the screen step did not reach the relaunch stage: %v", a.perm.stage)
	}
	view.Frame()
	if got := permissionRowCaption(t, view); got != "还差最后一步：重新打开 ShelfU 才生效" {
		t.Fatalf("relaunch caption = %q", got)
	}
	if !view.HasText("重新打开 ShelfU") {
		t.Fatalf("the relaunch button is missing; texts=%v", view.Texts())
	}
	if view.HasText("停止") {
		t.Fatal("the relaunch stage must replace the switch, not offer 停止 next to it")
	}
}

// The Full Disk Access step names the + / drag route, because the pane has
// no entry for an app that never asked.
func TestPermissionFlowFullDiskCaption(t *testing.T) {
	st, clk := stubPermissions(t)
	st.ax, st.screen = true, true
	_, view := newSettingsShelf(t)
	if err := view.Click(permissionRowCaption(t, view)); err != nil {
		t.Fatal(err)
	}
	view.Frame()
	want := "第 1 项，共 1 项：完整磁盘访问 · 在「系统设置」里打开 ShelfU，这里会自动往下走（列表里没有就点 +，或把访达里选中的 ShelfU 拖进去）"
	if got := permissionRowCaption(t, view); got != want {
		t.Fatalf("Full Disk Access caption = %q, want %q", got, want)
	}
	// The other steps do not repeat the Full Disk Access hint.
	st.fda = true
	clk.fireLast(t)
	view.Frame()
	if !view.HasText("还差最后一步：重新打开 ShelfU 才生效") {
		t.Fatalf("a Full Disk Access grant must end on the relaunch stage; texts=%v", view.Texts())
	}
	if strings.Contains(permissionRowCaption(t, view), "访达") {
		t.Fatal("the Full Disk Access hint leaked into another stage")
	}
}

// A Full Disk Access grant made during the walk ends it on the relaunch
// stage: a running process may keep its old answer for file access.
func TestPermissionFlowFullDiskGrantReachesRelaunch(t *testing.T) {
	st, clk := stubPermissions(t)
	st.fda, st.ax, st.screen = false, false, false
	a, view := newSettingsShelf(t)
	if err := view.Click(permissionRowCaption(t, view)); err != nil {
		t.Fatal(err)
	}
	view.Frame()
	if st.fdaRequests != 1 {
		t.Fatalf("Full Disk Access was not requested first: %d", st.fdaRequests)
	}
	st.fda = true
	clk.fireLast(t)
	view.Frame()
	// The remaining items are still walked rather than abandoned.
	if st.axRequests != 1 {
		t.Fatalf("the walk did not continue after the Full Disk Access grant: ax=%d", st.axRequests)
	}
	if a.perm.relaunch != true {
		t.Fatal("the Full Disk Access grant was not remembered for the relaunch")
	}
	st.ax = true
	clk.fireLast(t)
	st.screen = true
	clk.fireLast(t)
	if a.perm.stage != permissionRelaunch {
		t.Fatalf("the walk ended at %v, want the relaunch stage", a.perm.stage)
	}
	view.Frame()
	if !view.HasText("还差最后一步：重新打开 ShelfU 才生效") {
		t.Fatalf("the relaunch stage is not shown; texts=%v", view.Texts())
	}
}

// Without screen recording in the walk, the Full Disk Access grant alone
// still ends on the relaunch stage.
func TestPermissionFlowFullDiskOnlyEndsOnRelaunch(t *testing.T) {
	st, clk := stubPermissions(t)
	st.ax, st.screen = true, true
	a, view := newSettingsShelf(t)
	if err := view.Click(permissionRowCaption(t, view)); err != nil {
		t.Fatal(err)
	}
	st.fda = true
	clk.fireLast(t)
	if a.perm.stage != permissionRelaunch {
		t.Fatalf("a lone Full Disk Access grant ended at %v, want the relaunch stage", a.perm.stage)
	}
	view.Frame()
	if !view.HasText("重新打开 ShelfU") {
		t.Fatalf("the relaunch button is missing; texts=%v", view.Texts())
	}
}

// 停止 ends the walk and leaves no timer behind; a callback already in
// flight must do nothing.
func TestPermissionFlowStops(t *testing.T) {
	st, clk := stubPermissions(t)
	a, view := newSettingsShelf(t)
	if err := view.Click(permissionRowCaption(t, view)); err != nil {
		t.Fatal(err)
	}
	view.Frame()
	if err := view.Click("停止"); err != nil {
		t.Fatal(err)
	}
	if clk.last() == nil || !clk.last().stopped {
		t.Fatal("停止 left its timer armed")
	}
	if a.perm.stage != permissionIdle {
		t.Fatalf("停止 left the flow at %v", a.perm.stage)
	}
	view.Frame()
	if got := permissionRowCaption(t, view); got != "还差 3 项：完整磁盘访问、辅助功能、屏幕录制" {
		t.Fatalf("停止 did not return to the ordinary state: %q", got)
	}
	clk.fireLast(t)
	if a.perm.stage != permissionIdle || st.fdaRequests != 1 {
		t.Fatalf("a stale timer advanced a stopped flow: stage=%v requests=%d", a.perm.stage, st.fdaRequests)
	}
}

// Turning the pending switch off stops the walk too.
func TestPermissionSwitchOffWhileWaitingStops(t *testing.T) {
	st, clk := stubPermissions(t)
	a, view := newSettingsShelf(t)
	openSettingsPage(t, view, "系统权限")
	if err := view.Click(permissionRowCaption(t, view)); err != nil {
		t.Fatal(err)
	}
	view.Frame()
	if err := view.Click(permissionRowCaption(t, view)); err != nil {
		t.Fatal(err)
	}
	if a.perm.stage != permissionIdle {
		t.Fatalf("the pending switch did not stop the walk: %v", a.perm.stage)
	}
	if clk.last() == nil || !clk.last().stopped {
		t.Fatal("the pending switch left its timer armed")
	}
	if st.paneOpens != 0 {
		t.Fatal("stopping the walk opened the privacy pane")
	}
}

func TestSettingsCloseStopsPermissionFlow(t *testing.T) {
	st, clk := stubPermissions(t)
	a, view := newSettingsShelf(t)
	openSettingsPage(t, view, "系统权限")
	if err := view.Click(permissionRowCaption(t, view)); err != nil {
		t.Fatal(err)
	}
	if a.perm.stage != permissionWaiting {
		t.Fatal("flow did not start")
	}
	a.settingsClosed()
	if clk.last() == nil || !clk.last().stopped {
		t.Fatal("closing the window left the poll armed")
	}
	if a.perm.stage != permissionIdle {
		t.Fatalf("closing left the flow at %v", a.perm.stage)
	}
	before := st.fdaRequests
	clk.fireLast(t)
	if st.fdaRequests != before || a.perm.stage != permissionIdle {
		t.Fatal("a poll after close still ran")
	}
}

func TestPermissionFlowNeverRequestsGrantedItems(t *testing.T) {
	st, clk := stubPermissions(t)
	st.fda, st.ax, st.screen = true, true, true
	a := &shelf{settings: settings.Default()}
	a.startPermissionFlow()
	if a.perm.stage != permissionIdle || st.fdaRequests+st.axRequests+st.scRequests != 0 {
		t.Fatalf("all granted must not request anything: stage=%v fda=%d ax=%d screen=%d",
			a.perm.stage, st.fdaRequests, st.axRequests, st.scRequests)
	}
	if len(clk.timers) != 0 {
		t.Fatalf("all granted armed %d timers", len(clk.timers))
	}
}

// A broken settings.json locks the preference groups; the permission group
// is independent of that file and must keep working.
func TestPermissionsSectionWorksWhileSettingsBroken(t *testing.T) {
	st, _ := stubPermissions(t)
	dir := t.TempDir()
	a := &shelf{dataDir: dir, settings: settings.Default(), settingsBroken: true,
		thumbnails: map[string]*ui.Bitmap{}, thumbPending: map[string]bool{}, appNames: map[string]string{}}
	view := ui.NewTester(a.settingsView, settingsWidth, settingsHeight)
	openSettingsPage(t, view, "系统权限")
	if !view.HasText("settings.json 无法读取") || !view.HasText("还差 3 项") {
		t.Fatalf("the permission group is not usable while settings are broken; texts=%v", view.Texts())
	}
	if err := view.Click(permissionRowCaption(t, view)); err != nil {
		t.Fatal(err)
	}
	if st.fdaRequests != 1 {
		t.Fatalf("broken settings blocked the permission request: %d", st.fdaRequests)
	}
}

// The permission list is the one source of titles, order and short names.
func TestSystemPermissionsList(t *testing.T) {
	ps := systemPermissions()
	ids := make([]string, len(ps))
	shorts := make([]string, len(ps))
	for i, p := range ps {
		ids[i] = p.id
		shorts[i] = p.short
		if p.title == "" || p.detail == "" || p.gloss == "" {
			t.Fatalf("permission %q lacks its copy: %+v", p.id, p)
		}
	}
	if !slices.Equal(ids, []string{fullDiskPermissionID, accessibilityPermissionID, screenCapturePermissionID}) {
		t.Fatalf("permission order = %v", ids)
	}
	if !slices.Equal(shorts, []string{"完整磁盘访问", "辅助功能", "屏幕录制"}) {
		t.Fatalf("short names = %v", shorts)
	}
	if p, ok := permissionByID(fullDiskPermissionID); !ok || p.short != "完整磁盘访问" {
		t.Fatalf("permissionByID(fulldisk) = %+v, %v", p, ok)
	}
}

func TestRelaunchPlanUsesLaunchServicesInsideBundle(t *testing.T) {
	plan := relaunchPlanFor("/Applications/ShelfU App.app/Contents/MacOS/ShelfU",
		[]string{"--data-dir", "/tmp/a b", "--show"}, 4321)
	if plan.Exec != "/bin/sh" || len(plan.Args) != 2 || plan.Args[0] != "-c" {
		t.Fatalf("relaunch plan is not a shell waiter: %+v", plan)
	}
	script := plan.Args[1]
	for _, want := range []string{
		"kill -0 4321", "sleep 0.1", "sleep 0.3",
		"exec /usr/bin/open -n '/Applications/ShelfU App.app'",
		"--args '--data-dir' '/tmp/a b' '--show'",
	} {
		if !strings.Contains(script, want) {
			t.Fatalf("relaunch script lacks %q:\n%s", want, script)
		}
	}
	if strings.Contains(script, "Contents/MacOS") {
		t.Fatalf("a bundled relaunch must not exec the binary directly:\n%s", script)
	}
	// No arguments: open is called without an empty --args.
	noArgs := relaunchPlanFor("/tmp/My.app/Contents/MacOS/My", nil, 9).Args[1]
	if !strings.Contains(noArgs, "exec /usr/bin/open -n '/tmp/My.app'") || strings.Contains(noArgs, "--args") {
		t.Fatalf("empty argument list produced --args:\n%s", noArgs)
	}
}

func TestRelaunchPlanFallsBackOutsideBundle(t *testing.T) {
	plan := relaunchPlanFor("/tmp/ShelfU", []string{"--data-dir", "/tmp/a b"}, 4321)
	script := plan.Args[1]
	for _, want := range []string{
		"kill -0 4321",
		"exec '/tmp/ShelfU' '--data-dir' '/tmp/a b'",
	} {
		if !strings.Contains(script, want) {
			t.Fatalf("fallback script lacks %q:\n%s", want, script)
		}
	}
	if strings.Contains(script, "/usr/bin/open") {
		t.Fatalf("a bare executable must be started directly:\n%s", script)
	}
	// A path that merely looks app-ish but is not Contents/MacOS also falls
	// back rather than opening the wrong bundle.
	odd := relaunchPlanFor("/tmp/Not.app/tool", nil, 7).Args[1]
	if !strings.Contains(odd, "exec '/tmp/Not.app/tool'") {
		t.Fatalf("odd path was treated as a bundle:\n%s", odd)
	}
	quoted := relaunchPlanFor("/tmp/it's", nil, 7).Args[1]
	if !strings.Contains(quoted, `exec '/tmp/it'\''s'`) {
		t.Fatalf("single quote was not escaped: %s", quoted)
	}
}

func TestAppBundleForExecutable(t *testing.T) {
	for _, tt := range []struct {
		exe  string
		want string
		ok   bool
	}{
		{"/Applications/ShelfU.app/Contents/MacOS/ShelfU", "/Applications/ShelfU.app", true},
		{"/tmp/a b/My App.app/Contents/MacOS/My App", "/tmp/a b/My App.app", true},
		{"/tmp/ShelfU", "", false},
		{"/tmp/Not.app/tool", "", false},
		{"/Contents/MacOS/ShelfU", "", false},
		{"", "", false},
	} {
		got, ok := appBundleForExecutable(tt.exe)
		if got != tt.want || ok != tt.ok {
			t.Fatalf("appBundleForExecutable(%q) = %q,%v; want %q,%v", tt.exe, got, ok, tt.want, tt.ok)
		}
	}
}

func TestRelaunchShelfUsesSameArgsAndQuits(t *testing.T) {
	st, _ := stubPermissions(t)
	st.fda, st.ax, st.screen = true, true, true
	oldArgs := os.Args
	os.Args = []string{"/tmp/ShelfU", "--data-dir", "/tmp/data dir"}
	defer func() { os.Args = oldArgs }()
	origExe, origStart, origQuit := relaunchExecutable, relaunchStart, relaunchQuit
	relaunchExecutable = func() (string, error) { return "/tmp/ShelfU", nil }
	var got relaunchPlan
	relaunchStart = func(p relaunchPlan) error { got = p; return nil }
	quit := false
	relaunchQuit = func() { quit = true }
	t.Cleanup(func() { relaunchExecutable, relaunchStart, relaunchQuit = origExe, origStart, origQuit })

	a, _ := newSettingsShelf(t)
	a.perm = permissionFlow{stage: permissionRelaunch, ids: []string{screenCapturePermissionID}}
	a.relaunchShelf()
	if !quit {
		t.Fatal("relaunch did not quit the running app")
	}
	script := got.Args[1]
	if !strings.Contains(script, "'--data-dir' '/tmp/data dir'") {
		t.Fatalf("--data-dir was not preserved: %s", script)
	}
	if a.perm.stage != permissionIdle {
		t.Fatalf("relaunch left the flow at %v", a.perm.stage)
	}
}
