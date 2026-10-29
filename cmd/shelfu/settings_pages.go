//go:build darwin

package main

// The eight pages of the Settings window, each the design's hero card over
// its grouped cards. Every preference that settings.json or the tray menus
// held before lives on one of them; the split follows where a user looks
// for it (Coucou's Hub), not the old file's order.

import (
	"fmt"
	"runtime"
	"strconv"
	"time"

	"github.com/egoist/mygo/ui"

	"shelf/internal/settings"
	"shelf/locales"
)

// Hero gradients: one deep tone per page, with a glow of the page's accent.
var (
	heroGeneralFrom = ui.Hex("#1C2233")
	heroGeneralTo   = ui.Hex("#0C0F18")
	heroGeneralGlow = ui.RGBA(140, 160, 255, 1)

	heroNotchFrom = ui.Hex("#191A1F")
	heroNotchTo   = ui.Hex("#07080A")
	heroNotchGlow = ui.RGBA(255, 255, 255, 1)

	heroTrayFrom = ui.Hex("#102641")
	heroTrayTo   = ui.Hex("#08111E")
	heroTrayGlow = ui.RGBA(10, 132, 255, 1)

	heroClipFrom = ui.Hex("#33230C")
	heroClipTo   = ui.Hex("#140E06")
	heroClipGlow = ui.RGBA(255, 159, 10, 1)

	heroUsageFrom = ui.Hex("#0F2A1A")
	heroUsageTo   = ui.Hex("#07120B")
	heroUsageGlow = ui.RGBA(48, 209, 88, 1)

	heroKeysFrom = ui.Hex("#232327")
	heroKeysTo   = ui.Hex("#0C0C0E")
	heroKeysGlow = ui.RGBA(255, 255, 255, 1)

	heroPermFrom = ui.Hex("#1D1736")
	heroPermTo   = ui.Hex("#0B0916")
	heroPermGlow = ui.RGBA(94, 92, 230, 1)

	heroAboutFrom = ui.Hex("#2A2018")
	heroAboutTo   = ui.Hex("#100C09")
	heroAboutGlow = ui.RGBA(246, 228, 213, 1)
)

// settingsGeneralPage: 启动 (open at login, language) and 更新 (automatic
// checks, 现在检查 with its last result). Only the login item survives a
// broken settings.json; every other row is locked with the file.
func (a *shelf) settingsGeneralPage(c *ui.Context) {
	l := loc(c)
	settingsHero(c, "general", l.General(), l.SettingsGeneralHero(), "neutral",
		heroGeneralFrom, heroGeneralTo, heroGeneralGlow)

	settingsGroup(c, "startup", l.SettingsStartupGroup(), "", func() {
		loginCap, capColor := "", ink3
		if a.loginErr != "" {
			loginCap, capColor = a.loginErr, danger
		}
		settingsToggleRowCap(c, "login", l.SettingsLoginRow(), loginCap, capColor, a.loginOn, a.applyOpenAtLogin)
		settingsSep(c, "login")
		ui.Column(c.Key("language")).FillWidth().Disabled(a.settingsBroken).Children(func() {
			settingsRow(c, "language", l.LanguageRow(), l.LanguageCaption(), func() {
				idx := languageIndex(a.settings.Language)
				if sg := settingsSegmented(c, "language", l.LanguageRow(), &idx,
					l.LanguageFollowSystem(), l.LanguageChinese(), l.LanguageEnglish()); sg.Changed() {
					a.setLanguage(languageAt(idx))
				}
			})
		})
	})

	settingsGroup(c, "update", l.SettingsUpdateGroup(), "", func() {
		ui.Column(c.Key("update-rows")).FillWidth().Disabled(a.settingsBroken).Children(func() {
			settingsToggleRow(c, "autocheck", l.SettingsAutoCheckRow(), l.SettingsAutoCheckCaption(),
				a.settings.AutoCheckUpdates, a.setAutoCheckUpdates)
			settingsSep(c, "autocheck")
			settingsRow(c, "check", l.SettingsUpdateCheckRow(), a.updateCaption(l), func() {
				busy := a.updateChecking || a.updateInstalling
				settingsButton(c, "check-now", l.SettingsUpdateCheckNow(), false, func() { a.checkUpdates(true) }).Disabled(busy)
			})
		})
	})
	settingsFootnote(c, "update-foot", l.SettingsUpdateFoot())
}

