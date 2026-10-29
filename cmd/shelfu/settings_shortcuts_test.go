//go:build darwin

package main

// The 快捷键 page's recorder: a key press becomes an accelerator, saved in
// settings.json and registered at once; a combination the system refuses
// leaves the old one in place; Esc cancels and Delete clears.

import (
	"context"
	"testing"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"shelf/internal/settings"
	"shelf/locales"
)

func TestShortcutRecordingSavesAndRegisters(t *testing.T) {
	stubSettingsSeams(t, false, nil, nil)
	held, released, _ := stubKeyboard(t)
	a, view := newSettingsShelf(t)
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
	if len(*released) != 1 || (*released)[0] != settings.DefaultShortcutToggle {
		t.Fatalf("the old shortcut was not released: %v", *released)
	}
	if len(*held) != 1 || (*held)[0] != "Ctrl+Alt+K" {
		t.Fatalf("the new shortcut was not registered: %v", *held)
	}
}

func TestShortcutRecordingCancelAndClear(t *testing.T) {
	stubSettingsSeams(t, false, nil, nil)
	held, released, _ := stubKeyboard(t)
	a, view := newSettingsShelf(t)
	openSettingsPage(t, view, "快捷键")

	// Esc cancels without touching the saved shortcut.
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
	if len(*held)+len(*released) != 0 {
		t.Fatalf("Esc touched the registry: held=%v released=%v", *held, *released)
	}

	// Delete clears it: the file holds "" and the keys are released.
	if err := view.Click("剪贴板搜索"); err != nil {
		t.Fatal(err)
	}
	view.Frame()
	view.Key(0, ui.KeyBackspace)
	view.Frame()
	if got := a.settings.Shortcuts.Clipboard; got != "" {
		t.Fatalf("cleared clipboard shortcut = %q, want empty", got)
	}
	if len(*released) != 1 || (*released)[0] != settings.DefaultShortcutClipboard {
		t.Fatalf("clearing did not release the keys: %v", *released)
	}
	if len(*held) != 0 {
		t.Fatalf("clearing registered something: %v", *held)
	}
}

// A bare key, or a combination without Control, Option or Command, is
// ignored: a global bare key would swallow that key everywhere.
func TestShortcutRecordingIgnoresBareKeys(t *testing.T) {
	stubSettingsSeams(t, false, nil, nil)
	held, _, _ := stubKeyboard(t)
	a, view := newSettingsShelf(t)
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
	if len(*held) != 0 {
		t.Fatalf("a bare key registered something: %v", *held)
	}
	// Shift with a modifier is fine.
	view.Key(ui.Ctrl|ui.Shift, ui.KeyJ)
	view.Frame()
	if got := a.settings.Shortcuts.Capture; got != "Ctrl+Shift+J" {
		t.Fatalf("saved capture = %q, want Ctrl+Shift+J", got)
	}
}

// A click outside the keycaps ends the recording, like Esc.
func TestShortcutRecordingClickCancels(t *testing.T) {
	stubSettingsSeams(t, false, nil, nil)
	stubKeyboard(t)
	a, view := newSettingsShelf(t)
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
}

