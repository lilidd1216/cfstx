package main

import (
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"gitee.com/liliaic/cfstx/task"
	"gitee.com/liliaic/cfstx/utils"
	"gitee.com/liliaic/cfstx/version"
)

// defaultTestURL 是 CF 官方测速端点。
// 原 https://cf.xiu2.xyz/url 已被 Cloudflare 封禁（403），旧配置会在读取时自动迁移。
// CF 对该端点单次请求限制 100MB（>= 100000000 返回 403），这里取 90MB。
const defaultTestURL = "https://speed.cloudflare.com/__down?bytes=90000000"

// IP 来源标识
const (
	sourceIPv4      = "ipv4"      // 内置 ip.txt（可替换为自定义文件）
	sourceIPv6      = "ipv6"      // 内置 ipv6.txt
	sourceBestCF    = "bestcf"    // 在线优选 IP（bestcf.pages.dev）
	sourceOfficial  = "official"  // Cloudflare 官方 IP 段 API
	sourceCmliu     = "cmliu"     // cmliu 整理的 CF-CIDR 列表
	sourceCFDomains = "cfdomains" // BestCF 维护的 CF 官方域名池
	sourceCustom    = "custom"    // 用户自定义的在线 IP 列表 URL
	sourceManual    = "manual"    // 手动输入的 IP 段
)

// Config 是界面上可调整的全部测速参数。
type Config struct {
	// 延迟测速
	Threads     int    `json:"threads"`
	PingTimes   int    `json:"pingTimes"`
	Port        int    `json:"port"`
	Httping     bool   `json:"httping"`
	HttpingCode int    `json:"httpingCode"`
	Colo        string `json:"colo"`
	// ProbeEnabled 开启后，会先用 ProbeDomain 的 /cdn-cgi/trace 探测，
	// 剔除不服务该域名的 IP。该路径绕过 Worker，不消耗 Workers 配额。
	ProbeEnabled bool   `json:"probeEnabled"`
	ProbeDomain  string `json:"probeDomain"`

	// IP 来源
	Sources       []string `json:"sources"`       // ipv4 / ipv6 / bestcf / custom / manual
	IPFile        string   `json:"ipFile"`        // IPv4 数据文件，默认 ip.txt，可自定义
	IPText        string   `json:"ipText"`        // 手动指定的 IP 段
	CustomSources string   `json:"customSources"` // 自定义在线源，每行一个 URL

	// 过滤条件
	MaxDelay    int     `json:"maxDelay"`
	MinDelay    int     `json:"minDelay"`
	MaxLossRate float64 `json:"maxLossRate"`
	MinSpeed    float64 `json:"minSpeed"`

	// 下载测速
	DownloadTime    int    `json:"downloadTime"`
	TestCount       int    `json:"testCount"`
	DisableDownload bool   `json:"disableDownload"`
	TestAll         bool   `json:"testAll"`
	URL             string `json:"url"`

	// 输出
	OutputFile string `json:"outputFile"`
	PrintNum   int    `json:"printNum"`
	Debug      bool   `json:"debug"`
}

// ResultItem 是单条测速结果。
type ResultItem struct {
	IP       string  `json:"ip"`
	Port     int     `json:"port"`
	Sent     int     `json:"sent"`
	Received int     `json:"received"`
	LossRate float64 `json:"lossRate"`
	Delay    float64 `json:"delay"`
	Speed    float64 `json:"speed"`
	Colo     string  `json:"colo"`
}

// HistoryRecord 是一次完整测速的历史记录。
type HistoryRecord struct {
	ID        int64        `json:"id"`
	Time      string       `json:"time"`
	Duration  float64      `json:"duration"`
	Count     int          `json:"count"`
	BestIP    string       `json:"bestIp"`
	BestSpeed float64      `json:"bestSpeed"`
	Config    Config       `json:"config"`
	Results   []ResultItem `json:"results"`
}

// Status 是当前测速状态快照。
type Status struct {
	Running  bool         `json:"running"`
	Phase    string       `json:"phase"`
	Progress ProgressInfo `json:"progress"`
	Results  []ResultItem `json:"results"`
}

// ProxyStatus 描述当前系统代理状态。
// 测速走代理时，延迟反映的是到代理出口的情况，不能代表本地网络质量。
type ProxyStatus struct {
	Enabled bool   `json:"enabled"`
	Address string `json:"address"`
}

// CheckProxy 检测系统代理是否开启，供界面在开始测速前提示用户。
func (a *App) CheckProxy() ProxyStatus {
	addr := task.SystemProxyAddr()
	return ProxyStatus{Enabled: addr != "", Address: addr}
}

// LogEntry 是一次测速过程中的一条日志。
type LogEntry struct {
	Time    string `json:"time"`
	Level   string `json:"level"`
	Stage   string `json:"stage"`
	Message string `json:"message"`
}

// Diagnosis 描述一次测速结果为 0 时的具体原因，供界面提示用户。
type Diagnosis struct {
	TotalTargets   int     `json:"totalTargets"`   // 加载到的测速目标总数
	ProbePassed    int     `json:"probePassed"`    // 域名探测通过的 IP 数
	Available      int     `json:"available"`      // 延迟测速中连通的 IP 数
	AfterDelay     int     `json:"afterDelay"`     // 通过延迟/丢包过滤的 IP 数
	DownloadTested int     `json:"downloadTested"` // 实际做了下载测速的 IP 数
	MaxSpeed       float64 `json:"maxSpeed"`       // 测到的最高下载速度 MB/s
	Reason         string  `json:"reason"`         // 直接原因
	Suggestion     string  `json:"suggestion"`     // 建议操作
}

// ProgressInfo 是进度信息。
type ProgressInfo struct {
	Current   int     `json:"current"`
	Total     int     `json:"total"`
	Available int     `json:"available"`
	IP        string  `json:"ip"`
	Port      int     `json:"port"`
	Speed     float64 `json:"speed"`
	Msg       string  `json:"msg"`
}

