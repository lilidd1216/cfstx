package task

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
)

// ErrCancelled 表示用户在抓取过程中点了停止。
var ErrCancelled = errors.New("已取消")

const (
	// BestCFHome 是优选 IP 汇总站首页。
	BestCFHome = "https://bestcf.pages.dev/"
	// 首页中「优选IP BestIP」区块的锚点 id
	bestcfSectionID = "yxip"
	// 列表抓取并发数
	fetchConcurrency = 12
	// 域名解析并发数。
	//
	// DoH 查询是很轻量的 HTTP 请求（一次几 KB），8 个并发跑 420 个域名
	// 要 1~3 分钟，用户点了「获取 IP」后长时间没有任何反馈。
	resolveConcurrency = 24
	// 单个列表请求超时。
	//
	// 各来源是并发抓取的，总耗时约等于最慢的那个，而不是累加。
	// 30 秒对 raw.githubusercontent.com 这类国内链路偏紧，放宽到 60 秒，
	// 让慢源也有机会完整返回，避免「每次抓到的数量都不一样」。
	fetchTimeout = 60 * time.Second
	// 单次 DoH 查询超时。
	//
	// 和 fetchTimeout 分开：抓列表文件慢一点可以等，但一个域名解析不出来
	// 就该尽快放弃并换下一个 DoH，否则弱网下整轮会拖到几分钟。
	resolveTimeout = 8 * time.Second
	// 单个列表最大读取字节数，防止异常大文件拖垮内存
	maxBodyBytes = 4 << 20
	// 自定义源数量上限，避免误填过多把测速时间拖长
	maxCustomSources = 50

	// Cloudflare 官方 IP 段 API
	officialCIDRAPI = "https://api.cloudflare.com/client/v4/ips"
	// cmliu 整理的 CF CIDR 列表
	cmliuCIDRURL = "https://raw.githubusercontent.com/cmliu/cmliu/main/CF-CIDR.txt"
)

// cfDomainsURLs 是 BestCF 维护的「Cloudflare 官方域名」池，按顺序尝试。
//
// 这些域名本身托管在 Cloudflare 上，解析出来的就是 CF 边缘 IP，
// 相当于一份「已经被别人验证过确实在 CF 上」的域名清单。
//
// 国内直连 raw.githubusercontent.com 基本不通，所以镜像在前、官方放最后兜底。
// jsDelivr 偶尔会瞬时 404（CDN 缓存未命中），失败会自动换下一个。
var cfDomainsURLs = []string{
	"https://cdn.jsdelivr.net/gh/cmliu/CF-Pages-BestCF@main/cf_domains.txt",
	"https://gh-proxy.com/https://raw.githubusercontent.com/cmliu/CF-Pages-BestCF/main/cf_domains.txt",
	"https://ghfast.top/https://raw.githubusercontent.com/cmliu/CF-Pages-BestCF/main/cf_domains.txt",
	"https://ghproxy.net/https://raw.githubusercontent.com/cmliu/CF-Pages-BestCF/main/cf_domains.txt",
	"https://raw.githubusercontent.com/cmliu/CF-Pages-BestCF/main/cf_domains.txt",
}

var (
	linkRe   = regexp.MustCompile(`(?is)<span class="url-text"[^>]*>(.*?)</span>`)
	tagRe    = regexp.MustCompile(`(?s)<[^>]+>`)
	domainRe = regexp.MustCompile(`^[a-zA-Z0-9](?:[a-zA-Z0-9-]*[a-zA-Z0-9])?(?:\.[a-zA-Z0-9](?:[a-zA-Z0-9-]*[a-zA-Z0-9])?)+$`)
)

// FetchOptions 描述一次在线 IP 抓取。
type FetchOptions struct {
	DefaultPort         int      // 列表项没有显式端口时使用的端口
	AllowIPv6           bool     // 是否接受 IPv6
	IncludeBestCF       bool     // 是否抓取 bestcf.pages.dev「优选IP BestIP」区块
	IncludeOfficialCIDR bool     // 是否抓取 Cloudflare 官方 IP 段
	IncludeCmliuCIDR    bool     // 是否抓取 cmliu 整理的 CF CIDR
	IncludeCFDomains    bool     // 是否抓取 BestCF 维护的 CF 官方域名池
	CustomSources       []string // 用户自定义的 IP 列表 URL
	OnProgress          func(done, total int, msg string)
}

