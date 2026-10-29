package main

import (
	"testing"

	"shelf/internal/settings"
)

// The shortcut hints are rendered from the accelerators settings.json holds,
// so a hint can never disagree with the shortcut the app registers.
func TestShortcutLabelGlyphs(t *testing.T) {
	cases := map[string]string{
		"Ctrl+Alt+Space":        "⌃⌥Space",
		"Ctrl+Alt+S":            "⌃⌥S",
		"Cmd+Shift+Z":           "⌘⇧Z",
		"CmdOrCtrl+Shift+Space": "⌘⇧Space",
		"Super+Alt+2":           "⌘⌥2",
		"Shift+F5":              "⇧F5",
		// 未知写法原样显示，不虚构按键。
		"Bogus+Key": "Bogus+Key",
		"":          "",
	}
	for in, want := range cases {
		if got := shortcutLabel(in); got != want {
			t.Fatalf("shortcutLabel(%q) = %q, want %q", in, got, want)
		}
	}
}

// A fresh install's three shortcuts still render, and the fourth is unset.
func TestDefaultShortcutsRender(t *testing.T) {
	def := settings.Default().Shortcuts
	for acc, want := range map[string]string{
		def.Toggle:    "⌃⌥Space",
		def.Clipboard: "⌃⌥V",
		def.Capture:   "⌃⌥O",
	} {
		if got := shortcutLabel(acc); got != want {
			t.Fatalf("shortcutLabel(%q) = %q, want %q", acc, got, want)
		}
	}
	if def.Tray != "" {
		t.Fatalf("the tray shortcut must default to unset, got %q", def.Tray)
	}
}
