//go:build windows

package main

import (
	"syscall"
	"unsafe"
)

var (
	user32             = syscall.NewLazyDLL("user32.dll")
	dwmapi             = syscall.NewLazyDLL("dwmapi.dll")
	procSetWindowLongW = user32.NewProc("SetWindowLongW")
	procGetWindowLongW = user32.NewProc("GetWindowLongW")
	procSetWindowPos   = user32.NewProc("SetWindowPos")
	procReleaseCapture = user32.NewProc("ReleaseCapture")
	procSendMessageW   = user32.NewProc("SendMessageW")
	procShowWindow     = user32.NewProc("ShowWindow")
	procPostMessageW   = user32.NewProc("PostMessageW")
	procIsZoomed       = user32.NewProc("IsZoomed")
	procDwmSetAttr     = dwmapi.NewProc("DwmSetWindowAttribute")
)

// GWL_STYLE = -16，uintptr 下用补码表示，避免常量溢出
var gwlStyle = ^uintptr(15)

const (
	wsCaption = 0x00C00000 // 系统标题栏

	// DWM 属性（Windows 11）
	dwmwaBorderColor      = 34 // DWMWA_BORDER_COLOR
	dwmwaCornerPreference = 33 // DWMWA_WINDOW_CORNER_PREFERENCE
	dwmcpRound            = 2  // DWMWCP_ROUND

	wmNCLButtonDown = 0x00A1
	htCaption       = 2
	wmClose         = 0x0010

	swMaximize = 3
	swMinimize = 6
	swRestore  = 9

	swpNoSize       = 0x0001
	swpNoMove       = 0x0002
	swpNoZOrder     = 0x0004
	swpFrameChanged = 0x0020
)

// removeWindowTitleBar 去掉系统标题栏，保留 WS_THICKFRAME 以便拖拽边缘调整大小。
func removeWindowTitleBar(hwnd uintptr) {
	if hwnd == 0 {
		return
	}
	style, _, _ := procGetWindowLongW.Call(hwnd, gwlStyle)
	newStyle := style &^ wsCaption
	procSetWindowLongW.Call(hwnd, gwlStyle, newStyle)
	// 通知系统重新计算窗口框架
	procSetWindowPos.Call(hwnd, 0, 0, 0, 0, 0,
		swpNoMove|swpNoSize|swpNoZOrder|swpFrameChanged)
}

// setWindowFrameStyle 把 DWM 绘制的窗口边框改成深色并启用圆角，
// 避免浅色系统主题下无边框窗口顶部出现白条。
func setWindowFrameStyle(hwnd uintptr) {
	if hwnd == 0 {
		return
	}
	// COLORREF: 0x00BBGGRR -> #1b1b21
	border := uint32(0x00211B1B)
	procDwmSetAttr.Call(hwnd, dwmwaBorderColor,
		uintptr(unsafe.Pointer(&border)), unsafe.Sizeof(border))

	corner := int32(dwmcpRound)
	procDwmSetAttr.Call(hwnd, dwmwaCornerPreference,
		uintptr(unsafe.Pointer(&corner)), unsafe.Sizeof(corner))
}

// startWindowDrag 让系统接管标题栏区域的鼠标拖动（原生平滑移动）。
func startWindowDrag(hwnd uintptr) {
	if hwnd == 0 {
		return
	}
	procReleaseCapture.Call()
	procSendMessageW.Call(hwnd, wmNCLButtonDown, htCaption, 0)
}

func minimizeWindow(hwnd uintptr) {
	if hwnd != 0 {
		procShowWindow.Call(hwnd, swMinimize)
	}
}

// toggleMaximizeWindow 在最大化和还原之间切换。
func toggleMaximizeWindow(hwnd uintptr) {
	if hwnd == 0 {
		return
	}
	zoomed, _, _ := procIsZoomed.Call(hwnd)
	if zoomed != 0 {
		procShowWindow.Call(hwnd, swRestore)
	} else {
		procShowWindow.Call(hwnd, swMaximize)
	}
}

func closeWindow(hwnd uintptr) {
	if hwnd != 0 {
		procPostMessageW.Call(hwnd, wmClose, 0, 0)
	}
}
