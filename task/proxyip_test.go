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
		"not a host", // 含空格
		"localhost",  // 单段主机名
		"example",    // 单段主机名
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
		// 直连 Cloudflare 的：检测通过，但不是真中转，不能填 PROXYIP
		{Addr: "8.8.8.8:443", OK: true, Direct: true},
	}

	all := FormatProxyIPList(results, false)
	wantAll := "PROXYIP=1.2.3.4:443\nPROXYIP=5.6.7.8:8443\nPROXYIP=9.9.9.9:2053\nPROXYIP=8.8.8.8:443\n"
	if all != wantAll {
		t.Errorf("全部输出不对：\n got=%q\nwant=%q", all, wantAll)
	}

	// onlyOK 要同时排掉「不可用」和「直连 Cloudflare」两类
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
		// 直连 Cloudflare：哪怕更快，也要排在真中转后面
		{Addr: "direct.example:443", OK: true, Direct: true, TLSDelay: 10, HTTPDelay: 10},
		{Addr: "pending.example:443", Uncertain: true},
	}
	sortProxyIPResults(rs)

	wantOrder := []string{
		"fast.example:443",     // 真中转，最快
		"slow.example:443",     // 真中转，较慢
		"direct.example:443",   // 直连，即使 20ms 也排在真中转之后
		"pending.example:443",  // 本机无法验证
		"dead.example:443",     // 不可用
	}
	for i, want := range wantOrder {
		if rs[i].Addr != want {
			t.Errorf("第 %d 位应为 %s，实际 %s", i+1, want, rs[i].Addr)
		}
	}
}

func TestSameIP(t *testing.T) {
	cases := []struct {
		a, b string
		want bool
	}{
		{"1.2.3.4", "1.2.3.4", true},
		{"1.2.3.4", "1.2.3.5", false},
		{"  1.2.3.4  ", "1.2.3.4", true}, // trace 偶有空白
		{"2606:4700::99", "2606:4700::99", true},
		{"2606:4700::99", "2606:4700::100", false},
		// IPv4-mapped IPv6 与 IPv4 是同一个地址
		{"::ffff:1.2.3.4", "1.2.3.4", true},
		// v4 和 v6 不能相等
		{"1.2.3.4", "2606:4700::99", false},
		// 空值和非法值一律判否，避免把「取不到」误当成匹配
		{"", "1.2.3.4", false},
		{"1.2.3.4", "", false},
		{"", "", false},
		{"not-an-ip", "not-an-ip", false},
	}
	for _, c := range cases {
		if got := sameIP(c.a, c.b); got != c.want {
			t.Errorf("sameIP(%q, %q) = %v，应为 %v", c.a, c.b, got, c.want)
		}
	}
}

func TestParseTraceIPField(t *testing.T) {
	cases := []struct {
		name string
		body string
		want string
	}{
		{"正常", "fl=1\nip=1.2.3.4\nts=1\n", "1.2.3.4"},
		{"带空白", "  ip=  5.6.7.8  \n", "5.6.7.8"},
		{"IPv6", "ip=2606:4700::99\n", "2606:4700::99"},
		{"没有 ip 字段", "colo=SJC\nloc=US\n", ""},
		{"空响应", "", ""},
		// 前缀相同但字段名不同的不能被误认
		{"相似字段", "ipv6=1.2.3.4\nipfoo=x\n", ""},
	}
	for _, c := range cases {
		if got := parseTraceIPField([]byte(c.body)); got != c.want {
			t.Errorf("%s: parseTraceIPField = %q，应为 %q", c.name, got, c.want)
		}
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

// Xgonce 的 CSV 格式：IP,cf-meta-ip,端口,速度(Mbps),CF归属国,机房,TCP延迟,TLS延迟
func TestParseCSVCandidates(t *testing.T) {
	csv := `IP,cf-meta-ip,端口,速度(Mbps),CF归属国,机房,TCP延迟(ms),TLS延迟(ms)
85.192.42.198,85.192.42.198,443,4.11,AT,FRA,162.47,173.93
43.169.18.179,43.168.103.148,443,21.14,BR,GRU,152.45,164.73
35.182.59.30,35.182.59.30,2087,109.15,CA,YUL,100.9,81.38
85.192.42.198,85.192.42.198,443,4.11,AT,FRA,162.47,173.93`

	got := ParseSourceBody([]byte(csv), "csv")

	if len(got) != 3 {
		t.Fatalf("应解析出 3 条（表头跳过、重复去掉），实际 %d 条：%+v", len(got), got)
	}
	if got[0].Host != "85.192.42.198" || got[0].Port != 443 {
		t.Errorf("第 1 条 = %+v", got[0])
	}
	if !strings.Contains(got[0].Note, "FRA") {
		t.Errorf("备注应包含机房 FRA，实际 %q", got[0].Note)
	}
	if got[2].Port != 2087 {
		t.Errorf("第 3 条端口应为 2087，实际 %d", got[2].Port)
	}
}

// Mingyu 的纯 IP 格式
func TestParseIPOnlyCandidates(t *testing.T) {
	text := `8.222.219.140
47.57.245.232
# 这是注释
8.218.70.238
8.222.219.140
2606:4700::1`

	got := ParseSourceBody([]byte(text), "iponly")

	if len(got) != 4 {
		t.Fatalf("应解析出 4 条（去重、跳注释），实际 %d 条：%+v", len(got), got)
	}
	if got[0].Port != 443 {
		t.Errorf("默认端口应为 443，实际 %d", got[0].Port)
	}
	if got[3].Host != "2606:4700::1" {
		t.Errorf("IPv6 应保留，实际 %+v", got[3])
	}
}

// 三个源的格式不同，ParseSourceBody 要能分别处理
func TestParseSourceBodyDispatch(t *testing.T) {
	lineText := "1.2.3.4:8443#HKG 39.84MB/s\n5.6.7.8"
	if got := ParseSourceBody([]byte(lineText), "line"); len(got) != 2 {
		t.Fatalf("line 格式应解析出 2 条，实际 %d", len(got))
	}
	if got := ParseSourceBody([]byte(lineText), ""); len(got) != 2 {
		t.Fatalf("未指定格式时应走默认解析，实际 %d 条", len(got))
	}
}
