//go:build darwin

package main

// 总览：点刘海直接到的那一页（440 × 156，内容 416 × 106）。左列是状态文字和
// 海獭（静态图，只在状态之间换图），右列是「最近放入」五项和一行「当前最相关
// 的事」——一次只显示一件，按优先级从进行中的任务排到剪贴板条数。下面没有再
// 一级岛：页面图标在顶栏，点一下就长成 440 × 250 的页面。

import (
	"time"

	"github.com/egoist/mygo/transfer"
	"github.com/egoist/mygo/ui"

	"shelf/internal/mac"
	"shelf/internal/store"
)

const (
	overviewLeftW  = 104 // 左列：状态文字 + 居中的海獭
	overviewColGap = 10
	overviewHeadH  = 16 // 「最近放入」小标题
	overviewTileH  = 52
	overviewTileN  = 5
	overviewCtxH   = 30
	overviewOtterH = 74
	// otterSleepyAfter is how long without any use makes the otter sleepy.
	otterSleepyAfter = 10 * time.Minute
	// otterHappyFor is how long the otter stays happy after good news.
	otterHappyFor = 1600 * time.Millisecond
)

// overviewPage is the first page of the panel: what a single click on the
// notch shows.
type overviewPage struct {
	host *shelf
}

func (p *overviewPage) id() pageID    { return pageOverview }
func (p *overviewPage) label() string { return p.host.appLoc().OverviewTitle() }
func (p *overviewPage) icon(c *ui.Context, _ bool) ui.Element {
	return ui.Icon(c, overviewNavSymbol)
}
func (p *overviewPage) enabled() bool                      { return true }
func (p *overviewPage) title() string                      { return p.host.appLoc().OverviewTitle() }
func (p *overviewPage) actions(c *ui.Context) []ui.Element { return nil }

// body is the page interface's entry: the overview's own body.
func (p *overviewPage) body(c *ui.Context, over bool, _ bool) ui.Element {
	p.host.overviewBody(c, over)
	return ui.Box(c).Size(0, 0)
}

// overviewBody draws the 416 × 106 body.
func (a *shelf) overviewBody(c *ui.Context, _ bool) {
	if a.welcomeShown(c.Now()) {
		a.welcomeCard(c)
		return
	}
	if a.whatsNewShown(c.Now()) {
		a.whatsNewCard(c)
		return
	}
	a.overviewDeadlines(c)
	ui.Row(c.Key("overview-row")).Width(contentW).Height(overviewBodyH).Gap(overviewColGap).Children(func() {
		a.overviewMeta(c)
		ui.Column(c.Key("overview-right")).Grow(1).MinWidth(0).Gap(4).Children(func() {
			a.overviewRecent(c)
			a.overviewContext(c)
		})
	})
}

// overviewDeadlines works out when the overview's own words change: a copy
// ages out after two minutes, the otter's happiness ends, the quiet spell
// makes it sleepy, and a status holds only for its span. The monitor tick
// checks the deadline (tickOverviewDeadlines) instead of arming a frame
// timer: a timer left behind would wake a closed panel for one hidden
// frame, while the tick is already running and draws only while the
// overview is actually on screen.
func (a *shelf) overviewDeadlines(c *ui.Context) {
	now := c.Now()
	var at time.Time
	soon := func(t time.Time) {
		if t.IsZero() || !t.After(now) {
			return
		}
		if at.IsZero() || t.Before(at) {
			at = t
		}
	}
	if item, ok := a.latestClipboard(); ok {
		soon(item.CreatedAt.Add(2 * time.Minute))
	}
	soon(a.happyUntil)
	if !a.lastAct.IsZero() {
		soon(a.lastAct.Add(otterSleepyAfter))
	}
	if a.status != "" && !a.statusUntil.IsZero() {
		soon(a.statusUntil.Add(toastLife))
	}
	a.overviewDue = at
}