// settingsNotchPage: the shelf's mantle (shape, island position), the
// now-playing wings, the three live activities, and window snapping.
func (a *shelf) settingsNotchPage(c *ui.Context) {
	l := loc(c)
	settingsHero(c, "notch", l.SettingsNotchPage(), l.SettingsNotchHero(), "curious",
		heroNotchFrom, heroNotchTo, heroNotchGlow)

	settingsGroup(c, "shape", l.SettingsShapeGroup(), "", func() {
		ui.Column(c.Key("shape-rows")).FillWidth().Disabled(a.settingsBroken).Children(func() {
			settingsRow(c, "shellstyle", l.ShellStyleRow(), l.ShellStyleCaption(), func() {
				style := 0
				if a.settings.ShellStyle.Normalized() == settings.ShellStyleIsland {
					style = 1
				}
				if sg := settingsSegmented(c, "shellstyle", l.ShellStyleRow(), &style,
					l.ShellStyleNotch(), l.ShellStyleIsland()); sg.Changed() {
					next := settings.ShellStyleNotch
					if style == 1 {
						next = settings.ShellStyleIsland
					}
					a.setShellStyle(next)
				}
			})
			if a.island {
				settingsSep(c, "islandpos")
				settingsRow(c, "islandpos", l.SettingsIslandPosRow(), l.SettingsIslandPosCaption(), func() {
					settingsButton(c, "island-reset", l.SettingsIslandReset(), false, a.resetIslandPosition)
				})
			}
		})
	})
	settingsFootnote(c, "shape-foot", l.SettingsNotchFoot())

	settingsGroup(c, "hover", l.SettingsHoverGroup(), "", func() {
		ui.Column(c.Key("hover-rows")).FillWidth().Disabled(a.settingsBroken).Children(func() {
			settingsToggleRow(c, "np", l.SettingsNowPlayingRow(), "", a.settings.IslandNowPlaying, func(on bool) {
				a.setNowPlaying(func(s *settings.Settings) { s.IslandNowPlaying = on })
			})
		})
	})

	settingsGroup(c, "live", l.SettingsLiveSection(), "", func() {
		ui.Column(c.Key("live-rows")).FillWidth().Disabled(a.settingsBroken).Children(func() {
			settingsToggleRow(c, "live-charging", l.SettingsLiveCharging(), "", a.settings.LiveActivities.Charging,
				func(on bool) { a.setLiveActivity(func(s *settings.Settings) { s.LiveActivities.Charging = on }) })
			settingsSep(c, "live-charging")
			settingsToggleRow(c, "live-headphones", l.SettingsLiveHeadphones(), "", a.settings.LiveActivities.Headphones,
				func(on bool) { a.setLiveActivity(func(s *settings.Settings) { s.LiveActivities.Headphones = on }) })
			settingsSep(c, "live-headphones")
			settingsToggleRow(c, "live-downloads", l.SettingsLiveDownloads(), "", a.settings.LiveActivities.Downloads,
				func(on bool) { a.setLiveActivity(func(s *settings.Settings) { s.LiveActivities.Downloads = on }) })
		})
	})

	settingsGroup(c, "window", l.SettingsWindowGroup(), "", func() {
		ui.Column(c.Key("window-rows")).FillWidth().Disabled(a.settingsBroken).Children(func() {
			settingsToggleRow(c, "snap", l.SettingsSnapRow(), l.SettingsSnapCaption(),
				a.settings.SnapWindows, a.setSnapWindows)
		})
	})
}

// setLiveActivity saves one 实时活动 switch and repaints.
func (a *shelf) setLiveActivity(mutate func(*settings.Settings)) {
	next := a.settings
	mutate(&next)
	a.applySettings(next)
}

