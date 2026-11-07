package main

import (
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
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
	sourceIPv4     = "ipv4"     // 内置 ip.txt（可替换为自定义文件）
	sourceIPv6     = "ipv6"     // 内置 ipv6.txt
	sourceBestCF   = "bestcf"   // 在线优选 IP（bestcf.pages.dev）
	sourceOfficial = "official" // Cloudflare 官方 IP 段 API
	sourceCmliu    = "cmliu"    // cmliu 整理的 CF-CIDR 列表
	sourceCustom   = "custom"   // 用户自定义的在线 IP 列表 URL
	sourceManual   = "manual"   // 手动输入的 IP 段
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
		!hasSource(cfg.Sources, sourceCmliu) && !hasSource(cfg.Sources, sourceCustom) &&
		!hasSource(cfg.Sources, sourceManual) {
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
	return fmt.Sprintf("%t|%t|%t|%d|%t|%s",
		opts.IncludeBestCF, opts.IncludeOfficialCIDR, opts.IncludeCmliuCIDR,
		opts.DefaultPort, opts.AllowIPv6, strings.Join(opts.CustomSources, ","))
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
	if hasSource(cfg.Sources, sourceBestCF) || hasSource(cfg.Sources, sourceOfficial) ||
		hasSource(cfg.Sources, sourceCmliu) || hasSource(cfg.Sources, sourceCustom) {
		opts := task.FetchOptions{
			DefaultPort:         cfg.Port,
			AllowIPv6:           hasSource(cfg.Sources, sourceIPv6),
			IncludeBestCF:       hasSource(cfg.Sources, sourceBestCF),
			IncludeOfficialCIDR: hasSource(cfg.Sources, sourceOfficial),
			IncludeCmliuCIDR:    hasSource(cfg.Sources, sourceCmliu),
			CustomSources:       splitSourceLines(cfg.CustomSources),
			OnProgress: func(done, total int, msg string) {
				task.ProgressHook(task.ProgressEvent{
					Stage: "fetch", Current: done, Total: total, Msg: msg,
				})
			},
		}
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
