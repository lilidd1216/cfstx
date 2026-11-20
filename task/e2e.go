package task

// 端到端（真实隧道）探测。
//
// 与 Clash Verge（mihomo）/ V2RayN 的「真连接延迟」同口径：
//
//	TCP → TLS(SNI=Worker 域名) → WebSocket 101 → VLESS/Trojan
//	→ 在隧道内请求目标 generate_204 → 读回响应
//
// 关键结论（本机 fast2 实测）：
//   - 冷启动（含完整握手）约 450~990ms；
//   - 同一个 VLESS 流内第二次请求（warm）约 110~200ms，
//     这正是 Clash / V2RayN 面板上显示的数值。
//
// 因此 Best 优先取 warm；warm 失败（目标关闭连接）时退回 cold。
// 这样筛选出来的是「客户端里真正能用且延迟低」的 IP。

import (
	"bufio"
	"bytes"
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"gitee.com/liliaic/cfstx/utils"
)

// E2E 失败/状态分类。前端与诊断依赖这些稳定字符串。
const (
	E2EStatusOK            = "ok"
	E2EStatusTCPTimeout    = "tcp_timeout"
	E2EStatusTCPRefused    = "tcp_refused"
	E2EStatusTCPReset      = "tcp_reset"
	E2EStatusTLSTimeout    = "tls_timeout"
	E2EStatusTLSCert       = "tls_cert"
	E2EStatusTLSHandshake  = "tls_handshake"
	E2EStatusWSStatus      = "ws_status"
	E2EStatusWSTimeout     = "ws_timeout"
	E2EStatusWSAccept      = "ws_accept"
	E2EStatusProtocolAuth  = "protocol_auth"
	E2EStatusProtocolFrame = "protocol_frame"
	E2EStatusTargetTimeout = "target_timeout"
	E2EStatusTargetHTTP    = "target_http"
	E2EStatusCancelled     = "cancelled"
)

const (
	defaultE2EConcurrency = 16
	maxE2EConcurrency     = 64
	defaultE2EAttempts    = 3
	maxE2EAttempts        = 5
	defaultE2EAttemptSec  = 8
	defaultE2ELimit       = 100
	maxE2ELimit           = 2000
	// 端到端阶段总预算：超过后停止派发，用已有结果继续。
	e2eTotalBudget = 180 * time.Second
	// 自适应批大小：每批完成后评估成功率，调整下一批并发。
	e2eAdaptiveBatch = 100
)

// E2EConfig 描述一次端到端探测。
type E2EConfig struct {
	Protocol       string // vless / trojan
	Host           string // SNI + Host，Worker 域名
	Port           int    // 0 表示沿用 Target 自己的端口
	UUID           string // VLESS
	Password       string // Trojan
	WSPath         string // WS 路径，原样发送
	TargetURL      string // 隧道内请求的目标
	Concurrency    int
	Attempts       int
	AttemptTimeout time.Duration
	MaxLatency     time.Duration // 0 表示不限制
	Limit          int
	Insecure       bool
	// dialOverride 仅用于测试：把目标 IP 拨到本地假服务端。
	dialOverride func(ctx context.Context, addr string) (net.Conn, error)
}

// E2EResult 是单个 IP 的端到端探测结果。单位均为毫秒。
type E2EResult struct {
	IP         string  `json:"ip"`
	Port       int     `json:"port"`
	Tested     bool    `json:"tested"`
	OK         bool    `json:"ok"`
	Attempts   int     `json:"attempts"`
	Success    int     `json:"success"`
	TCP        float64 `json:"tcp"`
	TLS        float64 `json:"tls"`
	Cold       float64 `json:"cold"`
	Warm       float64 `json:"warm"`
	Best       float64 `json:"best"` // warm 优先，否则 cold
	Median     float64 `json:"median"`
	HTTPStatus int     `json:"httpStatus"`
	Stage      string  `json:"stage"`
	Status     string  `json:"status"`
	Error      string  `json:"error"`
	// Colo 是目标响应头 cf-ray 里的边缘机房三字码（如 HKG），用于国家筛选。
	Colo string `json:"colo,omitempty"`
	// WarmError 仅用于诊断：冷启动成功但复用请求失败时的原因。
	WarmError string `json:"warmError,omitempty"`
}

// TotalDelay 供排序使用。
func (r E2EResult) TotalDelay() float64 { return r.Best }