// settingsTrayPage: the tray's own two rules.
func (a *shelf) settingsTrayPage(c *ui.Context) {
	l := loc(c)
	settingsHero(c, "tray", l.SettingsTraySection(), l.SettingsTrayHero(), "happy",
		heroTrayFrom, heroTrayTo, heroTrayGlow)

	settingsGroupRows(c, "tidy", l.SettingsTidyGroup(), "", a.settingsBroken, func() {
		days := max(0, a.settings.RetentionDays)
		labels, values, selected := choiceSegments(days, retentionChoices, func(n int) string {
			if n <= 0 {
				return l.SettingsRetentionNever()
			}
			return l.SettingsRetentionDays(n)
		})
		settingsRow(c, "retention", l.SettingsRetentionRow(), l.SettingsRetentionCaption(), func() {
			if sg := settingsSegmented(c, "retention", l.SettingsRetentionRow(), &selected, labels...); sg.Changed() {
				a.setRetention(values[selected])
			}
		})
		settingsSep(c, "retention")
		settingsToggleRow(c, "dragout", l.SettingsDragOutRow(), l.SettingsDragOutCaption(),
			a.settings.RemoveAfterDrag, a.setRemoveAfterDrag)
	})
}

// settingsClipboardPage: the retained count and the excluded apps, with the
// same removal the old list offered.
func (a *shelf) settingsClipboardPage(c *ui.Context) {
	l := loc(c)
	settingsHero(c, "clip", l.SettingsClipboardSection(), l.SettingsClipHero(), "surprised",
		heroClipFrom, heroClipTo, heroClipGlow)

	settingsGroupRows(c, "record", l.SettingsClipGroup(), "", a.settingsBroken, func() {
		limit := a.settings.ClipboardLimit
		if limit < 1 {
			limit = settings.Default().ClipboardLimit
		}
		labels, values, selected := choiceSegments(limit, clipboardLimitChoices, strconv.Itoa)
		settingsRow(c, "limit", l.SettingsClipboardLimitRow(), l.SettingsClipLimitCaption(), func() {
			if sg := settingsSegmented(c, "limit", l.SettingsClipboardLimitRow(), &selected, labels...); sg.Changed() {
				a.setClipboardLimit(values[selected])
			}
		})
	})

	rows := func() {
		if len(a.settings.ExcludedApps) == 0 {
			settingsRow(c, "excl-none", l.SettingsClipboardExcludedNone(), "", func() {})
			return
		}
		for i, id := range a.settings.ExcludedApps {
			bundleID := id
			if i > 0 {
				settingsSep(c, "excl-"+bundleID)
			}
			ui.Row(c.Key("app-"+bundleID)).AlignItems(ui.Center).Gap(14).
				MinHeight(settingsRowMinHeight).Padding(settingsRowPadY, settingsRowPadX).FillWidth().Children(func() {
				if icon := settingsAppIcon(bundleID); icon != nil {
					ui.Image(c.Key("ic"), icon).Size(20, 20).Radius(5)
				} else {
					ui.Icon(c.Key("ic"), appGlyph).Size(20, 20).TextColor(ink3)
				}
				ui.Column(c.Key("id")).Gap(1).Grow(1).MinWidth(0).Children(func() {
					settingsLabel(c, a.appName(bundleID), 13.5, 600).TextColor(ink)
					ui.Text(c, bundleID).SingleLine().FontSize(11.5).TextColor(ink3)
				})
				settingsButton(c, "rm-"+bundleID, l.SettingsClipboardRemove(), false, func() {
					a.removeExcludedApp(bundleID)
				}).Label(l.SettingsClipboardRemoveLabel(bundleID))
			})
		}
	}
	settingsGroupRows(c, "excluded", l.SettingsClipboardExcludedRow(), "", a.settingsBroken, rows)
	settingsFootnote(c, "clip-foot", l.SettingsClipFoot())
}

