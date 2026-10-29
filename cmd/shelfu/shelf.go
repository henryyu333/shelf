//go:build darwin

package main

import (
	"errors"
	"log"
	"strings"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/transfer"
	"github.com/egoist/mygo/ui"

	"shelf/internal/layout"
	"shelf/internal/mac"
	"shelf/internal/settings"
	"shelf/internal/store"
)

// UI、历史与本机 API 由 mygo 主线程串行拥有。
type shelf struct {
	win                                                         *mygo.Window
	winH                                                        uintptr // cached on creation; zero for a headless surface
	tray                                                        *mygo.Tray
	store                                                       *store.Store
	stop                                                        chan struct{}
	quitting, visible, edgeOpened, received                     bool
	status                                                      string
	statusUntil                                                 time.Time
	statusSeenAt                                                time.Time     // 提示当前被看见的窗口起点（pill 或胶囊画出其一）
	statusSeen                                                  time.Duration // 累计被看见时长：胶囊/暂停时段不计入
	statusKind                                                  statusKind    // 提示级别：成功 / 普通信息 / 错误（pill 红点、胶囊配色）
	statusCapsule                                               bool          // 面板收起时是否转交刘海胶囊（后台信息不转交）
	statusCapsuleLimit                                          time.Duration // progress notes must not occupy the notch for the whole operation
	x, y                                                        int
	dispBounds                                                  mygo.Rectangle
	frame                                                       mygo.Rectangle // 常驻窗口当前实际映射的屏幕矩形（desiredFrame 最近一次应用值）
	settingFrame                                                bool
	buildingView, frameQueued                                   bool // defer native resize until the current UI frame has painted
	animationStart                                              time.Time
	animationFrom, progress                                     float32
	level                                                       int       // drawn level: 1 overview (156), 2 page (250)
	jumpLevel                                                   bool      // the next frame snaps the level spring (fresh open)
	closeAt                                                     time.Time // when hide started the content fade; zero = none
	settings                                                    settings.Settings
	locale                                                      string // language the native surfaces say (tray, menus, dialogs); "" = source
	modules                                                     []module
	np                                                          *nowPlayingModule // the peek wings read its state
	pwr                                                         *powerModule      // battery / adapter events (live.go)
	audio                                                       *audioModule      // headphone connect / disconnect
	dl                                                          *downloadsModule  // browser downloads in ~/Downloads
	live                                                        liveQueue         // transient charging / headphone / download-done events
	conv                                                        *converter        // image conversion jobs; nil until the app starts
	usage                                                       *usageFeature     // nil until the AI usage page registers itself
	clip                                                        *clipboardModule  // nil until monitoring starts
	awake                                                       *awakeModule      // nil until the app starts
	vol                                                         volumeControl     // system output volume; nil = CoreAudio
	pages                                                       []panelPage       // registered pages in ear order (built-ins first)
	pagesReady                                                  bool              // built-in pages have been registered
	tab                                                         pageID            // current page of the expanded panel (registry in pages.go)
	lastAct                                                     time.Time         // last time the user did anything with the shelf (the otter's sleepiness)
	happyUntil                                                  time.Time         // the otter is happy until then
	freshInstall                                                bool              // no state.json when this launch started: the welcome card's turn
	uiState                                                     uiState           // welcome / new-version card marks (ui.json)
	uiStateBroken                                               bool              // ui.json unreadable: never overwrite it
	welcomePending, welcomeUp                                   bool
	whatsNewPending, whatsNewUp                                 bool
	openedAt                                                    time.Time             // when this opening began, for the cards' delay
	overviewDue                                                 time.Time             // when the overview's words next change (checked by the monitor tick)
	tabAt                                                       time.Time             // when the tab last switched; drives the pages' crossfade
	tabFrom                                                     pageID                // the page being left by the last switch, drawn fading out
	tabOutK                                                     float32               // opacity the outgoing picture starts at (1, or where an interrupted switch left it)
	tabOutShift                                                 float32               // offset the outgoing picture starts at when a switch interrupted another
	warmed                                                      map[pageID]bool       // pages already drawn once while the panel rested
	warming                                                     pageID                // page queued for that warm draw
	search                                                      string                // clipboard page's search text
	titleWidths                                                 map[string]float32    // back-row page name → measured width at the title font (see backTitleWidth)
	reveal                                                      string                // row to keep in view after a keyboard move
	focusMode                                                   bool                  // the window took the keyboard for clipboard search
	prevAppPID                                                  int                   // app to hand the keyboard back to when focusMode ends
	appNames                                                    map[string]string     // bundle id → display name, cached for menu labels
	appIcons                                                    map[string]*ui.Bitmap // bundle id → icon, cached for clipboard rows
	write                                                       clipWriter            // where copies go; nil = the system clipboard
	dragSerial                                                  int64
	dragOpenTools                                               bool // 本次外部拖拽开始时面板开在工具页（design §1: 只有这时才用卡片接文件）
	dragging, suppressed, pointerDrag, ownDrag, dropHot, inBand bool
	drop                                                        dropZoneState           // incoming-drag zone memory; zero while no drag hovers
	dataDir                                                     string                  // where state.json and settings.json live
	settingsBroken                                              bool                    // settings.json unreadable: never overwrite it
	settingsWin                                                 *mygo.Window            // the 设置 window; nil while closed
	settingsPrevPID                                             int                     // app to hand the keyboard back to when it closes
	perm                                                        permissionFlow          // 系统权限 unified entry's guided walk; zero = idle
	loginOn                                                     bool                    // 开机时自动启动，read back each time the window opens
	loginErr                                                    string                  // last SetOpenAtLogin failure, shown inline
	rn                                                          *renameUI               // 重新命名 window session; nil until first use
	settingsScroll                                              ui.ScrollState          // the Settings window's scroll position
	settingsPage                                                settingsPageID          // the sidebar destination shown
	settingsRecording                                           shortcutKind            // the shortcut row recording keys, shortcutNone when idle
	shortcutBusy                                                map[shortcutKind]bool   // shortcuts the system refused
	shortcutReg                                                 map[shortcutKind]string // what the system holds per shortcut, "" for none
	shortcutsSuspended                                          bool                    // a recording released the shortcuts; restore them after
	update                                                      *mygo.Update            // version the feed offers, waiting for the user
	updateChecking, updateInstalling                            bool
	updateInstalled                                             bool    // swapped into place; runs at the next launch
	updateProgress                                              float64 // download progress while installing
	updateNext                                                  time.Time
	updateLastCheck                                             time.Time         // when the last check ran (this launch)
	updateLastResult                                            updateCheckResult // how it went
	updateLastVersion                                           string            // the version it found, if any
	guard                                                       dragGuard
	snap                                                        snapState // 拖窗口到刘海分屏 session / panel
	escHeld, spaceHeld, cmdCHeld                                bool
	selection                                                   selection
	stopMouseObservation, stopDragObservation                   func()
	leftSince                                                   time.Time
	topInset                                                    int
	notchX, notchY, notchW, notchH                              int
	hasNotch                                                    bool
	island                                                      bool           // this display draws the floating island shell
	islandTop                                                   int            // island shell's top inset inside the window
	dispWork                                                    mygo.Rectangle // the display's visible frame, measured by placeAt
	islandDX, islandDY                                          int            // dragged island: offsets from the default axis and row
	islandPress                                                 islandPress
	islandDrag                                                  bool // the press on the closed island became a move
	dispID                                                      int64
	idleIslands                                                 []*idleIslandWindow
	peeking                                                     bool
	peekProgress, peekFrom                                      float32
	peekAnimStart                                               time.Time
	fb                                                          feedback    // the notch's completion capsule (feedback.go)
	fx                                                          shellMotion // the shell drawn last, for the window frame
	ignoringMouse                                               bool
	watchingPointer                                             bool               // registered with the process-wide pointer gate
	material                                                    *mac.ShellMaterial // light appearance's backdrop material
	materialWin                                                 uintptr            // the window that material belongs to
	materialAt                                                  materialKey        // the material's last applied box
	systemDark, reduceTrans                                     bool               // cached system appearance (refreshed on a slow tick)
	appearanceAt                                                time.Time
	thumbnails                                                  map[string]*ui.Bitmap
	shake                                                       shakeDetector // 摇晃召唤：拖拽中左右晃动检测
	basket                                                      basketState   // 摇晃召唤的拖放篮
	thumbPending                                                map[string]bool
	thumbDiscard                                                map[string]bool
	thumbActive                                                 int
	pins                                                        []*pinItem // desktop-pinned cards (pin.go)
	pinReturning                                                bool       // a pin drag holds the notch's 松开放回 bulge

	// 框选文字：同一时间只有一轮（ocrScan，capture+识别全程）；ocrBusyAt
	// 记这轮开始时刻给进行中胶囊的 250ms 延迟计时；ocrSheet 为真时展开
	// 面板的舞台换成「屏幕录制」权限说明卡。三者只在 UI 线程读写。
	ocrScan   bool
	ocrBusyAt time.Time
	ocrRunID  int
	ocrSheet  bool

	// 拖出后从托盘移除：当前拖出会话从托盘带走的项目（剪贴板页为 nil），
	// 以及已拖出、等待最终移除的托盘项目及其放下时刻。
	dragOutIDs []string
	leaving    map[string]time.Time

	// toolsLay 缓存工具页的 layout.json（internal/layout）；懒加载，
	// mutateTools 写盘后保持最新。toolsLayBroken 与 settingsBroken 同规：
	// 文件读不通时界面用默认布局，但禁止任何写回以免覆盖可恢复原件。
	toolsLay       *layout.Layout
	toolsLayBroken bool
}

