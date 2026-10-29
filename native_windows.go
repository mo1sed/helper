//go:build windows

// Quick paste: Ctrl + right click anywhere opens a menu with the saved messages;
// picking one pastes it into the field under the caret. Plus a tray icon.
package main

import (
	"runtime"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"
	"unsafe"
)

const nativeAvailable = true

var (
	user32   = syscall.NewLazyDLL("user32.dll")
	kernel32 = syscall.NewLazyDLL("kernel32.dll")
	shell32  = syscall.NewLazyDLL("shell32.dll")

	pRegisterClassExW    = user32.NewProc("RegisterClassExW")
	pCreateWindowExW     = user32.NewProc("CreateWindowExW")
	pDefWindowProcW      = user32.NewProc("DefWindowProcW")
	pGetMessageW         = user32.NewProc("GetMessageW")
	pTranslateMessage    = user32.NewProc("TranslateMessage")
	pDispatchMessageW    = user32.NewProc("DispatchMessageW")
	pPostMessageW        = user32.NewProc("PostMessageW")
	pSetWindowsHookExW   = user32.NewProc("SetWindowsHookExW")
	pCallNextHookEx      = user32.NewProc("CallNextHookEx")
	pGetAsyncKeyState    = user32.NewProc("GetAsyncKeyState")
	pCreatePopupMenu     = user32.NewProc("CreatePopupMenu")
	pAppendMenuW         = user32.NewProc("AppendMenuW")
	pTrackPopupMenu      = user32.NewProc("TrackPopupMenu")
	pDestroyMenu         = user32.NewProc("DestroyMenu")
	pGetCursorPos        = user32.NewProc("GetCursorPos")
	pSetForegroundWindow = user32.NewProc("SetForegroundWindow")
	pGetForegroundWindow = user32.NewProc("GetForegroundWindow")
	pOpenClipboard       = user32.NewProc("OpenClipboard")
	pCloseClipboard      = user32.NewProc("CloseClipboard")
	pEmptyClipboard      = user32.NewProc("EmptyClipboard")
	pSetClipboardData    = user32.NewProc("SetClipboardData")
	pGetClipboardData    = user32.NewProc("GetClipboardData")
	pSendInput           = user32.NewProc("SendInput")
	pLoadImageW          = user32.NewProc("LoadImageW")
	pLoadIconW           = user32.NewProc("LoadIconW")
	pGetSystemMetrics    = user32.NewProc("GetSystemMetrics")

	pRegisterHotKey           = user32.NewProc("RegisterHotKey")
	pSystemParametersInfoW    = user32.NewProc("SystemParametersInfoW")
	pRegisterWindowMessageW   = user32.NewProc("RegisterWindowMessageW")
	pUnregisterHotKey         = user32.NewProc("UnregisterHotKey")
	pGetWindowThreadProcessId = user32.NewProc("GetWindowThreadProcessId")
	pAttachThreadInput        = user32.NewProc("AttachThreadInput")

	pGetCurrentThreadId = kernel32.NewProc("GetCurrentThreadId")
	pGetModuleHandleW   = kernel32.NewProc("GetModuleHandleW")
	pGlobalAlloc        = kernel32.NewProc("GlobalAlloc")
	pGlobalLock         = kernel32.NewProc("GlobalLock")
	pGlobalUnlock       = kernel32.NewProc("GlobalUnlock")
	pGlobalSize         = kernel32.NewProc("GlobalSize")
	pRtlMoveMemory      = kernel32.NewProc("RtlMoveMemory")

	pShellNotifyIconW = shell32.NewProc("Shell_NotifyIconW")
)

