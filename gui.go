package main

import (
	"embed"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"

	webview2 "github.com/jchv/go-webview2"
)

// runServe 只启动本地 HTTP 服务，不创建 WebView2 窗口。
//
// 用途：在没有交互式桌面会话的环境（远程调试、CI、自动化测试）里
// 用浏览器访问同一套界面。绑定的始终是 127.0.0.1，不对外暴露。
func runServe(assets embed.FS) error {
	app := NewApp()
	hub := NewEventHub()
	app.SetHub(hub)
	api := NewAPI(app, hub, assets)

	addr := os.Getenv("CFSTX_ADDR")
	if addr == "" {
		addr = "127.0.0.1:18080"
	}
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		// 端口被占用或落在 Windows 保留区间时，退回随机端口。
		// 实际监听地址会打印出来，调试时照抄即可。
		ln, err = net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			return err
		}
	}
	fmt.Printf("CFSTX 服务已启动：http://%s\n", ln.Addr().String())
	return http.Serve(ln, api.Routes())
}

// runGUI 启动本地 HTTP 服务并用 WebView2 窗口加载前端。
//
// 不使用 Wails：Wails 2.17.1 在 Windows 11 build 26200 上创建 WebView2
// 控制器会返回 0x8000FFFF，而底层 go-webview2 在该环境工作正常。
//
// 注意顺序：必须先创建 WebView2 窗口（完成主线程 COM/STA 初始化），
// 再启动 HTTP 服务 goroutine，否则 WebView2 控制器创建会失败。
func runGUI(assets embed.FS) error {
	app := NewApp()
	hub := NewEventHub()
	app.SetHub(hub)

	api := NewAPI(app, hub, assets)

	// 先只监听端口，拿到可用地址，暂不启动服务
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return fmt.Errorf("启动本地服务失败: %w", err)
	}
	addr := ln.Addr().String()

	// WebView2 的默认数据目录由 exe 文件名推导，一旦该目录损坏，
	// 创建控制器会稳定失败（0x8000FFFF）。这里显式使用固定目录。
	dataPath := filepath.Join(configDir(), "WebView2")
	_ = os.MkdirAll(dataPath, 0o755)

	// 第一步：创建窗口
	w := webview2.NewWithOptions(webview2.WebViewOptions{
		Debug:     false,
		AutoFocus: true,
		DataPath:  dataPath,
		WindowOptions: webview2.WindowOptions{
			Title:  "CFSTX",
			Width:  1360,
			Height: 900,
			// 使用 rsrc_windows_amd64.syso 中 cfstx.ico 的资源 ID（默认为 1）。
			// 注意：构建后必须执行 build.ps1 里的 icacls 提升完整性级别，
			// 否则 WebView2 在低完整性进程下创建控制器会失败。
			IconId: 1,
		},
	})
	if w == nil {
		_ = ln.Close()
		return fmt.Errorf("创建 WebView2 窗口失败，请确认已安装 WebView2 Runtime")
	}
	defer w.Destroy()

	// 第二步：窗口就绪后再启动 HTTP 服务
	srv := &http.Server{Handler: api.Routes()}
	go func() {
		_ = srv.Serve(ln)
	}()
	defer srv.Close()

	// 第三步：安装托盘。之后关闭窗口只隐藏到托盘，从托盘菜单
	// 「退出程序」才真正结束进程，保证定时任务能在后台常驻。
	// 初始化失败时保持「关闭即退出」的原始行为，不影响主功能。
	hwnd := uintptr(w.Window())
	if tr, err := installTray(hwnd, 1, trayHandlers{
		OnExit: func() { w.Terminate() },
	}); err != nil {
		app.appendLog("warn", "tray", "托盘初始化失败，关闭窗口将直接退出："+err.Error())
	} else {
		defer tr.remove()
	}

	// 启动定时自动同步。runServe 模式不启动，避免自动化测试误推送。
	if app.auto != nil {
		app.auto.Start()
	}

	// 第四步：加载页面并进入消息循环
	w.Navigate("http://" + addr)
	w.Run()

	app.Shutdown()
	return nil
}
