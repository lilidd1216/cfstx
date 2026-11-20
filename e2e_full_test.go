package main

import (
	"fmt"
	"os"
	"sort"
	"strings"
	"testing"
	"time"

	"gitee.com/liliaic/cfstx/task"
)

// TestE2EFullRun 按用户指定的条件跑一遍完整流程：
// ip.txt + 端到端上限 200ms + 速度下限 5MB/s。
//
//	CFSTX_FULL=1 \
//	CFSTX_E2E_HOST=fast2... CFSTX_E2E_UUID=... CFSTX_E2E_PATH='...' \
//	go test . -run TestE2EFullRun -v -timeout 15m
func TestE2EFullRun(t *testing.T) {
	if os.Getenv("CFSTX_FULL") != "1" {
		t.Skip("设置 CFSTX_FULL=1 后运行完整流程")
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

	var countryFilter []string
	if v := os.Getenv("CFSTX_COUNTRY"); v != "" {
		for _, s := range strings.Split(v, ",") {
			if s = strings.TrimSpace(s); s != "" {
				countryFilter = append(countryFilter, s)
			}
		}
	}

	cfg := Config{
		Threads:         200,
		PingTimes:       4,
		Port:            443,
		Httping:         true, // HTTPing 才能从 cf-ray 拿到 L1 机房，供国家筛选回退
		Sources:         []string{sourceIPv4},
		IPFile:          "ip.txt",
		ProbeEnabled:    true,
		ProbeDomain:     host,
		MaxDelay:        9999, // 启用端到端后 L1 只做候选池
		MaxLossRate:     1,
		MinSpeed:        0,
		DownloadTime:    10,
		TestCount:       20,
		DisableDownload: false,
		E2EEnabled:      true,
		E2EProtocol:     "vless",
		E2EHost:         host,
		E2EPort:         443,
		E2ETarget:       "http://www.gstatic.com/generate_204",
		E2EConcurrency:  16,
		E2ERetries:      3,
		E2EMaxLatency:   1000,
		E2ELimit:        100,
		PoolEnabled:     true,
		PoolLimit:       200,
		CoarseEnabled:   true,
		CoarseLimit:     50,
		CoarseTimeout:   1500,
		CountryFilter:   countryFilter,
	}

	start := time.Now()
	app.runTest(cfg)
	t.Logf("总耗时 %.0fs", time.Since(start).Seconds())
	app.mu.Lock()
	logs := append([]LogEntry(nil), app.logs...)
	app.mu.Unlock()
	if len(logs) > 40 {
		logs = logs[len(logs)-40:]
	}
	t.Logf("=== 日志（最近 %d 条）===", len(logs))
	for _, l := range logs {
		t.Logf("[%s] %s", l.Level, l.Message)
	}

	app.mu.Lock()
	results := append([]ResultItem(nil), app.results...)
	app.mu.Unlock()

	sort.SliceStable(results, func(i, j int) bool {
		ai, aj := results[i].E2E, results[j].E2E
		if ai != nil && aj != nil && ai.OK && aj.OK {
			return ai.Best < aj.Best
		}
		return results[i].Speed > results[j].Speed
	})

	t.Logf("=== 结果 %d 条（端到端上限 200ms，速度下限 5MB/s，国家=%v）===", len(results), countryFilter)
	t.Logf("%-18s %5s %9s %10s %6s %6s %10s %10s", "IP", "端口", "端到端", "速度MB/s", "地区", "国家", "<=200ms", ">=5MB/s")
	okCount := 0
	filtered := 0
	for _, r := range results {
		if r.Speed <= 0 {
			continue // 未测到/测速失败的不刷屏
		}
		e2e := "-"
		okE2E := false
		if r.E2E != nil && r.E2E.OK {
			e2e = fmt.Sprintf("%.0fms", r.E2E.Best)
			okE2E = r.E2E.Best <= 200
		}
		okSpeed := r.Speed >= 5
		mark := ""
		if okE2E && okSpeed {
			okCount++
			mark = "  ✓"
		}
		t.Logf("%-18s %5d %9s %10.2f %6s %6s%s", r.IP, r.Port, e2e, r.Speed, r.Colo, task.CountryOfColo(r.Colo), mark)
	}
	// 选了国家时，结果里所有能识别的机房都必须在筛选范围内。
	if len(countryFilter) > 0 {
		for _, r := range results {
			c := task.CountryOfColo(r.Colo)
			if c == "" {
				continue
			}
			allowed := false
			for _, f := range countryFilter {
				if strings.EqualFold(f, c) {
					allowed = true
				}
			}
			if !allowed {
				filtered++
				t.Errorf("%s 的地区 %s(%s) 不在筛选 %v 内", r.IP, r.Colo, c, countryFilter)
			}
		}
		if filtered == 0 {
			t.Logf("国家筛选校验通过：所有可识别结果都在 %v 内", countryFilter)
		}
	}
	t.Logf("同时满足端到端≤200ms 且速度≥5MB/s：%d 条", okCount)
}
