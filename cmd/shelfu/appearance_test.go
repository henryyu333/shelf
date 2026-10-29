//go:build darwin

package main

import (
	"testing"

	"github.com/egoist/mygo"

	"shelf/internal/settings"
)

// TestGlassNowPanelAppearance: the setting picks the expanded panel's
// paint. Follow System is black in Dark Mode and glass in Light Mode,
// Black is always black, Glass is glass even in Dark Mode. The island
// style and Reduce Transparency are black whatever the setting says, and
// a closed panel is never glass.
func TestGlassNowPanelAppearance(t *testing.T) {
	cases := []struct {
		name       string
		appearance settings.PanelAppearance
		dark       bool
		reduce     bool
		island     bool
		visible    bool
		want       bool
	}{
		{"follow-light", settings.PanelAppearanceFollow, false, false, false, true, true},
		{"follow-dark", settings.PanelAppearanceFollow, true, false, false, true, false},
		{"follow-reduce", settings.PanelAppearanceFollow, false, true, false, true, false},
		{"black-light", settings.PanelAppearanceBlack, false, false, false, true, false},
		{"black-dark", settings.PanelAppearanceBlack, true, false, false, true, false},
		{"glass-light", settings.PanelAppearanceGlass, false, false, false, true, true},
		{"glass-dark", settings.PanelAppearanceGlass, true, false, false, true, true},
		{"glass-reduce", settings.PanelAppearanceGlass, false, true, false, true, false},
		{"glass-island", settings.PanelAppearanceGlass, false, false, true, true, false},
		{"glass-closed", settings.PanelAppearanceGlass, false, false, false, false, false},
		{"unknown-light", settings.PanelAppearance("sepia"), false, false, false, true, true},
		{"unknown-dark", settings.PanelAppearance("sepia"), true, false, false, true, false},
	}
	for _, tc := range cases {
		prefs := settings.Default()
		prefs.PanelAppearance = tc.appearance
		a := &shelf{
			win: &mygo.Window{}, settings: prefs,
			visible: tc.visible, island: tc.island,
			systemDark: tc.dark, reduceTrans: tc.reduce,
		}
		if got := a.glassNow(); got != tc.want {
			t.Errorf("%s: glassNow = %v, want %v", tc.name, got, tc.want)
		}
	}
}

// TestSetPanelAppearanceSaves: the choice lands in settings.json and takes
// effect in memory at once, without a restart.
func TestSetPanelAppearanceSaves(t *testing.T) {
	dir := t.TempDir()
	a := &shelf{dataDir: dir, settings: settings.Default()}
	// The open panel's material is placed, hidden or shown in the same
	// change, not on the next size change.
	syncs := 0
	onSyncMaterial = func() { syncs++ }
	t.Cleanup(func() { onSyncMaterial = nil })
	a.setPanelAppearance(settings.PanelAppearanceGlass)
	if syncs != 1 {
		t.Fatalf("setPanelAppearance synced the material %d times, want 1", syncs)
	}
	if a.settings.PanelAppearance != settings.PanelAppearanceGlass {
		t.Fatalf("in-memory appearance = %q", a.settings.PanelAppearance)
	}
	got, err := settings.Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got.PanelAppearance != settings.PanelAppearanceGlass {
		t.Fatalf("saved appearance = %q, want glass", got.PanelAppearance)
	}
	// Choosing the value already saved is not a change.
	a.setPanelAppearance(settings.PanelAppearanceGlass)
	if syncs != 1 {
		t.Fatalf("an unchanged setting resynced the material: %d", syncs)
	}
}

// TestPanelAppearanceSegments: the segmented control's three segments and
// the saved values agree both ways.
func TestPanelAppearanceSegments(t *testing.T) {
	values := []settings.PanelAppearance{
		settings.PanelAppearanceFollow, settings.PanelAppearanceBlack, settings.PanelAppearanceGlass,
	}
	for idx, want := range values {
		if got := panelAppearanceIndex(want); got != idx {
			t.Errorf("index(%q) = %d, want %d", want, got, idx)
		}
		if got := panelAppearanceAt(idx); got != want {
			t.Errorf("appearance(%d) = %q, want %q", idx, got, want)
		}
	}
}
