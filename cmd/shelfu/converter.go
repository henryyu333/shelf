//go:build darwin

package main

import (
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"shelf/internal/docx"
	"shelf/internal/mac"
	"shelf/internal/store"
	"shelf/locales"
)

// Image conversion (DESIGN.md「图片转换」): the item menu offers 转换为 ▸
// JPEG / HEIC / PNG / PDF / Word for images, and 合并为一个 PDF for several.
// A job runs off the main thread; its outputs are written into the store's
// converted/ folder, added to the front of the tray and selected, so one
// drag takes them all. Originals are never touched.

// convertTarget is one kind of output the menu offers.
type convertTarget int

const (
	toJPEG convertTarget = iota
	toHEIC
	toPNG
	toPDF       // one PDF per image
	toWord      // recognized text + the picture, one .docx per image
	toMergedPDF // every image a page of one PDF
	toZip       // every file and folder in one .zip
	toLarge     // each image's longer side at most 2560 px
	toMedium    // … 1600 px
	toSmall     // … 800 px
	toFit500    // each image compressed under 500 KB
	toFit1M     // … 1 MB
	toFit2M     // … 2 MB
	toFit5M     // … 5 MB
	toExtract   // each archive unpacked into its own folder
)

// resizePx is the longer-side cap of a resize target, 0 for the others.
func resizePx(t convertTarget) int {
	switch t {
	case toLarge:
		return 2560
	case toMedium:
		return 1600
	case toSmall:
		return 800
	}
	return 0
}

// convertExt is the output file extension of a target. A resized image
// keeps its format (resizeExt), so resize targets have none here.
func convertExt(t convertTarget) string {
	switch t {
	case toJPEG:
		return "jpg"
	case toHEIC:
		return "heic"
	case toPNG:
		return "png"
	case toWord:
		return "docx"
	case toZip:
		return "zip"
	case toFit500, toFit1M, toFit2M, toFit5M, toExtract:
		return "" // chosen per source in planConvert
	default:
		return "pdf"
	}
}

// resizeExt is a resized image's extension: JPEG, PNG and HEIC keep
// their format, everything else (WebP, TIFF…, which ImageIO cannot
// always write) becomes JPEG.
func resizeExt(item store.Item) string {
	switch f := imageFormatOf(item); f {
	case "jpg", "png", "heic":
		return f
	}
	return "jpg"
}

// extFormat is the encoder for an output extension.
func extFormat(ext string) mac.ImageFormat {
	switch ext {
	case "jpg":
		return mac.FormatJPEG
	case "heic":
		return mac.FormatHEIC
	}
	return mac.FormatPNG
}

// convertShowAfter keeps instant conversions (a format change takes a few
// milliseconds) from flashing an activity chip into the band.
const convertShowAfter = 250 * time.Millisecond

// Seams: tests run jobs synchronously against fakes.
var (
	convertImage    = mac.ConvertImage
	resizeImage     = mac.ResizeImage
	imagesToPDF     = mac.ImagesToPDF
	recognizeText   = mac.RecognizeText
	imageSize       = mac.ImageSize
	pdfPageCount    = mac.PDFPageCount
	pdfText         = mac.PDFText
	renderPDFPage   = mac.RenderPDFPage
	writeDocx       = docx.Write
	writeDocxPages  = docx.WritePages
	convertWork     = func(fn func()) { go fn() }
	convertDispatch = mygo.RunOnMain
)

// recognizeMu serializes every Vision text request: the ocrIndex's
// single-worker rule only covers the paths that go through it, while the
// Word converter called the recognizer directly and could run a second
// Vision job — model load included — concurrently.
var recognizeMu sync.Mutex

// recognizeImageText is the one call site every recognition goes through —
// the background index, the menu's 「识别文字并复制」 and the Word
// converter — so at most one Vision request (including model load) is ever
// in flight.
func recognizeImageText(path string) ([]string, error) {
	recognizeMu.Lock()
	defer recognizeMu.Unlock()
	return recognizeText(path)
}

