//go:build darwin

// Package mac holds the AppKit/CoreGraphics calls ShelfU needs and the mygo
// public API does not expose: pasteboard change counters, the system drag
// session probe, the pressed-button state, per-file icons and the Mission
// Control drag guard.
//
// Everything is driven through the Objective-C runtime and CoreGraphics with
// purego, the same convention as mygo's internal/darwin (module-private, so
// it cannot be imported and is reimplemented here). No goroutines are
// installed. Mouse-only observation lives in clicks_darwin.go and requests no
// Accessibility or Input Monitoring permission; the only event tap is the
// optional drag guard in drag_guard.go, which macOS hands out solely to a
// process trusted for Accessibility and which stays inactive otherwise. Call
// Init once on the main thread; every exported function must be called on
// the main thread (the parent's event loop already is), except the image,
// PDF and text-recognition functions of convert_darwin.go, which are safe
// on any goroutine.
package mac

import (
	"errors"
	"fmt"
	"log"
	"math"
	"runtime"
	"slices"
	"structs"
	"sync"
	"unsafe"

	"github.com/ebitengine/purego"
	"github.com/ebitengine/purego/objc"
)

// id is an Objective-C object pointer, as in internal/darwin/objc.go.
type id = objc.ID

// NSSize mirrors the 64-bit CoreGraphics size.
type NSSize struct{ Width, Height float64 }

// NSPoint mirrors the 64-bit CoreGraphics point.
type NSPoint struct{ X, Y float64 }

// NSRect mirrors the 64-bit CoreGraphics rect.
type NSRect struct {
	Origin NSPoint
	Size   NSSize
}

// NSEdgeInsets mirrors the AppKit edge insets struct returned by
// NSScreen.safeAreaInsets.
type NSEdgeInsets struct{ Top, Left, Bottom, Right float64 }

// NSRange mirrors Foundation's NSRange (UTF-16 location/length).
type NSRange struct {
	_                structs.HostLayout
	Location, Length uint
}

var (
	libObjC, libAppKit, libCG uintptr
	msgSendAddr               uintptr
	dragBoardName             id

	// Typed variants for arguments/returns that travel in floating-point
	// registers (RegisterFunc, not raw SyscallN).
	msgSetSize func(obj id, sel objc.SEL, s NSSize)

	// msgSetRectBool sends setFrame:display: — an NSRect plus a BOOL.
	msgSetRectBool func(obj id, sel objc.SEL, r NSRect, display bool)

	// msgSetPoint sends setFrameTopLeftPoint: so a clamped setFrame can
	// still be pinned to the screen's top edge.
	msgSetPoint func(obj id, sel objc.SEL, p NSPoint)

	// class_replaceMethod installs a method on a class; class_getName names
	// a class for test logging. safeAreaInsets reads NSScreen's notch
	// insets (the struct return needs RegisterFunc, not raw SyscallN).
	classReplaceMethod           func(cls uintptr, sel objc.SEL, imp uintptr, types string) uintptr
	classGetMethodImplementation func(cls uintptr, sel objc.SEL) uintptr
	classGetSuperclass           func(cls uintptr) uintptr
	classGetName                 func(cls id) unsafe.Pointer
	setAssocObject               func(obj id, key unsafe.Pointer, value id, policy uintptr)
	getAssocObject               func(obj id, key unsafe.Pointer) id
	msgSafeAreaInsets            func(id, objc.SEL) NSEdgeInsets

	// msgRect reads NSRect-returning selectors (frame, auxiliaryTopLeftArea,
	// auxiliaryTopRightArea); the struct return needs RegisterFunc too.
	msgRect func(id, objc.SEL) NSRect

	// msgInitRect allocates an object with an NSRect (initWithFrame:);
	// msgSetFloat sends a double argument (a layer's cornerRadius).
	msgInitRect func(obj id, sel objc.SEL, r NSRect) id
	msgSetRect  func(obj id, sel objc.SEL, r NSRect) id
	msgSetFloat func(obj id, sel objc.SEL, v float64) id

	// msgFirstRectForRange calls a text input client's
	// firstRectForCharacterRange:actualRange: — the screen rect IME
	// candidate panels anchor to.
	msgFirstRectForRange func(obj id, sel objc.SEL, r NSRange, actual *NSRange) NSRect

	// unconstrainOnce builds the constrainFrameRect:toScreen: override IMP
	// once. The method still sits on the shared window class, but it
	// answers only for windows carrying the assocUnconstrain mark; every
	// other window forwards to the saved original IMP (classOrigs, found
	// by walking the instance's own class chain), so the patch is
	// window-specific in effect and dies with the window.
	unconstrainOnce   sync.Once
	unconstrainIMP    uintptr
	origRectScreenFns = map[uintptr]func(obj id, sel objc.SEL, x, y, w, h float64, screen id) NSRect{}

	// CGEventSourceButtonState reports hardware button state; reading it
	// requires no Input Monitoring permission (only *posting* events does).
	cgEventSourceButtonState func(state int32, button uint32) bool

	loadOnce sync.Once
	loadErr  error

	// keyGateOnce builds the canBecomeKeyWindow override IMP once. The
	// method sits on the shared panel class, but answers only for windows
	// carrying the assocNoKey mark (desktop pins); every other window
	// forwards to the saved original IMP found in classOrigs, so the
	// patch is pin-specific in effect and dies with the window.
	keyGateOnce sync.Once
	keyGateIMP  uintptr
	origBoolFns = map[uintptr]func(obj id, sel objc.SEL) bool{}

	// assocNoKey is the associated-object key marking pin windows whose
	// class-level canBecomeKeyWindow patch answers NO. Same ownership
	// model as assocUnconstrain in overlay_hit.go.
	assocNoKey byte
)

