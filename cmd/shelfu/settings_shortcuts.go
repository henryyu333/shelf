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
		return a.openTray
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

// shortcutHeld is the combination the system currently holds for the kind,
// "" for none. The saved value and this one differ exactly when the system
// refused a combination settings.json still names (a hand edit, another app
// holding it at launch).
func (a *shelf) shortcutHeld(k shortcutKind) string {
	return a.shortcutReg[k]
}

// heldShortcut records what the system now holds for the kind. It is the
// one place shortcutReg changes, so no register or unregister can leave it
// stale.
func (a *shelf) heldShortcut(k shortcutKind, acc string) {
	if a.shortcutReg == nil {
		a.shortcutReg = map[shortcutKind]string{}
	}
	if acc == "" {
		delete(a.shortcutReg, k)
		return
	}
	a.shortcutReg[k] = acc
}

// openTray shows the panel on the 托盘 page: it opens the shelf when it is
// closed, grows it from the overview, and switches it when another page is
// showing.
func (a *shelf) openTray() {
	if !a.visible {
		a.show(false)
	}
	if a.currentLevel() == 1 {
		a.setLevel(2)
	}
	a.setTab(pageTray)
	if a.win != nil {
		a.win.Invalidate()
	}
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
			continue
		}
		a.heldShortcut(k, acc)
	}
}

// suspendShortcuts releases every registered shortcut, and restoreShortcuts
// claims them back. Recording uses the pair: while a row listens for keys, a
// key the app already holds must reach the recorder instead of running its
// old action.
func (a *shelf) suspendShortcuts() {
	a.shortcutsSuspended = true
	for k := shortcutToggle; k < shortcutKindCount; k++ {
		acc := a.shortcutHeld(k)
		if acc == "" {
			continue
		}
		unregisterKey(acc)
		a.heldShortcut(k, "")
	}
}

// restoreShortcuts claims every saved shortcut the system does not hold any
// more, and keeps a refusal on the row: the combination is taken while the
// app needed it back.
func (a *shelf) restoreShortcuts() {
	if !a.shortcutsSuspended {
		// Nothing stood down: a shelf that never registered (a headless
		// window, a test) must not claim the user's combinations here.
		return
	}
	a.shortcutsSuspended = false
	if a.shortcutBusy == nil {
		a.shortcutBusy = map[shortcutKind]bool{}
	}
	for k := shortcutToggle; k < shortcutKindCount; k++ {
		acc := a.shortcutAcc(k)
		if acc == "" || a.shortcutHeld(k) == acc {
			continue
		}
		if err := registerKey(acc, a.shortcutAction(k)); err != nil {
			a.shortcutBusy[k] = true
			continue
		}
		a.heldShortcut(k, acc)
		// The combination is the app's again: whatever kept it busy before
		// — a refused launch registration, a failed restore — is over.
		delete(a.shortcutBusy, k)
	}
}

// settingsBlur ends a recording when the Settings window stops being key —
// the user switched to another app or window: the suspended shortcuts go
// back at once instead of staying dead while the user works elsewhere.
func (a *shelf) settingsBlur() {
	if a.quitting || a.settingsRecording == shortcutNone {
		return
	}
	a.cancelShortcutRecording()
}

// startShortcutRecording puts one row in listening mode: the app's own
// shortcuts stand down so the keys reach the recorder.
func (a *shelf) startShortcutRecording(k shortcutKind) {
	if a.settingsRecording != shortcutNone {
		return
	}
	a.suspendShortcuts()
	a.settingsRecording = k
	a.repaintSettings()
}

// cancelShortcutRecording ends the listening mode without a change and
// claims the suspended shortcuts back.
func (a *shelf) cancelShortcutRecording() {
	active := a.settingsRecording != shortcutNone
	a.settingsRecording = shortcutNone
	a.restoreShortcuts()
	if active {
		a.repaintSettings()
	}
}

// endShortcutRecording ends a listening row without repainting: the window
// is closing or leaving the page, which repaint themselves.
func (a *shelf) endShortcutRecording() {
	if a.settingsRecording == shortcutNone {
		return
	}
	a.settingsRecording = shortcutNone
	if a.quitting {
		// The app is going away: nothing may claim keys on the way out.
		a.shortcutsSuspended = false
		return
	}
	a.restoreShortcuts()
}

// applyShortcut makes acc the kind's shortcut. It claims the new keys
// before it releases the old ones and before it writes the file, so a
// refused combination changes nothing: the registry, settings.json and the
// running app stay on the previous one. A file that cannot be written
// releases the keys it just claimed.
func (a *shelf) applyShortcut(k shortcutKind, acc string) error {
	curSaved := a.shortcutAcc(k)
	if acc == curSaved && a.shortcutHeld(k) == acc {
		// Saved and held: nothing to do. A saved value the system refused
		// ("" held) is not this case: recording it again retries it.
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
	held := a.shortcutHeld(k)
	alreadyHeld := held == acc
	if acc != "" && !alreadyHeld {
		if err := registerKey(acc, a.shortcutAction(k)); err != nil {
			// The old registration was never released: nothing to undo.
			return errShortcutBusy
		}
	}
	next := a.settings
	*k.field(&next.Shortcuts) = acc
	if !a.applySettings(next) {
		if acc != "" && !alreadyHeld {
			unregisterKey(acc)
		}
		return errors.New("settings.json could not be saved")
	}
	a.heldShortcut(k, acc)
	if held != "" && held != acc {
		unregisterKey(held)
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
	err := a.applyShortcut(k, acc)
	// Whatever happened, the rows that stood down for the recording come
	// back; a refusal stays on its row.
	a.restoreShortcuts()
	if err != nil {
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
		a.cancelShortcutRecording()
		return true
	default:
		return false
	}
	switch ev.Key {
	case ui.KeyEscape:
		a.cancelShortcutRecording()
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