// portRank 决定同一 IP 出现多个端口时优先保留哪个。
// 数值越小越优先：CF 标准 HTTPS 端口优先，其余按遇到顺序。
func portRank(p int) int {
	switch p {
	case 443:
		return 0
	case 8443:
		return 1
	case 2053:
		return 2
	case 2083:
		return 3
	case 2087:
		return 4
	case 2096:
		return 5
	default:
		return 100
	}
}

// parseSourceLine 解析列表中的一行，返回 host（IP 或域名）与端口。
func parseSourceLine(line string, defaultPort int) (host string, port int, ok bool) {
	line = strings.TrimSpace(line)
	if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, "//") {
		return "", 0, false
	}
	// # 之后是备注
	if i := strings.IndexByte(line, '#'); i >= 0 {
		line = line[:i]
	}
	line = strings.TrimSpace(line)
	if line == "" {
		return "", 0, false
	}
	// 防止把完整 URL 当成条目
	if strings.Contains(strings.ToLower(line), "http") {
		return "", 0, false
	}
	host, port = SplitHostPort(line, defaultPort)
	if host == "" {
		return "", 0, false
	}
	return host, port, true
}

// isCIDRLine 判断一行是不是纯 CIDR 写法（如 173.245.48.0/20）。
// 支持行尾 # 备注，与 parseSourceLine 的处理保持一致。
func isCIDRLine(line string) bool {
	line = strings.TrimSpace(line)
	if line == "" || !strings.Contains(line, "/") {
		return false
	}
	if i := strings.IndexByte(line, '#'); i >= 0 {
		line = strings.TrimSpace(line[:i])
	}
	if line == "" || strings.Contains(strings.ToLower(line), "http") {
		return false
	}
	_, _, err := net.ParseCIDR(line)
	return err == nil
}

// isDomainName 判断 host 是否为域名（排除 IP 与 IPv6）。
func isDomainName(host string) bool {
	if host == "" || strings.Contains(host, ":") {
		return false
	}
	if net.ParseIP(host) != nil {
		return false
	}
	return domainRe.MatchString(host)
}

// extractBestCFLinks 从首页 HTML 中提取「优选IP BestIP」区块里的全部链接，
// 并排除 GitHub 相关地址。
func extractBestCFLinks(html string) []string {
	idx := strings.Index(html, `id="`+bestcfSectionID+`"`)
	if idx < 0 {
		return nil
	}
	end := len(html)
	if rel := strings.Index(html[idx:], "<section"); rel > 0 {
		end = idx + rel
	}
	block := html[idx:end]

	matches := linkRe.FindAllStringSubmatch(block, -1)
	links := make([]string, 0, len(matches))
	for _, m := range matches {
		u := strings.TrimSpace(tagRe.ReplaceAllString(m[1], ""))
		if !strings.HasPrefix(u, "http://") && !strings.HasPrefix(u, "https://") {
			continue
		}
		if strings.Contains(strings.ToLower(u), "github") {
			continue
		}
		links = append(links, u)
	}
	return links
}

func newFetchTransport() *http.Transport {
	return &http.Transport{
		// 部分第三方站点证书链不完整，抓取的是公开只读数据，跳过校验可提高成功率
		TLSClientConfig:     &tls.Config{InsecureSkipVerify: true},
		MaxIdleConns:        fetchConcurrency + resolveConcurrency,
		MaxIdleConnsPerHost: fetchConcurrency,
		IdleConnTimeout:     30 * time.Second,
	}
}

// bestCFClient 先直连，失败后再尝试系统代理。
// 两者都不通时返回直连的错误，便于界面给出准确原因。
type bestCFClient struct {
	direct *http.Client
	proxy  *http.Client
}

func newFetchClient() *bestCFClient {
	c := &bestCFClient{
		direct: &http.Client{Timeout: fetchTimeout, Transport: newFetchTransport()},
	}
	// Go 默认不读取 Windows 系统代理，这里显式读取一次作为兜底
	if proxyURL := systemProxyURL(); proxyURL != nil {
		tr := newFetchTransport()
		tr.Proxy = http.ProxyURL(proxyURL)
		c.proxy = &http.Client{Timeout: fetchTimeout, Transport: tr}
	}
	return c
}

