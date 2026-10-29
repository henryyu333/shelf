package layout

import (
	"encoding/json"
	"io"
	"log"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func cardIDs(l Layout) []string {
	out := make([]string, len(l.Cards))
	for i, c := range l.Cards {
		out[i] = c.ID
	}
	return out
}

func pinIDs(l Layout) []string {
	out := make([]string, len(l.Pins))
	for i, p := range l.Pins {
		out[i] = p.ID
	}
	return out
}

// writeFile stores v as JSON in dir/layout.json and returns the bytes.
func writeFile(t *testing.T, dir string, v any) []byte {
	t.Helper()
	data, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, FileName), data, 0o600); err != nil {
		t.Fatal(err)
	}
	return data
}

func readFile(t *testing.T, dir string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dir, FileName))
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func TestDefault(t *testing.T) {
	l := Default()
	if l.Version != 2 || Version != 2 {
		t.Fatalf("version = %d, want 2", l.Version)
	}
	// The widgets come first (the music player is 2 × 2), the action cards
	// after them.
	wantCards := []string{"playing", "awake", "usage", "recent", "clip",
		"ocr", "fit1m", "topdf", "zip", "tojpeg", "toword", "extract", "resize"}
	if !slices.Equal(cardIDs(l), wantCards) {
		t.Fatalf("cards = %v, want %v", cardIDs(l), wantCards)
	}
	for _, c := range l.Cards {
		switch c.ID {
		case "playing":
			if c.Cols() != 2 || c.Height() != 2 {
				t.Fatalf("playing = %dx%d, want 2x2", c.Cols(), c.Height())
			}
		case "recent", "clip":
			if c.Cols() != 2 || c.Height() != 1 {
				t.Fatalf("%s = %dx%d, want 2x1", c.ID, c.Cols(), c.Height())
			}
		default:
			if c.Cols() != 1 || c.Height() != 1 {
				t.Fatalf("%s = %dx%d, want 1x1", c.ID, c.Cols(), c.Height())
			}
		}
	}
	if len(l.Home) != 0 {
		t.Fatalf("the default still carries a home canvas: %+v", l.Home)
	}
	if len(l.Pins) != 0 {
		t.Fatalf("default has pins: %+v", l.Pins)
	}
}

func TestDefaultWith(t *testing.T) {
	withAI := DefaultWith(true)
	if !slices.Equal(cardIDs(withAI), cardIDs(Default())) {
		t.Fatalf("DefaultWith(true) differs from Default: %v", cardIDs(withAI))
	}
	noAI := DefaultWith(false)
	if noAI.Version != Version {
		t.Fatalf("version = %d", noAI.Version)
	}
	// The usage widget keeps its entry whatever the setting: it takes no
	// cell while the feature is off, and returns with it.
	if !slices.Equal(cardIDs(noAI), cardIDs(Default())) {
		t.Fatalf("cards = %v", cardIDs(noAI))
	}
}

func TestLoadCreatesDefault(t *testing.T) {
	dir := t.TempDir()
	l, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(cardIDs(l), cardIDs(Default())) {
		t.Fatalf("cards = %v, want default %v", cardIDs(l), cardIDs(Default()))
	}
	if len(l.Home) != 0 {
		t.Fatalf("home = %+v, want none", l.Home)
	}
	if _, err := os.Stat(filepath.Join(dir, FileName)); err != nil {
		t.Fatalf("layout.json was not written: %v", err)
	}
}

func TestLoadWithCreatesAvailabilityDefault(t *testing.T) {
	for _, aiUsage := range []bool{true, false} {
		dir := t.TempDir()
		l, err := LoadWith(dir, aiUsage)
		if err != nil {
			t.Fatalf("aiUsage %v: %v", aiUsage, err)
		}
		if !slices.Equal(cardIDs(l), cardIDs(DefaultWith(aiUsage))) {
			t.Fatalf("aiUsage %v: cards = %v", aiUsage, cardIDs(l))
		}
		var onDisk Layout
		if err := json.Unmarshal(readFile(t, dir), &onDisk); err != nil {
			t.Fatal(err)
		}
		if !slices.Equal(cardIDs(onDisk), cardIDs(DefaultWith(aiUsage))) {
			t.Fatalf("aiUsage %v: file holds %v", aiUsage, cardIDs(onDisk))
		}
	}
}

