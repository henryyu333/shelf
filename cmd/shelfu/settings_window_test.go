//go:build darwin

package main

// Tests for the Settings window: headless ui.Tester coverage of the view
// (toggles apply through the same save path as the tray menu, a broken
// settings.json locks editing, excluded apps can be removed) plus pure
// tests for the geometry/segment helpers, plus a gated native-window smoke
// test (SHELF_BASKET_SMOKE=1) that screenshots the real window.

import (
	"errors"
	"fmt"
	"image/png"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"shelf/internal/mac"
	"shelf/internal/settings"
)

// stubSettingsSeams replaces the app-level seams (login item, Shell.OpenPath,
// accessibility request) and returns the login state they share.
// loginStub models the system's login-item state: setOpenAtLogin flips it
// unless it fails, and openAtLogin always reports what the system holds.
// hold makes a set "succeed" while the system keeps reporting off, as an
// SMAppService registration awaiting the user's approval does.
type loginStub struct {
	on   bool
	err  error
	hold bool
}

func stubSettingsSeams(t *testing.T, loginOn bool, setErr error, opened *[]string) *loginStub {
	t.Helper()
	st := &loginStub{on: loginOn, err: setErr}
	origOpen, origSet, origPath, origAx := openAtLogin, setOpenAtLogin, openPath, accessibilityRequest
	openAtLogin = func() bool { return st.on }
	setOpenAtLogin = func(on bool) error {
		if st.err != nil {
			return st.err
		}
		if !st.hold {
			st.on = on
		}
		return nil
	}
	openPath = func(p string) error {
		if opened != nil {
			*opened = append(*opened, p)
		}
		return nil
	}
	accessibilityRequest = func() bool { return true }
	t.Cleanup(func() {
		openAtLogin, setOpenAtLogin, openPath, accessibilityRequest = origOpen, origSet, origPath, origAx
	})
	return st
}

// stubKeyboard replaces the global-hotkey and app-activation seams so the
// key-ownership flows can be checked without a running app.
func stubKeyboard(t *testing.T) (held *[]string, released *[]string, activated *[]int) {
	t.Helper()
	var h, r []string
	var act []int
	origReg, origUnreg := registerKey, unregisterKey
	origActive, origAct, origResign := appIsActive, activateApp, resignSelf
	registerKey = func(acc string, fn func()) error { h = append(h, acc); return nil }
	unregisterKey = func(acc string) { r = append(r, acc) }
	appIsActive = func() bool { return true }
	activateApp = func(pid int) bool { act = append(act, pid); return true }
	resignSelf = func() {}
	t.Cleanup(func() {
		registerKey, unregisterKey = origReg, origUnreg
		appIsActive, activateApp, resignSelf = origActive, origAct, origResign
	})
	return &h, &r, &act
}

func newSettingsShelf(t *testing.T) (*shelf, *ui.Tester) {
	t.Helper()
	dir := t.TempDir()
	a := &shelf{
		dataDir: dir, settings: settings.Default(),
		thumbnails: map[string]*ui.Bitmap{}, thumbPending: map[string]bool{},
		appNames: map[string]string{},
	}
	return a, ui.NewTester(a.settingsView, settingsWidth, settingsHeight)
}

func TestSettingsFrameCentersAndClamps(t *testing.T) {
	work := rectHelper(0, 25, 1512, 955)
	f := settingsFrame(work)
	if f.Width != settingsWidth || f.Height != settingsHeight {
		t.Fatalf("frame size changed: %+v", f)
	}
	cx, cy := f.X+f.Width/2, f.Y+f.Height/2
	wx, wy := work.X+work.Width/2, work.Y+work.Height/2
	if cx != wx || cy != wy {
		t.Fatalf("window not centered: center=(%d,%d) want (%d,%d)", cx, cy, wx, wy)
	}
	// A work area smaller than the window must clamp, not go negative.
	tiny := rectHelper(100, 50, 200, 150)
	f2 := settingsFrame(tiny)
	if f2.X < tiny.X || f2.Y < tiny.Y {
		t.Fatalf("frame left the work area: %+v vs %+v", f2, tiny)
	}
}