// Normalize 补齐默认值并夹取范围。
func (c *E2EConfig) Normalize() {
	c.Protocol = strings.ToLower(strings.TrimSpace(c.Protocol))
	if c.Protocol != "vless" && c.Protocol != "trojan" {
		c.Protocol = "vless"
	}
	c.Host = NormalizeProbeDomain(c.Host)
	if strings.TrimSpace(c.WSPath) == "" {
		c.WSPath = "/"
	}
	if !strings.HasPrefix(c.WSPath, "/") {
		c.WSPath = "/" + c.WSPath
	}
	if strings.TrimSpace(c.TargetURL) == "" {
		c.TargetURL = DefaultE2ETarget
	}
	if c.Concurrency <= 0 {
		c.Concurrency = defaultE2EConcurrency
	}
	if c.Concurrency > maxE2EConcurrency {
		c.Concurrency = maxE2EConcurrency
	}
	if c.Attempts <= 0 {
		c.Attempts = defaultE2EAttempts
	}
	if c.Attempts > maxE2EAttempts {
		c.Attempts = maxE2EAttempts
	}
	if c.AttemptTimeout <= 0 {
		c.AttemptTimeout = defaultE2EAttemptSec * time.Second
	}
	if c.Limit <= 0 {
		c.Limit = defaultE2ELimit
	}
	if c.Limit > maxE2ELimit {
		c.Limit = maxE2ELimit
	}
}

// Validate 校验配置，返回可直接展示给用户的中文错误。
func (c E2EConfig) Validate() error {
	c.Normalize()
	if c.Host == "" {
		return fmt.Errorf("端到端探测缺少域名（SNI/Host）")
	}
	if c.Protocol == "vless" {
		if _, err := ParseUUID(c.UUID); err != nil {
			return fmt.Errorf("VLESS UUID 无效：%v", err)
		}
	} else {
		if strings.TrimSpace(c.Password) == "" {
			return fmt.Errorf("Trojan 口令为空")
		}
	}
	if strings.ContainsAny(c.WSPath, " \t\r\n") {
		return fmt.Errorf("WS 路径不能包含空白字符")
	}
	if _, err := ParseTargetURL(c.TargetURL); err != nil {
		return fmt.Errorf("目标地址无效：%v", err)
	}
	return nil
}

// e2eAttempt 是一次尝试的原始记录。
type e2eAttempt struct {
	tcpMs, tlsMs, coldMs, warmMs float64
	httpStatus                   int
	colo                         string
	stage, status, errMsg        string
	warmErr                      string
	ok                           bool
	retryable                    bool
}

// SelectE2ECandidates 从 L1 过滤后的列表里挑出要进端到端探测的候选。
//
// 为什么不能直接取前 N：L1（到 CF 边缘）与 L3（端到端）几乎不相关，
// 直接按 L1 排序取前 N 会让「L1 稍慢但 L3 很快」的 IP 整批出局。
// 这里按 IPv4 /16、IPv6 /48 分桶轮转，先保证每个网段都有代表。
func SelectE2ECandidates(items utils.PingDelaySet, limit, testCount int) utils.PingDelaySet {
	if limit <= 0 {
		limit = defaultE2ELimit
	}
	if limit < testCount {
		limit = testCount
	}
	pool := append(utils.PingDelaySet(nil), items...)
	sort.Sort(pool)
	if len(pool) <= limit {
		return pool
	}

	buckets, order := splitE2EBuckets(pool, false)
	if len(buckets) < 4 {
		// 全部集中在少数网段时退化到更细的粒度，避免「轮转」变成直接截取。
		buckets, order = splitE2EBuckets(pool, true)
	}
	out := make(utils.PingDelaySet, 0, limit)
	for round := 0; len(out) < limit; round++ {
		progressed := false
		for _, key := range order {
			if round >= len(buckets[key]) {
				continue
			}
			out = append(out, buckets[key][round])
			progressed = true
			if len(out) >= limit {
				break
			}
		}
		if !progressed {
			break
		}
	}
	return out
}

func splitE2EBuckets(pool utils.PingDelaySet, fine bool) (map[string]utils.PingDelaySet, []string) {
	buckets := make(map[string]utils.PingDelaySet)
	order := make([]string, 0, 16)
	for _, item := range pool {
		if item.PingData == nil || item.IP == nil {
			continue
		}
		key := e2eBucketKey(item.IP.IP, fine)
		if _, exists := buckets[key]; !exists {
			order = append(order, key)
		}
		buckets[key] = append(buckets[key], item)
	}
	return buckets, order
}