func TestLoadWithBrokenFileFallsBack(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, FileName), []byte("{nope"), 0o600); err != nil {
		t.Fatal(err)
	}
	l, err := LoadWith(dir, false)
	if err == nil {
		t.Fatal("expected an error for corrupt JSON")
	}
	if !slices.Equal(cardIDs(l), cardIDs(DefaultWith(false))) {
		t.Fatalf("cards = %v", cardIDs(l))
	}
}

func TestWriteThenLoadRoundTrips(t *testing.T) {
	dir := t.TempDir()
	l := Default()
	l.MoveCard("resize", 0)
	l.SetSpan("ocr", 2)
	l.AddPin(Pin{ID: "clip", Span: 2, Edge: EdgeRight, X: 4, Y: 120, AlwaysOpen: true})
	if err := Write(dir, l); err != nil {
		t.Fatal(err)
	}
	got, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(cardIDs(got), cardIDs(l)) {
		t.Fatalf("cards = %v, want %v", cardIDs(got), cardIDs(l))
	}
	if got.Cards[slices.Index(cardIDs(got), "ocr")].Span != 2 {
		t.Fatal("ocr span did not persist")
	}
	if len(got.Pins) != 1 || got.Pins[0] != l.Pins[0] {
		t.Fatalf("pins = %+v", got.Pins)
	}
}

func TestLoadBadJSONKeepsFileAndDefaults(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, FileName)
	if err := os.WriteFile(p, []byte("{nope"), 0o600); err != nil {
		t.Fatal(err)
	}
	l, err := Load(dir)
	if err == nil {
		t.Fatal("expected an error for corrupt JSON")
	}
	if !slices.Equal(cardIDs(l), cardIDs(Default())) {
		t.Fatalf("got %+v, want defaults", l)
	}
	if data, _ := os.ReadFile(p); string(data) != "{nope" {
		t.Fatal("corrupt file was overwritten")
	}
}

func TestLoadFutureVersionDefaultsAndKeepsFile(t *testing.T) {
	dir := t.TempDir()
	raw := writeFile(t, dir, map[string]any{
		"version": 3,
		"cards":   []Card{{ID: "zip"}},
		"home":    []Widget{{ID: "playing", X: 0, Y: 0, W: 2, H: 2}},
	})
	l, err := Load(dir)
	if err == nil {
		t.Fatal("expected a version error")
	}
	if !slices.Equal(cardIDs(l), cardIDs(Default())) {
		t.Fatalf("got %+v, want defaults", l)
	}
	if got := readFile(t, dir); string(got) != string(raw) {
		t.Fatalf("future-version file changed: %s", got)
	}
	noAI, err := LoadWith(dir, false)
	if err == nil || !slices.Equal(cardIDs(noAI), cardIDs(DefaultWith(false))) {
		t.Fatalf("LoadWith future version: cards = %v, err = %v", cardIDs(noAI), err)
	}
}

// A file written before version stamps existed (version 0) is migrated
// like a version 1 file: known widgets keep their old rectangles as grid
// sizes, actions stay in the grid, and Load does not rewrite the file.
func TestLoadUnversionedFileMigrates(t *testing.T) {
	dir := t.TempDir()
	raw := writeFile(t, dir, map[string]any{
		"cards": []Card{{ID: "zip", Span: 7}, {ID: "clip"}},
	})
	l, err := Load(dir)
	if err != nil {
		t.Fatalf("unversioned file rejected: %v", err)
	}
	if !slices.Equal(cardIDs(l), []string{"clip", "zip"}) || l.Cards[1].Span != 1 {
		t.Fatalf("unversioned cards not migrated: %+v", l.Cards)
	}
	if c := l.Cards[0]; c.Cols() != 2 || c.Height() != 1 {
		t.Fatalf("clip = %dx%d, want 2x1", c.Cols(), c.Height())
	}
	if l.Version != Version {
		t.Fatalf("version = %d, want %d", l.Version, Version)
	}
	if got := readFile(t, dir); string(got) != string(raw) {
		t.Fatal("Load rewrote the migrated file")
	}
}

