package main

import (
	"encoding/csv"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"time"

	"gitee.com/liliaic/cfstx/task"
	"gitee.com/liliaic/cfstx/utils"
	"gitee.com/liliaic/cfstx/version"
)

// Config 是界面上可调整的全部测速参数。
type Config struct {
	// 延迟测速
	Threads     int    `json:"threads"`
	PingTimes   int    `json:"pingTimes"`
	Port        int    `json:"port"`
	IPFile      string `json:"ipFile"`
	IPText      string `json:"ipText"`
	Httping     bool   `json:"httping"`
	HttpingCode int    `json:"httpingCode"`
	Colo        string `json:"colo"`

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

// ProgressInfo 是进度信息。
type ProgressInfo struct {
	Current   int     `json:"current"`
	Total     int     `json:"total"`
	Available int     `json:"available"`
	IP        string  `json:"ip"`
	Speed     float64 `json:"speed"`
}

// App 承载全部业务逻辑，与界面实现无关。
type App struct {
	mu       sync.Mutex
	hub      *EventHub
	running  bool
	phase    string
	progress ProgressInfo
	results  []ResultItem
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
		IPFile:          "ip.txt",
		Httping:         false,
		HttpingCode:     0,
		MaxDelay:        9999,
		MinDelay:        0,
		MaxLossRate:     1.0,
		MinSpeed:        0,
		DownloadTime:    10,
		TestCount:       10,
		URL:             "https://cf.xiu2.xyz/url",
		OutputFile:      "result.csv",
		PrintNum:        10,
		DisableDownload: false,
	}
}

// GetConfig 读取已保存的配置，没有则返回默认值。
func (a *App) GetConfig() Config {
	var cfg Config
	if err := readJSONFile(a.configPath(), &cfg); err != nil {
		return defaultConfig()
	}
	return cfg
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
	a.running = true
	a.phase = "ping"
	a.results = nil
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

	task.ProgressHook = func(e task.ProgressEvent) {
		info := ProgressInfo{
			Current:   e.Current,
			Total:     e.Total,
			Available: e.Available,
			IP:        e.IP,
			Speed:     e.Speed,
		}
		a.mu.Lock()
		a.phase = e.Stage
		a.progress = info
		a.mu.Unlock()

		a.emit("test:progress", e)
	}
	task.LogHook = func(e task.LogEvent) {
		a.emit("test:log", e)
	}
	defer func() {
		task.ProgressHook = nil
		task.LogHook = nil
	}()

	task.InitRandSeed()
	a.emit("test:started", map[string]any{"time": start.Format("15:04:05")})

	pingData := task.NewPing().Run().FilterDelay().FilterLossRate()

	a.mu.Lock()
	a.phase = "download"
	a.mu.Unlock()
	a.emit("test:phase", "download")

	speedData := task.TestDownloadSpeed(pingData)
	results := convertResults(speedData)

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

	a.emit("test:finished", map[string]any{
		"results":  results,
		"duration": duration,
	})
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
	task.IPFile = cfg.IPFile
	task.IPText = cfg.IPText

	utils.InputMaxDelay = time.Duration(cfg.MaxDelay) * time.Millisecond
	utils.InputMinDelay = time.Duration(cfg.MinDelay) * time.Millisecond
	utils.InputMaxLossRate = float32(cfg.MaxLossRate)
	utils.Debug = cfg.Debug
	utils.Output = cfg.OutputFile
	utils.PrintNum = cfg.PrintNum
	utils.Silent = true
}

func convertResults(data utils.DownloadSpeedSet) []ResultItem {
	out := make([]ResultItem, 0, len(data))
	for _, d := range data {
		loss := 0.0
		if d.Sended > 0 {
			loss = float64(d.Sended-d.Received) / float64(d.Sended)
		}
		colo := d.Colo
		if colo == "" {
			colo = "N/A"
		}
		out = append(out, ResultItem{
			IP:       d.IP.String(),
			Sent:     d.Sended,
			Received: d.Received,
			LossRate: loss,
			Delay:    float64(d.Delay.Microseconds()) / 1000.0,
			Speed:    d.DownloadSpeed / 1024 / 1024,
			Colo:     colo,
		})
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
	_ = w.Write([]string{"IP 地址", "已发送", "已接收", "丢包率", "平均延迟", "下载速度(MB/s)", "地区码"})
	for _, r := range results {
		_ = w.Write([]string{
			r.IP,
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

// ---------------------------------------------------------------------------
// 历史记录
// ---------------------------------------------------------------------------

// GetHistory 返回历史记录（最新在前）。
func (a *App) GetHistory() []HistoryRecord {
	var list []HistoryRecord
	if err := readJSONFile(a.historyPath(), &list); err != nil {
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