const (
	wmNull        = 0x0000
	wmLButtonUp   = 0x0202
	wmLButtonDbl  = 0x0203
	wmRButtonDown = 0x0204
	wmRButtonUp   = 0x0205
	wmApp         = 0x8000
	wmShowMenu    = wmApp + 1
	wmTray        = wmApp + 2
	wmRebind      = wmApp + 3
	wmHotkey      = 0x0312

	whMouseLL = 14
	vkControl = 0x11
	vkV       = 0x56

	mfString    = 0x0
	mfGrayed    = 0x1
	mfPopup     = 0x10
	mfSeparator = 0x800

	tpmRightButton = 0x0002
	tpmReturnCmd   = 0x0100
	tpmNoNotify    = 0x0080

	cfUnicodeText = 13
	gmemMoveable  = 0x0002

	nimAdd     = 0
	nimDelete  = 2
	nifMessage = 1
	nifIcon    = 2
	nifTip     = 4

	cmdOpen = 1
	cmdQuit = 2
	cmdBase = 1000
)

type point struct{ X, Y int32 }

type winMsg struct {
	Hwnd    uintptr
	Message uint32
	WParam  uintptr
	LParam  uintptr
	Time    uint32
	Pt      point
	Private uint32
}

type wndClassEx struct {
	Size       uint32
	Style      uint32
	WndProc    uintptr
	ClsExtra   int32
	WndExtra   int32
	Instance   uintptr
	Icon       uintptr
	Cursor     uintptr
	Background uintptr
	MenuName   *uint16
	ClassName  *uint16
	IconSm     uintptr
}

type notifyIconData struct {
	Size            uint32
	Wnd             uintptr
	ID              uint32
	Flags           uint32
	CallbackMessage uint32
	Icon            uintptr
	Tip             [128]uint16
	State           uint32
	StateMask       uint32
	Info            [256]uint16
	Version         uint32
	InfoTitle       [64]uint16
	InfoFlags       uint32
	GuidItem        [16]byte
	BalloonIcon     uintptr
}

type keybdInput struct {
	Vk        uint16
	Scan      uint16
	Flags     uint32
	Time      uint32
	ExtraInfo uintptr
}

type input struct {
	Type uint32
	Ki   keybdInput
	_    [8]byte // INPUT is 40 bytes on 64-bit (the union is sized for MOUSEINPUT)
}

var (
	hwnd           uintptr
	taskbarCreated uintptr
	hInst          uintptr
	nid            notifyIconData
	trayOn         bool
	swallowUp      bool
	menuItems      []msg // command id -> message, rebuilt each time the menu opens
	hookProcCb     = syscall.NewCallback(hookProc)
	wndProcCb      = syscall.NewCallback(wndProc)
)

func u16(s string) *uint16 { p, _ := syscall.UTF16PtrFromString(s); return p }

func nativeStart() {
	ready := make(chan struct{})
	go func() {
		runtime.LockOSThread() // hook, window and message loop live on one thread
		hInst, _, _ = pGetModuleHandleW.Call(0)
		cls := u16("ViceDollsHelperQuickPaste")
		wc := wndClassEx{WndProc: wndProcCb, Instance: hInst, ClassName: cls}
		wc.Size = uint32(unsafe.Sizeof(wc))
		pRegisterClassExW.Call(uintptr(unsafe.Pointer(&wc)))
		hwnd, _, _ = pCreateWindowExW.Call(0, uintptr(unsafe.Pointer(cls)), uintptr(unsafe.Pointer(u16("Vice Dolls Helper"))),
			0, 0, 0, 0, 0, 0, 0, hInst, 0)
		taskbarCreated, _, _ = pRegisterWindowMessageW.Call(uintptr(unsafe.Pointer(u16("TaskbarCreated"))))
		addTray()
		rebindHotkeys()
		// The mouse hook gets its own thread: while a menu is open or something slow runs here,
		// Windows would otherwise see the hook as hung and silently remove it.
		go func() {
			runtime.LockOSThread()
			pSetWindowsHookExW.Call(whMouseLL, hookProcCb, hInst, 0)
			var hm winMsg
			for {
				if r, _, _ := pGetMessageW.Call(uintptr(unsafe.Pointer(&hm)), 0, 0, 0); int32(r) <= 0 {
					return
				}
			}
		}()
		close(ready)
		var m winMsg
		for {
			r, _, _ := pGetMessageW.Call(uintptr(unsafe.Pointer(&m)), 0, 0, 0)
			if int32(r) <= 0 {
				return
			}
			pTranslateMessage.Call(uintptr(unsafe.Pointer(&m)))
			pDispatchMessageW.Call(uintptr(unsafe.Pointer(&m)))
		}
	}()
	select {
	case <-ready:
	case <-time.After(3 * time.Second):
	}
}