// tickOverviewDeadlines redraws the overview when one of its deadlines has
// passed, and only while the overview is on screen: a closed panel keeps no
// deadline and asks for no frames.
func (a *shelf) tickOverviewDeadlines(now time.Time) {
	if !a.visible || a.currentLevel() != 1 {
		a.overviewDue = time.Time{}
		return
	}
	if a.overviewDue.IsZero() || now.Before(a.overviewDue) {
		return
	}
	a.overviewDue = time.Time{}
	if a.win != nil {
		a.win.Invalidate()
	}
}

// overviewMeta is the left column: the two status lines on top and the
// otter, centred horizontally and flush with the bottom.
func (a *shelf) overviewMeta(c *ui.Context) {
	l := loc(c)
	tray, clip := a.overviewCounts()
	top, bottom := l.OverviewTrayCount(tray), l.OverviewClipCount(clip)
	if a.overviewTaskRunning() {
		top, bottom = l.OverviewWorkingTitle(), l.OverviewWorkingHint()
	}
	col := ui.Column(c.Key("ov-meta")).Width(overviewLeftW).Height(overviewBodyH).AlignItems(ui.Center)
	col.Children(func() {
		ui.Column(c.Key("ov-meta-text")).Width(overviewLeftW).Gap(1).AlignItems(ui.Center).Children(func() {
			panelLabel(c, "ov-top", top, 13, 600).TextColor(ink).TextAlign(ui.Center).MaxWidth(overviewLeftW)
			panelLabel(c, "ov-bottom", bottom, 11, 0).TextColor(ink3).TextAlign(ui.Center).MaxWidth(overviewLeftW)
		})
		a.overviewOtter(c)
	})
}

// overviewOtter draws the otter picture for the current state, 74pt tall,
// horizontally centred in the left column. The picture is static: a state
// change is a different picture, and the caption above says what happened
// so colour and drawing never carry the message alone. The otter never
// moves on its own — it arrives with the overview's fade, so a switch from
// a page puts it on its final spot instead of sliding into it.
func (a *shelf) overviewOtter(c *ui.Context) {
	name := a.otterState(c.Now())
	box := ui.Row(c.Key("ov-otter")).Width(overviewLeftW).Grow(1).Justify(ui.Center).AlignItems(ui.End)
	box.Children(func() {
		if b := otterImage(name); b != nil {
			ui.Image(c.Key("ov-otter-art"), b).Size(overviewLeftW, overviewOtterH).Fit(ui.Contain)
			return
		}
		// No picture in this build: keep the column's shape, no glyph
		// pretending to be the mascot.
		ui.Box(c).Height(overviewOtterH)
	})
}

// overviewRecent is the right column's top half: the 「最近放入」 title with
// 全部 on the right, then the tray's five newest items, each draggable out
// exactly like the tray page's tiles.
func (a *shelf) overviewRecent(c *ui.Context) {
	l := loc(c)
	ui.Column(c.Key("ov-recent")).Grow(1).MinWidth(0).Children(func() {
		head := ui.Row(c.Key("ov-recent-head")).Height(overviewHeadH).FillWidth().AlignItems(ui.Center).Padding(0, 2, 0, 2)
		head.Children(func() {
			panelLabel(c, "ov-recent-title", l.OverviewRecent(), 11, 600).TextColor(ink2)
			ui.Box(c).Grow(1)
			all := ui.ButtonBase(c.Key("ov-all")).Height(16).Padding(0, 4, 0, 4).Radius(5).Center().
				Label(l.OverviewAll()).Tooltip(l.OverviewAll())
			if all.Hovered() {
				all.Background(fill1)
			}
			all.Children(func() {
				panelLabel(c, nil, l.OverviewAll(), 11, 0).TextColor(ink3)
			})
			if all.Clicked() {
				a.openPage(pageTray)
			}
		})
		items := a.overviewItems()
		strip := ui.Row(c.Key("ov-strip")).Height(overviewTileH).FillWidth().Gap(gapGrid)
		strip.Children(func() {
			for _, it := range items {
				a.overviewTile(c, it)
			}
			if len(items) == 0 {
				ui.Row(c.Key("ov-empty")).Height(overviewTileH).Grow(1).Padding(0, 2, 0, 2).AlignItems(ui.Center).Children(func() {
					panelLabel(c, nil, l.OverviewEmpty(), typeSub, 0).TextColor(ink3).
						MaxWidth(contentW - overviewLeftW - overviewColGap - 4)
				})
			}
		})
	})
}

