package task

import (
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// ProxyIPSource 是一个内置的候选数据源。
type ProxyIPSource struct {
	Key         string   `json:"key"`
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Kind        string   `json:"kind"`  // "remote" 联网抓取 / "builtin" 内置列表
	URLs        []string `json:"-"`     // 依次尝试，任意一个成功即可
	Count       int      `json:"count"` // 内置源的数量，联网源为 0
}

// proxyIPSourceFetchTimeout 是单个 URL 的抓取超时。
//
// GitHub raw 在大陆实测极不稳定（同一条链接一次 1.2 秒成功、一次 25 秒超时），
// 所以主用 jsDelivr 镜像，失败再回退到原始地址。每个只给 10 秒，
// 两个都失败就早点告诉用户手动粘贴，别让人干等。
const proxyIPSourceFetchTimeout = 10 * time.Second

// cmliussssDomains 是 cmliussss 官方维护的分地区反代域名。
// 这些是域名而不是 IP，默认 443 端口；DNS 会解析到对应的第三方服务器。
var cmliussssDomains = []string{
	"ProxyIP.CMLiussss.net",
	"ProxyIP.HK.CMLiussss.net",
	"ProxyIP.SG.CMLiussss.net",
	"ProxyIP.JP.CMLiussss.net",
	"ProxyIP.KR.CMLiussss.net",
	"ProxyIP.IN.CMLiussss.net",
	"ProxyIP.GB.CMLiussss.net",
	"ProxyIP.FR.CMLiussss.net",
	"ProxyIP.DE.CMLiussss.net",
	"ProxyIP.NL.CMLiussss.net",
	"ProxyIP.SE.CMLiussss.net",
	"ProxyIP.FI.CMLiussss.net",
	"ProxyIP.PL.CMLiussss.net",
	"ProxyIP.RU.CMLiussss.net",
	"ProxyIP.CH.CMLiussss.net",
	"ProxyIP.LV.CMLiussss.net",
	"ProxyIP.US.CMLiussss.net",
	"ProxyIP.CA.CMLiussss.net",
}

// BuiltinProxyIPSources 是界面上「一键导入」的数据源。
//
// 只放两个：多了反而让用户不知道该选哪个，也容易给源站造成压力。
var BuiltinProxyIPSources = []ProxyIPSource{
	{
		Key:         "xiaobei",
		Name:        "社区优选列表",
		Description: "社区每日巡检并筛选过的 IP:PORT，自带国家和测速标注",
		Kind:        "remote",
		URLs: []string{
			// jsDelivr 在国内通常能直连，作为主源
			"https://cdn.jsdelivr.net/gh/Xiaobei09/proxyip@main/data/valid/all_ltd.txt",
			// 原始地址作为兜底
			"https://raw.githubusercontent.com/Xiaobei09/proxyip/main/data/valid/all_ltd.txt",
		},
	},
	{
		Key:         "cmliussss",
		Name:        "cmliussss 地区域名",
		Description: "官方维护的分地区反代域名，固定 443 端口",
		Kind:        "builtin",
		Count:       len(cmliussssDomains),
	},
}

// FindProxyIPSource 按 key 查找内置数据源。
func FindProxyIPSource(key string) (ProxyIPSource, bool) {
	for _, s := range BuiltinProxyIPSources {
		if s.Key == key {
			return s, true
		}
	}
	return ProxyIPSource{}, false
}

// FetchProxyIPCandidates 取回一个数据源的候选列表。
//
// 联网源的失败信息会尽量说人话：GitHub raw 在大陆经常连不上，
// 直接抛 "Get ...: dial tcp ... i/o timeout" 对用户没有任何帮助。
func FetchProxyIPCandidates(src ProxyIPSource) ([]ProxyIPCandidate, error) {
	if src.Kind == "builtin" {
		cands := make([]ProxyIPCandidate, 0, len(cmliussssDomains))
		for _, d := range cmliussssDomains {
			cands = append(cands, ProxyIPCandidate{Host: d, Port: proxyIPDefaultPort})
		}
		return cands, nil
	}

	// 依次尝试每个地址，任意一个成功即可
	var lastReason string
	for _, u := range src.URLs {
		body, err := fetchSourceURL(u)
		if err != nil {
			lastReason = err.Error()
			continue
		}
		cands := ParseProxyIPCandidates(string(body))
		if len(cands) == 0 {
			lastReason = "内容为空或格式无法识别"
			continue
		}
		return cands, nil
	}

	if lastReason == "" {
		lastReason = "没有可用的数据源地址"
	}
	return nil, fmt.Errorf("抓取失败（%s）。网络不通时可以直接打开 %s 复制列表，粘到输入框里", lastReason, src.URLs[0])
}

// fetchSourceURL 抓一个地址的内容。
func fetchSourceURL(u string) ([]byte, error) {
	client := &http.Client{Timeout: proxyIPSourceFetchTimeout}
	resp, err := client.Get(u)
	if err != nil {
		return nil, fmt.Errorf("%s", friendlyNetErr(err))
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("数据源返回 HTTP %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return nil, fmt.Errorf("下载中断")
	}
	return body, nil
}

// friendlyNetErr 把 Go 的网络错误收拾成一句能看懂的话。
func friendlyNetErr(err error) string {
	msg := err.Error()
	switch {
	case strings.Contains(msg, "timeout"), strings.Contains(msg, "deadline exceeded"):
		return "连接超时"
	case strings.Contains(msg, "no such host"):
		return "域名解析失败"
	case strings.Contains(msg, "connection refused"):
		return "连接被拒绝"
	case strings.Contains(msg, "certificate"):
		return "证书校验失败"
	}
	return "网络错误"
}