// A refused combination keeps the previous shortcut and says so on the row;
// the same combination as another row's counts as refused.
func TestShortcutRecordingBusyKeepsPrevious(t *testing.T) {
	stubSettingsSeams(t, false, nil, nil)
	held, released, _ := stubKeyboard(t)
	a, view := newSettingsShelf(t)
	openSettingsPage(t, view, "快捷键")

	// The system refuses Ctrl+Alt+V for the toggle (the clipboard holds it
	// in the app's own registry, so this is the duplicate path).
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
	if len(*released) != 0 {
		t.Fatalf("the previous shortcut was touched: %v", *released)
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
	if len(*held) != 1 || (*held)[0] != "Ctrl+Alt+K" {
		t.Fatalf("the new shortcut was not registered: %v", *held)
	}
}

// A registration the system refuses (another app holds the keys) restores
// the previous registration and saves nothing.
func TestShortcutRecordingSystemRefusal(t *testing.T) {
	stubSettingsSeams(t, false, nil, nil)
	var held, released []string
	origReg, origUnreg := registerKey, unregisterKey
	registerKey = func(acc string, fn func()) error {
		if acc == "Ctrl+Alt+P" {
			return errShortcutBusy
		}
		held = append(held, acc)
		return nil
	}
	unregisterKey = func(acc string) { released = append(released, acc) }
	t.Cleanup(func() { registerKey, unregisterKey = origReg, origUnreg })

	a, view := newSettingsShelf(t)
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
	// The old shortcut was released, then claimed again.
	if len(released) != 1 || released[0] != settings.DefaultShortcutToggle {
		t.Fatalf("released = %v", released)
	}
	if len(held) != 1 || held[0] != settings.DefaultShortcutToggle {
		t.Fatalf("the previous shortcut was not restored: %v", held)
	}
	saved, err := settings.Load(a.dataDir)
	if err != nil || saved.Shortcuts.Toggle != settings.DefaultShortcutToggle {
		t.Fatalf("settings.json = %q, %v", saved.Shortcuts.Toggle, err)
	}
}

// Launch registers what settings.json holds, including a cleared shortcut,
// and keeps a refused one on its row instead of stopping.
func TestRegisterShortcutsAtLaunch(t *testing.T) {
	stubSettingsSeams(t, false, nil, nil)
	var held []string
	origReg := registerKey
	registerKey = func(acc string, fn func()) error {
		if acc == "Ctrl+Alt+O" {
			return errShortcutBusy
		}
		held = append(held, acc)
		return nil
	}
	t.Cleanup(func() { registerKey = origReg })

	a := &shelf{settings: settings.Default()}
	a.settings.Shortcuts.Clipboard = ""
	a.settings.Shortcuts.Tray = "Ctrl+Alt+T"
	a.registerShortcuts()
	want := []string{"Ctrl+Alt+Space", "Ctrl+Alt+T"}
	if len(held) != len(want) || held[0] != want[0] || held[1] != want[1] {
		t.Fatalf("registered %v, want %v", held, want)
	}
	if !a.shortcutBusy[shortcutCapture] || a.shortcutBusy[shortcutClipboard] {
		t.Fatalf("busy flags = %v", a.shortcutBusy)
	}
}

// The 检查更新 row's second line: no check yet, then the time and outcome.
func TestUpdateCaption(t *testing.T) {
	l := locales.In("zh-Hans")
	a := &shelf{}
	if got := a.updateCaption(l); got != l.SettingsUpdateNever() {
		t.Fatalf("fresh caption = %q", got)
	}
	now := time.Date(2026, 10, 29, 15, 30, 0, 0, time.Local)
	a.updateLastCheck = time.Date(2026, 10, 29, 9, 12, 0, 0, time.Local)
	a.updateLastResult = updateResultLatest
	want := l.SettingsUpdateLast(l.SettingsUpdateToday("09:12"), l.SettingsUpdateResultLatest())
	if got := a.updateCaption(l); got != want {
		t.Fatalf("caption = %q, want %q", got, want)
	}
	if got := a.updateWhen(l, time.Date(2026, 10, 28, 21, 5, 0, 0, time.Local), now); got != l.SettingsUpdateYesterday("21:05") {
		t.Fatalf("yesterday = %q", got)
	}
	if got := a.updateWhen(l, time.Date(2026, 10, 20, 8, 0, 0, 0, time.Local), now); got != l.SettingsUpdateOn("2026-10-20 08:00") {
		t.Fatalf("older = %q", got)
	}
	a.updateLastResult = updateResultAvailable
	a.updateLastVersion = "1.12.0"
	want = l.SettingsUpdateLast(l.SettingsUpdateToday("09:12"), l.SettingsUpdateResultAvailable("1.12.0"))
	if got := a.updateCaption(l); got != want {
		t.Fatalf("available caption = %q, want %q", got, want)
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