// App 承载全部业务逻辑，与界面实现无关。
type App struct {
	mu       sync.Mutex
	hub      *EventHub
	running  bool
	phase    string
	progress ProgressInfo
	results  []ResultItem

	// 在线 IP 缓存：本次启动后首次抓取的结果，之后复用，直到进程退出。
	onlineMu    sync.Mutex
	onlineKey   string
	onlineCache []*task.Target
	onlineTime  time.Time
	fetching    bool

	// 本次运行的过程日志，供导出分析
	logs []LogEntry

	// ProxyIP 检测的独立状态，不占用上面的测速状态
	proxyMu      sync.Mutex
	proxyRunning bool
	proxyDone    int
	proxyTotal   int
	proxyDomain  string
	proxyLocalIP string // 本机公网出口，用于判定直连；空表示取不到
	proxyResults []task.ProxyIPResult

	// 稳定性测试的独立状态，同样不占用主测速状态
	stabMu      sync.Mutex
	stabRunning bool
	stabDone    int
	stabTotal   int
	stabResults []task.StabilityResult

	// 优选域名体检的独立状态
	domMu      sync.Mutex
	domRunning bool
	domDone    int
	domTotal   int
	domResults []task.DomainResult
}

// appendLog 追加一条运行日志（最多保留最近 5000 条）。
func (a *App) appendLog(level, stage, msg string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.logs = append(a.logs, LogEntry{
		Time:    time.Now().Format("15:04:05.000"),
		Level:   level,
		Stage:   stage,
		Message: msg,
	})
	if len(a.logs) > 5000 {
		a.logs = a.logs[len(a.logs)-5000:]
	}
}

// NewApp 创建应用实例。
func NewApp() *App {
	return &App{phase: "idle"}
}

// SetHub 注入事件中心。
func (a *App) SetHub(hub *EventHub) {
	a.hub = hub
}

func (a *App) emit(event string, data any) {
	if a.hub != nil {
		a.hub.Emit(event, data)
	}
}

// Shutdown 在窗口关闭时停止正在进行的测速。
func (a *App) Shutdown() {
	task.Cancel()
}

// ---------------------------------------------------------------------------
// 配置
// ---------------------------------------------------------------------------

func defaultConfig() Config {
	return Config{
		Threads:         200,
		PingTimes:       4,
		Port:            443,
		Sources:         []string{sourceIPv4},
		IPFile:          "ip.txt",
		Httping:         true,
		HttpingCode:     0,
		MaxDelay:        200,
		MinDelay:        0,
		MaxLossRate:     1.0,
		MinSpeed:        5,
		DownloadTime:    10,
		TestCount:       10,
		URL:             defaultTestURL,
		OutputFile:      "result.csv",
		PrintNum:        10,
		DisableDownload: false,
	}
}

// normalizeConfig 为旧版本存档补齐新增字段。
func normalizeConfig(cfg Config) Config {
	if len(cfg.Sources) == 0 {
		cfg.Sources = []string{sourceIPv4}
	}
	// 至少要保留一个 IP 来源，否则测速会没有任何目标
	if !hasSource(cfg.Sources, sourceIPv4) && !hasSource(cfg.Sources, sourceIPv6) &&
		!hasSource(cfg.Sources, sourceBestCF) && !hasSource(cfg.Sources, sourceOfficial) &&
		!hasSource(cfg.Sources, sourceCmliu) && !hasSource(cfg.Sources, sourceCFDomains) &&
		!hasSource(cfg.Sources, sourceCustom) && !hasSource(cfg.Sources, sourceManual) {
		cfg.Sources = append(cfg.Sources, sourceIPv4)
	}
	if cfg.IPFile == "" {
		cfg.IPFile = "ip.txt"
	}
	cfg.URL = normalizeTestURL(cfg.URL)
	return cfg
}

// normalizeTestURL 修正已失效、或超出 Cloudflare 单次请求上限的测速地址。
func normalizeTestURL(raw string) string {
	if raw == "" || strings.Contains(raw, "cf.xiu2.xyz") {
		return defaultTestURL
	}
	// speed.cloudflare.com 的 __down 端点对 bytes >= 100000000 返回 403
	if strings.Contains(raw, "speed.cloudflare.com/__down") {
		if i := strings.Index(raw, "bytes="); i >= 0 {
			rest := raw[i+6:]
			if end := strings.IndexAny(rest, "&#"); end >= 0 {
				rest = rest[:end]
			}
			if n, err := strconv.ParseInt(rest, 10, 64); err == nil && n >= 100000000 {
				return defaultTestURL
			}
		}
	}
	return raw
}

// GetConfig 读取已保存的配置，没有则返回默认值。
func (a *App) GetConfig() Config {
	var cfg Config
	if err := readJSONFile(a.configPath(), &cfg); err != nil {
		return defaultConfig()
	}
	return normalizeConfig(cfg)
}

func hasSource(sources []string, name string) bool {
	for _, s := range sources {
		if s == name {
			return true
		}
	}
	return false
}

// onlineCacheKey 由抓取参数生成缓存键，参数变化时自动失效。
func onlineCacheKey(opts task.FetchOptions) string {
	return fmt.Sprintf("%t|%t|%t|%t|%d|%t|%s",
		opts.IncludeBestCF, opts.IncludeOfficialCIDR, opts.IncludeCmliuCIDR,
		opts.IncludeCFDomains, opts.DefaultPort, opts.AllowIPv6,
		strings.Join(opts.CustomSources, ","))
}

// buildFetchOptions 把界面配置翻译成抓取参数。
// runTest 和「主动获取」两条路径共用，避免两处逻辑走偏。
func buildFetchOptions(cfg Config, onProgress func(done, total int, msg string)) task.FetchOptions {
	return task.FetchOptions{
		DefaultPort:         cfg.Port,
		AllowIPv6:           hasSource(cfg.Sources, sourceIPv6),
		IncludeBestCF:       hasSource(cfg.Sources, sourceBestCF),
		IncludeOfficialCIDR: hasSource(cfg.Sources, sourceOfficial),
		IncludeCmliuCIDR:    hasSource(cfg.Sources, sourceCmliu),
		IncludeCFDomains:    hasSource(cfg.Sources, sourceCFDomains),
		CustomSources:       splitSourceLines(cfg.CustomSources),
		OnProgress:          onProgress,
	}
}

// hasOnlineSource 判断配置里是否勾选了需要联网抓取的来源。
func hasOnlineSource(cfg Config) bool {
	return hasSource(cfg.Sources, sourceBestCF) || hasSource(cfg.Sources, sourceOfficial) ||
		hasSource(cfg.Sources, sourceCmliu) || hasSource(cfg.Sources, sourceCFDomains) ||
		hasSource(cfg.Sources, sourceCustom)
}