func rectHelper(x, y, w, h int) mygo.Rectangle {
	return mygo.Rectangle{X: x, Y: y, Width: w, Height: h}
}

// openSettingsPage clicks a sidebar destination and settles the page's
// first frame. The destination's label also titles the page, so the click
// lands on the sidebar's item, which the tree draws first.
func openSettingsPage(t *testing.T, view *ui.Tester, title string) {
	t.Helper()
	if err := view.Click(title); err != nil {
		t.Fatalf("sidebar item %q: %v", title, err)
	}
	view.Frame()
}

// clickVisible scrolls the settings view until the element showing label is
// inside the viewport, then clicks it: a page may be taller than the
// window, so a target can start below the fold.
func clickVisible(t *testing.T, view *ui.Tester, label string) {
	t.Helper()
	for range 30 {
		if r, ok := view.Find(label); ok && r.W > 0 && r.H > 0 &&
			r.Y >= 0 && r.Y+r.H <= settingsHeight {
			if err := view.Click(label); err != nil {
				t.Fatal(err)
			}
			return
		}
		view.Scroll(settingsWidth/2, settingsHeight/2, 0, 120)
	}
	t.Fatalf("%q never scrolled into view", label)
}

func TestTrayMenuOffersSettings(t *testing.T) {
	stubSettingsSeams(t, false, nil, nil)
	a, _ := newSettingsShelf(t)
	labels := []string{}
	for _, it := range a.trayMenu().Items() {
		labels = append(labels, it.Label)
	}
	for _, want := range []string{"显示 / 收起 ShelfU", "自动清理", "剪贴板记录", "正在播放", "设置…", "退出 ShelfU"} {
		if !slices.Contains(labels, want) {
			t.Fatalf("tray menu lacks %q; labels=%v", want, labels)
		}
	}
}

func TestChoiceSegments(t *testing.T) {
	labels, values, sel := choiceSegments(7, []int{1, 7, 30}, func(n int) string { return fmt.Sprintf("%dd", n) })
	if sel != 1 || len(labels) != 3 || values[0] != 1 {
		t.Fatalf("bad segments: %v %v sel=%d", labels, values, sel)
	}
	// A hand-edited value not among the presets must appear, sorted in.
	labels, values, sel = choiceSegments(14, []int{1, 7, 30}, func(n int) string { return fmt.Sprintf("%dd", n) })
	if sel != 2 || values[2] != 14 || labels[2] != "14d" {
		t.Fatalf("foreign value not inserted in order: %v %v sel=%d", labels, values, sel)
	}
}

func TestSettingsViewShowsSections(t *testing.T) {
	stubSettingsSeams(t, false, nil, nil)
	a, view := newSettingsShelf(t)
	// Every preference the old single page held is still in the window,
	// on the page where a user looks for it.
	pages := []struct {
		nav   string
		texts []string
	}{
		{"通用", []string{"开机时自动启动", "自动检查更新", "检查更新", "现在检查"}},
		{"外形与提示", []string{"刘海样式", "展开外观", "刘海显示正在播放", "充电提示", "耳机连接提示", "下载进度", "拖窗口到刘海分屏"}},
		{"托盘", []string{"自动清理旧内容", "拖出后从托盘移除"}},
		{"剪贴板", []string{"保留条数", "排除的应用"}},
		{"AI 用量", []string{"在刘海里显示 AI 用量", "额度显示", "数据来源", "套餐额度", "本机用量"}},
		{"快捷键", []string{"显示 / 收起", "剪贴板搜索", "框选文字", "打开托盘", "在刘海里"}},
		{"系统权限", []string{"完整磁盘访问", "辅助功能", "屏幕录制"}},
		{"关于", []string{"打开数据文件夹", "新功能介绍", "源代码", "隐私"}},
	}
	for _, p := range pages {
		openSettingsPage(t, view, p.nav)
		for _, s := range p.texts {
			if !view.HasText(s) {
				t.Fatalf("%s page missing %q; texts=%v", p.nav, s, view.Texts())
			}
		}
	}
	a.settingsPage = settingsPageGeneral
}

