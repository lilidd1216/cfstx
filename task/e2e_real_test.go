package task

import (
	"net"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"
)

// TestE2EReal 是真实环境回归，默认跳过。
//
//	CFSTX_E2E=1 \
//	CFSTX_E2E_HOST=fast2.example.com \
//	CFSTX_E2E_UUID=... \
//	CFSTX_E2E_PATH="/proxyip=..." \
//	CFSTX_E2E_IPS="1.2.3.4:443,5.6.7.8:2053" \
//	go test ./task -run TestE2EReal -v
//
// 断言只做「至少一条成功、数值在合理范围」，不把凭据写进源码或日志。
func TestE2EReal(t *testing.T) {
	if os.Getenv("CFSTX_E2E") != "1" {
		t.Skip("设置 CFSTX_E2E=1 后运行真实环境回归")
	}
	host := os.Getenv("CFSTX_E2E_HOST")
	uuid := os.Getenv("CFSTX_E2E_UUID")
	path := os.Getenv("CFSTX_E2E_PATH")
	if host == "" || uuid == "" {
		t.Skip("缺少 CFSTX_E2E_HOST / CFSTX_E2E_UUID")
	}
	raw := os.Getenv("CFSTX_E2E_IPS")
	if raw == "" {
		t.Skip("缺少 CFSTX_E2E_IPS")
	}

	var targets []*Target
	for _, item := range strings.Split(raw, ",") {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		hostPart, portPart, err := net.SplitHostPort(item)
		if err != nil {
			hostPart, portPart = item, "443"
		}
		port, _ := strconv.Atoi(portPart)
		ip := net.ParseIP(hostPart)
		if ip == nil {
			continue
		}
		targets = append(targets, &Target{IP: &net.IPAddr{IP: ip}, Port: port})
	}

	cfg := E2EConfig{
		Protocol:       "vless",
		Host:           host,
		UUID:           uuid,
		WSPath:         path,
		TargetURL:      os.Getenv("CFSTX_E2E_TARGET"),
		Concurrency:    8,
		Attempts:       2,
		AttemptTimeout: 8 * time.Second,
	}
	if cfg.TargetURL == "" {
		cfg.TargetURL = "http://www.gstatic.com/generate_204"
	}
	results := ProbeE2E(targets, cfg, func(done, total, passed int, r *E2EResult) {
		t.Logf("[%d/%d] %s:%d ok=%v best=%.0fms cold=%.0fms warm=%.0fms colo=%s status=%s",
			done, total, r.IP, r.Port, r.OK, r.Best, r.Cold, r.Warm, r.Colo, r.Status)
	})
	ok, withColo := 0, 0
	for _, r := range results {
		if r.OK {
			ok++
			if r.Colo != "" {
				withColo++
			}
			if r.Best <= 0 || r.Best > 10000 {
				t.Errorf("%s:%d 的 Best 不合理: %.0f", r.IP, r.Port, r.Best)
			}
		}
	}
	if ok == 0 {
		t.Fatalf("没有一条成功，检查 host/uuid/path/网络")
	}
	// colo 来自目标响应头；gstatic 等非 CF 目标可能没有，属于正常情况，
	// app 会回退到 L1 HTTPing 的 colo，所以这里只记录不失败。
	t.Logf("成功 %d 条，其中 %d 条解析出机房 colo", ok, withColo)
}