// overviewItems are the five items shown: the tray's newest, pinned ones
// included, in the order the tray lists them (pinned first, then newest).
func (a *shelf) overviewItems() []store.Item {
	p, ok := a.page(pageTray).(*trayPage)
	if !ok {
		return nil
	}
	list := p.items()
	if len(list) > overviewTileN {
		list = list[:overviewTileN]
	}
	return list
}

// overviewTile is one small tile: a thumbnail over the name, draggable out
// and clickable through to the tray page.
func (a *shelf) overviewTile(c *ui.Context, item store.Item) {
	l := loc(c)
	name := itemTitleIn(l, item)
	tile := ui.Column(c.Key("ov-" + item.ID)).Grow(1).MinWidth(0).Height(overviewTileH).Radius(10).
		AlignItems(ui.Center).Justify(ui.Center).Gap(3).Label(name)
	if tile.Hovered() {
		tile.Background(fill1)
	}
	if !item.Missing {
		tile.DragDataFrom(func() transfer.Data {
			a.ownDrag = true
			a.takeServiceOwnership()
			if a.win != nil {
				a.win.Invalidate()
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
		ui.Row(c.Key("thumbnail")).Size(24, 26).Center().Children(func() {
			if thumb := a.thumbnail(item); thumb != nil {
				ui.Image(c.Key("bmp"), thumb).Size(22, 24).Fit(ui.Cover).Radius(4)
			} else if item.Kind == store.KindText {
				ui.Icon(c, textSymbol).Size(16, 16).TextColor(ink3)
			} else {
				ui.Icon(c, fileSymbol).Size(18, 18).TextColor(ink3)
			}
		})
		ui.Text(c, name).SingleLine().TextAlign(ui.Center).FontSize(typeMini).
			TextColor(ink2).MaxWidth(64).Padding(0, 2, 0, 2)
	})
	if item.Missing {
		tile.Opacity(0.38)
	}
	if tile.Clicked() {
		a.selection.only(item.ID)
		a.openPage(pageTray)
	}
}

// overviewCounts is the two numbers the left column says.
func (a *shelf) overviewCounts() (tray, clip int) {
	if p, ok := a.page(pageTray).(*trayPage); ok {
		tray = len(p.items())
	}
	if p, ok := a.page(pageClipboard).(*clipboardPage); ok {
		clip = len(p.items())
	}
	return tray, clip
}

// overviewTaskRunning reports a job the overview line shows progress for.
func (a *shelf) overviewTaskRunning() bool {
	return a.conv != nil && a.conv.busy()
}

// overviewContext is the one "most relevant thing" line. Order:
// a running job (progress, cancellable) > a fresh error > a fresh success >
// an update waiting for a restart > the current track > a copy from the
// last two minutes > the clipboard count.
func (a *shelf) overviewContext(c *ui.Context) {
	l := loc(c)
	now := c.Now()
	row := ui.Row(c.Key("ov-ctx")).Height(overviewCtxH).FillWidth().Radius(rRow).
		Padding(0, 4, 0, 8).Gap(8).AlignItems(ui.Center)
	// A status with a deadline is fresh until its hold is over; an error
	// with none stays until the toast clock clears it.
	fresh := a.status != ""
	if fresh && !a.statusUntil.IsZero() {
		fresh = now.Before(a.statusUntil.Add(toastLife))
	}
	switch {
	case a.overviewTaskRunning():
		row.Background(fill1).Children(func() {
			ui.Icon(c, fileSymbol).Size(14, 14).TextColor(ink2)
			label, _ := convertProgress(l, a.conv.job)
			progress := convertFraction(a.conv.job)
			ui.Text(c, label).SingleLine().FontSize(typeCallout).TextColor(ink)
			ui.Box(c).Grow(1)
			if progress >= 0 {
				bar := ui.Box(c.Key("ov-ctx-bar")).Width(64).Height(4).Radius(2).Background(fill3)
				bar.Children(func() {
					ui.Box(c).Height(4).Radius(2).Background(ink).Width(64 * progress)
				})
			}
			cancel := ui.ButtonBase(c.Key("ov-ctx-cancel")).Size(22, 22).Radius(11).Center().
				Label(l.ConvCancelLabel()).Tooltip(l.ConvCancelLabel())
			if cancel.Hovered() {
				cancel.Background(fill2)
			}
			cancel.Children(func() { ui.Icon(c, closeSymbol).Size(13, 13).TextColor(ink) })
			if cancel.Clicked() {
				a.conv.job.cancel.Store(true)
			}
		})
	case fresh && a.statusKind == statusErr:
		row.Background(danger.Alpha(0.14)).Children(func() {
			ui.Icon(c, warnSymbol).Size(14, 14).TextColor(danger)
			ui.Text(c, a.status).SingleLine().FontSize(typeCallout).TextColor(ink)
		})
	case fresh:
		row.Background(success.Alpha(0.12)).Children(func() {
			ui.Icon(c, checkSymbol).Size(14, 14).TextColor(success)
			ui.Text(c, a.status).SingleLine().FontSize(typeCallout).TextColor(ink)
		})
	case a.updateInstalled && a.update != nil:
		row.Background(fill1).Children(func() {
			ui.Icon(c, downloadSymbol).Size(14, 14).TextColor(ink2)
			ui.Text(c, l.OverviewUpdateReady()).SingleLine().FontSize(typeCallout).TextColor(ink)
			ui.Box(c).Grow(1)
			b := ui.ButtonBase(c.Key("ov-restart")).Height(22).Padding(0, 8, 0, 8).Radius(11).Center().
				Label(l.PanelRestart())
			if b.Hovered() {
				b.Background(fill2)
			}
			b.Children(func() { panelLabel(c, nil, l.PanelRestart(), 11, 0).TextColor(ink) })
			if b.Clicked() {
				relaunchApp()
			}
		})
	case a.update != nil && !a.updateInstalling:
		row.Background(fill1).Children(func() {
			ui.Icon(c, downloadSymbol).Size(14, 14).TextColor(ink2)
			ui.Text(c, l.UpdateAvailable(a.update.Version)).SingleLine().FontSize(typeCallout).TextColor(ink)
			ui.Box(c).Grow(1)
			b := ui.ButtonBase(c.Key("ov-install")).Height(22).Padding(0, 8, 0, 8).Radius(11).Center().
				Label(l.PanelInstallUpdate(a.update.Version))
			if b.Hovered() {
				b.Background(fill2)
			}
			b.Children(func() { panelLabel(c, nil, l.PanelInstallUpdate(a.update.Version), 11, 0).TextColor(ink) })
			if b.Clicked() {
				a.installUpdate()
			}
		})
	case a.npShowsTrack():
		row.Label(l.NpPlaying(a.npTrackTitle())).Tooltip(l.MusicOpen()).Children(func() {
			a.np.coverArt(c, "ov-ctx-cover", 18, 4)
			st, _ := a.np.track()
			title := st.Title
			meta := st.Artist
			if meta == "" {
				meta = a.np.cardMeta(st)
			}
			ui.Text(c, title).SingleLine().FontSize(typeCallout).TextColor(ink).MaxWidth(160)
			if meta != "" {
				ui.Text(c, "·").FontSize(typeCallout).TextColor(ink3)
				ui.Text(c, meta).SingleLine().FontSize(typeCallout).TextColor(ink3).MaxWidth(110)
			}
			ui.Box(c).Grow(1)
			a.np.chipToggle(c, "ov-ctx-toggle", st.Playing, 22, 13)
			next := ui.ButtonBase(c.Key("ov-ctx-next")).Size(22, 22).Radius(11).Center().Label(l.MusicNext())
			if next.Hovered() {
				next.Background(fill2)
			}
			next.Children(func() { ui.Icon(c, nextFill).Size(13, 13).TextColor(ink) })
			if next.Clicked() {
				a.np.send(mac.NPNext)
			}
		})
		if row.Clicked() {
			a.openPage(pagePlaying)
		}
		if row.Hovered() {
			row.Background(fill1)
		}
	case a.recentCopy(now):
		item, ok := a.latestClipboard()
		row.Children(func() {
			ui.Icon(c, clipNavSymbol).Size(14, 14).TextColor(ink2)
			ui.Text(c, l.OverviewJustCopied(itemTitle(item))).SingleLine().FontSize(typeCallout).TextColor(ink)
			ui.Box(c).Grow(1)
			b := ui.ButtonBase(c.Key("ov-ctx-all")).Height(22).Padding(0, 6, 0, 6).Radius(11).Center().Label(l.OverviewAll())
			if b.Hovered() {
				b.Background(fill2)
			}
			b.Children(func() { panelLabel(c, nil, l.OverviewAll(), 11, 0).TextColor(ink) })
			if b.Clicked() {
				a.openPage(pageClipboard)
			}
		})
		if row.Hovered() {
			row.Background(fill1)
		}
		if !ok {
			return
		}
	default:
		_, clip := a.overviewCounts()
		row.Children(func() {
			ui.Icon(c, clipNavSymbol).Size(14, 14).TextColor(ink2)
			ui.Text(c, l.OverviewClipCount(clip)).SingleLine().FontSize(typeCallout).TextColor(ink)
			ui.Box(c).Grow(1)
			b := ui.ButtonBase(c.Key("ov-ctx-open")).Height(22).Padding(0, 6, 0, 6).Radius(11).Center().Label(l.OverviewOpen())
			if b.Hovered() {
				b.Background(fill2)
			}
			b.Children(func() { panelLabel(c, nil, l.OverviewOpen(), 11, 0).TextColor(ink) })
			if b.Clicked() {
				a.openPage(pageClipboard)
			}
		})
		if row.Hovered() {
			row.Background(fill1)
		}
	}
}

// recentCopy reports a clipboard capture from the last two minutes.
func (a *shelf) recentCopy(now time.Time) bool {
	item, ok := a.latestClipboard()
	if !ok {
		return false
	}
	return now.Sub(item.CreatedAt) < 2*time.Minute
}

// latestClipboard is the newest clipboard capture.
func (a *shelf) latestClipboard() (store.Item, bool) {
	return a.latestClipboardItem()
}

// otterState picks the picture for the moment: a drag over the shelf
// surprises it, a running job makes it curious, a failure embarrasses it,
// good news makes it happy for a moment, and a long quiet spell puts it to
// sleep. Everything else is its neutral face.
func (a *shelf) otterState(now time.Time) string {
	switch {
	case a.dragging && !a.ownDrag:
		return "surprised"
	case a.statusKind == statusErr && a.status != "":
		// A failure has no deadline of its own; the toast clock clears the
		// text, and until then the otter looks embarrassed — a plain
		// report() must not leave it smiling.
		return "embarrassed"
	case a.overviewTaskRunning():
		return "curious"
	case !a.happyUntil.IsZero() && now.Before(a.happyUntil):
		return "happy"
	case !a.lastAct.IsZero() && now.Sub(a.lastAct) > otterSleepyAfter:
		return "sleepy"
	}
	return "neutral"
}

// otterHappy makes the otter happy for a moment after something good.
func (a *shelf) otterHappy(now time.Time) {
	a.happyUntil = now.Add(otterHappyFor)
	if a.win != nil {
		a.win.Invalidate()
	}
}

// npShowsTrack and its neighbours keep the overview readable without
// reaching into the module's nil-ness everywhere.
func (a *shelf) npShowsTrack() bool { return a.np != nil && a.np.hasTrack() }

func (a *shelf) npTrackTitle() string {
	if a.np == nil {
		return ""
	}
	if st, ok := a.np.track(); ok {
		return st.Title
	}
	return ""
}

// convertFraction is the running job's progress, 0–1, for the overview bar.
func convertFraction(j *convertJob) float32 {
	if j == nil {
		return 0
	}
	total := max(int(j.total.Load()), 1)
	return min(float32(j.done.Load())/float32(total), 1)
}
