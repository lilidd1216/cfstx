package task

import (
	"net/url"
	"os/exec"
	"strings"
)

// systemProxyURL 读取 Windows 系统代理（「设置 → 网络 → 代理」里配置的地址）。
// Go 的 http 包默认只认 HTTP_PROXY/HTTPS_PROXY 环境变量，不会读这里的设置，
// 所以在线抓取时需要单独读取一次。
//
// 未启用、读取失败或格式无法识别时返回 nil。
func systemProxyURL() *url.URL {
	out, err := exec.Command("reg", "query",
		`HKCU\Software\Microsoft\Windows\CurrentVersion\Internet Settings`).Output()
	if err != nil {
		return nil
	}
	text := string(out)

	if proxyValue(text, "ProxyEnable") != "0x1" {
		return nil
	}

	server := proxyValue(text, "ProxyServer")
	if server == "" {
		return nil
	}

	// 可能是单一地址，也可能是 http=127.0.0.1:7890;https=127.0.0.1:7890 这种分协议写法
	if strings.Contains(server, "=") {
		server = ""
		for _, part := range strings.Split(proxyValue(text, "ProxyServer"), ";") {
			part = strings.TrimSpace(part)
			switch {
			case strings.HasPrefix(part, "http="):
				server = strings.TrimPrefix(part, "http=")
			case strings.HasPrefix(part, "https=") && server == "":
				server = strings.TrimPrefix(part, "https=")
			}
			if server != "" {
				break
			}
		}
	}
	if server == "" || strings.Contains(server, "=") {
		return nil
	}

	u, err := url.Parse("http://" + server)
	if err != nil || u.Host == "" {
		return nil
	}
	return u
}

// proxyValue 从 reg query 的文本输出中取出指定名称的值。
// 输出形如：    ProxyEnable    REG_DWORD    0x1
func proxyValue(text, name string) string {
	for _, line := range strings.Split(text, "\n") {
		fields := strings.Fields(line)
		if len(fields) >= 3 && fields[0] == name {
			return fields[2]
		}
	}
	return ""
}