// FetchStatus 是当前在线 IP 缓存的状态快照。
type FetchStatus struct {
	Fetching bool   `json:"fetching"`
	Total    int    `json:"total"`
	Time     string `json:"time"` // 抓取时刻（HH:MM），空表示还没抓过
}

// FetchSourcesResult 是一次主动抓取的结果。
type FetchSourcesResult struct {
	Total int    `json:"total"`
	Time  string `json:"time"`
}

// GetFetchStatus 返回在线 IP 缓存状态。
func (a *App) GetFetchStatus() FetchStatus {
	a.onlineMu.Lock()
	defer a.onlineMu.Unlock()
	st := FetchStatus{
		Fetching: a.fetching,
		Total:    len(a.onlineCache),
	}
	if !a.onlineTime.IsZero() {
		st.Time = a.onlineTime.Format("15:04")
	}
	return st
}

// FetchSources 主动抓取所有勾选的在线 IP 来源。
//
// 为什么要单独开一个入口：抓取依赖外网（bestcf.pages.dev、GitHub 镜像、
// DoH 解析），弱网下部分来源会静默失败，导致每次拿到的总量不一样。
// 让用户挑网络好的时候（比如开着 VPN）主动抓一次、看到结果再测速，
// 比在测速流程里隐式抓取可控得多。
func (a *App) FetchSources(cfg Config) (FetchSourcesResult, error) {
	if !hasOnlineSource(cfg) {
		return FetchSourcesResult{}, fmt.Errorf("没有勾选需要联网获取的来源")
	}

	a.onlineMu.Lock()
	if a.fetching {
		a.onlineMu.Unlock()
		return FetchSourcesResult{}, fmt.Errorf("正在获取中，请稍候")
	}
	a.fetching = true
	a.onlineMu.Unlock()

	defer func() {
		a.onlineMu.Lock()
		a.fetching = false
		a.onlineMu.Unlock()
	}()

	task.ResetCancel()

	opts := buildFetchOptions(cfg, func(done, total int, msg string) {
		a.emit("test:progress", task.ProgressEvent{
			Stage: "fetch", Current: done, Total: total, Msg: msg,
		})
	})

	targets, err := task.FetchOnlineTargets(opts)
	if err == task.ErrCancelled {
		a.emit("test:log", task.LogEvent{Level: "info", Msg: "在线 IP 获取已取消"})
		return FetchSourcesResult{}, fmt.Errorf("已取消")
	}
	if err != nil {
		a.emit("test:log", task.LogEvent{Level: "warn", Msg: "在线 IP 获取失败：" + err.Error()})
		return FetchSourcesResult{}, err
	}

	now := time.Now()
	a.onlineMu.Lock()
	a.onlineKey = onlineCacheKey(opts)
	a.onlineCache = targets
	a.onlineTime = now
	a.onlineMu.Unlock()

	task.ExtraTargets = targets

	a.emit("test:log", task.LogEvent{
		Level: "info",
		Msg:   fmt.Sprintf("在线 IP 已获取：%d 个（本次启动内测速会直接复用）", len(targets)),
	})

	return FetchSourcesResult{Total: len(targets), Time: now.Format("15:04")}, nil
}

// splitSourceLines 把多行文本拆成 URL 列表，忽略空行与注释。
func splitSourceLines(text string) []string {
	var out []string
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		out = append(out, line)
	}
	return out
}

// SaveConfig 持久化配置。
func (a *App) SaveConfig(cfg Config) error {
	return writeFileJSON(a.configPath(), cfg)
}

// ResetConfig 恢复默认配置。
func (a *App) ResetConfig() Config {
	cfg := defaultConfig()
	_ = writeFileJSON(a.configPath(), cfg)
	return cfg
}

// ---------------------------------------------------------------------------
// 测速控制
// ---------------------------------------------------------------------------

// StartTest 启动一次测速。进度通过 test:* 事件推送。
func (a *App) StartTest(cfg Config) error {
	// 稳定性测试和主测速互斥：两者都会打满并发连接，
	// 同时跑会互相干扰，测出来的数字都没意义。
	// 锁顺序固定为 stabMu → mu，避免与 StartStability 交叉加锁死锁。
	a.stabMu.Lock()
	stabBusy := a.stabRunning
	a.stabMu.Unlock()
	if stabBusy {
		return fmt.Errorf("稳定性测试正在进行中，请先停止")
	}

	a.mu.Lock()
	if a.running {
		a.mu.Unlock()
		return fmt.Errorf("测速正在进行中，请先停止")
	}
	cfg = normalizeConfig(cfg)
	a.running = true
	a.phase = "ping"
	a.results = nil
	a.logs = nil
	a.progress = ProgressInfo{}
	a.mu.Unlock()

	_ = writeFileJSON(a.configPath(), cfg)
	go a.runTest(cfg)
	return nil
}

// StopTest 请求停止当前测速。
func (a *App) StopTest() {
	task.Cancel()
}

// Status 返回当前状态快照。
func (a *App) Status() Status {
	a.mu.Lock()
	defer a.mu.Unlock()
	return Status{
		Running:  a.running,
		Phase:    a.phase,
		Progress: a.progress,
		Results:  a.resultsOrEmpty(),
	}
}

// StabilityRequest 是前端发起的稳定性测试请求。
type StabilityRequest struct {
	Targets     []string `json:"targets"`     // "ip:port" 列表，为空表示测试当前全部结果
	Rounds      int      `json:"rounds"`      // 每个 IP 的采样轮数
	Concurrency int      `json:"concurrency"` // 并发测试的 IP 数量
	KeepAlive   bool     `json:"keepAlive"`   // 是否额外做长连接保活探测
	Domain      string   `json:"domain"`      // 测试目标域名，通常填自己的 Worker 域名
}

// StabilityStatus 是稳定性测试的状态快照。
type StabilityStatus struct {
	Running bool                   `json:"running"`
	Done    int                    `json:"done"`
	Total   int                    `json:"total"`
	Results []task.StabilityResult `json:"results"`
}