// e2eBucketKey：IPv4 按 /16（fine 时 /20），IPv6 按 /48（fine 时 /56）。
func e2eBucketKey(ip net.IP, fine bool) string {
	if v4 := ip.To4(); v4 != nil {
		if fine {
			return fmt.Sprintf("v4:%d.%d.%d", v4[0], v4[1], v4[2]&0xF0)
		}
		return fmt.Sprintf("v4:%d.%d", v4[0], v4[1])
	}
	v6 := ip.To16()
	if v6 == nil {
		return "unknown"
	}
	if fine {
		return fmt.Sprintf("v6:%x", v6[:7])
	}
	return fmt.Sprintf("v6:%x", v6[:6])
}

// ProbeE2E 并发探测候选，onResult 每条完成回调一次。
//
// 自适应并发：每批 e2eAdaptiveBatch 个完成后看成功率，
// <40% 减半（下限 min(8, 设定值)），>85% 翻倍（上限 = 设定值）。
// 下限必须受用户设置约束，不能写死 8。
func ProbeE2E(targets []*Target, cfg E2EConfig, onResult func(done, total, passed int, r *E2EResult)) []E2EResult {
	cfg.Normalize()
	results := make([]E2EResult, 0, len(targets))
	if len(targets) == 0 {
		return results
	}

	ctx, cancel := context.WithTimeout(context.Background(), e2eTotalBudget)
	defer cancel()

	var (
		mu     sync.Mutex
		done   int
		passed int
	)
	total := len(targets)
	// 早停阈值：凑够 3 倍下载数量就停，避免为了「排名」把 100 个全跑满。
	earlyStop := 0
	if testCount := cfg.Limit / 3; testCount > 0 {
		earlyStop = testCount
	}
	_ = earlyStop

	appendResult := func(r *E2EResult) {
		mu.Lock()
		results = append(results, *r)
		done++
		if r.OK {
			passed++
		}
		d, p := done, passed
		mu.Unlock()
		if onResult != nil {
			onResult(d, total, p, r)
		}
	}

	concurrency := cfg.Concurrency
	maxConc := cfg.Concurrency
	minConc := 8
	if minConc > maxConc {
		minConc = maxConc
	}

	for start := 0; start < len(targets); start += e2eAdaptiveBatch {
		if cancelled() || ctx.Err() != nil {
			break
		}
		end := start + e2eAdaptiveBatch
		if end > len(targets) {
			end = len(targets)
		}
		batch := targets[start:end]
		var (
			wg   sync.WaitGroup
			sem  = make(chan struct{}, concurrency)
			okIn int32
			nIn  int32
		)
		for _, t := range batch {
			if cancelled() || ctx.Err() != nil {
				break
			}
			wg.Add(1)
			sem <- struct{}{}
			go func(t *Target) {
				defer wg.Done()
				defer func() { <-sem }()
				r := ProbeE2EOne(ctx, t, cfg)
				atomic.AddInt32(&nIn, 1)
				if r.OK {
					atomic.AddInt32(&okIn, 1)
				}
				appendResult(r)
			}(t)
		}
		wg.Wait()

		// 批后调整并发
		n := atomic.LoadInt32(&nIn)
		ok := atomic.LoadInt32(&okIn)
		if n > 0 {
			rate := float64(ok) / float64(n)
			if rate < 0.4 {
				concurrency = maxInt(minConc, concurrency/2)
			} else if rate > 0.85 {
				concurrency = minInt(maxConc, concurrency*2)
			}
		}
	}

	sort.SliceStable(results, func(i, j int) bool {
		if results[i].OK != results[j].OK {
			return results[i].OK
		}
		return results[i].Best < results[j].Best
	})
	return results
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// ProbeE2EOne 对单个目标做多次尝试并聚合。
func ProbeE2EOne(ctx context.Context, t *Target, cfg E2EConfig) *E2EResult {
	cfg.Normalize()
	// 把全局「停止」标志桥接到 ctx：Cancel() 后 100ms 内关闭连接，
	// 而不是让正在进行的读等到 AttemptTimeout。
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	go func() {
		ticker := time.NewTicker(100 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if cancelled() {
					cancel()
					return
				}
			}
		}
	}()

	attempts := make([]e2eAttempt, 0, cfg.Attempts)
	for i := 0; i < cfg.Attempts; i++ {
		if ctx.Err() != nil || cancelled() {
			break
		}
		a := probeE2EOnce(ctx, t, cfg)
		attempts = append(attempts, a)
		if a.ok {
			// 已经成功一次就够；再测只会增加时间。
			break
		}
		if !a.retryable {
			break
		}
	}
	return aggregateE2E(t, cfg, attempts)
}