func (a *shelf) toggle() {
	if a.visible {
		a.hide(hideExplicit)
	} else {
		a.show(false)
	}
}

func (a *shelf) startMonitoring() {
	go func() {
		ticker := time.NewTicker(pointerInterval)
		defer ticker.Stop()
		for {
			select {
			case <-a.stop:
				return
			case now := <-ticker.C:
				mygo.RunOnMain(func() {
					if a.quitting {
						return
					}
					for _, m := range a.modules {
						m.tick(now)
					}
					a.animate(now)
					a.tickOverviewDeadlines(now)
					a.refreshAppearance(now)
					a.monitorDrag(now)
					// 内容高度随项目数/正在播放卡片/搜索状态变化：
					// desiredFrame 未变时是恒等比较，不触发原生缩放。
					a.syncWindowFrame()
					// The gate follows the shape the last frame drew, so it
					// is normally right already; asking again every tick
					// covers what draws nothing (a settled panel), a missed
					// move event and a shape that changed under a still
					// pointer, so a press never waits for the next move.
					a.syncPointerGate()
					if a.win != nil && a.overShell(cursorPoint()) {
						// The pointer is doing something on the shell: the
						// otter does not fall asleep under the user's hand.
						a.lastAct = now
					}
					a.nextWarmPage(now)
					a.tickUpdates(now)
					// The status's clock runs only while it is actually on
					// screen: the open panel's pill counts, a drawn capsule
					// counts, and a capsule paused by hover or a drag holds
					// both its own hold and the panel's deadline.
					seeing := a.status != "" && (a.visible || (a.fb.drawn && a.fb.pausedAt.IsZero()))
					if seeing {
						if a.statusSeenAt.IsZero() {
							a.statusSeenAt = now
						} else {
							a.statusSeen += now.Sub(a.statusSeenAt)
							a.statusSeenAt = now
						}
					} else {
						a.statusSeenAt = time.Time{}
					}
					// A capsule paused mid-hold extends the shared deadline:
					// the toast must not expire while its words are parked
					// off screen.
					until := a.statusUntil
					if !a.fb.pausedAt.IsZero() {
						if f := &a.fb; f.until.After(until) {
							until = f.until
						}
					}
					if !a.statusUntil.IsZero() && now.After(until) {
						a.dismissReadFeedback(now)
						a.status, a.statusUntil, a.statusSeen, a.statusSeenAt = "", time.Time{}, 0, time.Time{}
						if a.win != nil {
							a.win.Invalidate()
						}
					}
					// statusToast 给无期限状态（报错、启动提示）计时：
					// 看见满 toastLife 后清掉。
					if a.status != "" && a.statusUntil.IsZero() && a.statusSeen >= toastLife {
						a.dismissReadFeedback(now)
						a.status, a.statusSeen, a.statusSeenAt = "", 0, time.Time{}
						if a.win != nil {
							a.win.Invalidate()
						}
					}
				})
			}
		}
	}()
}

