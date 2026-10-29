<p align="center">
  <img src="https://github.com/henryyu333/shelfu/releases/download/v1.10.1/ShelfU-icon.png" width="128" alt="ShelfU icon">
</p>

<h1 align="center">ShelfU</h1>

<p align="center">
  A notch tool for the Mac. Files, clipboard, music and everyday tools grow out of the MacBook notch when you need them, and fold back into it when you're done.<br>
  住在 MacBook 刘海里的工具：文件、剪贴板、音乐和常用小工具，需要时从刘海里长出来，用完就缩回去。
</p>

<p align="center">
  <a href="https://github.com/henryyu333/shelfu/releases/latest/download/ShelfU.dmg"><img src="https://img.shields.io/badge/Download_for_macOS-ShelfU.dmg-007AFF?style=for-the-badge&logo=apple&logoColor=white" alt="Download ShelfU.dmg for macOS"></a>
</p>

<p align="center">
  Apple Silicon · macOS 13+ · Free and open source · <a href="#install">Install guide</a> · <a href="https://github.com/henryyu333/shelfu/releases/latest">All downloads</a><br>
  Apple Silicon 芯片 · macOS 13 或以上 · 免费开源 · <a href="#安装">安装说明</a> · <a href="https://github.com/henryyu333/shelfu/releases/latest">全部下载</a>
</p>

<p align="center">
  <a href="LICENSE"><img src="https://img.shields.io/badge/license-MIT-green.svg" alt="License: MIT"></a>
  <img src="https://img.shields.io/badge/macOS-13%2B%20Apple%20Silicon-blue.svg" alt="macOS 13+ Apple Silicon">
  <img src="https://img.shields.io/badge/built%20with-Go-00ADD8.svg" alt="Built with Go">
</p>

<p align="center">
  <a href="#english">English</a> | <a href="#简体中文">简体中文</a>
</p>

---

<a id="english"></a>

## What is ShelfU

ShelfU is a notch tool for the Mac: it turns the black notch around the camera into a working part of the screen. It stays out of sight until you need it: hover the notch and it grows a pair of wings, or a small down-arrow pill, and a click opens the Overview. The Overview holds the five items you put in last — drag any of them straight out — and one line about what matters now: a job still running, with Cancel, an error, a finished job, an update ready, the track that is playing, a copy from the last minute, or how many clips the Clipboard holds. The otter sits on the left. There is no second, smaller island to open first. The page icons sit on the left of the top row — Overview, Tray, Clipboard, Tools, and AI Usage when you turn it on — and a page grows the same panel down to 440 × 250, so it fits under your menu bar instead of covering it. The Overview's icon is a house, so it reads apart from the Tools grid at a glance. Drag something onto the notch instead and the Tray opens on its own, already waiting for the drop. Only the black shape on screen takes clicks: the menu bar beside it works as usual. Esc steps back one level at a time: a page returns to the Overview, and the Overview folds into the notch.

It lives in the menu bar, has no Dock icon, keeps everything on your Mac, and never copies your files — it only remembers where they are.

## Highlights

**A shelf in the notch**
- Drag files, folders, images or text toward the notch — it shows "Release to Add" and the Tray opens while you are still dragging. The page becomes a drop zone: drop on Put in Tray, or on an action tile that fits the files — compress pictures to 1 MB, turn them into a PDF or JPEG, convert PDFs to Word or images, extract archives, zip anything, or AirDrop it. Results land on the shelf; originals stay where they are.
- Files show as thumbnails. Drag one or several (⌘/⇧ to select) straight into any app.
- Press Space for Quick Look; right-click to open, reveal in Finder, AirDrop, copy, pin or remove — pictures get an extra "Copy Image" entry that copies the image itself.
- Convert in place: right-click a picture → Convert to → JPEG, HEIC, PNG or PDF, or Word (Recognize Text) for the text in it; select several to merge them into one PDF. Right-click a PDF → Convert to → Word (Extract Text), or PNG (One per Page) or JPEG (One per Page). Resize Images sets the long edge to 2560, 1600 or 800 px. Everything runs on this Mac, the original stays untouched, and results land at the front of the shelf.
- Tidy files where they sit: right-click a picture → Compress to and pick 500 KB, 1 MB, 2 MB or 5 MB; right-click a zip, tar, 7z or rar archive → Extract; right-click files → Rename… to rename a batch the way Finder does — replace text, add text, or name plus number — with a live preview and undo.
- Other ways in: shake the pointer while dragging to summon a drop basket beside the cursor, or use Services → "Add to ShelfU" in Finder and other apps.

**Clipboard history**
- Text you copy is kept automatically, along with the app it came from. Screenshots and copied images land on the shelf too.
- Press `⌃⌥V` to search: type to filter, ↑/↓ to choose, ⏎ copies it and takes you back to the app you were in. Text inside screenshots and copied pictures is recognized on this Mac and searchable too; right-click a picture → Copy Recognized Text to copy what it says.
- Pin what matters. Exclude apps you never want recorded. Passwords that password managers mark as private are skipped.

**Now playing**
- Hover the notch while music plays and it grows two small wings: the cover on the left, a moving equalizer on the right. Nothing drops down over your windows. Click anywhere on the wings and the Overview opens.
- The Overview's line carries the player: cover, title and artist, with previous, play-pause and next. Click that line to open the Now Playing page — previous, play-pause and next, a progress bar you drag to skip, and the volume bar for your default output device. The page keeps its size whether or not a track is playing.
- The music widget is still a card on the Tools page: at 2 × 2 it is the full player, at 2 × 1 it shows the cover, title, artist and play-pause, and at 1 × 1 a tap toggles playback.