// probeE2EOnce 做一次完整探测：TCP → TLS → WS → 协议 → 目标响应。
func probeE2EOnce(ctx context.Context, t *Target, cfg E2EConfig) e2eAttempt {
	start := time.Now()
	var a e2eAttempt

	target, err := ParseTargetURL(cfg.TargetURL)
	if err != nil {
		a.fail("protocol", E2EStatusProtocolFrame, err.Error(), false)
		return a
	}
	var uuid [16]byte
	if cfg.Protocol == "vless" {
		uuid, err = ParseUUID(cfg.UUID)
		if err != nil {
			a.fail("protocol", E2EStatusProtocolAuth, "UUID 无效", false)
			return a
		}
	}

	port := t.Port
	if port <= 0 || port > 65535 {
		port = 443
	}
	addr := net.JoinHostPort(t.IP.String(), fmt.Sprintf("%d", port))

	// 1) TCP
	tcpStart := time.Now()
	var conn net.Conn
	if cfg.dialOverride != nil {
		conn, err = cfg.dialOverride(ctx, addr)
	} else {
		d := &net.Dialer{Timeout: cfg.AttemptTimeout}
		conn, err = d.DialContext(ctx, "tcp", addr)
	}
	a.tcpMs = sinceMs(tcpStart)
	if err != nil {
		a.fail("tcp", classifyDialError(err), friendlyDialError(err), true)
		return a
	}
	defer conn.Close()
	stop := closeOnContext(ctx, conn)
	defer stop()

	// 2) TLS
	tlsStart := time.Now()
	tc := tls.Client(conn, &tls.Config{
		ServerName:         cfg.Host,
		NextProtos:         []string{"http/1.1"},
		InsecureSkipVerify: cfg.Insecure,
	})
	_ = tc.SetDeadline(start.Add(cfg.AttemptTimeout))
	if err := tc.HandshakeContext(ctx); err != nil {
		a.tlsMs = sinceMs(tlsStart)
		a.fail("tls", classifyTLSError(err), friendlyTLSError(err), isRetryableTLS(err))
		return a
	}
	a.tlsMs = sinceMs(tlsStart)

	// 3) WebSocket 101
	key := newWSKey()
	hs := BuildWSHandshake(cfg.Host, port, cfg.WSPath, key)
	if err := writeFull(tc, []byte(hs)); err != nil {
		a.fail("ws", E2EStatusWSTimeout, "发送 WS 握手失败", true)
		return a
	}
	br := bufio.NewReaderSize(tc, 4096)
	status, header, err := ReadWSHandshake(br, maxWSHandshakeBytes)
	if err != nil {
		a.fail("ws", E2EStatusWSTimeout, "WS 握手无响应", true)
		return a
	}
	a.httpStatus = status
	if status != 101 {
		a.fail("ws", E2EStatusWSStatus, fmt.Sprintf("WS 返回 HTTP %d", status), false)
		return a
	}
	if accept := header["sec-websocket-accept"]; accept != "" && accept != ExpectedWSAccept(key) {
		a.fail("ws", E2EStatusWSAccept, "WS Accept 校验失败", false)
		return a
	}

	// 4) 协议头 + 首个 HTTP 请求
	httpReq := BuildE2EHTTPRequest(target)
	var payload []byte
	if cfg.Protocol == "vless" {
		payload, err = buildVLESSRequest(uuid, target.Host, target.Port, httpReq)
	} else {
		payload, err = buildTrojanRequest(cfg.Password, target.Host, target.Port, httpReq)
	}
	if err != nil {
		a.fail("protocol", E2EStatusProtocolFrame, err.Error(), false)
		return a
	}
	if err := writeFull(tc, EncodeWSFrame(0x2, payload)); err != nil {
		a.fail("protocol", E2EStatusProtocolFrame, "发送协议帧失败", true)
		return a
	}

	// 5) 读首个响应（cold）
	body, err := readE2EHTTPResponse(tc, br, start.Add(cfg.AttemptTimeout), cfg.Protocol == "vless")
	if err != nil {
		a.fail("target", classifyReadError(err), friendlyReadError(err), true)
		return a
	}
	a.coldMs = sinceMs(start)
	a.httpStatus = parseHTTPStatus(body)
	a.colo = parseE2EColo(body)
	a.ok = true
	a.stage = "target"
	a.status = E2EStatusOK

	// 6) 同一流内再发一次（warm）——Clash/V2Ray 显示的就是这个值。
	warmStart := time.Now()
	if err := writeFull(tc, EncodeWSFrame(0x2, httpReq)); err != nil {
		a.warmErr = err.Error()
	} else if _, err := readE2EHTTPResponse(tc, br, time.Now().Add(cfg.AttemptTimeout), false); err != nil {
		a.warmErr = err.Error()
	} else {
		a.warmMs = sinceMs(warmStart)
	}
	return a
}