// imageExts are the file extensions the converter accepts as images:
// what ImageIO reads on every supported macOS.
var imageExts = map[string]bool{
	".jpg": true, ".jpeg": true, ".png": true, ".heic": true, ".heif": true,
	".webp": true, ".tif": true, ".tiff": true, ".gif": true, ".bmp": true, ".avif": true,
}

// convertible reports whether item is an image the converter can read: a
// screenshot or clipboard image, or an image file that is still there.
func convertible(item store.Item) bool {
	switch {
	case item.Missing:
		return false
	case item.Kind == store.KindImage:
		return true
	case item.Kind == store.KindFile:
		return imageExts[strings.ToLower(filepath.Ext(item.Path))]
	}
	return false
}

// isPDF reports whether item is a PDF file that is still there; PDFs
// convert to Word (their text) and to images (one per page).
func isPDF(item store.Item) bool {
	return item.Kind == store.KindFile && !item.Missing && strings.EqualFold(filepath.Ext(item.Path), ".pdf")
}

// imageFormatOf names an image item's format as an output extension
// ("png", "jpg", "heic"), so the menu can grey out 「当前格式」.
func imageFormatOf(item store.Item) string {
	if item.Kind == store.KindImage {
		return "png"
	}
	switch ext := strings.ToLower(strings.TrimPrefix(filepath.Ext(item.Path), ".")); ext {
	case "jpeg":
		return "jpg"
	case "heif":
		return "heic"
	default:
		return ext
	}
}

// convertBaseName is the output's file name without extension: the
// item's own name, or the screenshot title with the clock's colons (which
// Finder shows as slashes) turned into dots.
func convertBaseName(item store.Item) string {
	if item.Kind == store.KindImage {
		return strings.ReplaceAll(itemTitle(item), ":", ".")
	}
	base := filepath.Base(item.Path)
	return strings.TrimSuffix(base, filepath.Ext(base))
}

// convertJob is one run of the converter. total, done and the step flags
// are written by the worker and read by the main thread's drawing, hence
// atomics; target, pdf and unit never change.
type convertJob struct {
	target  convertTarget
	pdf     bool   // the sources are PDFs: a Word output without text is a blank document
	unit    string // what the step counts, in the language the job started in
	total   atomic.Int32
	done    atomic.Int32
	skipped atomic.Int32    // 「压缩到」sources already under the byte budget
	over    atomic.Int32    // 「压缩到」outputs that still exceed it
	taken   map[string]bool // output names this job reserved (worker only)
	ocr     atomic.Bool     // the current step is text recognition
	cancel  atomic.Bool
	started time.Time
}

// errConvertCanceled stops a multi-page step when the user cancels; its
// output is discarded, not reported as a failure.
var errConvertCanceled = errors.New("canceled")

// converter is the module that runs conversion jobs and shows the running
// one in the sheet's left ear. One job at a time.
type converter struct {
	host *shelf
	job  *convertJob
	// cancelOpen reveals 取消 beside the chip's ring after a tap.
	cancelOpen bool
}

func (m *converter) busy() bool { return m.job != nil }

// tick redraws once a job has run long enough to show, and keeps the
// chip's progress moving while it does.
func (m *converter) tick(time.Time) {
	if m.live() && m.host.win != nil {
		m.host.win.Invalidate()
	}
}

func (m *converter) live() bool {
	return m.job != nil && time.Since(m.job.started) >= convertShowAfter
}

