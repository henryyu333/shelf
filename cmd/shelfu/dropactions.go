//go:build darwin

package main

// The drop zone: while a system drag carrying files hovers over the
// expanded panel, the stage swaps the current page for a 放入托盘 row over
// a row of action cells picked for the dragged files. Dropping on a cell
// runs that action (the same conversion pipeline the item menu drives);
// dropping anywhere else, or dragging anything that isn't files, keeps
// the plain receive path. mygo only tells a hover which formats the drag
// offers, so the file paths come from the drag pasteboard via
// mac.DragFiles.
//
// Layout follows design/drop-actions-v1.html: the 64pt 放入托盘 row (icon
// well, title and 保存引用，随时拖出, up to three stacked thumbnails) over
// up to four 84pt action cells, 6pt gaps, fill1 surfaces, 14pt radii. The
// module bar reads 投放 with a count, and hints 松开放入托盘，或拖到动作上.

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/transfer"
	"github.com/egoist/mygo/ui"

	"shelf/internal/mac"
	"shelf/internal/store"
	"shelf/locales"
)

// Symbols for the action cells, after the design draft's glyphs.
var (
	shrinkSymbol  = symbol(`<path d="M4 14h6v6M20 10h-6V4M14 10l7-7M3 21l7-7"/>`)
	pdfSymbol     = symbol(`<path d="M14 3H6a1 1 0 0 0-1 1v16a1 1 0 0 0 1 1h12a1 1 0 0 0 1-1V8z"/><path d="M14 3v5h5"/>`)
	imgSymbol     = symbol(`<rect x="3" y="5" width="18" height="14" rx="2"/><circle cx="9" cy="10" r="1.6"/><path d="M21 16l-5-5-8 8"/>`)
	docSymbol     = symbol(`<path d="M14 3H6a1 1 0 0 0-1 1v16a1 1 0 0 0 1 1h12a1 1 0 0 0 1-1V8z"/><path d="M14 3v5h5M9 13h6M9 17h4"/>`)
	airDropSymbol = symbol(`<path d="M5.6 15.5a8 8 0 1 1 12.8 0"/><path d="M8.5 13a4.5 4.5 0 1 1 7 0"/><path d="M12 14l-4 6h8z"/>`)
	unzipSymbol   = symbol(`<path d="M4 7h16v13H4z"/><path d="M4 7l2-3h12l2 3M12 10v6M9.5 13.5L12 16l2.5-2.5"/>`)
	zipSymbol     = symbol(`<path d="M4 7h16v13H4z"/><path d="M4 7l2-3h12l2 3M11 10h2M11 13h2M11 16h2"/>`)
)

// The drop zone's measurements, straight from the design file
// (1px there = 1pt here).
const (
	dropTrayH    = 64 // 放入托盘 row
	dropActionH  = 84 // one action cell
	dropZoneGap  = 6  // tray row ↔ action row, and between action cells
	dropMaxCells = 4  // most action cells shown at once
	dropMaxThumb = 3  // most thumbnails stacked on the tray row
)

// dropZoneNeedH is the stage height the zone wants: tray row plus the
// action row and the gaps between them.
func dropZoneNeedH() int {
	return dropTrayH + dropZoneGap + dropActionH
}

// dropCellOpts narrows the cells' accepted formats to files: a text or
// image-data drag crossing a cell stays a plain receive, never an action.
var dropCellOpts = transfer.DropOptions{
	Formats:    []transfer.Format{transfer.FileList, transfer.URIList},
	Operations: transfer.Copy,
}

// Seams: the pasteboard reads, the dropped data's file extraction,
// AirDrop, the drag session probe and the cursor probe are substituted
// in tests.
var (
	dragFiles = func(a *shelf) []string {
		if a.win == nil {
			return nil
		}
		return mac.DragFiles()
	}
	dragFilesChange = func(a *shelf) int64 {
		if a.win == nil {
			return 0
		}
		return mac.DragFilesChange()
	}
	dropFilesData = func(d transfer.Data) ([]string, error) { return d.Files() }
	airDrop       = mac.AirDrop
	cursorPoint   = func() mygo.Point { return mygo.Screen.CursorScreenPoint() }
	dragState     = func() (bool, int64) { return mac.DragState() }
	dragTypes     = mac.DragTypes
	dragLeftDown  = func() bool { return mac.LeftMouseDown() }
)

