//go:build darwin

package main

// The Settings window: Coucou Hub's two-column layout — a pure black
// sidebar of grouped destinations and one rounded, darker panel on the
// right that shows a single page. Every page opens with a hero card (the
// page's name, a line about it, the otter in that page's mood) over
// grouped cards of rows. The window is always dark, like the notch.
//
// Controls call the same setters the tray menus call — applySettings is the
// single save-and-apply path — so window and menu can never diverge; a
// change made in one place repaints the other while it is open. Every
// preference lives in settings.json except the login item and the system
// permissions, which are read back from macOS.

import (
	"fmt"
	"runtime/debug"
	"slices"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"shelf/internal/mac"
)

// Seams around app-level calls so the headless view tests exercise the real
// controls without a running application.
var (
	openAtLogin          = mygo.App.OpenAtLogin
	setOpenAtLogin       = mygo.App.SetOpenAtLogin
	openPath             = mygo.Shell.OpenPath
	openExternal         = mygo.Shell.OpenExternal
	accessibilityRequest = mac.RequestAccessibility
	appVersionProbe      = mygo.App.Version
	setAppLocale         = mygo.App.SetLocale
)

// settingsTheme is the one dark theme the window repaints with: ShelfU's UI
// lives on the pure black notch shell, so Settings is always dark no matter
// the system appearance. Spacing sizes the stock widgets to the design:
// 42×23 switches, 9pt segment tracks and 12.5pt labels.
var settingsTheme = func() *ui.Theme {
	t := ui.DarkTheme()
	t.Background = surface
	t.Accent = accent
	t.AccentHover = t.Accent.Mix(ui.RGB(255, 255, 255), 0.18)
	t.AccentPressed = t.Accent.Mix(ui.RGB(0, 0, 0), 0.16)
	t.Surface = setCtl
	t.SurfaceHover = setCtlHover
	t.SurfacePressed = setCtlActive
	t.Border = setCardEdge
	t.Text = ink
	t.TextMuted = ink2
	t.Danger = danger
	t.Success = success
	t.Focus = ui.RGBA(10, 132, 255, 0.55)
	t.Scrollbar = ui.RGBA(255, 255, 255, 0.18)
	t.ScrollbarWidth = 3
	t.Radius = 9
	// 42/9 makes the theme's 9-space switch 42pt wide and 23pt tall, the
	// mock's 42×24; segments and buttons set their own metrics.
	t.Spacing = 42.0 / 9
	t.FontSize = 12.5
	return t
}()

// The window's colours are theme.go's tokens: the inks and the state
// colours are the panel's, and the settings-only surfaces sit in its
// Settings window group.

// settingsPageID is one destination of the sidebar.
type settingsPageID int

const (
	settingsPageGeneral settingsPageID = iota
	settingsPageNotch
	settingsPageTray
	settingsPageClipboard
	settingsPageAIUsage
	settingsPageShortcuts
	settingsPagePermissions
	settingsPageAbout
)

// settingsSidebarWidth and the metrics below are the design spec's numbers.
const (
	settingsSidebarWidth = 212
	settingsHeroHeight   = 118
	settingsRowMinHeight = 50
	settingsRowPadX      = 16
	settingsRowPadY      = 10
)

// appGlyph fills an excluded-app row when the app has no icon on disk.
var appGlyph = symbol(`<rect x="5" y="5" width="14" height="14" rx="3.5"/>`)

