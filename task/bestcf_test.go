package task

import "testing"

func TestIsCIDRLine(t *testing.T) {
	cases := []struct {
		in   string
		want bool
	}{
		{"173.245.48.0/20", true},
		{"173.245.48.0/20 # Cloudflare", true},
		{"  103.21.244.0/22  ", true},
		{"2400:cb00::/32", true},
		// 普通 IP、域名、URL、非法掩码都应判否
		{"1.2.3.4", false},
		{"example.com", false},
		{"https://www.cloudflare.com/ips-v4", false},
		{"1.2.3.4/33", false},
		{"", false},
		{"# 173.245.48.0/20", false},
	}
	for _, c := range cases {
		if got := isCIDRLine(c.in); got != c.want {
			t.Errorf("isCIDRLine(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}

// 自定义源里的 CIDR 清单（如 ips-v4）必须能被展开成 IP。
// 修复前 parseSourceLine 会把这类行整行丢掉，等于白抓。
func TestCIDRRangesExpands(t *testing.T) {
	r := newIPRanges()
	r.port = 443
	// /20 应展开成 16 个 /24 各取一个
	r.addLine("173.245.48.0/20", 443)
	if len(r.ips) != 16 {
		t.Fatalf("/20 应展开成 16 个目标，实际 %d", len(r.ips))
	}
	for _, tgt := range r.ips {
		if tgt.Port != 443 {
			t.Errorf("端口应沿用默认值 443，实际 %d", tgt.Port)
		}
		if tgt.IP == nil || tgt.IP.IP == nil {
			t.Error("展开出了空 IP")
		}
	}
}

func TestShortURL(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"https://www.cloudflare.com/ips-v4", "www.cloudflare.com/ips-v4"},
		{"https://bestcf.pages.dev/domain/all.txt", "bestcf.pages.dev/domain/all.txt"},
		{"https://example.com/", "example.com"},
	}
	for _, c := range cases {
		if got := shortURL(c.in); got != c.want {
			t.Errorf("shortURL(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}
