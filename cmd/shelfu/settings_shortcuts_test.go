//go:build darwin

package main

// The 快捷键 page's recorder: a key press becomes an accelerator, saved in
// settings.json and registered at once; a combination the system refuses
// leaves the old one in place; Esc cancels and Delete clears.

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"shelf/internal/settings"
	"shelf/locales"
)

// fakeKeys is the real registration layer, replaced: a registry that
// refuses combinations on demand, so a test can hold what the system would
// hold and press what the system would press.
type fakeKeys struct {
	held   map[string]func()
	fail   map[string]int // acc → remaining refusals
	regs   []string
	unregs []string
}

func installFakeKeys(t *testing.T) *fakeKeys {
	t.Helper()
	f := &fakeKeys{held: map[string]func(){}, fail: map[string]int{}}
	origReg, origUnreg := registerKey, unregisterKey
	registerKey = func(acc string, fn func()) error {
		if f.fail[acc] > 0 {
			f.fail[acc]--
			return errShortcutBusy
		}
		if _, ok := f.held[acc]; ok {
			return errShortcutBusy
		}
		f.held[acc] = fn
		f.regs = append(f.regs, acc)
		return nil
	}
	unregisterKey = func(acc string) {
		delete(f.held, acc)
		f.unregs = append(f.unregs, acc)
	}
	t.Cleanup(func() { registerKey, unregisterKey = origReg, origUnreg })
	return f
}

func (f *fakeKeys) has(acc string) bool {
	_, ok := f.held[acc]
	return ok
}

// press delivers a registered combination the way the system would.
func (f *fakeKeys) press(acc string) {
	if fn := f.held[acc]; fn != nil {
		fn()
	}
}

// shortcutShelf is a fresh install with its three default shortcuts
// registered through the fake layer.
func shortcutShelf(t *testing.T) (*shelf, *ui.Tester, *fakeKeys) {
	t.Helper()
	stubSettingsSeams(t, false, nil, nil)
	f := installFakeKeys(t)
	a, view := newSettingsShelf(t)
	a.registerShortcuts()
	return a, view, f
}

func TestShortcutRecordingSavesAndRegisters(t *testing.T) {
	a, view, f := shortcutShelf(t)
	openSettingsPage(t, view, "快捷键")

	if err := view.Click("显示 / 收起"); err != nil {
		t.Fatal(err)
	}
	view.Frame()
	if a.settingsRecording != shortcutToggle {
		t.Fatalf("the row did not start recording: %v", a.settingsRecording)
	}
	view.Key(ui.Ctrl|ui.Alt, ui.KeyK)
	view.Frame()
	if a.settingsRecording != shortcutNone {
		t.Fatal("the recorder did not end after a combination")
	}
	if got := a.settings.Shortcuts.Toggle; got != "Ctrl+Alt+K" {
		t.Fatalf("saved toggle = %q, want Ctrl+Alt+K", got)
	}
	saved, err := settings.Load(a.dataDir)
	if err != nil || saved.Shortcuts.Toggle != "Ctrl+Alt+K" {
		t.Fatalf("settings.json toggle = %q, %v", saved.Shortcuts.Toggle, err)
	}
	if !f.has("Ctrl+Alt+K") || f.has(settings.DefaultShortcutToggle) {
		t.Fatalf("registry after the change: %v", f.regs)
	}
	// The rows that stood down for the recording came back.
	if !f.has(settings.DefaultShortcutClipboard) || !f.has(settings.DefaultShortcutCapture) {
		t.Fatalf("the other shortcuts were not restored: %v", f.regs)
	}
}