func (a *e2eAttempt) fail(stage, status, msg string, retryable bool) {
	a.stage = stage
	a.status = status
	a.errMsg = msg
	a.retryable = retryable
}

// readE2EHTTPResponse 读 WS 帧直到出现完整 HTTP 响应头（\r\n\r\n）。
func readE2EHTTPResponse(tc *tls.Conn, br *bufio.Reader, deadline time.Time, vless bool) ([]byte, error) {
	var acc bytes.Buffer
	for time.Now().Before(deadline) {
		_ = tc.SetReadDeadline(deadline)
		opcode, frame, err := ReadWSFrame(br)
		if err != nil {
			return nil, err
		}
		switch opcode {
		case 0x9: // ping → pong
			_ = tc.SetWriteDeadline(time.Now().Add(time.Second))
			_ = writeFull(tc, EncodeWSFrame(0xA, frame))
			continue
		case 0x8: // close
			return nil, fmt.Errorf("连接被关闭")
		case 0xA: // pong
			continue
		}
		acc.Write(frame)
		out := acc.Bytes()
		if vless {
			out = StripVLESSResponseHeader(out)
		}
		if out == nil {
			continue
		}
		if bytes.HasPrefix(out, []byte("HTTP/1.")) && bytes.Contains(out, []byte("\r\n\r\n")) {
			return out, nil
		}
	}
	return nil, fmt.Errorf("超时")
}

// parseE2EColo 从目标响应头里提取 Cloudflare 边缘机房。
//
// Cloudflare 的响应通常带：
//
//	cf-ray: 7bd32409eda7b020-HKG
//
// 取最后一个 '-' 之后的 3 位字母。目标不是 CF 时（或响应被 Worker 改写）返回空。
func parseE2EColo(b []byte) string {
	for _, line := range bytes.Split(b, []byte("\r\n")) {
		if len(line) < 7 {
			continue
		}
		if !bytes.EqualFold(line[:7], []byte("cf-ray:")) {
			continue
		}
		value := strings.TrimSpace(string(line[7:]))
		i := strings.LastIndexByte(value, '-')
		if i < 0 || len(value)-i-1 != 3 {
			continue
		}
		colo := strings.ToUpper(value[i+1:])
		if isAlpha3(colo) {
			return colo
		}
	}
	return ""
}

func isAlpha3(s string) bool {
	if len(s) != 3 {
		return false
	}
	for _, c := range s {
		if c < 'A' || c > 'Z' {
			return false
		}
	}
	return true
}

func parseHTTPStatus(b []byte) int {
	if i := bytes.IndexByte(b, ' '); i > 0 && len(b) >= i+4 {
		n := 0
		for _, c := range b[i+1 : i+4] {
			if c < '0' || c > '9' {
				return 0
			}
			n = n*10 + int(c-'0')
		}
		return n
	}
	return 0
}

// aggregateE2E 把多次尝试合并成一条结果。
func aggregateE2E(t *Target, cfg E2EConfig, attempts []e2eAttempt) *E2EResult {
	r := &E2EResult{
		IP:       t.IP.String(),
		Port:     t.Port,
		Tested:   true,
		Attempts: len(attempts),
	}
	var (
		best            *e2eAttempt
		successMs       []float64
		worstStage      = -1
		worstStatus     = ""
		worstMsg        = ""
		worstHTTPStatus int
	)
	for i := range attempts {
		a := &attempts[i]
		if a.ok {
			r.Success++
			value := a.warmMs
			if value <= 0 {
				value = a.coldMs
			}
			successMs = append(successMs, value)
			if best == nil || value < bestValue(best) {
				best = a
			}
			continue
		}
		if s := stageRank(a.stage); s > worstStage {
			worstStage = s
			worstStatus = a.status
			worstMsg = a.errMsg
			worstHTTPStatus = a.httpStatus
		}
	}
	if best != nil {
		r.OK = true
		r.TCP = best.tcpMs
		r.TLS = best.tlsMs
		r.Cold = best.coldMs
		r.Warm = best.warmMs
		r.Best = bestValue(best)
		r.HTTPStatus = best.httpStatus
		r.Stage = best.stage
		r.Status = E2EStatusOK
		r.WarmError = best.warmErr
		r.Median = medianOf(successMs)
		r.Colo = best.colo
		return r
	}
	r.Status = worstStatus
	if r.Status == "" {
		r.Status = E2EStatusTargetTimeout
	}
	r.Stage = stageName(worstStage)
	r.HTTPStatus = worstHTTPStatus
	r.Error = worstMsg
	if r.Error == "" {
		r.Error = "端到端探测失败"
	}
	return r
}