func TestMigrateV1ActionsWidgetsPins(t *testing.T) {
	dir := t.TempDir()
	pins := []Pin{{ID: "fit1m", Span: 2, Edge: EdgeLeft, X: 12, Y: 40, AlwaysOpen: true}}
	raw := writeFile(t, dir, Layout{
		Version: 1,
		Cards: []Card{
			{ID: "ocr", Span: 1},
			{ID: "playing", Span: 2},
			{ID: "zip", Span: 0},
			{ID: "awake", Span: 1},
			{ID: "tojpeg", Span: 1},
			{ID: "clip", Span: 2},
		},
		Pins: pins,
	})
	l, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if l.Version != 2 {
		t.Fatalf("version = %d, want 2", l.Version)
	}
	if got, want := cardIDs(l), []string{"playing", "awake", "clip", "ocr", "zip", "tojpeg"}; !slices.Equal(got, want) {
		t.Fatalf("cards = %v, want %v", got, want)
	}
	sizes := map[string][2]int{"playing": {2, 2}, "awake": {1, 1}, "clip": {2, 1},
		"ocr": {1, 1}, "zip": {1, 1}, "tojpeg": {1, 1}}
	for _, c := range l.Cards {
		if got, want := [2]int{c.Cols(), c.Height()}, sizes[c.ID]; got != want {
			t.Fatalf("card %s = %v, want %v", c.ID, got, want)
		}
	}
	if len(l.Home) != 0 {
		t.Fatalf("home = %+v, want none", l.Home)
	}
	if !slices.Equal(l.Pins, pins) {
		t.Fatalf("pins = %+v, want %+v", l.Pins, pins)
	}
	if got := readFile(t, dir); string(got) != string(raw) {
		t.Fatal("Load rewrote the migrated file")
	}
}

// A version 2 file written before the tools page took the widgets still
// carries them under "home": they join the grid in reading order, ahead of
// the action cards, with the size their rectangle had.
func TestLegacyHomeMergesIntoTheGrid(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, Layout{
		Version: Version,
		Cards:   []Card{{ID: "ocr"}, {ID: "zip"}},
		Home: []Widget{
			{ID: "recent", X: 2, Y: 1, W: 2, H: 1},
			{ID: "playing", X: 0, Y: 0, W: 2, H: 2},
			{ID: "usage", X: 3, Y: 0, W: 1, H: 1},
		},
	})
	l, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := cardIDs(l), []string{"playing", "usage", "recent", "ocr", "zip"}; !slices.Equal(got, want) {
		t.Fatalf("cards = %v, want %v", got, want)
	}
	if c := l.Cards[0]; c.Cols() != 2 || c.Height() != 2 {
		t.Fatalf("playing = %dx%d", c.Cols(), c.Height())
	}
	if len(l.Home) != 0 {
		t.Fatalf("the merged home list survived: %+v", l.Home)
	}
}

func TestMigrateV1KeepsUnknownAndActionCards(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, Layout{Version: 1, Cards: []Card{
		{ID: "mystery", Span: 2},
		{ID: "playing", Span: 2},
		{ID: "ocr", Span: 1},
		{ID: "second-unknown", Span: 5},
	}})
	l, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := cardIDs(l), []string{"playing", "mystery", "ocr", "second-unknown"}; !slices.Equal(got, want) {
		t.Fatalf("cards = %v, want %v", got, want)
	}
	if l.Cards[0].Cols() != 2 || l.Cards[0].Height() != 2 {
		t.Fatalf("playing = %dx%d, want its 2x2 rectangle", l.Cards[0].Cols(), l.Cards[0].Height())
	}
	if l.Cards[1].Span != 2 || l.Cards[2].Span != 1 || l.Cards[3].Span != 1 {
		t.Fatalf("spans = %+v, want 2, 2, 1, 1 (5 clamps to 1)", l.Cards)
	}
}

