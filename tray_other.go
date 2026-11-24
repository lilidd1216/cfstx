//go:build !windows

package main

import "errors"

// 非 Windows 平台没有系统托盘，保持与 tray_windows.go 相同的签名，
// 便于主包在非 Windows 下通过语法检查。
type trayHandlers struct {
	OnExit func()
}

type winTray struct{}

func installTray(hwnd uintptr, iconID uint32, h trayHandlers) (*winTray, error) {
	return nil, errors.New("系统托盘仅支持 Windows")
}

func (t *winTray) remove() {}
