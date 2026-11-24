//go:build windows

package main

import (
	"fmt"
	"unsafe"

	"golang.org/x/sys/windows"
)

// Win32 消息与常量。
const (
	wmClose         = 0x0010
	wmDestroy       = 0x0002
	wmSysCommand    = 0x0112
	wmLButtonUp     = 0x0202
	wmLButtonDblClk = 0x0203
	wmRButtonUp     = 0x0205
	wmContextMenu   = 0x007B
	wmNull          = 0x0000

	// wmTray 是托盘图标回调消息。必须避开 0x8000——那是 go-webview2
	// 的 Dispatch 用来投递主线程任务的线程消息号，会被 Run() 特判消费。
	wmTray = 0x8000 + 1

	scMinimize = 0xF020
	scClose    = 0xF060

	swHide    = 0
	swShow    = 5
	swRestore = 9

	// GWLP_WNDPROC = -4
	gwlpWndProc = ^uintptr(3)

	nimAdd    = 0
	nimModify = 1
	nimDelete = 2

	nifMessage = 0x1
	nifIcon    = 0x2
	nifTip     = 0x4

	imageIcon     = 1
	lrDefaultSize = 0x0040
	lrShared      = 0x8000

	mfString    = 0x0000
	mfSeparator = 0x0800

	tpmReturnCmd   = 0x0100
	tpmRightButton = 0x0002
	tpmNoNotify    = 0x0080

	idTrayShow = 1001
	idTrayExit = 1002

	smCxIcon = 11
	smCyIcon = 12

	idiApplication = 32512
)

var (
	user32  = windows.NewLazySystemDLL("user32.dll")
	shell32 = windows.NewLazySystemDLL("shell32.dll")

	procSetWindowLongPtrW = user32.NewProc("SetWindowLongPtrW")
	procGetWindowLongPtrW = user32.NewProc("GetWindowLongPtrW")
	procCallWindowProcW   = user32.NewProc("CallWindowProcW")
	procDefWindowProcW    = user32.NewProc("DefWindowProcW")
	procShowWindow        = user32.NewProc("ShowWindow")
	procSetForeground     = user32.NewProc("SetForegroundWindow")
	procIsIconic          = user32.NewProc("IsIconic")
	procGetCursorPos      = user32.NewProc("GetCursorPos")
	procCreatePopupMenu   = user32.NewProc("CreatePopupMenu")
	procAppendMenuW       = user32.NewProc("AppendMenuW")
	procTrackPopupMenu    = user32.NewProc("TrackPopupMenu")
	procDestroyMenu       = user32.NewProc("DestroyMenu")
	procPostMessageW      = user32.NewProc("PostMessageW")
	procRegisterWindowMsg = user32.NewProc("RegisterWindowMessageW")
	procLoadImageW        = user32.NewProc("LoadImageW")
	procGetSystemMetrics  = user32.NewProc("GetSystemMetrics")
	procShellNotifyIcon   = shell32.NewProc("Shell_NotifyIconW")
)

// notifyIconData 对应 Win32 的 NOTIFYICONDATAW（v3 布局）。
type notifyIconData struct {
	cbSize           uint32
	hWnd             windows.HWND
	uID              uint32
	uFlags           uint32
	uCallbackMessage uint32
	hIcon            windows.Handle
	szTip            [128]uint16
	dwState          uint32
	dwStateMask      uint32
	szInfo           [256]uint16
	uVersion         uint32
	szInfoTitle      [64]uint16
	dwInfoFlags      uint32
	guidItem         windows.GUID
	hBalloonIcon     windows.Handle
}

type trayPoint struct {
	x, y int32
}

// trayHandlers 由 GUI 层注入的回调。
type trayHandlers struct {
	// OnExit 在用户从托盘菜单选择「退出」时调用（已在消息线程内）。
	// 约定实现为触发 WebView 消息循环退出。
	OnExit func()
}

type winTray struct {
	hwnd       uintptr
	hIcon      windows.Handle
	taskbarMsg uint32
	oldProc    uintptr
	quitting   bool
	added      bool
	handlers   trayHandlers
}

var (
	trayInstance *winTray
	// trayProcPtr 必须保存在包级变量中，否则 Go 回调会被 GC 回收，
	// 窗口过程一旦被回收，后续消息会直接崩溃。
	trayProcPtr uintptr
)

