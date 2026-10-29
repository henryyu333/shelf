//go:build darwin

package main

import (
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode"

	"github.com/egoist/mygo/i18n"
	"github.com/egoist/mygo/transfer"
	"github.com/egoist/mygo/ui"

	"shelf/internal/mac"
	"shelf/internal/store"
	"shelf/locales"
)

// Item rows come in two shapes: itemTile is the tray tab's 5-column grid
// tile (thumbnail over the name), clipRow the clipboard tab's list row
// (16pt source-app icon, text, 「应用 · 时间」 on the right). Both keep the same
// selection, click, context menu and drag-out behaviour.

// itemTile is one cell of the tray grid: a 40pt thumbnail (the stored
// image, or the file's Finder icon), the name under it, a pin badge top
// right and the accent ring while selected. Moved files dim to 38% and
// name 「· 已移动」.
func (a *shelf) itemTile(c *ui.Context, item store.Item) {
	l := loc(c)
	title := itemTitleIn(l, item)
	name := title
	if item.Missing {
		name = l.ClipMovedName(title)
	}
	tile := ui.Column(c.Key(item.ID)).Height(tileH).Radius(12).AlignItems(ui.Center).
		Padding(6, 0, 0, 0).Label(name)
	if a.reveal == item.ID {
		// 键盘移动选中行后把它留在可见范围内。
		tile.ScrollIntoView()
	}
	selected := a.selection.has(item.ID)
	switch {
	case selected:
		tile.Background(accent16).Border(1.5, accent.Alpha(0.9))
	case tile.Hovered():
		tile.Background(fill1)
	}
	if !item.Missing {
		tile.DragDataFrom(func() transfer.Data {
			// 本应用发起的拖出会话：拖拽监视据此抑制摇晃召唤与篮子。
			a.beginOwnDrag()
			a.takeServiceOwnership()
			if a.win != nil {
				a.win.Invalidate() // 下一帧撤掉自身的放入目标
			}
			a.dragOutIDs = a.dragOutIDs[:0]
			for _, it := range a.dragItems(item) {
				a.dragOutIDs = append(a.dragOutIDs, it.ID)
			}
			return a.dragOut(item)
		}, transfer.DragOptions{Operations: transfer.Copy, Done: a.completed})
	}
	tile.ContextMenu(func(m *ui.Menu) { a.itemMenu(m, item) })
	tile.Children(func() {
		a.tileThumb(c, item)
		ui.Text(c, name).SingleLine().TextAlign(ui.Center).FontSize(10.5).
			TextColor(ink.Alpha(0.86)).Margin(5, 4, 0, 4).MaxWidth(70)
		if item.Pinned {
			// 右上 16pt 黑底图钉角标：作为 tile 的子元素定位，不能放到
			// 网格层（否则所有角标叠在网格右上角并抢走点击）。
			ui.Row(c.Key("pin")).Absolute().Top(6).Right(10).Size(16, 16).Radius(8).Center().
				Background(surface).Label(l.ClipPinned()).
				Children(func() {
					ui.Icon(c, pinFill).Size(9, 9).TextColor(ink)
				})
		}
	})
	if item.Missing {
		tile.Opacity(0.38)
	}
	if tile.Clicked() {
		mods := tile.ClickModifiers()
		// 显示顺序来自当前标签（两个标签都是置顶在前），⇧ 选择跟着它走。
		a.selection.click(a.visibleItems(), item.ID, mods)
		a.reveal = ""
		if mods&(ui.Cmd|ui.Shift) == 0 && item.Kind == store.KindText {
			a.copyText(item.Text)
		}
		if a.win != nil {
			a.win.Invalidate()
		}
	}
}