func TestNormalizeLegacyHomeKeepsIds(t *testing.T) {
	l := Layout{
		Version: Version,
		Home: []Widget{
			{ID: "a", X: 0, Y: 0, W: 3, H: 0}, // sizes clamp to 2 and 1
			{ID: "", X: 2, Y: 0, W: 1, H: 1},  // empty id: dropped
			{ID: "a", X: 2, Y: 1, W: 2, H: 1}, // duplicate id: dropped
		},
	}
	l.normalize()
	want := []Widget{{ID: "a", X: 0, Y: 0, W: 2, H: 1}}
	if !slices.Equal(l.Home, want) {
		t.Fatalf("home = %+v, want %+v", l.Home, want)
	}
}

func TestNormalizeOnePlaceAcrossHomeCardsPins(t *testing.T) {
	l := Layout{
		Version: Version,
		Home:    []Widget{{ID: "awake", X: 0, Y: 0, W: 1, H: 1}},
		Cards:   []Card{{ID: "awake"}, {ID: "zip", Span: 0}, {ID: "zip", Span: 2}},
		Pins:    []Pin{{ID: "zip"}, {ID: "clip", Edge: "diagonal"}, {ID: "awake"}},
	}
	l.normalize()
	if got, want := cardIDs(l), []string{"zip"}; !slices.Equal(got, want) {
		t.Fatalf("cards = %v, want %v", got, want)
	}
	if l.Cards[0].Span != 1 {
		t.Fatalf("zip span = %d, want clamped 1", l.Cards[0].Span)
	}
	if !slices.Equal(pinIDs(l), []string{"clip"}) {
		t.Fatalf("pins = %v, want clip only", pinIDs(l))
	}
	if l.Pins[0].Edge != "" {
		t.Fatalf("unknown edge kept: %q", l.Pins[0].Edge)
	}
}

func TestNormalizeDedupesCardsAndPins(t *testing.T) {
	l := Layout{
		Version: Version,
		Cards:   []Card{{ID: "zip"}, {ID: "zip", Span: 2}, {ID: "fit1m"}},
		Pins:    []Pin{{ID: "fit1m"}, {ID: "topdf", Edge: "diagonal"}},
	}
	l.normalize()
	if !slices.Equal(cardIDs(l), []string{"zip", "fit1m"}) {
		t.Fatalf("cards = %v", cardIDs(l))
	}
	if l.Cards[0].Span != 1 {
		t.Fatal("first duplicate should win")
	}
	if !slices.Equal(pinIDs(l), []string{"topdf"}) {
		t.Fatalf("pins = %v: fit1m's pin should have dropped (grid wins)", pinIDs(l))
	}
	if l.Pins[0].Edge != "" {
		t.Fatalf("unknown edge kept: %q", l.Pins[0].Edge)
	}
}

func TestNormalizeClampsSpans(t *testing.T) {
	l := Layout{Version: Version, Cards: []Card{{ID: "awake", Span: 0}, {ID: "zip", Span: 3}}}
	l.normalize()
	for _, c := range l.Cards {
		if c.Span != 1 {
			t.Fatalf("span %d was not clamped", c.Span)
		}
	}
}

func TestCardLookups(t *testing.T) {
	l := Default()
	if i := l.index("playing"); i != 0 {
		t.Fatalf("index(playing) = %d, want 0", i)
	}
	if i := l.index("clip"); i != 4 {
		t.Fatalf("index(clip) = %d, want 4", i)
	}
	if l.index("missing") != -1 {
		t.Fatal("index found a card that is not there")
	}
	if !l.hasCard("playing") || !l.hasCard("ocr") {
		t.Fatal("hasCard missed a placed card")
	}
	l.AddPin(Pin{ID: "topdf", Span: 1})
	if !l.hasCard("topdf") {
		t.Fatal("hasCard missed a pinned card")
	}
	if l.hasCard("missing") || l.hasCard("") {
		t.Fatal("hasCard found a card that is not there")
	}
}