func nativeStop() {
	if trayOn {
		pShellNotifyIconW.Call(nimDelete, uintptr(unsafe.Pointer(&nid)))
		trayOn = false
	}
}

func addTray() {
	nid = notifyIconData{Wnd: hwnd, ID: 1, Flags: nifMessage | nifIcon | nifTip, CallbackMessage: wmTray}
	nid.Size = uint32(unsafe.Sizeof(nid))
	sz, _, _ := pGetSystemMetrics.Call(49) // SM_CXSMICON
	nid.Icon, _, _ = pLoadImageW.Call(hInst, 1, 1, sz, sz, 0)
	if nid.Icon == 0 {
		nid.Icon, _, _ = pLoadIconW.Call(0, 32512)
	}
	tip, _ := syscall.UTF16FromString("Vice Dolls Helper — Ctrl + правый клик для вставки")
	copy(nid.Tip[:127], tip)
	r, _, _ := pShellNotifyIconW.Call(nimAdd, uintptr(unsafe.Pointer(&nid)))
	trayOn = r != 0
}

// hookProc sees every mouse event system-wide; it must return fast.
func hookProc(nCode, wParam, lParam uintptr) uintptr {
	if int32(nCode) == 0 && quickPasteCached() {
		switch wParam {
		case wmRButtonDown:
			if st, _, _ := pGetAsyncKeyState.Call(vkControl); st&0x8000 != 0 {
				swallowUp = true
				pPostMessageW.Call(hwnd, wmShowMenu, 0, 0)
				return 1
			}
		case wmRButtonUp:
			if swallowUp {
				swallowUp = false
				return 1
			}
		}
	}
	r, _, _ := pCallNextHookEx.Call(0, nCode, wParam, lParam)
	return r
}

var (
	qpCache   bool
	qpChecked time.Time
)

// quickPasteCached avoids reading the settings file on every mouse event.
func quickPasteCached() bool {
	if time.Since(qpChecked) > 2*time.Second {
		qpCache = quickPasteOn()
		qpChecked = time.Now()
	}
	return qpCache
}

func wndProc(h, m, wParam, lParam uintptr) uintptr {
	switch m {
	case wmShowMenu:
		showPasteMenu("", "")
		return 0
	case wmHotkey:
		hkMu.Lock()
		t, ok := hkTargets[wParam]
		hkMu.Unlock()
		if ok {
			showPasteMenu(t.folder, t.sub)
		}
		return 0
	case wmRebind:
		rebindHotkeys()
		return 0
	case wmTray:
		switch lParam & 0xffff {
		case wmLButtonUp, wmLButtonDbl:
			go openWindow(true)
		case wmRButtonUp:
			showTrayMenu()
		}
		return 0
	}
	if taskbarCreated != 0 && m == taskbarCreated { // Explorer restarted: put the tray icon back
		addTray()
		return 0
	}
	r, _, _ := pDefWindowProcW.Call(h, m, wParam, lParam)
	return r
}

func popup() uintptr { h, _, _ := pCreatePopupMenu.Call(); return h }

func addItem(menu uintptr, flags uintptr, id uintptr, text string) {
	pAppendMenuW.Call(menu, flags, id, uintptr(unsafe.Pointer(u16(text))))
}

func label(t string) string {
	t = strings.TrimSpace(strings.ReplaceAll(strings.ReplaceAll(t, "\r", ""), "\n", " ⏎ "))
	r := []rune(t)
	if len(r) > 60 {
		t = string(r[:60]) + "…"
	}
	return strings.ReplaceAll(t, "&", "&&")
}

