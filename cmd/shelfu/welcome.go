//go:build darwin

package main

// 欢迎卡与新版本介绍：两者都占据总览的内容位置，不改变外壳尺寸。
//
// 欢迎卡只给全新安装，第一次启动后第一次展开时出现一次：一句介绍、一个真实
// 的练习（把文件拖到刘海）、一个跳过。真的拖进来一个文件就当作完成。
// 新版本介绍在版本号变了之后第一次展开时出现一次；全新安装不显示（欢迎卡
// 已经介绍过），开发构建（版本 dev）也不显示。
//
// 两个标记都不进 settings.json（设置结构由设置窗口那一侧负责），而是单独存在
// 数据目录的 ui.json：{"welcome":true,"whatsNew":"1.11.0"}。读不通就当没看过，
// 不改写原文件。

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"

	"github.com/egoist/mygo/ui"
)

const uiStateFile = "ui.json"

// uiState remembers what the overview cards have already said.
type uiState struct {
	Welcome  bool   `json:"welcome,omitempty"`
	WhatsNew string `json:"whatsNew,omitempty"`
}

// loadUIState reads the card marks. A missing file is an empty state; a
// file that cannot be read or parsed is an empty state *and* a broken one:
// the cards still work for this session, but nothing is written back, so
// the user's file stays as it is to repair — the same rule settings.json
// and layout.json follow.
func loadUIState(dir string) (uiState, bool) {
	var st uiState
	if dir == "" {
		return st, false
	}
	data, err := os.ReadFile(filepath.Join(dir, uiStateFile))
	if err != nil {
		if os.IsNotExist(err) {
			return st, false
		}
		return st, true
	}
	if err := json.Unmarshal(data, &st); err != nil {
		return uiState{}, true
	}
	return st, false
}

// saveUIState writes the marks atomically.
func saveUIState(dir string, st uiState) error {
	if dir == "" {
		return nil
	}
	data, err := json.Marshal(st)
	if err != nil {
		return err
	}
	tmp := filepath.Join(dir, uiStateFile+".tmp")
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, filepath.Join(dir, uiStateFile))
}

// planOverviewCards decides, once at startup, which card the overview shows
// and remembers the state it will write when the card is dismissed.
func (a *shelf) planOverviewCards() {
	var broken bool
	a.uiState, broken = loadUIState(a.dataDir)
	a.uiStateBroken = broken
	version := appVersion()
	switch {
	case a.freshInstall && !a.uiState.Welcome:
		a.welcomePending = true
	case version != "" && version != "dev" && a.uiState.WhatsNew != version:
		a.whatsNewPending = true
	}
}

// dismissWelcome marks the welcome card done and it never shows again. A
// fresh install also records the version whose new-version card it just
// stood in for: the next launch of the same build must not show both.
func (a *shelf) dismissWelcome() {
	a.welcomePending, a.welcomeUp = false, false
	a.uiState.Welcome = true
	if v := appVersion(); v != "" && v != "dev" {
		a.uiState.WhatsNew = v
	}
	a.saveCards()
	a.otterHappy(time.Now())
}

// dismissWhatsNew records the version whose card was read.
func (a *shelf) dismissWhatsNew() {
	a.whatsNewPending, a.whatsNewUp = false, false
	a.uiState.WhatsNew = appVersion()
	a.saveCards()
}

// saveCards persists the marks; a failure is reported like any other
// write. A file that did not parse is never overwritten: the user gets to
// repair it, and this session's marks live in memory only.
func (a *shelf) saveCards() {
	if a.uiStateBroken {
		return
	}
	if err := saveUIState(a.dataDir, a.uiState); err != nil {
		a.report(err)
	}
}

// welcomeShown reports whether the welcome card is up now: the pending card
// turns up about a second after the first opening, so the overview is seen
// first.
func (a *shelf) welcomeShown(now time.Time) bool {
	if !a.welcomePending {
		return a.welcomeUp
	}
	if a.welcomeUp {
		return true
	}
	if a.visible && !a.openedAt.IsZero() && now.Sub(a.openedAt) >= time.Second {
		a.welcomeUp = true
		if a.win != nil {
			a.win.Invalidate()
		}
	}
	return a.welcomeUp
}

// whatsNewShown reports whether the new-version card is up now. It waits
// until the welcome card has had its turn.
func (a *shelf) whatsNewShown(now time.Time) bool {
	if a.welcomeShown(now) {
		return false
	}
	if !a.whatsNewPending {
		return a.whatsNewUp
	}
	if a.whatsNewUp {
		return true
	}
	if a.visible && !a.openedAt.IsZero() && now.Sub(a.openedAt) >= 400*time.Millisecond {
		a.whatsNewUp = true
		if a.win != nil {
			a.win.Invalidate()
		}
	}
	return a.whatsNewUp
}

