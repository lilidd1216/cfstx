package task

import (
	"net"
	"strings"
	"testing"
)

func TestParseProxyIPCandidates(t *testing.T) {
	text := strings.Join([]string{
		"1.2.3.4",
		"5.6.7.8:8443",
		"proxyip.example.com",
		"proxyip.example.com:2053",
		"https://proxyip.example.com/path",
		"2606:4700::1",
		"[2606:4700::2]:2096",
		"9.9.9.9#🇺🇸US←US-16ms-200.92MB/s", // Xiaobei09 列表的真实格式
		"# 整行注释，跳过",
		"",
		"   ",
		"1.2.3.4",       // 重复，应去重
		"not a host",    // 非法，跳过
		"1.2.3.4:99999", // 端口越界，跳过
	}, "\n")

	got := ParseProxyIPCandidates(text)

	want := []struct {
		host string
		port int
		note string
	}{
		{"1.2.3.4", 443, ""},
		{"5.6.7.8", 8443, ""},
		{"proxyip.example.com", 443, ""},
		{"proxyip.example.com", 2053, ""},
		{"2606:4700::1", 443, ""},
		{"2606:4700::2", 2096, ""},
		{"9.9.9.9", 443, "🇺🇸US←US-16ms-200.92MB/s"},
	}

	if len(got) != len(want) {
		t.Fatalf("解析出 %d 条，期望 %d 条：%+v", len(got), len(want), got)
	}
	for i, w := range want {
		if got[i].Host != w.host || got[i].Port != w.port || got[i].Note != w.note {
			t.Errorf("第 %d 条 = %+v，期望 host=%s port=%d note=%q",
				i, got[i], w.host, w.port, w.note)
		}
	}
}

func TestParseProxyIPCandidatesDedup(t *testing.T) {
	// 同一个地址写两种形式（带不带默认端口）应视为重复
	text := "1.2.3.4\n1.2.3.4:443\n1.2.3.4#注释"
	got := ParseProxyIPCandidates(text)
	if len(got) != 1 {
		t.Fatalf("去重后应为 1 条，实际 %d 条：%+v", len(got), got)
	}
}

func TestSplitCandidateRejectsInvalid(t *testing.T) {
	bad := []string{
		"",
		"   ",
		"https://",
		"1.2.3.4:0",
		"1.2.3.4:70000",
		"1.2.3.4:abc",
		"[2606:4700::1",
		"bad_host_name!.com",
		"-leading-hyphen.com",
	}
	for _, s := range bad {
		if _, _, ok := splitCandidate(s); ok {
			t.Errorf("splitCandidate(%q) 应该失败，却通过了", s)
		}
	}
}

func TestIsCloudflareOwnedIP(t *testing.T) {
	cases := []struct {
		ip   string
		want bool
	}{
		{"104.16.0.1", true},     // 官方边缘
		{"104.23.255.255", true}, // 104.16.0.0/13 的最后一段
		{"104.24.0.1", true},     // 104.24.0.0/14 起始
		{"104.31.16.181", false}, // 104.31.x.x 不在 CF 官方公布段内（第三方 VPS）
		{"172.64.0.1", true},
		{"162.158.1.1", true},
		{"2606:4700::1", true}, // IPv6 段
		{"2400:cb00::1", true},
		{"165.22.137.198", false}, // DigitalOcean，可用 ProxyIP
		{"68.68.32.120", false},
		{"8.8.8.8", false},
		{"", false},
	}
	for _, c := range cases {
		ip := net.ParseIP(c.ip)
		if got := IsCloudflareOwnedIP(ip); got != c.want {
			t.Errorf("IsCloudflareOwnedIP(%q) = %v, want %v", c.ip, got, c.want)
		}
	}
}

func TestProxyIPCandidateAddr(t *testing.T) {
	cases := []struct {
		c    ProxyIPCandidate
		want string
	}{
		{ProxyIPCandidate{Host: "1.2.3.4", Port: 0}, "1.2.3.4:443"},
		{ProxyIPCandidate{Host: "1.2.3.4", Port: 8443}, "1.2.3.4:8443"},
		{ProxyIPCandidate{Host: "2606:4700::1", Port: 443}, "[2606:4700::1]:443"},
	}
	for _, c := range cases {
		if got := c.c.Addr(); got != c.want {
			t.Errorf("Addr() = %q, want %q", got, c.want)
		}
	}
}

func TestFormatProxyIPList(t *testing.T) {
	results := []ProxyIPResult{
		{Addr: "1.2.3.4:443", OK: true},
		{Addr: "5.6.7.8:8443", OK: false},
		{Addr: "9.9.9.9:2053", OK: true},
	}

	all := FormatProxyIPList(results, false)
	wantAll := "PROXYIP=1.2.3.4:443\nPROXYIP=5.6.7.8:8443\nPROXYIP=9.9.9.9:2053\n"
	if all != wantAll {
		t.Errorf("全部输出不对：\n got=%q\nwant=%q", all, wantAll)
	}

	onlyOK := FormatProxyIPList(results, true)
	wantOK := "PROXYIP=1.2.3.4:443\nPROXYIP=9.9.9.9:2053\n"
	if onlyOK != wantOK {
		t.Errorf("仅可用输出不对：\n got=%q\nwant=%q", onlyOK, wantOK)
	}
}

func TestSortProxyIPResults(t *testing.T) {
	rs := []ProxyIPResult{
		{Addr: "slow.example:443", OK: true, TLSDelay: 800, HTTPDelay: 400},
		{Addr: "dead.example:443", OK: false},
		{Addr: "fast.example:443", OK: true, TLSDelay: 200, HTTPDelay: 100},
	}
	sortProxyIPResults(rs)

	if !rs[0].OK || rs[0].Addr != "fast.example:443" {
		t.Errorf("第一名应是最快的可用项，实际 %+v", rs[0])
	}
	if !rs[1].OK || rs[1].Addr != "slow.example:443" {
		t.Errorf("第二名应是较慢的可用项，实际 %+v", rs[1])
	}
	if rs[2].OK {
		t.Errorf("不可用的应排最后，实际 %+v", rs[2])
	}
}

func TestParseProxyIPTrace(t *testing.T) {
	body := []byte("fl=1\nh=speed.cloudflare.com\nip=2606:4700::99\nts=1\ncolo=SJC\nloc=US\n")
	var r ProxyIPResult
	parseProxyIPTrace(body, &r)

	if r.ExitIP != "2606:4700::99" {
		t.Errorf("ExitIP = %q", r.ExitIP)
	}
	if !r.ExitIPv6 {
		t.Error("ExitIPv6 应为 true")
	}
	if r.ExitColo != "SJC" || r.ExitLoc != "US" {
		t.Errorf("colo/loc 解析错误：%+v", r)
	}

	// 缺 ip 字段应判为失败
	var bad ProxyIPResult
	parseProxyIPTrace([]byte("colo=SJC\n"), &bad)
	if bad.OK {
		t.Error("缺少 ip 字段时不应该算成功")
	}
}