func (c *bestCFClient) CloseIdleConnections() {
	c.direct.CloseIdleConnections()
	if c.proxy != nil {
		c.proxy.CloseIdleConnections()
	}
}

func (c *bestCFClient) get(rawURL string) (string, error) {
	return c.getWithTimeout(rawURL, 0)
}

func fetchBody(client *http.Client, rawURL string) (string, error) {
	return fetchBodyWithTimeout(client, rawURL, 0)
}

// fetchBodyWithTimeout 与 fetchBody 相同，但用独立的 context 控制超时。
// timeout <= 0 时回退到 client 自带的超时。
func fetchBodyWithTimeout(client *http.Client, rawURL string, timeout time.Duration) (string, error) {
	ctx := context.Background()
	if timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36")
	if strings.Contains(rawURL, "dns-query") || strings.Contains(rawURL, "resolve") {
		req.Header.Set("Accept", "application/dns-json")
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBodyBytes))
	if err != nil {
		return "", err
	}
	return string(body), nil
}

// getOnce 只尝试一轮（直连 → 代理），不重试。
// 供 DoH 查询这类需要「快速失败、换下一个」的场景使用。
func (c *bestCFClient) getOnce(rawURL string, timeout time.Duration) (string, error) {
	body, directErr := fetchBodyWithTimeout(c.direct, rawURL, timeout)
	if directErr == nil {
		return body, nil
	}
	if c.proxy != nil {
		if body, err := fetchBodyWithTimeout(c.proxy, rawURL, timeout); err == nil {
			return body, nil
		}
	}
	return "", directErr
}

// getWithTimeout 先直连、失败再走系统代理，两条路都失败时重试一轮。
//
// 为什么要重试：实测弱网下「连接重置」非常常见（同一域名同一路径，
// 前一次 5 秒被 RST，隔几秒再请求就正常）。抓取是并发一次性跑完的，
// 不重试的话一次抖动就会让整批源少掉一大截，用户看到的就是
// 「每次抓到的数量都不一样」。
func (c *bestCFClient) getWithTimeout(rawURL string, timeout time.Duration) (string, error) {
	var lastErr error

	for attempt := 0; attempt < 2; attempt++ {
		if attempt > 0 {
			time.Sleep(600 * time.Millisecond)
		}

		body, directErr := fetchBodyWithTimeout(c.direct, rawURL, timeout)
		if directErr == nil {
			return body, nil
		}
		lastErr = directErr

		if c.proxy != nil {
			if body, err := fetchBodyWithTimeout(c.proxy, rawURL, timeout); err == nil {
				return body, nil
			}
		}
	}
	return "", lastErr
}

func sanitizeSources(src []string) []string {
	seen := make(map[string]struct{}, len(src))
	out := make([]string, 0, len(src))
	for _, s := range src {
		s = strings.TrimSpace(s)
		if s == "" || strings.HasPrefix(s, "#") {
			continue
		}
		if !strings.HasPrefix(s, "http://") && !strings.HasPrefix(s, "https://") {
			continue
		}
		if _, ok := seen[s]; ok {
			continue
		}
		seen[s] = struct{}{}
		out = append(out, s)
		if len(out) >= maxCustomSources {
			break
		}
	}
	return out
}

func dedupeStrings(in []string) []string {
	seen := make(map[string]struct{}, len(in))
	out := make([]string, 0, len(in))
	for _, s := range in {
		if _, ok := seen[s]; ok {
			continue
		}
		seen[s] = struct{}{}
		out = append(out, s)
	}
	return out
}

// shortURL 把长 URL 压成「域名 + 路径」的紧凑形式，供进度提示使用。
func shortURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return raw
	}
	s := u.Host
	if p := strings.TrimSuffix(u.Path, "/"); p != "" {
		s += p
	}
	if len(s) > 52 {
		s = s[:52] + "…"
	}
	return s
}

