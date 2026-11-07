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
