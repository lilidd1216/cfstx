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
	"sort"
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
	MaxDelay      int      `json:"maxDelay"`
	MinDelay      int      `json:"minDelay"`
	MaxLossRate   float64  `json:"maxLossRate"`
	MinSpeed      float64  `json:"minSpeed"`
	CountryFilter []string `json:"countryFilter"` // 国家/地区代码（HK/JP/US...），空表示不过滤

	// 下载测速
	DownloadTime    int    `json:"downloadTime"`
	TestCount       int    `json:"testCount"`
	DisableDownload bool   `json:"disableDownload"`
	TestAll         bool   `json:"testAll"`
	URL             string `json:"url"`

	// 端到端（真实隧道）测速。凭据不在这里，单独存 e2e_secrets.json。
	E2EEnabled     bool   `json:"e2eEnabled"`
	E2EProtocol    string `json:"e2eProtocol"`
	E2EHost        string `json:"e2eHost"`
	E2EPort        int    `json:"e2ePort"`
	E2ETarget      string `json:"e2eTarget"`
	E2EConcurrency int    `json:"e2eConcurrency"`
	E2ERetries     int    `json:"e2eRetries"`
	E2EMaxLatency  int    `json:"e2eMaxLatency"`
	E2ELimit       int    `json:"e2eLimit"`
	E2EInsecure    bool   `json:"e2eInsecure"`

	// 历史优质 IP 池 / 两轮测速
	PoolEnabled   bool `json:"poolEnabled"`
	PoolLimit     int  `json:"poolLimit"`
	CoarseEnabled bool `json:"coarseEnabled"`
	CoarseLimit   int  `json:"coarseLimit"`
	CoarseTimeout int  `json:"coarseTimeout"`

	// 输出
	OutputFile string `json:"outputFile"`
	PrintNum   int    `json:"printNum"`
	Debug      bool   `json:"debug"`
}