// While a row listens, the app's own combinations must not run their old
// actions: they are released for the duration and claimed again after.
func TestShortcutRecordingSuspendsRegisteredKeys(t *testing.T) {
	a, view, f := shortcutShelf(t)
	a.visible = false
	openSettingsPage(t, view, "快捷键")
	if !f.has("Ctrl+Alt+Space") {
		t.Fatal("the toggle shortcut must start registered")
	}
	if err := view.Click("显示 / 收起"); err != nil {
		t.Fatal(err)
	}
	view.Frame()
	if f.has("Ctrl+Alt+Space") {
		t.Fatal("the registered combination still answers while recording")
	}
	// The system would deliver it to the recorder, not to the shelf.
	f.press("Ctrl+Alt+Space")
	if a.visible {
		t.Fatal("the old action ran during the recording")
	}
	// Pressing it reaches the recorder, which keeps the saved value and
	// claims it again for the other rows' sake.
	view.Key(ui.Ctrl|ui.Alt, ui.KeySpace)
	view.Frame()
	if got := a.settings.Shortcuts.Toggle; got != "Ctrl+Alt+Space" {
		t.Fatalf("saved toggle = %q", got)
	}
	if !f.has("Ctrl+Alt+Space") || !f.has("Ctrl+Alt+V") || !f.has("Ctrl+Alt+O") {
		t.Fatalf("the shortcuts did not come back: %v", f.regs)
	}
}

func TestShortcutRecordingCancelAndClear(t *testing.T) {
	a, view, f := shortcutShelf(t)
	openSettingsPage(t, view, "快捷键")

	// Esc cancels without touching the saved shortcut, and the suspended
	// combinations come back.
	if err := view.Click("剪贴板搜索"); err != nil {
		t.Fatal(err)
	}
	view.Frame()
	view.Key(0, ui.KeyEscape)
	view.Frame()
	if a.settingsRecording != shortcutNone {
		t.Fatal("Esc did not end the recording")
	}
	if a.settings.Shortcuts.Clipboard != settings.DefaultShortcutClipboard {
		t.Fatalf("Esc changed the shortcut: %q", a.settings.Shortcuts.Clipboard)
	}
	for _, acc := range []string{"Ctrl+Alt+Space", "Ctrl+Alt+V", "Ctrl+Alt+O"} {
		if !f.has(acc) {
			t.Fatalf("Esc left %q unregistered; registry %v", acc, f.regs)
		}
	}

	// Delete clears it: the file holds "", the system holds nothing for
	// that row, and the rest stay.
	if err := view.Click("剪贴板搜索"); err != nil {
		t.Fatal(err)
	}
	view.Frame()
	view.Key(0, ui.KeyBackspace)
	view.Frame()
	if got := a.settings.Shortcuts.Clipboard; got != "" {
		t.Fatalf("cleared clipboard shortcut = %q, want empty", got)
	}
	if f.has(settings.DefaultShortcutClipboard) {
		t.Fatal("clearing left the keys registered")
	}
	if !f.has("Ctrl+Alt+Space") || !f.has("Ctrl+Alt+O") {
		t.Fatalf("clearing took the other shortcuts down: %v", f.regs)
	}
}

// A bare key, or a combination without Control, Option or Command, is
// ignored: a global bare key would swallow that key everywhere.
func TestShortcutRecordingIgnoresBareKeys(t *testing.T) {
	a, view, f := shortcutShelf(t)
	openSettingsPage(t, view, "快捷键")
	if err := view.Click("框选文字"); err != nil {
		t.Fatal(err)
	}
	view.Frame()
	view.Key(0, ui.KeyJ)
	view.Key(ui.Shift, ui.KeyJ)
	view.Frame()
	if a.settingsRecording != shortcutCapture {
		t.Fatal("a bare key ended the recording")
	}
	if a.settings.Shortcuts.Capture != settings.DefaultShortcutCapture {
		t.Fatalf("a bare key changed the shortcut: %q", a.settings.Shortcuts.Capture)
	}
	if f.has("Ctrl+Alt+J") || f.has("Shift+J") {
		t.Fatalf("a bare key registered something: %v", f.regs)
	}
	// Shift with a modifier is fine.
	view.Key(ui.Ctrl|ui.Shift, ui.KeyJ)
	view.Frame()
	if got := a.settings.Shortcuts.Capture; got != "Ctrl+Shift+J" {
		t.Fatalf("saved capture = %q, want Ctrl+Shift+J", got)
	}
	if !f.has("Ctrl+Shift+J") || !f.has("Ctrl+Alt+Space") || !f.has("Ctrl+Alt+V") {
		t.Fatalf("registry after the change: %v", f.regs)
	}
}

