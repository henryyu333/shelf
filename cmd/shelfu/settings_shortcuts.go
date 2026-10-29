//go:build darwin

package main

// The global shortcuts, as preferences: the Settings window's 快捷键 page
// records one by key press, saves it in settings.json and re-registers it
// on the spot, so a change applies without a restart. The registry and the
// file are one: a combination the system refuses (another app holds it, a
// typo a hand-edited file) never replaces the registered one, and the row
// says so.
//
// The shortcuts the panel itself answers — Esc, ↑ ↓ ↩ — are not global keys
// and are not preferences: the page shows them read-only.

import (
	"errors"
	"fmt"
	"strings"

	"github.com/egoist/mygo/ui"

	"shelf/internal/settings"
)

// shortcutKind names one of the editable global shortcuts.
type shortcutKind int

const (
	// shortcutNone is the zero value, and settingsRecording's while no row
	// records.
	shortcutNone shortcutKind = iota
	shortcutToggle
	shortcutClipboard
	shortcutCapture
	shortcutOpenTray
	shortcutKindCount
)

// errShortcutBusy is what a save reports when the combination is taken: by
// another of the app's shortcuts, or by the system or another app.
var errShortcutBusy = errors.New("shortcut is in use")

// field is the settings.json field that holds the kind's combination.
func (k shortcutKind) field(s *settings.ShortcutSettings) *string {
	switch k {
	case shortcutToggle:
		return &s.Toggle
	case shortcutClipboard:
		return &s.Clipboard
	case shortcutCapture:
		return &s.Capture
	case shortcutOpenTray:
		return &s.Tray
	}
	return nil
}

// action is what pressing the kind's shortcut runs.
func (a *shelf) shortcutAction(k shortcutKind) func() {
	switch k {
	case shortcutToggle:
		return a.toggle
	case shortcutClipboard:
		return a.openClipboardSearch
	case shortcutCapture:
		return a.startScreenOCR
	case shortcutOpenTray:
		return func() { a.show(false) }
	}
	return func() {}
}

// shortcutAcc is the kind's saved combination, "" for none.
func (a *shelf) shortcutAcc(k shortcutKind) string {
	if p := k.field(&a.settings.Shortcuts); p != nil {
		return *p
	}
	return ""
}

// registerShortcuts registers every saved shortcut at launch, and keeps a
// failure on its row instead of stopping the launch: the menu bar icon
// still opens the shelf, and the 快捷键 page says which combination is
// taken.
func (a *shelf) registerShortcuts() {
	if a.shortcutBusy == nil {
		a.shortcutBusy = map[shortcutKind]bool{}
	}
	l := a.appLoc()
	for k := shortcutToggle; k < shortcutKindCount; k++ {
		acc := a.shortcutAcc(k)
		if acc == "" {
			continue
		}
		if err := registerKey(acc, a.shortcutAction(k)); err != nil {
			a.shortcutBusy[k] = true
			switch k {
			case shortcutToggle:
				a.notice(l.StartupShortcutBusy(err.Error()))
			case shortcutClipboard:
				a.notice(l.StartupClipboardShortcutBusy(err.Error()))
			case shortcutCapture:
				a.notice(l.StartupCaptureShortcutBusy(err.Error()))
			default:
				a.notice(l.StartupShortcutBusy(err.Error()))
			}
		}
	}
}

// applyShortcut makes acc the kind's shortcut: it claims the keys first and
// saves only once they are the app's own, so a refused combination leaves
// both the running app and settings.json on the previous one.
func (a *shelf) applyShortcut(k shortcutKind, acc string) error {
	cur := a.shortcutAcc(k)
	if acc == cur {
		return nil
	}
	if a.settingsBroken {
		// The page is locked while the file is unreadable; this is the
		// belt to that suspenders.
		return errors.New("settings.json is not readable")
	}
	if acc != "" {
		for other := shortcutToggle; other < shortcutKindCount; other++ {
			if other != k && a.shortcutAcc(other) == acc {
				return errShortcutBusy
			}
		}
	}
	if cur != "" {
		unregisterKey(cur)
	}
	if acc != "" {
		if err := registerKey(acc, a.shortcutAction(k)); err != nil {
			if cur != "" {
				_ = registerKey(cur, a.shortcutAction(k))
			}
			return errShortcutBusy
		}
	}
	next := a.settings
	*k.field(&next.Shortcuts) = acc
	if !a.applySettings(next) {
		// The file could not take it: put the keys back so what runs and
		// what is saved cannot disagree.
		if acc != "" {
			unregisterKey(acc)
		}
		if cur != "" {
			_ = registerKey(cur, a.shortcutAction(k))
		}
		return errors.New("settings.json could not be saved")
	}
	return nil
}

// finishShortcut ends a recording with acc ("" clears the shortcut) and
// keeps a failure visible on the row.
func (a *shelf) finishShortcut(acc string) {
	k := a.settingsRecording
	a.settingsRecording = shortcutNone
	if k == shortcutNone {
		return
	}
	if a.shortcutBusy == nil {
		a.shortcutBusy = map[shortcutKind]bool{}
	}
	if err := a.applyShortcut(k, acc); err != nil {
		a.shortcutBusy[k] = true
	} else {
		delete(a.shortcutBusy, k)
	}
	a.repaintSettings()
}