// StartStability 启动稳定性测试。
//
// 为什么需要独立于主测速：主测速只测一次「能不能连上」，
// 而用户真正关心的是「连上之后会不会断」。稳定性测试用多轮采样
// 把 TCP / TLS 分开计时，并统计成功率与抖动。
func (a *App) StartStability(req StabilityRequest) error {
	// 锁顺序：stabMu → mu（与 StartTest 保持一致）
	a.stabMu.Lock()
	if a.stabRunning {
		a.stabMu.Unlock()
		return fmt.Errorf("稳定性测试正在进行中，请先停止")
	}
	a.mu.Lock()
	busy := a.running
	a.mu.Unlock()
	if busy {
		a.stabMu.Unlock()
		return fmt.Errorf("主测速正在进行中，请等它结束后再测稳定性")
	}

	targets := a.stabilityTargets(req.Targets)
	if len(targets) == 0 {
		a.stabMu.Unlock()
		// 区分「一次测速都没跑」和「传进来的 IP 全解析失败」，
		// 否则用户看到「请先完成一次测速」会莫名其妙
		if len(req.Targets) > 0 {
			return fmt.Errorf("传入的 %d 个 IP 都无法解析，请检查格式", len(req.Targets))
		}
		return fmt.Errorf("没有可测试的 IP，请先完成一次测速")
	}

	domain := strings.TrimSpace(req.Domain)
	if domain == "" {
		domain = a.savedProbeDomain()
	}

	a.stabRunning = true
	a.stabDone = 0
	a.stabTotal = len(targets)
	a.stabResults = nil
	a.stabMu.Unlock()

	task.StabilityDomain = task.NormalizeProbeDomain(domain)
	task.ResetCancel()

	cfg := task.StabilityConfig{
		Rounds:      req.Rounds,
		Concurrency: req.Concurrency,
		KeepAlive:   req.KeepAlive,
	}

	target := task.StabilityDomain
	if target == "" {
		target = "默认测速端点"
	}
	a.appendLog("info", "stability", fmt.Sprintf("开始稳定性测试：%d 个 IP，目标 %s", len(targets), target))
	a.emit("stability:start", map[string]any{"total": len(targets)})

	go func() {
		defer func() {
			a.stabMu.Lock()
			done := a.stabDone
			a.stabRunning = false
			// 回调是完成顺序，这里排一次，让页面刷新后恢复的顺序也稳定
			task.SortStabilityResults(a.stabResults)
			a.stabMu.Unlock()
			a.emit("stability:done", map[string]any{"total": len(targets), "done": done})
			a.appendLog("info", "stability", fmt.Sprintf("稳定性测试结束：%d/%d", done, len(targets)))
		}()

		task.TestStability(targets, cfg, func(r *task.StabilityResult) {
			a.stabMu.Lock()
			a.stabDone++
			a.stabResults = append(a.stabResults, *r)
			done := a.stabDone
			a.stabMu.Unlock()
			a.emit("stability:result", map[string]any{
				"done":   done,
				"total":  len(targets),
				"result": r,
			})
		})
	}()
	return nil
}

// StopStability 请求停止稳定性测试。
func (a *App) StopStability() {
	task.Cancel()
}

// StabilityStatus 返回稳定性测试状态快照。
func (a *App) StabilityStatus() StabilityStatus {
	a.stabMu.Lock()
	defer a.stabMu.Unlock()
	rs := a.stabResults
	if rs == nil {
		rs = []task.StabilityResult{}
	}
	return StabilityStatus{
		Running: a.stabRunning,
		Done:    a.stabDone,
		Total:   a.stabTotal,
		Results: rs,
	}
}

// stabilityTargets 把前端传来的 "ip:port" 列表解析成 Target。
// 列表为空时取当前测速结果，方便「一键测全部」。
func (a *App) stabilityTargets(list []string) []*task.Target {
	if len(list) == 0 {
		a.mu.Lock()
		items := a.resultsOrEmpty()
		a.mu.Unlock()
		list = make([]string, 0, len(items))
		for _, it := range items {
			list = append(list, net.JoinHostPort(it.IP, strconv.Itoa(it.Port)))
		}
	}

	out := make([]*task.Target, 0, len(list))
	seen := make(map[string]struct{}, len(list))
	for _, s := range list {
		s = strings.TrimSpace(s)
		if s == "" {
			continue
		}
		host, portStr := s, "443"
		if h, p, err := net.SplitHostPort(s); err == nil {
			host, portStr = h, p
		}
		ip := net.ParseIP(strings.Trim(host, "[]"))
		if ip == nil {
			continue
		}
		port, err := strconv.Atoi(portStr)
		if err != nil || port <= 0 || port > 65535 {
			port = 443
		}
		key := net.JoinHostPort(ip.String(), strconv.Itoa(port))
		if _, dup := seen[key]; dup {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, &task.Target{IP: &net.IPAddr{IP: ip}, Port: port})
	}
	return out
}

// ---- 优选域名体检 ----

// DomainCheckRequest 是前端发起的优选域名体检请求。
type DomainCheckRequest struct {
	Host    string   `json:"host"`    // 测试用的 Host/SNI，通常是自己的入口域名
	Domains []string `json:"domains"` // 待测域名，为空时使用内置列表
}

// DomainCheckStatus 是体检状态快照。
type DomainCheckStatus struct {
	Running bool                `json:"running"`
	Done    int                 `json:"done"`
	Total   int                 `json:"total"`
	Results []task.DomainResult `json:"results"`
}

// GetDefaultDomains 返回内置的优选域名列表。
func (a *App) GetDefaultDomains() []string {
	return task.DefaultPreferredDomains
}

// ImportPreferredDomains 从远程列表导入域名候选。
//
// 优选域名列表变化很快（几周就有失效的），内置的 12 个只够起步。
// 这个接口让用户随时从 BestCF 的汇总列表同步一份最新的。
func (a *App) ImportPreferredDomains(rawURL string) ([]string, error) {
	domains, err := task.FetchDomainList(rawURL)
	if err != nil {
		a.appendLog("warn", "domain", "导入优选域名失败："+err.Error())
		return nil, fmt.Errorf("导入失败：%w", err)
	}
	a.appendLog("info", "domain", fmt.Sprintf("已导入 %d 个候选域名", len(domains)))
	return domains, nil
}

