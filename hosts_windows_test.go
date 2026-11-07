package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestStripCFSTXBlock(t *testing.T) {
	cases := []struct{ name, in, want string }{
		{
			name: "无区块时原样保留",
			in:   "127.0.0.1 localhost\n192.168.1.1 router",
			want: "127.0.0.1 localhost\n192.168.1.1 router",
		},
		{
			name: "删除区块",
			in:   "127.0.0.1 localhost\n" + hostsBeginMarker + "\n1.2.3.4 example.com\n" + hostsEndMarker,
			want: "127.0.0.1 localhost",
		},
		{
			name: "删除区块并保留其后内容",
			in:   "a\n" + hostsBeginMarker + "\nb\n" + hostsEndMarker + "\nc",
			want: "a\nc",
		},
		{
			name: "只有开始标记（文件损坏）时容错",
			in:   "a\n" + hostsBeginMarker + "\nb",
			want: "a",
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := stripCFSTXBlock(c.in); got != c.want {
				t.Fatalf("got %q, want %q", got, c.want)
			}
		})
	}
}

func TestReadHostsEntries(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "hosts")
	content := strings.Join([]string{
		"127.0.0.1 localhost",
		hostsBeginMarker,
		"104.16.0.1 example.com",
		"104.17.0.1  example2.com",
		"# 这是块内注释，应被忽略",
		hostsEndMarker,
		"192.168.1.1 router",
	}, "\n")
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	entries, err := readHostsEntries(p)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 {
		t.Fatalf("期望 2 条，实际 %d 条：%+v", len(entries), entries)
	}
	if entries[0].Domain != "example.com" || entries[0].IP != "104.16.0.1" {
		t.Fatalf("第一条解析错误：%+v", entries[0])
	}
	if entries[1].Domain != "example2.com" || entries[1].IP != "104.17.0.1" {
		t.Fatalf("第二条解析错误：%+v", entries[1])
	}
}

func TestHostsBlockRoundTrip(t *testing.T) {
	// 模拟「写入 → 读取 → 再写入」的完整流程，验证幂等
	original := "127.0.0.1 localhost\n"
	body := stripCFSTXBlock(original)

	build := func(ip, domain string) string {
		return strings.TrimRight(body, "\r\n") + "\r\n\r\n" +
			hostsBeginMarker + "\r\n" + ip + " " + domain + "\r\n" + hostsEndMarker + "\r\n"
	}

	first := build("104.16.0.1", "example.com")
	second := build("104.17.0.1", "example.com")

	// 第二次写入前先剥离旧区块，应回到原始正文（忽略行尾 CR/LF）
	if got := strings.TrimSpace(stripCFSTXBlock(first)); got != strings.TrimSpace(original) {
		t.Fatalf("剥离后正文不一致：%q", got)
	}

	dir := t.TempDir()
	p := filepath.Join(dir, "hosts")
	if err := os.WriteFile(p, []byte(second), 0o644); err != nil {
		t.Fatal(err)
	}
	entries, err := readHostsEntries(p)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].IP != "104.17.0.1" {
		t.Fatalf("覆盖写入后应只剩新条目，实际：%+v", entries)
	}
}

// hosts 里没有 CFSTX 区块时必须返回空切片而不是 nil。
// nil 会被 encoding/json 序列化成 null，前端读 entries.length 直接抛
// TypeError，整个 Hosts 卡片会被 Vue 卸载 —— v1.1.4 ~ v1.2.0 的
// 「优化工具里看不到 Hosts 功能」就是这个问题。
func TestReadHostsEntriesReturnsEmptySlice(t *testing.T) {
	f := filepath.Join(t.TempDir(), "hosts")
	if err := os.WriteFile(f, []byte("127.0.0.1 localhost\n::1 localhost\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	entries, err := readHostsEntries(f)
	if err != nil {
		t.Fatalf("读取失败: %v", err)
	}
	if entries == nil {
		t.Fatal("应返回空切片而不是 nil")
	}
	if len(entries) != 0 {
		t.Errorf("期望 0 条，实际 %d 条", len(entries))
	}
}

// 读不到 hosts 文件时同样不能返回 nil。
func TestReadHostsEntriesMissingFile(t *testing.T) {
	entries, err := readHostsEntries(filepath.Join(t.TempDir(), "not-exist"))
	if err == nil {
		t.Error("文件不存在时应返回错误")
	}
	_ = entries
}

// 直接验证 GET /api/hosts 的 JSON 里 entries 是 [] 而不是 null。
func TestHostsStatusJSONHasNoNull(t *testing.T) {
	raw, err := json.Marshal(NewApp().GetHostsStatus())
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), `"entries":null`) {
		t.Fatalf(`JSON 里出现了 "entries":null，前端会崩：%s`, raw)
	}
	if !strings.Contains(string(raw), `"entries":[`) {
		t.Fatalf(`JSON 里 entries 应为数组：%s`, raw)
	}
}

