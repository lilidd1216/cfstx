package main

import (
	"embed"
	"os"
	"path/filepath"

	"gitee.com/liliaic/cfstx/cli"
)

//go:embed all:frontend/dist
var assets embed.FS

func main() {
	// 单 exe 双模式：带参数时走命令行，双击（无参数）时启动图形界面。
	if len(os.Args) > 1 {
		attachConsole()
		cli.Run()
		return
	}

	if err := runGUI(assets); err != nil {
		// GUI 模式没有控制台，把启动错误写入临时文件便于排查
		logPath := filepath.Join(os.TempDir(), "cfstx_gui_error.log")
		_ = os.WriteFile(logPath, []byte(err.Error()), 0o644)
	}
}