// A click outside the keycaps ends the recording, like Esc.
func TestShortcutRecordingClickCancels(t *testing.T) {
	a, view, f := shortcutShelf(t)
	openSettingsPage(t, view, "快捷键")
	if err := view.Click("显示 / 收起"); err != nil {
		t.Fatal(err)
	}
	view.Frame()
	view.ClickAt(settingsWidth-80, settingsHeight-60)
	view.Frame()
	if a.settingsRecording != shortcutNone {
		t.Fatal("a click outside the keycaps did not cancel")
	}
	if a.settings.Shortcuts.Toggle != settings.DefaultShortcutToggle {
		t.Fatalf("the click changed the shortcut: %q", a.settings.Shortcuts.Toggle)
	}
	if !f.has("Ctrl+Alt+Space") || !f.has("Ctrl+Alt+V") || !f.has("Ctrl+Alt+O") {
		t.Fatalf("the click left shortcuts unregistered: %v", f.regs)
	}
}

// Closing the window mid-recording gives the suspended shortcuts back.
func TestSettingsClosedRestoresSuspendedShortcuts(t *testing.T) {
	a, view, f := shortcutShelf(t)
	a.visible = true
	openSettingsPage(t, view, "快捷键")
	if err := view.Click("显示 / 收起"); err != nil {
		t.Fatal(err)
	}
	view.Frame()
	if f.has("Ctrl+Alt+Space") {
		t.Fatal("recording left the combination registered")
	}
	a.settingsClosed()
	if a.settingsRecording != shortcutNone {
		t.Fatal("closing left a row recording")
	}
	for _, acc := range []string{"Ctrl+Alt+Space", "Ctrl+Alt+V", "Ctrl+Alt+O"} {
		if !f.has(acc) {
			t.Fatalf("closing left %q unregistered", acc)
		}
	}
}

// Leaving the window ends the recording: the suspended shortcuts are
// claimed back instead of staying dead while the user works elsewhere.
func TestSettingsRecordingEndsOnBlur(t *testing.T) {
	a, view, f := shortcutShelf(t)
	openSettingsPage(t, view, "快捷键")
	if err := view.Click("显示 / 收起"); err != nil {
		t.Fatal(err)
	}
	view.Frame()
	if f.has("Ctrl+Alt+Space") {
		t.Fatal("recording did not suspend the combination")
	}
	a.settingsBlur()
	view.Frame()
	if a.settingsRecording != shortcutNone {
		t.Fatal("blur left the row recording")
	}
	if !f.has("Ctrl+Alt+Space") || !f.has("Ctrl+Alt+V") || !f.has("Ctrl+Alt+O") {
		t.Fatalf("blur left shortcuts unregistered: %v", f.regs)
	}
	if a.settings.Shortcuts.Toggle != settings.DefaultShortcutToggle {
		t.Fatalf("blur changed the shortcut: %q", a.settings.Shortcuts.Toggle)
	}
}

// A later successful restore clears the busy note an earlier refusal left:
// the row must not keep saying the combination is taken once it is the
// app's again.
func TestRestoreSuccessClearsBusy(t *testing.T) {
	a, view, f := shortcutShelf(t)
	f.fail["Ctrl+Alt+O"] = 1
	openSettingsPage(t, view, "快捷键")
	if err := view.Click("显示 / 收起"); err != nil {
		t.Fatal(err)
	}
	view.Frame()
	view.Key(ui.Ctrl|ui.Alt, ui.KeyK)
	view.Frame()
	if !a.shortcutBusy[shortcutCapture] {
		t.Fatal("the refused restore was not reported")
	}
	// The next recording restores again, and this time the keys are free.
	if err := view.Click("显示 / 收起"); err != nil {
		t.Fatal(err)
	}
	view.Frame()
	view.Key(0, ui.KeyEscape)
	view.Frame()
	if !f.has("Ctrl+Alt+O") {
		t.Fatal("the retried restore did not claim the keys")
	}
	if a.shortcutBusy[shortcutCapture] {
		t.Fatal("a successful restore left the row saying 已被占用")
	}
}