// dropZoneState is the drop zone's memory of the drag in flight. paths
// are cached by the drag pasteboard's changeCount — the board answers a
// cheap serial so the file list (and its os.Stat classification) is only
// re-read when it actually changed, never once per frame. When the system
// says the drag ended, the zone holds for dropHoldGrace: the release's
// mouse-up clears the monitor's dragging flag before the drop reaches the
// render that owns the cells, and without the hold that frame would build
// no cell targets and the drop would be answered but never delivered —
// files swallowed. done marks the drop consumed (a cell or the plain
// receive path ran); the grace only ever covers the delivery lag.
type dropZoneState struct {
	serial int64     // drag session the cache belongs to
	count  int64     // drag pasteboard changeCount last read
	loaded bool      // count and files belong to this session, even when count is zero
	files  []string  // paths read at count
	class  dropClass // their classification
	until  time.Time // hold deadline once the drag flag fell
	done   bool      // this session's drop already landed
	frozen bool      // the release happened (or the board rewrote): the shown set is final
	on     bool      // the zone drew this frame

}

// dropHoldGrace is how long the zone outlives the dragging flag so the
// release's drop still finds its cells: a few frames is plenty — the
// native drop has already been answered by then.
const dropHoldGrace = 300 * time.Millisecond

// dropDebugFiles pins the zone's contents for the screenshot harness:
// SHELF_DEBUG_DROP_FILES=/a.png,/b.zip (path-list separated) makes a
// visible panel draw the zone as if those paths hovered, without a system
// drag. Unset in normal runs.
func dropDebugFiles() []string {
	return filepath.SplitList(os.Getenv("SHELF_DEBUG_DROP_FILES"))
}

// dropAction is one action cell: glyph, label, subtitle, and what a drop
// on it runs. target is the convertTarget fed to the conversion pipeline;
// airDrop is the odd one out — it opens the system sheet and stores
// nothing; isOCRCopy (the 框选文字 card) recognizes the images' text and
// puts it on the clipboard instead of shelving anything.
type dropAction struct {
	id        string
	icon      *ui.SVG
	label     string
	sub       string
	target    convertTarget
	isAirDrop bool
	isOCRCopy bool
}

// dropClass is what the dragged paths are, as a group.
type dropClass int

const (
	classImages   dropClass = iota // every path an image file
	classPDFs                      // every path a PDF
	classArchives                  // every path an archive the extractor takes
	classMixed                     // anything else: folders, other files, a mix
)

// classifyDrop sorts the dragged paths into the group the action rules
// read. A path that fails os.Stat counts as mixed — a vanished file could
// be anything, and the file actions would only fail later anyway.
func classifyDrop(paths []string) dropClass {
	if len(paths) == 0 {
		return classMixed
	}
	images, pdfs, archives := true, true, true
	for _, p := range paths {
		fi, err := os.Stat(p)
		if err != nil || fi.IsDir() {
			return classMixed
		}
		ext := strings.ToLower(filepath.Ext(p))
		if !imageExts[ext] {
			images = false
		}
		if ext != ".pdf" {
			pdfs = false
		}
		if archiveExt(p) == "" {
			archives = false
		}
	}
	switch {
	case images:
		return classImages
	case pdfs:
		return classPDFs
	case archives:
		return classArchives
	}
	return classMixed
}

// dropActions picks the action cells for the dragged paths, left to
// right, at most dropMaxCells. No paths — a source vending promised or
// remote files exposes none until the drop — shows only the 放入托盘 row.
// It names the cells in the source language; dropActionsIn says them in
// the language of l.
func dropActions(paths []string) []dropAction {
	return dropActionsIn(locales.In(""), paths)
}

// dropActionsIn is dropActions in the language of l.
func dropActionsIn(l locales.Messages, paths []string) []dropAction {
	return dropActionsForIn(l, paths, classifyDrop(paths))
}