// chip is the running job's capsule: a 16pt progress ring whose tooltip
// carries 「正在转成 JPEG · 2/3」. Tapping the ring turns it into 取消;
// finished outputs are kept.
func (m *converter) chip(c *ui.Context) ui.Element {
	j := m.job
	if j == nil {
		return ui.Box(c).Size(0, 0)
	}
	l := loc(c)
	done, total := int(j.done.Load()), max(int(j.total.Load()), 1)
	frac := float32(done) / float32(total)
	title := l.ConvToFormat(strings.ToUpper(convertExt(j.target)))
	switch j.target {
	case toJPEG:
		title = l.ConvToJPEG()
	case toWord:
		title = l.ConvToWord()
	case toMergedPDF:
		title = l.ConvMergingPDF()
	case toZip:
		title = l.ConvZipping()
	case toLarge, toMedium, toSmall:
		title = l.ConvResizing()
	case toFit500, toFit1M, toFit2M, toFit5M:
		title = l.ConvCompressing()
	case toExtract:
		title = l.ConvExtracting()
	}
	step := l.ConvStep(min(done+1, total), total)
	if j.cancel.Load() {
		step = l.ConvCanceling()
	}
	tip := l.ConvChipTooltip(title, step)
	chip := ui.Row(c.Key("conv-chip")).Height(chipH).Radius(chipH/2).Background(fill1).
		Padding(0, 2, 0, 2).Gap(3).AlignItems(ui.Center).
		Label(title).Tooltip(tip)
	chip.Children(func() {
		if m.cancelOpen {
			b := ui.ButtonBase(c.Key("conv-chip-cancel")).Height(16).Radius(8).
				Padding(0, 5, 0, 5).Center().Label(l.ConvCancelLabel())
			if b.Hovered() {
				b.Background(fill2)
			}
			b.Children(func() {
				ui.Text(c, l.ConvCancel()).SingleLine().FontSize(9.5).TextColor(ink)
			})
			if b.Clicked() {
				j.cancel.Store(true)
				m.cancelOpen = false
			}
			return
		}
		ring := ui.ButtonBase(c.Key("conv-chip-ring")).Size(16, 16).Radius(8).Center().Label(tip)
		ring.Draw(func(p *ui.Painter, r ui.Rect) {
			cx, cy, rad := r.X+r.W/2, r.Y+r.H/2, r.W/2-1.5
			var track ui.Path
			track.Circle(cx, cy, rad)
			p.StrokePath(&track, 2, fill3)
			if frac > 0 {
				var arc ui.Path
				arc.Circle(cx, cy, rad).Trim(0, frac)
				p.StrokePath(&arc, 2, ink)
			}
		})
		if ring.Clicked() {
			m.cancelOpen = true
		}
	})
	return chip
}

// convertOut is one finished output (or failure) of a job.
type convertOut struct {
	src, path     string // the source (an archive, for 解压) and the output
	err           error
	noText        bool  // a Word output for which nothing was recognized
	before, after int64 // a resized image's source and output sizes
}

// convertTask is one output of a job: src converted to dst (page > 0:
// render that page of the PDF src); err is a failure found while planning.
type convertTask struct {
	src, dst string
	pdf      bool
	page     int
	err      error
}