// statusKind 是提示的级别：做成的事情、普通信息、错误。面板底部 pill
// 只给错误加红点；刘海胶囊按级别给状态点配色（DESIGN.md：成功绿
// #30D158 ✓、普通信息中性白点、错误红 #FF453A !）。
type statusKind int

const (
	statusOK   statusKind = iota // 做成的结果：绿 ✓
	statusInfo                   // 普通信息：中性白点，无光晕
	statusErr                    // 错误：红 !，无期限时至少停 4 秒
)

// toastStatus posts a transient status pill at the panel's bottom centre:
// msg until the deadline (or toastLife from its first paint when until is
// zero); statusErr gives it the small red dot of error toasts. With the
// panel closed there is no pill to read, so the result of a user action
// grows out of the notch as a capsule instead (feedback.go) — even while
// the panel is still folding away, since the capsule's own clock starts
// when it can appear.
func (a *shelf) toastStatus(msg string, until time.Time, kind statusKind) {
	a.toast(msg, until, kind, true, 0)
}

// toastProgress preserves the panel deadline but caps the capsule's visible
// span. Completion and failure post their own unrestricted result.
func (a *shelf) toastProgress(msg string, until time.Time) {
	a.toast(msg, until, statusInfo, true, 4*time.Second)
}

// toastQuiet posts a status that never grows the notch capsule: background
// bookkeeping the user did not ask for (automatic cleanup) waits for the
// panel's next opening in the bottom pill.
func (a *shelf) toastQuiet(msg string, until time.Time, kind statusKind) {
	a.toast(msg, until, kind, false, 0)
}