// dropActionsFor is dropActionsIn on a classification the caller already
// made, in the source language.
func dropActionsFor(paths []string, class dropClass) []dropAction {
	return dropActionsForIn(locales.In(""), paths, class)
}

// dropActionsForIn is the cell table in the language of l; the zone
// classifies once per pasteboard change and reuses the class here.
func dropActionsForIn(l locales.Messages, paths []string, class dropClass) []dropAction {
	if len(paths) == 0 {
		return nil
	}
	air := dropAction{id: "airdrop", icon: airDropSymbol, label: l.CardAirDrop(), isAirDrop: true}
	switch class {
	case classImages:
		allJPEG := true
		pdfLabel, pdfSub, pdfTarget := l.CardToPDF(), l.DropPdfPage(), toPDF
		if len(paths) > 1 {
			pdfLabel, pdfSub, pdfTarget = l.DropPdfMerged(), l.DropPdfMergedSub(), toMergedPDF
		}
		for _, p := range paths {
			if ext := strings.ToLower(filepath.Ext(p)); ext != ".jpg" && ext != ".jpeg" {
				allJPEG = false
				break
			}
		}
		other := dropAction{id: "tojpeg", icon: imgSymbol, label: l.CardToJPEG(), sub: l.DropPerFile(), target: toJPEG}
		if allJPEG {
			other = dropAction{id: "topng", icon: imgSymbol, label: l.CardToPNG(), sub: l.DropPerFile(), target: toPNG}
		}
		return []dropAction{
			{id: "fit1m", icon: shrinkSymbol, label: l.CardFit1M(), sub: l.DropWithin(), target: toFit1M},
			{id: "topdf", icon: pdfSymbol, label: pdfLabel, sub: pdfSub, target: pdfTarget},
			other,
			air,
		}
	case classPDFs:
		return []dropAction{
			{id: "toword", icon: docSymbol, label: l.CardToWord(), sub: l.DropExtractText(), target: toWord},
			{id: "topng", icon: imgSymbol, label: l.DropPagesAsImages(), sub: l.DropPngPage(), target: toPNG},
			zipAction(l, len(paths)),
			air,
		}
	case classArchives:
		sub := filepath.Base(paths[0])
		if len(paths) > 1 {
			sub = l.DropCountItems(len(paths))
		}
		return []dropAction{
			{id: "extract", icon: unzipSymbol, label: l.CardExtract(), sub: sub, target: toExtract},
			air,
		}
	}
	return []dropAction{zipAction(l, len(paths)), air}
}

// zipAction builds the 打成 zip cell; its subtitle previews the count the
// archive name will carry.
func zipAction(l locales.Messages, n int) dropAction {
	sub := ""
	if n > 1 {
		sub = l.DropCountItems(n)
	}
	return dropAction{id: "zip", icon: zipSymbol, label: l.CardZip(), sub: sub, target: toZip}
}

// dropCountText is the module-bar count: 「3 张图片」, 「1 个压缩包」,
// 「2 个 PDF」, 「2 项」 for anything mixed.
func dropCountText(paths []string) string {
	return dropCountTextIn(locales.In(""), len(paths), classifyDrop(paths))
}

// dropCountTextFor is dropCountText on a classification the caller
// already made, in the source language.
func dropCountTextFor(n int, class dropClass) string {
	return dropCountTextIn(locales.In(""), n, class)
}

// dropCountTextIn says the count in the language of l: 「3 张图片」,
// 「1 个压缩包」, 「2 个 PDF」, 「2 项」 for anything mixed.
func dropCountTextIn(l locales.Messages, n int, class dropClass) string {
	if n == 0 {
		return ""
	}
	switch class {
	case classImages:
		return l.DropCountImages(n)
	case classPDFs:
		return l.DropCountPDFs(n)
	case classArchives:
		return l.DropCountArchives(n)
	}
	return l.DropCountItems(n)
}

