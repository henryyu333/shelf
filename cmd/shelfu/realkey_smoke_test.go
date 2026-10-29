//go:build darwin

package main

// Real-window proof for the two regressions the installed build showed
// (PLAN.md 验收反馈, 2026-10-20): the tray's ⌘C never reached the
// non-activating panel, and a refresh click could answer nothing visible.
// These tests drive the actual windows and post real key/pointer events;
// they are gated by SHELF_BASKET_SMOKE=1 like the other smoke tests and
// keep their screenshots in /tmp/shelf-fix2.

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ebitengine/purego"
	"github.com/ebitengine/purego/objc"
	"github.com/egoist/mygo"
	"github.com/egoist/mygo/transfer"
	"shelf/internal/aiusage"
	"shelf/internal/mac"
	"shelf/internal/settings"
	"unsafe"
)

const fix2ShotDir = "/tmp/shelf-fix2"

// fix2Shot screenshots the notch window to /tmp/shelf-fix2/name.png.
func fix2Shot(t *testing.T, a *shelf, name string) string {
	t.Helper()
	if a.win == nil {
		t.Fatal("no notch window")
	}
	var num int
	onMain(func() { num = mac.WindowNumber(a.win.NativeHandle()) })
	if num == 0 {
		t.Fatalf("%s: window has no CGWindowID", name)
	}
	if err := os.MkdirAll(fix2ShotDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if os.Getenv("SHELF_FIX2_BASELINE") == "1" {
		name = "before-" + name
	}
	time.Sleep(250 * time.Millisecond)
	shot := filepath.Join(fix2ShotDir, name+".png")
	out, err := exec.Command("screencapture", "-x", "-o", fmt.Sprintf("-l%d", num), shot).CombinedOutput()
	if err != nil {
		t.Fatalf("%s: screencapture failed (%v): %s", name, err, out)
	}
	t.Logf("shot %s -> %s", name, shot)
	return shot
}

// objcSend/objcStr/objcData build the raw-pasteboard calls both directions
// of the snapshot share with the private smoke writer.
func objcSend(obj objc.ID, sel string, args ...any) objc.ID {
	return obj.Send(objc.RegisterName(sel), args...)
}

func objcStr(s string) objc.ID {
	return objcSend(objc.ID(objc.GetClass("NSString")), "stringWithUTF8String:", s)
}

func objcData(b []byte) objc.ID {
	return objcSend(objc.ID(objc.GetClass("NSData")), "dataWithBytes:length:", b, uintptr(len(b)))
}

// Snapshot every pasteboard item with all of its UTI -> data pairs, not
// just the formats mygo.Clipboard understands: mygo rewrites unknown types
// like org.nspasteboard.ConcealedType, and restoring without that marker
// could let a running ShelfU record what was on the board. Restore writes
// the same raw NSPasteboardItems back.
func snapshotClipboard(t *testing.T) {
	t.Helper()
	var snap []map[string][]byte
	onMain(func() {
		board := objcSend(objc.ID(objc.GetClass("NSPasteboard")), "generalPasteboard")
		items := objcSend(board, "pasteboardItems")
		n := uintptr(objcSend(items, "count"))
		for i := range n {
			item := objcSend(items, "objectAtIndex:", i)
			types := objcSend(item, "types")
			m := uintptr(objcSend(types, "count"))
			got := make(map[string][]byte, m)
			for j := range m {
				tn := objcSend(types, "objectAtIndex:", j)
				u := objcGoStr(tn)
				if u == "" {
					continue
				}
				if d := objcSend(item, "dataForType:", tn); d != 0 {
					bp := objcSend(d, "bytes")
					bl := uintptr(objcSend(d, "length"))
					got[u] = append([]byte(nil), unsafe.Slice(*(**byte)(unsafe.Pointer(&bp)), bl)...)
				} else {
					got[u] = nil
				}
			}
			snap = append(snap, got)
		}
	})
	t.Cleanup(func() {
		onMain(func() {
			board := objcSend(objc.ID(objc.GetClass("NSPasteboard")), "generalPasteboard")
			objcSend(board, "clearContents")
			if len(snap) == 0 {
				return
			}
			arr := objcSend(objcSend(objc.ID(objc.GetClass("NSMutableArray")), "alloc"), "init")
			defer objcSend(arr, "release")
			for _, itm := range snap {
				pb := objcSend(objcSend(objc.ID(objc.GetClass("NSPasteboardItem")), "alloc"), "init")
				for uti, data := range itm {
					objcSend(pb, "setData:forType:", objcData(data), objcStr(uti))
				}
				objcSend(pb, "setData:forType:", objcData(nil), objcStr("org.nspasteboard.ConcealedType"))
				objcSend(arr, "addObject:", pb)
				objcSend(pb, "release")
			}
			if objcSend(board, "writeObjects:", arr) == 0 {
				t.Errorf("restore clipboard items")
			}
		})
	})
}

func objcGoStr(s objc.ID) string {
	p := objcSend(s, "UTF8String")
	if p == 0 {
		return ""
	}
	n := objcSend(s, "lengthOfBytesUsingEncoding:", 4) // NSUTF8StringEncoding
	if n == 0 {
		return ""
	}
	bp := *(*unsafe.Pointer)(unsafe.Pointer(&p))
	return string(unsafe.Slice(*(**byte)(unsafe.Pointer(&bp)), uintptr(n)))
}

// The smoke writer adds a concealed marker atomically with the real PNG
// data so an installed ShelfU's history does not collect test fixtures.
// The real key dispatch, copy logic, and general pasteboard remain in use.
type fix2PrivateClipboard struct{}

func (fix2PrivateClipboard) writeText(text string) error {
	return writePrivateClipboard("public.utf8-plain-text", []byte(text))
}

func (fix2PrivateClipboard) writeImage(png []byte) error {
	return writePrivateClipboard("public.png", png)
}

func writePrivateClipboard(format string, data []byte) error {
	send, str, nsData := objcSend, objcStr, objcData
	item := send(send(objc.ID(objc.GetClass("NSPasteboardItem")), "alloc"), "init")
	defer send(item, "release")
	if send(item, "setData:forType:", nsData(data), str(format)) == 0 ||
		send(item, "setData:forType:", nsData(nil), str("org.nspasteboard.ConcealedType")) == 0 {
		return fmt.Errorf("prepare private smoke clipboard")
	}
	items := send(objc.ID(objc.GetClass("NSArray")), "arrayWithObject:", item)
	board := send(objc.ID(objc.GetClass("NSPasteboard")), "generalPasteboard")
	send(board, "clearContents")
	if send(board, "writeObjects:", items) == 0 {
		return fmt.Errorf("write private smoke clipboard")
	}
	return nil
}

// clickAt posts a real left click at screen point (x, y) through the HID
// tap — the same path a physical mouse takes, so AppKit routes it to the
// topmost window. The pointer is put back afterwards.
func clickAt(t *testing.T, x, y float64) {
	t.Helper()
	lib, err := purego.Dlopen("/System/Library/Frameworks/CoreGraphics.framework/CoreGraphics", purego.RTLD_LAZY)
	if err != nil {
		t.Fatalf("CoreGraphics: %v", err)
	}
	var (
		create   func(source uintptr, typ uint32, p mac.CGPoint, button uint32) uintptr
		post     func(tap uint32, ev uintptr)
		setField func(ev uintptr, field uint32, value int64)
		createE  func(source uintptr) uintptr
		getLoc   func(ev uintptr) mac.CGPoint
		warp     func(p mac.CGPoint) int32
		release  func(cf uintptr)
	)
	purego.RegisterLibFunc(&create, lib, "CGEventCreateMouseEvent")
	purego.RegisterLibFunc(&post, lib, "CGEventPost")
	purego.RegisterLibFunc(&setField, lib, "CGEventSetIntegerValueField")
	purego.RegisterLibFunc(&createE, lib, "CGEventCreate")
	purego.RegisterLibFunc(&getLoc, lib, "CGEventGetLocation")
	purego.RegisterLibFunc(&warp, lib, "CGWarpMouseCursorPosition")
	libCF, err := purego.Dlopen("/System/Library/Frameworks/CoreFoundation.framework/CoreFoundation", purego.RTLD_LAZY)
	if err != nil {
		t.Fatalf("CoreFoundation: %v", err)
	}
	purego.RegisterLibFunc(&release, libCF, "CFRelease")

	const (
		leftDown = 1
		leftUp   = 2
		moved    = 5
		hidTap   = 0
	)
	var old mac.CGPoint
	if ev := createE(0); ev != 0 {
		old = getLoc(ev)
		release(ev)
	}
	// The notch overlay takes the mouse only over the shell it drew, and
	// its gate follows the pointer: a press sent while the pointer still
	// sits elsewhere reaches the window underneath and makes WindowAtPoint
	// answer that window. Move the pointer there first and give the move
	// monitor a beat to open the gate (the same precondition the pin tests
	// use), then check the target and press.
	warp(mac.CGPoint{X: x, Y: y})
	move := create(0, moved, mac.CGPoint{X: x, Y: y}, 0)
	post(hidTap, move)
	release(move)
	time.Sleep(150 * time.Millisecond)
	requireOwnMouseTarget(t, x, y)
	for _, typ := range []uint32{leftDown, leftUp} {
		ev := create(0, typ, mac.CGPoint{X: x, Y: y}, 0)
		if ev == 0 {
			t.Fatal("CGEventCreateMouseEvent returned nil")
		}
		setField(ev, 1 /* kCGMouseEventClickState */, 1)
		post(hidTap, ev)
		release(ev)
		time.Sleep(60 * time.Millisecond)
	}
	warp(old)
	time.Sleep(600 * time.Millisecond) // let the click, refresh and toast land
}

// postCmdC sends a real ⌘C through System Events, exactly what the user
// pressed: WindowServer delivers it, so a Carbon global hotkey sees it
// before the frontmost app does.
func postCmdC(t *testing.T) {
	t.Helper()
	out, err := exec.Command("osascript", "-e",
		`tell application "System Events" to key code 8 using {command down}`).CombinedOutput()
	if err != nil {
		t.Fatalf("⌘C: %v: %s", err, out)
	}
	time.Sleep(700 * time.Millisecond) // let the hotkey, copy and toast land
}

// TestSmokeTrayCmdCCopiesThroughTheRealKey proves the tray's ⌘C works with
// the panel out of focus (the overlay is never the key window): the image
// lands on the system clipboard as PNG and the toast says so.
func TestSmokeTrayCmdCCopiesThroughTheRealKey(t *testing.T) {
	a := redesignShelf(t)
	snapshotClipboard(t)

	// The real Carbon key for Cmd+C only: the Esc/Space registers stay the
	// smoke stubs so the test never steals them from the user.
	origReg, origUnreg := registerKey, unregisterKey
	registerKey = func(acc string, fn func()) error {
		if acc == "Cmd+C" {
			return mygo.GlobalShortcut.Register(acc, fn)
		}
		return nil
	}
	unregisterKey = func(acc string) {
		if acc == "Cmd+C" {
			mygo.GlobalShortcut.Unregister(acc)
		}
	}
	t.Cleanup(func() {
		onMain(func() {
			a.releaseKeys()
			a.hide(hidePassive)
			a.write = nil
		})
		mygo.GlobalShortcut.Unregister("Cmd+C")
		registerKey, unregisterKey = origReg, origUnreg
	})

	pngBytes := testPNG(t)
	var keyIsKey bool
	onMain(func() {
		a.write = fix2PrivateClipboard{}
		if err := a.store.AddImage(pngBytes); err != nil {
			t.Errorf("add image: %v", err)
		}
		a.selection.only(a.store.Items[0].ID)
	})
	expandShelf(t, a)
	time.Sleep(500 * time.Millisecond) // selection frame -> ⌘C held
	onMain(func() { keyIsKey = mac.IsKeyWindow(a.win.NativeHandle()) })
	if keyIsKey {
		t.Fatal("the overlay became the key window; the global-key fix is not what is being tested")
	}
	fix2Shot(t, a, "cmdc-selected")

	onMain(func() {
		if err := (fix2PrivateClipboard{}).writeText("shelf-fix2-sentinel"); err != nil {
			t.Errorf("clipboard sentinel: %v", err)
		}
	})
	postCmdC(t)

	var got []byte
	onMain(func() { got, _ = mygo.Clipboard.ReadFormat(transfer.PNG) })
	fix2Shot(t, a, "cmdc-result")
	if !bytes.Equal(got, pngBytes) {
		t.Errorf("⌘C left %d PNG bytes on the clipboard, want the selected %d", len(got), len(pngBytes))
	}
	var status string
	var types []string
	onMain(func() { status, types = a.status, mac.ClipboardTypes() })
	t.Logf("keyWindow=%v clipboardTypes=%v PNG=%d want=%d status=%q", keyIsKey, types, len(got), len(pngBytes), status)
	if !strings.Contains(status, "已复制图片") {
		t.Errorf("status = %q, want the 已复制图片 toast", status)
	}
}

// TestSmokeUsageRefreshAnswers reproduces the user's stuck state: Codex is
// in flight when the panel hides; the cancelled attempt must not block the
// next look, so reopening asks again and leaves 「正在查询…」 behind. The
// refresh button then has something to say instead of doing nothing.
func TestSmokeUsageRefreshAnswers(t *testing.T) {
	a := redesignShelf(t)
	var calls int
	var mu sync.Mutex
	var f *usageFeature
	onMain(func() {
		f = newUsageFeature(a, usageDeps{
			fetchCodex: func(ctx context.Context) aiusage.Quota {
				mu.Lock()
				calls++
				n := calls
				mu.Unlock()
				if n == 1 {
					<-ctx.Done() // the user leaves the page while this runs
					return aiusage.Quota{Tool: aiusage.ToolCodex, Status: aiusage.QuotaFailed, FetchedAt: time.Now()}
				}
				return aiusage.Quota{Tool: aiusage.ToolCodex, Status: aiusage.QuotaExpired, FetchedAt: time.Now()}
			},
			fetchClaude: func(context.Context) aiusage.Quota {
				return aiusage.Quota{Tool: aiusage.ToolClaude, Status: aiusage.QuotaExpired, FetchedAt: time.Now()}
			},
			pace: aiusage.Pace,
			now:  time.Now, // local usage stays off: quota cards only
		})
		a.settings.AIUsage = settings.AIUsageSettings{
			Enabled: true, QuotaCodex: true, QuotaClaude: true,
		}
		a.registerPage(f)
		a.usage = f
		a.setTab(pageUsage)
	})
	t.Cleanup(func() {
		onMain(func() {
			f.stopWork()
			a.hide(hidePassive)
			if a.settingsWin != nil {
				a.settingsWin.Close()
			}
			a.usage = nil
			a.settings.AIUsage.Enabled = false
		})
	})
	expandShelf(t, a)
	onMain(func() { a.setTab(pageUsage) }) // show() opens the overview
	// The page's own transition must settle before it may start a fetch.
	time.Sleep(300 * time.Millisecond)
	onMain(func() { f.tick(time.Now()) }) // Codex hangs, Claude answers expired
	time.Sleep(500 * time.Millisecond)
	fix2Shot(t, a, "refresh-inflight")

	onMain(func() {
		a.hide(hidePassive) // cancels the Codex fetch mid-flight
	})
	time.Sleep(400 * time.Millisecond)
	expandShelf(t, a)
	onMain(func() {
		a.setTab(pageUsage)
		a.openSettings() // the user's settings and panel were open together
	})
	// The AI 用量 page starts no fetch or scan until its 240ms transition
	// has settled (a switch must not pay for credential reads and local
	// log walks), so the fresh look happens after it, not on its first
	// frame.
	time.Sleep(300 * time.Millisecond)
	onMain(func() { f.tick(time.Now()) }) // must look again, not sit out the interval
	time.Sleep(700 * time.Millisecond)
	mu.Lock()
	ran := calls
	mu.Unlock()
	fix2Shot(t, a, "refresh-reopened")
	t.Logf("Codex fetches after reopening=%d", ran)
	if ran < 2 {
		t.Errorf("reopening did not ask again: %d Codex fetches", ran)
	}

	// The settings window is out of the way again before the pointer test
	// below: it was only here to prove a panel and the settings can be open
	// together, and it would take the synthetic click.
	onMain(func() {
		if a.settingsWin != nil {
			a.settingsClosed() // the product's close: it hands the app back
		}
	})
	time.Sleep(150 * time.Millisecond)

	// The button itself: a real pointer click on the refresh glyph answers
	// with the wait instead of silence (「刚查过，N 秒后可再刷新」). The glyph
	// sits in the back row, 45 pt under the window top and 413 pt from its
	// sentinel status tells a hit from a miss.
	b := a.win.Bounds()
	var status string
	onMain(func() { a.status = "click-probe" })
	// The refresh button ends the page's action row: 12pt margin + 22pt,
	// measured inside the shell (the window adds its ear margins).
	// The panel must be the window at that point: another copy of ShelfU
	// (the installed one) opens its own notch panel on hover and can sit
	// above ours, and the guard rightly refuses a click meant for it. A
	// raise of our own window is enough: it is the same status level and
	// the last ordered front wins.
	onMain(func() {
		a.win.ShowInactive()
		a.win.Invalidate()
	})
	time.Sleep(120 * time.Millisecond)
	clickAt(t, float64(b.X)+float64(b.Width-sheetW)/2+417, float64(b.Y)+45)
	onMain(func() { status = a.status })
	fix2Shot(t, a, "refresh-held-back")
	t.Logf("refresh click status=%q", status)
	if status == "click-probe" {
		t.Errorf("the refresh click gave no feedback (window %+v)", b)
	}
	if !strings.Contains(status, "刚查过") {
		t.Errorf("refresh click inside the gap said %q, want 刚查过…", status)
	}
}