// StartDomainCheck 启动优选域名体检。
//
// 解决的问题：优选域名（saas.sin.fan / cu.877774.xyz 这类）解析出的
// 是一批 CF 边缘 IP，好坏混杂，而 DNS 轮询是随机的。这个功能让你在
// 选定一个域名去 CNAME 之前，先看清它到底稳不稳。
func (a *App) StartDomainCheck(req DomainCheckRequest) error {
	a.domMu.Lock()
	if a.domRunning {
		a.domMu.Unlock()
		return fmt.Errorf("优选域名体检正在进行中，请先停止")
	}

	host := strings.TrimSpace(req.Host)
	if host == "" {
		host = a.savedProbeDomain()
	}
	if host == "" {
		a.domMu.Unlock()
		return fmt.Errorf("请填写测试域名（你自己的入口域名，如 fast2.lds1216.ccwu.cc）")
	}

	domains := req.Domains
	if len(domains) == 0 {
		domains = task.DefaultPreferredDomains
	}

	a.domRunning = true
	a.domDone = 0
	a.domTotal = len(domains)
	a.domResults = nil
	a.domMu.Unlock()

	task.ResetCancel()

	cfg := task.DomainCheckConfig{Host: host, Domains: domains}

	a.appendLog("info", "domain", fmt.Sprintf("开始优选域名体检：%d 个域名，Host=%s", len(domains), host))
	a.emit("domain:start", map[string]any{"total": len(domains)})

	go func() {
		defer func() {
			a.domMu.Lock()
			done := a.domDone
			a.domRunning = false
			a.domMu.Unlock()
			a.emit("domain:done", map[string]any{"total": len(domains), "done": done})
			a.appendLog("info", "domain", fmt.Sprintf("优选域名体检结束：%d/%d", done, len(domains)))
		}()

		task.CheckPreferredDomains(cfg, func(r *task.DomainResult) {
			a.domMu.Lock()
			a.domDone++
			a.domResults = append(a.domResults, *r)
			done := a.domDone
			a.domMu.Unlock()
			a.emit("domain:result", map[string]any{
				"done":   done,
				"total":  len(domains),
				"result": r,
			})
		})
	}()
	return nil
}

// StopDomainCheck 请求停止体检。
func (a *App) StopDomainCheck() {
	task.Cancel()
}

// DomainCheckStatus 返回体检状态快照。
func (a *App) DomainCheckStatus() DomainCheckStatus {
	a.domMu.Lock()
	defer a.domMu.Unlock()
	rs := a.domResults
	if rs == nil {
		rs = []task.DomainResult{}
	}
	return DomainCheckStatus{
		Running: a.domRunning,
		Done:    a.domDone,
		Total:   a.domTotal,
		Results: rs,
	}
}

// savedProbeDomain 从已保存的配置里读取探测域名，作为稳定性测试的兜底目标。
func (a *App) savedProbeDomain() string {
	data, err := os.ReadFile(a.configPath())
	if err != nil {
		return ""
	}
	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return ""
	}
	return cfg.ProbeDomain
}

func (a *App) resultsOrEmpty() []ResultItem {
	if a.results == nil {
		return []ResultItem{}
	}
	return a.results
}

