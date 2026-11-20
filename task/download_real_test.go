package task

import (
	"net"
	"os"
	"testing"
	"time"
)

// TestDownloadSpeedReal 验证下载测速的单位与稳态口径，默认跳过。
//
//	CFSTX_DL=1 go test ./task -run 'TestDownloadSpeedReal|TestCoarseSpeedReal' -v
func TestDownloadSpeedReal(t *testing.T) {
	if os.Getenv("CFSTX_DL") != "1" {
		t.Skip("设置 CFSTX_DL=1 后运行真实下载验证")
	}
	oldURL, oldTimeout := URL, Timeout
	defer func() { URL, Timeout = oldURL, oldTimeout }()
	URL = "https://speed.cloudflare.com/__down?bytes=5000000"
	Timeout = 4 * time.Second

	tgt := &Target{IP: &net.IPAddr{IP: net.ParseIP("104.16.0.1")}, Port: 443}
	speed, peak, colo := downloadHandler(tgt)
	mbps := speed / 1024 / 1024
	peakMbps := peak / 1024 / 1024
	t.Logf("steady=%.2f MB/s peak=%.2f MB/s colo=%s", mbps, peakMbps, colo)
	if speed <= 0 {
		t.Fatalf("稳态速度应为正（单位或测速逻辑有问题）")
	}
	if mbps > 200 {
		t.Fatalf("速度异常高（可能单位错误）: %.2f MB/s", mbps)
	}
	if peakMbps < mbps*0.5 {
		t.Fatalf("峰值不应远低于稳态: peak=%.2f steady=%.2f", peakMbps, mbps)
	}
}

func TestCoarseSpeedReal(t *testing.T) {
	if os.Getenv("CFSTX_DL") != "1" {
		t.Skip("设置 CFSTX_DL=1 后运行真实粗排验证")
	}
	oldURL := URL
	defer func() { URL = oldURL }()
	URL = "https://speed.cloudflare.com/__down?bytes=5000000"

	targets := []*Target{
		{IP: &net.IPAddr{IP: net.ParseIP("104.16.0.1")}, Port: 443},
		{IP: &net.IPAddr{IP: net.ParseIP("104.17.0.1")}, Port: 443},
	}
	speeds := CoarseSpeed(targets, 1500*time.Millisecond, len(targets), func(done, total int, r CoarseResult) {
		t.Logf("[%d/%d] %s:%d %.2f MB/s", done, total, r.IP, r.Port, r.Speed)
	})
	ok := 0
	for k, v := range speeds {
		if v > 0 {
			ok++
		}
		t.Logf("%s => %.2f MB/s", k, v)
	}
	if ok == 0 {
		t.Fatalf("粗排没有一条成功")
	}
}