func TestAddCardSizeRefusals(t *testing.T) {
	l := Default()
	cardsBefore := slices.Clone(l.Cards)
	pinsBefore := slices.Clone(l.Pins)

	// An id placed anywhere is not placed twice, and sizes stay 1..2.
	l.AddCardSize("playing", 1, 1) // already in the grid
	l.AddCardSize("", 1, 1)        // empty id
	if !slices.Equal(l.Cards, cardsBefore) {
		t.Fatalf("a refused AddCardSize mutated the grid: %+v", l.Cards)
	}
	l.AddCardSize("mystery", 9, 9)
	c, ok := l.card("mystery")
	if !ok {
		t.Fatal("AddCardSize refused a new card")
	}
	if c.Cols() != 1 || c.Height() != 2 {
		t.Fatalf("out-of-range size kept %dx%d, want 1x2 (both clamp)", c.Cols(), c.Height())
	}
	if !slices.Equal(l.Pins, pinsBefore) {
		t.Fatal("AddCardSize touched the pins")
	}
}

func TestSetCardSize(t *testing.T) {
	l := Default()
	l.SetCardSize("playing", 2, 1)
	if c, _ := l.card("playing"); c.Cols() != 2 || c.Height() != 1 {
		t.Fatalf("playing = %dx%d", c.Cols(), c.Height())
	}
	l.SetCardSize("playing", 3, 1) // out of range: ignored
	l.SetCardSize("playing", 1, 0) // zero rows: ignored
	if c, _ := l.card("playing"); c.Cols() != 2 || c.Height() != 1 {
		t.Fatalf("an out-of-range size applied: %+v", c)
	}
	l.SetCardSize("missing", 1, 1) // absent: no panic, nothing added
	if _, ok := l.card("missing"); ok {
		t.Fatal("SetCardSize added a card")
	}
}

func TestAddCardKeepsOnePlace(t *testing.T) {
	l := Default()
	before := len(l.Cards)
	l.AddCard("ocr", 2) // already placed: no-op
	if got := cardIDs(l); len(got) != before {
		t.Fatalf("duplicated card added: %v", got)
	}
	l.AddCard("mystery", 2)
	if got := cardIDs(l); got[len(got)-1] != "mystery" || l.Cards[len(l.Cards)-1].Span != 2 {
		t.Fatalf("mystery not appended with span 2: %+v", l.Cards)
	}
	l.AddPin(Pin{ID: "widgetx"})
	l.AddCard("widgetx", 2) // pinned already: still no-op
	if slices.Contains(cardIDs(l), "widgetx") {
		t.Fatalf("pinned card joined the grid: %v", cardIDs(l))
	}
	n := len(l.Cards)
	l.AddCard("playing", 1) // already in the grid: no-op
	if len(l.Cards) != n {
		t.Fatalf("duplicated card added: %v", cardIDs(l))
	}
}

func TestMoveCard(t *testing.T) {
	l := Default()
	l.MoveCard("zip", 0)
	if got := cardIDs(l); got[0] != "zip" {
		t.Fatalf("zip did not move first: %v", got)
	}
	before := cardIDs(l)
	l.MoveCard("zip", 0) // moving before itself is a no-op
	if !slices.Equal(cardIDs(l), before) {
		t.Fatalf("self-move changed order: %v", cardIDs(l))
	}
	l.MoveCard("zip", len(l.Cards))
	if got := cardIDs(l); got[len(got)-1] != "zip" {
		t.Fatalf("zip did not move last: %v", got)
	}
	l.MoveCard("missing", 0) // absent: no-op, no panic
}

func TestSetSpan(t *testing.T) {
	l := Default()
	l.SetSpan("ocr", 2)
	if l.Cards[l.index("ocr")].Span != 2 {
		t.Fatal("span not set")
	}
	l.SetSpan("ocr", 9) // out of range: ignored
	if l.Cards[l.index("ocr")].Span != 2 {
		t.Fatal("out-of-range span applied")
	}
	l.SetSpan("missing", 2) // absent: no panic
}