// The sidebar's icons, the design's 24pt line glyphs.
var (
	navGear  = symbol(`<circle cx="12" cy="12" r="3"/><path d="M12 3v2.5M12 18.5V21M4.2 7.5l2.2 1.3M17.6 15.2l2.2 1.3M4.2 16.5l2.2-1.3M17.6 8.8l2.2-1.3"/>`)
	navNotch = symbol(`<rect x="3" y="5" width="18" height="14" rx="3"/><path d="M8.5 5v2.5a1.5 1.5 0 0 0 1.5 1.5h4a1.5 1.5 0 0 0 1.5-1.5V5"/>`)
	navTray  = symbol(`<path d="M4 9v6a5 5 0 0 0 5 5h6a5 5 0 0 0 5-5V9"/><path d="M12 3v9m-3.5-3.5L12 12l3.5-3.5"/>`)
	navClip  = symbol(`<rect x="5" y="5" width="14" height="16" rx="3"/><path d="M9 3.5h6v3H9z"/><path d="M9 11h6M9 15h4"/>`)
	navUsage = symbol(`<path d="M4.5 16a8 8 0 1 1 15 0"/><path d="M12 16l3.5-5"/>`)
	navKey   = symbol(`<rect x="3" y="6" width="18" height="12" rx="3"/><path d="M7 10h.1M11 10h.1M15 10h.1M8 14h8"/>`)
	navHand  = symbol(`<path d="M12 3l7 3v5c0 5-3 8.5-7 10-4-1.5-7-5-7-10V6z"/>`)
	navInfo  = symbol(`<circle cx="12" cy="12" r="8.5"/><path d="M12 11v5.5M12 7.8v.1"/>`)
)

// settingsIcons caches decoded app icons for the 排除的应用 rows; nil values
// are cached too, so a bundle id that resolves to nothing is asked once.
var settingsIcons = map[string]*ui.Bitmap{}

// settingsAppIcon returns the bundle's icon as a bitmap, or nil.
func settingsAppIcon(bundleID string) *ui.Bitmap {
	if icon, ok := settingsIcons[bundleID]; ok {
		return icon
	}
	var icon *ui.Bitmap
	if png := mac.AppIcon(bundleID, 48); len(png) > 0 {
		icon, _ = ui.DecodeBitmap(png)
	}
	settingsIcons[bundleID] = icon
	return icon
}

// openSettings shows the Settings window, creating it the first time, or
// re-focuses the one already open (single instance). The window is a normal
// titled window — not the notch panel — centered on the cursor's display.
// Opening takes the app active and the keyboard, exactly like the
// clipboard-search open: the shelf gives its global Esc/Space back while
// the window is key, and closing hands the keyboard to wherever it belongs
// (settingsReleaseKeyboard) without ever hiding this app — NSApp hide would
// take the notch window away too. The open-at-login state is read back from
// the system every time the window opens or is re-focused, so the switch
// reflects changes the user made in System Settings too.
func (a *shelf) openSettings() {
	if a.settingsWin != nil {
		a.loginOn, a.loginErr = openAtLogin(), ""
		a.repaintSettings()
		mac.ActivateSelf()
		a.settingsWin.Show()
		a.settingsWin.Focus()
		return
	}
	a.settingsTakeKeyboard()
	a.loginOn, a.loginErr = openAtLogin(), ""
	a.settingsRecording = shortcutNone
	frame := settingsFrame(mygo.Screen.DisplayNearestPoint(mygo.Screen.CursorScreenPoint()).WorkArea)
	w := mygo.NewWindow(mygo.WindowOptions{
		Title: a.appLoc().WindowSettings(), Width: settingsWidth, Height: settingsHeight,
		MinWidth: settingsMinWidth, MinHeight: settingsMinHeight,
		X: frame.X, Y: frame.Y,
		Hidden: true, BackgroundColor: "#000000",
		// The mock hides AppKit's title bar and puts the traffic lights in
		// the sidebar: TrafficLightPosition sets their insets, and the
		// window's own name still names it to the system.
		TitleBarStyle:        mygo.TitleBarHidden,
		TrafficLightPosition: &mygo.Point{X: 18, Y: 14},
		DisableMaximize:      true, DisableFullScreen: true,
		Content: ui.View(a.settingsView),
	})
	// The title bar follows the window's own dark chrome whatever the system
	// appearance is, matching the mock's dark "ShelfU 设置" window.
	mac.SetWindowAppearance(w.NativeHandle(), true)
	w.SetBounds(frame)
	w.OnClosed(a.settingsClosed)
	a.settingsWin = w
	w.Show()
	mac.ActivateSelf()
	w.Focus()
}