// ResultItem 是单条测速结果。
type ResultItem struct {
	IP        string  `json:"ip"`
	Port      int     `json:"port"`
	Sent      int     `json:"sent"`
	Received  int     `json:"received"`
	LossRate  float64 `json:"lossRate"`
	Delay     float64 `json:"delay"`
	Speed     float64 `json:"speed"`
	PeakSpeed float64 `json:"peakSpeed,omitempty"`
	Colo      string  `json:"colo"`
	// E2E 是端到端探测结果；未启用或未测时为 nil。
	E2E *task.E2EResult `json:"e2e,omitempty"`
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
	// 端到端阶段的诊断信息
	E2ECandidates int            `json:"e2eCandidates"`
	E2EPassed     int            `json:"e2ePassed"`
	E2EFailed     int            `json:"e2eFailed"`
	E2EStages     map[string]int `json:"e2eStages"`
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
		MaxDelay:        9999,
		MinDelay:        0,
		MaxLossRate:     1.0,
		MinSpeed:        5,
		DownloadTime:    10,
		TestCount:       10,
		URL:             defaultTestURL,
		OutputFile:      "result.csv",
		PrintNum:        10,
		DisableDownload: false,
		E2EProtocol:     "vless",
		E2ETarget:       task.DefaultE2ETarget,
		E2EConcurrency:  16,
		E2ERetries:      3,
		E2EMaxLatency:   3000,
		E2ELimit:        150,
		PoolEnabled:     true,
		PoolLimit:       200,
		CoarseEnabled:   true,
		CoarseLimit:     60,
		CoarseTimeout:   1500,
		CountryFilter:   []string{},
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

	// 端到端探测参数归一化（旧存档没有这些字段，走这里补齐）
	cfg.E2EHost = task.NormalizeProbeDomain(cfg.E2EHost)
	cfg.E2EProtocol = strings.ToLower(strings.TrimSpace(cfg.E2EProtocol))
	if cfg.E2EProtocol != "vless" && cfg.E2EProtocol != "trojan" {
		cfg.E2EProtocol = "vless"
	}
	if cfg.E2ETarget == "" {
		cfg.E2ETarget = task.DefaultE2ETarget
	}
	if cfg.E2EPort <= 0 {
		cfg.E2EPort = cfg.Port
	}
	if cfg.E2EConcurrency <= 0 {
		cfg.E2EConcurrency = 16
	}
	if cfg.E2EConcurrency > 64 {
		cfg.E2EConcurrency = 64
	}
	if cfg.E2ERetries <= 0 {
		cfg.E2ERetries = 3
	}
	if cfg.E2ERetries > 5 {
		cfg.E2ERetries = 5
	}
	if cfg.E2ELimit <= 0 {
		cfg.E2ELimit = 150
	}
	if cfg.E2ELimit > 2000 {
		cfg.E2ELimit = 2000
	}
	// 0 是旧配置的零值，统一补成默认 3000；想「不限制」可以设一个很大的值。
	if cfg.E2EMaxLatency <= 0 {
		cfg.E2EMaxLatency = 3000
	}
	// 启用端到端后，「延迟上限」只作用于端到端；L1 仅作为内部候选池，
	// 不再按用户可见的阈值提前筛掉候选。
	if cfg.E2EEnabled {
		cfg.MaxDelay = 9999
		cfg.MinDelay = 0
	}
	cfg.CountryFilter = task.NormalizeCountryFilter(cfg.CountryFilter)
	if cfg.CountryFilter == nil {
		cfg.CountryFilter = []string{}
	}
	if cfg.PoolLimit <= 0 {
		cfg.PoolLimit = 200
	}
	if cfg.PoolLimit > 2000 {
		cfg.PoolLimit = 2000
	}
	if cfg.CoarseLimit <= 0 {
		cfg.CoarseLimit = 60
	}
	if cfg.CoarseLimit > 500 {
		cfg.CoarseLimit = 500
	}
	if cfg.CoarseTimeout <= 0 {
		cfg.CoarseTimeout = 1500
	}
	if cfg.CoarseTimeout < 500 {
		cfg.CoarseTimeout = 500
	}
	if cfg.CoarseTimeout > 10000 {
		cfg.CoarseTimeout = 10000
	}
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
	if err := a.validateE2EForStart(cfg); err != nil {
		a.mu.Unlock()
		return fmt.Errorf("端到端配置有误：%w", err)
	}
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

// validateE2EForStart 在开始测速前校验端到端配置。
// 凭据不完整时**不报错**——只跳过；填了但格式错才报错，避免静默不生效。
func (a *App) validateE2EForStart(cfg Config) error {
	if !cfg.E2EEnabled {
		return nil
	}
	creds := a.readE2ECredentials()
	if !hasCompleteE2ECredentials(cfg, creds) {
		return nil
	}
	vcfg := task.E2EConfig{
		Protocol:  cfg.E2EProtocol,
		Host:      cfg.E2EHost,
		Port:      cfg.E2EPort,
		UUID:      creds.UUID,
		Password:  creds.Password,
		WSPath:    creds.WSPath,
		TargetURL: cfg.E2ETarget,
	}
	return vcfg.Validate()
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
	// 跑完后按「评级 → 成功率 → 平均 TLS」排名；
	// 运行中保持完成顺序，否则每推一条整张表都会重排，行一直跳。
	if !a.domRunning && len(rs) > 1 {
		rs = task.SortDomainResults(rs)
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
	// 兜底归一化：API 层已经做过，但集成测试/CLI 可能直接调用。
	// 不补这一步时，HTTPing 模式的 URL 为空会让所有 IP 失败。
	cfg = normalizeConfig(cfg)
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
	// 下载测速每完成一个达标 IP 就实时推给界面，不必等全部结束。
	// e2eMap 在端到端阶段之后才赋值；闭包按引用读取。
	var e2eMap map[string]*task.E2EResult
	task.ResultHook = func(d utils.CloudflareIPData) {
		item := convertResultItem(d)
		if e2eMap != nil {
			if r, ok := e2eMap[fmt.Sprintf("%s:%d", item.IP, item.Port)]; ok {
				item.E2E = r
			}
		}
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

	// 历史优质 IP 池：上次端到端通过的地址优先参与本轮。
	// 放在在线来源抓取之后、域名探测之前，保证 ExtraTargets 已就绪。
	if cfg.PoolEnabled {
		if pool := task.LoadIPPool(a.poolPath()); pool != nil {
			poolTargets := pool.Targets(cfg.PoolLimit)
			if len(poolTargets) > 0 {
				task.ExtraTargets = append(poolTargets, task.ExtraTargets...)
				a.appendLog("info", "pool", fmt.Sprintf("已载入历史优质 IP：%d 个", len(poolTargets)))
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

	// ---- 端到端（真实隧道）终筛 ----
	//
	// 为什么放在下载之前：L1（到 CF 边缘）与 L3（端到端）几乎不相关；
	// 先下载再终筛会把最慢的下载时间花在客户端根本不可用的 IP 上。
	// 未启用端到端时，按 L1 的 colo 做国家过滤。
	// TCP 模式没有 colo，此时 MatchCountry 会保留全部（无法判断就不误杀）。
	if !cfg.E2EEnabled && len(cfg.CountryFilter) > 0 {
		filtered := make(utils.PingDelaySet, 0, len(pingData))
		for _, item := range pingData {
			if task.MatchCountry(item.Colo, cfg.CountryFilter) {
				filtered = append(filtered, item)
			}
		}
		if len(filtered) != len(pingData) {
			a.appendLog("info", "filter", fmt.Sprintf(
				"国家/地区筛选：%d → %d", len(pingData), len(filtered)))
		}
		pingData = filtered
	}

	downloadInput := pingData
	if cfg.E2EEnabled {
		creds := a.readE2ECredentials()
		if !hasCompleteE2ECredentials(cfg, creds) {
			a.appendLog("warn", "e2e", "未填写完整的端到端凭据，已跳过端到端测速")
		} else {
			candidates := task.SelectE2ECandidates(pingData, cfg.E2ELimit, cfg.TestCount)
			a.mu.Lock()
			a.phase = "e2e"
			a.mu.Unlock()
			a.emit("test:phase", "e2e")
			a.appendLog("info", "e2e", fmt.Sprintf(
				"开始端到端测速 | 候选=%d 并发=%d 重试=%d 目标=%s",
				len(candidates), cfg.E2EConcurrency, cfg.E2ERetries, cfg.E2ETarget))

			index := make(map[string]utils.CloudflareIPData, len(candidates))
			targets := make([]*task.Target, 0, len(candidates))
			for _, item := range candidates {
				index[fmt.Sprintf("%s:%d", item.IP.String(), item.Port)] = item
				targets = append(targets, &task.Target{IP: item.IP, Port: item.Port})
			}
			e2eCfg := task.E2EConfig{
				Protocol:       cfg.E2EProtocol,
				Host:           cfg.E2EHost,
				Port:           cfg.E2EPort,
				UUID:           creds.UUID,
				Password:       creds.Password,
				WSPath:         creds.WSPath,
				TargetURL:      cfg.E2ETarget,
				Concurrency:    cfg.E2EConcurrency,
				Attempts:       cfg.E2ERetries,
				AttemptTimeout: 8 * time.Second,
				MaxLatency:     time.Duration(cfg.E2EMaxLatency) * time.Millisecond,
				Limit:          cfg.E2ELimit,
				Insecure:       cfg.E2EInsecure,
			}
			e2eResults := task.ProbeE2E(targets, e2eCfg, func(done, total, passed int, r *task.E2EResult) {
				if task.ProgressHook != nil {
					task.ProgressHook(task.ProgressEvent{
						Stage: "e2e", Current: done, Total: total, Available: passed,
						IP: r.IP, Port: r.Port, Msg: e2eProgressMessage(r),
					})
				}
			})

			if cfg.PoolEnabled {
				pool := task.LoadIPPool(a.poolPath())
				if pool == nil {
					pool = task.NewIPPool()
				}
				pool.MergeResults(e2eResults)
				if err := task.SaveIPPool(a.poolPath(), pool); err == nil {
					a.appendLog("info", "pool", fmt.Sprintf("已更新历史优质 IP：%d 个", pool.Count()))
				}
			}

			e2eMap = make(map[string]*task.E2EResult, len(e2eResults))
			stages := map[string]int{}
			passed := make(utils.PingDelaySet, 0, len(e2eResults))
			countryFiltered := 0
			for i := range e2eResults {
				r := &e2eResults[i]
				key := fmt.Sprintf("%s:%d", r.IP, r.Port)
				e2eMap[key] = r
				if !r.OK {
					stages[r.Stage]++
					continue
				}
				// 目标响应没带 cf-ray 时，退回 L1 的 colo（HTTPing 模式才有）。
				if r.Colo == "" {
					if item, ok := index[key]; ok {
						r.Colo = item.Colo
					}
				}
				if cfg.E2EMaxLatency > 0 && r.Best > float64(cfg.E2EMaxLatency) {
					continue
				}
				if len(cfg.CountryFilter) > 0 && !task.MatchCountry(r.Colo, cfg.CountryFilter) {
					countryFiltered++
					continue
				}
				if item, ok := index[key]; ok {
					passed = append(passed, item)
				}
			}
			sort.SliceStable(passed, func(i, j int) bool {
				ki := fmt.Sprintf("%s:%d", passed[i].IP.String(), passed[i].Port)
				kj := fmt.Sprintf("%s:%d", passed[j].IP.String(), passed[j].Port)
				return e2eMap[ki].Best < e2eMap[kj].Best
			})
			downloadInput = passed
			a.appendLog("info", "e2e", fmt.Sprintf(
				"端到端测速完成 | 候选=%d 通过=%d 国家过滤=%d", len(e2eResults), len(passed), countryFiltered))
			if len(passed) == 0 {
				reason := e2eReason(e2eResults, cfg)
				suggestion := "检查域名、UUID/密码、WS 路径是否与客户端一致；也可能是 Worker 的 PROXYIP 中继不可用，可先用「ProxyIP 优选」换一个中继再试。"
				if countryFiltered > 0 {
					reason = fmt.Sprintf("有 %d 个 IP 端到端可用，但都不在所选国家/地区内", countryFiltered)
					suggestion = "放宽或清空「国家/地区」筛选，或提高端到端候选上限后重试。"
				}
				d := &Diagnosis{
					TotalTargets: totalTargets, ProbePassed: probePassed,
					Available: availableCount, AfterDelay: afterDelay,
					E2ECandidates: len(e2eResults), E2EPassed: 0,
					E2EFailed: len(e2eResults), E2EStages: stages,
					Reason:     reason,
					Suggestion: suggestion,
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
		}
	}

	// 两轮测速：先对端到端通过的候选做短时粗排，再对前 N 做完整精测。
	// 粗排值只用于排序，绝不写进结果。
	if cfg.CoarseEnabled && !cfg.DisableDownload && len(downloadInput) > cfg.TestCount {
		limit := cfg.CoarseLimit
		if limit <= 0 {
			limit = 60
		}
		if limit > len(downloadInput) {
			limit = len(downloadInput)
		}
		coarseTargets := make([]*task.Target, 0, limit)
		for _, item := range downloadInput[:limit] {
			coarseTargets = append(coarseTargets, &task.Target{IP: item.IP, Port: item.Port})
		}
		a.mu.Lock()
		a.phase = "coarse"
		a.mu.Unlock()
		a.emit("test:phase", "coarse")
		a.appendLog("info", "coarse", fmt.Sprintf(
			"开始两轮测速粗排 | 候选=%d 单次=%dms", limit, cfg.CoarseTimeout))
		speeds := task.CoarseSpeed(coarseTargets, time.Duration(cfg.CoarseTimeout)*time.Millisecond, limit,
			func(done, total int, r task.CoarseResult) {
				if task.ProgressHook != nil {
					task.ProgressHook(task.ProgressEvent{
						Stage: "coarse", Current: done, Total: total,
						IP: r.IP, Port: r.Port, Speed: r.Speed,
					})
				}
			})
		sort.SliceStable(downloadInput, func(i, j int) bool {
			ki := fmt.Sprintf("%s:%d", downloadInput[i].IP.String(), downloadInput[i].Port)
			kj := fmt.Sprintf("%s:%d", downloadInput[j].IP.String(), downloadInput[j].Port)
			return speeds[ki] > speeds[kj]
		})
	}

	a.mu.Lock()
	a.phase = "download"
	a.mu.Unlock()
	a.emit("test:phase", "download")

	speedData := task.TestDownloadSpeed(downloadInput)
	results := convertResults(speedData)
	attachE2E(results, e2eMap)
	if e2eMap == nil {
		// 没有端到端时按网段分散（P0）；有端到端时以真实延迟为准。
		results = diversifyResultItems(results)
	} else {
		sortResultsByE2E(results)
	}

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
		IP:        d.IP.String(),
		Port:      d.Port,
		Sent:      d.Sended,
		Received:  d.Received,
		LossRate:  loss,
		Delay:     float64(d.Delay.Microseconds()) / 1000.0,
		Speed:     d.DownloadSpeed / 1024 / 1024,
		PeakSpeed: d.PeakSpeed / 1024 / 1024,
		Colo:      colo,
	}
}

func convertResults(data utils.DownloadSpeedSet) []ResultItem {
	out := make([]ResultItem, 0, len(data))
	for _, d := range data {
		out = append(out, convertResultItem(d))
	}
	return out
}

// hasCompleteE2ECredentials 判断是否具备跑端到端探测的最低条件。
// 不完整时只记警告并跳过，不阻塞测速——否则旧用户升级后第一次测速会被卡住。
func hasCompleteE2ECredentials(cfg Config, c E2ECredentials) bool {
	if strings.TrimSpace(cfg.E2EHost) == "" {
		return false
	}
	if strings.TrimSpace(c.WSPath) == "" {
		return false
	}
	if cfg.E2EProtocol == "trojan" {
		return strings.TrimSpace(c.Password) != ""
	}
	return strings.TrimSpace(c.UUID) != ""
}

func e2eProgressMessage(r *task.E2EResult) string {
	if r.OK {
		if r.Warm > 0 {
			return fmt.Sprintf("%.0fms（冷启动 %.0fms）", r.Best, r.Cold)
		}
		return fmt.Sprintf("%.0fms", r.Best)
	}
	return r.Error
}

func e2eReason(results []task.E2EResult, cfg Config) string {
	failed, auth := 0, 0
	for i := range results {
		if results[i].OK {
			continue
		}
		failed++
		if results[i].Status == task.E2EStatusProtocolAuth {
			auth++
		}
	}
	if failed > 0 && auth*2 >= failed {
		return fmt.Sprintf("端到端探测了 %d 个 IP，其中 %d 个在认证阶段失败：UUID/密码或协议选错了", len(results), auth)
	}
	return fmt.Sprintf("端到端探测了 %d 个 IP，没有一个能在 %dms 内完成真实请求", len(results), cfg.E2EMaxLatency)
}

func attachE2E(results []ResultItem, m map[string]*task.E2EResult) {
	if len(m) == 0 {
		return
	}
	for i := range results {
		if r, ok := m[fmt.Sprintf("%s:%d", results[i].IP, results[i].Port)]; ok {
			results[i].E2E = r
			// 端到端拿到的是目标响应里的真实边缘机房，比 L1 更可信。
			if r.Colo != "" {
				results[i].Colo = r.Colo
			}
		}
	}
}

// diversifyResultItems 按 IPv4 /16、IPv6 /48 轮转重排，避免前 N 名挤在同一网段。
// 只调顺序、不截断：前端按 printNum 截断后自然分散，导出也受益。
func diversifyResultItems(results []ResultItem) []ResultItem {
	if len(results) <= 1 {
		return results
	}
	buckets := make(map[string][]ResultItem)
	order := make([]string, 0, 16)
	for _, r := range results {
		key := resultBucketKey(r.IP)
		if _, ok := buckets[key]; !ok {
			order = append(order, key)
		}
		buckets[key] = append(buckets[key], r)
	}
	out := make([]ResultItem, 0, len(results))
	for round := 0; len(out) < len(results); round++ {
		progressed := false
		for _, key := range order {
			if round < len(buckets[key]) {
				out = append(out, buckets[key][round])
				progressed = true
			}
		}
		if !progressed {
			break
		}
	}
	return out
}

func resultBucketKey(ip string) string {
	if strings.Contains(ip, ":") {
		parts := strings.Split(ip, ":")
		if len(parts) >= 3 {
			return parts[0] + ":" + parts[1] + ":" + parts[2]
		}
		return ip
	}
	parts := strings.Split(ip, ".")
	if len(parts) >= 2 {
		return parts[0] + "." + parts[1]
	}
	return ip
}

// sortResultsByE2E：有端到端的按 Best 升序，失败/无端到端排后，速度降序兜底。
func sortResultsByE2E(results []ResultItem) {
	sort.SliceStable(results, func(i, j int) bool {
		ai, aj := results[i].E2E, results[j].E2E
		aok := ai != nil && ai.OK
		bok := aj != nil && aj.OK
		if aok != bok {
			return aok
		}
		if aok && bok && ai.Best != aj.Best {
			return ai.Best < aj.Best
		}
		return results[i].Speed > results[j].Speed
	})
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

// orderResults 按前端当前展示顺序（评分排序）重排结果。
// order 是 "ip:port" 列表；不在列表里的结果保持原顺序排在后面。
func orderResults(results []ResultItem, order []string) []ResultItem {
	if len(order) == 0 || len(results) <= 1 {
		return results
	}
	index := make(map[string]int, len(order))
	for i, k := range order {
		if _, ok := index[k]; !ok {
			index[k] = i
		}
	}
	out := append([]ResultItem(nil), results...)
	sort.SliceStable(out, func(i, j int) bool {
		ki := fmt.Sprintf("%s:%d", out[i].IP, out[i].Port)
		kj := fmt.Sprintf("%s:%d", out[j].IP, out[j].Port)
		ii, iok := index[ki]
		jj, jok := index[kj]
		switch {
		case iok && jok:
			return ii < jj
		case iok:
			return true
		case jok:
			return false
		default:
			return false
		}
	})
	return out
}

// ExportCSV 导出结果。path 为空时写入当前目录的 result.csv。
// order 非空时按前端展示顺序导出。
func (a *App) ExportCSV(path string, order []string) (string, error) {
	a.mu.Lock()
	results := orderResults(a.results, order)
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
	_ = w.Write([]string{"IP 地址", "端口", "已发送", "已接收", "丢包率", "平均延迟", "下载速度(MB/s)", "地区码", "端到端(ms)", "端到端冷启动(ms)", "端到端状态"})
	for _, r := range results {
		e2eBest, e2eCold, e2eStatus := "", "", ""
		if r.E2E != nil && r.E2E.Tested {
			if r.E2E.OK {
				e2eBest = fmt.Sprintf("%.0f", r.E2E.Best)
				e2eCold = fmt.Sprintf("%.0f", r.E2E.Cold)
				e2eStatus = "ok"
			} else {
				e2eStatus = r.E2E.Status
			}
		}
		_ = w.Write([]string{
			r.IP,
			strconv.Itoa(r.Port),
			strconv.Itoa(r.Sent),
			strconv.Itoa(r.Received),
			fmt.Sprintf("%.2f", r.LossRate),
			fmt.Sprintf("%.2f", r.Delay),
			fmt.Sprintf("%.2f", r.Speed),
			r.Colo,
			e2eBest,
			e2eCold,
			e2eStatus,
		})
	}
	w.Flush()
	if err := w.Error(); err != nil {
		return "", err
	}
	return abs, nil
}

// ExportTXT 以「IP:端口#中文地区 速度」格式导出结果，可直接作为 IP 列表再次使用。
// order 非空时按前端展示顺序导出。
func (a *App) ExportTXT(path string, order []string) (string, error) {
	a.mu.Lock()
	results := orderResults(a.results, order)
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
		fmt.Fprintf(&b, "%s:%d#%s %.2fMB/s\n", r.IP, port, task.ColoLabel(r.Colo), r.Speed)
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
	// 测试/便携场景可以用 CFSTX_CONFIG_DIR 覆盖，
	// 避免集成测试写进用户真实的 config.json / history.json。
	if dir := strings.TrimSpace(os.Getenv("CFSTX_CONFIG_DIR")); dir != "" {
		_ = os.MkdirAll(dir, 0o755)
		return dir
	}
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

// e2eSecretsPath 是端到端凭据的独立存储。
// 为什么不放进 config.json：UUID/口令/WS 路径等同于访问凭据，
// 而 config.json 会被历史记录、导出、日志路径引用，混在一起容易泄露。
func (a *App) e2eSecretsPath() string { return filepath.Join(configDir(), "e2e_secrets.json") }

// poolPath 是历史优质 IP 池。
func (a *App) poolPath() string { return filepath.Join(configDir(), "ip_pool.json") }

// GetPoolCount 返回池里有效的条目数。
func (a *App) GetPoolCount() int {
	return task.LoadIPPool(a.poolPath()).Count()
}

// ClearPool 清空历史优质 IP。
func (a *App) ClearPool() error {
	err := os.Remove(a.poolPath())
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// E2ECredentials 是端到端探测所需的敏感信息。
type E2ECredentials struct {
	UUID     string `json:"uuid,omitempty"`
	Password string `json:"password,omitempty"`
	WSPath   string `json:"wsPath,omitempty"`
}

// E2ECredentialStatus 只回显「有没有」，不回显内容。
type E2ECredentialStatus struct {
	HasUUID     bool `json:"hasUuid"`
	HasPassword bool `json:"hasPassword"`
	HasPath     bool `json:"hasPath"`
}

func (a *App) readE2ECredentials() E2ECredentials {
	var c E2ECredentials
	_ = readJSONFile(a.e2eSecretsPath(), &c)
	return c
}

// SaveE2ECredentials 合并保存：空字段表示不修改。
func (a *App) SaveE2ECredentials(patch E2ECredentials) error {
	cur := a.readE2ECredentials()
	if strings.TrimSpace(patch.UUID) != "" {
		cur.UUID = strings.TrimSpace(patch.UUID)
	}
	if strings.TrimSpace(patch.Password) != "" {
		cur.Password = strings.TrimSpace(patch.Password)
	}
	if strings.TrimSpace(patch.WSPath) != "" {
		cur.WSPath = strings.TrimSpace(patch.WSPath)
	}
	data, err := json.MarshalIndent(cur, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(a.e2eSecretsPath(), data, 0o600)
}

func (a *App) ClearE2ECredentials() error {
	err := os.Remove(a.e2eSecretsPath())
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

func (a *App) GetE2ECredentialStatus() E2ECredentialStatus {
	c := a.readE2ECredentials()
	return E2ECredentialStatus{
		HasUUID:     c.UUID != "",
		HasPassword: c.Password != "",
		HasPath:     c.WSPath != "",
	}
}

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
