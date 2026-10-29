//go:build darwin

package main

import (
	"os"
	"testing"
	"time"

	"github.com/ebitengine/purego"
	"github.com/ebitengine/purego/objc"
	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"shelf/internal/mac"
)

// windowOwnerPID is the process that owns a window number, so a skip or a
// failure says whether a foreign app or our own test window took a point.
func windowOwnerPID(t *testing.T, num int) int {
	t.Helper()
	lib, err := purego.Dlopen("/System/Library/Frameworks/CoreGraphics.framework/CoreGraphics", purego.RTLD_LAZY)
	if err != nil {
		return 0
	}
	var copyList func(uint32, uint32) uintptr
	purego.RegisterLibFunc(&copyList, lib, "CGWindowListCopyWindowInfo")
	pid := 0
	onMain(func() {
		list := objc.ID(copyList(1, 0))
		if list == 0 {
			return
		}
		defer objcSend(list, "release")
		for i, n := 0, int(objcSend(list, "count")); i < n; i++ {
			d := objcSend(list, "objectAtIndex:", i)
			owner := int(objcSend(objcSend(d, "objectForKey:", objcStr("kCGWindowOwnerPID")), "intValue"))
			id := int(objcSend(objcSend(d, "objectForKey:", objcStr("kCGWindowNumber")), "intValue"))
			if id == num {
				pid = owner
			}
		}
	})
	return pid
}

func TestCollapsedMarginsReachUnderlyingWindow(t *testing.T) {
	a := redesignShelf(t)
	oldCursor := cursorPoint
	t.Cleanup(func() { onMain(func() { cursorPoint = oldCursor }); motionClosed(a) })
	for _, state := range []string{"peek", "capsule"} {
		motionClosed(a)
		time.Sleep(600 * time.Millisecond)
		onMain(func() {
			a.dragging, a.pointerDrag = false, false
			if state == "peek" {
				a.setPeek(true)
			} else {
				a.toastStatus("已复制", time.Now().Add(8*time.Second), statusOK)
				a.peekProgress = 0.5 // hover-out tail can coexist with the capsule
			}
			a.win.Invalidate()
		})
		time.Sleep(650 * time.Millisecond)
		// The pointer may sit at the notch from an earlier test; another
		// copy of ShelfU (the installed one) opens its panel on hover and
		// would be the window at the point. Park the pointer away, let any
		// hover panel retract, then place the receiver.
		d := mygo.Screen.PrimaryDisplay()
		warpPointerTo(t, float64(d.WorkArea.X+d.WorkArea.Width/2), float64(d.WorkArea.Y+d.WorkArea.Height*3/4))
		time.Sleep(450 * time.Millisecond)
		var receiver *mygo.Window
		var point mygo.Point
		var receiverNum, hit, received int
		var stop func()
		var ignoring bool
		onMain(func() {
			frame := a.win.Bounds()
			point = mygo.Point{X: frame.X + 2, Y: a.y + a.notchH + 12}
			cursorPoint = func() mygo.Point { return point }
			receiver = mygo.NewWindow(mygo.WindowOptions{Title: "ShelfU click receiver", Width: 40, Height: 40, Hidden: true, Frameless: true, DisableShadow: true, Panel: true, SkipTaskbar: true, Content: ui.View(func(c *ui.Context) { ui.Text(c, "Receiver") })})
			h := receiver.NativeHandle()
			mac.SetWindowFrame(h, point.X-10, point.Y-10, 40, 40)
			receiver.ShowInactive()
			mac.SetWindowLevel(h, mac.StatusWindowLevel-1)
			receiverNum = mac.WindowNumber(h)
			stop = mac.ObserveMouseDown(h, func(inside bool, button int) {
				if inside {
					received++
				}
			})
			a.syncMousePassThrough()
			ignoring = a.ignoringMouse
		})
		t.Cleanup(func() { onMain(func() { stop(); receiver.Close() }) })
		time.Sleep(150 * time.Millisecond) // let WindowServer apply ordering and ignoresMouseEvents
		onMain(func() {
			mac.SetWindowFrame(receiver.NativeHandle(), point.X-10, point.Y-10, 40, 40)
			hit = mac.WindowAtPoint(float64(point.X), float64(point.Y), float64(a.dispBounds.Y+a.dispBounds.Height))
		})
		if !ignoring || hit != receiverNum {
			t.Fatalf("%s: margin ignores=%v native target=%d want receiver=%d", state, ignoring, hit, receiverNum)
		}
		if mac.AccessibilityTrusted() {
			// Another copy of ShelfU (the installed one) opens its own
			// notch panel on hover and can sit above the receiver between
			// the check above and the click: this pass-through test can
			// only mean something when the point belongs to our receiver.
			// Park the pointer on the point first and let it settle: the
			// window at a point is only meaningful once nothing is about
			// to move there, and another copy of ShelfU (the installed
			// one) may open its own panel at the notch. Only a window owned
			// by another process may take the point; this process taking it
			// means our own margin stopped letting clicks through, which is
			// the regression this test exists to catch, so that fails.
			warpPointerTo(t, float64(point.X), float64(point.Y))
			time.Sleep(350 * time.Millisecond)
			var now int
			onMain(func() { now = mac.WindowAtPoint(float64(point.X), float64(point.Y), float64(a.dispBounds.Y+a.dispBounds.Height)) })
			if now != receiverNum {
				owner := windowOwnerPID(t, now)
				if owner > 0 && owner != os.Getpid() {
					t.Skipf("%s: window %d (pid %d) took the point, not the test's receiver %d",
						state, now, owner, receiverNum)
				}
				t.Fatalf("%s: window %d (owner pid %d) took the point, not the test's receiver %d; the owner is this process or unidentifiable",
					state, now, owner, receiverNum)
			}
			clickAt(t, float64(point.X), float64(point.Y))
			var count int
			onMain(func() { count = received })
			if count == 0 {
				t.Fatalf("%s: underlying window did not receive the click", state)
			}
			t.Logf("%s: native target=%d, underlying mouse-downs=%d", state, hit, count)
		} else {
			t.Logf("%s: native target=%d; injected click not exercised without Accessibility", state, hit)
		}
	}
}
