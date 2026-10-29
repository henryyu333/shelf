//go:build darwin

package main

import (
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/transfer"
	"github.com/egoist/mygo/ui"

	"shelf/internal/mac"
)

// Shake-to-summon basket: while a system file drag is in flight, wiggling
// the pointer left-right summons a small floating card next to the cursor
// that accepts the drop. The shake math lives in shake.go; this file owns
// the window, its placement and its lifetime.

const (
	basketWidth    = 200 // 设计:200×132 圆角 22 纯黑
	basketHeight   = 132
	basketGap      = 18 // 光标与篮子之间留出的安全距离
	basketPad      = 8  // 篮子离可见屏缘的内边距
	basketEdge     = 10 // 卡片四周的透明余量
	basketAfterEnd = 3 * time.Second
	// basketConfirmHold / basketFailHold 是放入成功与失败的展示时长；失败
	// 停留更久，让用户读得清原因。
	basketConfirmHold   = 900 * time.Millisecond
	basketFailHold      = 2500 * time.Millisecond
	basketCloseFallback = 500 * time.Millisecond
)

// basketState is the floating drop card. The window is created lazily on
// the first shake and destroyed on close, so ShelfU without a shake costs
// no extra overlay.
//
// Closing goes through closing + a render, never straight to Destroy from
// the monitor tick: mygo acknowledges a native drop and only hands it to
// ui.DropData on the NEXT render, so a tick that destroyed the window on
// pointer-leave could discard a drop that already happened.
type basketState struct {
	win       *mygo.Window
	shown     bool           // logically open (in tests: without a window)
	frame     mygo.Rectangle // last applied screen rect
	closing   bool           // close requested; the next render destroys it
	closedAt  time.Time      // fallback deadline for a stalled render
	dropped   bool           // intake succeeded; showing the confirmation
	failed    bool           // intake failed; showing the reason
	failMsg   string
	endAt     time.Time // the drag session ended at
	leftSince time.Time // pointer left the card after the drag ended
	hideAt    time.Time // display deadline after a drop (success or failure)
}

// basketFrame picks the card's on-screen rect: beside the cursor, clamped
// inside the display's visible frame so it can never sit under the menu
// bar or off-screen. The card prefers down-right of the cursor, flips to
// the left when there is no room, and above when the bottom edge would
// leave the visible area.
func basketFrame(visible mygo.Rectangle, cursor mygo.Point) mygo.Rectangle {
	x := cursor.X + basketGap
	y := cursor.Y + basketGap
	if x+basketWidth > visible.X+visible.Width-basketPad {
		x = cursor.X - basketGap - basketWidth
	}
	if y+basketHeight > visible.Y+visible.Height-basketPad {
		y = cursor.Y - basketGap - basketHeight
	}
	x = min(max(x, visible.X+basketPad), max(visible.X+basketPad, visible.X+visible.Width-basketPad-basketWidth))
	y = min(max(y, visible.Y+basketPad), max(visible.Y+basketPad, visible.Y+visible.Height-basketPad-basketHeight))
	return mygo.Rectangle{X: x, Y: y, Width: basketWidth, Height: basketHeight}
}

// showBasket summons the card next to the cursor. Repeated calls while it
// is open do nothing. A haptic tick marks the moment it appears, and the
// card rides above other windows like the shelf panel.
func (a *shelf) showBasket(p mygo.Point) {
	if a.basket.win != nil || a.basket.shown || a.quitting {
		return
	}
	d := mygo.Screen.DisplayNearestPoint(p)
	a.basket.frame = basketFrame(d.WorkArea, p)
	a.basket = basketState{shown: true, frame: a.basket.frame}
	w := mygo.NewWindow(mygo.WindowOptions{
		Title: a.appLoc().WindowBasket(), Width: basketWidth, Height: basketHeight,
		X: a.basket.frame.X, Y: a.basket.frame.Y,
		Hidden: true, Frameless: true, Transparent: true, DisableShadow: true,
		AlwaysOnTop: true, SkipTaskbar: true, Panel: true,
		DisableMove: true, DisableResize: true, DisableMinimize: true, DisableMaximize: true, DisableFullScreen: true,
		Content: ui.View(a.basketView),
	})
	a.basket.win = w
	w.SetVisibleOnAllWorkspaces(true)
	mac.ConfigureNotchWindow(w.NativeHandle())
	mac.SetWindowFrame(w.NativeHandle(), a.basket.frame.X, a.basket.frame.Y, a.basket.frame.Width, a.basket.frame.Height)
	w.OnClose(func(e *mygo.CloseEvent) {
		// A system close (Space switch, Exposé tidy-up) dismisses the card.
		a.basket = basketState{}
	})
	w.ShowInactive()
	mac.SetDragElevated(w.NativeHandle(), true)
	mac.HapticTick()
}

// closeBasket tears the card down at once. Idempotent; only reached from
// the render that consumed a pending drop, or the stall fallback.
func (a *shelf) closeBasket() {
	w := a.basket.win
	a.basket = basketState{}
	if w != nil {
		w.Destroy()
	}
}

