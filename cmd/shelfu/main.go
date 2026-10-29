//go:build darwin

package main

import (
	"context"
	"errors"
	"flag"
	"log"
	"os"
	"path/filepath"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"shelf/internal/aiusage"
	"shelf/internal/mac"
	"shelf/internal/settings"
	"shelf/internal/store"
	"shelf/locales"
)

func main() {
	// mygo starts this same binary with MYGO_GENERATE set to write the
	// TypeScript client, on every `mygo build` and `mygo dev`. That run must
	// not touch the data directory (it would create an empty one and block
	// the migration of the old one), rename a bundle, or open a store.
	if generatingClient() {
		mygo.App.SetName(appName)
		if err := mygo.App.Run(); err != nil {
			log.Fatal(err)
		}
		return
	}
	// A bundle an in-app update left at Shelf.app moves to ShelfU.app and
	// reopens from there before anything reads the data directory.
	selfRenameBundle()
	dataDir := flag.String("data-dir", "", "本地数据目录（默认 Library/Application Support/ShelfU）")
	show := flag.Bool("show", false, "启动时显示托盘")
	flag.Parse()
	if *dataDir == "" {
		root, err := store.DefaultRoot()
		if err != nil {
			log.Fatal(err)
		}
		// The default directory is the renamed app's; the old one is copied
		// over once. A --data-dir the user gave is never migrated into.
		if oldRoot, lerr := store.LegacyRoot(); lerr == nil {
			migrated, merr := migrateDataRoot(oldRoot, root)
			switch {
			case errors.Is(merr, errLegacyRootLocked):
				log.Printf("%s: %v", appName, merr)
				lm := startupLoc(root, oldRoot)
				exitWithDialog(lm.StartupLegacyRunningTitle(),
					lm.StartupLegacyRunningDetail(appName, oldRoot, root))
				return
			case merr != nil:
				log.Printf("%s: 数据迁移失败：%v", appName, merr)
				lm := startupLoc(root, oldRoot)
				exitWithDialog(lm.StartupMigrateFailedTitle(),
					lm.StartupMigrateFailedDetail(oldRoot, root, merr.Error()))
				return
			case migrated:
				log.Printf("%s: 数据已从 %s 迁移到 %s，旧目录保留为备份", appName, oldRoot, root)
			}
		}
		*dataDir = root
	}
	s, err := store.Open(*dataDir)
	if err != nil {
		// 历史未成功打开时不创建托盘、快捷键或监控，也不改写原件。
		log.Printf("%s: %v", appName, err)
		lm := startupLoc(*dataDir)
		message := lm.StartupHistoryUnreadable(appName)
		detail := lm.StartupHistoryUnreadableDetail(*dataDir, err.Error())
		if errors.Is(err, store.ErrLocked) {
			message = lm.StartupAlreadyRunning(appName)
			detail = lm.StartupAlreadyRunningDetail(*dataDir)
		}
		exitWithDialog(message, detail)
		return
	}
	if s != nil {
		if merr := s.MigrationError(); merr != nil {
			// 备份失败时存储已停止写入；这里只做启动日志，界面状态在 ready 中显示。
			log.Printf("ShelfU: 迁移前备份失败，已停止写入以免覆盖可恢复原件：%v", merr)
		}
	}
	prefs, perr := settings.Load(*dataDir)
	fresh := s != nil && s.Fresh()
	a := &shelf{store: s, settings: prefs, dataDir: *dataDir, stop: make(chan struct{}), thumbnails: make(map[string]*ui.Bitmap), thumbPending: make(map[string]bool), freshInstall: fresh}
	// 剪贴板文字限额来自设置；小于 1 的值由 store 回落到默认 30。
	s.SetTextLimit(prefs.ClipboardLimit)
	if perr != nil {
		a.settingsBroken = true
		a.notice(locales.In(resolveLanguage(prefs.Language, preferredLanguages())).StartupSettingsBroken(perr.Error()))
	}
	mygo.App.SetName(appName)
	// The language of settings.json wins over the system's; "" follows the
	// system again, with the one rule resolveLanguage states (a Chinese first
	// language shows Chinese, every other English). The launch pins what the
	// rule resolves, so what the app shows, its native AppKit text and its own
	// strings agree; every launch resolves again from settings.json.
	mygo.App.OnLocaleChanged(a.localeChanged)
	a.showLanguage()
	mygo.App.SetActivationPolicy(mygo.ActivationPolicyAccessory)
	mygo.App.OnBeforeQuit(func(*mygo.QuitEvent) { a.quitting = true })
	mygo.App.OnWillQuit(func(*mygo.QuitEvent) {
		close(a.stop)
		a.stopDragGuard()
		a.endSnapSession(true)
		a.cancelSnapConfirm()
		mygo.GlobalShortcut.UnregisterAll()
		if a.stopMouseObservation != nil {
			a.stopMouseObservation()
		}
		if a.stopDragObservation != nil {
			a.stopDragObservation()
		}
		if a.tray != nil {
			a.tray.Destroy()
		}
		a.flushLeaving(time.Now().Add(time.Second)) // dragged-out items leave for good
		a.closeIdleIslands()
		a.store.Close()
	})
	mygo.App.WhenReady(func() {
		if err := mac.Init(); err != nil {
			log.Fatal(err)
		}
		a.ready(*show)
	})
	mygo.App.OnDidBecomeActive(a.followSystemLanguage)
	mygo.App.OnActivate(func(bool) {
		// macOS reopen（如 open -a / Finder 再次启动已运行的实例）：设置
		// 窗口开着就归还给它；否则等价于菜单栏图标点击——切换而非强制
		// 展开，避免常驻窗口随激活事件自行弹出。
		if a.settingsWin != nil {
			mac.ActivateSelf()
			a.settingsWin.Focus()
			return
		}
		if a.win != nil {
			a.toggle()
		}
	})
	if err := mygo.App.Run(); err != nil {
		log.Fatal(err)
	}
}