// tileThumb draws the tile's 40pt thumbnail: the stored image (rounded,
// shadowed), the file's system icon, or for text a fill2 note card showing
// the first lines of the text (DESIGN review: no bare glyph).
func (a *shelf) tileThumb(c *ui.Context, item store.Item) {
	box := ui.Row(c.Key("thumbnail")).Size(tileArt, tileArt).Center()
	if item.Kind == store.KindText {
		// itemTitle 给出有界预览（折叠空白、160 字截断），完整文字仍归
		// 保存、复制与拖出用。
		text := itemTitle(item)
		if text == "" {
			box.Radius(8).Background(fill2).Children(func() {
				ui.Icon(c, textSymbol).Size(18, 18).TextColor(ink3)
			})
			return
		}
		box.Radius(8).Background(fill2).AlignItems(ui.Start).Justify(ui.Start).Clip().Children(func() {
			ui.Text(c, text).MaxLines(2).FontSize(7.5).TextColor(ink2).Padding(4, 4, 0, 4)
		})
		return
	}
	box.Children(func() {
		defer formatBadge(c, item)
		if thumb := a.thumbnail(item); thumb != nil {
			ext := strings.ToLower(filepath.Ext(item.Path))
			im := ui.Image(c.Key("bmp"), thumb).Size(tileArt, tileArt).Fit(ui.Cover)
			if item.Kind == store.KindImage || imageExts[ext] {
				// 图片缩略图：圆角 + 阴影，像 Finder 里的预览。
				im.Radius(8).Shadow(0, 2, 8, 0, ui.RGBA(0, 0, 0, 0.45))
			} else {
				// 文件/文件夹用系统图标：图标自带形状，不再裁圆角。
				im.Radius(0).Fit(ui.Contain)
			}
			return
		}
		if item.Kind == store.KindImage {
			ui.Icon(c, fileSymbol).Size(26, 26).TextColor(ink3)
			return
		}
		ui.Icon(c, fileSymbol).Size(30, 30).TextColor(ink3)
	})
}

// formatBadge marks a converted picture's new format — 「JPG」, 「HEIC」,
// 「PNG」 — at the thumbnail's bottom-left corner, so a converted copy reads
// apart from its original. Documents carry their kind in their icon.
func formatBadge(c *ui.Context, item store.Item) {
	if !item.Converted || item.Missing {
		return
	}
	ext := strings.ToUpper(strings.TrimPrefix(filepath.Ext(item.Path), "."))
	switch ext {
	case "JPG", "PNG", "HEIC":
	default:
		return
	}
	ui.Row(c.Key("format")).Absolute().Left(-4).Bottom(-4).Height(14).Padding(0, 4, 0, 4).Radius(4).
		Center().Background(surfaceRaise).Border(1, surface).Children(func() {
		ui.Text(c, ext).SingleLine().FontSize(8.5).FontWeight(700).TextColor(ink)
	})
}

// clipRow is one clipboard-tab list row: 30pt, the source app's icon (or
// a text glyph), the captured text single-line, 「应用 · 相对时间」 on the
// right, the pin mark on pinned rows and 「↩ 复制」 on the selected one
// while searching.
func (a *shelf) clipRow(c *ui.Context, item store.Item) {
	l := tloc(c)
	title := itemTitleIn(l, item)
	row := ui.Row(c.Key(item.ID)).Height(clipRowH).Padding(0, 8, 0, 8).Gap(8).Radius(8).
		AlignItems(ui.Center).Label(title)
	if a.reveal == item.ID {
		row.ScrollIntoView()
	}
	selected := a.selection.has(item.ID)
	switch {
	case selected:
		row.Background(accent24)
	case row.Hovered():
		row.Background(fill1)
	}
	if !item.Missing {
		row.DragDataFrom(func() transfer.Data {
			a.beginOwnDrag()
			a.takeServiceOwnership()
			if a.win != nil {
				a.win.Invalidate() // 下一帧撤掉自身的放入目标
			}
			a.dragOutIDs = nil // 剪贴板页是历史：拖出不移除
			return a.dragOut(item)
		}, transfer.DragOptions{Operations: transfer.Copy, Done: a.completed})
	}
	row.ContextMenu(func(m *ui.Menu) { a.itemMenu(m, item) })
	row.Children(func() {
		a.clipRowIcon(c, item)
		text := title
		if item.Kind == store.KindImage {
			text = l.ClipScreenshot(stampIn(c.Locale(), c.Region(), item.CreatedAt.Local()))
		}
		ui.Text(c, text).SingleLine().FontSize(12).TextColor(ink).Grow(1).MinWidth(0)
		if selected && a.focusMode {
			ui.Text(c, l.ClipCopyHint()).SingleLine().FontSize(10.5).TextColor(ink3)
		}
		if item.Pinned {
			ui.Icon(c.Key("pin"), pinFill).Size(10, 10).TextColor(ink.Alpha(0.5)).Label(l.ClipPinned())
		}
		ui.Text(c, a.clipRowMeta(l, item)).SingleLine().FontSize(10.5).TextColor(ink3)
	})
	if row.Clicked() {
		mods := row.ClickModifiers()
		a.selection.click(a.visibleItems(), item.ID, mods)
		a.reveal = ""
		if mods&(ui.Cmd|ui.Shift) == 0 && item.Kind == store.KindText {
			a.copyText(item.Text)
		}
		if a.win != nil {
			a.win.Invalidate()
		}
	}
}