// settingsAIUsagePage: the master switch, the quota display, and the source
// table. Turning the master off dims and locks the table (~40% opaque)
// while the checkbox states stay readable.
func (a *shelf) settingsAIUsagePage(c *ui.Context) {
	l := loc(c)
	settingsHero(c, "usage", l.SettingsAISection(), l.SettingsAIHero(), "curious",
		heroUsageFrom, heroUsageTo, heroUsageGlow)

	settingsGroupRows(c, "ai", l.SettingsAIEnableGroup(), "", a.settingsBroken, func() {
		settingsToggleRow(c, "aiusage", l.SettingsAIEnableRow(), "", a.settings.AIUsage.Enabled,
			func(on bool) { a.setAIUsage(func(s *settings.Settings) { s.AIUsage.Enabled = on }) })
		settingsSep(c, "aiusage")
		settingsRow(c, "quota-show", l.SettingsAIQuotaDisplayRow(), "", func() {
			shown := 0
			if a.settings.AIUsage.QuotaLeft {
				shown = 1
			}
			sg := settingsSegmented(c, "quota-show", l.SettingsAIQuotaDisplayRow(), &shown,
				l.SettingsAIQuotaUsed(), l.SettingsAIQuotaLeft())
			if !a.settings.AIUsage.Enabled {
				sg.Disabled(true)
			}
			if sg.Changed() {
				a.setAIUsage(func(s *settings.Settings) { s.AIUsage.QuotaLeft = shown == 1 })
			}
		})
	})

	settingsGroup(c, "sources", l.SettingsAISourcesGroup(), "", func() {
		table := ui.Column(c.Key("aiusage-table")).FillWidth().
			Disabled(a.settingsBroken || !a.settings.AIUsage.Enabled)
		if !a.settingsBroken && !a.settings.AIUsage.Enabled {
			table.Opacity(0.8)
		}
		table.Children(func() {
			aiUsageTable(c, a, l)
		})
	})
	settingsFootnote(c, "ai-foot", l.SettingsAIFoot())
}

// aiUsageTable draws the three-column source table: tool names on the left,
// one 18pt checkbox under each of the two headers.
func aiUsageTable(c *ui.Context, a *shelf, l locales.Messages) {
	col := func(key string, fn func()) {
		ui.Box(c.Key("c-" + key)).Width(84).Center().TextAlign(ui.Center).Children(fn)
	}
	check := func(key, label string, on bool, apply func(*settings.Settings, bool)) {
		b := ui.CheckboxBase(c.Key("ck-"+key), &on).Size(18, 18).Radius(5).Label(label)
		if on {
			b.Background(accent)
		} else {
			b.Border(1.5, ui.RGBA(255, 255, 255, 0.30))
		}
		b.DrawOver(func(p *ui.Painter, r ui.Rect) {
			if !on {
				return
			}
			var tick ui.Path
			tick.MoveTo(r.X+r.W*0.22, r.Y+r.H*0.52).LineTo(r.X+r.W*0.42, r.Y+r.H*0.71).
				LineTo(r.X+r.W*0.78, r.Y+r.H*0.31)
			p.StrokePath(&tick, 2, ui.RGB(255, 255, 255))
		})
		if b.Changed() {
			a.setAIUsage(func(s *settings.Settings) { apply(s, on) })
		}
	}
	toolRow := func(key, name string, quota, local bool,
		qApply, lApply func(*settings.Settings, bool)) {
		ui.Row(c.Key("row-"+key)).AlignItems(ui.Center).Gap(10).MinHeight(44).
			Padding(0, settingsRowPadX).FillWidth().Children(func() {
			ui.Box(c.Key("name")).Grow(1).MinWidth(0).Children(func() {
				settingsLabel(c, name, 13.5, 600).TextColor(ink)
			})
			col("quota-"+key, func() {
				if qApply == nil {
					ui.Text(c, "—").SingleLine().FontSize(12.5).TextColor(ink3)
					return
				}
				check("q-"+key, l.SettingsAIQuotaCheck(name), quota, qApply)
			})
			col("local-"+key, func() {
				check("l-"+key, l.SettingsAILocalCheck(name), local, lApply)
			})
		})
	}
	ui.Row(c.Key("aiusage-head")).AlignItems(ui.Center).Gap(10).MinHeight(28).
		Padding(0, settingsRowPadX).FillWidth().Children(func() {
		ui.Box(c.Key("hd-pad")).Grow(1)
		col("hd-quota", func() {
			ui.Text(c, l.SettingsAIQuotaColumn()).SingleLine().FontSize(10.5).FontWeight(700).
				LetterSpacing(0.8).TextColor(setLabel)
		})
		col("hd-local", func() {
			ui.Text(c, l.SettingsAILocalColumn()).SingleLine().FontSize(10.5).FontWeight(700).
				LetterSpacing(0.8).TextColor(setLabel)
		})
	})
	settingsSep(c, "aiusage-head")
	toolRow("codex", "Codex", a.settings.AIUsage.QuotaCodex, a.settings.AIUsage.LocalCodex,
		func(s *settings.Settings, on bool) { s.AIUsage.QuotaCodex = on },
		func(s *settings.Settings, on bool) { s.AIUsage.LocalCodex = on })
	settingsSep(c, "codex")
	toolRow("claude", "Claude Code", a.settings.AIUsage.QuotaClaude, a.settings.AIUsage.LocalClaude,
		func(s *settings.Settings, on bool) { s.AIUsage.QuotaClaude = on },
		func(s *settings.Settings, on bool) { s.AIUsage.LocalClaude = on })
	settingsSep(c, "claude")
	toolRow("omp", "omp", false, a.settings.AIUsage.LocalOmp, nil,
		func(s *settings.Settings, on bool) { s.AIUsage.LocalOmp = on })
}