// settingsCaptureLayer is the transparent layer that holds the keyboard
// while a shortcut records: every key reaches the recorder, and none of it
// reaches the window's own Esc/⌘W or the controls underneath.
func (a *shelf) settingsCaptureLayer(c *ui.Context) {
	ui.Box(c.Key("rec-capture")).Absolute().Top(0).Left(0).FillWidth().FillHeight().
		Focusable().AutoFocus().HandleInput(a.shortcutCaptureInput)
}

// shortcutCaptureInput is the recorder: Esc cancels, Delete clears, and a
// key with Control, Option or Command saves that combination. A bare key is
// ignored — it would swallow that key everywhere.
func (a *shelf) shortcutCaptureInput(ev ui.InputEvent) bool {
	if a.settingsRecording == shortcutNone {
		return false
	}
	switch ev.Kind {
	case ui.InputKeyDown:
	case ui.InputText, ui.InputCompose:
		return true
	case ui.InputPointerDown:
		// A click outside the keycaps cancels, as Esc does.
		a.settingsRecording = shortcutNone
		a.repaintSettings()
		return true
	default:
		return false
	}
	switch ev.Key {
	case ui.KeyEscape:
		a.settingsRecording = shortcutNone
		a.repaintSettings()
		return true
	case ui.KeyBackspace, ui.KeyDelete:
		a.finishShortcut("")
		return true
	}
	acc, ok := shortcutAccelerator(ev.Mods, ev.Key)
	if !ok {
		return true
	}
	a.finishShortcut(acc)
	return true
}

// shortcutAccelerator renders a key press as the accelerator mygo's global
// shortcuts take ("Ctrl+Alt+Space"), in the order its canonical form uses.
// ok is false for a bare key and for keys the accelerator grammar has no
// name for.
func shortcutAccelerator(mods ui.Modifiers, key ui.Key) (string, bool) {
	if mods&(ui.Ctrl|ui.Alt|ui.Super) == 0 {
		return "", false
	}
	token, ok := acceleratorKey(key)
	if !ok {
		return "", false
	}
	var parts []string
	if mods&ui.Super != 0 {
		parts = append(parts, "Super")
	}
	if mods&ui.Ctrl != 0 {
		parts = append(parts, "Ctrl")
	}
	if mods&ui.Alt != 0 {
		parts = append(parts, "Alt")
	}
	if mods&ui.Shift != 0 {
		parts = append(parts, "Shift")
	}
	return strings.Join(append(parts, token), "+"), true
}

// acceleratorKey names one key the way accelerator syntax does.
func acceleratorKey(key ui.Key) (string, bool) {
	switch {
	case key >= ui.KeyA && key <= ui.KeyZ:
		return string(rune('A' + int(key-ui.KeyA))), true
	case key >= ui.Key0 && key <= ui.Key9:
		return string(rune('0' + int(key-ui.Key0))), true
	case key >= ui.KeyF1 && key <= ui.KeyF12:
		return fmt.Sprintf("F%d", int(key-ui.KeyF1)+1), true
	}
	named := map[ui.Key]string{
		ui.KeySpace: "Space", ui.KeyTab: "Tab", ui.KeyEnter: "Enter",
		ui.KeyBackspace: "Backspace", ui.KeyDelete: "Delete", ui.KeyInsert: "Insert",
		ui.KeyHome: "Home", ui.KeyEnd: "End", ui.KeyPageUp: "PageUp", ui.KeyPageDown: "PageDown",
		ui.KeyUp: "Up", ui.KeyDown: "Down", ui.KeyLeft: "Left", ui.KeyRight: "Right",
		ui.KeyMinus: "-", ui.KeyEqual: "=", ui.KeyComma: ",", ui.KeyPeriod: ".",
		ui.KeySlash: "/", ui.KeySemicolon: ";", ui.KeyQuote: "'",
		ui.KeyBracketLeft: "[", ui.KeyBracketRight: "]", ui.KeyBackslash: "\\", ui.KeyBackquote: "`",
	}
	name, ok := named[key]
	return name, ok
}

// shortcutTokens splits an accelerator into the keycaps the page draws:
// modifiers as glyphs ("⌃", "⌥", "⌘", "⇧") and the key as it is spelled.
// ok is false for an empty or malformed accelerator, so the row can show
// 「点一下设置」 instead of claiming a shortcut.
func shortcutTokens(acc string) (tokens []string, ok bool) {
	if strings.TrimSpace(acc) == "" {
		return nil, false
	}
	parts := strings.Split(acc, "+")
	tokens = make([]string, 0, len(parts))
	for i, raw := range parts {
		token := strings.TrimSpace(raw)
		if token == "" {
			return nil, false
		}
		if i == len(parts)-1 {
			switch {
			case strings.EqualFold(token, "space"):
				token = "Space"
			case len([]rune(token)) == 1:
				token = strings.ToUpper(token)
			}
			tokens = append(tokens, token)
			continue
		}
		switch strings.ToLower(token) {
		case "cmd", "command", "super", "meta", "win", "cmdorctrl":
			tokens = append(tokens, "⌘")
		case "ctrl", "control":
			tokens = append(tokens, "⌃")
		case "alt", "option":
			tokens = append(tokens, "⌥")
		case "shift":
			tokens = append(tokens, "⇧")
		default:
			return nil, false
		}
	}
	return tokens, true
}
