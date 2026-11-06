//go:build !windows

package main

// 非 Windows 平台为空实现，便于跨平台编译检查。

func removeWindowTitleBar(hwnd uintptr) {}

func startWindowDrag(hwnd uintptr) {}

func minimizeWindow(hwnd uintptr) {}

func toggleMaximizeWindow(hwnd uintptr) {}

func closeWindow(hwnd uintptr) {}