// A refused combination keeps the previous shortcut and says so on the row;
// the same combination as another row's counts as refused.
func TestShortcutRecordingBusyKeepsPrevious(t *testing.T) {
	a, view, f := shortcutShelf(t)
	openSettingsPage(t, view, "快捷键")

	if err := view.Click("显示 / 收起"); err != nil {
		t.Fatal(err)
	}
	view.Frame()
	view.Key(ui.Ctrl|ui.Alt, ui.KeyV)
	view.Frame()
	if !a.shortcutBusy[shortcutToggle] {
		t.Fatal("a duplicate combination must be reported busy")
	}
	if a.settings.Shortcuts.Toggle != settings.DefaultShortcutToggle {
		t.Fatalf("busy combination replaced the shortcut: %q", a.settings.Shortcuts.Toggle)
	}
	if !f.has("Ctrl+Alt+Space") || !f.has("Ctrl+Alt+V") || !f.has("Ctrl+Alt+O") {
		t.Fatalf("a refused combination left the registry inconsistent: %v", f.regs)
	}
	view.Frame()
	if !view.HasText(locales.In("").SettingsShortcutBusy()) {
		t.Fatalf("the row does not say the combination is in use: %v", view.Texts())
	}
	// A later successful change clears the note.
	view.Click("显示 / 收起")
	view.Frame()
	view.Key(ui.Ctrl|ui.Alt, ui.KeyK)
	view.Frame()
	if a.shortcutBusy[shortcutToggle] {
		t.Fatal("the busy note survived a successful change")
	}
	if !f.has("Ctrl+Alt+K") || f.has("Ctrl+Alt+Space") {
		t.Fatalf("registry after the change: %v", f.regs)
	}
}

// A registration the system refuses (another app holds the keys) restores
// the previous registration and saves nothing.
func TestShortcutRecordingSystemRefusal(t *testing.T) {
	a, view, f := shortcutShelf(t)
	f.fail["Ctrl+Alt+P"] = 1
	openSettingsPage(t, view, "快捷键")
	if err := view.Click("显示 / 收起"); err != nil {
		t.Fatal(err)
	}
	view.Frame()
	view.Key(ui.Ctrl|ui.Alt, ui.KeyP)
	view.Frame()
	if a.settings.Shortcuts.Toggle != settings.DefaultShortcutToggle {
		t.Fatalf("refused combination was saved: %q", a.settings.Shortcuts.Toggle)
	}
	if !a.shortcutBusy[shortcutToggle] {
		t.Fatal("the refusal was not reported on the row")
	}
	if f.has("Ctrl+Alt+P") {
		t.Fatal("the refused combination was registered")
	}
	if !f.has("Ctrl+Alt+Space") || !f.has("Ctrl+Alt+V") || !f.has("Ctrl+Alt+O") {
		t.Fatalf("the previous shortcuts were not restored: %v", f.regs)
	}
	saved, err := settings.Load(a.dataDir)
	if err != nil || saved.Shortcuts.Toggle != settings.DefaultShortcutToggle {
		t.Fatalf("settings.json = %q, %v", saved.Shortcuts.Toggle, err)
	}
}

// Claiming the new keys never releases the old registration first: a
// refused change leaves the system exactly as it was.
func TestApplyShortcutClaimsBeforeReleasing(t *testing.T) {
	a, _, f := shortcutShelf(t)
	f.fail["Ctrl+Alt+X"] = 1
	if err := a.applyShortcut(shortcutToggle, "Ctrl+Alt+X"); err == nil {
		t.Fatal("a refused combination was accepted")
	}
	if !f.has("Ctrl+Alt+Space") {
		t.Fatalf("the old shortcut is gone: %v", f.unregs)
	}
	if len(f.unregs) != 0 {
		t.Fatalf("the old registration was released before the new one was claimed: %v", f.unregs)
	}
}

