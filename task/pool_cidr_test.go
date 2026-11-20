package task

import (
	"path/filepath"
	"testing"
)

func TestCIDRWeight(t *testing.T) {
	cases := []struct {
		cidr string
		want float64
	}{
		{"104.16.0.0/13", 0.90},
		{"104.24.0.0/14", 0.95},
		{"103.22.200.0/22", 0},
		{"9.9.9.0/24", defaultCidrWeight},
	}
	for _, c := range cases {
		if got := CIDRWeight(c.cidr); got != c.want {
			t.Fatalf("%s 权重应为 %v，得到 %v", c.cidr, c.want, got)
		}
	}
}

func TestSampleCIDRTargets(t *testing.T) {
	InitRandSeed()
	ts := sampleCIDRTargets("104.16.0.0/16", 5, 443)
	if len(ts) != 5 {
		t.Fatalf("应取 5 个，得到 %d", len(ts))
	}
	seen := map[string]bool{}
	for _, tgt := range ts {
		if tgt.IP == nil || tgt.Port != 443 {
			t.Fatalf("目标无效: %+v", tgt)
		}
		seen[tgt.IP.String()] = true
	}
	if len(seen) != 5 {
		t.Fatalf("样本应互不重复，实际 %d", len(seen))
	}
}

func TestExpandCIDRsWeighted(t *testing.T) {
	InitRandSeed()
	targets := expandCIDRsWeighted([]string{
		"104.16.0.0/13",
		"103.22.200.0/22", // 权重 0，仍保留 1 个探针
	}, 443)
	if len(targets) == 0 {
		t.Fatalf("应生成目标")
	}
	// 权重 0 的段至少有一个
	foundZero := false
	for _, tgt := range targets {
		if tgt.IP.IP.To4() != nil && tgt.IP.IP.To4()[0] == 103 {
			foundZero = true
		}
	}
	if !foundZero {
		t.Fatalf("权重 0 的网段应保留探针")
	}
}

func TestIPPool(t *testing.T) {
	p := NewIPPool()
	p.MergeResults([]E2EResult{
		{IP: "1.2.3.4", Port: 443, OK: true, Best: 120},
		{IP: "5.6.7.8", Port: 443, OK: false, Best: 0},
	})
	if p.Count() != 1 {
		t.Fatalf("成功过的条目应为 1，得到 %d", p.Count())
	}
	targets := p.Targets(10)
	if len(targets) != 1 || targets[0].IP.String() != "1.2.3.4" || targets[0].Port != 443 {
		t.Fatalf("Targets 结果错误: %+v", targets)
	}

	// 再成功一次，成功次数应累加
	p.MergeResults([]E2EResult{{IP: "1.2.3.4", Port: 443, OK: true, Best: 100}})
	if p.Entries[0].Success != 2 || p.Entries[0].Best != 100 {
		t.Fatalf("合并统计错误: %+v", p.Entries[0])
	}

	path := filepath.Join(t.TempDir(), "ip_pool.json")
	if err := SaveIPPool(path, p); err != nil {
		t.Fatal(err)
	}
	q := LoadIPPool(path)
	if q == nil || q.Count() != 1 {
		t.Fatalf("读取池失败: %+v", q)
	}
}
