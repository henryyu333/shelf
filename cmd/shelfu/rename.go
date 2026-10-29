//go:build darwin

package main

// Batch rename: the 重新命名 window — Finder's "Rename N Items" for the
// tray's files. The window is a normal titled window like Settings: one
// instance, opened with the app active and the keyboard, and closing
// hands the keyboard back to whoever had it. Logic — name computation,
// conflicts, the safe two-phase move and rollback — lives in renamer.go;
// this file owns the window and its view.

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"shelf/internal/mac"
	"shelf/internal/store"
	"shelf/locales"
)

// renameWidth/renameHeight size the 重新命名 window: a normal titled
// window like Settings, roomy enough for the preview list.
const (
	renameWidth  = 480
	renameHeight = 470
)

// renameable reports whether an item can take part in a rename: only real
// files and folders still on disk. ShelfU's own captures (screenshots,
// clipboard images, text) and its converted outputs in the data directory
// are never renamed — their paths are managed, not the user's.
func (a *shelf) renameable(it store.Item) bool {
	if it.Kind != store.KindFile || it.Missing || it.Converted || it.Path == "" {
		return false
	}
	if a.dataDir != "" && dataDirHolds(a.dataDir, it.Path) {
		return false
	}
	return true
}

// dataDirHolds reports whether p sits inside the data directory. Both the
// path as given and its symlink-resolved spelling are checked: a rename
// must not reach into the data folder through either one.
func dataDirHolds(dir, p string) bool {
	return pathInside(dir, p) || insideDataDir(dir, p)
}