func (a *shelf) toast(msg string, until time.Time, kind statusKind, capsule bool, limit time.Duration) {
	a.status = msg
	a.statusUntil, a.statusKind, a.statusCapsule = until, kind, capsule
	a.statusSeen, a.statusSeenAt = 0, time.Time{}
	a.statusCapsuleLimit = limit
	if !a.visible && capsule {
		a.showStatusFeedback(time.Now())
	}
	if a.win != nil {
		a.win.Invalidate()
	}
}

func (a *shelf) showStatusFeedback(now time.Time) {
	until := a.statusUntil
	if limit := a.statusCapsuleLimit; limit > 0 && (until.IsZero() || until.After(now.Add(limit))) {
		until = now.Add(limit)
	}
	a.fb.show(a.status, a.statusKind, until, now)
}

// handoffStatus moves a live status to the notch capsule when the panel
// folds: a toast posted moments before a collapse (回车复制即收起) would
// otherwise die with the pill after a single frame. A toast the panel or
// the capsule already showed long enough (its kind's floor) is not
// replayed — an armed capsule that ran its span stays done, and a capsule
// still holding the words simply plays out its remaining time. Background
// bookkeeping (toastQuiet) never hands off.
func (a *shelf) handoffStatus(now time.Time) {
	if a.status == "" || !a.statusCapsule {
		return
	}
	if a.dismissReadFeedback(now) {
		return
	}
	if !a.statusUntil.IsZero() && now.After(a.statusUntil) {
		return
	}
	line, _, _ := strings.Cut(a.status, "\n")
	if f := &a.fb; f.text == line && (f.armed || f.up(now)) {
		return // shown already, or still on its own remaining time
	}
	if a.fb.played == line {
		return // the capsule already played this toast out: no second show
	}
	a.showStatusFeedback(now)
}

// dismissReadFeedback counts only the time the words were actually on
// screen: the pill's visible span plus the capsule's drawn span. It also
// runs before the panel expires its text, so an unseen capsule cannot
// outlive the acknowledgement already read there.
func (a *shelf) dismissReadFeedback(now time.Time) bool {
	seen := a.statusSeen
	if !a.statusSeenAt.IsZero() && a.status != "" &&
		(a.visible || (a.fb.drawn && a.fb.pausedAt.IsZero())) {
		seen += now.Sub(a.statusSeenAt)
	}
	if seen < a.statusKind.holdFloor() {
		return false
	}
	line, _, _ := strings.Cut(a.status, "\n")
	if f := &a.fb; f.text == line {
		f.text, f.liveToken, f.pausedAt = "", nil, time.Time{}
	}
	return true
}