// startConvert begins converting items — all images or all PDFs, or for
// toZip any files, folders and pictures — to target. Everything that
// touches the files, counting PDF pages included, runs on the worker so
// a large PDF never stalls the panel; outputs are named there too, which
// is safe because one job runs at a time.
func (a *shelf) startConvert(items []store.Item, target convertTarget) bool {
	if a.conv == nil || a.conv.busy() || a.store == nil || len(items) == 0 {
		return false
	}
	l := a.appLoc()
	dir := a.store.ConvertedDir()
	// Outputs are written straight into converted/; never into whatever a
	// symlink or stray file there points at.
	if fi, err := os.Lstat(dir); err != nil || !fi.IsDir() {
		a.report(errors.New(l.ConvNoConvertDir(dir)))
		return false
	}
	srcs := make([]string, len(items))
	names := make([]string, len(items))
	exts := make([]string, len(items))
	px := resizePx(target)
	for i, it := range items {
		srcs[i], names[i], exts[i] = it.Path, convertBaseName(it), convertExt(target)
		switch {
		case px > 0:
			names[i] = l.ConvResizeName(names[i], px)
			exts[i] = resizeExt(it)
		case fitLimit(target) > 0:
			names[i] = l.ConvCompressedName(names[i])
			exts[i] = "" // compressOutExt decides in planConvert (worker side)
		case target == toExtract:
			names[i] = archiveStem(l, it.Path)
		}
	}
	var roots []string
	if target == toZip {
		// Finder-like: 「报告.pdf.zip」, 「my.project 等 3 项.zip」 — a
		// file keeps its extension in the archive's name.
		roots = zipRoots(items)
		names[0] = roots[0]
		if len(items) > 1 {
			first := filepath.Base(items[0].Path)
			if items[0].Kind == store.KindImage {
				first = convertBaseName(items[0])
			}
			names[0] = l.ConvZipName(first, len(items))
		}
	}
	pdf := isPDF(items[0]) && target != toZip && target != toExtract
	unit := l.ConvUnitImages()
	switch {
	case target == toZip:
		unit = l.ConvUnitFiles()
	case target == toExtract:
		unit = l.ConvUnitItems()
	case pdf && target == toWord:
		unit = l.ConvUnitItems()
	case pdf:
		unit = l.ConvUnitPages()
	}
	job := &convertJob{target: target, pdf: pdf, unit: unit, started: time.Now()}
	job.total.Store(int32(len(items)))
	a.conv.job = job
	a.conv.cancelOpen = false
	convertWork(func() {
		tasks := planConvert(l, dir, srcs, names, exts, pdf, target, job)
		if target != toZip {
			job.total.Store(int32(len(tasks)))
		}
		format := mac.FormatPNG
		switch target {
		case toJPEG:
			format = mac.FormatJPEG
		case toHEIC:
			format = mac.FormatHEIC
		}
		var outs []convertOut
		for _, t := range tasks {
			if job.cancel.Load() {
				break
			}
			job.ocr.Store(false)
			out := convertOut{src: t.src, path: t.dst, err: t.err}
			switch {
			case out.err != nil:
			case target == toZip:
				out.err = writeZipIn(l, t.dst, srcs, roots, job)
			case target == toMergedPDF:
				out.err = imagesToPDF(srcs, t.dst)
			case t.pdf && target == toWord:
				out.noText, out.err = wordFromPDF(t.src, t.dst, job)
			case t.pdf:
				out.err = renderPDFPage(t.src, t.page, t.dst, format)
			case target == toPDF:
				out.err = imagesToPDF([]string{t.src}, t.dst)
			case target == toWord:
				job.ocr.Store(true)
				out.noText, out.err = wordFromImage(t.src, t.dst)
			case px > 0:
				out.err = resizeImage(t.src, t.dst, extFormat(strings.TrimPrefix(filepath.Ext(t.dst), ".")), px)
				out.before, out.after = fileSize(t.src), fileSize(t.dst)
			case fitLimit(target) > 0:
				over, ferr := fitImage(t.src, t.dst, compressFormat(t.dst), fitLimit(target))
				out.err = ferr
				if over {
					job.over.Add(1)
				}
				out.before, out.after = fileSize(t.src), fileSize(t.dst)
			case target == toExtract:
				out.path, out.err = extractArchive(l, t.src, t.dst, job)
			default:
				out.err = convertImage(t.src, t.dst, format)
			}
			if errors.Is(out.err, errConvertCanceled) {
				break
			}
			outs = append(outs, out)
			if target != toZip { // the archive counts its own entries
				job.done.Add(1)
			}
		}
		convertDispatch(func() { a.finishConvert(job, outs) })
	})
	return true
}

// fileSize is p's size in bytes, 0 when it cannot be read.
func fileSize(p string) int64 {
	fi, err := os.Stat(p)
	if err != nil {
		return 0
	}
	return fi.Size()
}

// formatBytes renders a size the way Finder does (decimal units): 「812 KB」,
// 「4.2 MB」, with the byte count in the language of l.
func formatBytes(l locales.Messages, n int64) string {
	switch {
	case n >= 1e9:
		return fmt.Sprintf("%.1f GB", float64(n)/1e9)
	case n >= 1e6:
		return fmt.Sprintf("%.1f MB", float64(n)/1e6)
	case n >= 1e3:
		return fmt.Sprintf("%.0f KB", float64(n)/1e3)
	}
	return l.ConvBytes(int(n))
}