// focus brings a window to the front even when Windows' focus lock would refuse it.
func focus(w uintptr) {
	fg, _, _ := pGetForegroundWindow.Call()
	fgThread, _, _ := pGetWindowThreadProcessId.Call(fg, 0)
	me, _, _ := pGetCurrentThreadId.Call()
	if fgThread != 0 && fgThread != me {
		pAttachThreadInput.Call(me, fgThread, 1)
		pSetForegroundWindow.Call(w)
		pAttachThreadInput.Call(me, fgThread, 0)
		return
	}
	pSetForegroundWindow.Call(w)
}

func track(menu uintptr) uintptr {
	var pt point
	pGetCursorPos.Call(uintptr(unsafe.Pointer(&pt)))
	focus(hwnd)
	// Windows waits ~400 ms (or more) before opening a submenu on hover; make it instant
	// while our menu is open, then put the user's setting back.
	var delay uint32
	got, _, _ := pSystemParametersInfoW.Call(0x006A, 0, uintptr(unsafe.Pointer(&delay)), 0) // SPI_GETMENUSHOWDELAY
	if got != 0 {
		pSystemParametersInfoW.Call(0x006B, 0, 0, 0) // SPI_SETMENUSHOWDELAY = 0, this session only
	}
	cmd, _, _ := pTrackPopupMenu.Call(menu, tpmReturnCmd|tpmRightButton|tpmNoNotify, uintptr(pt.X), uintptr(pt.Y), 0, hwnd, 0)
	if got != 0 {
		pSystemParametersInfoW.Call(0x006B, uintptr(delay), 0, 0)
	}
	pPostMessageW.Call(hwnd, wmNull, 0, 0)
	return cmd
}

func showTrayMenu() {
	menu := popup()
	addItem(menu, mfString, cmdOpen, "Открыть Vice Dolls Helper")
	addItem(menu, mfSeparator, 0, "")
	addItem(menu, mfString, cmdQuit, "Выход")
	cmd := track(menu)
	pDestroyMenu.Call(menu)
	switch cmd {
	case cmdOpen:
		go openWindow(true)
	case cmdQuit:
		go quit()
	}
}

// msgsOfSub: messages of a folder that belong to one of its sub-categories
// (unknown/missing sub counts as the first one, like in the app).
func msgsOfSub(f folder, inF []msg, k int) []msg {
	var out []msg
	for _, m := range inF {
		known := false
		for _, x := range f.Subs {
			known = known || x.ID == m.Sub
		}
		if (known && m.Sub == f.Subs[k].ID) || (!known && k == 0) {
			out = append(out, m)
		}
	}
	return out
}

func menuLabel(m msg) string {
	if strings.TrimSpace(m.Label) != "" {
		return label(m.Label)
	}
	return label(m.Text)
}