// dropZoneShow answers whether the dragged files become the drop zone
// (needH then). Design §1: only a drag that began while the panel was
// already open on the tools page keeps the grid and routes drops to the
// cards; a drag that opens the panel always gets the zone, whatever tab
// the panel shows.
func (a *shelf) dropZoneShow() (on bool, need int) {
	if a.dragOpenTools {
		return false, 0
	}
	return true, dropZoneNeedH()
}

// dropZoneSnap answers which files the zone should show, updating the
// shelf's dropZoneState. Order matters: the pointer check runs before any
// pasteboard read, so a drag merely passing over the notch strip never
// touches the board. While the drag is in flight the files come from the
// board, refetched only when its changeCount moved; classification rides
// the same cache. Once the flag falls the cached files keep answering for
// dropHoldGrace — or until the drop is consumed — so the release frame
// still builds the cells it needs. ShelfU's own drag-out, a drag carrying
// no files, and a release outside the panel all answer nil. The debug pin
// SHELF_DEBUG_DROP_FILES skips the monitor: it makes the shelf build the
// drop zone for a fixed file list, for screenshots and smoke tests.
func (a *shelf) dropZoneSnap(over bool) []string {
	d := &a.drop
	d.on = false
	if a.ownDrag {
		// ShelfU's own drag-out writes the pasteboard too. It is not an
		// incoming drag, and a set an earlier drop left must not linger.
		*d = dropZoneState{serial: d.serial}
		return nil
	}
	if raw := os.Getenv("SHELF_DEBUG_DROP_FILES"); raw != "" {
		if !slices.Equal(d.files, dropDebugFiles()) {
			d.files = dropDebugFiles()
			d.class = classifyDrop(d.files)
		}
		if len(d.files) > 0 {
			d.on, _ = a.dropZoneShow()
			return d.files
		}
	} else if !a.dragging && !d.loaded && !d.frozen {
		d.files = nil
	}
	// The release freezes the shown set: the pasteboard's changeCount bumps
	// while the monitor still reports the drag, and a fresh drag's serial
	// change can land before an answered drop is delivered — neither may
	// swap or discard the cells the user released on. The freeze holds the
	// same files until the drop lands (done) or the hold expires.
	if d.frozen {
		if d.done || len(d.files) == 0 || (d.until.IsZero() || time.Now().After(d.until)) {
			d.frozen = false
			*d = dropZoneState{}
			return nil
		}
		if a.win != nil {
			p := cursorPoint()
			if !insideRect(p, a.panelDropRect()) {
				d.frozen = false
				*d = dropZoneState{}
				return nil // released outside the panel: nothing to deliver
			}
		}
		d.on, _ = a.dropZoneShow()
		return d.files
	}
	if a.dragging {
		if d.serial != a.dragSerial {
			*d = dropZoneState{serial: a.dragSerial}
		}
		inside := over || a.win == nil
		if !inside {
			p := cursorPoint()
			inside = insideRect(p, a.panelDropRect())
		}
		if !inside {
			// The drag left the panel: no cells are under it, so the held
			// set must not keep painting the tools page. Re-entering reads
			// the board again below.
			d.files, d.class, d.loaded = nil, 0, false
			return nil
		}
		// Once the zone has files its picture is the snapshot the user aims
		// at; only an empty zone tracks board changes (promised files can
		// arrive late). A count change on a loaded zone is the release's
		// rewrite — freeze instead of swapping cells under the pointer.
		if count := dragFilesChange(a); !d.loaded || (count != d.count && len(d.files) == 0) {
			d.count, d.loaded = count, true
			d.files = dragFiles(a)
			d.class = classifyDrop(d.files)
		} else if len(d.files) > 0 && count != d.count {
			d.frozen = true
			d.until = time.Now().Add(dropHoldGrace)
			d.on, _ = a.dropZoneShow()
			return d.files
		}
		if len(d.files) == 0 {
			return nil
		}
		d.on, _ = a.dropZoneShow()
		return d.files
	}
	// The drag flag fell: keep serving this session's files briefly so a
	// release already answered but not yet rendered still finds its cell.
	// The serial check is dropped: a new drag starting inside grace must
	// not discard a drop the old session already accepted.
	if d.done {
		// The drop was consumed: nothing is left to deliver, so the set
		// does not outlive its own drop (a held set would keep the tools
		// page looking like a drag the user already finished).
		*d = dropZoneState{serial: d.serial}
		return nil
	}
	if len(d.files) == 0 {
		return nil
	}
	if d.until.IsZero() {
		d.until = time.Now().Add(dropHoldGrace)
	} else if time.Now().After(d.until) {
		return nil
	}
	if a.win != nil {
		p := cursorPoint()
		if !insideRect(p, a.panelDropRect()) {
			return nil // released outside the panel: nothing to deliver
		}
	}
	d.frozen = true // the drag ended: this set is final until delivered
	d.on, _ = a.dropZoneShow()
	return d.files
}