// settingsTakeKeyboard prepares the handover when the Settings window opens:
// the shelf releases Esc and Space (the window's own Esc/⌘W and its controls
// handle them), and the app the user came from is remembered so closing can
// give the keyboard back.
func (a *shelf) settingsTakeKeyboard() {
	if pid := frontmostPID(); pid > 0 && pid != mac.OwnPID() {
		a.settingsPrevPID = pid
	}
	a.releaseKeys()
}

// settingsClosed runs when the Settings window has closed: the pointer goes
// first (a repaint must never touch a destroyed window), then the keyboard
// goes back to whoever had it before the window opened. A quit closes the
// window too; nothing may be re-registered or re-activated then.
func (a *shelf) settingsClosed() {
	a.stopPermissionFlow()
	a.settingsRecording = shortcutNone
	a.settingsWin = nil
	if a.quitting {
		return
	}
	a.settingsReleaseKeyboard()
}

// settingsKeyboardExit is who gets the keyboard when the Settings window
// closes: the panel when it holds it for clipboard search, the shelf itself
// (Esc/Space again) when the panel is merely open, else the app the user
// came from. Pure; unit-tested.
type settingsKeyboardExit int

const (
	settingsToPanel settingsKeyboardExit = iota
	settingsToShelf
	settingsToPrevApp
)

func (a *shelf) settingsKeyboardExit() settingsKeyboardExit {
	switch {
	case a.focusMode:
		return settingsToPanel
	case a.visible:
		return settingsToShelf
	default:
		return settingsToPrevApp
	}
}

// settingsReleaseKeyboard gives the keyboard back when the Settings window
// closes, and never hides this app: an NSApp hide would take the notch
// window with it.
func (a *shelf) settingsReleaseKeyboard() {
	prev := a.settingsPrevPID
	a.settingsPrevPID = 0
	switch a.settingsKeyboardExit() {
	case settingsToPanel:
		// Clipboard search: the panel's search field must keep the
		// keyboard, so the app we came from must not take it back.
		if a.win != nil {
			a.win.Focus()
		}
		return
	case settingsToShelf:
		a.holdKeys()
	}
	a.handBackApp(prev)
}

// repaintSettings redraws the Settings window when it is open, used after a
// change that did not go through applySettings (login item, accessibility,
// a finished update check).
func (a *shelf) repaintSettings() {
	if a.settingsWin != nil {
		a.settingsWin.Invalidate()
	}
}

// settingsFrame centers the settings window inside the cursor display's
// work area (menu bar and Dock stay clear). Pure; unit-tested.
func settingsFrame(work mygo.Rectangle) mygo.Rectangle {
	x := work.X + (work.Width-settingsWidth)/2
	y := work.Y + (work.Height-settingsHeight)/2
	x = min(max(x, work.X), max(work.X, work.X+work.Width-settingsWidth))
	y = min(max(y, work.Y), max(work.Y, work.Y+work.Height-settingsHeight))
	return mygo.Rectangle{X: x, Y: y, Width: settingsWidth, Height: settingsHeight}
}

// setSettingsPage shows one page and starts it at the top, as a sidebar
// click does.
func (a *shelf) setSettingsPage(page settingsPageID) {
	if a.settingsPage == page {
		return
	}
	a.settingsPage = page
	a.settingsScroll.Y = 0
	a.settingsRecording = shortcutNone
	a.repaintSettings()
}