// showPasteMenu: onlyFolder=="" shows every folder (Ctrl + right click);
// otherwise just that folder, or one sub-category of it (a bound hotkey).
func showPasteMenu(onlyFolder, onlySub string) {
	prev, _, _ := pGetForegroundWindow.Call()
	folders, msgs := readLibrary()
	sort.SliceStable(msgs, func(i, j int) bool {
		if msgs[i].Hot != msgs[j].Hot {
			return msgs[i].Hot // hot messages float to the top
		}
		return msgs[i].sortKey() < msgs[j].sortKey()
	})
	menuItems = menuItems[:0]
	menu := popup()
	fill := func(target uintptr, list []msg) {
		if len(list) == 0 {
			addItem(target, mfGrayed, 0, "(пусто)")
			return
		}
		for _, m := range list {
			menuItems = append(menuItems, m)
			addItem(target, mfString, uintptr(cmdBase+len(menuItems)-1), menuLabel(m))
		}
	}
	// folderInto puts a folder's content into target: sub-category submenus or a flat list.
	folderInto := func(target uintptr, f folder, inF []msg) {
		if len(f.Subs) == 0 {
			fill(target, inF)
			return
		}
		for k, s := range f.Subs {
			sm := popup()
			fill(sm, msgsOfSub(f, inF, k))
			addItem(target, mfPopup, sm, label(s.Name))
		}
	}
	for _, f := range folders {
		if onlyFolder != "" && f.ID != onlyFolder {
			continue
		}
		if onlyFolder == "" && f.QpHidden { // hidden from the full Ctrl menu (still usable in-app and via its own hotkey)
			continue
		}
		var inF []msg
		for _, m := range msgs {
			if m.Folder == f.ID {
				inF = append(inF, m)
			}
		}
		switch {
		case onlyFolder == "":
			fm := popup()
			folderInto(fm, f, inF)
			addItem(menu, mfPopup, fm, label(f.Name))
		case onlySub != "":
			for k, s := range f.Subs {
				if s.ID == onlySub {
					fill(menu, msgsOfSub(f, inF, k))
				}
			}
		default:
			folderInto(menu, f, inF)
		}
	}
	addItem(menu, mfSeparator, 0, "")
	addItem(menu, mfString, cmdOpen, "Открыть Vice Dolls Helper")
	cmd := track(menu)
	pDestroyMenu.Call(menu) // also frees the submenus attached to it

	switch {
	case cmd == cmdOpen:
		go openWindow(true)
	case cmd >= cmdBase && int(cmd-cmdBase) < len(menuItems):
		m := menuItems[cmd-cmdBase]
		focus(prev)
		go func() { // keep the message loop (and the mouse hook) free while pasting
			time.Sleep(120 * time.Millisecond)
			releaseModifiers()
			pasteText(m.Text)
			markUsed(m.ID)
		}()
	}
}

/* ---------- bound hotkeys (RegisterHotKey) ---------- */

type hkTarget struct{ folder, sub string }

var (
	hkMu      sync.Mutex
	hkTargets = map[uintptr]hkTarget{}
	hkState   = map[string]string{} // "Ctrl+Z" -> "ok" | "busy" | "bad"
)

func hotkeyStatus() map[string]string {
	hkMu.Lock()
	defer hkMu.Unlock()
	out := map[string]string{}
	for k, v := range hkState {
		out[k] = v
	}
	return out
}

// nativeRebind asks the window thread (which owns the hotkeys) to re-register them.
func nativeRebind() {
	if hwnd != 0 {
		pPostMessageW.Call(hwnd, wmRebind, 0, 0)
	}
}

// parseHotkey turns "Ctrl+Alt+Z" into RegisterHotKey modifiers and a virtual key.
func parseHotkey(s string) (mods, vk uintptr, ok bool) {
	for _, p := range strings.Split(s, "+") {
		switch p = strings.TrimSpace(p); {
		case p == "Ctrl":
			mods |= 2
		case p == "Alt":
			mods |= 1
		case p == "Shift":
			mods |= 4
		case p == "Win":
			mods |= 8
		case len(p) == 1 && p[0] >= 'A' && p[0] <= 'Z', len(p) == 1 && p[0] >= '0' && p[0] <= '9':
			vk = uintptr(p[0])
		case len(p) >= 2 && p[0] == 'F':
			n := 0
			for _, c := range p[1:] {
				if c < '0' || c > '9' {
					return 0, 0, false
				}
				n = n*10 + int(c-'0')
			}
			if n < 1 || n > 24 {
				return 0, 0, false
			}
			vk = uintptr(0x6F + n)
		default:
			return 0, 0, false
		}
	}
	return mods, vk, vk != 0
}