const (
	// kCGEventSourceStateCombinedSessionState merges every event source in
	// the login session: the state seen by user-driven devices.
	kCGEventSourceStateCombinedSessionState = 0
	kCGMouseButtonLeft                      = 0

	nsPNGFileType = 4 // NSPNGFileType

	// Borderless + nonactivating: NSWindowStyleMaskNonactivatingPanel is
	// 1<<7. Combined with mask 0 this is an NSPanel that has no title bar
	// for AppKit to keep below the menu bar.
	nsWindowStyleMaskNonactivatingPanel = 1 << 7
	// nsStatusWindowLevel is the overlay's resting level; the exported
	// names live in overlay_hit.go next to the other band levels.
	nsStatusWindowLevel = StatusWindowLevel

	collectionCanJoinAllSpaces    = 1 << 0
	collectionTransient           = 1 << 3
	collectionStationary          = 1 << 4
	collectionIgnoresCycle        = 1 << 6
	collectionFullScreenAuxiliary = 1 << 8
	// AppKit allows at most one of Managed, Transient and Stationary. The
	// overlay must be Stationary: that is what keeps it out of the Exposé
	// / Show Desktop sweep. Adding Transient ("hidden by exposé", the
	// default implied by a non-normal window level) alongside it made
	// WindowServer sweep the overlay to the bottom edge on Show Desktop
	// even though the documented Stationary bit was set.
	notchCollectionBehavior = collectionCanJoinAllSpaces | collectionStationary | collectionIgnoresCycle | collectionFullScreenAuxiliary
)

// load opens the frameworks once, mirroring internal/darwin's load(); it
// needs only libobjc, AppKit and CoreGraphics.
func load() {
	loadOnce.Do(func() {
		defer func() {
			if r := recover(); r != nil {
				loadErr = fmt.Errorf("mac: framework setup failed: %v", r)
			}
		}()
		var err error
		dlopen := func(path string) uintptr {
			h, e := purego.Dlopen(path, purego.RTLD_GLOBAL|purego.RTLD_NOW)
			if e != nil {
				err = fmt.Errorf("mac: cannot load %s: %w", path, e)
				return 0
			}
			return h
		}
		libObjC = dlopen("/usr/lib/libobjc.A.dylib")
		libAppKit = dlopen("/System/Library/Frameworks/AppKit.framework/AppKit")
		libCG = dlopen("/System/Library/Frameworks/CoreGraphics.framework/CoreGraphics")
		if err != nil {
			loadErr = err
			return
		}
		msgSendAddr, err = purego.Dlsym(libObjC, "objc_msgSend")
		if err != nil {
			loadErr = fmt.Errorf("mac: %w", err)
			return
		}
		address, err := purego.Dlsym(libAppKit, "NSPasteboardNameDrag")
		if err != nil {
			loadErr = fmt.Errorf("mac: drag pasteboard constant: %w", err)
			return
		}
		// Keep the foreign address at the FFI boundary; copy its NSString
		// pointer into Go storage without treating native memory as a Go pointer.
		var copySymbol func(dst *id, src uintptr, size uintptr) uintptr
		purego.RegisterLibFunc(&copySymbol, purego.RTLD_DEFAULT, "memcpy")
		copySymbol(&dragBoardName, address, unsafe.Sizeof(dragBoardName))
		purego.RegisterFunc(&msgSetSize, msgSendAddr)
		purego.RegisterFunc(&msgSetRectBool, msgSendAddr)
		purego.RegisterFunc(&msgSetPoint, msgSendAddr)
		purego.RegisterFunc(&msgFirstRectForRange, msgSendAddr)
		purego.RegisterFunc(&msgInitRect, msgSendAddr)
		purego.RegisterFunc(&msgSetRect, msgSendAddr)
		purego.RegisterFunc(&msgSetFloat, msgSendAddr)
		purego.RegisterLibFunc(&cgEventSourceButtonState, libCG, "CGEventSourceButtonState")
		purego.RegisterLibFunc(&classReplaceMethod, libObjC, "class_replaceMethod")
		purego.RegisterLibFunc(&classGetMethodImplementation, libObjC, "class_getMethodImplementation")
		purego.RegisterLibFunc(&classGetName, libObjC, "class_getName")
		purego.RegisterLibFunc(&classGetSuperclass, libObjC, "class_getSuperclass")
		purego.RegisterLibFunc(&setAssocObject, libObjC, "objc_setAssociatedObject")
		purego.RegisterLibFunc(&getAssocObject, libObjC, "objc_getAssociatedObject")
		purego.RegisterFunc(&msgSafeAreaInsets, msgSendAddr)
		purego.RegisterFunc(&msgRect, msgSendAddr)
	})
}

