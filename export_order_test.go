package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestOrderResults(t *testing.T) {
	results := []ResultItem{
		{IP: "1.1.1.1", Port: 443},
		{IP: "2.2.2.2", Port: 443},
		{IP: "3.3.3.3", Port: 443},
	}
	got := orderResults(results, []string{"3.3.3.3:443", "1.1.1.1:443"})
	want := []string{"3.3.3.3", "1.1.1.1", "2.2.2.2"}
	for i := range want {
		if got[i].IP != want[i] {
			t.Fatalf("第 %d 个应为 %s，得到 %s", i, want[i], got[i].IP)
		}
	}

	// 空 order 保持原顺序。
	plain := orderResults(results, nil)
	for i := range results {
		if plain[i].IP != results[i].IP {
			t.Fatalf("空 order 不应改变顺序")
		}
	}
}

func TestExportTXTChineseColoAndOrder(t *testing.T) {
	app := NewApp()
	app.results = []ResultItem{
		{IP: "1.1.1.1", Port: 443, Colo: "LAX", Speed: 3.2},
		{IP: "2.2.2.2", Port: 443, Colo: "HKG", Speed: 6.5},
	}
	path, err := app.ExportTXT(filepath.Join(t.TempDir(), "result.txt"),
		[]string{"2.2.2.2:443", "1.1.1.1:443"})
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	if !strings.Contains(text, "2.2.2.2:443#香港 6.50MB/s") {
		t.Fatalf("TXT 应使用中文地区并保持导出顺序，实际:\n%s", text)
	}
	if !strings.Contains(text, "1.1.1.1:443#洛杉矶 3.20MB/s") {
		t.Fatalf("TXT 应使用中文地区，实际:\n%s", text)
	}
	if strings.Index(text, "2.2.2.2") > strings.Index(text, "1.1.1.1") {
		t.Fatalf("导出顺序应与 order 一致，实际:\n%s", text)
	}
}

func TestSetResultsEnablesExport(t *testing.T) {
	app := NewApp()
	app.SetResults([]ResultItem{{IP: "1.2.3.4", Port: 443, Colo: "HKG", Speed: 5}})
	path, err := app.ExportTXT(filepath.Join(t.TempDir(), "r.txt"), nil)
	if err != nil {
		t.Fatalf("载入历史后应可导出: %v", err)
	}
	data, _ := os.ReadFile(path)
	if !strings.Contains(string(data), "1.2.3.4:443#香港 5.00MB/s") {
		t.Fatalf("导出内容错误: %s", data)
	}
}