// settingsView draws the whole Settings window each frame: the sidebar, the
// page panel, and — while a shortcut records — the key-capture layer over
// everything. While settings.json is broken a banner on every page explains
// it and every preference control is disabled so the file is never
// rewritten.
func (a *shelf) settingsView(c *ui.Context) {
	c.SetTheme(settingsTheme)
	c.Root().Background(surface)
	recording := a.settingsRecording != shortcutNone
	if a.settingsWin != nil {
		w := a.settingsWin
		// While a shortcut records, every key belongs to the recorder;
		// closing on Esc would swallow the cancel.
		if !recording {
			c.OnShortcut(0, ui.KeyEscape, w.Close)
			c.OnShortcut(ui.Cmd, ui.KeyW, w.Close)
		}
	}
	ui.Row(c.Key("win")).Fill().Children(func() {
		a.settingsSidebar(c)
		ui.Column(c.Key("panel")).Grow(1).FillHeight().Margin(10, 10, 10, 0).
			Radius(20).Background(setPanel).Clip().Children(func() {
			ui.Scroll(c.Key("page-scroll")).Fill().TrackScroll(&a.settingsScroll).Children(func() {
				ui.Column(c.Key("page")).FillWidth().Padding(14, 18, 22, 18).Gap(18).Children(func() {
					if a.settingsBroken {
						settingsBrokenBanner(c)
					}
					a.settingsPageBody(c)
				})
			})
		})
		if recording {
			a.settingsCaptureLayer(c)
		}
	})
}

// settingsSidebar draws the pure black sidebar: the traffic lights' row, the
// brand (a way to 关于), the grouped destinations, and the permission status
// at the bottom.
func (a *shelf) settingsSidebar(c *ui.Context) {
	l := loc(c)
	ui.Column(c.Key("side")).Width(settingsSidebarWidth).FillHeight().Background(surface).
		Padding(0, 12, 12, 12).Children(func() {
		// The traffic lights are AppKit's, over this row; the row drags
		// the window, as a title bar would.
		ui.Box(c.Key("lights")).Height(40).FillWidth().DragWindow()

		brand := ui.ButtonBase(c.Key("brand")).Height(44).FillWidth().Radius(9).
			Padding(0, 8, 0, 8).AlignItems(ui.Center).Justify(ui.Start).Gap(8).Label(appName)
		if brand.Hovered() {
			brand.Background(fill1)
		}
		brand.Children(func() {
			if b := otterImage("neutral"); b != nil {
				ui.Image(c.Key("otter"), b).Size(30, 30).Fit(ui.Contain)
			}
			ui.Text(c, appName).SingleLine().FontSize(17).FontWeight(700).TextColor(ink)
		})
		if brand.Clicked() {
			a.setSettingsPage(settingsPageAbout)
		}
		ui.Box(c.Key("brand-gap")).Height(6)

		a.settingsNavItem(c, settingsPageGeneral, navGear, l.General())
		settingsNavGroup(c, l.SettingsGroupNotch())
		a.settingsNavItem(c, settingsPageNotch, navNotch, l.SettingsNotchNav())
		settingsNavGroup(c, l.SettingsGroupContent())
		a.settingsNavItem(c, settingsPageTray, navTray, l.SettingsTraySection())
		a.settingsNavItem(c, settingsPageClipboard, navClip, l.SettingsClipboardSection())
		a.settingsNavItem(c, settingsPageAIUsage, navUsage, l.SettingsAISection())
		settingsNavGroup(c, l.SettingsGroupSystem())
		a.settingsNavItem(c, settingsPageShortcuts, navKey, l.SettingsShortcutsSection())
		a.settingsNavItem(c, settingsPagePermissions, navHand, l.PermTitle())
		a.settingsNavItem(c, settingsPageAbout, navInfo, l.SettingsAboutPage())

		ui.Box(c.Key("side-grow")).Grow(1)
		a.settingsPermissionStatus(c)
	})
}

// settingsNavGroup is a sidebar group label: 10.5pt, bold, letterspaced,
// white 40%.
func settingsNavGroup(c *ui.Context, title string) {
	ui.Text(c, title).SingleLine().FontSize(10.5).FontWeight(700).LetterSpacing(1.2).
		TextColor(setLabel).Padding(10, 10, 4, 10)
}

