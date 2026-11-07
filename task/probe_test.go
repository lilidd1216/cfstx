package task

import "testing"

func TestNormalizeProbeDomain(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		// 正确写法原样保留
		{"fast3.example.com", "fast3.example.com"},
		// 浏览器地址栏直接复制的常见形态
		{"https://fast3.example.com/", "fast3.example.com"},
		{"https://fast3.example.com", "fast3.example.com"},
		{"http://fast3.example.com", "fast3.example.com"},
		{"https://fast3.example.com/admin?x=1", "fast3.example.com"},
		{"https://fast3.example.com//cdn-cgi/trace", "fast3.example.com"},
		// 带端口
		{"fast3.example.com:443", "fast3.example.com"},
		{"https://fast3.example.com:8443/path", "fast3.example.com"},
		{"[2606:4700::1]:443", "2606:4700::1"},
		// 大小写与空白
		{"  Fast3.Example.COM  ", "fast3.example.com"},
		{"\thttps://Fast3.Example.COM/\n", "fast3.example.com"},
		// 末尾点（FQDN 写法）
		{"fast3.example.com.", "fast3.example.com"},
		// 空与非域名
		{"", ""},
		{"   ", ""},
		{"https://", ""},
		{"https:///path", ""},
	}

	for _, c := range cases {
		if got := NormalizeProbeDomain(c.in); got != c.want {
			t.Errorf("NormalizeProbeDomain(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// 用户把带协议的地址粘进来时，规范化后必须能拼出合法 URL。
// 这是 v1.1.5 的回归 bug：https://https://xxx//cdn-cgi/trace 的 Host 会变成 "https"。
func TestNormalizedDomainFormsValidURL(t *testing.T) {
	inputs := []string{
		"https://fast3.example.com/",
		"http://fast3.example.com",
		"fast3.example.com/admin",
	}
	for _, in := range inputs {
		d := NormalizeProbeDomain(in)
		if d != "fast3.example.com" {
			t.Fatalf("NormalizeProbeDomain(%q) = %q", in, d)
		}
		raw := "https://" + d + probePath
		if raw != "https://fast3.example.com/cdn-cgi/trace" {
			t.Fatalf("拼接结果不对: %q", raw)
		}
	}
}