var (
	selMu    sync.RWMutex
	selCache = map[string]objc.SEL{}
	clsCache = map[string]id{}
)

// sel returns the selector for name, cached (internal/darwin convention).
func sel(name string) objc.SEL {
	selMu.RLock()
	s, ok := selCache[name]
	selMu.RUnlock()
	if ok {
		return s
	}
	s = objc.RegisterName(name)
	selMu.Lock()
	selCache[name] = s
	selMu.Unlock()
	return s
}

// class returns the class object for name, cached. It panics for classes
// that do not exist since that is a programming error.
func class(name string) id {
	selMu.RLock()
	c, ok := clsCache[name]
	selMu.RUnlock()
	if ok {
		return c
	}
	c = id(objc.GetClass(name))
	if c == 0 {
		panic("mac: Objective-C class " + name + " not found")
	}
	selMu.Lock()
	clsCache[name] = c
	selMu.Unlock()
	return c
}

// send sends a message whose arguments are integers or object pointers, as
// send() in internal/darwin/objc.go. It lazily loads the frameworks.
//
//go:uintptrescapes
func send(obj id, selector string, args ...uintptr) id {
	load()
	var a [12]uintptr
	a[0], a[1] = uintptr(obj), uintptr(sel(selector))
	n := copy(a[2:], args)
	r, _, _ := purego.SyscallN(msgSendAddr, a[:n+2]...)
	return id(r)
}

//go:uintptrescapes
func sendInt(obj id, selector string, args ...uintptr) int {
	return int(send(obj, selector, args...))
}

//go:uintptrescapes
func sendInt64(obj id, selector string, args ...uintptr) int64 {
	return int64(send(obj, selector, args...))
}

const nsUTF8StringEncoding = 4

// nsString returns an autoreleased NSString.
func nsString(s string) id {
	b := unsafe.StringData(s)
	str := send(send(class("NSString"), "alloc"), "initWithBytes:length:encoding:",
		uintptr(unsafe.Pointer(b)), uintptr(len(s)), nsUTF8StringEncoding)
	runtime.KeepAlive(s)
	return send(str, "autorelease")
}

// goString copies an NSString into a Go string.
func goString(str id) string {
	if str == 0 {
		return ""
	}
	p := send(str, "UTF8String")
	if p == 0 {
		return ""
	}
	n := sendInt(str, "lengthOfBytesUsingEncoding:", nsUTF8StringEncoding)
	return string(unsafe.Slice(*(**byte)(unsafe.Pointer(&p)), n))
}

// goBytes copies an NSData.
func goBytes(data id) []byte {
	if data == 0 {
		return nil
	}
	n := sendInt(data, "length")
	if n == 0 {
		return []byte{}
	}
	p := send(data, "bytes")
	return append([]byte(nil), unsafe.Slice(*(**byte)(unsafe.Pointer(&p)), n)...)
}

// callOrigBool forwards a (self, sel) -> BOOL call — the
// canBecomeKeyWindow signature — to the saved original IMP. A missing
// original returns true, matching NSPanel's stock answer so an
// unexpected window keeps the behavior it already had.
func callOrigBool(self id, cmd objc.SEL, selName string) bool {
	orig := origIMP(self, selName)
	if orig == 0 {
		return true
	}
	fn, ok := origBoolFns[orig]
	if !ok {
		purego.RegisterFunc(&fn, orig)
		origBoolFns[orig] = fn
	}
	return fn(self, cmd)
}

var (
	poolPushFn, poolPopFn uintptr
	poolOnce              sync.Once
)

// withPool runs fn inside an autorelease pool on the calling thread, as
// internal/darwin does: the pool belongs to the thread that pushed it.
func withPool(fn func()) {
	_ = Init()
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	pool, _, _ := purego.SyscallN(poolPushFn)
	defer purego.SyscallN(poolPopFn, pool)
	fn()
}

// Init opens the Objective-C runtime and the frameworks this package uses.
// Call it once on the main thread before the other functions; they call it
// lazily themselves too, so calling Init first only surfaces the error
// early. Safe from any goroutine: the conversion and thumbnail workers
// reach it through withPool. It installs no monitors, event taps or
// observers and requests no permissions.
func Init() error {
	load()
	poolOnce.Do(func() {
		if loadErr == nil {
			poolPushFn, _ = purego.Dlsym(libObjC, "objc_autoreleasePoolPush")
			poolPopFn, _ = purego.Dlsym(libObjC, "objc_autoreleasePoolPop")
			if poolPushFn == 0 || poolPopFn == 0 {
				loadErr = errors.New("mac: autorelease pool entry points missing")
			}
		}
	})
	return loadErr
}