// requestBasketClose asks for the card to go away. The actual Destroy
// happens in basketView's next frame, after DropData has been consumed, so
// an acknowledged drop cannot be lost when the pointer leaves quickly.
// The monitor's fallback destroys it anyway if renders stall.
func (a *shelf) requestBasketClose(now time.Time) {
	b := &a.basket
	if !b.shown || b.closing {
		return
	}
	b.closing, b.closedAt = true, now
	if b.win != nil {
		b.win.Invalidate()
	}
}

// basketTick drives the card's lifetime from the 30 ms monitor: it closes
// once the drag ends (after a short linger while the pointer still rests
// on it), about a second after the pointer leaves a lingering card, and at
// the display deadline after content landed.
func (a *shelf) basketTick(now time.Time, p mygo.Point) {
	b := &a.basket
	if !b.shown {
		return
	}
	if b.closing {
		// The render that runs the close may itself consume an
		// acknowledged-but-undelivered drop. If it stalls, destroy anyway.
		if now.Sub(b.closedAt) >= basketCloseFallback {
			a.closeBasket()
		}
		return
	}
	if !b.hideAt.IsZero() {
		if !now.Before(b.hideAt) {
			a.closeBasket()
		}
		return
	}
	if a.dragging {
		b.endAt, b.leftSince = time.Time{}, time.Time{}
		return
	}
	if b.endAt.IsZero() {
		b.endAt = now
	}
	if !insideRect(p, b.frame) {
		if !b.dropped && !b.failed {
			a.requestBasketClose(now)
			return
		}
		if b.leftSince.IsZero() {
			b.leftSince = now
		}
		if now.Sub(b.leftSince) >= time.Second {
			a.requestBasketClose(now)
		}
		return
	}
	b.leftSince = time.Time{}
	if !b.dropped && !b.failed && now.Sub(b.endAt) >= basketAfterEnd {
		a.requestBasketClose(now)
	}
}

// basketReceive is the card's drop path: the same intake as the notch,
// then a brief confirmation — or, when intake fails, the failure reason
// held a little longer so it can be read. An incoming drop always cancels
// a pending close: the card is still needed.
func (a *shelf) basketReceive(data transfer.Data) {
	err := a.receive(data)
	b := &a.basket
	b.closing, b.closedAt = false, time.Time{}
	if err != nil {
		b.failed = true
		b.failMsg = err.Error()
		b.hideAt = time.Now().Add(basketFailHold)
	} else {
		b.dropped = true
		b.hideAt = time.Now().Add(basketConfirmHold)
	}
	if b.win != nil {
		b.win.Invalidate()
	}
}

// basketView draws the card: a dark rounded panel with the shelf icon and
// 放到这里, highlighted while a drag is over it, briefly confirming after
// a drop. The outer padding leaves a transparent margin around the card.
//
// DropData is consumed before the pending close: a drop the system
// delivered between the pointer leaving and this frame must still land.
func (a *shelf) basketView(c *ui.Context) {
	c.Root().Background(ui.Transparent)
	opts := transfer.DropOptions{Formats: []transfer.Format{transfer.FileList, transfer.URIList, transfer.PNG, transfer.Text}, Operations: transfer.Copy}
	zone := ui.Column(c.Key("basket")).Fill().Padding(basketEdge).Clip()
	_, over := ui.DataDragOver(zone, opts)
	zone.Children(func() {
		card := ui.Column(c.Key("card")).Fill().Radius(22).Center().Gap(8).Padding(10)
		accent := c.Theme().Accent
		card.Background(surface)
		if a.basket.dropped || over {
			// 拖到上方：2pt accent 描边；空闲无描边。
			card.Border(2, accent)
		}
		card.Children(func() {
			l := loc(c)
			label := l.BasketDropHere()
			switch {
			case a.basket.dropped:
				label = l.BasketSaved()
			case a.basket.failed:
				label = l.BasketFailed()
			}
			iconColor := ink
			if a.basket.failed {
				iconColor = ink3
			}
			ui.Icon(c, shelfSymbol).Size(28, 28).TextColor(iconColor)
			ui.Text(c, label).FontSize(13).FontWeight(600).TextColor(ink)
			switch {
			case a.basket.failed && a.basket.failMsg != "":
				ui.Text(c, a.basket.failMsg).MaxLines(2).TextAlign(ui.Center).FontSize(11).TextColor(ink3)
			case a.basket.failed || a.basket.dropped:
				// 完成/失败态不再显示拖入提示副标题。
			default:
				ui.Text(c, l.BasketReleaseHint()).SingleLine().FontSize(11).TextColor(ink3)
			}
		})
	})
	if drop, ok := ui.DropData(zone, opts); ok {
		a.basketReceive(drop.Data)
		return
	}
	// No drop arrived this frame: honour a pending close now that any
	// acknowledged drop has had its chance to land.
	if a.basket.closing {
		a.closeBasket()
	}
}