func (a *shelf) receive(data transfer.Data) error {
	l := a.appLoc()
	// A file dropped on the panel's drop zone comes back to the overview
	// when it lands: the zone was a passing gesture, not a page change.
	dropped := a.drop.on
	if a.store == nil {
		return errors.New(l.StatusNoStore())
	}
	var err error
	if paths, e := data.Files(); e == nil && len(paths) > 0 {
		err = a.store.AddFiles(paths)
	} else if b, e := data.Read(transfer.PNG); e == nil && len(b) > 0 {
		err = a.store.AddImage(b)
	} else if b, e := data.Read(transfer.Text); e == nil && len(b) > 0 {
		err = a.store.AddText(string(b))
	} else {
		err = errors.New(l.StatusUnsupported())
		a.report(err)
		return err
	}
	if err == nil {
		a.received = true
		a.lastAct = time.Now()
		if a.welcomeUp {
			// The welcome card's practice is the drop itself: one real
			// file finishes it.
			a.dismissWelcome()
			a.toastStatus(l.WelcomeDone(), time.Now().Add(3*time.Second), statusOK)
		} else {
			a.toastStatus(l.StatusAdded(), time.Now().Add(3*time.Second), statusOK)
		}
		a.otterHappy(time.Now())
		if dropped && a.visible && a.currentLevel() == 2 {
			a.openPage(pageOverview)
		}
	}
	a.changed(err)
	return err
}

func (a *shelf) changed(err error) {
	if err != nil {
		a.report(err)
		return
	}
	if a.store != nil && len(a.store.Items) > 0 {
		a.invalidateThumbnail(a.store.Items[0].ID)
	}
	if a.win != nil {
		a.win.Invalidate()
	}
}
func (a *shelf) report(err error) {
	a.lastAct = time.Now()
	a.toastStatus(a.appLoc().StatusFailed(err.Error()), time.Time{}, statusErr)
	log.Print(a.status)
	if a.win != nil {
		a.win.Invalidate()
	}
}

// notice adds a startup message to the status toast without hiding an
// earlier one (settings, shortcut and migration problems can all occur).
// Notices are problems, so they show the error dot and live a little
// longer than a confirmation.
func (a *shelf) notice(msg string) {
	log.Print(msg)
	if a.status != "" {
		a.status += "\n"
	}
	a.toastQuiet(a.status+msg, time.Now().Add(8*time.Second), statusErr)
}
func (a *shelf) completed(r transfer.Result) {
	a.ownDrag = false
	ids := a.dragOutIDs
	a.dragOutIDs = nil
	if r.Err != nil {
		a.report(r.Err)
		return
	}
	if !r.Canceled && r.Operation != transfer.None {
		a.leaveTray(ids, time.Now())
		a.hide(hidePassive)
	}
}

// applySettings is the only path that changes a preference: it saves next
// to settings.json first and only then replaces the running copy, so a
// failed write leaves the running setting unchanged and a broken file is
// never overwritten. The tray menus and the Settings window both go
// through it, so they can never disagree. After the save both windows
// repaint, which keeps an open Settings window in sync with menu choices.
// Side effects that follow a changed field (module start/stop, store
// limit, an immediate cleanup pass) run in each setter after this returns.
func (a *shelf) applySettings(next settings.Settings) bool {
	if a.settingsBroken {
		return false
	}
	if err := settings.Save(a.dataDir, next); err != nil {
		a.report(err)
		return false
	}
	a.settings = next
	if a.win != nil {
		a.win.Invalidate()
	}
	a.repaintSettings()
	return true
}

// applyClipboardHistory starts the recorder once for this session. Recording
// is always enabled; settings still control retention and excluded apps.
func (a *shelf) applyClipboardHistory() {
	if a.clip != nil {
		return
	}
	a.clip = newClipboardModule(a)
	a.modules = append(a.modules, a.clip)
}

// setClipboardLimit saves the new clipboard text limit and applies it to
// the store at once (the next add trims to it). Disabled while
// settings.json could not be read, so a broken file is never overwritten.
func (a *shelf) setClipboardLimit(n int) {
	if n < 1 {
		n = settings.Default().ClipboardLimit
	}
	if a.settings.ClipboardLimit == n {
		return
	}
	next := a.settings
	next.ClipboardLimit = n
	if !a.applySettings(next) {
		return
	}
	if a.store != nil {
		a.store.SetTextLimit(n)
	}
}