// settingsNavItem is one destination: 32pt tall, 9pt corners, the selected
// one on #1D1F23 in white, the others white 62%.
func (a *shelf) settingsNavItem(c *ui.Context, page settingsPageID, icon *ui.SVG, label string) {
	on := a.settingsPage == page
	color := ink2
	if on {
		color = ink
	}
	item := ui.ButtonBase(c.Key(fmt.Sprintf("nav-%d", page))).Height(32).FillWidth().Radius(9).
		Padding(0, 10, 0, 10).AlignItems(ui.Center).Justify(ui.Start).Gap(10).Label(label)
	if on {
		item.Background(setSelect)
	} else if item.Hovered() {
		item.Background(fill1)
	}
	item.Children(func() {
		ui.Icon(c.Key("i"), icon).Size(16, 16).TextColor(color)
		ui.Text(c, label).SingleLine().FontSize(13.5).TextColor(color)
	})
	if item.Clicked() {
		a.setSettingsPage(page)
	}
}

// settingsPermissionStatus is the sidebar's footer: a green dot when every
// permission is on, an orange one and a count when not; it opens the 系统权限
// page.
func (a *shelf) settingsPermissionStatus(c *ui.Context) {
	l := loc(c)
	missing := len(missingSystemPermissions())
	dot := success
	if missing > 0 {
		dot = warning
	}
	text := l.SettingsPermStatusOn()
	if missing > 0 {
		text = l.SettingsPermStatusMissing(missing)
	}
	row := ui.ButtonBase(c.Key("perm-status")).Height(30).FillWidth().Radius(9).
		Padding(0, 10, 0, 10).Background(setStatus).AlignItems(ui.Center).Gap(7).Label(text)
	if row.Hovered() {
		row.Background(fill1)
	}
	row.Children(func() {
		ui.Box(c.Key("dot")).Size(7, 7).Radius(4).Background(dot)
		ui.Text(c, text).SingleLine().FontSize(12).TextColor(ink2).Grow(1).MinWidth(0)
	})
	if row.Clicked() {
		a.setSettingsPage(settingsPagePermissions)
	}
}

// settingsBrokenBanner explains an unreadable settings.json above every
// page's content: the controls below it are locked.
func settingsBrokenBanner(c *ui.Context) {
	l := loc(c)
	ui.Column(c.Key("broken")).Padding(10, 12).Gap(4).Radius(12).
		Background(danger.Alpha(0.12)).
		Border(0.5, danger.Alpha(0.35)).Children(func() {
		ui.Text(c, l.SettingsBrokenTitle()).FontSize(12).FontWeight(600).TextColor(danger)
		ui.Text(c, l.SettingsBrokenBody()).FontSize(11).TextColor(ink3)
	})
}

// settingsPageBody dispatches to the page the sidebar selected.
func (a *shelf) settingsPageBody(c *ui.Context) {
	switch a.settingsPage {
	case settingsPageGeneral:
		a.settingsGeneralPage(c)
	case settingsPageNotch:
		a.settingsNotchPage(c)
	case settingsPageTray:
		a.settingsTrayPage(c)
	case settingsPageClipboard:
		a.settingsClipboardPage(c)
	case settingsPageAIUsage:
		a.settingsAIUsagePage(c)
	case settingsPageShortcuts:
		a.settingsShortcutsPage(c)
	case settingsPagePermissions:
		a.settingsPermissionsPage(c)
	case settingsPageAbout:
		a.settingsAboutPage(c)
	}
}

