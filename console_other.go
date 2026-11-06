//go:build !windows

package main

// attachConsole 在非 Windows 平台上是空操作，便于跨平台编译检查。
func attachConsole() {}