// dropStage draws the content during a file drag: the 放入托盘 row, then
// the action cells, inside the fixed 396 × 174 box (154 of it used).
func (a *shelf) dropStage(c *ui.Context, paths []string, class dropClass) {
	zone := ui.Column(c.Key("drop-zone")).FillWidth().Grow(1).MinHeight(float32(dropZoneNeedH())).Gap(dropZoneGap)
	zone.Children(func() {
		a.dropTrayCell(c, paths)
		if actions := dropActionsForIn(loc(c), paths, class); len(actions) > 0 {
			a.dropActionRow(c, actions)
		}
	})
}

// dropTrayCell is the 64pt 放入托盘 row: a file-icon well, the title and
// 保存引用，随时拖出, and up to three stacked thumbnails of the dragged
// files. It is a nested drop target, so releasing over it — the largest
// target — means 放入托盘 exactly like dropping on empty space.
func (a *shelf) dropTrayCell(c *ui.Context, paths []string) {
	row := ui.Row(c.Key("drop-tray")).Height(dropTrayH).FillWidth().Radius(14).
		Padding(0, 16, 0, 16).Gap(12).AlignItems(ui.Center).Label(loc(c).DropPutInTray())
	_, hot := ui.DataDragOver(row, dropCellOpts)
	if hot {
		row.Background(accent16).Border(1.5, accent.Alpha(0.9))
	} else {
		row.Background(fill1).Border(1, ui.RGBA(255, 255, 255, 0.08))
	}
	row.Children(func() {
		ui.Row(c).Size(34, 34).Radius(10).Background(fill2).Center().Children(func() {
			ui.Icon(c, trayNavSymbol).Size(18, 18).TextColor(ink)
		})
		ui.Column(c).Gap(2).Children(func() {
			panelLabel(c, nil, loc(c).DropPutInTray(), 13, 600).TextColor(ink)
			panelLabel(c, nil, loc(c).DropTrayHint(), 11, 0).TextColor(ink3)
		})
		ui.Box(c).Grow(1)
		a.dropThumbStack(c, paths)
	})
	if drop, ok := ui.DropData(row, dropCellOpts); ok {
		a.drop.done = true
		a.receive(drop.Data)
	}
}

// dropThumbStack draws up to dropMaxThumb overlapped thumbnails at the
// tray row's right edge, newest on top. A path with no decoded picture
// yet paints a tinted square; the stack fills in as decodes land.
func (a *shelf) dropThumbStack(c *ui.Context, paths []string) {
	show := paths[:min(len(paths), dropMaxThumb)]
	bitmaps := a.dropThumbs(show)
	ui.Row(c.Key("drop-stack")).AlignItems(ui.Center).Children(func() {
		for i, p := range show {
			cell := ui.Row(c.Key(p)).Size(26, 26).Radius(6).Center().Clip().
				Border(1.5, surface)
			if i > 0 {
				cell.Margin(0, 0, 0, -8) // overlap like the design's stack
			}
			if b := bitmaps[i]; b != nil {
				cell.Children(func() {
					ui.Image(c.Key("bmp"), b).Size(26, 26).Fit(ui.Cover)
				})
			} else {
				cell.Background(fill3).Children(func() {
					ui.Icon(c, fileSymbol).Size(13, 13).TextColor(ink2)
				})
			}
		}
	})
}

