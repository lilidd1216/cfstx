package main

import (
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