func TestSettingsViewNowPlayingToggleSaves(t *testing.T) {
	stubSettingsSeams(t, false, nil, nil)
	a, view := newSettingsShelf(t)
	openSettingsPage(t, view, "外形与提示")
	clickVisible(t, view, "刘海显示正在播放")
	if a.settings.IslandNowPlaying {
		t.Fatal("now-playing toggle did not apply")
	}
	saved, _ := settings.Load(a.dataDir)
	if saved.IslandNowPlaying {
		t.Fatalf("saved flags wrong: %+v", saved)
	}
}

// The 通用 section's 自动检查更新 switch drives setAutoCheckUpdates through
// the same applySettings path the menus use, so the file holds the choice.
func TestSettingsViewAutoCheckToggle(t *testing.T) {
	stubSettingsSeams(t, false, nil, nil)
	a, view := newSettingsShelf(t)
	if !a.settings.AutoCheckUpdates {
		t.Fatal("autoCheckUpdates must default on")
	}
	clickVisible(t, view, "自动检查更新")
	if a.settings.AutoCheckUpdates {
		t.Fatal("toggle did not flip the running setting")
	}
	saved, err := settings.Load(a.dataDir)
	if err != nil {
		t.Fatal(err)
	}
	if saved.AutoCheckUpdates {
		t.Fatal("settings.json was not updated by the window toggle")
	}
	// Turning it back on schedules a check at once.
	clickVisible(t, view, "自动检查更新")
	if !a.settings.AutoCheckUpdates || a.updateNext.IsZero() {
		t.Fatal("re-enabling did not schedule a check")
	}
}

// The expanded panel's appearance: three segments, saved and applied at
// once, follow-the-system on a fresh install.
func TestSettingsViewPanelAppearance(t *testing.T) {
	stubSettingsSeams(t, false, nil, nil)
	a, view := newSettingsShelf(t)
	if a.settings.PanelAppearance != settings.PanelAppearanceFollow {
		t.Fatalf("default appearance = %q, want follow", a.settings.PanelAppearance)
	}
	openSettingsPage(t, view, "外形与提示")
	clickVisible(t, view, "毛玻璃")
	if a.settings.PanelAppearance != settings.PanelAppearanceGlass {
		t.Fatalf("glass not applied: %q", a.settings.PanelAppearance)
	}
	saved, err := settings.Load(a.dataDir)
	if err != nil {
		t.Fatal(err)
	}
	if saved.PanelAppearance != settings.PanelAppearanceGlass {
		t.Fatalf("glass not saved: %+v", saved)
	}
	clickVisible(t, view, "纯黑")
	if a.settings.PanelAppearance != settings.PanelAppearanceBlack {
		t.Fatalf("black not applied: %q", a.settings.PanelAppearance)
	}
	clickVisible(t, view, "跟随系统")
	if a.settings.PanelAppearance != settings.PanelAppearanceFollow {
		t.Fatalf("follow not applied: %q", a.settings.PanelAppearance)
	}
}

func TestSettingsViewSegmentedApplies(t *testing.T) {
	stubSettingsSeams(t, false, nil, nil)
	a, view := newSettingsShelf(t)
	openSettingsPage(t, view, "剪贴板")
	clickVisible(t, view, "100")
	if a.settings.ClipboardLimit != 100 {
		t.Fatalf("limit not applied: %d", a.settings.ClipboardLimit)
	}
	saved, _ := settings.Load(a.dataDir)
	if saved.ClipboardLimit != 100 {
		t.Fatalf("limit not saved: %+v", saved)
	}
	openSettingsPage(t, view, "托盘")
	clickVisible(t, view, "7 天")
	saved, _ = settings.Load(a.dataDir)
	if saved.RetentionDays != 7 {
		t.Fatalf("retention not saved: %+v", saved)
	}
}

