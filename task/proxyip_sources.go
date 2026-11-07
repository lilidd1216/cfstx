package task

import (
	"encoding/csv"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// ProxyIPSource 是一个内置的候选数据源。
type ProxyIPSource struct {
	Key         string   `json:"key"`
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Kind        string   `json:"kind"`   // "remote" 联网抓取 / "builtin" 内置列表
	Format      string   `json:"format"` // "line" IP:端口#备注 / "csv" / "iponly" 纯 IP
	URLs        []string `json:"-"`      // 依次尝试，任意一个成功即可
	Count       int      `json:"count"`  // 内置源的数量，联网源为 0
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
// 前三个来自 bestcf.pages.dev 的 ProxyIP 专区，各自格式不同，
// 所以导入后统一走 ParseSourceBody 解析。
// cmliussss 地区域名是内置的，质量相对稳定，保留。
var BuiltinProxyIPSources = []ProxyIPSource{
	{
		Key:         "xgonce",
		Name:        "Xgonce",
		Description: "每 6 小时更新，带速度、机房和延迟标注",
		Kind:        "remote",
		Format:      "csv",
		URLs: []string{
			"https://cdn.jsdelivr.net/gh/xgonce/Cloudflare_IP@main/result.csv",
			"https://raw.githubusercontent.com/xgonce/Cloudflare_IP/refs/heads/main/result.csv",
		},
	},
	{
		Key:         "xiaobei",
		Name:        "Xiaobei09",
		Description: "每 3 小时更新，IP:端口 形式，带国家和测速标注",
		Kind:        "remote",
		Format:      "line",
		URLs: []string{
			"https://cdn.jsdelivr.net/gh/Xiaobei09/proxyip@main/data/valid/all_ltd.txt",
			"https://raw.githubusercontent.com/Xiaobei09/proxyip/main/data/valid/all_ltd.txt",
		},
	},
	{
		Key:         "mingyu",
		Name:        "Mingyu",
		Description: "每半小时更新，只给地址，端口按 443 处理",
		Kind:        "remote",
		Format:      "iponly",
		URLs: []string{
			"https://ipdb.api.030101.xyz/?type=bestproxy",
		},
	},
	{
		Key:         "cmliussss",
		Name:        "cmliussss 地区域名",
		Description: "官方维护的分地区反代域名，固定 443 端口",
		Kind:        "builtin",
		Format:      "line",
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
		cands := ParseSourceBody(body, src.Format)
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

// ParseSourceBody 按数据源的格式解析内容。
//
// bestcf 上的三个源格式完全不一样，不能都用同一套解析：
//
//	xgonce  → CSV：IP,cf-meta-ip,端口,速度(Mbps),CF归属国,机房,TCP延迟,TLS延迟
//	xiaobei → 行式：IP:端口#备注
//	mingyu  → 纯 IP，每行一个，端口按 443
func ParseSourceBody(body []byte, format string) []ProxyIPCandidate {
	text := string(body)
	switch format {
	case "csv":
		return parseCSVCandidates(text)
	case "iponly":
		return parseIPOnlyCandidates(text)
	default:
		return ParseProxyIPCandidates(text)
	}
}

// parseCSVCandidates 解析 Xgonce 的 result.csv。
//
// 列顺序：IP,cf-meta-ip,端口,速度(Mbps),CF归属国,机房,TCP延迟,TLS延迟
//
// 注意用的是 **IP 列**（第 0 列），不是 cf-meta-ip。
// 实测 40 组两列不同的样本：IP 列 30/40 能通过证书校验，
// cf-meta-ip 列只有 2/40 —— 后者是元数据地址，不能当反代用。
//
// 用标准库 csv 读，而不是按逗号切：文件带 BOM，字段也可能被引号包住。
func parseCSVCandidates(text string) []ProxyIPCandidate {
	text = strings.TrimPrefix(text, "\ufeff")
	r := csv.NewReader(strings.NewReader(text))
	r.FieldsPerRecord = -1 // 列数不固定时不报错
	r.LazyQuotes = true

	records, err := r.ReadAll()
	if err != nil && len(records) == 0 {
		return nil
	}

	out := make([]ProxyIPCandidate, 0, len(records))
	seen := make(map[string]struct{}, len(records))

	for _, fields := range records {
		if len(fields) < 3 {
			continue
		}

		host := strings.TrimSpace(fields[0])
		if net.ParseIP(host) == nil {
			continue // 表头或非法行
		}

		port := proxyIPDefaultPort
		if p, err := strconv.Atoi(strings.TrimSpace(fields[2])); err == nil && p >= 1 && p <= 65535 {
			port = p
		}

		// 备注拼上机房和速度，方便用户判断
		var noteParts []string
		if len(fields) > 5 {
			if colo := strings.TrimSpace(fields[5]); colo != "" {
				noteParts = append(noteParts, colo)
			}
		}
		if len(fields) > 3 {
			if speed := strings.TrimSpace(fields[3]); speed != "" {
				noteParts = append(noteParts, speed+"Mbps")
			}
		}

		key := net.JoinHostPort(host, strconv.Itoa(port))
		if _, dup := seen[key]; dup {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, ProxyIPCandidate{
			Host: host,
			Port: port,
			Note: strings.Join(noteParts, " "),
		})
	}
	return out
}

// parseIPOnlyCandidates 解析只有地址的列表（Mingyu）。
func parseIPOnlyCandidates(text string) []ProxyIPCandidate {
	out := make([]ProxyIPCandidate, 0, len(text)/16+1)
	seen := make(map[string]struct{})

	for _, line := range strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		// 兼容偶尔带了端口或备注的情况
		if i := strings.IndexAny(line, "# "); i >= 0 {
			line = strings.TrimSpace(line[:i])
		}
		host, port, ok := splitCandidate(line)
		if !ok {
			continue
		}
		key := net.JoinHostPort(host, strconv.Itoa(port))
		if _, dup := seen[key]; dup {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, ProxyIPCandidate{Host: host, Port: port})
	}
	return out
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