// generalPasteboard returns the shared NSPasteboard (NSGeneralPboard).
func generalPasteboard() id { return send(class("NSPasteboard"), "generalPasteboard") }

// dragPasteboard returns the pasteboard that backs drag sessions
// (NSDragPboard). The object persists across sessions; only its contents
// change, so changeCount is the serial that marks drag activity.
func dragPasteboard() id {
	return send(class("NSPasteboard"), "pasteboardWithName:", uintptr(dragBoardName))
}

// ClipboardChangeCount returns the general pasteboard's changeCount: it
// increments for every clipboard mutation system-wide. Poll it (~0.5 s)
// instead of keeping clipboard text in memory; when it differs from
// the last seen value, read mygo.Clipboard for the new item. Safe while
// the app is inactive; main thread only.
func ClipboardChangeCount() int64 {
	var n int64
	withPool(func() { n = sendInt64(generalPasteboard(), "changeCount") })
	return n
}

// ClipboardTypes returns the general pasteboard's declared type
// identifiers in the source's order of preference (most faithful first).
// It reads metadata only, never the clipboard contents. Main thread only.
func ClipboardTypes() []string {
	var out []string
	withPool(func() { out = pasteboardTypes(generalPasteboard()) })
	return out
}

// pasteboardTypes lists board's declared types; call inside withPool.
func pasteboardTypes(board id) []string {
	types := send(board, "types")
	n := sendInt(types, "count")
	out := make([]string, 0, n)
	for i := range n {
		out = append(out, goString(send(types, "objectAtIndex:", uintptr(i))))
	}
	return out
}

// LeftMouseDown reports whether the left (primary) mouse button is
// physically held right now, read through CGEventSourceButtonState on the
// combined-session state — the same source the window server consults, so
// it stays true during a drag into or out of another app. This is state,
// not an event tap: no permission prompt. Main thread only.
func LeftMouseDown() bool {
	load()
	return cgEventSourceButtonState(kCGEventSourceStateCombinedSessionState, kCGMouseButtonLeft)
}

// dragTypeKnown lists types a Finder/NSDraggingSession item exposes on the
// drag pasteboard. It is a confirmation signal for a freshly bumped board
// (a count bump with no declared type means the board was cleared, not a
// drag), NOT an activity signal by itself: types stay declared after the
// drop, so testing them alone makes an ordinary click after any past drag
// look like a new drag.
var dragTypeKnown = []string{
	"NSFilenamesPboardType",
	"public.file-url",
	"public.url",
	"public.utf8-plain-text",
	"public.utf16-plain-text",
	"public.tiff",
	"public.png",
	"public.data",
	"public.content",
	"com.apple.NSFilePromiseItemMetaData",
	"com.apple.pasteboard.promised-file-url",
}

// DragTypes returns the drag pasteboard's declared type identifiers right
// now. Like pasteboardTypes it reads metadata only; the caller gates on
// DragState so the stale types a finished session leaves behind are never
// mistaken for a live drag. Main thread only.
func DragTypes() []string {
	var out []string
	withPool(func() { out = pasteboardTypes(dragPasteboard()) })
	return out
}

// dragPasteboardHasTypes reports whether the drag pasteboard declares at
// least one known public type right now. Stale after a drop; only
// meaningful at the moment a changeCount bump is observed.
func dragPasteboardHasTypes(board id) bool {
	for _, name := range pasteboardTypes(board) {
		if slices.Contains(dragTypeKnown, name) {
			return true
		}
	}
	return false
}

// dragTracker is the state machine behind DragState, kept pure (no ObjC)
// so its boundary behavior is unit-testable: step takes the observed drag
// pasteboard changeCount, whether the board declared items at that count,
// and whether the left button is held.
type dragTracker struct {
	primed  bool  // the first sample only baselines, never reports a drag
	base    int64 // changeCount attributed to the current state
	serial  int64
	active  bool
	wasDown bool
	pressed bool // a left press event arrived since the last sample
}

// press records a real left-button-down event (from ObserveMouseDown).
// The 30 ms poll can miss a release between holds — a fast drop and
// re-grab, or a tap-drag the event source reports late — leaving wasDown
// stuck true and folding a brand-new drag's board write into the previous
// session's serial. The press event is the edge the poll missed.
func (t *dragTracker) press() {
	t.pressed = true
}