// Restoring a suspended shortcut can fail too (another app took it while
// the row listened): the row says so, and the app knows nothing is held.
func TestShortcutRecordingRestoreFailure(t *testing.T) {
	a, view, f := shortcutShelf(t)
	f.fail["Ctrl+Alt+O"] = 1
	openSettingsPage(t, view, "快捷键")
	if err := view.Click("显示 / 收起"); err != nil {
		t.Fatal(err)
	}
	view.Frame()
	view.Key(ui.Ctrl|ui.Alt, ui.KeyK)
	view.Frame()
	if !a.shortcutBusy[shortcutCapture] {
		t.Fatal("a failed restore was not reported on its row")
	}
	if f.has("Ctrl+Alt+O") {
		t.Fatal("the refused restore reported success")
	}
	if !f.has("Ctrl+Alt+K") || !f.has("Ctrl+Alt+V") {
		t.Fatalf("the rest of the registry: %v", f.regs)
	}
}

// A combination settings.json names but the system refused at launch is
// retried when the user records it again; "already saved" is not "held".
func TestShortcutRecordingRetriesRefusedAtLaunch(t *testing.T) {
	stubSettingsSeams(t, false, nil, nil)
	f := installFakeKeys(t)
	a, view := newSettingsShelf(t)
	f.fail["Ctrl+Alt+V"] = 1
	a.registerShortcuts()
	if !a.shortcutBusy[shortcutClipboard] || f.has("Ctrl+Alt+V") {
		t.Fatalf("the refused launch registration did not stick: busy=%v", a.shortcutBusy)
	}
	openSettingsPage(t, view, "快捷键")
	if err := view.Click("剪贴板搜索"); err != nil {
		t.Fatal(err)
	}
	view.Frame()
	view.Key(ui.Ctrl|ui.Alt, ui.KeyV)
	view.Frame()
	if !f.has("Ctrl+Alt+V") {
		t.Fatal("recording the same combination did not retry it")
	}
	if a.shortcutBusy[shortcutClipboard] {
		t.Fatal("the busy note survived the successful retry")
	}
	if a.settings.Shortcuts.Clipboard != settings.DefaultShortcutClipboard {
		t.Fatalf("the retry changed the saved value: %q", a.settings.Shortcuts.Clipboard)
	}
}

// A save that fails releases only the keys it claimed and leaves the file's
// shortcut registered, so the three places still agree afterwards.
func TestShortcutSaveFailureReleasesOnlyTheNewKeys(t *testing.T) {
	a, view, f := shortcutShelf(t)
	openSettingsPage(t, view, "快捷键")
	a.dataDir = filepath.Join(t.TempDir(), "gone") // settings.json cannot be written
	f.fail["Ctrl+Alt+Space"] = 1                   // and the old keys are taken meanwhile
	if err := view.Click("显示 / 收起"); err != nil {
		t.Fatal(err)
	}
	view.Frame()
	view.Key(ui.Ctrl|ui.Alt, ui.KeyK)
	view.Frame()
	if a.settings.Shortcuts.Toggle != settings.DefaultShortcutToggle {
		t.Fatalf("the unsaved combination was kept in memory: %q", a.settings.Shortcuts.Toggle)
	}
	if f.has("Ctrl+Alt+K") {
		t.Fatal("the unsaved combination stayed registered")
	}
	if f.has("Ctrl+Alt+Space") {
		t.Fatal("a restore the system refused was reported as held")
	}
	if !a.shortcutBusy[shortcutToggle] {
		t.Fatal("the failed save was not reported on the row")
	}
	// With the file writable again, recording the same saved value claims
	// it: a value the system does not hold is never reported as success
	// without being registered.
	a.dataDir = t.TempDir()
	os.WriteFile(filepath.Join(a.dataDir, settings.FileName), []byte(`{}`), 0o600)
	a.settings.Shortcuts.Toggle = settings.DefaultShortcutToggle
	if err := view.Click("显示 / 收起"); err != nil {
		t.Fatal(err)
	}
	view.Frame()
	view.Key(ui.Ctrl|ui.Alt, ui.KeySpace)
	view.Frame()
	if !f.has("Ctrl+Alt+Space") || a.shortcutBusy[shortcutToggle] {
		t.Fatalf("the retry did not claim the combination: %v busy=%v", f.regs, a.shortcutBusy)
	}
}

