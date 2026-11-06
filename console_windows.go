//go:build windows

package main

import (
	"os"
	"syscall"
)

// Windows 标准句柄常量是负数：STD_OUTPUT_HANDLE = -11、STD_ERROR_HANDLE = -12。
// ^uintptr(n) 等于按位取反得到 -(n+1)，所以 -11 要写成 ^uintptr(10)。
const (
	attachParentProcess = ^uintptr(0)  // (DWORD)-1
	stdOutputHandle     = ^uintptr(10) // (DWORD)-11
	stdErrorHandle      = ^uintptr(11) // (DWORD)-12
)

// stdHandleValid 判断标准输出句柄是否已经可用（被重定向或继承）。
func stdHandleValid() bool {
	kernel32 := syscall.NewLazyDLL("kernel32.dll")
	getStdHandle := kernel32.NewProc("GetStdHandle")

	h, _, _ := getStdHandle.Call(stdOutputHandle)
	if h == 0 || h == ^uintptr(0) {
		return false
	}
	hErr, _, _ := getStdHandle.Call(stdErrorHandle)
	return hErr != 0 && hErr != ^uintptr(0)
}

// attachConsole 让 GUI 子系统程序在命令行中运行时能够输出到父控制台。
//
// Wails 构建的 exe 是 windowsgui 子系统：
//   - 直接在 CMD/PowerShell 中运行：没有控制台句柄，需要 AttachConsole 后重绑 CONOUT$
//   - 使用 > 重定向到文件：stdout/stdout 句柄已有效，直接沿用，不要覆盖
func attachConsole() {
	if stdHandleValid() {
		return
	}

	kernel32 := syscall.NewLazyDLL("kernel32.dll")
	attach := kernel32.NewProc("AttachConsole")
	_, _, _ = attach.Call(attachParentProcess)

	out, err := os.OpenFile("CONOUT$", os.O_WRONLY, 0)
	if err != nil {
		return
	}
	os.Stdout = out
	os.Stderr = out
}
