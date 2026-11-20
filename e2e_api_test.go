package main

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
)

// newE2ETestAPI 用临时配置目录起一个测试 API，避免写用户真实配置。
func newE2ETestAPI(t *testing.T) *httptest.Server {
	t.Helper()
	t.Setenv("CFSTX_CONFIG_DIR", t.TempDir())
	app := NewApp()
	hub := NewEventHub()
	app.SetHub(hub)
	ts := httptest.NewServer(NewAPI(app, hub, fstest.MapFS{}).Routes())
	t.Cleanup(ts.Close)
	return ts
}

func TestE2ECredentialsMasked(t *testing.T) {
	ts := newE2ETestAPI(t)
	secretUUID := "11111111-2222-3333-4444-555555555555"
	secretPath := "/proxyip=secret-host:443"

	body := `{"uuid":"` + secretUUID + `","wsPath":"` + secretPath + `"}`
	resp, err := http.Post(ts.URL+"/api/e2e/credentials", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()

	get, err := http.Get(ts.URL + "/api/e2e/credentials")
	if err != nil {
		t.Fatal(err)
	}
	data, _ := io.ReadAll(get.Body)
	get.Body.Close()
	if strings.Contains(string(data), secretUUID) || strings.Contains(string(data), "secret-host") {
		t.Fatalf("GET 泄露了凭据: %s", data)
	}
	if !strings.Contains(string(data), `"hasUuid":true`) || !strings.Contains(string(data), `"hasPath":true`) {
		t.Fatalf("应只返回布尔状态: %s", data)
	}
	if get.Header.Get("Cache-Control") != "no-store" {
		t.Fatalf("凭据状态不应被缓存")
	}

	// config.json 不应包含凭据
	cfgData, _ := os.ReadFile(filepath.Join(os.Getenv("CFSTX_CONFIG_DIR"), "config.json"))
	if strings.Contains(string(cfgData), secretUUID) {
		t.Fatalf("config.json 泄露了凭据")
	}

	// DELETE 后清空
	req, _ := http.NewRequest(http.MethodDelete, ts.URL+"/api/e2e/credentials", nil)
	del, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	del.Body.Close()
	get2, _ := http.Get(ts.URL + "/api/e2e/credentials")
	data2, _ := io.ReadAll(get2.Body)
	get2.Body.Close()
	if !strings.Contains(string(data2), `"hasUuid":false`) {
		t.Fatalf("DELETE 后应为 false: %s", data2)
	}
}

func TestE2EStartFlow(t *testing.T) {
	if os.Getenv("CFSTX_E2E") != "1" {
		t.Skip("设置 CFSTX_E2E=1 后运行真实环境集成测试")
	}
	host := os.Getenv("CFSTX_E2E_HOST")
	uuid := os.Getenv("CFSTX_E2E_UUID")
	path := os.Getenv("CFSTX_E2E_PATH")
	if host == "" || uuid == "" || path == "" {
		t.Skip("缺少 CFSTX_E2E_HOST / CFSTX_E2E_UUID / CFSTX_E2E_PATH")
	}

	t.Setenv("CFSTX_CONFIG_DIR", t.TempDir())
	app := NewApp()
	hub := NewEventHub()
	app.SetHub(hub)
	if err := app.SaveE2ECredentials(E2ECredentials{UUID: uuid, WSPath: path}); err != nil {
		t.Fatal(err)
	}

	cfg := Config{
		Threads:         8,
		PingTimes:       1,
		Port:            443,
		Httping:         false,
		Sources:         []string{sourceManual},
		IPText:          "139.84.164.189,13.233.174.246,104.17.132.164",
		MaxDelay:        9999,
		MaxLossRate:     1,
		TestCount:       3,
		DisableDownload: true,
		TestAll:         false,
		E2EEnabled:      true,
		E2EProtocol:     "vless",
		E2EHost:         host,
		E2EPort:         443,
		E2ETarget:       "http://www.gstatic.com/generate_204",
		E2EConcurrency:  8,
		E2ERetries:      2,
		E2EMaxLatency:   3000,
		E2ELimit:        10,
	}
	app.runTest(cfg)

	app.mu.Lock()
	results := append([]ResultItem(nil), app.results...)
	app.mu.Unlock()
	if len(results) == 0 {
		t.Fatalf("端到端流程没有产生结果")
	}
	for _, r := range results {
		if r.E2E == nil || !r.E2E.OK {
			t.Fatalf("%s:%d 缺少成功的端到端结果: %+v", r.IP, r.Port, r.E2E)
		}
		if r.E2E.Best <= 0 {
			t.Fatalf("%s:%d 的端到端延迟无效: %.3f", r.IP, r.Port, r.E2E.Best)
		}
	}
	t.Logf("端到端结果 %d 条，最优 %s:%d %.0fms", len(results), results[0].IP, results[0].Port, results[0].E2E.Best)
}