func TestAddPinRemovesFromGrid(t *testing.T) {
	l := Default()
	l.AddPin(Pin{ID: "zip", Span: 1, Edge: EdgeLeft})
	if slices.Contains(cardIDs(l), "zip") {
		t.Fatal("pinned card stayed on the grid")
	}
	if !slices.Equal(pinIDs(l), []string{"zip"}) {
		t.Fatalf("pins = %v", pinIDs(l))
	}
	l.AddPin(Pin{ID: "zip", Edge: EdgeRight}) // already pinned: keeps the first
	if len(l.Pins) != 1 || l.Pins[0].Edge != EdgeLeft {
		t.Fatalf("second pin mutated the first: %+v", l.Pins)
	}

	l.AddPin(Pin{ID: "awake", Span: 2, Edge: EdgeRight})
	if !slices.Contains(pinIDs(l), "awake") {
		t.Fatalf("awake not pinned: %v", pinIDs(l))
	}
	if slices.Contains(cardIDs(l), "awake") {
		t.Fatal("a pinned widget stayed in the grid")
	}
}

// A pin keeps the rows its card had: 放回刘海 returns the music player 2 × 2.
func TestRemovePinKeepsRows(t *testing.T) {
	l := Default()
	l.AddPin(Pin{ID: "playing", Span: 2, Rows: 2})
	l.RemovePin("playing", true, 0)
	c, ok := l.card("playing")
	if !ok || c.Cols() != 2 || c.Height() != 2 {
		t.Fatalf("playing returned as %+v, want 2x2", c)
	}
}

func TestRemovePinPutsBack(t *testing.T) {
	l := Default()
	l.AddPin(Pin{ID: "awake", Span: 2, Edge: EdgeRight})
	l.RemovePin("awake", true, 1)
	if len(l.Pins) != 0 {
		t.Fatal("pin was not removed")
	}
	if got := cardIDs(l); got[1] != "awake" {
		t.Fatalf("awake did not return at index 1: %v", got)
	}
	if l.Cards[1].Span != 2 {
		t.Fatal("returned card lost its span")
	}
	// Out-of-range position appends.
	l.AddPin(Pin{ID: "awake", Span: 1})
	l.RemovePin("awake", true, 99)
	if got := cardIDs(l); got[len(got)-1] != "awake" {
		t.Fatalf("awake did not append: %v", got)
	}
	// Without putBack the card is just gone.
	l.AddPin(Pin{ID: "zip"})
	l.RemovePin("zip", false, 0)
	if slices.Contains(cardIDs(l), "zip") || len(l.Pins) != 0 {
		t.Fatal("removed card came back")
	}
}

func TestLoadNormalizesWithoutFailing(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, Layout{Version: Version,
		Cards: []Card{{ID: "zip"}, {ID: "zip"}, {ID: "awake", Span: 7}}})
	l, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(cardIDs(l), []string{"zip", "awake"}) || l.Cards[1].Span != 1 {
		t.Fatalf("normalize did not repair: %+v", l.Cards)
	}
}

func TestGridPersistenceRoundTrip(t *testing.T) {
	dir := t.TempDir()
	l := Default()
	l.RemoveCard("recent")
	l.AddCardSize("recent", 2, 2)
	l.MoveCard("awake", 2)
	l.SetCardSize("playing", 1, 1)
	l.AddPin(Pin{ID: "clip", Span: 2, Rows: 1, Edge: EdgeLeft, X: 10, Y: 60})
	if err := Write(dir, l); err != nil {
		t.Fatal(err)
	}
	got, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(cardIDs(got), cardIDs(l)) {
		t.Fatalf("cards = %v, want %v", cardIDs(got), cardIDs(l))
	}
	for i := range l.Cards {
		if got.Cards[i].ID != l.Cards[i].ID || got.Cards[i].Cols() != l.Cards[i].Cols() ||
			got.Cards[i].Height() != l.Cards[i].Height() {
			t.Fatalf("card %d = %+v, want %+v", i, got.Cards[i], l.Cards[i])
		}
	}
	if len(got.Pins) != 1 || got.Pins[0] != l.Pins[0] {
		t.Fatalf("pins = %+v, want %+v", got.Pins, l.Pins)
	}
}