// fetchAll 并发抓取全部列表，返回与 links 等长的正文数组（失败的位置为空串）。
func fetchAll(client *bestCFClient, links []string, report func(done, total int, msg string)) []string {
	bodies := make([]string, len(links))
	var (
		wg   sync.WaitGroup
		sem  = make(chan struct{}, fetchConcurrency)
		mu   sync.Mutex
		done int
	)
	for i, link := range links {
		if cancelled() {
			break
		}
		wg.Add(1)
		sem <- struct{}{}
		go func(i int, link string) {
			defer wg.Done()
			defer func() { <-sem }()
			body, err := client.get(link)
			mu.Lock()
			if err == nil {
				bodies[i] = body
			}
			done++
			// 带上成功/失败标记：用户需要能看出哪个源没抓到，
			// 否则「总数变少」永远是个谜
			mark := "✓ "
			if err != nil {
				mark = "✗ "
			}
			report(done, len(links), mark+shortURL(link))
			mu.Unlock()
		}(i, link)
	}
	wg.Wait()
	return bodies
}

// resolveOne 依次尝试阿里与 Cloudflare 的 DoH，返回全部 A/AAAA 记录。
func resolveOne(client *bestCFClient, host string, allowIPv6 bool) []net.IP {
	endpoints := []string{
		"https://dns.alidns.com/resolve",
		"https://cloudflare-dns.com/dns-query",
	}
	for _, ep := range endpoints {
		ips := lookupDoH(client, ep, host, "A")
		if allowIPv6 {
			ips = append(ips, lookupDoH(client, ep, host, "AAAA")...)
		}
		if len(ips) > 0 {
			return ips
		}
	}
	return nil
}

func lookupDoH(client *bestCFClient, endpoint, host, qtype string) []net.IP {
	u := endpoint + "?name=" + url.QueryEscape(host) + "&type=" + qtype
	body, err := client.getOnce(u, resolveTimeout)
	if err != nil {
		return nil
	}
	var out struct {
		Status int `json:"Status"`
		Answer []struct {
			Type int    `json:"type"`
			Data string `json:"data"`
		} `json:"Answer"`
	}
	if err := json.Unmarshal([]byte(body), &out); err != nil || out.Status != 0 {
		return nil
	}
	want := 1
	if qtype == "AAAA" {
		want = 28
	}
	var ips []net.IP
	for _, a := range out.Answer {
		if a.Type != want {
			continue
		}
		if ip := net.ParseIP(a.Data); ip != nil {
			ips = append(ips, ip)
		}
	}
	return ips
}

// resolveDomains 并发解析域名，返回 host -> IP 列表。
func resolveDomains(client *bestCFClient, domains map[string]int, allowIPv6 bool, report func(done, total int, msg string)) map[string][]net.IP {
	hosts := make([]string, 0, len(domains))
	for h := range domains {
		hosts = append(hosts, h)
	}

	out := make(map[string][]net.IP, len(hosts))
	var (
		wg   sync.WaitGroup
		sem  = make(chan struct{}, resolveConcurrency)
		mu   sync.Mutex
		done int
	)
	for _, h := range hosts {
		if cancelled() {
			break
		}
		wg.Add(1)
		sem <- struct{}{}
		go func(h string) {
			defer wg.Done()
			defer func() { <-sem }()
			ips := resolveOne(client, h, allowIPv6)
			mu.Lock()
			if len(ips) > 0 {
				out[h] = ips
			}
			done++
			report(done, len(hosts), h)
			mu.Unlock()
		}(h)
	}
	wg.Wait()
	return out
}

// parseOfficialCIDR 从 Cloudflare 官方 API 的 JSON 中提取 IP 段。
func parseOfficialCIDR(body string, allowIPv6 bool) []string {
	var out struct {
		Result struct {
			IPv4 []string `json:"ipv4_cidrs"`
			IPv6 []string `json:"ipv6_cidrs"`
		} `json:"result"`
		Success bool `json:"success"`
	}
	if err := json.Unmarshal([]byte(body), &out); err != nil || !out.Success {
		return nil
	}
	cidrs := append([]string{}, out.Result.IPv4...)
	if allowIPv6 {
		cidrs = append(cidrs, out.Result.IPv6...)
	}
	return cidrs
}