// clipRowIcon is the row's 16pt source-app icon, cached by bundle id, or
// the kind's glyph when no app is known.
func (a *shelf) clipRowIcon(c *ui.Context, item store.Item) {
	if item.SourceApp == clipSourceOCR {
		// ShelfU's own recognition has no app: the 框选文字 glyph says where
		// the words came from, like the tools card that produced them.
		ui.Box(c).Size(16, 16).Radius(4).Center().Background(fill2).Children(func() {
			ui.Icon(c, ocrSymbol).Size(10, 10).TextColor(ink3)
		})
		return
	}
	if item.SourceApp != "" {
		if icon := a.clipAppIcon(item.SourceApp); icon != nil {
			ui.Image(c.Key("ai-"+item.SourceApp), icon).Size(16, 16)
			return
		}
	}
	icon := textSymbol
	if item.Kind == store.KindImage {
		icon = fileSymbol
	}
	ui.Box(c).Size(16, 16).Radius(4).Center().Background(fill2).Children(func() {
		ui.Icon(c, icon).Size(10, 10).TextColor(ink3)
	})
}

// clipRowMeta is the row's 「应用 · 相对时间」 line.
func (a *shelf) clipRowMeta(l locales.Messages, item store.Item) string {
	ago := relativeTime(l, item.CreatedAt, time.Now())
	if item.SourceApp == "" {
		return ago
	}
	return l.ClipMeta(a.appName(item.SourceApp), ago)
}

// clipAppIcon caches mac.AppIcon bitmaps per bundle id so a long list
// never asks the workspace twice for the same app. The cache is bounded:
// past appIconCacheLimit entries it is rebuilt on demand — clipboard rows
// re-resolve their icons on the next paint, apps gone from the list are
// simply never asked for again.
func (a *shelf) clipAppIcon(bundle string) *ui.Bitmap {
	if a.appIcons == nil {
		a.appIcons = make(map[string]*ui.Bitmap)
	}
	if b, ok := a.appIcons[bundle]; ok {
		return b
	}
	if len(a.appIcons) >= appIconCacheLimit {
		a.appIcons = make(map[string]*ui.Bitmap)
	}
	var b *ui.Bitmap
	if png := mac.AppIcon(bundle, 32); len(png) > 0 {
		if decoded, err := ui.DecodeBitmap(png); err == nil {
			b = decoded
		}
	}
	a.appIcons[bundle] = b
	return b
}

// appIconCacheLimit caps the source-app icon cache; rows re-resolve on
// the next paint after a flush, so a cap costs at most one repaint's
// worth of lookups.
const appIconCacheLimit = 64

// relativeTime renders the clipboard row's 「· 相对时间」: 刚刚 / N 分钟前 /
// 今天 15:04 / 昨天 / N 天前 / M月D日, in the language of l — the clock
// and the calendar follow the message's language.
func relativeTime(l locales.Messages, t, now time.Time) string {
	if t.IsZero() {
		return ""
	}
	local := t.Local()
	d := now.Sub(local)
	switch {
	case d < time.Minute:
		return l.ClipJustNow()
	case d < time.Hour:
		return l.ClipMinutesAgo(int(d.Minutes()))
	case d < 24*time.Hour && local.Day() == now.Day() && local.Month() == now.Month():
		return l.ClipTodayAt(local)
	case d < 48*time.Hour:
		return l.ClipYesterday()
	case d < 7*24*time.Hour:
		return l.ClipDaysAgo(int(d.Hours() / 24))
	default:
		return l.ClipDate(local)
	}
}

// itemTitle is itemTitleIn in the language the app shows, for the
// surfaces outside a view (home widgets, tool cards, drag-out names).
func itemTitle(item store.Item) string { return itemTitleIn(locales.In(i18n.Locale()), item) }

// itemTitleIn is the item's title — a text preview, a screenshot's name or
// the file's own — in the language of l.
func itemTitleIn(l locales.Messages, item store.Item) string {
	if item.Kind == store.KindText {
		// 只缩短显示摘要；保存、复制和拖出仍使用完整文字。
		const limit = 160
		var preview strings.Builder
		preview.Grow(min(len(item.Text), limit*3))
		count, space := 0, false
		for _, r := range item.Text {
			if unicode.IsSpace(r) {
				space = count > 0
				continue
			}
			if count >= limit || space && count+1 >= limit {
				preview.WriteRune('…')
				break
			}
			if space {
				preview.WriteByte(' ')
				count++
				space = false
			}
			preview.WriteRune(r)
			count++
		}
		return preview.String()
	}
	if item.Kind == store.KindImage {
		return l.ClipScreenshot(item.CreatedAt.Local().Format("01-02 15:04:05"))
	}
	return filepath.Base(item.Path)
}

