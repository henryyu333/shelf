//go:build darwin

package main

// The brand otter, as static art. The pictures live outside the repository
// (resources/otter/, ignored by git: the brand artwork is local only), so a
// build from a public checkout — or any build with the folder missing —
// decodes nothing. Every caller falls back: no otter, or the existing glyph
// it used before. A missing picture is never an error.
//
// Only these eight names exist. The otter never blinks, floats or squashes:
// a new state is a different picture, nothing more.

import (
	"os"
	"path/filepath"
	"sync"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

// otterStates are the accepted picture names.
var otterStates = map[string]bool{
	"neutral": true, "happy": true, "surprised": true, "sleepy": true,
	"curious": true, "embarrassed": true, "front": true, "head": true,
}

var (
	otterMu     sync.Mutex
	otterCache  = map[string]*ui.Bitmap{}
	otterMisses = map[string]bool{}
)

// otterDir is where the pictures are found; tests point it at a temp folder.
var otterDir = func() string {
	if dir, err := mygo.App.Path(mygo.PathResources); err == nil {
		return filepath.Join(dir, "otter")
	}
	return ""
}

// otterImage returns the named picture, decoding it once. nil when the name
// is not one of the eight, or when the file cannot be read or decoded.
func otterImage(name string) *ui.Bitmap {
	if !otterStates[name] {
		return nil
	}
	otterMu.Lock()
	defer otterMu.Unlock()
	if b, ok := otterCache[name]; ok {
		return b
	}
	if otterMisses[name] {
		return nil
	}
	dir := otterDir()
	if dir == "" {
		otterMisses[name] = true
		return nil
	}
	data, err := os.ReadFile(filepath.Join(dir, "shelfu-otter_ui_"+name+"_v001.png"))
	if err != nil {
		otterMisses[name] = true
		return nil
	}
	b, err := ui.DecodeBitmap(data)
	if err != nil {
		otterMisses[name] = true
		return nil
	}
	otterCache[name] = b
	return b
}

// resetOtters drops the decoded pictures and the miss marks: a test that
// puts resources in place after an earlier miss calls it.
func resetOtters() {
	otterMu.Lock()
	defer otterMu.Unlock()
	otterCache = map[string]*ui.Bitmap{}
	otterMisses = map[string]bool{}
}
