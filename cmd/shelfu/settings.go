//go:build darwin

package main

// The Settings window gathers every preference the tray submenus and
// settings.json spread over the app into one normal titled window. Its
// controls call the same setters the menus do — applySettings is the single
// save-and-apply path — so window and menu can never diverge; a change made
// in one place repaints the other while it is open.

import (
	"fmt"
	"runtime/debug"
	"slices"
	"strconv"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"shelf/internal/mac"
	"shelf/internal/settings"
)

// Seams around app-level calls so the headless view tests exercise the real
// controls without a running application.
var (
	openAtLogin          = mygo.App.OpenAtLogin
	setOpenAtLogin       = mygo.App.SetOpenAtLogin
	openPath             = mygo.Shell.OpenPath
	accessibilityRequest = mac.RequestAccessibility
	appVersionProbe      = mygo.App.Version
	setAppLocale         = mygo.App.SetLocale
)

// settingsTheme is the one dark theme the window repaints with: ShelfU's UI
// lives on the pure black notch shell, so Settings is always dark no matter
// the system appearance. It carries the DESIGN.md palette: system-blue
// accent, white-alpha surfaces and the compact metrics of the mock's
// grouped lists (32pt switches, white-8% segment tracks).
var settingsTheme = func() *ui.Theme {
	t := ui.DarkTheme()
	t.Background = ui.Hex("#1E1E1F")
	t.Accent = ui.Hex("#0A84FF")
	t.AccentHover = t.Accent.Mix(ui.RGB(255, 255, 255), 0.18)
	t.AccentPressed = t.Accent.Mix(ui.RGB(0, 0, 0), 0.16)
	t.Surface = ui.RGBA(255, 255, 255, 0.08)
	t.SurfaceHover = ui.RGBA(255, 255, 255, 0.16)
	t.SurfacePressed = ui.RGBA(255, 255, 255, 0.20)
	t.Border = ui.RGBA(255, 255, 255, 0.07)
	t.Text = setInk
	t.TextMuted = setInk2
	t.Danger = ui.Hex("#FF453A")
	t.Success = ui.Hex("#30D158")
	t.Focus = ui.RGBA(10, 132, 255, 0.55)
	t.Scrollbar = ui.RGBA(255, 255, 255, 0.28)
	t.ScrollbarWidth = 4
	t.Radius = 7
	// The switch sizes off Spacing: 3.5 gives the mock's ~32x19. FontSize 12
	// is what the mock's segments and small buttons draw; every Text in the
	// window sets its own size.
	t.Spacing = 3.5
	t.FontSize = 12
	return t
}()

// The settings window's text and surface colors, straight from DESIGN.md's
// token table (white-alpha inks on the #1E1E1F window).
var (
	setInk      = ui.Hex("#F5F5F7")
	setInk2     = ui.RGBA(255, 255, 255, 0.56)
	setInk3     = ui.RGBA(255, 255, 255, 0.34)
	setCard     = ui.RGBA(255, 255, 255, 0.05)
	setCardEdge = ui.RGBA(255, 255, 255, 0.07)
	setRowSep   = ui.RGBA(255, 255, 255, 0.08)
)