func TestSettingsViewBrokenDisablesEditing(t *testing.T) {
	stubSettingsSeams(t, false, nil, nil)
	dir := t.TempDir()
	a := &shelf{
		dataDir: dir, settings: settings.Default(), settingsBroken: true,
		thumbnails: map[string]*ui.Bitmap{}, thumbPending: map[string]bool{},
		appNames: map[string]string{},
	}
	a.settings.ExcludedApps = []string{"com.example.One"}
	view := ui.NewTester(a.settingsView, settingsWidth, settingsHeight)
	if !view.HasText("settings.json 无法读取") {
		t.Fatalf("broken banner missing; texts=%v", view.Texts())
	}
	// Clicks on disabled controls must change nothing and write nothing.
	openSettingsPage(t, view, "外形与提示")
	clickVisible(t, view, "刘海显示正在播放")
	openSettingsPage(t, view, "剪贴板")
	clickVisible(t, view, "100")
	clickVisible(t, view, "移除 com.example.One")
	if !a.settings.IslandNowPlaying || a.settings.ClipboardLimit != 30 || len(a.settings.ExcludedApps) != 1 {
		t.Fatalf("broken settings were mutated: %+v", a.settings)
	}
	if _, err := os.Stat(filepath.Join(dir, settings.FileName)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("broken settings.json was written: %v", err)
	}
}

func TestSettingsViewExcludedAppRemoval(t *testing.T) {
	stubSettingsSeams(t, false, nil, nil)
	a, view := newSettingsShelf(t)
	a.settings.ExcludedApps = []string{"com.example.One", "com.example.Two"}
	view = ui.NewTester(a.settingsView, settingsWidth, settingsHeight)
	openSettingsPage(t, view, "剪贴板")
	if !view.HasText("com.example.Two") {
		t.Fatal("excluded app not listed")
	}
	clickVisible(t, view, "移除 com.example.One")
	if len(a.settings.ExcludedApps) != 1 || a.settings.ExcludedApps[0] != "com.example.Two" {
		t.Fatalf("wrong remaining exclusions: %v", a.settings.ExcludedApps)
	}
	saved, _ := settings.Load(a.dataDir)
	if len(saved.ExcludedApps) != 1 || saved.ExcludedApps[0] != "com.example.Two" {
		t.Fatalf("exclusion removal not saved: %+v", saved.ExcludedApps)
	}
	if view.HasText("com.example.One") {
		t.Fatal("removed app still rendered")
	}
}

func TestSettingsViewLoginSwitch(t *testing.T) {
	// Failing backend shows the error inline and leaves the switch off.
	st := stubSettingsSeams(t, false, errors.New("app must be installed in /Applications"), nil)
	a, view := newSettingsShelf(t)
	clickVisible(t, view, "开机时自动启动")
	if a.loginOn || st.on {
		t.Fatal("login flag flipped despite backend failure")
	}
	if a.loginErr == "" || !view.HasText("/Applications") {
		t.Fatalf("inline login error missing: %q", a.loginErr)
	}
	// A working backend flips the system state, clears the error and the
	// switch follows the state the system reports.
	st.err = nil
	clickVisible(t, view, "开机时自动启动")
	if a.loginErr != "" || !a.loginOn {
		t.Fatalf("enabled login item not reflected: on=%v err=%q", a.loginOn, a.loginErr)
	}
}

// SMAppService can accept a registration and still report the item as off
// until the user approves it in System Settings: the switch must then show
// the system's answer, not what the click asked for.
func TestSettingsViewLoginFollowsSystemState(t *testing.T) {
	st := stubSettingsSeams(t, false, nil, nil)
	st.hold = true
	a, view := newSettingsShelf(t)
	clickVisible(t, view, "开机时自动启动")
	if a.loginOn {
		t.Fatal("switch must show the system's state, not the requested one")
	}
	if a.loginErr != "" {
		t.Fatalf("an accepted registration is not an error: %q", a.loginErr)
	}
}