// settingsHero paints one page's title card: a 118pt gradient with a glow
// and sparse stars, the page's name and a line about it on the left, and
// the otter in the page's mood standing at the right, cut by the card's
// bottom edge. A missing otter picture leaves the card as it is.
func settingsHero(c *ui.Context, key, title, subtitle, mood string, from, to, glow ui.Color) {
	box := ui.Box(c.Key("hero-" + key)).Height(settingsHeroHeight).FillWidth().Radius(16).Clip().
		LinearGradient(ui.LinearGradient{From: from, To: to, Angle: 135})
	box.Draw(func(p *ui.Painter, r ui.Rect) {
		p.Clip(r, 16, func() {
			// The glow: a radial falloff (bottom right, past the corner),
			// built from concentric fills that accumulate to a soft light.
			cx, cy := r.X+r.W*0.8, r.Y+r.H*1.2
			for i := range 14 {
				rad := float32(16 + i*11)
				p.Fill(ui.Rect{X: cx - rad, Y: cy - rad, W: rad * 2, H: rad * 2}, glow.Alpha(0.026), rad)
			}
			// The sparse stars of the design, at its own places.
			stars := [][3]float32{
				{0.12, 0.30, 0.55}, {0.28, 0.72, 0.35}, {0.46, 0.22, 0.45},
				{0.63, 0.64, 0.30}, {0.78, 0.28, 0.50}, {0.88, 0.80, 0.35},
			}
			for _, s := range stars {
				d := float32(1.1)
				if s[2] > 0.45 {
					d = 1.6
				}
				p.Fill(ui.Rect{X: r.X + r.W*s[0], Y: r.Y + r.H*s[1], W: d, H: d}, ui.RGBA(255, 255, 255, s[2]), d/2)
			}
		})
	})
	box.Children(func() {
		ui.Text(c, title).SingleLine().Absolute().Left(22).Top(24).
			FontSize(26).FontWeight(700).TextColor(ink)
		ui.Text(c, subtitle).Absolute().Left(22).Top(64).MaxWidth(330).
			FontSize(12.5).TextColor(ink2)
		if b := otterImage(mood); b != nil {
			w, h := b.Size()
			height := float32(96)
			width := height * float32(w) / float32(h)
			ui.Image(c.Key("otter"), b).Absolute().Right(22).Bottom(-6).
				Size(width, height).Fit(ui.Contain)
		}
	})
}

// settingsGroup is a section of a page: the letterspaced label, the card,
// and an optional note under it. mt is the space above the label.
func settingsGroup(c *ui.Context, key, title, foot string, fn func()) {
	ui.Column(c.Key("grp-" + key)).FillWidth().Gap(8).Children(func() {
		ui.Text(c, title).SingleLine().FontSize(10.5).FontWeight(700).LetterSpacing(1.2).
			TextColor(setLabel).Padding(0, 4, 0, 4)
		ui.Column(c.Key("card-"+key)).Radius(14).Background(setCard).
			Border(1, setCardEdge).FillWidth().Children(fn)
		if foot != "" {
			ui.Text(c, foot).FontSize(11.5).TextColor(ink3).Padding(0, 6, 0, 6).MaxWidth(520)
		}
	})
}

// settingsSep is the 1pt hairline between two rows, inset like the row's
// text (16pt on both sides).
func settingsSep(c *ui.Context, key string) {
	ui.Row(c.Key("sep-"+key)).FillWidth().Padding(0, settingsRowPadX).Children(func() {
		ui.Box(c.Key("line")).Height(1).FillWidth().Background(setSep)
	})
}

// settingsRowLine is one row's frame: the design's 50pt minimum, 10pt
// vertical and 16pt horizontal padding, 14pt between text and control.
func settingsRowLine(c *ui.Context, key string) ui.Element {
	return ui.Row(c.Key("row-"+key)).FillWidth().MinHeight(settingsRowMinHeight).
		AlignItems(ui.Center).Gap(14).Padding(settingsRowPadY, settingsRowPadX)
}

// settingsRowText is a row's left column: the title at 13.5pt semibold and
// the optional caption at 11.5pt white 48%.
func settingsRowText(c *ui.Context, label, caption string) {
	ui.Column(c.Key("text")).Gap(2).Grow(1).MinWidth(0).Children(func() {
		settingsLabel(c, label, 13.5, 600).TextColor(ink)
		if caption != "" {
			ui.Text(c, caption).FontSize(11.5).TextColor(ink3)
		}
	})
}

