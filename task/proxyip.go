package task

import (
	"bufio"
	"bytes"
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// ProxyIP 检测用独立的取消标志，避免和主页面的「停止测速」互相干扰。
var proxyIPCancelled int32

// CancelProxyIPProbe 请求停止正在进行的 ProxyIP 检测。
func CancelProxyIPProbe() { atomic.StoreInt32(&proxyIPCancelled, 1) }

// ResetProxyIPProbe 清除停止标志，开始新一轮检测前调用。
func ResetProxyIPProbe() { atomic.StoreInt32(&proxyIPCancelled, 0) }

func proxyIPStopped() bool { return atomic.LoadInt32(&proxyIPCancelled) == 1 }

// ProxyIP 检测。
//
// 背景：edgetunnel 这类 Workers 脚本需要配置 PROXYIP（一个能代为转发到
// Cloudflare 的第三方服务器）。公共 ProxyIP 列表里的地址衰减很快，
// 别人测着 20ms，到你这里可能根本连不上，所以必须自己测一遍。
//
// 判据（比 Worker 端更严格）：
//  1. TCP 能连上；
//  2. 用探测域名做 SNI 能完成 TLS 握手；
//  3. 对方返回的证书确实是这个探测域名的（真反代会把 ClientHello
//     透传给 CF 边缘，CF 根据 SNI 返回对应证书；普通服务器只会返回
//     自己的证书）；
//  4. 端到端请求 /cdn-cgi/trace 能拿到 Cloudflare 的响应。
//
// 第 3 步很关键：cmliu 的 Worker 版只检查握手的首字节是否为 0x16，
// 任何 TLS 服务器都能通过，会产生大量假阳性。

const (
	// DefaultProxyProbeDomain 是默认探测域名。
	// speed.cloudflare.com 由 Cloudflare 自己托管，证书必然存在，
	// 只要对方真的转发到 CF 边缘就一定能拿到这张证书。
	DefaultProxyProbeDomain = "speed.cloudflare.com"

	proxyIPTracePath = "/cdn-cgi/trace"

	proxyIPDialTimeout      = 4 * time.Second
	proxyIPHandshakeTimeout = 6 * time.Second
	proxyIPReadTimeout      = 6 * time.Second
	proxyIPDNSTimeout       = 5 * time.Second

	proxyIPDefaultPort    = 443
	proxyIPDefaultConc    = 32
	proxyIPMaxConcurrency = 64
)

// ProxyIPCandidate 是一个待检测的候选地址。
type ProxyIPCandidate struct {
	Host string `json:"host"` // IP 或域名
	Port int    `json:"port"` // 默认 443
	Note string `json:"note"` // 候选列表里 # 后面的说明
}

// Addr 返回 host:port。
func (c ProxyIPCandidate) Addr() string {
	port := c.Port
	if port == 0 {
		port = proxyIPDefaultPort
	}
	return net.JoinHostPort(c.Host, strconv.Itoa(port))
}

// ProxyIPResult 是一条检测结果。
type ProxyIPResult struct {
	Addr      string `json:"addr"` // host:port
	Host      string `json:"host"`
	Port      int    `json:"port"`
	Note      string `json:"note"`
	OK        bool   `json:"ok"`
	CertMatch bool   `json:"certMatch"`
	TLSDelay  int64  `json:"tlsDelay"`  // ms，TCP+TLS 握手耗时
	HTTPDelay int64  `json:"httpDelay"` // ms，trace 请求耗时
	ExitIP    string `json:"exitIp"`
	ExitColo  string `json:"exitColo"`
	ExitLoc   string `json:"exitLoc"`
	ExitIPv6  bool   `json:"exitIpv6"`
	Error     string `json:"error"`
}

// TotalDelay 是判定优劣用的总耗时（越小越好）。
func (r ProxyIPResult) TotalDelay() int64 { return r.TLSDelay + r.HTTPDelay }

// ---------------------------------------------------------------------------
// 候选解析

// hostnamePattern 校验域名形态，长度上限 63 是单段 DNS 标签的限制。
var hostnameLabelPattern = regexp.MustCompile(`^[a-zA-Z0-9]([a-zA-Z0-9-]*[a-zA-Z0-9])?$`)

// ParseProxyIPCandidates 解析用户粘贴或从数据源抓来的候选列表。
//
// 每行支持：
//
//	1.2.3.4
//	1.2.3.4:8443
//	proxyip.example.com
//	proxyip.example.com:8443
//	https://proxyip.example.com/
//	2606:4700::1
//	[2606:4700::1]:8443
//	随便什么#以 # 开头的内容作为备注
//
// 空行和以 # 开头的整行注释会跳过，重复地址只保留一次。
func ParseProxyIPCandidates(text string) []ProxyIPCandidate {
	out := make([]ProxyIPCandidate, 0, 64)
	seen := make(map[string]struct{})

	for _, rawLine := range strings.Split(text, "\n") {
		line := strings.TrimSpace(strings.TrimSuffix(rawLine, "\r"))
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		note := ""
		if i := strings.Index(line, "#"); i >= 0 {
			note = strings.TrimSpace(line[i+1:])
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
		out = append(out, ProxyIPCandidate{Host: host, Port: port, Note: note})
	}
	return out
}

// splitCandidate 把一行拆成 host 和 port。
func splitCandidate(s string) (string, int, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return "", 0, false
	}

	// 去掉协议前缀和路径，兼容直接粘贴网址的情况
	if i := strings.Index(s, "://"); i >= 0 {
		s = s[i+3:]
	}
	if i := strings.IndexAny(s, "/?"); i >= 0 {
		s = s[:i]
	}
	if s == "" {
		return "", 0, false
	}

	host, portStr := s, ""
	switch {
	case strings.HasPrefix(s, "["):
		// [IPv6]:port 或 [IPv6]
		end := strings.LastIndex(s, "]")
		if end < 0 {
			return "", 0, false
		}
		host = s[1:end]
		rest := s[end+1:]
		if rest != "" {
			if !strings.HasPrefix(rest, ":") {
				return "", 0, false
			}
			portStr = rest[1:]
		}
	case strings.Count(s, ":") == 1:
		// host:port
		host, portStr, _ = strings.Cut(s, ":")
	default:
		// 裸域名或裸 IPv6
		host = s
	}

	host = strings.TrimSpace(host)
	if host == "" {
		return "", 0, false
	}

	port := proxyIPDefaultPort
	if portStr != "" {
		p, err := strconv.Atoi(portStr)
		if err != nil || p < 1 || p > 65535 {
			return "", 0, false
		}
		port = p
	}

	if net.ParseIP(host) == nil && !isValidHostname(host) {
		return "", 0, false
	}
	return host, port, true
}

func isValidHostname(s string) bool {
	s = strings.TrimSuffix(s, ".")
	if s == "" || len(s) > 253 {
		return false
	}
	for _, label := range strings.Split(s, ".") {
		if len(label) > 63 || !hostnameLabelPattern.MatchString(label) {
			return false
		}
	}
	return true
}

// ---------------------------------------------------------------------------
// Cloudflare 自有网段

// cloudflareOwnedCIDRs 是 Cloudflare 官方公布的 IP 段。
// 这些地址本身就是 CF 边缘节点，证书和 trace 都没问题，但 Worker 的
// connect() 明确禁止连接它们，所以必须从 ProxyIP 候选里剔除，
// 否则会出现「检测通过但配进节点完全不工作」的假阳性。
var cloudflareOwnedCIDRs = []string{
	// IPv4
	"173.245.48.0/20",
	"103.21.244.0/22",
	"103.22.200.0/22",
	"103.31.4.0/22",
	"141.101.64.0/18",
	"108.162.192.0/18",
	"190.93.240.0/20",
	"188.114.96.0/20",
	"197.234.240.0/22",
	"198.41.128.0/17",
	"162.158.0.0/15",
	"104.16.0.0/13",
	"104.24.0.0/14",
	"172.64.0.0/13",
	"131.0.72.0/22",
	// IPv6
	"2400:cb00::/32",
	"2606:4700::/32",
	"2803:f800::/32",
	"2405:b500::/32",
	"2405:8100::/32",
	"2a06:98c0::/29",
	"2c0f:f248::/32",
}

var cloudflareOwnedNets = func() []*net.IPNet {
	nets := make([]*net.IPNet, 0, len(cloudflareOwnedCIDRs))
	for _, c := range cloudflareOwnedCIDRs {
		if _, n, err := net.ParseCIDR(c); err == nil {
			nets = append(nets, n)
		}
	}
	return nets
}()

// IsCloudflareOwnedIP 判断地址是否落在 Cloudflare 自有网段内。
func IsCloudflareOwnedIP(ip net.IP) bool {
	if ip == nil {
		return false
	}
	for _, n := range cloudflareOwnedNets {
		if n.Contains(ip) {
			return true
		}
	}
	return false
}

// ---------------------------------------------------------------------------
// 探测

// ProbeProxyIPs 并发探测候选列表。
//
// probeDomain 是 TLS 的 SNI，默认 DefaultProxyProbeDomain；用户也可以
// 填自己的 Worker 域名，这样探测出来的结果更贴近真实使用场景。
// onResult 每完成一条回调一次，供界面实时追加。
func ProbeProxyIPs(
	cands []ProxyIPCandidate,
	probeDomain string,
	concurrency int,
	onResult func(ProxyIPResult),
) []ProxyIPResult {
	probeDomain = NormalizeProbeDomain(probeDomain)
	if probeDomain == "" {
		probeDomain = DefaultProxyProbeDomain
	}

	if concurrency <= 0 {
		concurrency = proxyIPDefaultConc
	}
	if concurrency > proxyIPMaxConcurrency {
		concurrency = proxyIPMaxConcurrency
	}

	var (
		mu      sync.Mutex
		wg      sync.WaitGroup
		sem     = make(chan struct{}, concurrency)
		results = make([]ProxyIPResult, 0, len(cands))
	)

	for _, c := range cands {
		if proxyIPStopped() {
			break
		}
		wg.Add(1)
		sem <- struct{}{}
		go func(c ProxyIPCandidate) {
			defer wg.Done()
			defer func() { <-sem }()

			r := probeOneProxyIP(c, probeDomain)

			mu.Lock()
			results = append(results, r)
			mu.Unlock()

			if onResult != nil {
				onResult(r)
			}
		}(c)
	}
	wg.Wait()

	sortProxyIPResults(results)
	return results
}

// sortProxyIPResults 把可用的排在前面，同组内按总耗时升序。
func sortProxyIPResults(rs []ProxyIPResult) {
	sort.SliceStable(rs, func(i, j int) bool {
		if rs[i].OK != rs[j].OK {
			return rs[i].OK
		}
		if rs[i].OK {
			if a, b := rs[i].TotalDelay(), rs[j].TotalDelay(); a != b {
				return a < b
			}
		}
		return rs[i].Addr < rs[j].Addr
	})
}

// probeOneProxyIP 探测单个候选。
func probeOneProxyIP(c ProxyIPCandidate, probeDomain string) ProxyIPResult {
	port := c.Port
	if port == 0 {
		port = proxyIPDefaultPort
	}
	res := ProxyIPResult{
		Host: c.Host,
		Port: port,
		Note: c.Note,
	}
	res.Addr = net.JoinHostPort(c.Host, strconv.Itoa(port))

	// 1. 先解析出可以连的 IP。
	// 域名形式的 ProxyIP 有一部分直接 CNAME 到了 Cloudflare，
	// 这些地址证书和 trace 都会「通过」，但配进节点完全不工作，
	// 必须先剔除 —— 否则用户会以为自己挑到了好节点。
	targets, errMsg := resolveProxyIPTargets(c.Host)
	if errMsg != "" {
		res.Error = errMsg
		return res
	}

	// 2. 多个 IP 并行尝试，取第一个成功的。
	// 串行太慢：一个域名常常解析出 IPv6 + IPv4 两条，IPv6 往往不通，
	// 串着试要等两倍时间。
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	type outcome struct {
		res ProxyIPResult
		ok  bool
	}
	ch := make(chan outcome, len(targets))
	for _, ip := range targets {
		go func(ip net.IP) {
			addr := net.JoinHostPort(ip.String(), strconv.Itoa(port))
			r, ok := probeCandidateAddr(ctx, addr, probeDomain)
			ch <- outcome{r, ok}
		}(ip)
	}

	var lastErr string
	for range targets {
		o := <-ch
		if o.ok {
			cancel() // 让其余尝试尽快退出
			o.res.Host = c.Host
			o.res.Port = port
			o.res.Note = c.Note
			o.res.Addr = res.Addr
			return o.res
		}
		lastErr = o.res.Error
	}

	res.Error = lastErr
	if res.Error == "" {
		res.Error = "无法连接（端口未开放或被墙）"
	}
	return res
}

// resolveProxyIPTargets 把候选主机名解析成可连接的 IP 列表，并剔除
// Cloudflare 自有网段。返回的第二个值非空表示这是一条错误说明。
func resolveProxyIPTargets(host string) ([]net.IP, string) {
	if ip := net.ParseIP(host); ip != nil {
		if IsCloudflareOwnedIP(ip) {
			return nil, "属于 Cloudflare 自有网段，不能作为 ProxyIP"
		}
		return []net.IP{ip}, ""
	}

	ips, ok := lookupIPsWithTimeout(host, proxyIPDNSTimeout)
	if !ok {
		return nil, "域名解析超时（DNS 无响应）"
	}
	if len(ips) == 0 {
		return nil, "域名解析失败（DNS 查询不到该域名）"
	}

	var usable []net.IP
	for _, ip := range ips {
		if !IsCloudflareOwnedIP(ip) {
			usable = append(usable, ip)
		}
	}
	if len(usable) == 0 {
		return nil, "域名解析到 Cloudflare 自有网段，不能作为 ProxyIP"
	}
	return usable, ""
}

// lookupIPsWithTimeout 带硬超时的 DNS 查询。
//
// 不能用 net.Resolver 的 ctx —— Windows 上底层走的是 DnsQuery API，
// context 取消不会中断正在进行的查询，遇到黑洞域名会一直挂住，
// 把整批检测拖死。这里用 goroutine + 定时器兜底。
func lookupIPsWithTimeout(host string, timeout time.Duration) ([]net.IP, bool) {
	type dnsResult struct{ ips []net.IP }
	ch := make(chan dnsResult, 1)
	go func() {
		ips, _ := net.LookupIP(host)
		ch <- dnsResult{ips: ips}
	}()

	select {
	case r := <-ch:
		return r.ips, true
	case <-time.After(timeout):
		return nil, false
	}
}

// probeCandidateAddr 对已经确定好 IP 的地址做完整探测。
// ctx 取消时主动关闭连接，避免同一候选的其它 IP 已经成功、
// 这里还在慢慢等超时。
func probeCandidateAddr(ctx context.Context, addr, probeDomain string) (ProxyIPResult, bool) {
	var res ProxyIPResult

	dialer := &net.Dialer{Timeout: proxyIPDialTimeout}
	raw, err := dialer.DialContext(ctx, "tcp", addr)
	if err != nil {
		res.Error = "无法连接（端口未开放或被墙）"
		return res, false
	}
	defer raw.Close()

	done := make(chan struct{})
	defer close(done)
	go func() {
		select {
		case <-ctx.Done():
			_ = raw.Close()
		case <-done:
		}
	}()

	// 兜底：DNS 结果可能在解析和连接之间变化
	if tcpAddr, ok := raw.RemoteAddr().(*net.TCPAddr); ok && IsCloudflareOwnedIP(tcpAddr.IP) {
		res.Error = "属于 Cloudflare 自有网段，不能作为 ProxyIP"
		return res, false
	}

	// 2. TLS 握手，SNI 用探测域名
	_ = raw.SetDeadline(time.Now().Add(proxyIPHandshakeTimeout + proxyIPReadTimeout))

	tlsStart := time.Now()
	tlsConn := tls.Client(raw, &tls.Config{
		ServerName:         probeDomain,
		InsecureSkipVerify: true, // 自己校验主机名，见下一步
	})
	if err := tlsConn.Handshake(); err != nil {
		res.Error = "TLS 握手失败（未转发到 Cloudflare 边缘）"
		return res, false
	}
	res.TLSDelay = time.Since(tlsStart).Milliseconds()

	// 3. 证书必须是探测域名的。
	// VerifyHostname 会自动处理 *.example.com 通配符 —— 用户自己的域名
	// 通常只签了通配符证书，用字符串相等判断会全部误判。
	state := tlsConn.ConnectionState()
	if len(state.PeerCertificates) == 0 {
		res.Error = "对方未返回证书"
		return res, false
	}
	if err := state.PeerCertificates[0].VerifyHostname(probeDomain); err != nil {
		res.Error = "证书不属于该域名（是普通服务器，不能作反代）"
		return res, false
	}
	res.CertMatch = true

	// 4. 端到端请求 trace
	httpStart := time.Now()
	req := "GET " + proxyIPTracePath + " HTTP/1.1\r\n" +
		"Host: " + probeDomain + "\r\n" +
		"User-Agent: CFSTX-ProxyIP/1.0\r\n" +
		"Accept: */*\r\n" +
		"Connection: close\r\n\r\n"
	if _, err := tlsConn.Write([]byte(req)); err != nil {
		res.Error = "请求发送失败"
		return res, false
	}

	br := bufio.NewReader(tlsConn)
	statusLine, err := br.ReadString('\n')
	if err != nil {
		res.Error = "未收到响应"
		return res, false
	}
	if !strings.Contains(statusLine, "200") {
		res.Error = "返回 " + compactStatus(statusLine)
		return res, false
	}

	body, err := io.ReadAll(io.LimitReader(br, 4096))
	if err != nil || !bytes.Contains(body, []byte("colo=")) {
		res.Error = "响应不是 Cloudflare trace"
		return res, false
	}

	res.HTTPDelay = time.Since(httpStart).Milliseconds()
	res.OK = true
	parseProxyIPTrace(body, &res)
	return res, res.OK
}

func compactStatus(line string) string {
	line = strings.TrimSpace(line)
	if len(line) > 40 {
		line = line[:40] + "…"
	}
	return line
}

// parseProxyIPTrace 从 /cdn-cgi/trace 的响应里取出出口信息。
func parseProxyIPTrace(body []byte, res *ProxyIPResult) {
	for _, line := range strings.Split(string(body), "\n") {
		line = strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(line, "ip="):
			res.ExitIP = strings.TrimPrefix(line, "ip=")
			res.ExitIPv6 = strings.Contains(res.ExitIP, ":")
		case strings.HasPrefix(line, "colo="):
			res.ExitColo = strings.TrimPrefix(line, "colo=")
		case strings.HasPrefix(line, "loc="):
			res.ExitLoc = strings.TrimPrefix(line, "loc=")
		}
	}
	if res.ExitIP == "" {
		res.Error = "trace 里没有 ip 字段"
		res.OK = false
	}
}

// FormatProxyIPList 把可用结果格式化成 edgetunnel 可直接使用的写法。
//
//	PROXYIP=1.2.3.4:443
//	PROXYIP=5.6.7.8:8443
//
// 端口必须带上：常用端口除了 443 还有 8443/2053/2096 等。
func FormatProxyIPList(results []ProxyIPResult, onlyOK bool) string {
	var b strings.Builder
	for _, r := range results {
		if onlyOK && !r.OK {
			continue
		}
		fmt.Fprintf(&b, "PROXYIP=%s\n", r.Addr)
	}
	return b.String()
}