// zipRoots names each item at an archive's root: a file or folder by its
// own name, a screenshot by its title as a .png; a name already taken
// becomes 「名 2.ext」.
func zipRoots(items []store.Item) []string {
	roots := make([]string, len(items))
	taken := map[string]bool{}
	for i, it := range items {
		name := filepath.Base(it.Path)
		if it.Kind == store.KindImage {
			name = convertBaseName(it) + ".png"
		}
		stem, ext := name, filepath.Ext(name)
		if ext != name {
			stem = strings.TrimSuffix(name, ext)
		} else {
			ext = ""
		}
		candidate := name
		for n := 2; taken[strings.ToLower(candidate)]; n++ {
			candidate = fmt.Sprintf("%s %d%s", stem, n, ext)
		}
		taken[strings.ToLower(candidate)] = true
		roots[i] = candidate
	}
	return roots
}

// planConvert lists a job's outputs with their reserved names: one per
// source (named names[i].exts[i]), one merged PDF or archive, or for a
// PDF turned into images one per page (「名 第 2 页.png」, just 「名.png」
// for a one-page PDF). A PDF whose pages cannot be counted becomes a
// failed task. Counting stops when the job is cancelled.
func planConvert(l locales.Messages, dir string, srcs, names, exts []string, pdf bool, target convertTarget, job *convertJob) []convertTask {
	taken := map[string]bool{}
	job.taken = taken
	var tasks []convertTask
	switch {
	case target == toZip:
		return []convertTask{{dst: uniquePath(dir, names[0], "zip", taken)}}
	case target == toMergedPDF:
		name := names[0]
		if len(srcs) > 1 {
			name = l.ConvMergedName(name, len(srcs))
		}
		return []convertTask{{dst: uniquePath(dir, name, "pdf", taken)}}
	case target == toExtract:
		for i, src := range srcs {
			tasks = append(tasks, convertTask{src: src, dst: uniqueDir(dir, names[i], taken)})
		}
	case fitLimit(target) > 0:
		for i, src := range srcs {
			if job.cancel.Load() {
				break
			}
			ext := compressOutExt(src)
			if fi, err := os.Stat(src); err != nil {
				tasks = append(tasks, convertTask{src: src, dst: filepath.Join(dir, names[i]+"."+ext), err: err})
			} else if fi.Size() <= fitLimit(target) {
				job.skipped.Add(1) // already under the budget: no output
			} else {
				tasks = append(tasks, convertTask{src: src, dst: uniquePath(dir, names[i], ext, taken)})
			}
		}
	case pdf && target == toWord:
		for i, src := range srcs {
			tasks = append(tasks, convertTask{src: src, pdf: true, dst: uniquePath(dir, names[i], "docx", taken)})
		}
	case pdf:
		for i, src := range srcs {
			if job.cancel.Load() {
				break
			}
			n, err := pdfPageCount(src)
			if err == nil && n < 1 {
				err = errors.New(l.ConvNoPages(filepath.Base(src)))
			}
			if err != nil {
				tasks = append(tasks, convertTask{src: src, pdf: true, dst: filepath.Join(dir, names[i]), err: err})
				continue
			}
			for p := 1; p <= n; p++ {
				name := names[i]
				if n > 1 {
					name = l.ConvPageName(names[i], p)
				}
				tasks = append(tasks, convertTask{src: src, pdf: true, page: p, dst: uniquePath(dir, name, convertExt(target), taken)})
			}
		}
	default:
		for i, src := range srcs {
			tasks = append(tasks, convertTask{src: src, dst: uniquePath(dir, names[i], exts[i], taken)})
		}
	}
	return tasks
}