// step folds one observation into the tracker.
//
// Semantics, matching the DragState doc comment:
//   - first sample only establishes a baseline (stale board contents from
//     before the app started are never reported as a drag);
//   - button release ends the hold and re-baselines the board, so the next
//     press needs a *new* count to arm;
//   - on a fresh press — the button newly observed down, or a real press
//     event that filled a missed-release gap — a board change that arrived
//     since the previous sample arms immediately IF the board declares
//     items — a typed bump only ever comes from a drag session declaring
//     items, and a press that coincides with one is the drag's own start;
//   - while a hold is already active, any count change is consumed into
//     the same serial (session rewrites do not look like a new drag);
//   - during a hold that is not yet a drag, the first count change arms
//     exactly one serial per hold — but only if the board declares items
//     (typed); a bump on an empty board is a clear, not a drag.
func (t *dragTracker) step(count int64, typed, down bool) {
	pressed := t.pressed
	t.pressed = false
	if !t.primed {
		t.primed = true
		t.base = count
		t.wasDown = down
		return
	}
	if !down {
		t.active = false
		t.wasDown = false
		t.base = count
		return
	}
	if !t.wasDown || pressed {
		t.wasDown = true
		if count != t.base && typed {
			// Drag began inside the same poll window as the press.
			t.serial++
			t.active = true
		} else {
			t.active = false
		}
		t.base = count
		return
	}
	if t.active {
		if count != t.base {
			t.base = count // consume session rewrites into the same drag
		}
		return
	}
	if count != t.base && typed {
		t.serial++
		t.active = true
	}
	t.base = count
}

// tracker is the live DragState state, main thread only.
var tracker dragTracker

// DragState snapshots whether a system drag session is in flight.
//
// Detection: NSDraggingSession's pasteboard is the shared NSDragPboard.
// Its changeCount increments when a dragging source declares items — i.e.
// when a drag starts — and again if the source rewrites promised data
// mid-drag. A drag also requires the button held, so
//
//	active = a changeCount arrived *while the left button was down*
//	         AND the board then carried a known drag type,
//
// and stays active until the button comes up. serial increments once per
// detected drag generation (per button hold), so the parent can tell
// "the same drag I've been tracking" from "a new drag began" without a
// global monitor.
//
// Stale-board caveat: NSDragPboard keeps the last session's declared types
// after the drop ends. That is why changeCount, not types, drives the
// state machine, why a fresh press re-baselines the board, and why the
// first sample after Init only primes: an ordinary click — or the very
// first observation — cannot resurrect the board's previous drag.
//
// Accepted limitations:
//   - up to one poll interval of latency (parent polls ~30 ms);
//   - a drag in flight before the first sample is invisible until its
//     source next writes the board (usually never) — the alternative,
//     treating a held button over a stale board as a drag, false-fires on
//     every click-hold after any drag;
//   - a source that never writes NSDragPboard (custom private drags) is
//     invisible;
//   - our own outbound drags (mygo StartDataDrag) count as drags too —
//     the parent should suppress on its own session instead.
//
// Reading the metadata is legal while inactive and triggers no prompt.
// Main thread only.
func DragState() (active bool, serial int64) {
	withPool(func() {
		board := dragPasteboard()
		count := sendInt64(board, "changeCount")
		down := LeftMouseDown()
		typed := count != tracker.base && dragPasteboardHasTypes(board)
		tracker.step(count, typed, down)
	})
	return tracker.active, tracker.serial
}

// NoteLeftPress tells the tracker a real left-button press event arrived:
// the shelf's ObserveMouseDown monitor sees presses even when the 30 ms
// DragState poll misses the release between two holds, and a hold that
// never looked released must split at the press. Main thread only.
func NoteLeftPress() {
	tracker.press()
}

// CollectionBehavior reads a window's NSWindowCollectionBehavior. Used
// only to log the value mygo sets after SetVisibleOnAllWorkspaces.
func CollectionBehavior(window uintptr) uint {
	var out uint
	withPool(func() {
		out = uint(sendInt(id(window), "collectionBehavior"))
	})
	return out
}

// ConfigureNotchWindow turns the shelf into a borderless status-level overlay
// that can occupy the hardware-notch strip at screen Y=0.
//
// mygo creates even a "frameless" window with NSWindowStyleMaskTitled plus a
// hidden title bar. AppKit's default constrainFrameRect:toScreen: then keeps
// that title bar below the menu bar, so the overlay sits ~24–38 pt down: the
// peek pill floats in the wallpaper instead of growing out of the camera
// housing, and the real top edge is still a Mission Control / Spaces trigger.
// Stripping the style mask to a nonactivating panel, pinning the window at
// NSStatusWindowLevel, marking it stationary (so Mission Control does not
// suck it into the Spaces UI), and returning constrainFrameRect unchanged
// are what actually put a drop target on the top edge.
func ConfigureNotchWindow(window uintptr) {
	if window == 0 {
		return
	}
	if err := Init(); err != nil {
		return
	}
	withPool(func() {
		w := id(window)
		unconstrainOnce.Do(func() {
			// NSRect is an ARM64 HFA of four doubles (d0–d3), not a
			// stacked struct. Spell the arguments as floats so the
			// callback matches the C ABI; the ObjC type string still
			// describes constrainFrameRect:toScreen:.
			unconstrainIMP = purego.NewCallback(func(self id, cmd objc.SEL, fx, fy, fw, fh float64, screen id) NSRect {
				if !hasAssoc(self, &assocUnconstrain) {
					// A sibling window of the shared class: forward to
					// the IMP its class resolved before the patch.
					return callOrigRectScreen(self, cmd, fx, fy, fw, fh, screen)
				}
				return NSRect{Origin: NSPoint{X: fx, Y: fy}, Size: NSSize{Width: fw, Height: fh}}
			})
		})
		// The override sits on the shared window class but answers only
		// for windows carrying the mark: every other mygo window (Settings
		// included) forwards to the saved original IMP and keeps AppKit's
		// menu-bar constraint. The mark dies with the window, so a
		// recycled address never inherits it.
		classPatch(id(send(w, "class")), "constrainFrameRect:toScreen:", unconstrainIMP,
			"{CGRect={CGPoint=dd}{CGSize=dd}}@:{CGRect={CGPoint=dd}{CGSize=dd}}@")
		markAssoc(w, &assocUnconstrain)
		send(w, "setStyleMask:", nsWindowStyleMaskNonactivatingPanel)
		send(w, "setLevel:", nsStatusWindowLevel)
		send(w, "setHasShadow:", 0)
		send(w, "setHidesOnDeactivate:", 0)
		send(w, "setMovable:", 0)
		send(w, "setOpaque:", 0)
		send(w, "setBackgroundColor:", uintptr(send(class("NSColor"), "clearColor")))
		send(w, "setCollectionBehavior:", notchCollectionBehavior)
		if send(w, "respondsToSelector:", uintptr(sel("setFloatingPanel:"))) != 0 {
			send(w, "setFloatingPanel:", 1)
		}
		send(w, "setIgnoresMouseEvents:", 0)
		registerOverlayDropTypes(w)
		// The panel keeps the arrow while the pointer is on it even
		// though it is never the active app: the window underneath must
		// not leave its own I-beam or zoom cursor behind.
		if err := installPanelCursor(w); err != nil {
			log.Printf("面板指针: %v", err)
		}
	})
}