func (a *App) runTest(cfg Config) {
	start := time.Now()
	defer func() {
		a.mu.Lock()
		a.running = false
		a.mu.Unlock()
	}()

	task.ResetCancel()
	applyConfig(cfg)

	var totalTargets, probePassed int
	task.ProgressHook = func(e task.ProgressEvent) {
		if e.Stage == "ping" && e.Total > totalTargets {
			totalTargets = e.Total
		}
		info := ProgressInfo{
			Current:   e.Current,
			Total:     e.Total,
			Available: e.Available,
			IP:        e.IP,
			Port:      e.Port,
			Speed:     e.Speed,
			Msg:       e.Msg,
		}
		a.mu.Lock()
		a.phase = e.Stage
		a.progress = info
		a.mu.Unlock()

		a.emit("test:progress", e)
	}
	task.LogHook = func(e task.LogEvent) {
		a.appendLog(e.Level, "", e.Msg)
		a.emit("test:log", e)
	}
	// 下载测速每完成一个达标 IP 就实时推给界面，不必等全部结束
	task.ResultHook = func(d utils.CloudflareIPData) {
		item := convertResultItem(d)
		a.mu.Lock()
		a.results = append(a.results, item)
		a.mu.Unlock()
		a.emit("test:result", item)
	}
	defer func() {
		task.ProgressHook = nil
		task.LogHook = nil
		task.ResultHook = nil
	}()

	task.InitRandSeed()
	a.appendLog("info", "start", fmt.Sprintf(
		"开始测速 | 来源=%v 端口=%d HTTPing=%v 延迟≤%dms 丢包≤%.2f 速度≥%.2fMB/s 数量=%d",
		cfg.Sources, cfg.Port, cfg.Httping, cfg.MaxDelay, cfg.MaxLossRate, cfg.MinSpeed, cfg.TestCount))
	a.emit("test:started", map[string]any{"time": start.Format("15:04:05")})

	// 勾选了在线来源时：本次启动首次抓取并缓存，之后的测速直接复用
	if hasOnlineSource(cfg) {
		opts := buildFetchOptions(cfg, func(done, total int, msg string) {
			task.ProgressHook(task.ProgressEvent{
				Stage: "fetch", Current: done, Total: total, Msg: msg,
			})
		})
		key := onlineCacheKey(opts)

		a.onlineMu.Lock()
		useCache := a.onlineKey == key && len(a.onlineCache) > 0
		cached := a.onlineCache
		a.onlineMu.Unlock()

		if useCache {
			// 同一次启动内复用，避免每次都花 20~40 秒重新抓取
			task.ExtraTargets = cached
			a.emit("test:log", task.LogEvent{
				Level: "info",
				Msg:   fmt.Sprintf("复用本次启动已获取的在线 IP：%d 个（重新打开程序才会再次获取）", len(cached)),
			})
		} else {
			a.mu.Lock()
			a.phase = "fetch"
			a.mu.Unlock()
			a.emit("test:phase", "fetch")

			targets, err := task.FetchOnlineTargets(opts)
			switch {
			case err == task.ErrCancelled:
				a.mu.Lock()
				a.phase = "done"
				a.mu.Unlock()
				a.emit("test:finished", map[string]any{
					"results":  []ResultItem{},
					"duration": time.Since(start).Seconds(),
				})
				return
			case err != nil:
				task.ExtraTargets = nil
				a.emit("test:log", task.LogEvent{Level: "warn", Msg: "在线优选 IP 获取失败：" + err.Error()})
			default:
				a.onlineMu.Lock()
				a.onlineKey = key
				a.onlineCache = targets
				a.onlineTime = time.Now()
				a.onlineMu.Unlock()
				task.ExtraTargets = targets
				a.emit("test:log", task.LogEvent{
					Level: "info",
					Msg:   fmt.Sprintf("在线优选 IP 已获取：%d 个（已按 IP 去重，本次启动内复用）", len(targets)),
				})
			}
		}
	}

	// 域名探测：先用 /cdn-cgi/trace 剔除不服务该域名的 IP
	// 该路径绕过 Worker，不消耗 Workers 请求配额
	if cfg.ProbeEnabled && cfg.ProbeDomain != "" {
		// 用户多半是从浏览器地址栏粘过来的（带 https:// 和末尾斜杠），先归一化
		probeDomain := task.NormalizeProbeDomain(cfg.ProbeDomain)
		if probeDomain == "" {
			d := &Diagnosis{
				Reason:     fmt.Sprintf("测速域名「%s」无效，无法解析出主机名", cfg.ProbeDomain),
				Suggestion: "只填写域名本身，例如 fast3.example.com，不要带 https:// 或末尾的斜杠",
			}
			a.appendLog("warn", "diagnosis", d.Reason+"；建议："+d.Suggestion)
			a.mu.Lock()
			a.phase = "done"
			a.mu.Unlock()
			a.emit("test:finished", map[string]any{
				"results":   []ResultItem{},
				"duration":  time.Since(start).Seconds(),
				"diagnosis": d,
			})
			return
		}
		if probeDomain != strings.TrimSpace(cfg.ProbeDomain) {
			a.appendLog("info", "probe", fmt.Sprintf(
				"测速域名已自动规范化为 %s", probeDomain))
		}
		cfg.ProbeDomain = probeDomain

		base := task.LoadTargets()
		totalTargets = len(base)
		a.appendLog("info", "probe", fmt.Sprintf(
			"开始域名探测 | 目标=%d 域名=%s", totalTargets, cfg.ProbeDomain))

		a.mu.Lock()
		a.phase = "probe"
		a.mu.Unlock()
		a.emit("test:phase", "probe")

		probed := task.ProbeDomain(base, cfg.ProbeDomain, func(done, total int) {
			task.ProgressHook(task.ProgressEvent{
				Stage: "probe", Current: done, Total: total,
			})
		})
		probePassed = len(probed)
		task.OverrideTargets = probed

		a.appendLog("info", "probe", fmt.Sprintf(
			"域名探测完成 | %d → %d 个（剔除 %d 个）",
			totalTargets, probePassed, totalTargets-probePassed))

		if len(probed) == 0 {
			d := &Diagnosis{
				TotalTargets: totalTargets,
				ProbePassed:  0,
				Reason:       fmt.Sprintf("加载到 %d 个目标，但没有一个能服务域名 %s", totalTargets, cfg.ProbeDomain),
				Suggestion:   "确认该域名已绑定到 Worker/Pages 且 DNS 托管在 Cloudflare；也可以清空「自定义测速域名」后再试",
			}
			a.appendLog("warn", "diagnosis", d.Reason+"；建议："+d.Suggestion)
			a.mu.Lock()
			a.phase = "done"
			a.mu.Unlock()
			a.emit("test:finished", map[string]any{
				"results":   []ResultItem{},
				"duration":  time.Since(start).Seconds(),
				"diagnosis": d,
			})
			return
		}
	} else {
		probePassed = -1 // 未启用探测
	}

	ping := task.NewPing()
	pingAll := ping.Run() // 延迟测速：返回所有连通的 IP
	availableCount := len(pingAll)
	pingData := pingAll.FilterDelay().FilterLossRate() // 再按延迟/丢包条件过滤
	afterDelay := len(pingData)

	participated := totalTargets
	if probePassed >= 0 {
		participated = probePassed
	}
	a.appendLog("info", "ping", fmt.Sprintf(
		"延迟测速完成 | 参与=%d 连通=%d 通过延迟/丢包过滤=%d", participated, availableCount, afterDelay))

	a.mu.Lock()
	a.phase = "download"
	a.mu.Unlock()
	a.emit("test:phase", "download")

	speedData := task.TestDownloadSpeed(pingData)
	results := convertResults(speedData)

	a.appendLog("info", "download", fmt.Sprintf(
		"下载测速完成 | 参与=%d 结果=%d", len(speedData), len(results)))

	a.mu.Lock()
	a.results = results
	a.phase = "done"
	a.mu.Unlock()

	if cfg.OutputFile != "" {
		utils.Output = cfg.OutputFile
		utils.ExportCsv(speedData)
	}

	duration := time.Since(start).Seconds()
	rec := HistoryRecord{
		ID:       time.Now().UnixNano(),
		Time:     time.Now().Format("2006-01-02 15:04:05"),
		Duration: duration,
		Count:    len(results),
		Config:   cfg,
		Results:  results,
	}
	if len(results) > 0 {
		rec.BestIP = results[0].IP
		rec.BestSpeed = results[0].Speed
	}
	a.appendHistory(rec)

	finished := map[string]any{
		"results":  results,
		"duration": duration,
	}
	// 结果为 0 时附带诊断信息，让界面能明确告诉用户卡在哪一步
	if len(results) == 0 {
		d := buildDiagnosis(cfg, totalTargets, probePassed, availableCount, afterDelay, speedData)
		finished["diagnosis"] = d
		a.appendLog("warn", "diagnosis", d.Reason+"；建议："+d.Suggestion)
	}
	a.emit("test:finished", finished)
}

// applyConfig 把界面配置写入 task/utils 的全局参数。
func applyConfig(cfg Config) {
	task.Routines = cfg.Threads
	task.PingTimes = cfg.PingTimes
	task.TestCount = cfg.TestCount
	task.Timeout = time.Duration(cfg.DownloadTime) * time.Second
	task.TCPPort = cfg.Port
	task.URL = cfg.URL
	task.Httping = cfg.Httping
	task.HttpingStatusCode = cfg.HttpingCode
	task.HttpingCFColo = cfg.Colo
	task.HttpingCFColomap = task.MapColoMap()
	task.Disable = cfg.DisableDownload
	task.TestAll = cfg.TestAll
	task.MinSpeed = cfg.MinSpeed

	// IP 来源
	task.SourceIPv4 = hasSource(cfg.Sources, sourceIPv4)
	task.SourceIPv6 = hasSource(cfg.Sources, sourceIPv6)
	task.IPFile = cfg.IPFile
	if task.IPFile == "" {
		task.IPFile = "ip.txt"
	}
	task.IPText = ""
	if hasSource(cfg.Sources, sourceManual) {
		task.IPText = cfg.IPText
	}
	task.ExtraTargets = nil    // 在线优选 IP 由 runTest 抓取后填充
	task.OverrideTargets = nil // 域名探测结果由 runTest 填充

	utils.InputMaxDelay = time.Duration(cfg.MaxDelay) * time.Millisecond
	utils.InputMinDelay = time.Duration(cfg.MinDelay) * time.Millisecond
	utils.InputMaxLossRate = float32(cfg.MaxLossRate)
	utils.Debug = cfg.Debug
	utils.Output = cfg.OutputFile
	utils.PrintNum = cfg.PrintNum
	utils.Silent = true
}