// settingsShortcutsPage: four editable global shortcuts (click the keycaps,
// press the new combination) and the two the panel answers, read-only.
func (a *shelf) settingsShortcutsPage(c *ui.Context) {
	l := loc(c)
	settingsHero(c, "keys", l.SettingsShortcutsSection(), l.SettingsKeysHero(), "neutral",
		heroKeysFrom, heroKeysTo, heroKeysGlow)

	settingsGroupRows(c, "keys", l.SettingsKeysGlobalGroup(), "", a.settingsBroken, func() {
		a.settingsShortcutRow(c, shortcutToggle, l.SettingsShortcutTray(), "")
		settingsSep(c, "sc-toggle")
		a.settingsShortcutRow(c, shortcutClipboard, l.SettingsShortcutClipboard(), "")
		settingsSep(c, "sc-clipboard")
		a.settingsShortcutRow(c, shortcutCapture, l.SettingsShortcutOCR(), "")
		settingsSep(c, "sc-capture")
		a.settingsShortcutRow(c, shortcutOpenTray, l.SettingsShortcutOpenTray(), l.SettingsShortcutTrayCaption())
	})
	settingsFootnote(c, "keys-foot", l.SettingsKeysFoot())

	settingsGroup(c, "keys-notch", l.SettingsKeysNotchGroup(), "", func() {
		settingsReadonlyShortcutRow(c, "sc-esc", l.SettingsShortcutEscRow(), l.SettingsShortcutEscCaption(),
			[]string{"Esc"})
		settingsSep(c, "sc-esc")
		settingsReadonlyShortcutRow(c, "sc-select", l.SettingsShortcutSelectRow(), "",
			[]string{"↑", "↓", "↩"})
	})
}

// settingsShortcutRow is one editable shortcut: the label and caption on the
// left, the keycaps on the right. The row itself starts the recording, as a
// switch row toggles. A row that could not claim its combination says so in
// place of its caption.
func (a *shelf) settingsShortcutRow(c *ui.Context, kind shortcutKind, label, caption string) {
	l := loc(c)
	rowCaption, captionColor := caption, ink3
	if a.shortcutBusy[kind] {
		rowCaption, captionColor = l.SettingsShortcutBusy(), danger
	}
	recording := a.settingsRecording == kind
	row := ui.ButtonBase(c.Key(fmt.Sprintf("sc-%d", kind))).FillWidth().Radius(0).
		MinHeight(settingsRowMinHeight).AlignItems(ui.Center).Justify(ui.Start).
		Gap(14).Padding(settingsRowPadY, settingsRowPadX).Label(label)
	if acc := a.shortcutAcc(kind); acc != "" {
		// VoiceOver says the combination the row shows.
		row.Description(shortcutLabel(acc))
	}
	row.Children(func() {
		ui.Column(c.Key("text")).Gap(2).Grow(1).MinWidth(0).Children(func() {
			settingsLabel(c, label, 13.5, 600).TextColor(ink)
			if rowCaption != "" {
				ui.Text(c, rowCaption).FontSize(11.5).TextColor(captionColor)
			}
		})
		ui.Row(c.Key("keys")).AlignItems(ui.Center).Gap(4).Children(func() {
			if recording {
				ui.Box(c.Key("rec")).Height(26).Radius(7).Padding(0, 8, 0, 8).
					Border(1.5, accent).Center().Children(func() {
					ui.Text(c, l.SettingsShortcutRecording()).SingleLine().FontSize(11.5).TextColor(accent)
				})
				return
			}
			if tokens, ok := shortcutTokens(a.shortcutAcc(kind)); ok {
				for i, token := range tokens {
					settingsKeycap(c, fmt.Sprintf("k%d", i), token, false)
				}
				return
			}
			ui.Box(c.Key("none")).Height(26).Radius(7).Padding(0, 8, 0, 8).
				Border(1, ui.RGBA(255, 255, 255, 0.12)).Center().Children(func() {
				ui.Text(c, l.SettingsShortcutNoShortcut()).SingleLine().FontSize(11.5).TextColor(ink3)
			})
		})
	})
	if row.Clicked() {
		a.startShortcutRecording(kind)
	}
}

