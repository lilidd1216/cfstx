package task

import "testing"

func TestCountryOfColo(t *testing.T) {
	cases := []struct {
		colo string
		want string
	}{
		{"HKG", "HK"},
		{"hkg", "HK"},
		{" NRT ", "JP"},
		{"TPE", "TW"},
		{"SJC", "US"},
		{"FRA", "DE"},
		{"LHR", "GB"},
		{"N/A", ""},
		{"", ""},
		{"XYZ", ""},
		{"US", "US"}, // 兼容直接传国家代码
	}
	for _, c := range cases {
		if got := CountryOfColo(c.colo); got != c.want {
			t.Fatalf("CountryOfColo(%q) = %q，期望 %q", c.colo, got, c.want)
		}
	}
}

func TestNormalizeCountryFilter(t *testing.T) {
	got := NormalizeCountryFilter([]string{"hk", "JP", "HK", "zz", "", "us"})
	want := []string{"HK", "JP", "US"}
	if len(got) != len(want) {
		t.Fatalf("期望 %v，得到 %v", want, got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("期望 %v，得到 %v", want, got)
		}
	}
	if NormalizeCountryFilter(nil) != nil {
		t.Fatalf("空输入应返回 nil")
	}
	if NormalizeCountryFilter([]string{"zz"}) != nil {
		t.Fatalf("全部非法时应返回 nil")
	}
}

func TestMatchCountry(t *testing.T) {
	cases := []struct {
		colo   string
		filter []string
		want   bool
	}{
		{"NRT", nil, true},          // 不筛选
		{"NRT", []string{"JP"}, true},
		{"HKG", []string{"JP"}, false},
		{"", []string{"JP"}, true},           // 无法判断不误杀
		{"XYZ", []string{"JP"}, true},        // 未收录不误杀
		{"SJC", []string{"JP", "US"}, true},  // 多选
	}
	for _, c := range cases {
		if got := MatchCountry(c.colo, c.filter); got != c.want {
			t.Fatalf("MatchCountry(%q, %v) = %v，期望 %v", c.colo, c.filter, got, c.want)
		}
	}
}

func TestParseE2EColo(t *testing.T) {
	cases := []struct {
		body string
		want string
	}{
		{"HTTP/1.1 204 No Content\r\ncf-ray: 7bd32409eda7b020-HKG\r\n\r\n", "HKG"},
		{"HTTP/1.1 200 OK\r\nCF-Ray: abc-SJC\r\n\r\n", "SJC"},
		{"HTTP/1.1 204 No Content\r\nserver: cloudflare\r\n\r\n", ""},
		{"HTTP/1.1 204 No Content\r\ncf-ray: no-dash-here\r\n\r\n", ""},
	}
	for _, c := range cases {
		if got := parseE2EColo([]byte(c.body)); got != c.want {
			t.Fatalf("parseE2EColo(%q) = %q，期望 %q", c.body, got, c.want)
		}
	}
}
