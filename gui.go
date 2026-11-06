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
		},
	})
	if w == nil {
		_ = ln.Close()
		return fmt.Errorf("创建 WebView2 窗口失败，请确认已安装 WebView2 Runtime")
	}
	defer w.Destroy()

	// 去掉系统标题栏，由前端绘制自定义标题栏
	hwnd := uintptr(w.Window())
	removeWindowTitleBar(hwnd)
	api.SetHWND(hwnd)

	// 第二步：窗口就绪后再启动 HTTP 服务
	srv := &http.Server{Handler: api.Routes()}
	go func() {
		_ = srv.Serve(ln)
	}()
	defer srv.Close()

	// 第三步：加载页面并进入消息循环
	w.Navigate("http://" + addr)
	w.Run()

	app.Shutdown()
	return nil
}