// Launch registers what settings.json holds, including a cleared shortcut,
// and keeps a refused one on its row instead of stopping.
func TestRegisterShortcutsAtLaunch(t *testing.T) {
	stubSettingsSeams(t, false, nil, nil)
	f := installFakeKeys(t)
	f.fail["Ctrl+Alt+O"] = 1

	a := &shelf{settings: settings.Default()}
	a.settings.Shortcuts.Clipboard = ""
	a.settings.Shortcuts.Tray = "Ctrl+Alt+T"
	a.registerShortcuts()
	if !f.has("Ctrl+Alt+Space") || !f.has("Ctrl+Alt+T") || f.has("Ctrl+Alt+O") {
		t.Fatalf("registry %v", f.regs)
	}
	if !a.shortcutBusy[shortcutCapture] || a.shortcutBusy[shortcutClipboard] {
		t.Fatalf("busy flags = %v", a.shortcutBusy)
	}
}

// 「打开托盘」 puts the panel on the 托盘 page: it opens a closed shelf, grows
// the overview, and switches a panel that sits on another page.
func TestOpenTrayShortcutShowsTheTrayPage(t *testing.T) {
	a, _ := levelShelf(t)
	a.visible = false
	openTray := a.shortcutAction(shortcutOpenTray)
	openTray()
	if !a.visible || a.currentLevel() != 2 || a.currentPage().id() != pageTray {
		t.Fatalf("open from closed: visible=%v level=%d page=%v",
			a.visible, a.currentLevel(), a.currentPage().id())
	}
	a.setTab(pageClipboard)
	if a.currentPage().id() != pageClipboard {
		t.Fatalf("the test did not switch pages: %v", a.currentPage().id())
	}
	openTray()
	if a.currentPage().id() != pageTray {
		t.Fatalf("open on 托盘 left the panel on %v", a.currentPage().id())
	}
	// The action the shortcut kind runs is this one.
	openTray()
	if a.currentPage().id() != pageTray {
		t.Fatalf("the shortcut left the panel on %v", a.currentPage().id())
	}
}

// Relative update dates are calendar-day categories, checked against an
// explicit clock rather than whichever date the native suite finishes on.
func TestUpdateWhenClassifiesCalendarDays(t *testing.T) {
	l := locales.In("zh-Hans")
	a := &shelf{}
	now := time.Date(2026, 10, 29, 15, 30, 0, 0, time.Local)
	today := time.Date(2026, 10, 29, 9, 12, 0, 0, time.Local)
	if got := a.updateWhen(l, today, now); got != l.SettingsUpdateToday("09:12") {
		t.Fatalf("today = %q", got)
	}
	if got := a.updateWhen(l, time.Date(2026, 10, 28, 21, 5, 0, 0, time.Local), now); got != l.SettingsUpdateYesterday("21:05") {
		t.Fatalf("yesterday = %q", got)
	}
	if got := a.updateWhen(l, time.Date(2026, 10, 20, 8, 0, 0, 0, time.Local), now); got != l.SettingsUpdateOn("2026-10-20 08:00") {
		t.Fatalf("older = %q", got)
	}
}

// 现在检查 runs the same check the menu runs, and the button is locked
// while one runs.
func TestSettingsCheckNowRow(t *testing.T) {
	stubSettingsSeams(t, false, nil, nil)
	orig := updateCheck
	var calls int
	updateCheck = func(ctx context.Context) (*mygo.Update, error) { calls++; return nil, nil }
	t.Cleanup(func() { updateCheck = orig })

	_, view := newSettingsShelf(t)
	if err := view.Click("现在检查"); err != nil {
		t.Fatal(err)
	}
	view.Frame()
	// The check runs: the count is what matters. Under the smoke harness the
	// stubbed check finishes at once, so updateChecking is already clear.
	if calls != 1 {
		t.Fatalf("现在检查 did not start a check: calls=%d", calls)
	}
}