// settingsReadonlyShortcutRow shows a shortcut the panel answers itself.
func settingsReadonlyShortcutRow(c *ui.Context, key, label, caption string, tokens []string) {
	settingsRowLine(c, key).Children(func() {
		settingsRowText(c, label, caption)
		ui.Row(c.Key("keys")).AlignItems(ui.Center).Gap(4).Children(func() {
			for i, token := range tokens {
				settingsKeycap(c, fmt.Sprintf("%s-%d", key, i), token, false)
			}
		})
	})
}

// settingsKeycap is one 26pt key: #1F2024, 7pt corners, a hairline of light
// along the bottom, 12.5pt semibold label.
func settingsKeycap(c *ui.Context, key, label string, on bool) {
	box := ui.Box(c.Key("cap-"+key)).Height(26).MinWidth(26).Radius(7).
		Padding(0, 7, 0, 7).Center().Background(setCtl)
	if on {
		box.Border(1.5, accent)
	} else {
		box.InsetShadow(0, -1, 0, 0, ui.RGBA(255, 255, 255, 0.06))
	}
	box.Children(func() {
		ui.Text(c, label).SingleLine().FontSize(12.5).FontWeight(600).TextColor(ink)
	})
}

// settingsPermissionsPage: every permission with its live state, and the
// one switch that walks the user through whatever is missing — the same
// flow the tray menu's item starts.
func (a *shelf) settingsPermissionsPage(c *ui.Context) {
	l := loc(c)
	permissions := systemPermissionSet(l)
	missing := missingPermissions(permissions)
	settingsHero(c, "perm", l.PermTitle(), l.SettingsPermHero(), "neutral",
		heroPermFrom, heroPermTo, heroPermGlow)

	settingsGroup(c, "perm", l.SettingsPermNeeded(len(permissions)), "", func() {
		for i, p := range permissions {
			if i > 0 {
				settingsSep(c, "perm-"+p.id)
			}
			granted := p.enabled()
			row := settingsRowLine(c, "perm-"+p.id)
			row.Children(func() {
				settingsRowText(c, p.short, p.detail)
				ui.Row(c.Key("state")).AlignItems(ui.Center).Gap(6).Children(func() {
					dot := success
					text := l.SettingsPermOn()
					if !granted {
						dot, text = warning, l.SettingsPermOff()
					}
					ui.Box(c.Key("dot")).Size(7, 7).Radius(4).Background(dot)
					ui.Text(c, text).SingleLine().FontSize(12).TextColor(ink2)
				})
			})
		}
		settingsSep(c, "perm-switch")
		a.settingsPermissionSwitch(c, permissions, missing)
	})
	settingsFootnote(c, "perm-foot", l.SettingsPermFoot())
}