func TestLoadQuietWith(t *testing.T) {
	prev := log.Writer()
	log.SetOutput(io.Discard)
	t.Cleanup(func() { log.SetOutput(prev) })

	missing := t.TempDir()
	if got := LoadQuietWith(missing, false); !slices.Equal(got.Home, DefaultWith(false).Home) {
		t.Fatalf("missing file home = %+v", got.Home)
	}
	if _, err := os.Stat(filepath.Join(missing, FileName)); err != nil {
		t.Fatalf("LoadQuietWith did not create the file: %v", err)
	}

	broken := t.TempDir()
	if err := os.WriteFile(filepath.Join(broken, FileName), []byte("{nope"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := LoadQuietWith(broken, true); !slices.Equal(cardIDs(got), cardIDs(Default())) {
		t.Fatalf("broken file cards = %v", cardIDs(got))
	}
	if got := LoadQuiet(broken); !slices.Equal(cardIDs(got), cardIDs(Default())) {
		t.Fatalf("LoadQuiet cards = %v", cardIDs(got))
	}
}

func TestWriteIsAtomic(t *testing.T) {
	dir := t.TempDir()
	l := Default()
	if err := Write(dir, l); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), "."+FileName+".tmp-") {
			t.Fatalf("temp file left behind: %s", e.Name())
		}
	}
}

// TestLegacyHomeAndPinsLoad is the compatibility half: a version 2 file
// written before the union (widgets under "home", pins with a canvas rect)
// loads, merges, and is not rewritten.
func TestLegacyHomeAndPinsLoad(t *testing.T) {
	dir := t.TempDir()
	old := `{
  "version": 2,
  "home": [{"id": "playing", "x": 0, "y": 0, "w": 2, "h": 2}],
  "cards": [{"id": "ocr", "span": 1}],
  "pins": [{"id": "recent", "span": 2, "display": "1", "x": 10, "y": 20, "homeW": 2, "homeH": 1}]
}`
	file := filepath.Join(dir, FileName)
	if err := os.WriteFile(file, []byte(old), 0o600); err != nil {
		t.Fatal(err)
	}
	l, err := Load(dir)
	if err != nil {
		t.Fatalf("an old file failed to load: %v", err)
	}
	if got, want := cardIDs(l), []string{"playing", "ocr"}; !slices.Equal(got, want) {
		t.Fatalf("cards = %v, want %v", got, want)
	}
	if c := l.Cards[0]; c.Cols() != 2 || c.Height() != 2 {
		t.Fatalf("playing = %dx%d, want 2x2", c.Cols(), c.Height())
	}
	if len(l.Home) != 0 {
		t.Fatalf("home = %+v, want none", l.Home)
	}
	if len(l.Pins) != 1 || l.Pins[0].ID != "recent" || l.Pins[0].Span != 2 {
		t.Fatalf("pins = %+v", l.Pins)
	}
	data, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != old {
		t.Fatalf("loading an old file rewrote it:\n%s", data)
	}
}

// TestInactiveIsRuntimeOnly: the Inactive seam is never written, so a
// switched-off widget keeps its entry for the next launch.
func TestInactiveIsRuntimeOnly(t *testing.T) {
	dir := t.TempDir()
	l := Default()
	l.Inactive = func(id string) bool { return id == "usage" }
	if err := Write(dir, l); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(readFile(t, dir)), "inactive") {
		t.Fatal("the runtime seam was written to the file")
	}
	got, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := got.card("usage"); !ok {
		t.Fatal("the switched-off widget lost its entry")
	}
	if got.Inactive != nil {
		t.Fatal("a loaded layout carries a seam")
	}
}