// wordFromPDF writes dst as a text-only Word document with one page per
// page of the PDF src: the page's own text, or for a scanned page without
// a text layer, the text recognized in its picture. Cancelling stops it
// between pages with errConvertCanceled. It reports whether no page had
// any text.
func wordFromPDF(src, dst string, job *convertJob) (noText bool, err error) {
	pages, err := pdfText(src)
	if err != nil {
		return false, err
	}
	noText = true
	for i, lines := range pages {
		if job.cancel.Load() {
			return false, errConvertCanceled
		}
		if len(lines) == 0 {
			job.ocr.Store(true)
			tmp := filepath.Join(filepath.Dir(dst), fmt.Sprintf(".%s.page%d.png", filepath.Base(dst), i+1))
			err := renderPDFPage(src, i+1, tmp, mac.FormatPNG)
			if err == nil {
				lines, err = recognizeImageText(tmp)
			}
			os.Remove(tmp)
			job.ocr.Store(false)
			if err != nil {
				return false, err
			}
			pages[i] = normalizeLines(lines)
		}
		if len(pages[i]) > 0 {
			noText = false
		}
	}
	if job.cancel.Load() {
		return false, errConvertCanceled
	}
	return noText, writeDocxPages(dst, pages)
}

// normalizeLines maps recognized lines to ordinary characters (Vision can
// return CJK compatibility forms such as 「⼯」 for 「工」).
func normalizeLines(lines []string) []string {
	for i, l := range lines {
		lines[i] = mac.NormalizeText(l)
	}
	return lines
}

// wordFromImage writes dst as a Word document holding the text recognized
// in src and, below it, the picture itself (upright, as PNG). It reports
// whether no text was found.
func wordFromImage(src, dst string) (noText bool, err error) {
	lines, err := recognizeImageText(src)
	if err != nil {
		return false, err
	}
	lines = normalizeLines(lines)
	w, h, err := imageSize(src)
	if err != nil {
		return false, err
	}
	// The picture goes in as an upright PNG: Word does not reliably honour
	// a JPEG's orientation tag, and HEIC/WebP cannot be embedded at all.
	tmp := filepath.Join(filepath.Dir(dst), "."+filepath.Base(dst)+".png")
	defer os.Remove(tmp)
	if err := convertImage(src, tmp, mac.FormatPNG); err != nil {
		return false, err
	}
	data, err := os.ReadFile(tmp)
	if err != nil {
		return false, err
	}
	return len(lines) == 0, writeDocx(dst, lines, &docx.Picture{Data: data, Ext: "png", W: w, H: h})
}