// ConfigurePinWindow turns a window into a desktop pin: a borderless
// non-activating panel at the floating level (NSFloatingWindowLevel) so it
// rides above normal windows but stays below the menu bar and the notch
// overlay. Unlike ConfigureNotchWindow it keeps the menu-bar constraint —
// pins live in the work area, never on the top strip — and registers the
// same drop types so files land on the card. It also refuses key status:
// canBecomeKeyWindow answers NO for this window only (same per-instance
// graft as constrainFrameRect:), so a click on the card can never pull
// keyboard focus away from the app the user was typing in. Main thread
// only.
func ConfigurePinWindow(window uintptr) {
	if window == 0 {
		return
	}
	if err := Init(); err != nil {
		return
	}
	withPool(func() {
		w := id(window)
		send(w, "setStyleMask:", nsWindowStyleMaskNonactivatingPanel)
		send(w, "setLevel:", uintptr(3))    // NSFloatingWindowLevel
		send(w, "setAnimationBehavior:", 0) // NSWindowAnimationBehaviorDefault
		send(w, "setHasShadow:", 1)         // the card's drop shadow, hugging its opaque pixels
		send(w, "setHidesOnDeactivate:", 0)
		send(w, "setMovable:", 0)
		send(w, "setOpaque:", 0)
		send(w, "setBackgroundColor:", uintptr(send(class("NSColor"), "clearColor")))
		send(w, "setCollectionBehavior:", notchCollectionBehavior)
		if send(w, "respondsToSelector:", uintptr(sel("setFloatingPanel:"))) != 0 {
			send(w, "setFloatingPanel:", 1)
		}
		// The card never takes the keyboard. canBecomeKeyWindow is what
		// AppKit actually consults: mygo's shared panel class answers YES,
		// and setBecomesKeyOnlyIfNeeded: does not stop a click from making
		// the window key, so gate the selector on the assocNoKey mark and
		// forward every sibling window to the saved original IMP.
		keyGateOnce.Do(func() {
			keyGateIMP = purego.NewCallback(func(self id, cmd objc.SEL) bool {
				if !hasAssoc(self, &assocNoKey) {
					return callOrigBool(self, cmd, "canBecomeKeyWindow")
				}
				return false
			})
		})
		classPatch(id(send(w, "class")), "canBecomeKeyWindow", keyGateIMP, "B@:")
		markAssoc(w, &assocNoKey)
		// becomesKeyOnlyIfNeeded is a second refusal for AppKit paths
		// that skip canBecomeKeyWindow; the gate above is authoritative.
		send(w, "setBecomesKeyOnlyIfNeeded:", 1)
		send(w, "setIgnoresMouseEvents:", 0)
		registerOverlayDropTypes(w)
	})
}

// ConfigureDragPreview uses the pin's non-activating appearance, but never
// intercepts the source gesture or animates out over the returning grid copy.
// The animation setting is per-window; it does not change system preferences.
func ConfigureDragPreview(window uintptr, level int) {
	ConfigurePinWindow(window)
	if window == 0 {
		return
	}
	withPool(func() {
		w := id(window)
		send(w, "setAnimationBehavior:", 2) // NSWindowAnimationBehaviorNone
		send(w, "setIgnoresMouseEvents:", 1)
		send(w, "setLevel:", uintptr(level))
	})
}