// generatingClient reports whether mygo started this binary to write the
// TypeScript client (MYGO_GENERATE), as mygo build, mygo dev and mygo
// generate do.
func generatingClient() bool {
	return os.Getenv("MYGO_GENERATE") != ""
}

// exitWithDialog shows one error dialog and quits: the startup path that
// cannot open a usable data directory never creates a tray, shortcuts or
// monitors, and never writes to the files it could not read.
func exitWithDialog(message, detail string) {
	mygo.App.SetName(appName)
	mygo.App.SetActivationPolicy(mygo.ActivationPolicyAccessory)
	mygo.App.WhenReady(func() {
		go func() {
			_, _ = mygo.Dialog.Message(mygo.MessageOptions{
				Type:    mygo.MessageError,
				Title:   appName,
				Message: message,
				Detail:  detail,
			})
			mygo.App.Quit()
		}()
	})
	if err := mygo.App.Run(); err != nil {
		log.Fatal(err)
	}
}

// startupLoc says the messages of the language a launch shows, for the
// startup path that fails before the shelf exists: the language settings.json
// names, or the system's by the same rule resolveLanguage states. It reads
// settings.json without writing it (settings.LanguageOf), so a launch that
// cannot open the data directory leaves every file as it was; dirs are tried
// in order, the first with a settings.json answering.
func startupLoc(dirs ...string) locales.Messages {
	setting := ""
	for _, dir := range dirs {
		if lang, ok := settings.LanguageOf(dir); ok {
			setting = lang
			break
		}
	}
	return locales.In(resolveLanguage(setting, preferredLanguages()))
}