// finishConvert adds a job's outputs to the front of the tray, selects
// them and says how it went.
func (a *shelf) finishConvert(job *convertJob, outs []convertOut) {
	if a.conv != nil && a.conv.job == job {
		a.conv.job = nil
	}
	if a.quitting {
		return
	}
	var paths []string
	failed, noText := 0, 0
	var firstErr error
	for _, o := range outs {
		if o.err != nil {
			failed++
			if firstErr == nil {
				firstErr = o.err
			}
			verb, name := "转换失败", filepath.Base(o.path)
			if job.target == toExtract {
				verb, name = "解压失败", filepath.Base(o.src)
			}
			log.Printf("ShelfU: %s %s：%v", verb, name, o.err)
			continue
		}
		if o.noText {
			noText++
		}
		paths = append(paths, o.path)
	}
	if len(paths) > 0 {
		var ids []string
		var err error
		if job.target == toExtract {
			ids, err = addExtracted(a.store, paths)
		} else {
			ids, err = a.store.AddConverted(paths)
		}
		if err != nil {
			for _, p := range paths {
				if job.target == toExtract {
					// Extracted folders are this job's own new results.
					_ = os.RemoveAll(p)
				} else {
					_ = os.Remove(p)
				}
			}
			a.report(err)
			return
		}
		if a.tab != pageTray {
			a.setTab(pageTray)
		}
		a.selection.onlyAll(ids)
	}
	l := a.appLoc()
	switch {
	case len(paths) == 0 && failed > 0:
		a.report(errors.New(convertFailText(l, job.target, firstErr)))
	case failed > 0:
		a.toastStatus(l.ConvPartial(len(paths), failed), time.Now().Add(4*time.Second), statusErr)
	case job.cancel.Load():
		a.toastStatus(l.ConvCanceledDone(len(paths)), time.Now().Add(3*time.Second), statusInfo)
	case noText > 0 && noText == len(paths) && job.pdf:
		a.toastStatus(l.ConvNoTextPDF(), time.Now().Add(4*time.Second), statusInfo)
	case noText > 0 && noText == len(paths):
		a.toastStatus(l.ConvNoTextImage(), time.Now().Add(4*time.Second), statusInfo)
	case job.target == toZip:
		a.toastStatus(l.ConvZipDone(formatBytes(l, fileSize(paths[0]))), time.Now().Add(3*time.Second), statusOK)
	case job.target == toExtract:
		a.toastStatus(l.ConvExtractDone(len(paths)), time.Now().Add(3*time.Second), statusOK)
	case fitLimit(job.target) > 0:
		var before, after int64
		for _, o := range outs {
			before, after = before+o.before, after+o.after
		}
		skipped, over := int(job.skipped.Load()), int(job.over.Load())
		sizes := "no"
		if len(paths) > 0 {
			sizes = "yes"
		}
		msg := l.ConvCompressDone(len(paths), skipped, over,
			formatBytes(l, fitLimit(job.target)), sizes,
			formatBytes(l, before), formatBytes(l, after))
		kind := statusOK
		if over > 0 {
			kind = statusErr
		}
		a.toastStatus(msg, time.Now().Add(5*time.Second), kind)
	case resizePx(job.target) > 0:
		var before, after int64
		for _, o := range outs {
			before, after = before+o.before, after+o.after
		}
		a.toastStatus(l.ConvResizeDone(len(paths), formatBytes(l, before), formatBytes(l, after)), time.Now().Add(4*time.Second), statusOK)
	case len(paths) > 0:
		a.toastStatus(l.ConvConverted(len(paths)), time.Now().Add(3*time.Second), statusOK)
	}
	a.syncWindowFrame()
	if a.win != nil {
		a.win.Invalidate()
	}
	a.syncCopyKey()
}

// convertFailText words a job that produced nothing, in the language of l:
// the operation the job was doing names the failure.
func convertFailText(l locales.Messages, target convertTarget, err error) string {
	switch {
	case target == toExtract:
		return l.ConvFailedExtract(err.Error())
	case target == toZip, fitLimit(target) > 0:
		return l.ConvFailedZip(err.Error())
	}
	return l.ConvFailed(err.Error())
}

// uniquePath is dir/name.ext, or 「name 2.ext」, 「name 3.ext」… when that
// file exists or another output of the same job took it.
func uniquePath(dir, name, ext string, taken map[string]bool) string {
	for n := 1; ; n++ {
		candidate := name
		if n > 1 {
			candidate = fmt.Sprintf("%s %d", name, n)
		}
		p := filepath.Join(dir, candidate+"."+ext)
		if taken[p] {
			continue
		}
		if _, err := os.Lstat(p); err == nil {
			continue
		}
		taken[p] = true
		return p
	}
}

