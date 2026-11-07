package task

import (
	"bytes"
	"crypto/tls"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

const (
	// probePath 是 Cloudflare 边缘的 trace 端点。
	// 它由边缘节点直接生成响应，不进入 Workers 脚本，
	// 因此用它做探测不会消耗 Workers 的请求配额。
	probePath = "/cdn-cgi/trace"
	// 单个目标的探测超时。trace 端点在大陆访问通常 1~2 秒。
	probeTimeout = 5 * time.Second
	// 探测阶段额外的并发上限，避免把网络打满
	probeMaxConcurrency = 300
)

// NormalizeProbeDomain 把用户随手粘来的各种写法归一成纯 hostname。
//
// 用户常见的输入是浏览器地址栏复制来的 https://xxx/，或用带端口的 host:443。
// 直接拼接到 "https://" 后面会得到 https://https://xxx//cdn-cgi/trace，
// 解析出的 Host 变成 "https"，所有探测必然失败。所以在入口统一收拾干净。
//
//	https://fast3.example.com/      → fast3.example.com
//	fast3.example.com/admin         → fast3.example.com
//	fast3.example.com:8443          → fast3.example.com
//	"  Fast3.Example.COM  "         → fast3.example.com
func NormalizeProbeDomain(s string) string {
	s = strings.TrimSpace(s)
	// 去掉协议前缀（http://、https://，以及任何 xxx://）
	if i := strings.Index(s, "://"); i >= 0 {
		s = s[i+3:]
	}
	// 去掉路径、查询串、片段
	if i := strings.IndexAny(s, "/?#"); i >= 0 {
		s = s[:i]
	}
	// 去掉端口，兼容 [::1]:443 这类 IPv6 写法
	if h, _, err := net.SplitHostPort(s); err == nil {
		s = h
	}
	s = strings.TrimSuffix(strings.TrimSpace(s), ".")
	return strings.ToLower(s)
}

// ProbeDomain 筛选出真正服务指定域名的目标。
//
// 背景：Cloudflare 的 Anycast IP 并不是每个都能服务所有域名。
// 用户的 Worker / Pages 自定义域名，只在部分边缘节点上有配置，
// 其余节点会返回 403（error 1034）或超时。
//
// 用 https://<domain>/cdn-cgi/trace 探测可以准确区分：
//   - 正常：返回 200，且响应体包含 colo= 字段
//   - 不服务该域名：返回 403 或直接超时
//
// 该路径绕过 Worker，不消耗 Workers 请求配额。
func ProbeDomain(targets []*Target, domain string, onProgress func(done, total int)) []*Target {
	// 兜底：调用方若忘了规范化，这里保证不会拼出非法 URL
	domain = NormalizeProbeDomain(domain)
	if domain == "" || len(targets) == 0 {
		return targets
	}

	concurrency := Routines
	if concurrency <= 0 {
		concurrency = defaultRoutines
	}
	if concurrency > probeMaxConcurrency {
		concurrency = probeMaxConcurrency
	}

	var (
		mu    sync.Mutex
		wg    sync.WaitGroup
		sem   = make(chan struct{}, concurrency)
		done  int32
		total = len(targets)
	)
	out := make([]*Target, 0, total)

	for _, t := range targets {
		if cancelled() {
			break
		}
		wg.Add(1)
		sem <- struct{}{}
		go func(t *Target) {
			defer wg.Done()
			defer func() { <-sem }()

			ok := probeTarget(t, domain)
			mu.Lock()
			if ok {
				out = append(out, t)
			}
			mu.Unlock()

			n := int(atomic.AddInt32(&done, 1))
			// 每 50 个或结束时回报一次，避免事件刷屏
			if onProgress != nil && (n%50 == 0 || n == total) {
				onProgress(n, total)
			}
		}(t)
	}
	wg.Wait()

	return out
}

// probeTarget 对单个目标做一次 trace 探测。
func probeTarget(t *Target, domain string) bool {
	hc := &http.Client{
		Timeout: probeTimeout,
		Transport: &http.Transport{
			// 把连接强制指向目标 IP，TLS 的 SNI 仍是 domain
			DialContext: getDialContext(t),
			// 只关心该 IP 是否服务这个域名，不校验证书链
			// （CF 边缘对所有 IP 都返回同一张证书，校验无意义）
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
		},
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	defer hc.CloseIdleConnections()

	req, err := http.NewRequest(http.MethodGet, "https://"+domain+probePath, nil)
	if err != nil {
		return false
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64)")

	resp, err := hc.Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return false
	}

	// trace 响应很小（约 200 字节），读 2KB 足够
	body, err := io.ReadAll(io.LimitReader(resp.Body, 2048))
	if err != nil {
		return false
	}
	return bytes.Contains(body, []byte("colo="))
}