**AI usage** (optional, off by default)
- Enable it in Settings → AI Usage and the panel gains one more page icon: the left column holds a double-ring card per tool — outer ring weekly, inner ring 5-hour — and the right column holds this Mac's token usage for omp, Claude Code and Codex, over Today / 7D / 30D / All, as four numbers and a stacked chart with its legend. The page fits one screen and never scrolls. Hover a card for its reset times and any per-model weekly limits; hover the token or cost number for its parts.
- Quotas show how much is used by default; switch to what is left in Settings → AI Usage → Quota Shown. The AI Usage card on Tools reads the same quotas: at 1 × 1 it draws one double ring — outer weekly, inner 5-hour — around the mark of the tool with the least left; hover it for the numbers. At 2 × 1 it keeps the ring beside its name.
- The Claude Code quota comes from Claude Code itself: ShelfU runs your installed Claude Code's `/usage` command, so the numbers match what Claude Code reports. Claude Code must be installed; ShelfU never downloads it. The Codex quota reads the login Codex already saved on this Mac and sends it only to chatgpt.com.
- Opening the page or pressing refresh reads the quota (Claude Code at most once every 30 seconds); while the page stays open, Claude Code is read again every 5–15 minutes, and only after you used it. The last reading is kept, so the page shows it right after a restart.

**Stay awake**
- Right-click the menu-bar icon → Keep Awake and your Mac won't fall asleep for 30 minutes, 1, 2 or 4 hours, or until you turn it off. Tick Keep Screen On to keep the display awake as well.
- While it is on, every open page shows a moon in its top row, beside the settings icon: hover it for the time left, click it to end the hold.

**Overview**
- A click on the notch lands here. On the left: the Tray and Clipboard counts, the otter below them — it looks curious while a job runs, unhappy after a failure, happy after good news, and sleepy after ten quiet minutes. While a job runs, the two lines read Working / Finished items stay in the Tray.
- On the right: the five items at the front of the Tray, pinned ones first, as small tiles. Drag any tile straight into an app, right-click it for the usual menu, or click it to open the Tray with that item selected. All › opens the Tray too.
- Below the tiles: one line about the most relevant thing right now. A running job shows its progress and a Cancel button; an error or a finished job shows its words; a downloaded update shows Update ready · Restart to finish with a Restart button; a track shows cover, title and artist with previous, play-pause and next; a copy from the last two minutes shows Copied: … with All ›. With nothing else to say, it shows how many clips the Clipboard holds, with Open.
- The first time you open it after installing, a welcome card takes the Overview's place: drag a file onto the notch to try it, which finishes the card, or click Skip. After an update, the first opening explains what is new; About → What's New shows it again.

**Tools page**
- Tools is one grid: the action cards and the widgets live together. A new install fits exactly two rows with no scrolling: the music widget at 2 × 2, Keep Awake and AI Usage at 1 × 1 (Fit 1 MB takes that cell while AI Usage is off), then Capture Text and Zip. Every other card, AirDrop included, is in the + catalogue. A layout you already have stays as it is. The grid scrolls inside the page when it holds more than two rows.
- Only a real drag of files over the panel dims the action cards that cannot take them. Widgets and cards pinned to the desktop stay as they are.
- If you used the Home page in an earlier version, the widgets you placed there move into the Tools grid, at the sizes they had.
- Tap an action card to run it on the pictures or files selected on the shelf, or drop files straight onto it.
- Long-press a card (or click the pencil) to edit: drag to reorder, ⊖ to remove, the corner handle to cycle the sizes a widget offers (1 × 1, 2 × 1, 2 × 2), and the dashed + cell to add a card from the catalogue. Done or Esc to finish. Drag a card out of the panel to keep it on the desktop.

**Pin to the desktop**
- While editing, drag a card or a widget out of the shelf and it stays on the desktop as a small floating card that still takes taps and dropped files.
- Let go near the left or right screen edge and it tucks into a thin strip: hover to open it, move away and it closes after 0.6 s, or bring a file drag close and it opens to take the drop. Right-click → Always Expanded, Return to Notch or Remove from Desktop; drag it up to the notch to put it back — a widget returns to its cell on the Tools page, an action card to its cell there too.