// installTray 子类化主窗口过程并注册托盘图标。
//
// 失败时返回 error，调用方应回退为「关闭窗口即退出」的原始行为。
func installTray(hwnd uintptr, iconID uint32, h trayHandlers) (*winTray, error) {
	if hwnd == 0 {
		return nil, fmt.Errorf("窗口句柄为空")
	}
	t := &winTray{hwnd: hwnd, handlers: h}

	var hInst windows.Handle
	_ = windows.GetModuleHandleEx(0, nil, &hInst)
	hi, _, _ := procLoadImageW.Call(
		uintptr(hInst),
		uintptr(iconID),
		imageIcon,
		0, 0,
		uintptr(lrDefaultSize|lrShared),
	)
	if hi == 0 {
		// 回退到系统默认应用图标
		cx, _, _ := procGetSystemMetrics.Call(smCxIcon)
		cy, _, _ := procGetSystemMetrics.Call(smCyIcon)
		hi, _, _ = procLoadImageW.Call(
			0, idiApplication, imageIcon,
			cx, cy, uintptr(lrShared),
		)
	}
	if hi == 0 {
		return nil, fmt.Errorf("加载托盘图标失败")
	}
	t.hIcon = windows.Handle(hi)

	old, _, _ := procGetWindowLongPtrW.Call(hwnd, gwlpWndProc)
	if old == 0 {
		return nil, fmt.Errorf("读取窗口过程失败")
	}
	t.oldProc = old

	trayInstance = t
	if trayProcPtr == 0 {
		trayProcPtr = windows.NewCallback(traySubclassProc)
	}
	// SetWindowLongPtrW 成功时返回原窗口过程地址（非 0）；返回 0 视为失败。
	if r, _, _ := procSetWindowLongPtrW.Call(hwnd, gwlpWndProc, trayProcPtr); r == 0 {
		trayInstance = nil
		return nil, fmt.Errorf("替换窗口过程失败")
	}

	if err := t.addIcon(); err != nil {
		procSetWindowLongPtrW.Call(hwnd, gwlpWndProc, t.oldProc)
		trayInstance = nil
		return nil, err
	}

	name, _ := windows.UTF16PtrFromString("TaskbarCreated")
	msg, _, _ := procRegisterWindowMsg.Call(uintptr(unsafe.Pointer(name)))
	t.taskbarMsg = uint32(msg)

	return t, nil
}

// remove 删除托盘图标并恢复原始窗口过程（幂等）。
func (t *winTray) remove() {
	if t == nil {
		return
	}
	t.deleteIcon()
	if t.hwnd != 0 && t.oldProc != 0 {
		procSetWindowLongPtrW.Call(t.hwnd, gwlpWndProc, t.oldProc)
	}
	if trayInstance == t {
		trayInstance = nil
	}
}

func (t *winTray) addIcon() error {
	var nid notifyIconData
	nid.cbSize = uint32(unsafe.Sizeof(nid))
	nid.hWnd = windows.HWND(t.hwnd)
	nid.uID = 1
	nid.uFlags = nifMessage | nifIcon | nifTip
	nid.uCallbackMessage = wmTray
	nid.hIcon = t.hIcon
	copyUTF16(nid.szTip[:], "CFSTX 优选IP · 双击显示，右键退出")

	r, _, _ := procShellNotifyIcon.Call(nimAdd, uintptr(unsafe.Pointer(&nid)))
	if r == 0 {
		return fmt.Errorf("Shell_NotifyIcon(NIM_ADD) 失败")
	}
	t.added = true
	return nil
}

func (t *winTray) deleteIcon() {
	if t == nil || !t.added {
		return
	}
	var nid notifyIconData
	nid.cbSize = uint32(unsafe.Sizeof(nid))
	nid.hWnd = windows.HWND(t.hwnd)
	nid.uID = 1
	procShellNotifyIcon.Call(nimDelete, uintptr(unsafe.Pointer(&nid)))
	t.added = false
}

func (t *winTray) showWindow() {
	if r, _, _ := procIsIconic.Call(t.hwnd); r != 0 {
		procShowWindow.Call(t.hwnd, swRestore)
	}
	procShowWindow.Call(t.hwnd, swShow)
	procSetForeground.Call(t.hwnd)
}

func (t *winTray) hideWindow() {
	procShowWindow.Call(t.hwnd, swHide)
}