// dropActionRow lays out the action cells in one row of at most four
// equal columns, matching the design's grid; a shorter rule leaves the
// empty slots blank rather than stretching the cells wider.
func (a *shelf) dropActionRow(c *ui.Context, actions []dropAction) {
	busy := a.conv != nil && a.conv.busy()
	ui.Row(c.Key("drop-acts")).FillWidth().Gap(dropZoneGap).Children(func() {
		for _, act := range actions {
			a.dropActionCell(c, act, busy)
		}
		for i := len(actions); i < dropMaxCells; i++ {
			ui.Box(c).Grow(1)
		}
	})
}

// dropActionCell is one action: an 84pt fill1 card with the 20pt glyph,
// the 12 semibold name and a 10pt ink3 subtitle. While the pointer drags
// over it the card takes the accent treatment — 16% accent fill, a 1.5pt
// inner stroke, a light spring to 1.03× — and it is its own drop target,
// so a release there runs the action instead of shelving the files. A
// running conversion greys the whole row: disabled, a cell's target is
// skipped by the drop router and the release falls through to the window
// as 放入托盘 — 「一次只跑一个任务」, same as the item menu.
func (a *shelf) dropActionCell(c *ui.Context, act dropAction, busy bool) {
	cell := ui.Column(c.Key("drop-act-" + act.id)).Height(dropActionH).Grow(1).MinWidth(0).
		Radius(14).Center().Gap(6).Label(act.label)
	_, hot := ui.DataDragOver(cell, dropCellOpts)
	if hot && !busy {
		cell.Background(accent16).Border(1.5, accent.Alpha(0.9))
	} else {
		cell.Background(fill1)
	}
	if a.win != nil && !c.Preferences().ReduceMotion {
		target := float32(1)
		if hot && !busy {
			target = 1.03
		}
		scale := cell.AnimateTo("hot", target, ui.Animation{Spring: ui.SpringOptions{Duration: 200 * time.Millisecond, Bounce: 0.15}})
		cell.Scale(scale, scale)
	} else if hot && !busy {
		cell.Scale(1.03, 1.03)
	}
	cell.Children(func() {
		ui.Icon(c, act.icon).Size(20, 20).TextColor(ink)
		panelLabel(c, nil, act.label, 12, 600).TextColor(ink)
		if act.sub != "" {
			panelLabel(c, nil, act.sub, 10, 0).TextColor(ink3)
		}
	})
	if busy {
		cell.Disabled(true).Opacity(0.38)
	}
	if drop, ok := ui.DropData(cell, dropCellOpts); ok {
		a.runDropAction(act, drop.Data)
	}
}

// runDropAction executes the cell the files were released on. The set it
// acts on is the set the cells drew — a.drop.files — frozen at release so
// a board rewrite under the pointer can't swap the job; the drop data is
// the fallback for sources that never exposed paths mid-drag. AirDrop
// only opens the system sheet, isOCRCopy only fills the clipboard.
// Everything else drives the existing conversion pipeline on items built
// from the paths: they are not shelved; only the outputs land in the
// tray, front and selected, so the panel settles on the tray page.
func (a *shelf) runDropAction(act dropAction, data transfer.Data) {
	a.received = true
	a.drop.done = true
	files := a.drop.files
	if len(files) == 0 {
		var err error
		if files, err = dropFilesData(data); err != nil || len(files) == 0 {
			if a.tab != pageTray {
				a.setTab(pageTray)
			}
			a.receive(data)
			return
		}
	}
	a.runDropActionFiles(act, files, data)
}

