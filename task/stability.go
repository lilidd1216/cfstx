package task

import (
	"context"
	"crypto/tls"
	"io"
	"math"
	"net/http"
	"net/http/httptrace"
	"sync"
	"time"

	"gitee.com/liliaic/cfstx/utils"
)

// 稳定性测试的默认参数。
//
// 为什么需要这个功能：延迟测速只测 TCP 握手（或一次性 HEAD），
// 而实际使用要经过 TLS 握手 + WebSocket + Worker 执行。
// 实测中同一个 IP 的 TCP 只要 165ms，TLS 却要 473ms~4872ms，
// 且 5 次里有 3 次失败——这些在「平均延迟」里完全看不出来。
const (
	defaultStabilityRounds      = 10
	defaultStabilityConcurrency = 8
	// 单次请求超时。取值依据：实测最慢的一次 TLS 握手约 4.8 秒，
	// 留出余量避免把「慢但能用」的节点误判为不可用。
	stabilityRequestTimeout = 8 * time.Second
	// 连续失败这么多次就提前放弃，避免一个死节点占满 10 轮 × 8 秒。
	stabilityFailFast = 3
	// 长连接保活探测的间隔与次数。
	keepAliveInterval = 2 * time.Second
	keepAliveProbes   = 5
)

// StabilityDomain 是稳定性测试的目标域名，通常是用户自己的 Worker 域名。
//
// 用 Worker 域名而不是 speed.cloudflare.com 的原因：CF 的边缘会按 Host
// 做后端分流，只有真实域名才会走到和实际使用一致的路径。
// 为空时退回到 HttpingURL()（速度测速端点）。
var StabilityDomain string

// StabilityConfig 是稳定性测试的参数。
type StabilityConfig struct {
	Rounds      int  `json:"rounds"`      // 每个 IP 的采样轮数
	Concurrency int  `json:"concurrency"` // 同时测试的 IP 数量
	KeepAlive   bool `json:"keepAlive"`   // 是否额外做长连接保活探测
}

func (c *StabilityConfig) normalize() {
	if c.Rounds <= 0 {
		c.Rounds = defaultStabilityRounds
	}
	if c.Rounds > 50 {
		c.Rounds = 50
	}
	if c.Concurrency <= 0 {
		c.Concurrency = defaultStabilityConcurrency
	}
	if c.Concurrency > 50 {
		c.Concurrency = 50
	}
}

// StabilityResult 是单个 IP 的稳定性测试结果。
// 所有时延单位均为毫秒，0 表示无有效样本。
type StabilityResult struct {
	IP              string  `json:"ip"`
	Port            int     `json:"port"`
	Rounds          int     `json:"rounds"`      // 计划采样轮数
	Attempts        int     `json:"attempts"`    // 实际尝试次数（提前终止时小于 Rounds）
	Success         int     `json:"success"`     // 成功次数
	SuccessRate     float64 `json:"successRate"` // 成功率 0~1
	TCPAvg          float64 `json:"tcpAvg"`
	TCPJitter       float64 `json:"tcpJitter"` // 标准差
	TLSAvg          float64 `json:"tlsAvg"`
	TLSJitter       float64 `json:"tlsJitter"` // 标准差
	TotalAvg        float64 `json:"totalAvg"`
	TotalMax        float64 `json:"totalMax"`
	KeepAliveOK     bool    `json:"keepAliveOk"`
	KeepAliveTested bool    `json:"keepAliveTested"` // 是否真的做了长连接探测（区分「没测」和「测了但断」）
	Reused          int     `json:"reused"`          // 长连接复用次数
	Score           string  `json:"score"`           // S / A / B / C / D
	Error           string  `json:"error"`           // 全部失败时的最后一次错误
}

// stabilityURL 返回稳定性测试使用的 URL。
func stabilityURL() string {
	if StabilityDomain != "" {
		return "https://" + StabilityDomain + "/"
	}
	return HttpingURL()
}

// stabilityHost 返回 URL 对应的主机名，用于显式指定 SNI。
func stabilityHost() string {
	if StabilityDomain != "" {
		return StabilityDomain
	}
	// HttpingURL 必然是 https:// 开头，截到第一个 '/' 之后
	u := HttpingURL()
	if i := len("https://"); len(u) > i {
		rest := u[i:]
		for j := 0; j < len(rest); j++ {
			if rest[j] == '/' {
				return rest[:j]
			}
		}
		return rest
	}
	return ""
}