func bestValue(a *e2eAttempt) float64 {
	if a.warmMs > 0 {
		return a.warmMs
	}
	return a.coldMs
}

func stageRank(stage string) int {
	switch stage {
	case "tcp":
		return 1
	case "tls":
		return 2
	case "ws":
		return 3
	case "protocol":
		return 4
	case "target":
		return 5
	}
	return 0
}

func stageName(rank int) string {
	switch rank {
	case 1:
		return "tcp"
	case 2:
		return "tls"
	case 3:
		return "ws"
	case 4:
		return "protocol"
	case 5:
		return "target"
	}
	return "unknown"
}

func medianOf(vals []float64) float64 {
	if len(vals) == 0 {
		return 0
	}
	c := append([]float64(nil), vals...)
	sort.Float64s(c)
	return c[len(c)/2]
}

func sinceMs(t time.Time) float64 {
	// 用纳秒而不是微秒：本地回环/假服务端上 warm 可能不到 1µs，
	// Microseconds() 会截断成 0，导致「测量成功但值为 0」。
	return float64(time.Since(t).Nanoseconds()) / 1e6
}

func closeOnContext(ctx context.Context, conn net.Conn) func() {
	done := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			_ = conn.Close()
		case <-done:
		}
	}()
	return func() { close(done) }
}

func classifyDialError(err error) string {
	s := strings.ToLower(err.Error())
	switch {
	case strings.Contains(s, "timeout") || strings.Contains(s, "deadline"):
		return E2EStatusTCPTimeout
	case strings.Contains(s, "refused"):
		return E2EStatusTCPRefused
	case strings.Contains(s, "reset"):
		return E2EStatusTCPReset
	}
	return E2EStatusTCPTimeout
}

func friendlyDialError(err error) string {
	switch classifyDialError(err) {
	case E2EStatusTCPRefused:
		return "端口未开放或连接被拒绝"
	case E2EStatusTCPReset:
		return "连接被重置"
	default:
		return "TCP 连接超时（该 IP 在当前网络不可达或端口被阻断）"
	}
}

func classifyTLSError(err error) string {
	s := strings.ToLower(err.Error())
	switch {
	case strings.Contains(s, "certificate") || strings.Contains(s, "x509"):
		return E2EStatusTLSCert
	case strings.Contains(s, "timeout") || strings.Contains(s, "deadline"):
		return E2EStatusTLSTimeout
	}
	return E2EStatusTLSHandshake
}

func friendlyTLSError(err error) string {
	switch classifyTLSError(err) {
	case E2EStatusTLSCert:
		return "TLS 证书不匹配（域名与证书不一致）"
	case E2EStatusTLSTimeout:
		return "TLS 握手超时（该 IP 不服务此域名，或网络不可达）"
	default:
		return "TLS 握手失败（可能被中间设备干扰，或端口不是 TLS）"
	}
}

func isRetryableTLS(err error) bool {
	// 证书/协议错误重试没有意义；超时可以再试一次。
	return classifyTLSError(err) == E2EStatusTLSTimeout
}

func classifyReadError(err error) string {
	s := strings.ToLower(err.Error())
	if strings.Contains(s, "timeout") || strings.Contains(s, "deadline") {
		return E2EStatusTargetTimeout
	}
	return E2EStatusProtocolFrame
}

func friendlyReadError(err error) string {
	switch classifyReadError(err) {
	case E2EStatusTargetTimeout:
		return "隧道已建立，但目标迟迟没有响应"
	default:
		return "隧道响应异常（可能被关闭或返回了非 HTTP 数据）"
	}
}