// fetchCIDRTargets 抓取 CIDR 源并展开成测速目标。
// 每个 CIDR 段按其下的 /24 子段各随机取一个 IP，避免展开出几万个地址。
func fetchCIDRTargets(client *bestCFClient, opts FetchOptions, report func(done, total int, msg string)) []*Target {
	var cidrs []string

	if opts.IncludeOfficialCIDR {
		report(0, 0, "正在读取 Cloudflare 官方 IP 段…")
		if body, err := client.get(officialCIDRAPI); err == nil {
			cidrs = append(cidrs, parseOfficialCIDR(body, opts.AllowIPv6)...)
		} else {
			report(0, 0, "Cloudflare 官方 IP 段读取失败，已跳过")
		}
	}

	if opts.IncludeCmliuCIDR {
		report(0, 0, "正在读取 cmliu 的 CF-CIDR 列表…")
		if body, err := client.get(cmliuCIDRURL); err == nil {
			for _, line := range strings.Split(body, "\n") {
				line = strings.TrimSpace(line)
				if !isCIDRLine(line) {
					continue
				}
				cidrs = append(cidrs, line)
			}
		} else {
			report(0, 0, "cmliu CF-CIDR 读取失败，已跳过")
		}
	}

	// 按「网段大小 × 实测可用率」分配采样预算，把候选集中到真正能用的段。
	targets := expandCIDRsWeighted(cidrs, opts.DefaultPort)
	if len(targets) > 0 {
		report(0, 0, fmt.Sprintf("按网段可用率加权采样：%d 个目标", len(targets)))
	}
	return targets
}