// settingsAboutPage: the big otter, the version, the actions, and the
// privacy and source notes.
func (a *shelf) settingsAboutPage(c *ui.Context) {
	l := loc(c)
	settingsHero(c, "about", l.SettingsAboutHero(), l.SettingsAboutHeroLine(), "",
		heroAboutFrom, heroAboutTo, heroAboutGlow)

	ui.Row(c.Key("about")).FillWidth().AlignItems(ui.Center).Gap(18).Padding(4, 4, 4, 4).Children(func() {
		if b := otterImage("happy"); b != nil {
			w, h := b.Size()
			width := float32(150)
			height := width * float32(h) / float32(w)
			ui.Image(c.Key("otter"), b).Size(width, height).Fit(ui.Contain)
		}
		ui.Column(c.Key("about-text")).Gap(2).Grow(1).MinWidth(0).Children(func() {
			ui.Text(c, appName).SingleLine().FontSize(24).FontWeight(700).TextColor(ink)
			ui.Text(c, l.SettingsAboutVersion(appVersion(), runtime.GOARCH)).SingleLine().
				FontSize(12).TextColor(ink3).Padding(2, 0, 0, 0)
			ui.Text(c, l.SettingsAboutTagline()).FontSize(13).TextColor(ink2).Padding(8, 0, 0, 0)
			ui.Row(c.Key("about-actions")).AlignItems(ui.Center).Gap(8).Padding(14, 0, 0, 0).Children(func() {
				settingsButton(c, "update", l.SettingsUpdateCheckNow(), false, func() { a.checkUpdates(true) }).
					Disabled(a.updateChecking || a.updateInstalling)
				settingsButton(c, "whatsnew", l.SettingsAboutWhatsNew(), false, func() { a.ShowWhatsNew() })
				settingsButton(c, "data", l.SettingsOpenDataFolder(), false, func() {
					if err := openPath(a.dataDir); err != nil {
						a.report(err)
					}
				})
			})
		})
	})

	settingsGroup(c, "privacy", l.SettingsAboutPrivacyGroup(), "", func() {
		settingsRow(c, "privacy", l.SettingsAboutPrivacy(), l.SettingsAboutPrivacyCaption(), func() {})
		settingsSep(c, "source")
		settingsRow(c, "source", l.SettingsAboutSource(), l.SettingsAboutSourceCaption(), func() {
			settingsButton(c, "source-open", l.SettingsAboutSourceOpen(), false, func() {
				if err := openExternal(sourceURL); err != nil {
					a.report(err)
				}
			})
		})
	})
}

// sourceURL is the project's public repository, the one the 关于 page links
// to.
const sourceURL = "https://github.com/henryyu333/shelfu"

// updateCaption is the 检查更新 row's second line: the last check's time and
// outcome, or what is happening now.
func (a *shelf) updateCaption(l locales.Messages) string {
	switch {
	case a.updateChecking:
		return l.UpdateChecking()
	case a.updateInstalling:
		return l.UpdateDownloading(a.update.Version)
	case a.updateInstalled:
		return l.UpdateInstalled(a.update.Version)
	case a.updateLastCheck.IsZero():
		return l.SettingsUpdateNever()
	}
	when := a.updateWhen(l, a.updateLastCheck, time.Now())
	var result string
	switch a.updateLastResult {
	case updateResultLatest:
		result = l.SettingsUpdateResultLatest()
	case updateResultAvailable:
		result = l.SettingsUpdateResultAvailable(a.updateLastVersion)
	case updateResultFailed:
		result = l.SettingsUpdateResultFailed()
	case updateResultDisabled:
		result = l.SettingsUpdateResultDisabled()
	default:
		result = l.SettingsUpdateResultLatest()
	}
	return l.SettingsUpdateLast(when, result)
}

// updateWhen says a check's time as 今天 09:12, 昨天 21:05 or a date, in the
// window's language. Pure; unit-tested.
func (a *shelf) updateWhen(l locales.Messages, at, now time.Time) string {
	y1, m1, d1 := at.Date()
	y2, m2, d2 := now.Date()
	clock := at.Format("15:04")
	switch {
	case y1 == y2 && m1 == m2 && d1 == d2:
		return l.SettingsUpdateToday(clock)
	case y1 == y2 && m1 == m2 && d1 == d2-1:
		return l.SettingsUpdateYesterday(clock)
	}
	return l.SettingsUpdateOn(at.Format("2006-01-02 15:04"))
}