func rebindHotkeys() {
	hkMu.Lock()
	defer hkMu.Unlock()
	for id := range hkTargets {
		pUnregisterHotKey.Call(hwnd, id)
	}
	hkTargets = map[uintptr]hkTarget{}
	hkState = map[string]string{}
	if !quickPasteOn() {
		return
	}
	folders, _ := readLibrary()
	id := uintptr(1)
	bind := func(key string, t hkTarget) {
		if key == "" {
			return
		}
		if _, dup := hkState[key]; dup {
			return
		}
		mods, vk, ok := parseHotkey(key)
		if !ok {
			hkState[key] = "bad"
			return
		}
		if r, _, _ := pRegisterHotKey.Call(hwnd, id, mods|0x4000, vk); r == 0 { // MOD_NOREPEAT
			hkState[key] = "busy"
			return
		}
		hkState[key] = "ok"
		hkTargets[id] = t
		id++
	}
	for _, f := range folders {
		bind(f.Hotkey, hkTarget{f.ID, ""})
		for _, s := range f.Subs {
			bind(s.Hotkey, hkTarget{f.ID, s.ID})
		}
	}
}

// releaseModifiers lifts Ctrl/Alt/Shift/Win if they are still held from the hotkey,
// so the Ctrl+V we send is not turned into some other shortcut.
func releaseModifiers() {
	var ins []input
	for _, vk := range []uint16{0x10, 0x11, 0x12, 0x5B, 0x5C} {
		if st, _, _ := pGetAsyncKeyState.Call(uintptr(vk)); st&0x8000 != 0 {
			ins = append(ins, key(vk, true))
		}
	}
	if len(ins) > 0 {
		pSendInput.Call(uintptr(len(ins)), uintptr(unsafe.Pointer(&ins[0])), unsafe.Sizeof(ins[0]))
		time.Sleep(30 * time.Millisecond)
	}
}

func readClipboard() (string, bool) {
	h, _, _ := pGetClipboardData.Call(cfUnicodeText)
	if h == 0 {
		return "", false
	}
	p, _, _ := pGlobalLock.Call(h)
	if p == 0 {
		return "", false
	}
	defer pGlobalUnlock.Call(h)
	size, _, _ := pGlobalSize.Call(h)
	buf := make([]uint16, size/2+1)
	if size > 0 {
		pRtlMoveMemory.Call(uintptr(unsafe.Pointer(&buf[0])), p, size)
	}
	return syscall.UTF16ToString(buf), true
}

func openClip() bool {
	for i := 0; i < 10; i++ {
		if r, _, _ := pOpenClipboard.Call(hwnd); r != 0 {
			return true
		}
		time.Sleep(20 * time.Millisecond)
	}
	return false
}

func setClipboard(text string) bool {
	if !openClip() {
		return false
	}
	defer pCloseClipboard.Call()
	pEmptyClipboard.Call()
	text = strings.ReplaceAll(strings.ReplaceAll(text, "\r\n", "\n"), "\n", "\r\n")
	w, _ := syscall.UTF16FromString(text)
	size := uintptr(len(w) * 2)
	h, _, _ := pGlobalAlloc.Call(gmemMoveable, size)
	if h == 0 {
		return false
	}
	p, _, _ := pGlobalLock.Call(h)
	if p == 0 {
		return false
	}
	pRtlMoveMemory.Call(p, uintptr(unsafe.Pointer(&w[0])), size)
	pGlobalUnlock.Call(h)
	r, _, _ := pSetClipboardData.Call(cfUnicodeText, h)
	return r != 0
}

func key(vk uint16, up bool) input {
	in := input{Type: 1}
	in.Ki.Vk = vk
	if up {
		in.Ki.Flags = 2 // KEYEVENTF_KEYUP
	}
	return in
}

// pasteText puts the text on the clipboard, presses Ctrl+V, then gives the old clipboard back.
func pasteText(text string) {
	var prevText string
	var hadPrev bool
	if openClip() {
		prevText, hadPrev = readClipboard()
		pCloseClipboard.Call()
	}
	if !setClipboard(text) {
		return
	}
	ins := []input{key(vkControl, false), key(vkV, false), key(vkV, true), key(vkControl, true)}
	pSendInput.Call(uintptr(len(ins)), uintptr(unsafe.Pointer(&ins[0])), unsafe.Sizeof(ins[0]))
	if hadPrev {
		go func() {
			time.Sleep(900 * time.Millisecond) // slow apps read the clipboard late
			setClipboard(prevText)
		}()
	}
}