// The keyboard handover around the Settings window: opening releases the
// shelf's global Esc/Space, and closing gives them or the activation back
// to whoever should have them.
func TestSettingsKeyboardOwnership(t *testing.T) {
	_, released, activated := stubKeyboard(t)

	// While the window is key, the shelf must not take Esc/Space (its own
	// Esc/⌘W and controls handle them), and must release keys if held.
	w := &mygo.Window{}
	a := &shelf{settingsWin: w, escHeld: true, spaceHeld: true, visible: true}
	a.holdKeys()
	if a.escHeld || a.spaceHeld {
		t.Fatal("holdKeys must not keep Esc/Space while the Settings window is key")
	}
	if len(*released) != 2 || !slices.Contains(*released, "Escape") || !slices.Contains(*released, "Space") {
		t.Fatalf("held keys must be released: %v", *released)
	}

	// Opening remembers where the user came from and releases the keys.
	// The frontmost lookup is a seam: a smoke run may leave this process
	// active, which no assertion about this function should depend on.
	oldFront := frontmostPID
	frontmostPID = func() int { return 4242 }
	t.Cleanup(func() { frontmostPID = oldFront })
	opened := &shelf{escHeld: true}
	opened.settingsTakeKeyboard()
	if opened.settingsPrevPID == 0 || opened.escHeld {
		t.Fatalf("open must remember the app and release Esc: prev=%d esc=%v", opened.settingsPrevPID, opened.escHeld)
	}

	// Closing with the panel merely open takes Esc/Space back for the shelf
	// and hands the activation back to the app the user came from.
	*released, *activated = nil, nil
	panel := &shelf{settingsWin: w, visible: true, settingsPrevPID: 4242}
	panel.settingsClosed()
	if panel.settingsWin != nil {
		t.Fatal("close must nil the window pointer")
	}
	if !panel.escHeld || !panel.spaceHeld {
		t.Fatal("closing with the panel open must take Esc/Space back")
	}
	if len(*activated) != 1 || (*activated)[0] != 4242 {
		t.Fatalf("closing must hand the keyboard back to the app we came from: %v", *activated)
	}

	// Closing after clipboard search must leave the keyboard to the panel
	// and must not pull the previous app forward.
	*released, *activated = nil, nil
	search := &shelf{settingsWin: w, focusMode: true, visible: true, settingsPrevPID: 4242}
	search.settingsClosed()
	if search.escHeld || search.spaceHeld {
		t.Fatal("clipboard search keeps the keys, not the shelf")
	}
	if len(*activated) != 0 {
		t.Fatalf("clipboard search must keep the keyboard: activated=%v", *activated)
	}
}

// settingsKeyboardExit is the pure decision the handover runs on.
func TestSettingsKeyboardExit(t *testing.T) {
	if got := (&shelf{focusMode: true, visible: true}).settingsKeyboardExit(); got != settingsToPanel {
		t.Fatalf("clipboard search must win: %v", got)
	}
	if got := (&shelf{visible: true}).settingsKeyboardExit(); got != settingsToShelf {
		t.Fatalf("an open panel takes the keys back: %v", got)
	}
	if got := (&shelf{}).settingsKeyboardExit(); got != settingsToPrevApp {
		t.Fatalf("otherwise the app we came from gets the keyboard: %v", got)
	}
}

// Segmented settings controls carry the same accessible name as their row.
func TestSettingsViewControlLabels(t *testing.T) {
	stubSettingsSeams(t, false, nil, nil)
	_, view := newSettingsShelf(t)
	find := func(name string) int {
		n := 0
		for _, l := range view.Texts() {
			if l == name {
				n++
			}
		}
		return n
	}
	for nav, names := range map[string][]string{
		"剪贴板": {"保留条数"},
		"托盘":  {"自动清理旧内容"},
	} {
		openSettingsPage(t, view, nav)
		for _, name := range names {
			if n := find(name); n < 2 {
				t.Fatalf("%q must name its segmented control too, got %d", name, n)
			}
		}
	}
}

func TestSettingsViewOpenDataFolder(t *testing.T) {
	var opened []string
	stubSettingsSeams(t, false, nil, &opened)
	a, view := newSettingsShelf(t)
	openSettingsPage(t, view, "关于")
	clickVisible(t, view, "打开数据文件夹")
	if len(opened) != 1 || opened[0] != a.dataDir {
		t.Fatalf("data dir not opened: %v", opened)
	}
}

