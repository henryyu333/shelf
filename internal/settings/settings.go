// Package settings keeps the user's preferences in settings.json inside the
// data directory, next to the shelf's own state.
package settings

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

// FileName is the preferences file inside the data directory.
const FileName = "settings.json"

// Settings are the user-adjustable preferences. Every field must have its
// default in Default: a key missing from the file keeps that default.
type Settings struct {
	// RetentionDays removes items older than this many days; 0 or less
	// keeps everything.
	RetentionDays int `json:"retentionDays"`
	// ClipboardLimit caps how many unpinned clipboard texts are kept;
	// values below 1 load as the default.
	ClipboardLimit int `json:"clipboardLimit"`
	// ExcludedApps lists bundle identifiers whose copies are never
	// recorded into the shelf.
	ExcludedApps []string `json:"excludedApps"`
	// IslandNowPlaying shows the now-playing peek wings and panel card.
	IslandNowPlaying bool `json:"islandNowPlaying"`
	// AutoCheckUpdates checks the GitHub release feed for a newer version
	// in the background; it is the app's only network access.
	AutoCheckUpdates bool `json:"autoCheckUpdates"`
	// RemoveAfterDrag takes an item off the tray once it was dragged out
	// and dropped; pinned items stay.
	RemoveAfterDrag bool `json:"removeAfterDrag"`
	// ShellStyle is the closed shelf's outer shape on a screen with a
	// camera housing. Screens without one always draw the island.
	ShellStyle ShellStyle `json:"shellStyle"`
	// PanelAppearance is the expanded panel's paint: follow the system,
	// pure black, or glass.
	PanelAppearance PanelAppearance `json:"panelAppearance"`
	// SnapWindows lets a window dragged by its title bar to the notch pick
	// one of six arrangements.
	SnapWindows bool `json:"snapWindows"`
	// Language is the language the interface says: "" follows the system,
	// else a tag of a language the app ships ("zh-Hans", "en"). The app
	// keeps it here, not in the system's language settings.
	Language string `json:"language"`
	// AIUsage is the AI 用量 page: off by default; each source (quota and
	// local usage) can be switched separately.
	AIUsage AIUsageSettings `json:"aiUsage"`
	// LiveActivities is the 实时活动 group: the charging, headphone and
	// download notices on the notch. Every switch is on for a fresh
	// install; a settings.json without the object keeps all three.
	LiveActivities LiveActivitySettings `json:"liveActivities"`
	// IslandPos is where the floating island was left on each display, by
	// display id. A display with no entry keeps the default position; the
	// notch style ignores the whole map.
	IslandPos map[string]IslandPos `json:"islandPos,omitempty"`
	// Shortcuts are the global keyboard shortcuts. They are user-editable
	// in the Settings window, and every one is an Electron-style
	// accelerator ("Ctrl+Alt+Space").
	Shortcuts ShortcutSettings `json:"shortcuts"`
}

// ShortcutSettings are the global keyboard shortcuts. A missing key keeps
// its default (see Default); an explicit empty string turns that shortcut
// off, which is what Delete in the Settings window saves.
type ShortcutSettings struct {
	Toggle    string `json:"toggle"`
	Clipboard string `json:"clipboard"`
	Capture   string `json:"capture"`
	Tray      string `json:"tray"`
}

// The shortcut defaults: the tray toggle, clipboard search and text
// capture ship set; opening the tray alone has none.
const (
	DefaultShortcutToggle    = "Ctrl+Alt+Space"
	DefaultShortcutClipboard = "Ctrl+Alt+V"
	DefaultShortcutCapture   = "Ctrl+Alt+O"
	DefaultShortcutTray      = ""
)

// IslandPos is the floating island's place on one display: the shell's
// centre X and its top Y, measured from the display's origin. Storing the
// spot rather than a delta keeps it meaningful when a display's origin
// moves in 显示器 settings.
type IslandPos struct {
	X int `json:"x"`
	Y int `json:"y"`
}

// LiveActivitySettings switches the three realtime activities. A missing
// key keeps the default (on), so an older settings.json shows all of them
// after the update.
type LiveActivitySettings struct {
	Charging   bool `json:"charging"`
	Headphones bool `json:"headphones"`
	Downloads  bool `json:"downloads"`
}

// ShellStyle picks the closed shelf's outer shape on a screen with a
// camera housing. A screen without one always draws the island, whatever
// this says ("notch" is the default).
type ShellStyle string

const (
	ShellStyleNotch  ShellStyle = "notch"
	ShellStyleIsland ShellStyle = "island"
)

// Normalized maps an unknown or empty value (an older settings.json, a
// hand edit) to the default style.
func (s ShellStyle) Normalized() ShellStyle {
	if s == ShellStyleIsland {
		return ShellStyleIsland
	}
	return ShellStyleNotch
}