// welcomeCard is the first-run card in the overview's content box.
func (a *shelf) welcomeCard(c *ui.Context) {
	l := loc(c)
	row := ui.Row(c.Key("welcome")).Width(contentW).Height(overviewBodyH).Gap(14).AlignItems(ui.Center)
	row.Children(func() {
		a.cardOtter(c, "welcome-art", "front")
		ui.Column(c.Key("welcome-text")).Grow(1).MinWidth(0).Gap(4).Children(func() {
			panelLabel(c, nil, l.WelcomeTitle(), 15, 600).TextColor(ink)
			ui.Text(c.Key("welcome-body"), l.WelcomeBody()).MaxLines(2).FontSize(typeSub).
				TextColor(ink2).MaxWidth(contentW - 168)
			ui.Row(c.Key("welcome-try")).Gap(6).AlignItems(ui.Center).Children(func() {
				ui.Icon(c, shelfSymbol).Size(14, 14).TextColor(ink2)
				panelLabel(c, nil, l.WelcomeTry(), typeCallout, 0).TextColor(ink3)
			})
		})
		skip := ui.ButtonBase(c.Key("welcome-skip")).Height(22).Padding(0, 10, 0, 10).Radius(11).Center().
			Label(l.WelcomeSkip()).Tooltip(l.WelcomeSkip())
		if skip.Hovered() {
			skip.Background(fill2)
		}
		skip.Children(func() { panelLabel(c, nil, l.WelcomeSkip(), typeSub, 0).TextColor(ink2) })
		if skip.Clicked() {
			a.dismissWelcome()
		}
	})
}

// whatIsNewCard is the upgrade card in the overview's content box.
func (a *shelf) whatsNewCard(c *ui.Context) {
	l := loc(c)
	row := ui.Row(c.Key("whatsnew")).Width(contentW).Height(overviewBodyH).Gap(14).AlignItems(ui.Center)
	row.Children(func() {
		a.cardOtter(c, "whatsnew-art", "happy")
		ui.Column(c.Key("whatsnew-text")).Grow(1).MinWidth(0).Gap(2).Children(func() {
			panelLabel(c, nil, l.WhatsNewTitle(appVersion()), typeCallout, 600).TextColor(ink)
			a.whatsNewLine(c, l.WhatsNewOne())
			a.whatsNewLine(c, l.WhatsNewTwo())
			a.whatsNewLine(c, l.WhatsNewThree())
		})
		ok := ui.ButtonBase(c.Key("whatsnew-ok")).Height(24).Padding(0, 12, 0, 12).Radius(12).Center().
			Background(accent).Label(l.WhatsNewOK())
		ok.Children(func() { panelLabel(c, nil, l.WhatsNewOK(), typeCallout, 600).TextColor(ink) })
		if ok.Clicked() {
			a.dismissWhatsNew()
		}
	})
}

// whatsNewLine is one bullet line of the card.
func (a *shelf) whatsNewLine(c *ui.Context, text string) {
	ui.Row(c.Key("wn-" + text)).Gap(6).AlignItems(ui.Center).Children(func() {
		ui.Box(c).Size(3, 3).Radius(1.5).Background(ink3)
		panelLabel(c, nil, text, typeSub, 0).TextColor(ink2).MaxWidth(contentW - 190)
	})
}

// cardOtter is the card's picture: tall as the body allows, or nothing when
// the build has no artwork.
func (a *shelf) cardOtter(c *ui.Context, key, name string) {
	box := ui.Row(c.Key(key)).Width(90).Height(overviewBodyH).Justify(ui.Center).AlignItems(ui.Center)
	box.Children(func() {
		if b := otterImage(name); b != nil {
			ui.Image(c, b).Size(90, 94).Fit(ui.Contain)
		}
	})
}

// ShowWhatsNew replays the new-version card. The Settings window's About
// page calls it (settings side of the app, same package): the panel opens
// on the overview and shows the card until 好的 is pressed. It does not
// change what the next launch shows once the card has been read.
func (a *shelf) ShowWhatsNew() {
	a.welcomePending, a.welcomeUp = false, false
	a.whatsNewPending, a.whatsNewUp = true, true
	a.tab, a.tabFrom = pageOverview, pageOverview
	a.level = 1
	if !a.visible {
		a.show(false)
	} else {
		a.setLevel(1)
	}
	a.whatsNewUp = true
	a.openedAt = time.Time{}
	if a.win != nil {
		a.win.Invalidate()
	}
}