// 关于's source row opens the public repository, and its 新功能介绍 button
// replays the panel's new-feature card.
func TestSettingsViewAboutActions(t *testing.T) {
	var opened []string
	stubSettingsSeams(t, false, nil, nil)
	origExternal := openExternal
	openExternal = func(url string) error {
		opened = append(opened, url)
		return nil
	}
	t.Cleanup(func() { openExternal = origExternal })

	a, view := newSettingsShelf(t)
	openSettingsPage(t, view, "关于")
	clickVisible(t, view, "打开")
	if len(opened) != 1 || opened[0] != sourceURL {
		t.Fatalf("source link opened %v, want %q", opened, sourceURL)
	}
	clickVisible(t, view, "新功能介绍")
	if !a.whatsNewUp || a.tab != pageOverview {
		t.Fatalf("新功能介绍 did not show the card: up=%v tab=%v", a.whatsNewUp, a.tab)
	}
}

func TestAppVersionFallback(t *testing.T) {
	orig := appVersionProbe
	defer func() { appVersionProbe = orig }()
	appVersionProbe = func() string { return "1.2.3" }
	if v := appVersion(); v != "1.2.3" {
		t.Fatalf("packaged version not used: %q", v)
	}
	appVersionProbe = func() string { return "" }
	if v := appVersion(); v == "" {
		t.Fatal("empty version")
	}
}

// The 正在播放 tray item saves its setting and is disabled while
// settings.json could not be read (so a broken file is never overwritten).
func TestNowPlayingMenuSavesToggle(t *testing.T) {
	stubSettingsSeams(t, false, nil, nil)
	dir := t.TempDir()
	a := &shelf{dataDir: dir, settings: settings.Default()}
	var item *mygo.MenuItem
	for _, it := range a.trayMenu().Items() {
		if it.Label == "正在播放" {
			item = it
		}
	}
	if item == nil || !item.Checked {
		t.Fatal("the tray menu must offer a checked 正在播放 item")
	}
	item.Click(nil, nil)
	saved, err := settings.Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if saved.IslandNowPlaying || a.settings.IslandNowPlaying {
		t.Fatal("unchecking 正在播放 must save islandNowPlaying=false and follow it")
	}
	// A rebuilt menu shows the saved state.
	var again *mygo.MenuItem
	for _, it := range a.trayMenu().Items() {
		if it.Label == "正在播放" {
			again = it
		}
	}
	if again == nil || again.Checked {
		t.Fatal("a rebuilt menu must show the saved off state")
	}
	// A broken settings file is never overwritten: the item is disabled
	// and its click must not write anything.
	broken := &shelf{dataDir: t.TempDir(), settingsBroken: true, settings: settings.Default()}
	for _, it := range broken.trayMenu().Items() {
		if it.Label != "正在播放" {
			continue
		}
		if !it.Disabled {
			t.Fatal("the 正在播放 item must be disabled while settings are broken")
		}
		it.Click(nil, nil)
	}
	if _, err := os.Stat(filepath.Join(broken.dataDir, settings.FileName)); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("broken settings were written: %v", err)
	}
}

// --- Gated native-window smoke test -------------------------------------