// settingsFit, when a test installs it, is called with each single-line
// label whose words need more width than the box the layout gave it — the
// settings window's counterpart of the panel's fitIssue. Production leaves
// it nil and pays one nil check.
var settingsFit func(string)

// settingsLabel is a single-line label of the Settings window. While a test
// measures (settingsFit is set), it reports a label that cannot fit its box,
// the way panelLabel does for the panel; a caption is not measured here
// because it wraps on purpose and the row grows with it.
func settingsLabel(c *ui.Context, text string, size float32, weight int) ui.Element {
	t := ui.Text(c, text).SingleLine().FontSize(size)
	if weight != 0 {
		t.FontWeight(weight)
	}
	if settingsFit != nil {
		if b := t.Bounds(); b.W > 0 {
			w, _ := c.MeasureText(0, ui.Span{Text: text, Size: size, Weight: weight})
			if w > b.W+0.5 {
				settingsFit(fmt.Sprintf("%q needs %.1fpt, box is %.1fpt (size %g): the label is cut", text, w, b.W, size))
			}
		}
	}
	return t
}

// settingsToggleRow is one switch row: the label (and optional caption) on
// the left, a macOS-style switch on the right. The row itself is the
// toggle — a click anywhere on it flips the switch — and the inner switch
// mirrors the same value, so the two bindings can never fight. apply
// receives the new state only when the user actually changed it.
func settingsToggleRow(c *ui.Context, key, label, caption string, on bool, apply func(bool)) {
	settingsToggleRowCap(c, key, label, caption, ink3, on, apply)
}

// settingsToggleRowCap is settingsToggleRow with a caption color of its own:
// the login item shows a failure in red there.
func settingsToggleRowCap(c *ui.Context, key, label, caption string, captionColor ui.Color, on bool, apply func(bool)) {
	row := ui.ToggleBase(c.Key("toggle-"+key), &on).FillWidth().AlignItems(ui.Center).
		Gap(14).MinHeight(settingsRowMinHeight).Padding(settingsRowPadY, settingsRowPadX).Label(label)
	var sw ui.Element
	row.Children(func() {
		ui.Column(c.Key("text")).Gap(2).Grow(1).MinWidth(0).Children(func() {
			settingsLabel(c, label, 13.5, 600).TextColor(ink)
			if caption != "" {
				ui.Text(c, caption).FontSize(11.5).TextColor(captionColor)
			}
		})
		sw = ui.Switch(c, &on).Label(label)
	})
	if row.Changed() || sw.Changed() {
		apply(on)
	}
}

// settingsRow adds a row whose control fn builds on the right.
func settingsRow(c *ui.Context, key, label, caption string, fn func()) {
	settingsRowLine(c, key).Children(func() {
		settingsRowText(c, label, caption)
		fn()
	})
}

// settingsSection is the older name the 重新命名 window builds its form
// with: a titled card whose rows can be locked as a whole.
func settingsSection(c *ui.Context, title, foot string, disabled bool, fn func()) {
	settingsGroup(c, title, title, foot, func() {
		ui.Column(c.Key("rows-" + title)).FillWidth().Disabled(disabled).Children(fn)
	})
}

// settingsGroupRows wraps a group of rows that settings.json backs: it is
// locked (and dimmed) while the file is broken, so it is never overwritten.
func settingsGroupRows(c *ui.Context, key, title, foot string, disabled bool, fn func()) {
	settingsGroup(c, key, title, foot, func() {
		ui.Column(c.Key("rows-" + key)).FillWidth().Disabled(disabled).Children(fn)
	})
}

