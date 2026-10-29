package settings

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestMissingFileIsCreatedWithDefaults(t *testing.T) {
	dir := t.TempDir()
	s, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(s, Default()) {
		t.Fatalf("fresh load = %+v, want defaults %+v", s, Default())
	}
	again, err := Load(dir)
	if err != nil || !reflect.DeepEqual(again, Default()) {
		t.Fatalf("reloading the created file = %+v, %v", again, err)
	}
}

func TestMissingKeyKeepsDefault(t *testing.T) {
	// A file from an older version (or hand-edited) without the key must not
	// silently turn a feature off through Go's zero value.
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, FileName), []byte(`{"somethingElse": 1}`), 0o600); err != nil {
		t.Fatal(err)
	}
	s, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if s.ClipboardLimit != 30 {
		t.Fatalf("missing clipboardLimit = %d, want 30", s.ClipboardLimit)
	}
	if len(s.ExcludedApps) != 0 {
		t.Fatalf("missing excludedApps = %v, want empty", s.ExcludedApps)
	}
	if !s.IslandNowPlaying {
		t.Fatalf("missing islandNowPlaying must stay on: %+v", s)
	}
}

// The now-playing toggle still loads from the file, and the removed
// battery/volume island keys in a legacy settings.json are ignored
// (unknown keys never fail a load) while the rest keep their defaults.
// snapWindows defaults on: a file without the key (older versions) must
// not silently turn window snapping off, and an explicit false is honoured.
func TestSnapWindowsLoad(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, FileName), []byte(`{}`), 0o600); err != nil {
		t.Fatal(err)
	}
	s, err := Load(dir)
	if err != nil || !s.SnapWindows {
		t.Fatalf("missing snapWindows = %v, %v; want true", s.SnapWindows, err)
	}
	if err := os.WriteFile(filepath.Join(dir, FileName), []byte(`{"snapWindows": false}`), 0o600); err != nil {
		t.Fatal(err)
	}
	s, err = Load(dir)
	if err != nil || s.SnapWindows {
		t.Fatalf("snapWindows: false => %v, %v; want false", s.SnapWindows, err)
	}
}

func TestNowPlayingToggleAndLegacyIslandKeysLoad(t *testing.T) {
	dir := t.TempDir()
	legacy := `{"islandNowPlaying": false, "islandBattery": false, "islandVolume": false}`
	if err := os.WriteFile(filepath.Join(dir, FileName), []byte(legacy), 0o600); err != nil {
		t.Fatal(err)
	}
	s, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if s.IslandNowPlaying {
		t.Fatal("islandNowPlaying: false was ignored")
	}
	if !s.AutoCheckUpdates {
		t.Fatalf("unrelated keys must keep their defaults: %+v", s)
	}
}

// autoCheckUpdates defaults on so updates reach users; a file without the
// key (older versions, hand edits) must not silently disable it, and an
// explicit false is honoured.
func TestAutoCheckUpdatesLoad(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, FileName), []byte(`{}`), 0o600); err != nil {
		t.Fatal(err)
	}
	s, err := Load(dir)
	if err != nil || !s.AutoCheckUpdates {
		t.Fatalf("missing autoCheckUpdates = %v, %v; want true", s.AutoCheckUpdates, err)
	}
	if err := os.WriteFile(filepath.Join(dir, FileName), []byte(`{"autoCheckUpdates": false}`), 0o600); err != nil {
		t.Fatal(err)
	}
	s, err = Load(dir)
	if err != nil || s.AutoCheckUpdates {
		t.Fatalf("autoCheckUpdates: false ignored: %v, %v", s.AutoCheckUpdates, err)
	}
}