func (a *shelf) dragData(item store.Item) transfer.Data {
	if item.Kind == store.KindText {
		return transfer.TextData(item.Text)
	}
	if _, err := os.Stat(item.Path); err != nil {
		a.store.Refresh()
		a.report(errors.New(a.appLoc().StatusFileGone(err.Error())))
		return transfer.Data{}
	}
	path := item.Path
	if item.Kind == store.KindImage {
		// 托管截图以内容哈希命名（<sha>.png）；拖出时用托盘里显示的可读
		// 名字，原文件保持不变。
		path = a.dragOutPath(item)
	}
	files, err := transfer.FileData(path)
	if err != nil {
		a.report(err)
		return transfer.Data{}
	}
	if item.Kind != store.KindImage {
		return files
	}
	// 一个对象的原生文件 URL 和 PNG 双表示：Finder 接受文件，编辑器接受图片。
	png, err := os.ReadFile(item.Path)
	if err != nil {
		a.report(err)
		return transfer.Data{}
	}
	itemReps := files.Items()[0]
	var reps []transfer.Representation
	for _, format := range itemReps.Formats() {
		b, err := itemReps.Read(format)
		if err != nil {
			a.report(err)
			return transfer.Data{}
		}
		reps = append(reps, transfer.Bytes(format, b))
	}
	reps = append(reps, transfer.Bytes(transfer.PNG, png))
	return transfer.New(transfer.NewItem(reps...))
}

// dragOutDir is the scratch folder under the data directory that holds
// readable-name links for dragged-out pictures.
const dragOutDir = "drag-out"

// dragOutLife is how long a drag-out link stays before the next drag
// clears it: long past any receiver's read, short enough not to pile up.
const dragOutLife = time.Hour

// dragOutName is the file name a dragged-out picture is offered under: the
// name the tray shows, with the screenshot time's colons written as dots,
// the way macOS names screenshots (「截图 10-19 20.07.41.png」).
func dragOutName(item store.Item) string {
	name := strings.ReplaceAll(itemTitle(item), ":", ".")
	if !strings.HasSuffix(strings.ToLower(name), ".png") {
		name += ".png"
	}
	return name
}

// dragOutPath materialises a readable file name for item in
// <dataDir>/drag-out: a copy with its own inode and mtime — never a hard
// link, which shares the managed file's mtime and would make every link
// older than dragOutLife look stale at once, and a Chtimes fix-up would
// bump the managed original. Files older than dragOutLife are cleared
// once per drag by dragOut. Every failure falls back to the managed
// path — a drag must still work.
func (a *shelf) dragOutPath(item store.Item) string {
	if a.dataDir == "" {
		return item.Path
	}
	dir := filepath.Join(a.dataDir, dragOutDir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return item.Path
	}
	name := dragOutName(item)
	candidate := filepath.Join(dir, name)
	// Dragging the same picture again reuses its copy instead of piling
	// up " 2" names. Content identity, not inode identity — the copy is a
	// separate file from the managed original.
	if sameBytes(candidate, item.Path) {
		return candidate
	}
	dst := uniquePath(dir, strings.TrimSuffix(name, ".png"), "png", map[string]bool{})
	if err := copyFile(item.Path, dst); err != nil {
		return item.Path
	}
	return dst
}

// sameBytes reports whether both paths hold the same bytes: the scratch
// copy of the same screenshot, but never a different one.
func sameBytes(a, b string) bool {
	ab, err := os.ReadFile(a)
	if err != nil {
		return false
	}
	bb, err := os.ReadFile(b)
	return err == nil && bytes.Equal(ab, bb)
}

// cleanStaleDragOuts removes drag-out files last modified before the
// cutoff; anything newer may belong to a drag still in flight.
func cleanStaleDragOuts(dir string, before time.Time) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, e := range entries {
		info, err := e.Info()
		if err != nil || !info.Mode().IsRegular() || !info.ModTime().Before(before) {
			continue
		}
		_ = os.Remove(filepath.Join(dir, e.Name()))
	}
}

// copyFile copies src to a new dst with its own inode and a fresh mtime,
// which is exactly what keeps the drag-out cleanup from treating the
// readable name as old. A partial copy is removed so a later drag never
// offers a truncated file.
func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		os.Remove(dst)
		return err
	}
	if err := out.Close(); err != nil {
		os.Remove(dst)
		return err
	}
	return nil
}