// settingsButton is the design's 28pt control: #1F2024 with white text, or
// white with black text for the primary action.
func settingsButton(c *ui.Context, key, label string, primary bool, fn func()) ui.Element {
	b := ui.ButtonBase(c.Key("btn-"+key)).Height(28).Radius(8).Padding(0, 12, 0, 12).
		AlignItems(ui.Center).Label(label)
	fg := ink
	if primary {
		b.Background(ink)
		fg = ui.RGB(0, 0, 0)
	} else {
		b.Background(setCtl)
		if b.Hovered() {
			b.Background(setCtlHover)
		}
	}
	b.Children(func() {
		t := ui.Text(c, label).SingleLine().FontSize(12.5).TextColor(fg)
		if primary {
			t.FontWeight(600)
		}
	})
	if b.Clicked() {
		fn()
	}
	return b
}

// settingsSegmented is a segmented control named after its row.
func settingsSegmented(c *ui.Context, key, label string, selected *int, labels ...string) ui.Element {
	return ui.Segmented(c.Key("seg-"+key), selected, labels...).Label(label)
}

// choiceSegments turns an integer preference and its usual choices into a
// segmented control's data: labels, the values they stand for (sorted, with
// the current value inserted if the user set something the presets do not
// offer), and the selected index. Pure; unit-tested.
func choiceSegments(current int, choices []int, format func(int) string) (labels []string, values []int, selected int) {
	values = slices.Clone(choices)
	if !slices.Contains(values, current) {
		values = append(values, current)
	}
	slices.Sort(values)
	selected = slices.Index(values, current)
	for _, v := range values {
		labels = append(labels, format(v))
	}
	return labels, values, selected
}

// languageIndex maps a saved language to the segmented control's segment:
// 0 follows the system, 1 is zh-Hans, 2 is English.
func languageIndex(tag string) int {
	switch tag {
	case "zh-Hans":
		return 1
	case "en":
		return 2
	}
	return 0
}

// languageAt maps the segmented control's segment to a saved language.
func languageAt(idx int) string {
	switch idx {
	case 1:
		return "zh-Hans"
	case 2:
		return "en"
	}
	return ""
}

// setLanguage saves the chosen language and shows it at once: settings.json
// is the source of truth, and mygo.App.SetLocale redraws every window in it
// without a restart. Follow System runs the same rule a launch does, so
// choosing it shows the language the system asks for — a Chinese system
// Chinese, every other English — rather than the source language mygo would
// fall back to.
func (a *shelf) setLanguage(tag string) {
	tag = languageTag(tag)
	if tag == a.settings.Language {
		return
	}
	next := a.settings
	next.Language = tag
	if !a.applySettings(next) {
		return
	}
	a.showLanguage()
}

// applyOpenAtLogin switches the login item through SMAppService (macOS 13+)
// and keeps the shown state and any failure visible inline — running from
// outside a signed app bundle, for example, reports the error here instead
// of silently reverting the switch. Whatever the call returned, the switch
// then shows what the system actually reports (an SMAppService registration
// may still need the user's approval in System Settings), so it can never
// claim a state that is not real.
func (a *shelf) applyOpenAtLogin(on bool) {
	err := setOpenAtLogin(on)
	a.loginOn = openAtLogin()
	if err != nil {
		a.loginErr = err.Error()
	} else {
		a.loginErr = ""
	}
	a.repaintSettings()
}

// settingsFootnote is the muted note under a page's last card.
func settingsFootnote(c *ui.Context, key, text string) {
	ui.Text(c, text).FontSize(11.5).TextColor(ink3).Padding(0, 6, 0, 6).MaxWidth(520)
}

// appVersion is the bundle version (mygo.json's version → Info.plist) when
// packaged, else the module version of a source build, else "dev". Pure
// apart from the package-level probe; unit-tested.
func appVersion() string {
	if v := appVersionProbe(); v != "" {
		return v
	}
	if info, ok := debug.ReadBuildInfo(); ok && info.Main.Version != "" && info.Main.Version != "(devel)" {
		return info.Main.Version
	}
	return "dev"
}