// buildDiagnosis 根据各阶段的数量，推断结果为空的具体原因并给出建议。
func buildDiagnosis(cfg Config, totalTargets, probePassed, available, afterDelay int, speedData utils.DownloadSpeedSet) *Diagnosis {
	d := &Diagnosis{
		TotalTargets:   totalTargets,
		ProbePassed:    probePassed,
		Available:      available,
		AfterDelay:     afterDelay,
		DownloadTested: len(speedData),
	}
	for _, s := range speedData {
		if mbps := s.DownloadSpeed / 1024 / 1024; mbps > d.MaxSpeed {
			d.MaxSpeed = mbps
		}
	}

	switch {
	case totalTargets == 0:
		d.Reason = "没有加载到任何 IP 目标"
		d.Suggestion = "确认 ip.txt 与 cfstx.exe 在同一目录，或至少勾选一个在线 IP 来源"
	case probePassed == 0:
		d.Reason = fmt.Sprintf("域名 %s 探测失败，所有 IP 都被剔除", cfg.ProbeDomain)
		d.Suggestion = "确认该域名已绑定到 Worker/Pages 且 DNS 托管在 Cloudflare；也可以清空「自定义测速域名」后再试"
	case available == 0:
		d.Reason = "延迟测速中所有 IP 均未响应"
		d.Suggestion = "检查网络连接；如果开着 VPN/代理请先关闭，测速需要直连每个目标 IP"
	case afterDelay == 0:
		d.Reason = fmt.Sprintf("有 %d 个 IP 连通，但延迟全部超过上限 %d ms", available, cfg.MaxDelay)
		d.Suggestion = "把「延迟上限」调大试试：中国大陆到 Cloudflare 常在 150~250ms，建议先设 300 或 9999 看全貌"
	case len(speedData) == 0:
		d.Reason = fmt.Sprintf("%d 个 IP 通过了延迟过滤，但下载测速一个都没完成", afterDelay)
		d.Suggestion = "稍等几分钟再试；测速地址可能被 Cloudflare 限流"
	case d.MaxSpeed <= 0:
		d.Reason = fmt.Sprintf("%d 个 IP 做了下载测速，但全部失败（速度 0）", len(speedData))
		d.Suggestion = "多为测速地址被限流（HTTP 429）导致。等待 5~10 分钟后重试，或在「测速地址」中换用自建地址"
	case cfg.MinSpeed > 0:
		d.Reason = fmt.Sprintf("%d 个 IP 完成测速，最高速度仅 %.2f MB/s，低于下限 %.1f MB/s",
			len(speedData), d.MaxSpeed, cfg.MinSpeed)
		d.Suggestion = "调低「速度下限」，或放宽「延迟上限」以纳入更多节点"
	default:
		d.Reason = "测速完成但没有产生结果"
		d.Suggestion = "请在结果区导出日志后反馈"
	}
	return d
}

func convertResultItem(d utils.CloudflareIPData) ResultItem {
	loss := 0.0
	if d.Sended > 0 {
		loss = float64(d.Sended-d.Received) / float64(d.Sended)
	}
	colo := d.Colo
	if colo == "" {
		colo = "N/A"
	}
	return ResultItem{
		IP:       d.IP.String(),
		Port:     d.Port,
		Sent:     d.Sended,
		Received: d.Received,
		LossRate: loss,
		Delay:    float64(d.Delay.Microseconds()) / 1000.0,
		Speed:    d.DownloadSpeed / 1024 / 1024,
		Colo:     colo,
	}
}

func convertResults(data utils.DownloadSpeedSet) []ResultItem {
	out := make([]ResultItem, 0, len(data))
	for _, d := range data {
		out = append(out, convertResultItem(d))
	}
	return out
}

// ---------------------------------------------------------------------------
// 结果与导出
// ---------------------------------------------------------------------------

// GetResults 返回最近一次测速的完整结果。
func (a *App) GetResults() []ResultItem {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.resultsOrEmpty()
}

// ExportCSV 导出结果。path 为空时写入当前目录的 result.csv。
func (a *App) ExportCSV(path string) (string, error) {
	a.mu.Lock()
	results := a.results
	a.mu.Unlock()

	if len(results) == 0 {
		return "", fmt.Errorf("没有可导出的测速结果")
	}

	name := path
	if name == "" {
		name = "result.csv"
	}
	abs := absOutputPath(name)

	f, err := os.Create(abs)
	if err != nil {
		return "", fmt.Errorf("创建文件失败: %w", err)
	}
	defer f.Close()

	w := csv.NewWriter(f)
	_ = w.Write([]string{"IP 地址", "端口", "已发送", "已接收", "丢包率", "平均延迟", "下载速度(MB/s)", "地区码"})
	for _, r := range results {
		_ = w.Write([]string{
			r.IP,
			strconv.Itoa(r.Port),
			strconv.Itoa(r.Sent),
			strconv.Itoa(r.Received),
			fmt.Sprintf("%.2f", r.LossRate),
			fmt.Sprintf("%.2f", r.Delay),
			fmt.Sprintf("%.2f", r.Speed),
			r.Colo,
		})
	}
	w.Flush()
	if err := w.Error(); err != nil {
		return "", err
	}
	return abs, nil
}

// ExportTXT 以「IP:端口#备注」格式导出结果，可直接作为 IP 列表再次使用。
func (a *App) ExportTXT(path string) (string, error) {
	a.mu.Lock()
	results := a.results
	a.mu.Unlock()

	if len(results) == 0 {
		return "", fmt.Errorf("没有可导出的测速结果")
	}

	name := path
	if name == "" {
		name = "result.txt"
	}
	abs := absOutputPath(name)

	var b strings.Builder
	for _, r := range results {
		port := r.Port
		if port <= 0 || port > 65535 {
			port = 443
		}
		colo := r.Colo
		if colo == "" {
			colo = "N/A"
		}
		fmt.Fprintf(&b, "%s:%d#%s %.2fMB/s\n", r.IP, port, colo, r.Speed)
	}

	if err := os.WriteFile(abs, []byte(b.String()), 0o644); err != nil {
		return "", fmt.Errorf("写入文件失败: %w", err)
	}
	return abs, nil
}