// measureOnce 对单个目标发一次 HEAD 请求，并用 httptrace 分别记录
// TCP 握手、TLS 握手和总耗时三段。
//
// 每次调用都使用独立的 Transport（DisableKeepAlives），保证拿到的是
// 全新建连的耗时——如果复用连接，TCP/TLS 会恒为 0，测出来毫无意义。
func measureOnce(t *Target, url, host string) (tcpDur, tlsDur, total time.Duration, err error) {
	var tcpStart, tlsStart time.Time

	trace := &httptrace.ClientTrace{
		ConnectStart: func(_, _ string) { tcpStart = time.Now() },
		ConnectDone: func(_, _ string, e error) {
			if e == nil && !tcpStart.IsZero() {
				tcpDur = time.Since(tcpStart)
			}
		},
		TLSHandshakeStart: func() { tlsStart = time.Now() },
		TLSHandshakeDone: func(_ tls.ConnectionState, e error) {
			if e == nil && !tlsStart.IsZero() {
				tlsDur = time.Since(tlsStart)
			}
		},
	}

	tr := &http.Transport{
		DialContext:         getDialContext(t),
		DisableKeepAlives:   true,
		TLSHandshakeTimeout: stabilityRequestTimeout,
		TLSClientConfig:     &tls.Config{ServerName: host},
	}
	defer tr.CloseIdleConnections()

	client := &http.Client{
		Transport: tr,
		Timeout:   stabilityRequestTimeout,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}

	ctx, cancel := context.WithTimeout(context.Background(), stabilityRequestTimeout)
	defer cancel()

	req, reqErr := http.NewRequestWithContext(
		httptrace.WithClientTrace(ctx, trace), http.MethodHead, url, nil)
	if reqErr != nil {
		return 0, 0, 0, reqErr
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36")

	begin := time.Now()
	resp, doErr := client.Do(req)
	total = time.Since(begin)
	if doErr != nil {
		return tcpDur, tlsDur, total, doErr
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)
	return tcpDur, tlsDur, total, nil
}

// measureKeepAlive 复用同一条连接连发多次 HEAD，用来判断长连接会不会被中途切断。
//
// 这是「下载到一半断掉」最直接的探针：如果连接被 RST，下一次请求会重新拨号，
// GotConnInfo.Reused 就会变成 false。返回复用成功次数。
func measureKeepAlive(t *Target, url, host string, probes int) (reused int, ok bool) {
	tr := &http.Transport{
		DialContext:         getDialContext(t),
		MaxIdleConnsPerHost: 1,
		IdleConnTimeout:     30 * time.Second,
		DisableKeepAlives:   false,
		TLSHandshakeTimeout: stabilityRequestTimeout,
		TLSClientConfig:     &tls.Config{ServerName: host},
	}
	defer tr.CloseIdleConnections()

	client := &http.Client{
		Transport: tr,
		Timeout:   stabilityRequestTimeout,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}

	ok = true
	for i := 0; i < probes; i++ {
		var wasReused bool
		trace := &httptrace.ClientTrace{
			GotConn: func(info httptrace.GotConnInfo) { wasReused = info.Reused },
		}
		ctx, cancel := context.WithTimeout(context.Background(), stabilityRequestTimeout)
		req, err := http.NewRequestWithContext(
			httptrace.WithClientTrace(ctx, trace), http.MethodHead, url, nil)
		if err != nil {
			cancel()
			ok = false
			break
		}
		req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36")
		resp, err := client.Do(req)
		if err != nil {
			cancel()
			ok = false
			break
		}
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
		cancel()

		if wasReused {
			reused++
		}
		// 最后一次不用再等
		if i < probes-1 {
			time.Sleep(keepAliveInterval)
		}
	}
	return reused, ok
}

// statsOf 返回平均值与标准差（抖动）。样本不足时标准差记 0。
func statsOf(vals []float64) (avg, jitter float64) {
	n := len(vals)
	if n == 0 {
		return 0, 0
	}
	var sum float64
	for _, v := range vals {
		sum += v
	}
	avg = sum / float64(n)
	if n < 2 {
		return avg, 0
	}
	var sq float64
	for _, v := range vals {
		d := v - avg
		sq += d * d
	}
	jitter = math.Sqrt(sq / float64(n))
	return avg, jitter
}

// scoreOf 把稳定性结果折算成 S/A/B/C/D 评级。
//
// 评分口径是照着「mihomo url-test 会怎么选」来定的：
// url-test 只看单次延迟最低，完全不知道成功率。所以这里把成功率
// 作为第一权重，其次是 TLS 阶段的表现（因为它决定实际观感）。
//
// 长连接断裂单独重罚：能连上、能测速、但传数据会断——这正是
// 「下载到一半断掉」的形态。这种 IP 拿来测速很漂亮，用来下载很痛苦，
// 所以即便成功率和延迟都满分，也要压到 C。
func scoreOf(r *StabilityResult) string {
	// 一个样本都没成功，直接判死
	if r.Success == 0 {
		return "D"
	}

	base := scoreByRate(r)

	// 长连接测过但没撑住：下载场景等价于不可用
	if r.KeepAliveTested && !r.KeepAliveOK {
		if base == "S" || base == "A" || base == "B" {
			return "C"
		}
	}
	return base
}

func scoreByRate(r *StabilityResult) string {
	switch {
	case r.SuccessRate < 0.6:
		return "D"
	case r.SuccessRate < 0.8:
		return "C"
	case r.SuccessRate < 1.0:
		// 偶有失败：TLS 再慢就降一档
		if r.TLSAvg > 2000 {
			return "C"
		}
		return "B"
	}
	// 成功率 100%，按 TLS 阶段质量和抖动分档
	if r.TLSAvg > 2000 {
		return "C"
	}
	if r.TLSAvg > 1000 {
		return "B"
	}
	// 抖动要从多个样本才算得出来。只测 1~2 次时标准差恒为 0，
	// 直接判 S 等于把「没验证过」当成「很稳定」，会误导用户。
	if r.Success < 3 {
		return "A"
	}
	if r.TLSJitter < 150 && r.TCPJitter < 60 && r.TLSAvg < 800 {
		return "S"
	}
	return "A"
}

// testOneStability 对单个目标执行完整的稳定性测试。
func testOneStability(t *Target, cfg StabilityConfig, url, host string) *StabilityResult {
	r := &StabilityResult{
		IP:     t.IP.String(),
		Port:   t.Port,
		Rounds: cfg.Rounds,
	}

	var (
		tcpVals    []float64
		tlsVals    []float64
		totalVals  []float64
		consecFail int
	)

	for i := 0; i < cfg.Rounds; i++ {
		if cancelled() {
			break
		}
		r.Attempts++
		tcpDur, tlsDur, total, err := measureOnce(t, url, host)
		if err != nil {
			consecFail++
			r.Error = err.Error()
			// 连续失败足够多次，说明这个节点已经不可用，没必要耗满全程
			if consecFail >= stabilityFailFast {
				break
			}
			continue
		}
		consecFail = 0
		r.Success++
		if tcpDur > 0 {
			tcpVals = append(tcpVals, float64(tcpDur.Milliseconds()))
		}
		if tlsDur > 0 {
			tlsVals = append(tlsVals, float64(tlsDur.Milliseconds()))
		}
		totalVals = append(totalVals, float64(total.Milliseconds()))
	}

	if r.Attempts > 0 {
		r.SuccessRate = float64(r.Success) / float64(r.Attempts)
	}
	r.TCPAvg, r.TCPJitter = statsOf(tcpVals)
	r.TLSAvg, r.TLSJitter = statsOf(tlsVals)
	r.TotalAvg, _ = statsOf(totalVals)
	for _, v := range totalVals {
		if v > r.TotalMax {
			r.TotalMax = v
		}
	}

	// 只有还有效的节点才值得为长连接再花时间
	if cfg.KeepAlive && r.Success > 0 && !cancelled() {
		r.KeepAliveTested = true
		reused, ok := measureKeepAlive(t, url, host, keepAliveProbes)
		r.Reused = reused
		// 复用次数达到探测次数-2 以上才算长连接健康（允许首连和一次意外）
		r.KeepAliveOK = ok && reused >= keepAliveProbes-2
	}

	r.Score = scoreOf(r)
	return r
}

// TestStability 对目标列表并发执行稳定性测试。
// onResult 每完成一个 IP 就回调一次，用于增量推送给前端。
func TestStability(targets []*Target, cfg StabilityConfig, onResult func(*StabilityResult)) []StabilityResult {
	cfg.normalize()
	results := make([]StabilityResult, 0, len(targets))
	if len(targets) == 0 {
		return results
	}

	url := stabilityURL()
	host := stabilityHost()

	utils.Cyan.Printf("开始稳定性测试（目标：%s，轮数：%d，并发：%d）\n", url, cfg.Rounds, cfg.Concurrency)

	sem := make(chan struct{}, cfg.Concurrency)
	var (
		wg sync.WaitGroup
		mu sync.Mutex
	)
	for _, t := range targets {
		if cancelled() {
			break
		}
		wg.Add(1)
		sem <- struct{}{}
		go func(t *Target) {
			defer wg.Done()
			defer func() { <-sem }()
			r := testOneStability(t, cfg, url, host)
			mu.Lock()
			results = append(results, *r)
			mu.Unlock()
			if onResult != nil {
				onResult(r)
			}
		}(t)
	}
	wg.Wait()

	// 按评分从好到坏、再按成功率降序排列，方便直接取前几个用
	rank := map[string]int{"S": 0, "A": 1, "B": 2, "C": 3, "D": 4}
	sortStability(results, rank)
	return results
}

// SortStabilityResults 按「评分从好到坏、同分按成功率降序」原地排序。
//
// 供上层在结果收集完后调用：TestStability 的返回值已经排好序，
// 但上层是通过 onResult 回调逐条累积的，拿到的是完成顺序。
func SortStabilityResults(rs []StabilityResult) {
	rank := map[string]int{"S": 0, "A": 1, "B": 2, "C": 3, "D": 4}
	sortStability(rs, rank)
}

func sortStability(rs []StabilityResult, rank map[string]int) {
	for i := 1; i < len(rs); i++ {
		for j := i; j > 0; j-- {
			a, b := rs[j-1], rs[j]
			if rank[a.Score] < rank[b.Score] {
				break
			}
			if rank[a.Score] == rank[b.Score] && a.SuccessRate >= b.SuccessRate {
				break
			}
			rs[j-1], rs[j] = rs[j], rs[j-1]
		}
	}
}