// 后端必须能自己清理用户从地址栏粘来的完整网址。
// 前端失焦时会清理一次，但用户可能直接点按钮，后端不能只依赖前端。
func TestValidateHostsInput(t *testing.T) {
	cases := []struct {
		name       string
		domain     string
		ip         string
		wantDomain string
		wantIP     string
		wantErr    string
	}{
		{"标准写法", "fast3.example.com", "1.2.3.4", "fast3.example.com", "1.2.3.4", ""},
		{"带协议和斜杠", "https://fast3.example.com/", "1.2.3.4", "fast3.example.com", "1.2.3.4", ""},
		{"带路径和查询", "https://Fast3.Example.COM/admin?x=1", "1.2.3.4", "fast3.example.com", "1.2.3.4", ""},
		{"带端口", "fast3.example.com:8443", "1.2.3.4", "fast3.example.com", "1.2.3.4", ""},
		{"前后空格", "  fast3.example.com  ", "1.2.3.4", "fast3.example.com", "1.2.3.4", ""},
		{"空域名", "", "1.2.3.4", "", "", "请填写域名"},
		{"只有协议", "https://", "1.2.3.4", "", "", "请填写域名"},
		{"空 IP", "fast3.example.com", "", "", "", "请选择一个 IP"},
		{"非法 IP", "fast3.example.com", "999.1.1.1", "", "", "IP 格式不正确"},
		{"双点域名", "a..b", "1.2.3.4", "", "", "域名格式不正确"},
		{"IPv6 地址", "fast3.example.com", "2606:4700::1", "fast3.example.com", "2606:4700::1", ""},
		// 443 端口可以照用，端口会被去掉
		{"IPv4 带 443 端口", "fast3.example.com", "1.2.3.4:443", "fast3.example.com", "1.2.3.4", ""},
		{"IPv6 带 443 端口", "fast3.example.com", "[2606:4700::1]:443", "fast3.example.com", "2606:4700::1", ""},
		{"IPv6 带方括号", "fast3.example.com", "[2606:4700::1]", "fast3.example.com", "2606:4700::1", ""},
		// 非 443 端口必须拒绝：hosts 不能指定端口，写进去连不上
		{"IPv4 非 443 端口", "fast3.example.com", "1.2.3.4:10001", "", "", "端口不能用"},
		{"IPv6 非 443 端口", "fast3.example.com", "[2606:4700::1]:8443", "", "", "端口不能用"},
		// TXT 导出格式：IP:端口#备注 速度
		{"TXT 格式 443", "fast3.example.com", "52.74.51.158:443#SIN 34.58MB/s", "fast3.example.com", "52.74.51.158", ""},
		{"TXT 格式 非443", "fast3.example.com", "27.44.142.135:10001#HKG 39.84MB/s", "", "", "端口不能用"},
		{"带备注无端口", "fast3.example.com", "104.16.0.1#HKG", "fast3.example.com", "104.16.0.1", ""},
		// hosts 不支持通配符，必须逐个写全
		{"通配符域名", "*.lds1216.ccwu.cc", "1.2.3.4", "", "", "不支持通配符"},
		// 多个域名：换行 / 逗号 / 分号 / 空格都支持
		{"换行分隔", "a.example.com\nb.example.com", "1.2.3.4", "a.example.com,b.example.com", "1.2.3.4", ""},
		{"逗号分隔", "a.example.com,b.example.com", "1.2.3.4", "a.example.com,b.example.com", "1.2.3.4", ""},
		{"分号分隔", "a.example.com; b.example.com", "1.2.3.4", "a.example.com,b.example.com", "1.2.3.4", ""},
		{"多域名去重", "a.example.com a.example.com\nA.EXAMPLE.COM", "1.2.3.4", "a.example.com", "1.2.3.4", ""},
		{"多域名中有一个通配符", "a.example.com\n*.b.example.com", "1.2.3.4", "", "", "不支持通配符"},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			gotDomains, gotIP, err := validateHostsInput(c.domain, c.ip)
			if c.wantErr != "" {
				if err == nil {
					t.Fatalf("期望报错 %q，实际通过（domains=%v ip=%q）", c.wantErr, gotDomains, gotIP)
				}
				if !strings.Contains(err.Error(), c.wantErr) {
					t.Errorf("错误信息 = %q，期望包含 %q", err.Error(), c.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("意外报错: %v", err)
			}
			gotDomain := strings.Join(gotDomains, ",")
			if gotDomain != c.wantDomain {
				t.Errorf("domains = %q，期望 %q", gotDomain, c.wantDomain)
			}
			if gotIP != c.wantIP {
				t.Errorf("ip = %q，期望 %q", gotIP, c.wantIP)
			}
		})
	}
}

// 多个域名要写成多条记录，一行一个，都指向同一个 IP。
func TestBuildHostsBlock(t *testing.T) {
	got := buildHostsBlock([]string{"a.example.com", "b.example.com"}, "1.2.3.4")
	want := hostsBeginMarker + "\r\n" +
		"1.2.3.4 a.example.com\r\n" +
		"1.2.3.4 b.example.com\r\n" +
		hostsEndMarker + "\r\n"
	if got != want {
		t.Errorf("生成内容不对：\n got=%q\nwant=%q", got, want)
	}

	// 单个域名
	got = buildHostsBlock([]string{"fast3.example.com"}, "104.16.0.1")
	want = hostsBeginMarker + "\r\n104.16.0.1 fast3.example.com\r\n" + hostsEndMarker + "\r\n"
	if got != want {
		t.Errorf("单域名生成内容不对：\n got=%q\nwant=%q", got, want)
	}
}

// 写入的区块能被 readHostsEntries 正确读回来 —— 覆盖「应用 → 展示」的往返。
func TestHostsBlockMultiDomainRoundTrip(t *testing.T) {
	block := buildHostsBlock([]string{"a.example.com", "b.example.com"}, "1.2.3.4")
	path := filepath.Join(t.TempDir(), "hosts")
	if err := os.WriteFile(path, []byte("127.0.0.1 localhost\r\n\r\n"+block), 0o644); err != nil {
		t.Fatal(err)
	}

	entries, err := readHostsEntries(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 {
		t.Fatalf("应读回 2 条，实际 %d 条：%+v", len(entries), entries)
	}
	for _, e := range entries {
		if e.IP != "1.2.3.4" {
			t.Errorf("IP = %q", e.IP)
		}
	}
	if entries[0].Domain != "a.example.com" || entries[1].Domain != "b.example.com" {
		t.Errorf("域名顺序不对：%+v", entries)
	}
}