// FetchOnlineTargets 抓取所有启用的在线来源，解析出 IP 与端口。
// 列表中的域名会通过 DoH 解析成 IP，最终按 IP 去重返回。
func FetchOnlineTargets(opts FetchOptions) ([]*Target, error) {
	if opts.DefaultPort <= 0 || opts.DefaultPort > 65535 {
		opts.DefaultPort = 443
	}
	if !opts.IncludeBestCF && !opts.IncludeOfficialCIDR && !opts.IncludeCmliuCIDR &&
		!opts.IncludeCFDomains && len(opts.CustomSources) == 0 {
		return nil, nil
	}

	report := func(done, total int, msg string) {
		if opts.OnProgress != nil {
			opts.OnProgress(done, total, msg)
		}
	}

	client := newFetchClient()
	defer client.CloseIdleConnections()

	var links []string
	if opts.IncludeBestCF {
		report(0, 0, "正在读取 bestcf.pages.dev 首页…")
		home, err := client.get(BestCFHome)
		if err != nil {
			// 只要还有其他来源（自定义源 / CIDR / 域名池），就不因为单个站点失败而中断
			if len(opts.CustomSources) == 0 && !opts.IncludeOfficialCIDR &&
				!opts.IncludeCmliuCIDR && !opts.IncludeCFDomains {
				return nil, fmt.Errorf("读取 bestcf.pages.dev 失败：%w", err)
			}
			report(0, 0, "bestcf.pages.dev 读取失败，继续抓取其他来源")
		} else {
			links = extractBestCFLinks(home)
		}
	}

	links = append(links, sanitizeSources(opts.CustomSources)...)
	links = dedupeStrings(links)

	var bodies []string
	if len(links) > 0 {
		report(0, len(links), fmt.Sprintf("共 %d 个列表地址，开始并发抓取…", len(links)))
		bodies = fetchAll(client, links, report)
	} else if !opts.IncludeOfficialCIDR && !opts.IncludeCmliuCIDR && !opts.IncludeCFDomains {
		return nil, fmt.Errorf("没有可抓取的 IP 列表地址")
	}

	// CF 官方域名池：内容就是一份域名列表，抓到后和普通列表一样按行解析，
	// 域名走 DoH 解析成 IP。只取第一个成功的镜像，避免重复抓 5 份相同内容。
	//
	// 单独存一份而不是直接塞进 bodies：这份清单里有约 4% 的域名已经不在
	// Cloudflare 上（CNAME 到了 AWS / Akamai），解析出来的 IP 测了也白测，
	// 后面会对这个池子单独做「只在 CF 网段内」的过滤。
	var cfDomainsBody string
	if opts.IncludeCFDomains {
		report(0, 0, "正在读取 CF 官方域名池…")
		for _, u := range cfDomainsURLs {
			if cancelled() {
				return nil, ErrCancelled
			}
			body, err := client.get(u)
			if err == nil && strings.TrimSpace(body) != "" {
				cfDomainsBody = body
				break
			}
		}
		if cfDomainsBody != "" {
			report(0, 0, "CF 官方域名池读取成功")
		} else {
			report(0, 0, "CF 官方域名池读取失败，已跳过")
		}
	}

	if cancelled() {
		return nil, ErrCancelled
	}

	// IP 直接入库；域名先收集起来统一解析
	best := make(map[string]*Target, 2048)
	ranks := make(map[string]int, 2048)
	domains := make(map[string]int, 64)

	addIP := func(ip net.IP, port int) {
		if v4 := ip.To4(); v4 != nil {
			ip = v4
		} else if !opts.AllowIPv6 {
			return
		}
		key := ip.String()
		r := portRank(port)
		if old, ok := ranks[key]; ok && old <= r {
			return
		}
		best[key] = &Target{IP: &net.IPAddr{IP: ip}, Port: port}
		ranks[key] = r
	}

	// 正文里的 CIDR 行（例如 ips-v4 这种清单）单独收集。
	// parseSourceLine 只认 IP:port 和域名，之前这类行会被整行丢掉，
	// 用户把 https://www.cloudflare.com/ips-v4 填进自定义源等于白抓。
	cidrRanges := newIPRanges()
	cidrRanges.port = opts.DefaultPort

	for _, body := range bodies {
		if body == "" {
			continue
		}
		for _, line := range strings.Split(body, "\n") {
			// CIDR 必须最先判断：parseSourceLine 会把 "173.245.48.0/20"
			// 当作 host 原样返回并标记 ok=true，后面的 IP/域名判断都会落空，
			// 整行被静默丢弃。
			if isCIDRLine(line) {
				cidrRanges.addLine(line, opts.DefaultPort)
				continue
			}
			host, port, ok := parseSourceLine(line, opts.DefaultPort)
			if !ok {
				continue
			}
			if ip := net.ParseIP(host); ip != nil {
				addIP(ip, port)
			} else if isDomainName(host) {
				if old, exists := domains[host]; !exists || portRank(port) < portRank(old) {
					domains[host] = port
				}
			}
		}
	}

	for _, t := range cidrRanges.ips {
		addIP(t.IP.IP, t.Port)
	}

	// 域名池里的域名单独记账：同一个域名也可能出现在别的来源里，
	// 只有来自这个池子的解析结果才做 CF 网段过滤。
	cfPoolHosts := make(map[string]bool, 512)
	if cfDomainsBody != "" {
		for _, line := range strings.Split(cfDomainsBody, "\n") {
			host, port, ok := parseSourceLine(line, opts.DefaultPort)
			if !ok {
				continue
			}
			if ip := net.ParseIP(host); ip != nil {
				if IsCloudflareOwnedIP(ip) {
					addIP(ip, port)
				}
			} else if isDomainName(host) {
				if old, exists := domains[host]; !exists || portRank(port) < portRank(old) {
					domains[host] = port
				}
				cfPoolHosts[host] = true
			}
		}
	}

	if len(domains) > 0 {
		report(0, len(domains), fmt.Sprintf("发现 %d 个域名，正在通过 DoH 解析…", len(domains)))
		resolved := resolveDomains(client, domains, opts.AllowIPv6, report)
		for host, port := range domains {
			for _, ip := range resolved[host] {
				// 域名池里那些已经不在 CF 上的域名，解析结果直接丢掉
				if cfPoolHosts[host] && !IsCloudflareOwnedIP(ip) {
					continue
				}
				addIP(ip, port)
			}
		}
	}

	// CIDR 源：官方 API 与 cmliu 列表，展开后并入结果
	if opts.IncludeOfficialCIDR || opts.IncludeCmliuCIDR {
		for _, t := range fetchCIDRTargets(client, opts, report) {
			addIP(t.IP.IP, t.Port)
		}
	}

	if cancelled() {
		return nil, ErrCancelled
	}

	out := make([]*Target, 0, len(best))
	for _, t := range best {
		out = append(out, t)
	}
	sort.Slice(out, func(i, j int) bool {
		return bytes.Compare(out[i].IP.IP, out[j].IP.IP) < 0
	})
	return out, nil
}