// appGlyph fills an excluded-app row when the app has no icon on disk.
var appGlyph = symbol(`<rect x="5" y="5" width="14" height="14" rx="3.5"/>`)

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
	frame := settingsFrame(mygo.Screen.DisplayNearestPoint(mygo.Screen.CursorScreenPoint()).WorkArea)
	w := mygo.NewWindow(mygo.WindowOptions{
		Title: a.appLoc().WindowSettings(), Width: settingsWidth, Height: settingsHeight,
		X: frame.X, Y: frame.Y,
		Hidden: true, BackgroundColor: "#1E1E1F",
		// The mock's title bar centers the title, which AppKit's stock bar
		// never does: hide it and let settingsView draw its own under the
		// traffic lights, which TrafficLightPosition puts at the mock's
		// insets. The title still names the window to the system.
		TitleBarStyle:        mygo.TitleBarHidden,
		TrafficLightPosition: &mygo.Point{X: 9, Y: 12},
		DisableResize:        true, DisableMaximize: true, DisableFullScreen: true,
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
// change that did not go through applySettings (login item, accessibility).
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

// settingsView draws the whole Settings window each frame: DESIGN.md's
// grouped-inset-list look — a small muted heading over a card of 40pt rows
// split by hairlines — then a footer with the version and the data-folder
// button. While settings.json is broken a banner explains it and every
// preference control is disabled so the file is never rewritten.
func (a *shelf) settingsView(c *ui.Context) {
	c.SetTheme(settingsTheme)
	c.Root().Background(ui.Hex("#1E1E1F"))
	if a.settingsWin != nil {
		w := a.settingsWin
		c.OnShortcut(0, ui.KeyEscape, w.Close)
		c.OnShortcut(ui.Cmd, ui.KeyW, w.Close)
	}
	ui.Column(c.Key("win")).Fill().Children(func() {
		// The drawn title bar: traffic lights sit over it (TrafficLight-
		// Position, or wherever c.TitleBar reports them), the title centers
		// like the mock's, and the bar drags the window. The side padding is
		// symmetric: padding only the controls' side would centre the title
		// in the remaining space and shift it away from the window centre.
		bar := c.TitleBar()
		edge := max(bar.Left, bar.Right)
		ui.Row(c.Key("titlebar")).Height(float32(max(bar.Height, 38))).FillWidth().
			AlignItems(ui.Center).Justify(ui.Center).Padding(0, edge, 0, edge).
			DragWindow().Children(func() {
			ui.Text(c, loc(c).WindowSettings()).SingleLine().FontSize(13).FontWeight(600).TextColor(setInk2)
		})
		ui.Scroll(c.Key("settings")).Fill().TrackScroll(&a.settingsScroll).Children(func() {
			ui.Column(c.Key("sections")).Padding(2, 20, 16, 20).Gap(16).FillWidth().Children(func() {
				if a.settingsBroken {
					ui.Column(c.Key("broken")).Padding(10, 12).Gap(4).Radius(10).
						Background(ui.RGBA(255, 69, 58, 0.12)).
						Border(0.5, ui.RGBA(255, 69, 58, 0.35)).Children(func() {
						l := loc(c)
						ui.Text(c, l.SettingsBrokenTitle()).FontSize(12).FontWeight(600).TextColor(settingsTheme.Danger)
						ui.Text(c, l.SettingsBrokenBody()).FontSize(11).TextColor(setInk3)
					})
				}
				a.settingsPermissionsSection(c)
				a.settingsGeneralSection(c)
				a.settingsLiveSection(c)
				a.settingsAIUsageSection(c)
				a.settingsClipboardSection(c)
				a.settingsTraySection(c)
				a.settingsShortcutsSection(c)
				a.settingsFooter(c)
			})
		})
	})
}

// settingsSection renders one group: an 11pt semibold heading over a
// rounded card (white 5% fill, 0.5pt edge) holding the group's rows.
// disabled greys and locks the whole card. foot, when not empty, is the
// System Settings-style note under the card, aligned with the row text.
// title is the heading the user reads, in the window's language, and also
// names the group's element.
func settingsSection(c *ui.Context, title, foot string, disabled bool, fn func()) {
	ui.Column(c.Key("sec-" + title)).FillWidth().Children(func() {
		ui.Text(c, title).SingleLine().FontSize(11).FontWeight(600).TextColor(setInk3).Padding(0, 0, 6, 4)
		ui.Column(c.Key("card")).Radius(10).Background(setCard).Border(0.5, setCardEdge).
			Disabled(disabled).FillWidth().Children(fn)
		if foot != "" {
			ui.Text(c, foot).FontSize(11).TextColor(setInk3).Padding(6, 12, 0, 12)
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

// settingsRowPadY is a row's vertical padding. A row is 40pt for one line of
// text; a caption that wraps grows the row by its extra lines and keeps this
// much air above and below, so a second line never presses the hairline or
// the next row.
const settingsRowPadY = 4

// settingsSep is the 0.5pt hairline between two rows of one card.
func settingsSep(c *ui.Context, key string) {
	ui.Box(c.Key("sep-" + key)).Height(0.5).FillWidth().Background(setRowSep)
}

// settingsToggle is one switch row: the label (and optional caption) on the
// left, a macOS-style switch on the right. The row itself is the toggle — a
// click anywhere on it flips the switch — and the inner switch mirrors the
// same value, so the two bindings can never fight (a click has exactly one
// target). apply receives the new state only when the user actually changed
// it.
func settingsToggle(c *ui.Context, key, label string, on bool, apply func(bool)) {
	settingsToggleCap(c, key, label, "", setInk3, on, apply)
}

// settingsToggleCap is settingsToggle with a caption under the label;
// captionColor lets a row show an error (the login item's) in danger red.
func settingsToggleCap(c *ui.Context, key, label, caption string, captionColor ui.Color, on bool, apply func(bool)) {
	row := ui.ToggleBase(c.Key("toggle-"+key), &on).FillWidth().AlignItems(ui.Center).Gap(10).MinHeight(40).Padding(settingsRowPadY, 12).Label(label)
	var sw ui.Element
	row.Children(func() {
		ui.Column(c.Key("k")).Gap(2).Grow(1).MinWidth(0).Children(func() {
			settingsLabel(c, label, 13, 0).TextColor(setInk)
			if caption != "" {
				ui.Text(c, caption).FontSize(11).TextColor(captionColor)
			}
		})
		sw = ui.Switch(c, &on).Label(label)
	})
	if row.Changed() || sw.Changed() {
		apply(on)
	}
}

// settingsRow is a plain label/value line inside a section, at least 40pt
// tall with the mock's 12pt side padding.
func settingsRow(c *ui.Context, key, label string, fn func()) {
	settingsRowCap(c, key, label, "", fn)
}

// settingsRowCap is settingsRow with an 11pt muted caption under the label.
func settingsRowCap(c *ui.Context, key, label, caption string, fn func()) {
	ui.Row(c.Key("row-"+key)).AlignItems(ui.Center).Gap(10).MinHeight(40).Padding(settingsRowPadY, 12).FillWidth().Children(func() {
		ui.Column(c.Key("k")).Gap(2).Grow(1).MinWidth(0).Children(func() {
			settingsLabel(c, label, 13, 0).TextColor(setInk)
			if caption != "" {
				ui.Text(c, caption).FontSize(11).TextColor(setInk3)
			}
		})
		fn()
	})
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

// settingsGeneralSection: 开机时自动启动 and 自动检查更新. Only 自动检查更新
// lives in settings.json; the login item stays editable while the file is
// broken.
func (a *shelf) settingsGeneralSection(c *ui.Context) {
	l := loc(c)
	settingsSection(c, l.General(), "", false, func() {
		loginCap, capColor := "", setInk3
		if a.loginErr != "" {
			loginCap, capColor = a.loginErr, settingsTheme.Danger
		}
		settingsToggleCap(c, "login", l.SettingsLoginRow(), loginCap, capColor, a.loginOn, a.applyOpenAtLogin)
		settingsSep(c, "login")
		// 自动检查更新存 settings.json，文件损坏时随其余偏好一起锁定；
		// 登录项不依赖该文件，保持可用。
		ui.Column(c.Key("autocheck")).Disabled(a.settingsBroken).FillWidth().Children(func() {
			settingsToggleCap(c, "autocheck", l.SettingsAutoCheckRow(), l.SettingsAutoCheckCaption(), setInk3,
				a.settings.AutoCheckUpdates, a.setAutoCheckUpdates)
		})
		settingsSep(c, "shellstyle")
		ui.Column(c.Key("shellstyle")).Disabled(a.settingsBroken).FillWidth().Children(func() {
			settingsRowCap(c, "shellstyle", l.ShellStyleRow(), l.ShellStyleCaption(), func() {
				style := 0
				if a.settings.ShellStyle.Normalized() == settings.ShellStyleIsland {
					style = 1
				}
				if sg := ui.Segmented(c, &style, l.ShellStyleNotch(), l.ShellStyleIsland()).Label(l.ShellStyleRow()); sg.Changed() {
					next := settings.ShellStyleNotch
					if style == 1 {
						next = settings.ShellStyleIsland
					}
					a.setShellStyle(next)
				}
			})
		})
		settingsSep(c, "language")
		ui.Column(c.Key("language")).Disabled(a.settingsBroken).FillWidth().Children(func() {
			settingsRowCap(c, "language", l.LanguageRow(), l.LanguageCaption(), func() {
				idx := languageIndex(a.settings.Language)
				if sg := ui.Segmented(c, &idx, l.LanguageFollowSystem(), l.LanguageChinese(), l.LanguageEnglish()).
					Label(l.LanguageRow()); sg.Changed() {
					a.setLanguage(languageAt(idx))
				}
			})
		})
		if a.island {
			settingsSep(c, "islandpos")
			ui.Column(c.Key("islandpos")).Disabled(a.settingsBroken).FillWidth().Children(func() {
				settingsRowCap(c, "islandpos", l.SettingsIslandPosRow(), l.SettingsIslandPosCaption(), func() {
					if ui.Button(c, l.SettingsIslandReset()).FontSize(12).Label(l.SettingsIslandReset()).Clicked() {
						a.resetIslandPosition()
					}
				})
			})
		}
		settingsSep(c, "snap")
		ui.Column(c.Key("snap")).Disabled(a.settingsBroken).FillWidth().Children(func() {
			settingsToggleCap(c, "snap", l.SettingsSnapRow(), l.SettingsSnapCaption(), setInk3,
				a.settings.SnapWindows, a.setSnapWindows)
		})
	})
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

// settingsLiveSection is the 实时活动 group: charging, headphones and
// downloads, all on for a fresh install. The switches go through the same
// applySettings path as every other preference; the modules read them on
// each tick, so a change applies at once.
func (a *shelf) settingsLiveSection(c *ui.Context) {
	l := loc(c)
	settingsSection(c, l.SettingsLiveSection(), "", a.settingsBroken, func() {
		settingsToggle(c, "live-charging", l.SettingsLiveCharging(), a.settings.LiveActivities.Charging, func(on bool) {
			a.setLiveActivity(func(s *settings.Settings) { s.LiveActivities.Charging = on })
		})
		settingsSep(c, "live-charging")
		settingsToggle(c, "live-headphones", l.SettingsLiveHeadphones(), a.settings.LiveActivities.Headphones, func(on bool) {
			a.setLiveActivity(func(s *settings.Settings) { s.LiveActivities.Headphones = on })
		})
		settingsSep(c, "live-headphones")
		settingsToggle(c, "live-downloads", l.SettingsLiveDownloads(), a.settings.LiveActivities.Downloads, func(on bool) {
			a.setLiveActivity(func(s *settings.Settings) { s.LiveActivities.Downloads = on })
		})
	})
}

// setLiveActivity saves one 实时活动 switch and repaints.
func (a *shelf) setLiveActivity(mutate func(*settings.Settings)) {
	next := a.settings
	mutate(&next)
	a.applySettings(next)
}

// settingsClipboardSection contains the retained count and excluded apps,
// through the same setters the tray menu uses. Recording is always enabled.
func (a *shelf) settingsClipboardSection(c *ui.Context) {
	l := loc(c)
	settingsSection(c, l.SettingsClipboardSection(), "", a.settingsBroken, func() {
		limit := a.settings.ClipboardLimit
		if limit < 1 {
			limit = settings.Default().ClipboardLimit
		}
		labels, values, selected := choiceSegments(limit, clipboardLimitChoices, strconv.Itoa)
		settingsRow(c, "limit", l.SettingsClipboardLimitRow(), func() {
			if sg := ui.Segmented(c, &selected, labels...).Label(l.SettingsClipboardLimitRow()); sg.Changed() {
				a.setClipboardLimit(values[selected])
			}
		})
		settingsSep(c, "limit")
		settingsRowCap(c, "excl", l.SettingsClipboardExcludedRow(), l.SettingsClipboardExcludedCaption(), func() {
			if len(a.settings.ExcludedApps) == 0 {
				ui.Text(c, l.SettingsClipboardExcludedNone()).SingleLine().FontSize(12).TextColor(setInk3)
			}
		})
		for _, id := range a.settings.ExcludedApps {
			bundleID := id
			settingsSep(c, "excl-"+bundleID)
			ui.Row(c.Key("app-"+bundleID)).AlignItems(ui.Center).Gap(10).MinHeight(40).Padding(settingsRowPadY, 12).FillWidth().Children(func() {
				if icon := settingsAppIcon(bundleID); icon != nil {
					ui.Image(c.Key("ic"), icon).Size(20, 20).Radius(4.5)
				} else {
					ui.Icon(c.Key("ic"), appGlyph).Size(20, 20).TextColor(setInk3)
				}
				ui.Column(c.Key("id")).Gap(1).Grow(1).MinWidth(0).Children(func() {
					settingsLabel(c, a.appName(bundleID), 13, 0).TextColor(setInk)
					ui.Text(c, bundleID).SingleLine().FontSize(11).TextColor(setInk3)
				})
				if ui.Button(c, l.SettingsClipboardRemove()).FontSize(12).
					Label(l.SettingsClipboardRemoveLabel(bundleID)).Clicked() {
					a.removeExcludedApp(bundleID)
				}
			})
		}
	})
}

// settingsAIUsageSection is the AI 用量 group (design/usage-fix-v2.png):
// a single-line master switch, then a two-column table — header cells
// 「套餐额度」「本机用量」 (64pt wide, centred, 11pt semibold ink3), then
// one row per tool with a 16pt checkbox under each header (omp has no
// quota cell: 「—」, not clickable). Turning the master off dims and
// locks the whole table (~40% opaque) while the checkbox states stay.
// The privacy note lives under the card, as System Settings does. Every
// change still goes through applySettings.
func (a *shelf) settingsAIUsageSection(c *ui.Context) {
	l := loc(c)
	// col is one of the table's two right columns: 64pt wide, centred.
	col := func(key string, fn func()) {
		ui.Box(c.Key("c-" + key)).Width(64).Center().TextAlign(ui.Center).Children(fn)
	}
	// check is one 16pt source checkbox: accent fill + white tick while
	// on, a 1pt white-28% outline while off (mock's .ck). Its accessible
	// label prefixes the column so same-named cells stay distinct.
	check := func(key, label string, on bool, apply func(*settings.Settings, bool)) {
		b := ui.CheckboxBase(c.Key("ck-"+key), &on).Size(16, 16).Radius(4).Label(label)
		if on {
			b.Background(settingsTheme.Accent)
		} else {
			b.Border(1, ui.RGBA(255, 255, 255, 0.28))
		}
		b.DrawOver(func(p *ui.Painter, r ui.Rect) {
			if on {
				var tick ui.Path
				tick.MoveTo(r.X+r.W*0.22, r.Y+r.H*0.52).LineTo(r.X+r.W*0.42, r.Y+r.H*0.71).LineTo(r.X+r.W*0.78, r.Y+r.H*0.31)
				p.StrokePath(&tick, 1.8, ui.RGB(255, 255, 255))
			}
		})
		if b.Changed() {
			a.setAIUsage(func(s *settings.Settings) { apply(s, on) })
		}
	}
	// toolRow is one tool line: the name on the left, then the quota and
	// local cells — check boxes, or dash for omp's nonexistent quota.
	toolRow := func(key, name, qKey, lKey string, quota, local bool, qApply, lApply func(*settings.Settings, bool)) {
		ui.Row(c.Key("row-"+key)).AlignItems(ui.Center).Gap(10).MinHeight(40).Padding(settingsRowPadY, 12).FillWidth().Children(func() {
			settingsLabel(c, name, 13, 0).TextColor(setInk).Grow(1).MinWidth(0)
			col("quota-"+key, func() {
				if qApply == nil {
					ui.Text(c, "—").SingleLine().FontSize(12).TextColor(setInk3)
					return
				}
				check(qKey, l.SettingsAIQuotaCheck(name), quota, qApply)
			})
			col("local-"+key, func() {
				check(lKey, l.SettingsAILocalCheck(name), local, lApply)
			})
		})
	}
	settingsSection(c, l.SettingsAISection(), l.SettingsAIFoot(),
		a.settingsBroken, func() {
			settingsToggle(c, "aiusage", l.SettingsAIEnableRow(), a.settings.AIUsage.Enabled,
				func(on bool) { a.setAIUsage(func(s *settings.Settings) { s.AIUsage.Enabled = on }) })
			settingsSep(c, "aiusage")
			// Disabled alone already halves the table's opacity; ×0.8 lands
			// on the mock's ~40% while every widget stays unclickable.
			table := ui.Column(c.Key("aiusage-table")).Disabled(!a.settings.AIUsage.Enabled).FillWidth()
			if !a.settings.AIUsage.Enabled {
				table.Opacity(0.8)
			}
			table.Children(func() {
				ui.Row(c.Key("aiusage-head")).AlignItems(ui.Center).Gap(10).MinHeight(26).Padding(0, 12).FillWidth().Children(func() {
					ui.Box(c.Key("hd-pad")).Grow(1)
					col("hd-quota", func() {
						ui.Text(c, l.SettingsAIQuotaColumn()).SingleLine().FontSize(11).FontWeight(600).TextColor(setInk3)
					})
					col("hd-local", func() {
						ui.Text(c, l.SettingsAILocalColumn()).SingleLine().FontSize(11).FontWeight(600).TextColor(setInk3)
					})
				})
				settingsSep(c, "head")
				toolRow("codex", "Codex", "q-codex", "l-codex",
					a.settings.AIUsage.QuotaCodex, a.settings.AIUsage.LocalCodex,
					func(s *settings.Settings, on bool) { s.AIUsage.QuotaCodex = on },
					func(s *settings.Settings, on bool) { s.AIUsage.LocalCodex = on })
				settingsSep(c, "codex")
				toolRow("claude", "Claude Code", "q-claude", "l-claude",
					a.settings.AIUsage.QuotaClaude, a.settings.AIUsage.LocalClaude,
					func(s *settings.Settings, on bool) { s.AIUsage.QuotaClaude = on },
					func(s *settings.Settings, on bool) { s.AIUsage.LocalClaude = on })
				settingsSep(c, "claude")
				toolRow("omp", "omp", "q-omp", "l-omp",
					false, a.settings.AIUsage.LocalOmp,
					nil,
					func(s *settings.Settings, on bool) { s.AIUsage.LocalOmp = on })
			})
			settingsSep(c, "quota-show")
			settingsRow(c, "quota-show", l.SettingsAIQuotaDisplayRow(), func() {
				shown := 0
				if a.settings.AIUsage.QuotaLeft {
					shown = 1
				}
				sg := ui.Segmented(c, &shown, l.SettingsAIQuotaUsed(), l.SettingsAIQuotaLeft()).
					Label(l.SettingsAIQuotaDisplayRow())
				if !a.settings.AIUsage.Enabled {
					sg.Disabled(true)
				}
				if sg.Changed() {
					a.setAIUsage(func(s *settings.Settings) { s.AIUsage.QuotaLeft = shown == 1 })
				}
			})
		})
}

// settingsTraySection: 自动清理旧内容, 拖出后从托盘移除 and 刘海显示正在
// 播放 — the same setters as the tray's 自动清理 submenu and 正在播放 item.
func (a *shelf) settingsTraySection(c *ui.Context) {
	l := loc(c)
	settingsSection(c, l.SettingsTraySection(), "", a.settingsBroken, func() {
		days := max(0, a.settings.RetentionDays)
		labels, values, selected := choiceSegments(days, retentionChoices, func(n int) string {
			if n <= 0 {
				return l.SettingsRetentionNever()
			}
			return l.SettingsRetentionDays(n)
		})
		settingsRow(c, "retention", l.SettingsRetentionRow(), func() {
			if sg := ui.Segmented(c, &selected, labels...).Label(l.SettingsRetentionRow()); sg.Changed() {
				a.setRetention(values[selected])
			}
		})
		settingsSep(c, "retention")
		settingsToggleCap(c, "dragout", l.SettingsDragOutRow(), l.SettingsDragOutCaption(), setInk3,
			a.settings.RemoveAfterDrag, a.setRemoveAfterDrag)
		settingsSep(c, "dragout")
		settingsToggle(c, "np", l.SettingsNowPlayingRow(), a.settings.IslandNowPlaying, func(on bool) {
			a.setNowPlaying(func(s *settings.Settings) { s.IslandNowPlaying = on })
		})
	})
}

// settingsShortcutsSection lists the global shortcuts read-only; they are
// compiled in (config.go), not preferences.
func (a *shelf) settingsShortcutsSection(c *ui.Context) {
	l := loc(c)
	settingsSection(c, l.SettingsShortcutsSection(), "", false, func() {
		settingsRow(c, "sc-tray", l.SettingsShortcutTray(), func() {
			ui.Text(c, shortcutHint).SingleLine().FontSize(13).TextColor(setInk2)
		})
		settingsSep(c, "sc")
		settingsRow(c, "sc-clip", l.SettingsShortcutClipboard(), func() {
			ui.Text(c, clipboardHint).SingleLine().FontSize(13).TextColor(setInk2)
		})
		settingsSep(c, "sc-ocr")
		settingsRow(c, "sc-ocr", l.SettingsShortcutOCR(), func() {
			ui.Text(c, ocrHint).SingleLine().FontSize(13).TextColor(setInk2)
		})
	})
}

// settingsFooter: the app version (from the bundle's Info.plist when
// packaged) and the button that opens the data folder in Finder.
func (a *shelf) settingsFooter(c *ui.Context) {
	l := loc(c)
	ui.Row(c.Key("footer")).AlignItems(ui.Center).Gap(10).Padding(0, 4).FillWidth().Children(func() {
		ui.Text(c, l.SettingsVersionLine(appName, appVersion())).SingleLine().FontSize(11).TextColor(setInk3).Grow(1)
		if ui.Button(c, l.SettingsOpenDataFolder()).FontSize(12).Clicked() {
			if err := openPath(a.dataDir); err != nil {
				a.report(err)
			}
		}
	})
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
