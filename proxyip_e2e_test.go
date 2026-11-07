package main

import (
	"bytes"
	"embed"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"
)

// TestServeForBrowser 起一个常驻的本地服务，供浏览器手动验证界面。
// 默认跳过，只在开发时用：
//
//	CFSTX_SERVE=1 go test -run TestServeForBrowser -timeout 0 -v .
func TestServeForBrowser(t *testing.T) {
	if os.Getenv("CFSTX_SERVE") == "" {
		t.Skip("设置 CFSTX_SERVE=1 才启动")
	}

	app := NewApp()
	hub := NewEventHub()
	app.SetHub(hub)
	// 前端资源走磁盘，这样改完前端刷新即可，不用重新 embed
	api := NewAPI(app, hub, os.DirFS("."))

	ts := httptest.NewServer(api.Routes())
	defer ts.Close()

	t.Logf("SERVE_URL=%s", ts.URL)
	_ = os.MkdirAll("build/tmp", 0o755)
	_ = os.WriteFile("build/tmp/serve_url.txt", []byte(ts.URL), 0o644)
	select {}
}

// TestProxyIPAPIFlow 端到端验证 ProxyIP 检测的 HTTP 流程：
// 数据源 → 开始检测 → SSE 进度 → 完成事件。
//
// 需要外网，所以默认跳过。手动验证时：
//
//	CFSTX_E2E=1 go test -run TestProxyIPAPIFlow -v .
func TestProxyIPAPIFlow(t *testing.T) {
	if os.Getenv("CFSTX_E2E") == "" {
		t.Skip("需要外网，设置 CFSTX_E2E=1 才运行")
	}

	app := NewApp()
	hub := NewEventHub()
	app.SetHub(hub)
	api := NewAPI(app, hub, embed.FS{})

	ts := httptest.NewServer(api.Routes())
	defer ts.Close()

	// 1. 数据源列表
	resp, err := http.Get(ts.URL + "/api/proxyip/sources")
	if err != nil {
		t.Fatalf("获取数据源失败: %v", err)
	}
	var sources []ProxyIPSourceDTO
	if err := json.NewDecoder(resp.Body).Decode(&sources); err != nil {
		t.Fatalf("解析数据源失败: %v", err)
	}
	resp.Body.Close()
	if len(sources) == 0 {
		t.Fatal("数据源列表为空")
	}
	t.Logf("数据源：%d 个，第一个是 %s（%d 条）", len(sources), sources[0].Name, sources[0].Count)

	// 2. 订阅事件后再发请求，避免漏掉 started
	ch := hub.Subscribe()
	defer hub.Unsubscribe(ch)

	// 3. 用内置域名源（不依赖 GitHub）作为候选
	body, _ := json.Marshal(ProxyIPCheckRequest{
		Text:        "68.68.32.120:443\n165.22.137.198:443\n104.16.0.1:443\nwww.baidu.com:443",
		Domain:      "",
		Concurrency: 4,
	})
	resp, err = http.Post(ts.URL+"/api/proxyip/check", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("启动检测失败: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("启动检测返回 %d", resp.StatusCode)
	}
	resp.Body.Close()

	// 4. 等完成事件
	deadline := time.After(60 * time.Second)
	var finished map[string]any
	progress := 0
	for finished == nil {
		select {
		case <-deadline:
			t.Fatalf("等待完成超时，已收到 %d 个进度事件", progress)
		case raw := <-ch:
			var msg struct {
				Event string         `json:"event"`
				Data  map[string]any `json:"data"`
			}
			if err := json.Unmarshal(raw, &msg); err != nil {
				continue
			}
			switch msg.Event {
			case "proxyip:progress":
				progress++
			case "proxyip:finished":
				finished = msg.Data
			}
		}
	}

	results, _ := finished["results"].([]any)
	okCount, _ := finished["okCount"].(float64)
	total, _ := finished["total"].(float64)
	t.Logf("检测完成：%.0f 个候选，%.0f 个可用，收到 %d 个进度事件",
		total, okCount, progress)

	if len(results) != 4 {
		t.Errorf("结果数应为 4，实际 %d", len(results))
	}
	if okCount == 0 {
		t.Error("应该至少有一股可用 ProxyIP")
	}
	if progress != int(total) {
		t.Errorf("进度事件数 %d 应等于候选数 %.0f", progress, total)
	}

	// 5. 确认 CF 自有网段被剔除
	for _, item := range results {
		r := item.(map[string]any)
		addr, _ := r["addr"].(string)
		if addr == "104.16.0.1:443" {
			if ok, _ := r["ok"].(bool); ok {
				t.Error("104.16.0.1 属于 Cloudflare 自有网段，不应判为可用")
			}
		}
	}

	// 6. 状态接口
	resp, err = http.Get(ts.URL + "/api/proxyip/status")
	if err != nil {
		t.Fatalf("获取状态失败: %v", err)
	}
	var st ProxyIPStatus
	if err := json.NewDecoder(resp.Body).Decode(&st); err != nil {
		t.Fatalf("解析状态失败: %v", err)
	}
	resp.Body.Close()
	if st.Running {
		t.Error("检测已结束，running 应为 false")
	}
	if len(st.Results) != 4 {
		t.Errorf("状态里的结果数应为 4，实际 %d", len(st.Results))
	}
}
