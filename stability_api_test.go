package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"

	"gitee.com/liliaic/cfstx/task"
)

func newStabilityTestAPI() http.Handler {
	app := NewApp()
	hub := NewEventHub()
	app.SetHub(hub)
	return NewAPI(app, hub, fstest.MapFS{}).Routes()
}

func TestStabilityStartWithoutResults(t *testing.T) {
	h := newStabilityTestAPI()
	req := httptest.NewRequest(http.MethodPost, "/api/stability/start",
		strings.NewReader(`{"rounds":5,"concurrency":2}`))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusConflict {
		t.Fatalf("没有测速结果时应返回 409，实际 %d：%s", rec.Code, rec.Body.String())
	}
}

func TestStabilityStatusReturnsEmptyArray(t *testing.T) {
	h := newStabilityTestAPI()
	req := httptest.NewRequest(http.MethodGet, "/api/stability/status", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("状态接口应返回 200，实际 %d", rec.Code)
	}
	var got StabilityStatus
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("响应不是合法 JSON：%v", err)
	}
	// null 会让前端的 v-for 和 filter 报错，必须是空数组
	if got.Results == nil {
		t.Fatal("results 应序列化为 [] 而不是 null")
	}
}

// 前端按字段名取值，任何一个改名都会让表格列静默变空白。
func TestStabilityResultJSONKeys(t *testing.T) {
	b, err := json.Marshal(task.StabilityResult{IP: "1.2.3.4", Port: 443})
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{
		"ip", "port", "attempts", "success", "successRate",
		"tcpAvg", "tcpJitter", "tlsAvg", "tlsJitter",
		"totalAvg", "totalMax", "keepAliveOk", "keepAliveTested",
		"reused", "score",
	} {
		if _, ok := m[k]; !ok {
			t.Errorf("缺少字段 %q —— 前端会读到 undefined", k)
		}
	}
}

// 稳定性测试和主测速互斥，两者同时跑数字都没意义。
func TestStabilityMutualExclusionWithSpeedTest(t *testing.T) {
	app := NewApp()
	app.SetHub(NewEventHub())
	app.results = []ResultItem{{IP: "1.2.3.4", Port: 443}}

	// 伪造「主测速正在跑」的状态
	app.mu.Lock()
	app.running = true
	app.mu.Unlock()

	if err := app.StartStability(StabilityRequest{Rounds: 1}); err == nil {
		t.Fatal("主测速进行中时，稳定性测试应被拒绝")
	}

	app.mu.Lock()
	app.running = false
	app.mu.Unlock()
}