// hideExit is how a hide was reached. Restoring the keyboard to the app
// the user copied from is right only for exits the user chose while using
// the shelf — Esc, Enter-copy, the collapse button, the tray/shortcut
// toggle. A click outside or a finished drag-out means the user moved on
// to another app themselves; stealing it back would fight that choice.
type hideExit int

const (
	hideExplicit hideExit = iota // the user dismissed the shelf itself
	hidePassive                  // click elsewhere, a finished drag-out, a flash timer
)

// shouldRestoreApp reports whether hide wants the remembered frontmost app
// re-activated: only when the shelf had the keyboard (focusMode) and the
// exit was the user's own dismiss. Pure; unit-tested.
func (a *shelf) shouldRestoreApp(exit hideExit) bool {
	return a.focusMode && exit == hideExplicit
}

// openClipboardSearch opens the shelf on the clipboard tab with the search
// field focused and ready for typing (the clipboard hotkey). The app is
// activated so the window takes the keyboard, and the app the user came
// from is remembered so hiding hands the keyboard back to it. Only this
// keyboard flow activates the app; hover, drag, tray and service opens keep
// their ShowInactive behaviour.
func (a *shelf) openClipboardSearch() {
	// This open takes the panel over: a queued services flash or a
	// drag-edge auto-collapse must never hide the search underneath.
	a.takeServiceOwnership()
	a.edgeOpened = false
	if a.win != nil && !a.focusMode {
		if pid := mac.FrontmostPID(); pid > 0 && pid != mac.OwnPID() {
			a.prevAppPID = pid
		}
	}
	a.switchTab(pageClipboard) // leaving pageUsage retires its work the same way setTab does
	a.search = ""
	a.focusMode = true
	a.level = 2
	a.jumpLevel = true
	if a.win == nil {
		return // headless surface: state only
	}
	// The window must sit below the IM candidate band before the search
	// field takes the keyboard — even when the panel was already open
	// and show() returns early. syncMousePassThrough applies windowLevel.
	a.syncMousePassThrough()
	a.show(false)
	mac.ActivateSelf()
	a.win.Focus()
	// 键盘已经在这个窗口里：Esc 与空格交给窗口处理，全局注册会把
	// 按键从搜索框手里抢走。
	a.releaseKeys()
}

// switchTab changes the current page without a fade: the search hotkey's
// direct jump. setTab keeps its own copy because it also records tabFrom
// for the crossfade; both retire the AI usage page's background work the
// moment it is left.
func (a *shelf) switchTab(id pageID) {
	if a.tab == pageUsage && id != pageUsage && a.usage != nil {
		a.usage.stopWork()
	}
	if a.tab == pageTools && id != pageTools {
		a.resetToolsEdit()
	}
	a.tab, a.tabFrom = id, id
	a.tabAt = time.Time{}
	a.tabOutK, a.tabOutShift = 1, 0
}

// Keyboard handback seams: tests stub these (mac's own calls need the app
// running) to prove who gets the keyboard when a keyboard-taking surface
// closes.
var (
	appIsActive = mac.AppActive
	activateApp = mac.ActivateApp
	resignSelf  = mac.DeactivateSelf
)

// activatePreviousApp hands the keyboard back to the app the user came
// from when the shelf was opened for clipboard search. It runs only while
// this app is still the active one — a user who switched away already
// must not be pulled back — and if the remembered app is gone, ShelfU's
// own activation is resigned rather than leaving a hidden app in front.
func (a *shelf) activatePreviousApp(pid int) { a.handBack(pid, true) }

// handBackApp is activatePreviousApp without the resign fallback: the
// Settings window hands the keyboard back the same way, but must never
// hide this app (NSApp hide would take the notch window with it).
func (a *shelf) handBackApp(pid int) { a.handBack(pid, false) }

func (a *shelf) handBack(pid int, resignWhenGone bool) {
	if pid <= 0 || pid == mac.OwnPID() || !appIsActive() {
		return
	}
	if !activateApp(pid) && resignWhenGone {
		resignSelf()
	}
}