// insideDataDir reports whether p sits inside the data directory;
// symlinked spellings resolve first so a file reached through a link
// still counts as managed.
func insideDataDir(dir, p string) bool {
	rel, err := filepath.Rel(resolvePath(dir), resolvePath(p))
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// resolvePath resolves the symlinks in p. A path that does not exist yet
// (a rename target, an item whose file moved) resolves through its
// directory, so a link in the middle is still followed.
func resolvePath(p string) string {
	p = filepath.Clean(p)
	if q, err := filepath.EvalSymlinks(p); err == nil {
		return q
	}
	dir := filepath.Dir(p)
	if dir == p {
		return p
	}
	// A missing tail resolves through its nearest existing ancestor, so a
	// link in the middle of the path is still followed.
	return filepath.Join(resolvePath(dir), filepath.Base(p))
}

// renameUI is the window's session state, kept on the shelf as a.rn so
// the module can watch it while the window is up. Inputs live here, not
// in closures, so the view rebuild reads the latest text each frame.
type renameUI struct {
	host         *shelf
	win          *mygo.Window
	prevPID      int
	items        []store.Item
	mode         int // segmented index into renameMode
	find         string
	replace      string
	addText      string
	addSeg       int // 0 = 加在名称前, 1 = 名称后
	fmtName      string
	fmtStartText string
	digitsSeg    int // 0–3 → 1–4 位
	posSeg       int // 0 = 序号在名称前, 1 = 名称后
	scroll       ui.ScrollState
	booted       bool        // first frame focused the mode's first field
	done         *renameDone // last applied batch, while undo remains open
	undoUntil    time.Time   // the panel's 撤销 pill shows until then
	err          string      // apply/undo failure shown inline
}

// renameUndoLife is how long the panel's completion pill keeps its 撤销
// button after a rename — and again after the window closes.
const renameUndoLife = 10 * time.Second

// Seams around app-level calls so headless tests can run the window's
// session without a running app.
var (
	frontmostPID   = mac.FrontmostPID
	ownPID         = mac.OwnPID
	renameActivate = mac.ActivateSelf
	renameShow     = func(w *mygo.Window) { w.Show() }
	renameFocus    = func(w *mygo.Window) { w.Focus() }
)

// renameDone remembers one applied batch for undo: the disk moves (current
// name → original name), every tray path the batch changed — items inside
// a renamed folder included — and the paths to put back if an undo's save
// fails.
type renameDone struct {
	ops   []renameOp
	paths map[string]string // item id → original path
}

// params reads the inputs into the planner's form. An unparsable 起始号
// is -1, which startOK rejects before 改名 is enabled.
func (r *renameUI) params() renameParams {
	p := renameParams{
		mode:       renameMode(r.mode),
		find:       r.find,
		replace:    r.replace,
		addText:    r.addText,
		addBefore:  r.addSeg == 0,
		fmtName:    r.fmtName,
		fmtDigits:  r.digitsSeg + 1,
		fmtNumLast: r.posSeg == 1,
	}
	if n, err := strconv.Atoi(r.fmtStartText); err == nil {
		p.fmtStart = n
	} else {
		p.fmtStart = -1
	}
	return p
}

// startOK reports whether 起始号 parses; the input defaults to "1" and
// any non-number shows as an inline hint and disables 改名.
func (r *renameUI) startOK() bool {
	n, err := strconv.Atoi(r.fmtStartText)
	return err == nil && n >= 0
}

// plan is the live preview of the current inputs over the chosen items.
func (r *renameUI) plan() renamePlan {
	return planRenameIn(r.host.appLoc(), r.items, r.params())
}

// blocked reports whether 改名 must stay disabled: a conflicting row, no
// change at all, or an unparsable 起始号. The button and the Enter
// shortcut both consult it, so the keyboard can never start a batch the
// button would refuse.
func (r *renameUI) blocked(pl renamePlan) bool {
	return pl.conflicts > 0 || pl.changed == 0 ||
		(renameMode(r.mode) == modeFormat && !r.startOK())
}

// setErr shows msg inline and repaints.
func (r *renameUI) setErr(msg string) {
	r.err = msg
	r.repaint()
}

// fail shows msg where the user is looking: inline while the window is
// open, and in the panel's error pill when it is closed — the 撤销 pill's
// own case, where an inline message would be invisible.
func (r *renameUI) fail(msg string) {
	r.err = msg
	if r.win != nil {
		r.repaint()
		return
	}
	r.host.toastStatus(msg, time.Now().Add(8*time.Second), statusErr)
}

// undoBlockedName names an original name a batch's undo cannot go back to
// because something else holds it now.
func undoBlockedName(ops []renameOp) string {
	for _, op := range ops {
		st, err := os.Lstat(op.dst)
		if err != nil || sameFile(op.src, st) {
			continue
		}
		return filepath.Base(op.dst)
	}
	return ""
}

// undoErr words a failed undo: a taken original name is named outright,
// everything else keeps the executor's explanation, in the language of l.
func undoErr(l locales.Messages, ops []renameOp, err error) string {
	if name := undoBlockedName(ops); name != "" {
		return l.RenameUndoNameTaken(name)
	}
	return l.RenameUndoFailed(err.Error())
}

// repaint redraws the rename window and the panel (the 撤销 pill lives
// there), skipping whichever is not up.
func (r *renameUI) repaint() {
	if r.win != nil {
		r.win.Invalidate()
	}
	if r.host.win != nil {
		r.host.win.Invalidate()
	}
}

// undoOps returns ops with source and target swapped.
func undoOps(ops []renameOp) []renameOp {
	out := make([]renameOp, len(ops))
	for i, op := range ops {
		out[i] = renameOp{src: op.dst, dst: op.src}
	}
	return out
}

// pathUpdates maps the batch's items — and every tray item inside a
// renamed folder, whose path changes with it — to their new paths, and
// returns the original paths so 撤销 can put both back.
func (r *renameUI) pathUpdates(pl renamePlan) (next, orig map[string]string) {
	next = make(map[string]string, len(pl.ops))
	orig = make(map[string]string, len(pl.ops))
	for _, row := range pl.rows {
		if row.newPath == row.oldPath {
			continue
		}
		next[row.item.ID] = row.newPath
		orig[row.item.ID] = row.oldPath
	}
	for _, it := range r.host.store.Items {
		if it.Path == "" { // text items have no path to follow
			continue
		}
		if _, done := next[it.ID]; done {
			continue
		}
		for _, row := range pl.rows {
			if row.newPath == row.oldPath || !pathInside(row.oldPath, it.Path) {
				continue
			}
			rel, err := filepath.Rel(row.oldPath, it.Path)
			if err != nil {
				continue
			}
			next[it.ID] = filepath.Join(row.newPath, rel)
			orig[it.ID] = it.Path
			break
		}
	}
	return next, orig
}

// apply runs the previewed batch: disk moves first, then the tray paths.
// A disk failure already rolled back inside execRename; a store failure
// rolls the disk moves back so the tray never points at names that do not
// exist. On success the window shows its done state and the panel gets
// the 撤销 pill.
func (r *renameUI) apply() {
	if r.win == nil || r.done != nil {
		return
	}
	pl := r.plan()
	if r.blocked(pl) {
		return
	}
	l := r.host.appLoc()
	if err := execRenameIn(l, pl.ops); err != nil {
		r.setErr(err.Error())
		return
	}
	next, orig := r.pathUpdates(pl)
	if _, err := r.host.store.UpdatePaths(next); err != nil {
		r.setErr(saveErr(l, err, renameRestore(l, pl.ops)))
		return
	}
	for id := range next {
		r.host.invalidateThumbnail(id) // the cached thumb was decoded at the old path
	}
	r.err = ""
	r.done = &renameDone{ops: undoOps(pl.ops), paths: orig}
	r.undoUntil = time.Now().Add(renameUndoLife)
	r.repaint()
}

// renameRestore puts the disk names back after the tray could not record
// the batch, and returns what the failed rollback said in the language of
// l, nil when every name is back.
func renameRestore(l locales.Messages, ops []renameOp) error {
	return execRenameIn(l, undoOps(ops))
}

// saveErr words a failed tray save: a rollback that could not put every
// name back says so, a clean one says the names were restored.
func saveErr(l locales.Messages, err, restore error) string {
	if restore != nil {
		return l.RenameSaveFailedRestore(err.Error(), restore.Error())
	}
	return l.RenameSaveFailedRestored(err.Error())
}

// undo moves the last batch's files back to their original names — never
// overwriting, so an original name taken since fails instead of losing
// the newer file — and restores the tray paths, nested items included.
// Only the latest batch can be undone; a successful undo consumes it.
func (r *renameUI) undo() {
	d := r.done
	if d == nil {
		return
	}
	l := r.host.appLoc()
	if err := execRenameIn(l, d.ops); err != nil {
		r.fail(undoErr(l, d.ops, err))
		return
	}
	if _, err := r.host.store.UpdatePaths(d.paths); err != nil {
		// The names are back but the tray could not record it: rename
		// forward again so tray and disk agree, and keep 撤销 available.
		r.setErr(saveErr(l, err, renameRestore(l, d.ops)))
		return
	}
	for id := range d.paths {
		r.host.invalidateThumbnail(id)
	}
	n := len(d.ops)
	r.done, r.undoUntil, r.err = nil, time.Time{}, ""
	r.host.toastStatus(l.RenameUndone(n), time.Now().Add(4*time.Second), statusOK)
	r.repaint()
}

// openRename shows the 重新命名 window for items, or re-focuses the one
// already open — a single instance like Settings. The shelf releases its
// global Esc/Space while the window is key (the same handover Settings
// uses) and remembers the app to give the keyboard back to on close; that
// memory is taken only when the window is created, so re-opening an
// existing window never loses it.
func (a *shelf) openRename(items []store.Item) {
	if len(items) == 0 || a.store == nil {
		return
	}
	if a.rn == nil {
		a.rn = &renameUI{host: a}
		a.modules = append(a.modules, renameModule{host: a})
	}
	r := a.rn
	r.items = items
	r.mode, r.find, r.replace = int(modeReplace), "", ""
	r.addText, r.addSeg = "", 1
	r.fmtName, r.fmtStartText, r.digitsSeg, r.posSeg = "", "1", 0, 1
	r.done, r.undoUntil, r.err, r.booted = nil, time.Time{}, "", false
	a.releaseKeys()
	if r.win != nil {
		renameActivate()
		renameShow(r.win)
		renameFocus(r.win)
		return
	}
	r.prevPID = 0
	if pid := frontmostPID(); pid > 0 && pid != ownPID() {
		r.prevPID = pid
	}
	frame := centeredFrame(renameWidth, renameHeight,
		mygo.Screen.DisplayNearestPoint(mygo.Screen.CursorScreenPoint()).WorkArea)
	w := mygo.NewWindow(mygo.WindowOptions{
		Title: a.renameTitle(len(items)), Width: renameWidth, Height: renameHeight,
		X: frame.X, Y: frame.Y,
		Hidden: true, BackgroundColor: "#1E1E1F",
		TitleBarStyle:        mygo.TitleBarHidden,
		TrafficLightPosition: &mygo.Point{X: 9, Y: 12},
		DisableResize:        true, DisableMaximize: true, DisableFullScreen: true,
		Content: ui.View(a.renameView),
	})
	mac.SetWindowAppearance(w.NativeHandle(), true)
	w.SetBounds(frame)
	w.OnClosed(a.renameClosed)
	r.win = w
	renameShow(w)
	renameActivate()
	renameFocus(w)
}

// renameTitle names the window and the menu item in the source language;
// renameTitleIn says it in the language of l, and a.renameTitle in the
// language the shelf shows.
func renameTitle(n int) string { return renameTitleIn(locales.In(""), n) }

// renameTitleIn is renameTitle in the language of l.
func renameTitleIn(l locales.Messages, n int) string {
	if n > 1 {
		return l.RenameTitleMany(n)
	}
	return l.RenameTitle()
}

// renameTitle names the 重新命名 window in the language the shelf shows.
func (a *shelf) renameTitle(n int) string { return renameTitleIn(a.appLoc(), n) }

// centeredFrame centers a w×h window inside a display work area, as the
// Settings window centers itself.
func centeredFrame(w, h int, work mygo.Rectangle) mygo.Rectangle {
	x := work.X + (work.Width-w)/2
	y := work.Y + (work.Height-h)/2
	x = min(max(x, work.X), max(work.X, work.X+work.Width-w))
	y = min(max(y, work.Y), max(work.Y, work.Y+work.Height-h))
	return mygo.Rectangle{X: x, Y: y, Width: w, Height: h}
}

// renameModule keeps the shelf's global Esc/Space out of the rename
// window's reach: they are released when the window opens, but a click
// back on the panel would re-grab them, and a held Space never reaches a
// text field. tick puts them down again while the window is up, and
// retires the panel's 撤销 pill when its time is up.
type renameModule struct{ host *shelf }

func (renameModule) live() bool { return false }

func (m renameModule) tick(now time.Time) {
	r := m.host.rn
	if r == nil {
		return
	}
	if r.win != nil {
		m.host.releaseKeys()
	}
	if r.done != nil && !r.undoUntil.IsZero() && now.After(r.undoUntil) {
		r.undoUntil = time.Time{}
		if m.host.win != nil {
			m.host.win.Invalidate()
		}
	}
}

// renameClosed mirrors settingsClosed: the pointer drops first, then the
// keyboard goes back to the panel (clipboard search), the shelf (panel
// open) or the app the user came from — never hiding this app. Closing
// also restarts the 撤销 pill's clock, so it stays reachable for a full
// renameUndoLife after the window goes away.
func (a *shelf) renameClosed() {
	r := a.rn
	if r == nil {
		return
	}
	r.win = nil
	if r.done != nil {
		r.undoUntil = time.Now().Add(renameUndoLife)
	}
	if a.quitting {
		return
	}
	prev := r.prevPID
	r.prevPID = 0
	switch a.settingsKeyboardExit() {
	case settingsToPanel:
		if a.win != nil {
			a.win.Focus()
		}
		return
	case settingsToShelf:
		a.holdKeys()
	}
	a.handBackApp(prev)
}

// renameUndoPill is the completion prompt: 「已重新命名 N 项」 with 撤销 in
// the panel's toast stack, so the undo outlives the window. It stays for
// renameUndoLife and disappears on its own after that.
func (a *shelf) renameUndoPill(c *ui.Context) {
	r := a.rn
	if r == nil || r.done == nil || r.undoUntil.IsZero() || c.Now().After(r.undoUntil) {
		return
	}
	l := loc(c)
	p := ui.Row(c.Key("pill-rename-undo")).Height(30).Padding(0, 8, 0, 14).Radius(15).
		AlignItems(ui.Center).Gap(8).Background(fill2)
	p.Children(func() {
		ui.Text(c, l.RenameDoneCount(len(r.done.ops))).SingleLine().FontSize(11).TextColor(ink)
		if ui.Button(c, l.RenameUndo()).Label(l.RenameUndoLabel()).Clicked() {
			r.undo()
		}
	})
}

// renameView draws the window each frame: the drawn title bar (centered
// like the Settings mock), the three-way segment, that mode's inputs, the
// live preview, and 取消/改名. The done state replaces the form with the
// result and 撤销/完成.
func (a *shelf) renameView(c *ui.Context) {
	c.SetTheme(settingsTheme)
	c.Root().Background(ui.Hex("#1E1E1F"))
	r := a.rn
	if r == nil || r.win == nil {
		return
	}
	c.OnShortcut(0, ui.KeyEscape, r.win.Close)
	c.OnShortcut(ui.Cmd, ui.KeyW, r.win.Close)
	applyOrClose := func() {
		if r.done != nil {
			r.win.Close()
		} else {
			r.apply()
		}
	}
	c.OnShortcut(0, ui.KeyEnter, applyOrClose)
	l := loc(c)
	ui.Column(c.Key("win")).Fill().Children(func() {
		bar := c.TitleBar()
		ui.Row(c.Key("titlebar")).Height(float32(max(bar.Height, 38))).FillWidth().
			AlignItems(ui.Center).Justify(ui.Center).Padding(0, bar.Right, 0, bar.Left).
			DragWindow().Children(func() {
			ui.Text(c, renameTitleIn(l, len(r.items))).SingleLine().FontSize(13).FontWeight(600).TextColor(setInk2)
		})
		if r.done != nil {
			r.doneView(c)
			return
		}
		ui.Column(c.Key("body")).Padding(0, 16, 0, 16).Gap(10).Fill().Children(func() {
			if sg := ui.Segmented(c.Key("mode"), &r.mode,
				l.RenameModeReplace(), l.RenameModeAdd(), l.RenameModeFormat()).
				FillWidth().Label(l.RenameModeLabel()); sg.Changed() {
				r.booted = false
			}
			r.inputSection(c)
			r.previewSection(c)
			if r.err != "" {
				ui.Text(c, r.err).FontSize(11).TextColor(danger).Padding(0, 2, 0, 2)
			}
			r.buttons(c, applyOrClose)
		})
	})
	r.booted = true
}

// inputSection renders the active mode's card: the settings-window row
// look (label left, control right) on a grouped card.
func (r *renameUI) inputSection(c *ui.Context) {
	l := loc(c)
	title := l.RenameModeReplace()
	switch renameMode(r.mode) {
	case modeAdd:
		title = l.RenameModeAdd()
	case modeFormat:
		title = l.RenameModeFormat()
	}
	settingsSection(c, title, "", false, func() {
		switch renameMode(r.mode) {
		case modeReplace:
			settingsRow(c, "rn-find", l.RenameFindRow(), "", func() {
				r.field(c, "rn-find-in", &r.find, l.RenameFindPlaceholder())
			})
			settingsSep(c, "rn-find")
			settingsRow(c, "rn-with", l.RenameWithRow(), "", func() {
				r.field(c, "rn-with-in", &r.replace, l.RenameWithPlaceholder())
			})
		case modeAdd:
			settingsRow(c, "rn-add", l.RenameAddRow(), "", func() {
				r.field(c, "rn-add-in", &r.addText, l.RenameAddPlaceholder())
			})
			settingsSep(c, "rn-add")
			settingsRow(c, "rn-addpos", l.RenamePositionRow(), "", func() {
				ui.Segmented(c, &r.addSeg, l.RenameBefore(), l.RenameAfter()).Label(l.RenameAddPosLabel())
			})
		default: // modeFormat
			settingsRow(c, "rn-name", l.RenameNameRow(), "", func() {
				r.field(c, "rn-name-in", &r.fmtName, l.RenameNamePlaceholder())
			})
			settingsSep(c, "rn-name")
			settingsRow(c, "rn-start", l.RenameStartRow(), "", func() {
				r.field(c, "rn-start-in", &r.fmtStartText, "1")
			})
			settingsSep(c, "rn-start")
			settingsRow(c, "rn-digits", l.RenameDigitsRow(), "", func() {
				ui.Segmented(c, &r.digitsSeg, "1", "2", "3", "4").Label(l.RenameDigitsLabel())
			})
			settingsSep(c, "rn-digits")
			settingsRow(c, "rn-pos", l.RenamePosRow(), "", func() {
				ui.Segmented(c, &r.posSeg, l.RenameBefore(), l.RenameAfter()).Label(l.RenamePosLabel())
			})
		}
	})
}

// field is one right-aligned single-line input inside a settingsRow:
// Enter applies the rename and Escape closes the window, the same keys
// the buttons answer — an input keeps them to itself otherwise.
func (r *renameUI) field(c *ui.Context, key string, value *string, placeholder string) {
	in := ui.TextInputBase(c.Key(key), value).Width(180).FontSize(13).TextColor(setInk).
		Placeholder(placeholder).Label(placeholder)
	if !r.booted && (renameMode(r.mode) == modeReplace && key == "rn-find-in" ||
		renameMode(r.mode) == modeAdd && key == "rn-add-in" ||
		renameMode(r.mode) == modeFormat && key == "rn-name-in") {
		in.Focus()
	}
	in.OnTextKey(func(k ui.TextKey) bool {
		switch k.Key {
		case ui.KeyEnter:
			r.apply()
			return true
		case ui.KeyEscape:
			if r.win != nil {
				r.win.Close()
			}
			return true
		}
		return false
	})
}

// previewSection is the live old → new list: conflicts render in danger
// red with their reason, unchanged rows dim. The list scrolls inside its
// card when the batch is long.
func (r *renameUI) previewSection(c *ui.Context) {
	l := loc(c)
	pl := r.plan()
	// The preview takes whatever height the mode's card leaves, so the
	// format mode's four rows never push 改名 below the window: the list
	// scrolls inside its card instead.
	ui.Column(c.Key("sec-preview")).FillWidth().Grow(1).MinHeight(0).Children(func() {
		ui.Text(c, l.RenamePreview()).SingleLine().FontSize(11).FontWeight(600).TextColor(setInk3).Padding(0, 0, 6, 4)
		ui.Column(c.Key("card-preview")).Radius(10).Background(setCard).Border(0.5, setCardEdge).
			FillWidth().Grow(1).MinHeight(0).Children(func() {
			ui.Scroll(c.Key("rn-preview")).Fill().MinHeight(0).TrackScroll(&r.scroll).Children(func() {
				ui.Column(c.Key("rn-rows")).FillWidth().Children(func() {
					for i, row := range pl.rows {
						if i > 0 {
							settingsSep(c, "rn-"+row.item.ID)
						}
						rowEl := ui.Row(c.Key("rn-row-"+row.item.ID)).AlignItems(ui.Center).Gap(8).
							MinHeight(30).Padding(0, 12).FillWidth()
						rowEl.Children(func() {
							ui.Text(c, row.oldName).SingleLine().FontSize(12).TextColor(setInk2)
							ui.Text(c, "→").SingleLine().FontSize(12).TextColor(setInk3)
							newColor := setInk
							if row.warn != "" {
								newColor = danger
							} else if row.newName == row.oldName {
								newColor = setInk3
							}
							ui.Text(c, row.newName).SingleLine().FontSize(12).FontWeight(600).TextColor(newColor)
							ui.Box(c).Grow(1)
							if row.warn != "" {
								ui.Text(c, row.warn).SingleLine().FontSize(11).TextColor(danger)
							}
						})
					}
				})
			})
		})
		// The status line under the card: how many rows change, or why
		// 改名 is locked.
		foot := ""
		switch {
		case !r.startOK() && renameMode(r.mode) == modeFormat:
			foot = l.RenameStartNotNumber()
		case pl.conflicts > 0:
			foot = l.RenameConflicts(pl.conflicts)
		case pl.changed == 0:
			foot = l.RenameNoChange()
		default:
			foot = l.RenameWillChange(pl.changed)
		}
		ui.Text(c, foot).FontSize(11).TextColor(setInk3).Padding(4, 12, 0, 12)
	})
}

// buttons is the footer row: 取消 on the left's right side pair as macOS
// dialogs do — 取消 secondary, 改名 the accented default (Enter), greyed
// while anything blocks the batch.
func (r *renameUI) buttons(c *ui.Context, apply func()) {
	l := loc(c)
	blocked := r.blocked(r.plan())
	ui.Row(c.Key("rn-buttons")).FillWidth().Justify(ui.End).Gap(8).Padding(4, 0, 0, 0).Children(func() {
		if ui.Button(c, l.RenameCancel()).Label(l.RenameCancelLabel()).Clicked() {
			r.win.Close()
		}
		if ui.Button(c, l.RenameApply()).Label(l.RenameApplyLabel()).Disabled(blocked).Clicked() {
			apply()
		}
	})
}

// doneView replaces the form after a batch applied: the completion line
// with 撤销 (which restores the original names and stays available until
// used or a new batch applies) and 完成 to close. Enter/Esc close here.
func (r *renameUI) doneView(c *ui.Context) {
	l := loc(c)
	ui.Column(c.Key("done")).Fill().Center().Gap(14).Children(func() {
		ui.Text(c, l.RenameDoneCount(len(r.done.ops))).FontSize(15).FontWeight(600).TextColor(setInk)
		ui.Text(c, l.RenameDoneSynced()).FontSize(11).TextColor(setInk3)
		if r.err != "" {
			ui.Text(c, r.err).FontSize(11).TextColor(danger).Padding(0, 24, 0, 24)
		}
		ui.Row(c.Key("done-buttons")).Gap(8).Children(func() {
			if ui.Button(c, l.RenameUndo()).Label(l.RenameUndoThisLabel()).Clicked() {
				r.undo()
			}
			if ui.Button(c, l.RenameDoneClose()).Label(l.RenameDoneCloseLabel()).Clicked() {
				r.win.Close()
			}
		})
	})
}