func (a *shelf) ready(show bool) {
	a.locale = mygo.App.Locale()
	a.newNotchWindow()
	a.stopMouseObservation = mac.ObserveMouseDown(a.win.NativeHandle(), func(inside bool, button int) {
		a.mouseDown(inside, button, mygo.Screen.CursorScreenPoint())
	})
	a.stopDragObservation = mac.ObserveLeftMouseDragged(a.pointerDragged)
	a.win.OnClose(func(e *mygo.CloseEvent) {
		if !a.quitting {
			e.PreventDefault()
			a.hide(hideExplicit)
		}
	})
	var err error
	trayOpts := mygo.TrayOptions{IconIsTemplate: true, ToolTip: a.appLoc().AppTrayTooltip()}
	if dir, derr := mygo.App.Path(mygo.PathResources); derr == nil {
		trayOpts.Icon = trayIcon(dir)
	}
	if len(trayOpts.Icon) == 0 {
		// No icon file: the title keeps the menu-bar item visible and
		// clickable.
		trayOpts.Title = appName
	}
	a.tray, err = mygo.NewTray(trayOpts)
	if err != nil {
		log.Fatal(err)
	}
	a.tray.OnClick(a.toggle)
	a.tray.OnRightClick(func() {
		a.tray.SetMenu(a.trayMenu())
		defer a.tray.SetMenu(nil)
		a.tray.PopUpMenu()
	})
	if err := mygo.GlobalShortcut.Register(trayShortcut, a.toggle); err != nil {
		a.notice(a.appLoc().StartupShortcutBusy(err.Error()))
	}
	if err := mygo.GlobalShortcut.Register(clipboardShortcut, a.openClipboardSearch); err != nil {
		a.notice(a.appLoc().StartupClipboardShortcutBusy(err.Error()))
	}
	if err := mygo.GlobalShortcut.Register(ocrShortcut, a.startScreenOCR); err != nil {
		a.notice(a.appLoc().StartupCaptureShortcutBusy(err.Error()))
	}
	a.applyClipboardHistory()
	a.modules = append(a.modules, &retentionModule{host: a})
	a.np = &nowPlayingModule{host: a}
	a.modules = append(a.modules, a.np)
	a.conv = &converter{host: a}
	a.dl = newDownloadsModule(a)
	// The strip keeps the first two activities. The converter's cancel
	// control comes first; a running download's chip may take the second.
	a.modules = append(a.modules, a.conv, a.dl, &leavingModule{host: a}, &ocrIndex{host: a})
	// After the converter: a running conversion's only cancel control must
	// not be displaced.
	a.awake = newAwakeModule(a)
	a.modules = append(a.modules, a.awake)
	// Realtime activities: battery, headphone and download notices. They
	// only post to the shared queue (live.go); the queue decides what the
	// notch shows.
	a.pwr = newPowerModule(a)
	a.audio = newAudioModule(a)
	a.modules = append(a.modules, a.pwr, a.audio)
	// IOKit's change notifications wake the battery poll, so a plug-in
	// posts within a tick. Without the observer the two-second poll alone
	// carries the notices, as it always did.
	a.pwr.watchPower()

	// AI 用量页：额度查询与本地用量扫描都注入为可替换的实现，页面只认
	// internal/aiusage 的类型。额度读取本机已保存的登录信息，只发给
	// 对应服务的官方端点；本地扫描的增量缓存放数据目录的
	// usage-cache/。这里的组装不碰凭据、文件与网络——一切取数都在
	// tick/点击触发且 aiUsage.enabled 为 true 之后才开始。
	usageEnv := aiusage.DefaultEnv()
	deps := usageDeps{
		fetchCodex:  func(ctx context.Context) aiusage.Quota { return aiusage.FetchCodex(ctx, usageEnv) },
		fetchClaude: func(ctx context.Context) aiusage.Quota { return aiusage.FetchClaude(ctx, usageEnv) },
		pace:        aiusage.Pace,
		// Claude Code's own logs tell whether an allowance moved at all;
		// the /usage read runs only when it did.
		claudeUsedSince: func(t time.Time) bool {
			return aiusage.ClaudeUsedSince(usageEnv.ClaudeConfigDir, usageEnv.Home, t)
		},
		loadLast: func() []aiusage.Quota { return aiusage.LoadLastQuotas(a.dataDir) },
		saveLast: func(q aiusage.Quota) { _ = aiusage.SaveLastQuota(a.dataDir, q) },
		// A fresh scanner per refresh follows the local* switches; the
		// incremental cache in usage-cache keeps rebuilds cheap.
		newScanner: func() usageScanner {
			if usageEnv.Home == "" {
				return nil
			}
			return aiusage.NewScanner(usageEnv.Home, filepath.Join(a.dataDir, "usage-cache"), usageSources(&a.settings.AIUsage))
		},
		now: time.Now,
	}
	usage := newUsageFeature(a, deps)
	a.registerPage(usage)
	a.modules = append(a.modules, usage)
	_, a.dragSerial = mac.DragState()
	mygo.Screen.OnDisplaysChanged(func() {
		// 常驻窗口跟随光标所在显示器重新定位（收起态无感）。
		a.place(mygo.Screen.CursorScreenPoint())
		// 拔掉显示器的固定项回主显示器同一边。
		a.pinDisplaysChanged()
	})
	if a.store != nil {
		if merr := a.store.MigrationError(); merr != nil {
			a.notice(a.appLoc().StartupBackupFailed(merr.Error()))
		}
	}
	// The welcome and new-version cards are decided once per launch, from
	// the data directory and the bundle version.
	a.planOverviewCards()
	if a.welcomePending {
		// A first run shows the overview once, about a second in, so the
		// new user sees where things go without being told to click.
		time.AfterFunc(time.Second, func() {
			mygo.RunOnMain(func() {
				if a.welcomePending && !a.visible && !a.quitting {
					a.show(false)
					a.welcomeUp = true
					a.openedAt = time.Time{}
					a.win.Invalidate()
				}
			})
		})
	}
	if show {
		a.show(false)
	} else {
		// Keep the same top-anchored transparent strip when idle or peeking;
		// only its drawing and mouse pass-through change on hover.
		a.place(mygo.Screen.CursorScreenPoint())
		a.syncMousePassThrough()
		a.win.ShowInactive()
	}
	a.installServices()
	a.pinRestore()
	a.startMonitoring()
	log.Print("ShelfU ready")
}