// ExportLog 把本次运行的完整过程日志写入文本文件，便于排查问题。
func (a *App) ExportLog(path string) (string, error) {
	a.mu.Lock()
	logs := append([]LogEntry(nil), a.logs...)
	a.mu.Unlock()

	if len(logs) == 0 {
		return "", fmt.Errorf("没有可导出的日志，请先运行一次测速")
	}

	name := path
	if name == "" {
		name = "cfstx-log.txt"
	}
	abs := absOutputPath(name)

	var b strings.Builder
	b.WriteString("CFSTX 运行日志\n")
	fmt.Fprintf(&b, "版本: %s\n", version.Current)
	fmt.Fprintf(&b, "导出时间: %s\n", time.Now().Format("2006-01-02 15:04:05"))
	fmt.Fprintf(&b, "系统: %s/%s\n", runtime.GOOS, runtime.GOARCH)
	fmt.Fprintf(&b, "配置目录: %s\n", configDir())
	b.WriteString(strings.Repeat("=", 64) + "\n\n")
	for _, l := range logs {
		stage := ""
		if l.Stage != "" {
			stage = "[" + l.Stage + "] "
		}
		fmt.Fprintf(&b, "[%s] %-5s %s%s\n", l.Time, strings.ToUpper(l.Level), stage, l.Message)
	}

	if err := os.WriteFile(abs, []byte(b.String()), 0o644); err != nil {
		return "", fmt.Errorf("写入文件失败: %w", err)
	}
	return abs, nil
}

// ---------------------------------------------------------------------------
// 历史记录
// ---------------------------------------------------------------------------

// GetHistory 返回历史记录（最新在前）。
func (a *App) GetHistory() []HistoryRecord {
	var list []HistoryRecord
	if err := readJSONFile(a.historyPath(), &list); err != nil || list == nil {
		// 返回空切片而不是 nil：nil 会被序列化成 null，前端读 .length 会崩
		return []HistoryRecord{}
	}
	return list
}

// DeleteHistory 删除指定历史记录。
func (a *App) DeleteHistory(id int64) error {
	var list []HistoryRecord
	_ = readJSONFile(a.historyPath(), &list)
	filtered := list[:0]
	for _, r := range list {
		if r.ID != id {
			filtered = append(filtered, r)
		}
	}
	return writeFileJSON(a.historyPath(), filtered)
}

// ClearHistory 清空全部历史记录。
func (a *App) ClearHistory() error {
	return writeFileJSON(a.historyPath(), []HistoryRecord{})
}

func (a *App) appendHistory(rec HistoryRecord) {
	var list []HistoryRecord
	_ = readJSONFile(a.historyPath(), &list)
	list = append([]HistoryRecord{rec}, list...)
	if len(list) > 20 {
		list = list[:20]
	}
	_ = writeFileJSON(a.historyPath(), list)
}

// ---------------------------------------------------------------------------
// 版本
// ---------------------------------------------------------------------------

// GetVersion 返回当前版本号。
func (a *App) GetVersion() string {
	return version.Current
}

// CheckUpdate 检查更新，返回最新版本号（空字符串表示已是最新）。
func (a *App) CheckUpdate() string {
	return version.Check()
}

// CheckRelease 查询 Gitee 最新 Release 信息。
func (a *App) CheckRelease() (*version.ReleaseInfo, error) {
	return version.CheckRelease()
}

// DownloadUpdate 把安装包下载到临时目录，进度通过 update:progress 事件推送。
func (a *App) DownloadUpdate(url, filename string) (string, error) {
	if filename == "" {
		filename = "CFSTX-Setup.exe"
	}
	dir := filepath.Join(os.TempDir(), "CFSTX-Update")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("创建下载目录失败: %w", err)
	}
	dest := filepath.Join(dir, filename)

	client := &http.Client{Timeout: 5 * time.Minute}
	resp, err := client.Get(url)
	if err != nil {
		return "", fmt.Errorf("下载失败: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("下载失败: HTTP %s", resp.Status)
	}

	f, err := os.Create(dest)
	if err != nil {
		return "", fmt.Errorf("创建文件失败: %w", err)
	}
	defer f.Close()

	total := resp.ContentLength
	var written int64
	buf := make([]byte, 64*1024)
	lastEmit := time.Now()

	for {
		n, readErr := resp.Body.Read(buf)
		if n > 0 {
			if _, werr := f.Write(buf[:n]); werr != nil {
				return "", werr
			}
			written += int64(n)
			if time.Since(lastEmit) > 100*time.Millisecond {
				percent := 0.0
				if total > 0 {
					percent = float64(written) / float64(total) * 100
				}
				a.emit("update:progress", map[string]any{
					"downloaded": written,
					"total":      total,
					"percent":    percent,
				})
				lastEmit = time.Now()
			}
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			return "", readErr
		}
	}

	a.emit("update:progress", map[string]any{
		"downloaded": written,
		"total":      total,
		"percent":    100.0,
	})
	return dest, nil
}

// InstallUpdate 启动安装程序并退出当前应用。
func (a *App) InstallUpdate(path string) error {
	if _, err := os.Stat(path); err != nil {
		return fmt.Errorf("安装包不存在: %w", err)
	}
	cmd := exec.Command(path, "/SILENT", "/SUPPRESSMSGBOXES", "/NORESTART")
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("启动安装程序失败: %w", err)
	}
	// 延迟退出，确保 HTTP 响应先返回
	go func() {
		time.Sleep(800 * time.Millisecond)
		os.Exit(0)
	}()
	return nil
}

// ---------------------------------------------------------------------------
// 文件与路径
// ---------------------------------------------------------------------------

func configDir() string {
	dir, err := os.UserConfigDir()
	if err != nil {
		dir = "."
	}
	p := filepath.Join(dir, "CFSTX")
	_ = os.MkdirAll(p, 0o755)
	return p
}

func (a *App) configPath() string  { return filepath.Join(configDir(), "config.json") }
func (a *App) historyPath() string { return filepath.Join(configDir(), "history.json") }

func readJSONFile(path string, v any) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, v)
}

func writeFileJSON(path string, v any) error {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}