func (t *winTray) showMenu() {
	menu, _, _ := procCreatePopupMenu.Call()
	if menu == 0 {
		return
	}
	defer procDestroyMenu.Call(menu)

	appendMenu(menu, mfString, idTrayShow, "显示主界面")
	appendMenu(menu, mfSeparator, 0, "")
	appendMenu(menu, mfString, idTrayExit, "退出程序")

	var pt trayPoint
	procGetCursorPos.Call(uintptr(unsafe.Pointer(&pt)))
	// 必须先 SetForegroundWindow，否则菜单点击后不会自动消失。
	procSetForeground.Call(t.hwnd)

	cmd, _, _ := procTrackPopupMenu.Call(
		menu,
		uintptr(tpmReturnCmd|tpmRightButton|tpmNoNotify),
		uintptr(pt.x), uintptr(pt.y),
		0,
		t.hwnd,
		0,
	)
	procPostMessageW.Call(t.hwnd, wmNull, 0, 0)

	switch cmd {
	case idTrayShow:
		t.showWindow()
	case idTrayExit:
		t.requestQuit()
	}
}

// requestQuit 执行退出流程：删图标、恢复原窗口过程、通知 GUI 层结束消息循环。
func (t *winTray) requestQuit() {
	if t == nil || t.quitting {
		return
	}
	t.quitting = true
	// 托盘图标必须在窗口销毁前删除，否则会残留幽灵图标。
	t.deleteIcon()
	if t.hwnd != 0 && t.oldProc != 0 {
		procSetWindowLongPtrW.Call(t.hwnd, gwlpWndProc, t.oldProc)
	}
	if t.handlers.OnExit != nil {
		t.handlers.OnExit()
	}
}

// traySubclassProc 是替换后的窗口过程。
func traySubclassProc(hwnd, msg, wp, lp uintptr) uintptr {
	t := trayInstance
	if t == nil || t.hwnd != hwnd {
		return defaultWndProc(hwnd, msg, wp, lp)
	}

	switch msg {
	case wmClose:
		if !t.quitting {
			t.hideWindow()
			return 0
		}
		// 退出流程中放行，交给原始过程执行销毁。
		return callOldWndProc(t, hwnd, msg, wp, lp)

	case wmSysCommand:
		cmd := wp & 0xFFF0
		if cmd == scMinimize {
			t.hideWindow()
			return 0
		}
		if cmd == scClose && !t.quitting {
			t.hideWindow()
			return 0
		}
		return callOldWndProc(t, hwnd, msg, wp, lp)

	case wmTray:
		switch uint32(lp & 0xFFFF) {
		case wmLButtonUp, wmLButtonDblClk:
			t.showWindow()
		case wmRButtonUp, wmContextMenu:
			t.showMenu()
		}
		return 0
	}

	// Explorer 重启后任务栏重建，需要把图标重新加回去。
	if t.taskbarMsg != 0 && uint32(msg) == t.taskbarMsg {
		_ = t.addIcon()
		return 0
	}

	return callOldWndProc(t, hwnd, msg, wp, lp)
}

func callOldWndProc(t *winTray, hwnd, msg, wp, lp uintptr) uintptr {
	if t != nil && t.oldProc != 0 {
		r, _, _ := procCallWindowProcW.Call(t.oldProc, hwnd, msg, wp, lp)
		return r
	}
	return defaultWndProc(hwnd, msg, wp, lp)
}

func defaultWndProc(hwnd, msg, wp, lp uintptr) uintptr {
	r, _, _ := procDefWindowProcW.Call(hwnd, msg, wp, lp)
	return r
}

func appendMenu(menu, flags, id uintptr, text string) {
	var ptr uintptr
	if text != "" {
		u, _ := windows.UTF16PtrFromString(text)
		ptr = uintptr(unsafe.Pointer(u))
	}
	procAppendMenuW.Call(menu, flags, id, ptr)
}

func copyUTF16(dst []uint16, s string) {
	u, err := windows.UTF16FromString(s)
	if err != nil {
		// 字符串含 NUL 时 UTF16FromString 报错，退化为逐字符复制。
		u = make([]uint16, 0, len(s)+1)
		for _, r := range s {
			if r == 0 {
				break
			}
			u = append(u, uint16(r))
		}
		u = append(u, 0)
	}
	copy(dst, u)
}