// TestSettingsWindowSmoke opens the real Settings window in the live app
// loop, verifies it is a real WindowServer window centered on the cursor's
// display, screenshots it with `screencapture -l <id>` and checks the shot
// is a dark window with bright text. SHELF_SETTINGS_HOLD=1 keeps the window
// up for an external screenshot. Run with SHELF_BASKET_SMOKE=1.
func TestSettingsWindowSmoke(t *testing.T) {
	if os.Getenv("SHELF_BASKET_SMOKE") != "1" {
		t.Skip("set SHELF_BASKET_SMOKE=1 to run the native settings window smoke test")
	}
	a := smokeShelf
	// Give the smoke instance a writable data dir and real settings.
	dir := t.TempDir()
	a.dataDir = dir
	prefs, _ := settings.Load(dir)
	// A fixture row for 排除的应用 so the shot shows an app icon + 移除.
	prefs.ExcludedApps = []string{"com.apple.Safari"}
	a.settings = prefs
	var first *mygo.Window
	onMain(func() {
		a.openSettings()
		first = a.settingsWin
	})
	if first == nil {
		t.Fatal("settings window was not created")
	}
	defer onMain(func() { first.Destroy() })

	// Single instance: a second open must not create another window.
	var second *mygo.Window
	onMain(func() {
		a.openSettings()
		second = a.settingsWin
	})
	if second != first {
		t.Fatal("openSettings created a second window — not single instance")
	}

	num := mac.WindowNumber(first.NativeHandle())
	if num == 0 {
		t.Fatal("settings window has no CGWindowID")
	}
	// WindowServer must show it at the centered frame computed from the
	// cursor display's work area.
	want := settingsFrame(mygo.Screen.DisplayNearestPoint(mygo.Screen.CursorScreenPoint()).WorkArea)
	var got mac.NSRect
	settled := false
	for range 60 {
		onMain(func() {
			var ok bool
			got, ok = mac.WindowScreenRect(num)
			settled = ok && int(got.Size.Width) == want.Width && int(got.Size.Height) == want.Height &&
				int(got.Origin.X) == want.X && int(got.Origin.Y) == want.Y
		})
		if settled {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if !settled {
		t.Fatalf("settings window %d never settled to %+v; last seen %+v", num, want, got)
	}
	t.Logf("settings CGWindowID=%d frame=%+v — screencapture -l %d shots it", num, want, num)
	if os.Getenv("SHELF_SETTINGS_HOLD") != "" {
		time.Sleep(8 * time.Second)
	}
	time.Sleep(400 * time.Millisecond) // let the frame settle

	shot := filepath.Join(os.TempDir(), "shelf-settings-smoke.png")
	out, err := exec.Command("screencapture", "-x", "-o", fmt.Sprintf("-l%d", num), shot).CombinedOutput()
	if err != nil {
		t.Fatalf("screencapture failed (%v): %s", err, out)
	}
	f, err := os.Open(shot)
	if err != nil {
		t.Fatal(err)
	}
	img, err := png.Decode(f)
	f.Close()
	if err != nil {
		t.Fatalf("decode %s: %v", shot, err)
	}
	box := opaqueBounds(img)
	if box.Dx() < settingsWidth || box.Dy() < settingsHeight {
		t.Fatalf("screenshot %s shows only %v of paint; want ~%dx%d", shot, box, settingsWidth, settingsHeight)
	}
	// The shot must be a dark window with light text on top.
	dark, light := 0, 0
	b := img.Bounds()
	for y := b.Min.Y; y < b.Max.Y; y += 4 {
		for x := b.Min.X; x < b.Max.X; x += 4 {
			r, g, bl, _ := img.At(x, y).RGBA()
			brightness := int(r>>8) + int(g>>8) + int(bl>>8)
			if brightness < 90 {
				dark++
			} else if brightness > 450 {
				light++
			}
		}
	}
	if dark == 0 || light == 0 {
		t.Fatalf("shot has no dark surface or no light text: dark=%d light=%d", dark, light)
	}
	t.Logf("settings shot=%s box=%v dark=%d light=%d", shot, box, dark, light)
}

// The three 实时活动 switches save through applySettings like every other
// preference, and a fresh install has all of them on.
func TestSettingsViewLiveTogglesSave(t *testing.T) {
	stubSettingsSeams(t, false, nil, nil)
	a, view := newSettingsShelf(t)
	if !a.settings.LiveActivities.Charging || !a.settings.LiveActivities.Headphones || !a.settings.LiveActivities.Downloads {
		t.Fatalf("live activities must default on: %+v", a.settings.LiveActivities)
	}
	openSettingsPage(t, view, "外形与提示")
	clickVisible(t, view, "充电提示")
	clickVisible(t, view, "耳机连接提示")
	clickVisible(t, view, "下载进度")
	saved, err := settings.Load(a.dataDir)
	if err != nil {
		t.Fatal(err)
	}
	if saved.LiveActivities.Charging || saved.LiveActivities.Headphones || saved.LiveActivities.Downloads {
		t.Fatalf("the switches did not reach settings.json: %+v", saved.LiveActivities)
	}
}