// SetWindowFrame moves and resizes window in one setFrame:display: and then
// pins the top-left corner. x, y, w and h are MyGo top-left screen
// coordinates (the same convention as mygo Window.SetBounds). Splitting the
// update into setFrameSize + setFrameOrigin leaves a half-moved rectangle
// that can swallow drops meant for the apps underneath. Main thread only.
func SetWindowFrame(window uintptr, x, y, w, h int) {
	if window == 0 {
		return
	}
	if err := Init(); err != nil {
		return
	}
	withPool(func() {
		screenH := primaryScreenHeight()
		r := windowFrameFor(x, y, w, h, screenH)
		msgSetRectBool(id(window), sel("setFrame:display:"), r, true)
		// setFrame still consults constrainFrameRect; pin the top-left in
		// case a titled ancestor shifted the origin down by the menu bar.
		msgSetPoint(id(window), sel("setFrameTopLeftPoint:"), windowTopLeftFor(x, y, screenH))
	})
}

// windowFrameFor converts a MyGo top-left frame (global Y down from the
// primary display's top edge) into the AppKit rect setFrame:display: wants
// (bottom-left origin, Y up). screenH is the primary screen's height; displays
// above the primary produce negative y and therefore origins above screenH,
// displays to its left produce negative x. Pure: kept separate so synthetic
// secondary geometries are unit-testable.
func windowFrameFor(x, y, w, h int, screenH float64) NSRect {
	return NSRect{
		Origin: NSPoint{X: float64(x), Y: screenH - float64(y) - float64(h)},
		Size:   NSSize{Width: float64(w), Height: float64(h)},
	}
}

// windowTopLeftFor converts the same top-left corner for
// setFrameTopLeftPoint:.
func windowTopLeftFor(x, y int, screenH float64) NSPoint {
	return NSPoint{X: float64(x), Y: screenH - float64(y)}
}

// SetWindowIgnoresMouseEvents toggles whether window is transparent to mouse
// clicks and drags (NSWindow.setIgnoresMouseEvents:). When true, clicks pass
// straight through to the menu bar and underlying apps; when false, the
// window intercepts clicks and drag-and-drop sessions at the notch.
//
// Clearing click-through is not just the AppKit flag. If the overlay was
// ordered in while ignoring events (the idle strip), WindowServer keeps it
// out of hit-testing until it is ordered in again — the peek pill then
// paints but swallows no clicks. Nudge the level and re-order so the
// hit-test list actually updates. Main thread only.
func SetWindowIgnoresMouseEvents(window uintptr, ignore bool) {
	if window == 0 {
		return
	}
	if err := Init(); err != nil {
		return
	}
	withPool(func() {
		w := id(window)
		var arg uintptr
		if ignore {
			arg = 1
		}
		send(w, "setIgnoresMouseEvents:", arg)
		if ignore {
			return
		}
		level := sendInt(w, "level")
		send(w, "setLevel:", uintptr(level+1))
		send(w, "setLevel:", uintptr(level))
		send(w, "orderFrontRegardless")
	})
}

// primaryScreenHeight returns the primary screen's frame height, needed to
// flip between AppKit's bottom-left and MyGo's top-left screen origins.
// Mirrors primaryScreenHeight() in mygo's internal/darwin. Main thread only.
func primaryScreenHeight() float64 {
	screens := send(class("NSScreen"), "screens")
	if sendInt(screens, "count") == 0 {
		return 0
	}
	return msgRect(send(screens, "objectAtIndex:", 0), sel("frame")).Size.Height
}

// NotchTopInset returns the top safe-area inset in points (e.g. ~32 on a
// notched MacBook display, 0 on a non-notched display) for the screen
// matching displayID. Main thread only.
func NotchTopInset(displayID int64) int {
	if err := Init(); err != nil {
		return 0
	}
	var top int
	withPool(func() {
		screens := send(class("NSScreen"), "screens")
		n := sendInt(screens, "count")
		key := nsString("NSScreenNumber")
		for i := range n {
			s := send(screens, "objectAtIndex:", uintptr(i))
			num := send(send(s, "deviceDescription"), "objectForKey:", uintptr(key))
			if uint32(send(num, "unsignedIntValue")) == uint32(displayID) {
				insets := msgSafeAreaInsets(s, sel("safeAreaInsets"))
				if insets.Top > 0 {
					top = int(math.Ceil(insets.Top))
				}
				return
			}
		}
	})
	return top
}