// convertMenu adds the conversion group to an item menu. Every target an
// image, or every target a PDF — a mixed selection is never half
// converted: images get 「转换为 ▸」 (「转换 N 张为 ▸」) JPEG / HEIC / PNG /
// PDF / Word, the format every target already has greyed as 「当前格式」,
// for several 「合并为一个 PDF」, and 「缩小图片 ▸」 by longer side, a size
// no target exceeds greyed, then 「压缩到 ▸」 by byte budget; PDFs get
// 「转换为 ▸」 (「转换 N 个为 ▸」) Word, PNG, JPEG. Archives get 「解压」,
// and any files, folders and pictures still on disk get 「压缩为 zip」.
// Everything is disabled while a job runs.
func (a *shelf) convertMenu(m *ui.Menu, items []store.Item) {
	if a.conv == nil || a.store == nil || len(items) == 0 {
		return
	}
	l := a.appLoc()
	allImages, allPDFs, allFiles, allArchives := true, true, true, true
	for _, it := range items {
		allImages = allImages && convertible(it)
		allPDFs = allPDFs && isPDF(it)
		allFiles = allFiles && !it.Missing && (it.Kind == store.KindFile || it.Kind == store.KindImage)
		allArchives = allArchives && isArchive(it)
	}
	if !allFiles {
		return
	}
	busy := a.conv.busy()
	m.Separator()
	switch {
	case allPDFs:
		label := l.ConvMenuConvert()
		if len(items) > 1 {
			label = l.ConvMenuConvertItems(len(items))
		}
		m.Submenu(label, func(sm *ui.Menu) {
			if sm.Item(l.ConvMenuPDFWord()).Disabled(busy).Chosen() {
				a.startConvert(items, toWord)
			}
			sm.Separator()
			if sm.Item(l.ConvMenuPDFPNG()).Disabled(busy).Chosen() {
				a.startConvert(items, toPNG)
			}
			if sm.Item(l.ConvMenuPDFJPEG()).Disabled(busy).Chosen() {
				a.startConvert(items, toJPEG)
			}
		}).Disabled(busy)
	case allImages:
		a.imageConvertMenu(m, items, busy)
	}
	if allArchives {
		label := l.ConvMenuExtract()
		if len(items) > 1 {
			label = l.ConvMenuExtractN(len(items))
		}
		if m.Item(label).Disabled(busy).Chosen() {
			a.startConvert(items, toExtract)
		}
	}
	label := l.ConvMenuZip()
	if len(items) > 1 {
		label = l.ConvMenuZipN(len(items))
	}
	if m.Item(label).Disabled(busy).Chosen() {
		a.startConvert(items, toZip)
	}
}

// imageConvertMenu is convertMenu's group for pictures.
func (a *shelf) imageConvertMenu(m *ui.Menu, items []store.Item, busy bool) {
	l := a.appLoc()
	current := imageFormatOf(items[0])
	for _, it := range items[1:] {
		if imageFormatOf(it) != current {
			current = ""
		}
	}
	label := l.ConvMenuConvert()
	if len(items) > 1 {
		label = l.ConvMenuConvertImages(len(items))
	}
	m.Submenu(label, func(sm *ui.Menu) {
		for _, f := range []struct {
			t     convertTarget
			label string
		}{{toJPEG, "JPEG"}, {toHEIC, "HEIC"}, {toPNG, "PNG"}} {
			name, same := f.label, convertExt(f.t) == current
			if same {
				name = l.ConvMenuCurrent(name)
			}
			if sm.Item(name).Disabled(busy || same).Chosen() {
				a.startConvert(items, f.t)
			}
		}
		sm.Separator()
		if sm.Item("PDF").Disabled(busy).Chosen() {
			a.startConvert(items, toPDF)
		}
		if sm.Item(l.ConvMenuWord()).Disabled(busy).Chosen() {
			a.startConvert(items, toWord)
		}
	}).Disabled(busy)
	if len(items) > 1 {
		if m.Item(l.ConvMenuMergePDF()).Disabled(busy).Chosen() {
			a.startConvert(items, toMergedPDF)
		}
	}
	// The longest side among the targets: a size it does not exceed would
	// only re-encode, so that choice is greyed.
	longest := 0
	for _, it := range items {
		if w, h, err := imageSize(it.Path); err == nil {
			longest = max(longest, w, h)
		}
	}
	label = l.ConvMenuResize()
	if len(items) > 1 {
		label = l.ConvMenuResizeN(len(items))
	}
	m.Submenu(label, func(sm *ui.Menu) {
		for _, f := range []struct {
			t     convertTarget
			label string
		}{{toLarge, l.ConvMenuLarge()}, {toMedium, l.ConvMenuMedium()}, {toSmall, l.ConvMenuSmall()}} {
			px := resizePx(f.t)
			name := l.ConvMenuTier(f.label, px)
			if sm.Item(name).Disabled(busy || longest <= px).Chosen() {
				a.startConvert(items, f.t)
			}
		}
	}).Disabled(busy || longest <= resizePx(toSmall))
	a.compressMenu(m, items, busy)
}