// PanelAppearance picks how the expanded panel is painted: follow the
// system (black in Dark Mode, dark glass in Light Mode), always black, or
// always glass. The island style is black whatever this says, and Reduce
// Transparency turns glass black too.
type PanelAppearance string

const (
	PanelAppearanceFollow PanelAppearance = "follow"
	PanelAppearanceBlack  PanelAppearance = "black"
	PanelAppearanceGlass  PanelAppearance = "glass"
)

// Normalized maps an unknown or empty value (an older settings.json, a
// hand edit) to the default, follow the system.
func (p PanelAppearance) Normalized() PanelAppearance {
	switch p {
	case PanelAppearanceBlack, PanelAppearanceGlass:
		return p
	}
	return PanelAppearanceFollow
}

// AIUsageSettings is the aiUsage object in settings.json. Enabled turns
// the AI 用量 page on; the rest pick which data the page reads: the Codex
// and Claude Code subscription quotas, and the local token usage of omp,
// Claude Code and Codex.
type AIUsageSettings struct {
	Enabled     bool `json:"enabled"`
	QuotaCodex  bool `json:"quotaCodex"`
	QuotaClaude bool `json:"quotaClaude"`
	LocalOmp    bool `json:"localOmp"`
	LocalClaude bool `json:"localClaude"`
	LocalCodex  bool `json:"localCodex"`
	// QuotaLeft shows how much of a quota window is left instead of how
	// much is used; the used share is the default, as Magpie shows it.
	QuotaLeft bool `json:"quotaLeft"`
}

// defaultClipboardLimit is the clipboard text history cap a fresh
// install uses.
const defaultClipboardLimit = 30

// Default is what a fresh install uses.
func Default() Settings {
	return Settings{
		ClipboardLimit:   defaultClipboardLimit,
		IslandNowPlaying: true,
		AutoCheckUpdates: true,
		RemoveAfterDrag:  false,
		SnapWindows:      true,
		ShellStyle:       ShellStyleNotch,
		PanelAppearance:  PanelAppearanceFollow,
		LiveActivities: LiveActivitySettings{
			Charging:   true,
			Headphones: true,
			Downloads:  true,
		},
		AIUsage: AIUsageSettings{
			Enabled:     false,
			QuotaCodex:  true,
			QuotaClaude: true,
			LocalOmp:    true,
			LocalClaude: true,
			LocalCodex:  true,
		},
		Shortcuts: ShortcutSettings{
			Toggle:    DefaultShortcutToggle,
			Clipboard: DefaultShortcutClipboard,
			Capture:   DefaultShortcutCapture,
			Tray:      DefaultShortcutTray,
		},
	}
}

// Save replaces dir/settings.json with s, atomically.
func Save(dir string, s Settings) error {
	if err := write(dir, s); err != nil {
		return fmt.Errorf("settings: saving %s: %w", FileName, err)
	}
	return nil
}

// Load reads dir/settings.json over the defaults.
//
// A missing file is created with the defaults so the user can find and edit
// it. A file that cannot be read or parsed is never rewritten: Load returns
// the defaults together with the error, and the caller decides how to tell
// the user.
func Load(dir string) (Settings, error) {
	s := Default()
	path := filepath.Join(dir, FileName)
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		if werr := write(dir, s); werr != nil {
			return s, fmt.Errorf("settings: creating %s: %w", FileName, werr)
		}
		return s, nil
	}
	if err != nil {
		return s, fmt.Errorf("settings: reading %s: %w", FileName, err)
	}
	if err := json.Unmarshal(data, &s); err != nil {
		return Default(), fmt.Errorf("settings: %s is not valid JSON, using defaults: %w", FileName, err)
	}
	if s.ClipboardLimit < 1 {
		s.ClipboardLimit = defaultClipboardLimit
	}
	s.ShellStyle = s.ShellStyle.Normalized()
	s.PanelAppearance = s.PanelAppearance.Normalized()
	return s, nil
}

// LanguageOf reads the language dir/settings.json names, without creating,
// rewriting or otherwise touching the file: the startup path that cannot open
// the data directory still has to say its refusal in the user's language, and
// it must leave the directory as it found it. ok is false when the file is
// missing, unreadable or not JSON, so the caller can try another directory; a
// file that parses reports its language, "" for follow the system.
func LanguageOf(dir string) (language string, ok bool) {
	data, err := os.ReadFile(filepath.Join(dir, FileName))
	if err != nil {
		return "", false
	}
	var s struct {
		Language string `json:"language"`
	}
	if err := json.Unmarshal(data, &s); err != nil {
		return "", false
	}
	return s.Language, true
}

// write saves s atomically: a temporary file in dir renamed over the target,
// so a crash never leaves a half-written settings.json.
func write(dir string, s Settings) error {
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	tmp, err := os.CreateTemp(dir, "."+FileName+".tmp-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName) // no-op after a successful rename
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, filepath.Join(dir, FileName))
}
