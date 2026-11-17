package task

import (
	"math"
	"testing"
)

func TestStatsOf(t *testing.T) {
	avg, jitter := statsOf([]float64{100, 200, 300})
	if math.Abs(avg-200) > 0.01 {
		t.Fatalf("avg = %v, want 200", avg)
	}
	// 总体标准差 = sqrt(((100-200)² + 0 + (300-200)²) / 3) ≈ 81.65
	if math.Abs(jitter-81.65) > 0.1 {
		t.Fatalf("jitter = %v, want ~81.65", jitter)
	}
}

func TestStatsOfEdgeCases(t *testing.T) {
	if avg, jitter := statsOf(nil); avg != 0 || jitter != 0 {
		t.Fatalf("空样本应为 0/0，实际 %v/%v", avg, jitter)
	}
	// 单个样本标准差不该是 NaN 或除零
	avg, jitter := statsOf([]float64{42})
	if avg != 42 || jitter != 0 {
		t.Fatalf("单样本应为 42/0，实际 %v/%v", avg, jitter)
	}
}

func TestScoreOf(t *testing.T) {
	cases := []struct {
		name string
		r    StabilityResult
		want string
	}{
		{"全失败", StabilityResult{Success: 0, Attempts: 5}, "D"},
		{"成功率40%", StabilityResult{Success: 4, Attempts: 10, SuccessRate: 0.4}, "D"},
		{"成功率70%", StabilityResult{Success: 7, Attempts: 10, SuccessRate: 0.7, TLSAvg: 300}, "C"},
		{"成功率90%", StabilityResult{Success: 9, Attempts: 10, SuccessRate: 0.9, TLSAvg: 300}, "B"},
		{"成功率100%但TLS 1.5s", StabilityResult{Success: 10, Attempts: 10, SuccessRate: 1, TLSAvg: 1500}, "B"},
		{"成功率100%但TLS 2.5s", StabilityResult{Success: 10, Attempts: 10, SuccessRate: 1, TLSAvg: 2500}, "C"},
		{"完美", StabilityResult{Success: 10, Attempts: 10, SuccessRate: 1, TLSAvg: 400, TLSJitter: 50, TCPJitter: 10}, "S"},
		{"完美但抖动大", StabilityResult{Success: 10, Attempts: 10, SuccessRate: 1, TLSAvg: 400, TLSJitter: 500, TCPJitter: 10}, "A"},
		// 长连接断裂 = 下载必断，即便其他指标满分也要压到 C
		{"长连接断", StabilityResult{Success: 10, Attempts: 10, SuccessRate: 1, TLSAvg: 400, TLSJitter: 50, TCPJitter: 10, KeepAliveTested: true, KeepAliveOK: false}, "C"},
		// 没开长连接探测时不应该被误罚
		{"未测长连接", StabilityResult{Success: 10, Attempts: 10, SuccessRate: 1, TLSAvg: 400, TLSJitter: 50, TCPJitter: 10, KeepAliveTested: false}, "S"},
		// 样本不足时抖动恒为 0，不能据此判 S
		{"只成功1次", StabilityResult{Success: 1, Attempts: 1, SuccessRate: 1, TLSAvg: 400, TLSJitter: 0, TCPJitter: 0}, "A"},
		{"只成功2次", StabilityResult{Success: 2, Attempts: 2, SuccessRate: 1, TLSAvg: 400, TLSJitter: 0, TCPJitter: 0}, "A"},
		{"成功3次可判S", StabilityResult{Success: 3, Attempts: 3, SuccessRate: 1, TLSAvg: 400, TLSJitter: 50, TCPJitter: 10}, "S"},
	}
	for _, c := range cases {
		if got := scoreOf(&c.r); got != c.want {
			t.Errorf("%s: scoreOf = %s, want %s", c.name, got, c.want)
		}
	}
}

func TestStabilityConfigNormalize(t *testing.T) {
	c := StabilityConfig{}
	c.normalize()
	if c.Rounds != defaultStabilityRounds || c.Concurrency != defaultStabilityConcurrency {
		t.Fatalf("默认值未生效: %+v", c)
	}

	// 越界值要夹到上限，避免用户填 10000 轮把程序卡死
	c = StabilityConfig{Rounds: 10000, Concurrency: 999}
	c.normalize()
	if c.Rounds != 50 || c.Concurrency != 50 {
		t.Fatalf("上限未夹住: %+v", c)
	}
}

func TestStabilityHostFallback(t *testing.T) {
	// 指定域名时直接返回域名
	StabilityDomain = "fast2.example.com"
	t.Cleanup(func() { StabilityDomain = "" })
	if got := stabilityHost(); got != "fast2.example.com" {
		t.Fatalf("stabilityHost = %q", got)
	}
	if got := stabilityURL(); got != "https://fast2.example.com/" {
		t.Fatalf("stabilityURL = %q", got)
	}

	// 未指定时从默认测速 URL 里解析出主机名
	StabilityDomain = ""
	host := stabilityHost()
	if host == "" {
		t.Fatal("未指定域名时也应能从默认 URL 解析出主机名")
	}
}