// NotchRect returns the top-center notch (or Dynamic Island anchor) rectangle
// in MyGo top-left screen coordinates for the display matching displayID, and
// whether the display has a physical camera housing (safeAreaInsets.top > 0).
// On a non-notched display it returns a centered 180×32 fallback. When no
// screen matches displayID it returns w=0, h=0, hasNotch=false so callers can
// tell "no notch geometry" apart from a real rect at the origin — reporting a
// 180×32 box at (0,0) would invent an interactive zone in the top-left corner
// of the primary screen.
// Main thread only.
func NotchRect(displayID int64) (x, y, w, h int, hasNotch bool) {
	if err := Init(); err != nil {
		return 0, 0, 0, 0, false
	}
	withPool(func() {
		screens := send(class("NSScreen"), "screens")
		n := sendInt(screens, "count")
		if n == 0 {
			return
		}
		primaryH := primaryScreenHeight()
		key := nsString("NSScreenNumber")
		for i := range n {
			s := send(screens, "objectAtIndex:", uintptr(i))
			num := send(send(s, "deviceDescription"), "objectForKey:", uintptr(key))
			if uint32(send(num, "unsignedIntValue")) != uint32(displayID) {
				continue
			}
			// The match gates the 180×32 fallback; when no screen matches the
			// helper's w=h=0 result is returned, so the caller disables notch
			// interaction instead of inventing a hot zone at (0,0).
			frame := msgRect(s, sel("frame"))
			insets := msgSafeAreaInsets(s, sel("safeAreaInsets"))
			leftAux := msgRect(s, sel("auxiliaryTopLeftArea"))
			rightAux := msgRect(s, sel("auxiliaryTopRightArea"))
			x, y, w, h, hasNotch = notchRectFor(frame, insets, leftAux, rightAux, primaryH)
			return
		}
	})
	return x, y, w, h, hasNotch
}

// notchRectFor is the pure geometry behind NotchRect, taking the raw NSScreen
// values so synthetic multi-display layouts can be unit-tested. frame is the
// screen's frame in AppKit global space (origin bottom-left of the primary,
// Y up); primaryH is the primary screen's height used to flip to the global
// top-left convention CGEvent and MyGo share. The auxiliary areas are local
// to the screen (bottom-left origin); only their X extents matter, so the
// flip does not touch them and any secondary frame.Origin.X keeps the
// housing centred under the physical notch.
func notchRectFor(frame NSRect, insets NSEdgeInsets, leftAux, rightAux NSRect, primaryH float64) (x, y, w, h int, hasNotch bool) {
	w, h = 180, 32
	y = int(math.Round(primaryH - frame.Origin.Y - frame.Size.Height))
	if insets.Top > 0 {
		hasNotch = true
		h = int(math.Ceil(insets.Top))
	}
	if leftAux.Size.Width > 0 && rightAux.Size.Width > 0 {
		gap := rightAux.Origin.X - (leftAux.Origin.X + leftAux.Size.Width)
		if gap >= 120 {
			w = max(180, int(math.Round(gap)))
			x = int(math.Round(frame.Origin.X + leftAux.Origin.X + leftAux.Size.Width))
			return x, y, w, h, hasNotch
		}
	}
	x = int(math.Round(frame.Origin.X + (frame.Size.Width-float64(w))/2))
	return x, y, w, h, hasNotch
}

// FileIcon returns the PNG bytes of the Finder icon for path rendered at
// size×size points, at the screen's backing scale. Pipeline:
// NSWorkspace.iconForFile: → NSImage.CGImageForProposedRect → PNG. nil is
// returned when the icon or conversion fails (a nonexistent path still
// yields the generic document icon). Read-only: never writes the file or
// the clipboard, no network.
// Main thread only; cache per path — the parent does.
func FileIcon(path string, size int) []byte {
	return iconPNG(size, func(ws id) id {
		return send(ws, "iconForFile:", uintptr(nsString(path)))
	})
}

// AppIcon returns the PNG bytes of a bundle id's application icon at
// size×size points, or nil when the app cannot be located. It is the
// fallback picture for players that publish metadata without artwork, so
// the now-playing card shows the app instead of a generic music glyph.
// Main thread only; cache per bundle id — the parent does.
func AppIcon(bundle string, size int) []byte {
	if bundle == "" {
		return nil
	}
	return iconPNG(size, func(ws id) id {
		url := send(ws, "URLForApplicationWithBundleIdentifier:", uintptr(nsString(bundle)))
		if url == 0 {
			return 0
		}
		path := send(url, "path")
		if path == 0 {
			return 0
		}
		return send(ws, "iconForFile:", uintptr(path))
	})
}

// iconPNG rasterises iconOf's NSImage to PNG bytes at size×size points.
// Only the one size is drawn: TIFFRepresentation would encode every
// representation up to 1024px, ~160ms per icon on the main thread.
func iconPNG(size int, iconOf func(ws id) id) []byte {
	var out []byte
	withPool(func() {
		ws := send(class("NSWorkspace"), "sharedWorkspace")
		icon := iconOf(ws)
		if icon == 0 {
			return
		}
		// The proposed rect is in points; with no context AppKit renders
		// it at the main screen's backing scale (2× pixels on Retina).
		rect := NSRect{Size: NSSize{Width: float64(size), Height: float64(size)}}
		cg := send(icon, "CGImageForProposedRect:context:hints:", uintptr(unsafe.Pointer(&rect)), 0, 0)
		if cg == 0 {
			return
		}
		rep := send(send(class("NSBitmapImageRep"), "alloc"), "initWithCGImage:", uintptr(cg))
		if rep == 0 {
			return
		}
		defer send(rep, "release")
		png := send(rep, "representationUsingType:properties:", nsPNGFileType, 0)
		out = goBytes(png)
	})
	return out
}