// runDropActionFiles is runDropAction with the file set decided by the
// caller: the pin windows' drops never pass through a.drop.files (their
// drag is its own), so they hand the paths over directly. A release whose
// action cannot run — the pipeline is busy or the convert job fails to
// start — still shelves the files rather than losing them.
func (a *shelf) runDropActionFiles(act dropAction, files []string, data transfer.Data) {
	if act.isOCRCopy {
		// 识别文字并复制：产出是剪贴板，不进托盘，页面留在原地。
		a.copyDroppedText(files)
		return
	}
	if a.tab != pageTray {
		a.setTab(pageTray)
	}
	if act.isAirDrop {
		if err := airDrop(files, nil); err != nil {
			a.report(err)
		}
		return
	}
	items := make([]store.Item, len(files))
	for i, p := range files {
		if abs, err := filepath.Abs(p); err == nil {
			p = abs
		}
		_, missing := os.Stat(p)
		items[i] = store.Item{ID: "drop-" + p, Kind: store.KindFile, Path: p, Missing: missing != nil}
	}
	if !a.startConvert(items, act.target) {
		// The pipeline refused — busy with a job, or its own already-
		// reported failure — so the files go to the tray like a release
		// on a cell that could not run, instead of vanishing on the drop.
		a.receive(data)
	}
}

// dropThumbs caches one thumbnail per dragged path for the tray row's
// stack, keyed by path since the files are not shelf items. Picture files
// decode off the main thread; everything else takes its Finder icon.
// There is one shelf, so the cache lives on the package; each frame drops
// entries for paths no longer dragged in, keeping it bounded by the
// handful of files a drag carries.
var (
	dropThumbCache  = map[string]*ui.Bitmap{}
	dropThumbBusy   = map[string]int{} // path → generation of the decode in flight
	dropThumbSeq    int
	dropThumbActive int // decoders in flight, capped like the tray's
)

// dropThumbWork and dropThumbUpdate are the two seams headless tests swap:
// the goroutine spawn and the main-thread hand-off.
var (
	dropThumbWork   = func(fn func()) { go fn() }
	dropThumbUpdate = func(a *shelf, fn func()) { a.win.Update(fn) }
)

// dropThumbs answers the cached thumbnails for paths in order (nil for
// undecoded or undecodable), starting decodes for new paths. Headless
// renders never start work — no window to repaint into.
func (a *shelf) dropThumbs(paths []string) []*ui.Bitmap {
	for k := range dropThumbCache {
		if !slices.Contains(paths, k) {
			delete(dropThumbCache, k)
		}
	}
	for k := range dropThumbBusy {
		if !slices.Contains(paths, k) {
			// The path left the stack mid-decode: removing the busy mark is
			// what stops finishDropThumb from writing the now-unwanted
			// result back into the cache.
			delete(dropThumbBusy, k)
		}
	}
	out := make([]*ui.Bitmap, len(paths))
	for i, p := range paths {
		if b, ok := dropThumbCache[p]; ok {
			out[i] = b
			continue
		}
		a.startDropThumb(p)
	}
	return out
}

// startDropThumb begins decoding path's thumbnail; the picture decode
// runs off the main thread, the Finder-icon fallback on it, and the
// result lands through a window update. At most maxConcurrentThumbs run
// at once — a full lane retries next frame instead of queueing, same as
// the tray thumbnails.
func (a *shelf) startDropThumb(path string) {
	if a.win == nil || a.winH == 0 || dropThumbBusy[path] != 0 {
		return
	}
	if dropThumbActive >= maxConcurrentThumbs {
		return
	}
	dropThumbSeq++
	seq := dropThumbSeq
	dropThumbBusy[path] = seq
	dropThumbActive++
	dropThumbWork(func() {
		var bmp *ui.Bitmap
		if img := decodeThumb(path); img != nil {
			bmp = ui.NewBitmap(img)
		}
		dropThumbUpdate(a, func() { a.finishDropThumb(path, bmp, seq) })
	})
}

// finishDropThumb runs on the main thread through the window's update:
// it writes the decoded bitmap unless the path's busy mark was cleared
// (the path left the stack mid-decode) or replaced (a newer decode for
// the same path owns it — the stale result must not evict that slot).
func (a *shelf) finishDropThumb(path string, bmp *ui.Bitmap, seq int) {
	dropThumbActive--
	if dropThumbBusy[path] != seq || a.quitting {
		return
	}
	delete(dropThumbBusy, path)
	if bmp == nil {
		if icon := mac.FileIcon(path, 26); len(icon) > 0 {
			bmp, _ = ui.DecodeBitmap(icon)
		}
	}
	dropThumbCache[path] = bmp
}