// aiUsage is a nested object: a file without the key (every install before
// the AI 用量 page) must load the page's defaults — the feature off, every
// source on — never Go's zero value. Partial objects keep defaults for
// their missing keys too, and an explicit enabled is honoured.
func TestAIUsageLoad(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, FileName), []byte(`{}`), 0o600); err != nil {
		t.Fatal(err)
	}
	s, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if s.AIUsage.Enabled {
		t.Fatal("missing aiUsage turned the page on")
	}
	for name, on := range map[string]bool{
		"quotaCodex": s.AIUsage.QuotaCodex, "quotaClaude": s.AIUsage.QuotaClaude,
		"localOmp": s.AIUsage.LocalOmp, "localClaude": s.AIUsage.LocalClaude, "localCodex": s.AIUsage.LocalCodex,
	} {
		if !on {
			t.Fatalf("missing aiUsage.%s loaded as off", name)
		}
	}
	// A partial object fills only the keys it sets.
	if err := os.WriteFile(filepath.Join(dir, FileName), []byte(`{"aiUsage": {"enabled": true, "localCodex": false}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	s, err = Load(dir)
	if err != nil || !s.AIUsage.Enabled || s.AIUsage.LocalCodex || !s.AIUsage.QuotaCodex || !s.AIUsage.LocalOmp {
		t.Fatalf("partial aiUsage = %+v, %v; want enabled, localCodex off, the rest on", s.AIUsage, err)
	}
}

func TestSaveRoundTrips(t *testing.T) {
	dir := t.TempDir()
	want := Settings{RetentionDays: 7, ClipboardLimit: 12, ExcludedApps: []string{"com.example.one", "com.example.two"},
		ShellStyle: ShellStyleIsland, PanelAppearance: PanelAppearanceGlass}
	if err := Save(dir, want); err != nil {
		t.Fatal(err)
	}
	got, err := Load(dir)
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("Load after Save = %+v, %v; want %+v", got, err, want)
	}
}

// A stored clipboardLimit below 1 means "use the default", so the file
// can never disable or blow up the history cap by hand-editing.
func TestNonPositiveClipboardLimitLoadsAsDefault(t *testing.T) {
	for _, jsonLimit := range []string{"0", "-5"} {
		dir := t.TempDir()
		data := []byte(`{"clipboardLimit": ` + jsonLimit + `}`)
		if err := os.WriteFile(filepath.Join(dir, FileName), data, 0o600); err != nil {
			t.Fatal(err)
		}
		s, err := Load(dir)
		if err != nil {
			t.Fatal(err)
		}
		if s.ClipboardLimit != 30 {
			t.Fatalf("clipboardLimit %s loaded as %d, want 30", jsonLimit, s.ClipboardLimit)
		}
	}
}

func TestInvalidFileIsNeverRewritten(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, FileName)
	bad := []byte(`{"clipboardLimit": invalid`)
	if err := os.WriteFile(path, bad, 0o600); err != nil {
		t.Fatal(err)
	}
	s, err := Load(dir)
	if err == nil {
		t.Fatal("invalid JSON must be reported")
	}
	if !reflect.DeepEqual(s, Default()) {
		t.Fatalf("invalid file gave %+v, want defaults", s)
	}
	got, _ := os.ReadFile(path)
	if string(got) != string(bad) {
		t.Fatalf("invalid settings.json was overwritten: %q", got)
	}
}

// shellStyle is new in this version: a file without it keeps the hardware
// notch shape, and only the exact "island" value switches the style —
// hand-edited junk must not choose something the app cannot draw.
func TestShellStyleLoad(t *testing.T) {
	for _, tt := range []struct {
		raw  string
		want ShellStyle
	}{
		{`{}`, ShellStyleNotch},
		{`{"shellStyle": "island"}`, ShellStyleIsland},
		{`{"shellStyle": "notch"}`, ShellStyleNotch},
		{`{"shellStyle": "ISLAND"}`, ShellStyleNotch},
		{`{"shellStyle": ""}`, ShellStyleNotch},
	} {
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, FileName), []byte(tt.raw), 0o600); err != nil {
			t.Fatal(err)
		}
		s, err := Load(dir)
		if err != nil {
			t.Fatalf("%s: %v", tt.raw, err)
		}
		if s.ShellStyle != tt.want {
			t.Fatalf("%s loaded shellStyle %q, want %q", tt.raw, s.ShellStyle, tt.want)
		}
	}
}

// LanguageOf reads the language without touching the directory: the startup
// path that cannot open its data directory still names the user's language in
// its refusal, and must not create settings.json, rewrite a broken one, or
// report a language from a file it could not parse.
func TestLanguageOf(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, FileName)
	if lang, ok := LanguageOf(dir); ok || lang != "" {
		t.Fatalf("a missing settings.json answered %q, %v", lang, ok)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("LanguageOf created settings.json: %v", err)
	}

	s := Default()
	s.Language = "zh-Hans"
	if err := Save(dir, s); err != nil {
		t.Fatal(err)
	}
	if lang, ok := LanguageOf(dir); !ok || lang != "zh-Hans" {
		t.Fatalf("LanguageOf = %q, %v, want zh-Hans, true", lang, ok)
	}
	// Follow System is a language the file names: "" with ok true.
	if err := Save(dir, Default()); err != nil {
		t.Fatal(err)
	}
	if lang, ok := LanguageOf(dir); !ok || lang != "" {
		t.Fatalf("LanguageOf = %q, %v, want the empty language, true", lang, ok)
	}

	bad := []byte(`{"language": `)
	if err := os.WriteFile(path, bad, 0o600); err != nil {
		t.Fatal(err)
	}
	if lang, ok := LanguageOf(dir); ok || lang != "" {
		t.Fatalf("a broken settings.json answered %q, %v", lang, ok)
	}
	if got, err := os.ReadFile(path); err != nil || string(got) != string(bad) {
		t.Fatalf("LanguageOf rewrote the broken file: %q, %v", got, err)
	}
}

// The shortcuts are preferences: they round-trip, and a file from an older
// version without them keeps every default.
func TestShortcutSettingsLoad(t *testing.T) {
	dir := t.TempDir()
	prefs := Default()
	prefs.Shortcuts.Toggle = "Super+Shift+K"
	prefs.Shortcuts.Tray = ""
	if err := Save(dir, prefs); err != nil {
		t.Fatal(err)
	}
	loaded, err := Load(dir)
	if err != nil || loaded.Shortcuts != prefs.Shortcuts {
		t.Fatalf("shortcuts did not round-trip: %+v, %v", loaded.Shortcuts, err)
	}
	if err := os.WriteFile(filepath.Join(dir, FileName), []byte(`{"language": ""}`), 0o600); err != nil {
		t.Fatal(err)
	}
	loaded, err = Load(dir)
	if err != nil || loaded.Shortcuts != Default().Shortcuts {
		t.Fatalf("missing shortcuts did not default: %+v, %v", loaded.Shortcuts, err)
	}
}