// newNotchWindow creates the persistent notch window: one transparent,
// frameless, non-activating panel that the three states redraw. The smoke
// test builds the same window, so the ⌃⌥V focus behaviour is checked
// against what production shows.
func (a *shelf) newNotchWindow() {
	a.material, a.materialWin = nil, 0
	a.win = mygo.NewWindow(mygo.WindowOptions{
		Title: appName, Width: sheetW, Height: sheetH,
		Hidden: true, Frameless: true, Transparent: true, DisableShadow: true,
		AlwaysOnTop: true, SkipTaskbar: true, Panel: true,
		DisableMove: true, DisableResize: true, DisableMinimize: true, DisableMaximize: true, DisableFullScreen: true,
		Content: ui.View(a.view),
	})
	a.win.SetVisibleOnAllWorkspaces(true)
	a.winH = a.win.NativeHandle()
	mac.ConfigureNotchWindow(a.winH)
	// The gate belongs to the window, not to a placement: an app that
	// starts with the panel open never places, and a test shelf must watch
	// the pointer as production does.
	a.watchPointer()
	a.win.OnMove(a.windowGeometryChanged)
	a.win.OnResize(a.windowGeometryChanged)
	log.Printf("window collectionBehavior=0x%x", mac.CollectionBehavior(a.win.NativeHandle()))
}

// trayMenu is rebuilt on every right-click so it reflects the current
// system-permission state, which the user changes in System Settings, and
// the current settings.
func (a *shelf) trayMenu() *mygo.Menu {
	l := a.appLoc()
	return mygo.NewMenu([]*mygo.MenuItem{
		{Label: l.TrayShowShelf(), Click: func(*mygo.MenuItem, *mygo.Window) { a.toggle() }},
		a.permissionMenuItem(),
		{Label: l.TrayRetention(), Type: mygo.MenuItemSubmenu, Submenu: a.retentionMenu()},
		{Label: l.TrayClipboard(), Type: mygo.MenuItemSubmenu, Submenu: a.clipboardMenu()},
		{Label: l.TrayNowPlaying(), Type: mygo.MenuItemCheckbox, Checked: a.settings.IslandNowPlaying, Disabled: a.settingsBroken,
			Click: func(*mygo.MenuItem, *mygo.Window) {
				a.setNowPlaying(func(s *settings.Settings) { s.IslandNowPlaying = !s.IslandNowPlaying })
			}},
		{Label: l.AwakeTitle(), Type: mygo.MenuItemSubmenu, Submenu: a.awakeMenu()},
		{Label: l.ItemCaptureText(), Click: func(*mygo.MenuItem, *mygo.Window) { a.startScreenOCR() }},
		mygo.Separator(),
		{Label: l.TraySettings(), Click: func(*mygo.MenuItem, *mygo.Window) { a.openSettings() }},
		a.updateMenu(),
		mygo.Separator(),
		{Label: l.TrayQuit(), Click: func(*mygo.MenuItem, *mygo.Window) { mygo.App.Quit() }},
	})
}

// retentionMenu offers the retention periods. It is disabled while
// settings.json could not be read, so a broken file is never overwritten.
func (a *shelf) retentionMenu() []*mygo.MenuItem {
	l := a.appLoc()
	choice := func(label string, days int) *mygo.MenuItem {
		current := a.settings.RetentionDays
		if current < 0 {
			current = 0
		}
		return &mygo.MenuItem{
			Label: label, Type: mygo.MenuItemRadio, Checked: current == days, Disabled: a.settingsBroken,
			Click: func(*mygo.MenuItem, *mygo.Window) { a.setRetention(days) },
		}
	}
	items := []*mygo.MenuItem{choice(l.RetentionNever(), 0)}
	for _, d := range retentionChoices {
		items = append(items, choice(l.RetentionDays(d), d))
	}
	return items
}

// setRetention saves the new period and applies it at once.
func (a *shelf) setRetention(days int) {
	if a.settings.RetentionDays == days {
		return
	}
	next := a.settings
	next.RetentionDays = days
	if !a.applySettings(next) {
		return
	}
	a.removeExpired(time.Now())
}

// clipboardMenu offers the retained text limits. A broken settings file locks
// these choices, but never disables the clipboard recorder itself.
func (a *shelf) clipboardMenu() []*mygo.MenuItem {
	l := a.appLoc()
	var items []*mygo.MenuItem
	limit := a.settings.ClipboardLimit
	if limit < 1 {
		limit = settings.Default().ClipboardLimit
	}
	for _, n := range clipboardLimitChoices {
		items = append(items, &mygo.MenuItem{
			Label: l.ClipMenuKeep(n), Type: mygo.MenuItemRadio, Checked: limit == n, Disabled: a.settingsBroken,
			Click: func(*mygo.MenuItem, *mygo.Window) { a.setClipboardLimit(n) },
		})
	}
	return items
}

// setNowPlaying saves the now-playing preference; the module reads its
// setting on every tick, so a change applies at once without a restart.
// Disabled while settings.json could not be read, so a broken file is
// never overwritten.
func (a *shelf) setNowPlaying(mutate func(*settings.Settings)) {
	next := a.settings
	mutate(&next)
	a.applySettings(next)
}