**Copy text from the screen**
- Press `⌃⌥O`, use the menu-bar icon → Capture Text, or tap its card, then drag over any part of the screen: the text in it is recognized on this Mac, copied to the clipboard and kept in the Clipboard list, marked Capture Text.
- Needs Screen Recording permission — see [Optional](#optional).

**Live activities**
- Plug in the charger and the notch widens for a few seconds: Charging on the left, a green level bar and the percentage on the right. It also tells you once when the battery is full, and once at 20 % and 10 % while on battery.
- Connect Bluetooth headphones and the notch shows their name — for AirPods, the left, right and case levels when macOS reports them — and again when they disconnect.
- While a browser downloads into Downloads, the notch shows a progress ring and the percentage (or the size so far); when the download finishes, click the notice to put the file on the Tray. Several downloads that finish together share one notice, and a click puts all of them on the tray. Needs Full Disk Access — see [Optional](#optional).
- Each one can be turned off in Settings → Appearance → Live Activities.

**Dynamic Island style**
- On a display without a notch, ShelfU floats as a Dynamic Island: a small black pill near the top that grows into everything the notch does.
- On a MacBook you can choose it too: Settings → Appearance → Notch Style → Dynamic Island. The notch style stays the default.
- Hold the island and drag it anywhere on the screen; it stays where you let go, on each display. The panel finds its own place: it opens below the island when there is room, moves aside near a screen edge, and opens above the island when the space below is too small. While the panel is open, hold a blank part of its top row and the whole panel moves with the island. Settings → Appearance → Reset Position brings it back to the top.
- The notch style cannot be dragged.

**Arrange windows from the notch**
- Drag any window by its title bar toward the notch: a panel offers left half, full, right half and left, middle or right third. Hover a tile to see where the window will go, let go to arrange it. Move away to cancel.
- Uses Accessibility permission; turn it off in Settings → Appearance → Snap Windows at the Notch.

**Language**
- Settings → General → Language offers Follow System (the default), 简体中文 and English.
- Follow System shows Chinese when the Mac's first preferred language is Chinese — Simplified or Traditional, both in Simplified Chinese — and English for every other first language. A language you pick is shown exactly as you picked it. The switch takes effect at once, with no restart.
- Dates and times follow the Mac's region and its 12- or 24-hour setting, whatever the interface language. The Services menu item, Add to ShelfU, follows the system language.

**Quiet by design**
- One shortcut, `⌃⌥Space`, opens the Overview or closes the shelf; clicking anywhere else tucks it away, and Esc steps back one level at a time — a page returns to the Overview, the Overview folds into the notch.
- Settings → Appearance → Panel Appearance offers Follow System (the default), Black and Translucent. Follow System is black in Dark Mode and translucent in Light Mode. Reduce Transparency also draws the translucent panel black, and the island style is always black. Reduce Motion keeps the fades and drops the travel and bounce. Secondary text — times, counts, captions — is drawn brighter than before, so it stays readable on black.
- Every page switch is one fade: the page you leave fades out while the new page fades in. Over the open panel the pointer stays a plain arrow.
- Settings… is a normal window with a sidebar: General (Open at Login, Language, automatic updates with Check Now), Appearance (notch style, panel appearance, island position, now playing, live activities, snap windows), Tray, Clipboard, AI Usage, Shortcuts, System Permissions and About. The sidebar's footer says whether every permission is on; About holds the version, Check Now, What's New and the data folder. Every control applies at once.
- The four global shortcuts can be changed in Settings → Shortcuts: click a row, press the new keys, Delete to clear it, Esc to cancel. Show / Hide is `⌃⌥Space`, Clipboard Search `⌃⌥V`, Capture Text `⌃⌥O`, and Open Tray has none until you set one. A combination another app already holds leaves the previous shortcut in place.
- Updates are checked against this repository's GitHub releases. ShelfU tells you when one is out and installs it only when you say so; once it is downloaded, the Overview says Update ready · Restart to finish with a Restart button.

<a id="install"></a>

## Install

Requires a Mac with Apple Silicon (M1 or later) running macOS 13 or later. Intel Macs are not supported.

1. Download **[ShelfU.dmg](https://github.com/henryyu333/shelfu/releases/latest/download/ShelfU.dmg)**.
2. Open the downloaded file and drag **ShelfU** onto the **Applications** folder.
3. Open ShelfU from Applications. The first time, macOS will stop it — follow [First launch](#first-launch) below. You only do this once.
4. ShelfU lives in the menu bar, not the Dock. Hover the notch, or press `⌃⌥Space`.

Prefer a zip? Every release on the [Releases page](https://github.com/henryyu333/shelfu/releases/latest) also has a `.zip` of the same version.

<a id="first-launch"></a>

### First launch: "ShelfU Not Opened"

ShelfU is free and not notarized by Apple, so macOS asks you to confirm it once. Only do this for a copy you downloaded from this repository.

**macOS 15 or later**

1. Double-click ShelfU in Applications. When macOS says ShelfU was not opened, click **Done** (not Move to Trash).
2. Open **System Settings → Privacy & Security** and scroll down to **Security**.
3. Next to "ShelfU was blocked to protect your Mac", click **Open Anyway**.
4. Enter your password or use Touch ID, then click **Open Anyway** once more.

**macOS 13 or 14**

Control-click (or right-click) ShelfU in Applications, choose **Open**, then click **Open** in the dialog.

From then on ShelfU opens normally. If macOS ever says the app **is damaged**, don't bypass it — delete it and download it again from the Releases page. Apple's guide: [Safely open apps on your Mac](https://support.apple.com/102445).

### Optional

- **Start at login**: right-click the menu-bar icon → Settings → General → Open at Login.
- **System permissions**: Settings → System Permissions lists the three permissions with their state, and its last row is one switch; you can also right-click the menu-bar icon → Turn On System Permissions (3 missing)… — the number is how many are still off. Turning it on walks you through three System Settings pages in turn: Full Disk Access, Accessibility, then Screen Recording. Turn ShelfU on in each (on the Full Disk Access page, press + and choose ShelfU, or drag in the ShelfU that Finder shows), and ShelfU moves to the next one by itself; at the end, click Reopen ShelfU. macOS requires you to switch on each one yourself. Moving from Shelf to ShelfU asks for them once more (see below). Official ShelfU releases keep the grants across later updates; a build you compile yourself is signed afresh each time and may ask again.
- **Full Disk Access**: files dragged in from Downloads, Desktop, Documents or iCloud Drive no longer bring up a "ShelfU would like to access files…" dialog.
- **Accessibility** (notch drop protection): while you drag toward the notch, the pointer stays in the drop area instead of hitting the very top edge of the screen.
- **Screen Recording** (Capture Text): only the area you select is captured, text is recognized on this Mac, and the shot is not kept.
- **Uninstall**: right-click the menu-bar icon → Quit, then move ShelfU from Applications to the Trash. To also delete your saved items, remove `~/Library/Application Support/ShelfU/`.

### Coming from Shelf

ShelfU is the new name of Shelf. The first time ShelfU opens, it copies your saved items from `~/Library/Application Support/Shelf/` to `~/Library/Application Support/ShelfU/`; the old folder stays as it was, and you can delete it once everything is there. macOS treats ShelfU as a new app, so turn its system permissions and Open at login on again.

- **Updating inside Shelf**: after the update is installed, open Shelf from Applications. It renames itself to `ShelfU.app` and opens as ShelfU.
- **Installing from ShelfU.dmg**: quit Shelf and move `Shelf.app` to the Trash first, so that only ShelfU keeps using your items.

## Everyday use

| To | Do this |
| --- | --- |
| Open or close the shelf | Press `⌃⌥Space`, click the menu-bar icon, or click the notch (hover first to see the wings or the pill) — the Overview opens |
| Open a page | Click a page icon at the left of the top row: Overview, Tray, Clipboard, Tools, or AI Usage when enabled; the panel grows down to the page |
| Open the player | Click the track line in the Overview |
| Go back | Press Esc — a page returns to the Overview, and Esc again folds it into the notch; clicking outside the panel (not on the menu bar) or the shortcut closes the whole shelf |
| Put something in | Drag it toward the notch: the Tray opens as the drag arrives, and you let go to add it; or shake the pointer while dragging and drop on the basket |
| Take something out | Drag it from the shelf into any app; ⌘/⇧-click to take several at once. The Overview's tiles drag out the same way |
| Copy text back | Click a text item, then paste with `⌘V` |
| Copy image data | Select one picture and press `⌘C`, or right-click it → Copy Image — the clipboard gets PNG data, not the path |
| Search what you copied | Press `⌃⌥V` — the Clipboard page opens with the search field ready; type, pick with ↑/↓, press ⏎ |
| Switch pages | Click a page icon at the left of the top row — the current page is there too, with its capsule sliding to the new one; the panel keeps its size |
| Control music | Click the track line in the Overview to open Now Playing and drag the progress or volume bar, or use the music card on Tools |
| Pin, remove, Quick Look, AirDrop | Right-click an item (Space also opens Quick Look) |
| Copy text from the screen | Press `⌃⌥O` and drag over the text |
| Convert a picture or a PDF | Right-click a picture → Convert to; right-click several pictures → Merge into One PDF; right-click a PDF → Convert to → Word (Extract Text), or PNG (One per Page) / JPEG (One per Page) |
| Shrink a picture, zip files | Right-click a picture → Resize Images and pick 2560 / 1600 / 800 px; right-click files or folders → Compress to zip |
| Run a tool card | Open Tools, then tap a card for the selected shelf items or drop files on it |
| Arrange widgets or cards | Long-press a card or widget on Tools to edit; drag it out of the shelf to pin it to the desktop |
| Shrink a picture to a file size | Right-click it → Compress to → 500 KB / 1 MB / 2 MB / 5 MB |
| Unpack an archive | Right-click a zip, tar, 7z or rar file → Extract; the folder lands on the shelf |
| Rename files | Right-click one or more files → Rename…, check the preview, then rename; undo from the notice that follows |
| Keep the Mac awake | Right-click the menu-bar icon → Keep Awake and pick how long |
| Clear the shelf | Click the trash icon in the page's action row — only that page is cleared, pinned items stay, original files are never deleted |
| Change settings | Right-click the menu-bar icon → Settings… |
| Change a shortcut | Settings → Shortcuts: click the row, press the new combination; Delete clears it, Esc cancels |
| Check for updates | Right-click the menu-bar icon → Check for Updates…, or Settings → General → Check Now |

The full manual is in [docs/MANUAL.zh-CN.md](docs/MANUAL.zh-CN.md) (Chinese).

## Privacy

Everything stays on your Mac: no account, no telemetry, no cloud sync. The only network requests are the update check — shortly after launch and then once a day, ShelfU asks this repository's latest GitHub release whether a newer version exists — and, only if you enable the optional AI usage page, its quota lookups: the Codex quota goes to chatgpt.com with the login Codex saved, and the Claude Code quota is read by running your installed Claude Code, which talks to Anthropic itself as it always does. Nothing else is sent. The update request sends nothing but the app version in the standard User-Agent header. Turn it off in Settings → General → Check for Updates Automatically; checking by hand from the menu still works.

Clipboard text is stored in plain text in `~/Library/Application Support/ShelfU/`. Recording is always on. To keep an app's copies out, right-click one of its clipboard entries and exclude the app; to copy something you don't want kept, quit ShelfU first.

## Build from source

Requires Go 1.27 or later and Xcode Command Line Tools. Run the following command from the repository root:

```sh
go run github.com/egoist/mygo/cmd/mygo build
```

The packaged `ShelfU.app` and a `.dmg` land in a `darwin-arm64` subfolder under the output directory configured in `mygo.json`. ShelfU is written in Go with [MyGo](https://github.com/egoist/mygo). The app icon and the menu-bar icon are not in this repository: a build from source uses the default icon and shows the name in the menu bar.

### Source layout

| Path | Contents |
| --- | --- |
| `cmd/shelfu/` | Application entry point, native interface and application tests |
| `internal/` | macOS integration, storage, settings, layout, document export and AI usage modules |
| `resources/` | Third-party notices and generated native helper |
| `docs/` | User manual |

Tests stay beside the code they exercise. Run the non-interactive checks from the repository root:

```sh
go vet ./...
go test -race -count=1 ./...
```

MyGo reads the application package from `mygo.json`; the build command above also builds the native now-playing helper.

## License

ShelfU's source code is licensed under the MIT License (see [LICENSE](LICENSE)).

The ShelfU name, app icon, menu-bar icon, sea otter mascot, and associated brand artwork, including character variants and original design files, are excluded from the MIT License.

Users may install and use official ShelfU releases. Separate extraction, reproduction, modification, or redistribution of these brand assets requires authorization.

---

<a id="简体中文"></a>

## ShelfU 是什么

ShelfU 是住在 Mac 刘海里的工具：它把摄像头周围那块黑色刘海变成屏幕上能用的一部分。平时它不露面；鼠标移到刘海上，它会向两侧长出两翼或一个小箭头提示，点一下就打开「总览」。总览里是最近放入的五项——每一项都能直接拖出去——和一行「当前最相关的事」：还在进行的任务（带取消）、出错、刚做完、新版本已就绪、正在播放的歌、刚复制的内容，或剪贴板的条数。左边是海獭；不再有一级小岛。页面图标在顶栏左侧：总览、托盘、剪贴板、工具，打开 AI 用量后还有 AI 用量；点一个图标，同一个面板向下长高到 440 × 250，停在菜单栏下面，不会盖住菜单栏和窗口。总览的图标是房子，和工具页的四宫格图标一眼就能分开。如果是拖着东西过来，打开的是「托盘」页，直接等着接收。屏幕上只有画出来的黑块接鼠标，旁边的菜单栏照常可点。Esc 一级一级往回退：页面回到总览，再按一次收回刘海。

它只在菜单栏里，不占 Dock；所有内容都留在你的 Mac 上；放进来的文件不会被复制，ShelfU 只记住它们在哪儿。

## 亮点

**刘海里的托盘**
- 把文件、文件夹、图片或文字往刘海拖，刘海会提示「松开放入」，还在拖着的时候「托盘」页就已经打开。整页变成投放区：松在「放入托盘」就收进托盘；松在下方的动作格上，就按文件类型直接压缩到 1 MB、转成 PDF 或 JPEG、PDF 转 Word 或图片、解压、打成 zip，或隔空投送。结果放进托盘，原文件不动。
- 文件以缩略图显示（iPhone 拍的 HEIC 照片也有预览），可以一次拖出一个或多个（按住 ⌘ 或 ⇧ 多选），直接拖进任何应用；也可以在设置里让拖出去的东西自动离开托盘。
- 按空格快速查看；右键可以打开、在 Finder 中显示、隔空投送、复制（图片多一项「复制图片」，复制图片数据本身）、置顶或移除。
- 图片和 PDF 可以直接转换：右键图片 →「转换为」JPEG、HEIC、PNG、PDF，或识别图里的文字生成 Word，多张还能合并成一个 PDF；大图可以一键缩小；右键 PDF 可以提取文字生成 Word，或每页存成一张图片。文件和文件夹可以压缩成 zip。全部在本机完成，原文件不动，做好的文件放在托盘最前面。
- 文件整理也能就地完成：右键图片 →「压缩到」500 KB / 1 MB / 2 MB / 5 MB 以内；右键 zip、tar、7z、rar 压缩包 →「解压」；右键托盘里的文件 →「重新命名…」，像 Finder 一样替换文字、添加文字或按序号命名，改之前先预览，改完可以撤销。
- 还有别的放法：拖着东西时左右晃动指针，光标旁会出现一个暂存篮；也可以在 Finder 或其他应用里右键 →「服务」→「添加到 ShelfU」。

**剪贴板记录**
- 复制过的文字会自动收好，并记住来自哪个应用；截图和复制的图片也会进托盘。
- 按 `⌃⌥V` 搜索：输入即筛选，`↑`/`↓` 选择，回车复制并回到你原来的应用。截图和复制的图片里的文字会在本机识别，也能搜到；右键图片 →「识别文字并复制」可以直接复制图中文字。
- 重要的内容可以置顶；不想被记录的应用可以排除；密码管理器标记为隐私的密码不会被记录。

**正在播放**
- 放音乐时把鼠标移到刘海上，刘海只向左右各长出一小截：左边是封面，右边是跳动的音频条，不会向下盖住窗口。点这一小截，打开的是总览。
- 总览里的那一行就是播放器：封面、歌名、歌手，还有播放暂停和下一首。点这一行打开「正在播放」页：上一首 / 播放暂停 / 下一首、可拖动跳转的进度条，以及系统默认输出设备的音量条。有歌没歌，这一页都是同样大小。
- 音乐小组件仍留在工具页：2×2 是完整播放器，2×1 显示封面、歌名、歌手和播放暂停，1×1 点一下切换播放。

**AI 用量**（可选，默认关闭）
- 在设置 → AI 用量里打开「在刘海里显示 AI 用量」，顶栏会多出一个页面图标：左栏是 Codex、Claude Code 的套餐额度卡（每张卡内外双圈：外圈每周、内圈 5 小时），右栏是这台电脑上 omp、Claude Code、Codex 的 token 用量——今天 / 7 天 / 30 天 / 全部、四个数字、按来源叠加的柱图和图例。整页一屏看全，不用滚动。悬停额度卡看重置时间和按模型的每周额度；悬停 Token 或费用数字看它的组成部分。
- 额度默认显示「已用」；可以在 设置… → AI 用量 →「额度显示」改成「剩余」。工具页的 AI 用量卡用同一份额度数据：1×1 只画一个双圈——外圈每周、内圈 5 小时——中间是剩余额度最少的那个工具的标志，鼠标悬停看具体数字；2×1 仍是圈在左、名字在右。
- Claude Code 的额度由 Claude Code 自己给出：ShelfU 运行你装好的 Claude Code 的 `/usage` 命令，所以数字和 Claude Code 显示的一致。需要先装好 Claude Code，ShelfU 不会替你下载。Codex 的额度读取 Codex 保存在本机的登录信息，只发给 chatgpt.com。
- 打开这一页或点刷新时会查询额度（Claude Code 最多 30 秒查一次）；页面一直开着时，只有你用过 Claude Code 才会每 5–15 分钟再查一次。上次的结果会保存，重启后打开就能看到。

**保持唤醒**
- 右键菜单栏图标 →「保持唤醒」，选 30 分钟、1 小时、2 小时、4 小时或「一直（直到关闭）」，这段时间里 Mac 不会自动睡眠；勾选「屏幕也保持亮着」，屏幕也不会熄灭。
- 开启后，打开任何一页都能在顶栏看到一个月亮图标，就在设置图标旁边：鼠标停在上面看剩余时间，点一下结束。

**总览**
- 点刘海就到这里。左边是「托盘 N 项」「剪贴板 N 条」两行字，下面是海獭——有任务在跑时好奇、出错时装作难过、有好消息时开心、安静十分钟后打瞌睡；任务进行中时两行字变成「正在处理 / 做完的会留在托盘」。
- 右边是托盘最前面的五项（置顶的排在最前），以小格子显示。抓住格子就能直接拖进任何应用；右键是常用的菜单；点一下打开「托盘」页并选中这一项。「全部 ›」同样打开「托盘」页。
- 格子下面是一行「当前最相关的事」，一次只显示一件：进行中的任务带进度条和取消按钮；出错或刚做完显示那句话；下载好的新版本显示「新版本已就绪 · 重新启动」和「重新启动」按钮；有歌时显示封面、歌名、歌手和上一首、播放暂停、下一首；两分钟内复制过东西时显示「刚复制：…」和「全部 ›」。都没有时显示剪贴板的条数和「打开」。
- 装好第一次打开时，这里换成欢迎卡：把一个文件拖到刘海试试，拖进来就算完成；也可以点「跳过」。更新后第一次展开会介绍新功能，关于页的「新功能介绍」可以再看一次。

**工具页**
- 工具页是一张统一的网格：动作卡和小组件放在一起。全新安装时正好两行、不用滚动：音乐 2×2，保持唤醒和 AI 用量各 1×1（AI 用量页关着时，这一格是「压缩到 1 MB」），下面是框选文字和打成 zip。其余卡片（包括隔空投送）都在「+」打开的目录里，可以自己加。已有的布局不变。超过两行时在页内滚动。
- 只有真的把文件拖过面板时，不能处理的动作卡才变暗；小组件和固定到桌面的卡片保持原样。
- 如果你用过旧版本的「主页」，那里的小组件会按原来的尺寸搬进工具页的网格。
- 点按动作卡，对托盘里选中的图片或文件执行；也可以把文件直接拖到卡片上。
- 长按卡片（或点铅笔图标）进入编辑：拖动排序、点 ⊖ 删除、拖右下角的把手在小组件支持的 1×1、2×1、2×2 之间切换、点虚线「+」格从目录添加。点「完成」或按 Esc 结束。把卡片拖出面板，它就固定到桌面。

**固定到桌面**
- 编辑时把小组件或卡片拖出面板，它会留在桌面上成为一张小浮窗，照样可以点按、接收拖入的文件。
- 松在屏幕左右边缘附近，它会收成一条窄条：鼠标移上去展开，移开 0.6 秒后收回；拖着文件靠近也会展开接收。右键可选「始终展开」「放回刘海」「从桌面移除」；把它拖回刘海也能放回——小组件和动作卡都回到工具页原来的格子。

**框选屏幕文字**
- 按 `⌃⌥O`、右键菜单栏图标 →「框选文字」，或点它的卡片，然后在屏幕上框一块区域：里面的文字在本机识别后直接复制到剪贴板，并记进剪贴板页，来源显示「框选文字」。
- 需要「屏幕录制」权限，见下面的[可选](#可选)。

**实时活动**
- 插上电源时，刘海向两边变宽几秒：左边是「正在充电」，右边是绿色电量条和电量数字。充满时提醒一次；不插电时，电量降到 20% 和 10% 各提醒一次。
- 蓝牙耳机连上时，刘海显示耳机名字；AirPods 在系统给出电量时还会显示左耳、右耳和充电盒的电量。断开时也会提示一次。
- 浏览器往「下载」文件夹下载文件时，刘海显示进度环和百分比（不知道总大小时显示已下载多少）；下载完成后点一下提示，文件就放进「托盘」。同时下载完的几个文件合成一条提示，点一下全部放进托盘。需要「完整磁盘访问」，见下面的[可选](#可选)。
- 每一项都可以在 设置… →「外形与提示」→「实时活动」里关掉。

**灵动岛样式**
- 在没有刘海的屏幕上，ShelfU 变成一颗悬浮的灵动岛：屏幕顶部一颗小黑胶囊，刘海能做的事它都能做。
- MacBook 上也可以选：设置… →「外形与提示」→「刘海样式」→「灵动岛」。默认仍是刘海样式。
- 按住小岛可以拖到屏幕任何位置，松手就留在那里，每块屏幕分别记住。展开后的面板自己找位置：下方放得下就向下展开，靠边时和岛错开，下方放不下就向岛上方展开。面板展开时，按住顶栏的空白处也能拖，整块面板跟着岛一起走。设置… →「外形与提示」→「恢复默认位置」让它回到屏幕顶部。
- 刘海样式不能拖。

**语言**
- 设置… → 通用 →「语言」有三档：「跟随系统」（默认）、「简体中文」和「English」。
- 跟随系统时，Mac 的首选语言是中文就显示中文（简体、繁体都显示简体中文），其他语言显示英文；选定哪一档就按哪一档显示。切换立即生效，不用重新打开 ShelfU。
- 日期和时间按 Mac 的地区和 12/24 小时制显示；服务菜单里的「添加到 ShelfU」跟系统语言走。

**拖窗口到刘海分屏**
- 拖着任意窗口的标题栏往刘海走，会出现排列面板：左半、全屏、右半、左 1/3、中 1/3、右 1/3。指针停在哪一格就预览窗口会落在哪儿，松手就排好；拖离刘海就取消。
- 需要「辅助功能」权限；可以在 设置… →「外形与提示」→「拖窗口到刘海分屏」关掉。

**安静、不打扰**
- 一个快捷键 `⌃⌥Space` 打开总览或收起；点别处就自动缩回刘海。Esc 一级一级往回退：先回到总览，再按一次收回刘海。
- 设置… →「外形与提示」→「展开外观」有三档：「跟随系统」（默认）、「纯黑」和「半透明」。跟随系统时深色纯黑、浅色半透明；打开「减少透明度」时半透明也是纯黑；灵动岛样式始终纯黑。「减少动态效果」时只保留淡入淡出。时间、计数、说明这类次要文字比过去更亮，在纯黑上更容易看清。
- 总览和各页面用同一种切换：旧页淡出，新页淡入。指针在展开的面板上始终是普通箭头。
- 设置… 是一个带侧栏的普通窗口：通用（开机时自动启动、语言、自动更新和「现在检查」）、外形与提示（刘海样式、展开外观、灵动岛位置、正在播放、实时活动、拖窗口分屏）、托盘、剪贴板、AI 用量、快捷键、系统权限和关于。侧栏底部显示权限状态；关于页有版本号、「现在检查」「新功能介绍」和数据文件夹。改完立即生效。
- 四个全局快捷键可以在 设置… → 快捷键 里改：点一下那一行，按新的组合键；Delete 清空，Esc 取消。显示 / 收起是 `⌃⌥Space`、剪贴板搜索 `⌃⌥V`、框选文字 `⌃⌥O`，「打开托盘」默认不设。组合被其他应用占用时，保留原来的组合并提示。
- 新版本从本仓库的 GitHub Releases 检查；有更新时会提示你，你点了才会安装。下载好后，总览里显示「新版本已就绪 · 重新启动」。

<a id="安装"></a>

## 安装

需要 Apple Silicon 芯片（M1 或更新）的 Mac，系统 macOS 13 或以上。不支持 Intel 芯片的 Mac。

1. 下载 **[ShelfU.dmg](https://github.com/henryyu333/shelfu/releases/latest/download/ShelfU.dmg)**。
2. 打开下载好的文件，把 **ShelfU** 拖到 **Applications（应用程序）** 文件夹上。
3. 从“应用程序”里打开 ShelfU。第一次打开会被系统拦下，按下面的[第一次打开](#第一次打开)操作即可，只需要做一次。
4. ShelfU 在屏幕顶部的菜单栏里，不在 Dock 上。把鼠标移到刘海上，或按 `⌃⌥Space`。

想要 zip 压缩包？[Releases 页面](https://github.com/henryyu333/shelfu/releases/latest)的每个版本里也有同一版本的 `.zip`。

<a id="第一次打开"></a>

### 第一次打开：提示“未打开 ShelfU”

ShelfU 是免费软件，没有经过 Apple 公证，所以 macOS 会让你确认一次。请只对从本仓库下载的版本这样做。

**macOS 15 或更新**

1. 在“应用程序”里双击 ShelfU。系统提示未打开时，点 **完成**（不要点“移到废纸篓”）。
2. 打开 **系统设置 → 隐私与安全性**，往下滚动到 **安全性**。
3. 在“已阻止 ShelfU 以保护 Mac”旁边，点 **仍要打开**。
4. 输入开机密码或用触控 ID 确认，再点一次 **仍要打开**。

**macOS 13 或 14**

在“应用程序”里按住 Control 点按（或右键点按）ShelfU，选 **打开**，再在弹窗里点 **打开**。

之后就能正常打开了。如果系统提示 App **已损坏**，不要绕过，删掉后从 Releases 页面重新下载。Apple 官方说明：[在 Mac 上安全地打开 App](https://support.apple.com/zh-cn/102445)。

### 可选

- **开机自动启动**：右键菜单栏图标 → 设置… → 通用 →「开机时自动启动」。
- **系统权限**：设置… → 系统权限 里列出三项权限的状态，最后一行是一个开关；也可以右键菜单栏图标 →「开启系统权限」。打开开关后，ShelfU 依次打开系统设置里的三页：「完整磁盘访问」「辅助功能」「屏幕录制」。在每页打开 ShelfU 的开关（「完整磁盘访问」这一页要点 + 选 ShelfU，或把访达里选中的 ShelfU 拖进列表），ShelfU 会自动走到下一项；最后点「重新打开 ShelfU」。macOS 要求每一项都由你自己打开。从 Shelf 换到 ShelfU 时要再开一次（见下）；ShelfU 的正式发布版在之后更新时保留授权，自己编译的构建每次签名都会变，可能要重新开启。
- **完整磁盘访问**：从「下载」「桌面」「文稿」或 iCloud 云盘拖文件进来时，不再弹出「ShelfU 想要取用……中的文件」。
- **辅助功能**（刘海拖放保护）：拖着东西靠近刘海时，指针会停在托盘的接收区里，不会顶到屏幕最上沿。
- **屏幕录制**（框选文字）：只截取你框选的那一块，文字在本机识别，不保存截图。
- **卸载**：右键菜单栏图标 → 退出 ShelfU，再把“应用程序”里的 ShelfU 移到废纸篓。如果连保存的内容也要删除，删掉 `~/Library/Application Support/ShelfU/` 文件夹。

### 从 Shelf 升级

ShelfU 是 Shelf 的新名字。ShelfU 第一次打开时，会把 `~/Library/Application Support/Shelf/` 里保存的内容复制到 `~/Library/Application Support/ShelfU/`；旧文件夹保持原样，确认内容都在以后可以删掉。macOS 会把 ShelfU 当成一个新应用，所以系统权限和「开机时自动启动」要重新打开一次。

- **在 Shelf 里应用内更新**：更新装好后，在“应用程序”里打开 Shelf。它会自己改名为 `ShelfU.app` 并以 ShelfU 打开。
- **用 ShelfU.dmg 安装**：先退出 Shelf，把 `Shelf.app` 移到废纸篓，只让 ShelfU 使用你的内容。

## 日常使用

| 想做什么 | 怎么做 |
| --- | --- |
| 打开或收起 | 按 `⌃⌥Space`、单击菜单栏图标，或点刘海（可以先悬停看两翼或小箭头）——打开的是总览 |
| 打开某一页 | 点顶栏左侧的页面图标：总览、托盘、剪贴板、工具，开了 AI 用量时还有 AI 用量；面板向下长高 |
| 打开播放器 | 点总览里正在播放的那一行 |
| 退回上一层 | 按 Esc：先回到总览，再按一次收回刘海。点面板外（菜单栏高度内不算）或再按快捷键都直接收起 |
| 放东西进来 | 拖着它靠近刘海，还没松手「托盘」页就打开了，松手放进去；或拖拽时左右晃动指针，放进暂存篮 |
| 拿东西出去 | 从托盘拖到任意应用；按住 `⌘` 或 `⇧` 可以一次拖多个。总览里的格子也能直接拖出去 |
| 把文字复制回来 | 单击文字项，再在任意地方 `⌘V` 粘贴 |
| 复制图片数据 | 选中一张图片后按 `⌘C`，或右键图片 →「复制图片」，再在目标应用 `⌘V` 粘贴（复制的是图片，不是路径） |
| 搜索复制过的内容 | 按 `⌃⌥V`，「剪贴板」页直接打开、搜索框已就位；输入关键词，`↑`/`↓` 选择，回车 |
| 切换页面 | 点顶栏左侧的页面图标（当前页的图标也在，底下的胶囊会滑过去）；面板尺寸不变 |
| 控制音乐 | 点总览里的歌名那一行打开「正在播放」，拖动进度和音量；也可以用工具页的音乐卡片 |
| 置顶、移除、快速查看、隔空投送 | 右键项目（快速查看也可以直接按空格） |
| 框选屏幕文字 | 按 `⌃⌥O`，框住要复制的文字 |
| 用工具卡 | 打开「工具」页，点按卡片处理托盘里选中的项目，或把文件拖到卡片上 |
| 排列小组件或卡片 | 长按「工具」页的卡片或小组件进入编辑；把它们拖出面板就固定到桌面 |
| 转换图片格式、转 PDF 或 Word | 右键图片 →「转换为」；多张图片右键 →「合并为一个 PDF」；右键 PDF →「转换为」Word 或每页一张图片 |
| 缩小图片、压缩成 zip | 右键图片 →「缩小图片」选大小；右键文件或文件夹 →「压缩为 zip」 |
| 压缩图片到指定大小 | 右键图片 →「压缩到」→ 500 KB / 1 MB / 2 MB / 5 MB 以内 |
| 解压压缩包 | 右键 zip、tar、7z、rar 文件 →「解压」，解出的文件夹放进托盘 |
| 批量重新命名 | 右键一个或多个文件 →「重新命名…」，看过预览再点「改名」；改完可以在提示上撤销 |
| 不让 Mac 睡眠 | 右键菜单栏图标 →「保持唤醒」，选时长 |
| 清空当前列表 | 点页面动作行右边的垃圾桶图标——只清这一页，置顶的保留，原文件不会被删除 |
| 修改设置 | 右键菜单栏图标 → 设置… |
| 修改快捷键 | 设置… → 快捷键：点一下那一行，按新的组合键；Delete 清空，Esc 取消 |
| 检查更新 | 右键菜单栏图标 →「检查更新…」，或 设置… → 通用 →「现在检查」 |

完整说明见 [使用手册](docs/MANUAL.zh-CN.md)。

## 隐私

一切都留在你的 Mac 上：不用注册账号，没有遥测，没有云同步。联网请求只有两类：检查更新（启动后不久一次、之后每天一次，向本仓库 GitHub 上的最新 Release 询问有没有新版本，请求里只带一个标准的 User-Agent 头）；以及可选的 AI 用量页的额度查询——只有打开它才会发生：Codex 的额度用 Codex 保存的登录信息发给 chatgpt.com；Claude Code 的额度通过运行你装好的 Claude Code 读取，由 Claude Code 自己照常联系 Anthropic。除此之外不发送任何内容。「自动检查更新」可以在 设置… → 通用 里关闭；关闭后仍可在菜单里手动检查。

剪贴板文字以明文保存在 `~/Library/Application Support/ShelfU/`。剪贴板记录始终开启。不想记录某个应用复制的内容，右键它的剪贴板条目把应用排除；要复制不希望被保存的内容时，先退出 ShelfU 再复制。

## 从源码构建

需要 Go 1.27 或以上，以及 Xcode Command Line Tools。在仓库根目录运行以下命令：

```sh
go run github.com/egoist/mygo/cmd/mygo build
```

打包好的 `ShelfU.app` 和 `.dmg` 在 `mygo.json` 配置的输出目录下的 `darwin-arm64` 子文件夹中。ShelfU 用 Go 和 [MyGo](https://github.com/egoist/mygo) 编写。应用图标和菜单栏图标不在本仓库里：自己从源码构建时用的是默认图标，菜单栏里显示文字名称。

### 源码结构

| 路径 | 内容 |
| --- | --- |
| `cmd/shelfu/` | 应用入口、原生界面和应用测试 |
| `internal/` | macOS 集成、存储、设置、布局、文档导出和 AI 用量模块 |
| `resources/` | 第三方声明和构建生成的原生辅助模块 |
| `docs/` | 使用手册 |

测试和它们覆盖的代码放在同一目录。在仓库根目录运行非交互式检查：

```sh
go vet ./...
go test -race -count=1 ./...
```

MyGo 从 `mygo.json` 读取应用入口；上面的构建命令也会生成正在播放功能使用的原生辅助模块。

## 许可

ShelfU 的源代码采用 MIT 许可证，详见 [LICENSE](LICENSE)。

ShelfU 的品牌名称、应用图标、菜单栏图标、海獭品牌角色及其相关视觉素材（包括不同表情、姿态和原始设计文件）不属于 MIT 许可证的授权范围。

用户可以正常安装和使用官方发布的 ShelfU 应用，但未经授权，不得将上述品牌素材提取后用于其他产品或独立复制、修改和再分发。
