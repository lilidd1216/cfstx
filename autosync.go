package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"gitee.com/liliaic/cfstx/task"
)

// maxAutoSyncPush 是单次推送的硬上限，避免把面板写爆。
const maxAutoSyncPush = 200

// AutoSyncFilters 是推送前的二次筛选条件，多个条件可同时生效。
// 语义：地区 → 速度下限 → 延迟上限 → 按速度降序取前 N。
type AutoSyncFilters struct {
	SpeedTopN int      `json:"speedTopN"` // 按下载速度取前 N，0=不截断
	MaxDelay  int      `json:"maxDelay"`  // 延迟上限 ms，0=不限
	Countries []string `json:"countries"` // 国家/地区代码，空=不过滤
	MinSpeed  float64  `json:"minSpeed"`  // 下载速度下限 MB/s，0=不限
}

// AutoSyncConfig 是自动同步的持久化配置。密码不在这里，单独存 panel_secrets.json。
type AutoSyncConfig struct {
	Enabled   bool            `json:"enabled"`
	DailyTime string          `json:"dailyTime"` // 每天运行时刻，格式 HH:MM（本地时间）
	PanelURL  string          `json:"panelUrl"`
	UserAgent string          `json:"userAgent"`
	Filters   AutoSyncFilters `json:"filters"`
}

// AutoSyncConfigView 是返回给前端的配置视图，密码只回显是否已设置。
type AutoSyncConfigView struct {
	AutoSyncConfig
	HasPassword bool `json:"hasPassword"`
}

// AutoSyncPushItem 是推送列表中的一条记录。
type AutoSyncPushItem struct {
	IP    string  `json:"ip"`
	Port  int     `json:"port"`
	Colo  string  `json:"colo"`
	Speed float64 `json:"speed"`
	Delay float64 `json:"delay"`
}

// AutoSyncStatus 是调度器运行状态快照。
type AutoSyncStatus struct {
	Enabled         bool               `json:"enabled"`
	Running         bool               `json:"running"`
	DailyTime       string             `json:"dailyTime"`
	NextRun         string             `json:"nextRun"`
	LastRun         string             `json:"lastRun"`
	LastStatus      string             `json:"lastStatus"`
	LastError       string             `json:"lastError"`
	LastWarn        string             `json:"lastWarn"`
	LastPushedCount int                `json:"lastPushedCount"`
	LastPushed      []AutoSyncPushItem `json:"lastPushed"`
	HasPassword     bool               `json:"hasPassword"`
}

// SelectionReport 记录一次筛选各阶段的数量，便于界面展示与排查。
type SelectionReport struct {
	Input         int      `json:"input"`
	AfterCountry  int      `json:"afterCountry"`
	AfterMinSpeed int      `json:"afterMinSpeed"`
	AfterMaxDelay int      `json:"afterMaxDelay"`
	Selected      int      `json:"selected"`
	Warnings      []string `json:"warnings"`
}

// AutoScheduler 是自动同步的定时调度器，跑在 GUI 进程内。
type AutoScheduler struct {
	app *App

	mu       sync.Mutex
	cfg      AutoSyncConfig
	password string
	enabled  bool
	running  bool
	started  bool

	nextRun    time.Time
	lastRun    time.Time
	lastStatus string
	lastError  string
	lastWarn   string
	lastPushed []AutoSyncPushItem

	wake chan struct{}
	stop chan struct{}
}

func NewAutoScheduler(app *App) *AutoScheduler {
	return &AutoScheduler{app: app, lastStatus: "never"}
}

// Start 读取持久化配置并启动调度循环。
func (s *AutoScheduler) Start() {
	s.mu.Lock()
	if s.started {
		s.mu.Unlock()
		return
	}
	s.started = true

	var cfg AutoSyncConfig
	if err := readJSONFile(s.app.schedulePath(), &cfg); err != nil {
		cfg = defaultAutoSyncConfig()
	}
	cfg = normalizeAutoSyncConfig(cfg)
	s.cfg = cfg
	s.enabled = cfg.Enabled
	s.password = s.app.readPanelPassword()
	if cfg.Enabled {
		s.nextRun = nextRunFrom(time.Now(), cfg)
	}
	s.wake = make(chan struct{}, 1)
	s.stop = make(chan struct{})
	s.mu.Unlock()

	go s.loop()
}

// Stop 停止调度循环。正在执行的测速由 App.Shutdown 里的 task.Cancel 负责打断。
func (s *AutoScheduler) Stop() {
	s.mu.Lock()
	if !s.started {
		s.mu.Unlock()
		return
	}
	s.started = false
	stop := s.stop
	s.mu.Unlock()
	if stop != nil {
		close(stop)
	}
}

// Apply 应用新配置，并唤醒调度循环重新计算下次运行时间。
func (s *AutoScheduler) Apply(cfg AutoSyncConfig, password string) {
	s.mu.Lock()
	s.cfg = cfg
	s.enabled = cfg.Enabled
	if password != "" {
		s.password = password
	}
	if cfg.Enabled {
		s.nextRun = nextRunFrom(time.Now(), cfg)
	} else {
		s.nextRun = time.Time{}
	}
	wake := s.wake
	s.mu.Unlock()

	if wake != nil {
		select {
		case wake <- struct{}{}:
		default:
		}
	}
}

// RunNow 立即异步执行一次同步。
func (s *AutoScheduler) RunNow() error {
	s.mu.Lock()
	if s.running {
		s.mu.Unlock()
		return fmt.Errorf("自动同步正在运行中")
	}
	if s.password == "" {
		s.mu.Unlock()
		return fmt.Errorf("尚未设置面板密码")
	}
	if strings.TrimSpace(s.cfg.PanelURL) == "" {
		s.mu.Unlock()
		return fmt.Errorf("尚未设置面板地址")
	}
	s.mu.Unlock()

	go s.runCycle("manual")
	return nil
}

// Status 返回当前状态快照。
func (s *AutoScheduler) Status() AutoSyncStatus {
	s.mu.Lock()
	defer s.mu.Unlock()

	st := AutoSyncStatus{
		Enabled:         s.enabled,
		Running:         s.running,
		DailyTime:       s.cfg.DailyTime,
		LastStatus:      s.lastStatus,
		LastError:       s.lastError,
		LastWarn:        s.lastWarn,
		LastPushedCount: len(s.lastPushed),
		LastPushed:      s.lastPushed,
	}
	if !s.nextRun.IsZero() {
		st.NextRun = s.nextRun.Format(time.RFC3339)
	}
	if !s.lastRun.IsZero() {
		st.LastRun = s.lastRun.Format(time.RFC3339)
	}
	return st
}

func (s *AutoScheduler) loop() {
	for {
		s.mu.Lock()
		enabled := s.enabled
		next := s.nextRun
		stopCh := s.stop
		wake := s.wake
		s.mu.Unlock()

		if !enabled || next.IsZero() {
			select {
			case <-stopCh:
				return
			case <-wake:
				continue
			}
		}

		delay := time.Until(next)
		if delay < 0 {
			delay = 0
		}
		timer := time.NewTimer(delay)
		select {
		case <-timer.C:
			s.runCycle("schedule")
		case <-wake:
			timer.Stop()
		case <-stopCh:
			timer.Stop()
			return
		}
	}
}

// runCycle 执行一次完整的「预检 → 测速 → 筛选 → 推送」。
func (s *AutoScheduler) runCycle(trigger string) {
	s.mu.Lock()
	if s.running || (!s.enabled && trigger == "schedule") {
		s.mu.Unlock()
		return
	}
	s.running = true
	cfg := s.cfg
	password := s.password
	s.lastStatus = "running"
	s.lastError = ""
	s.lastWarn = ""
	s.mu.Unlock()

	defer func() {
		if r := recover(); r != nil {
			s.finish("error", fmt.Sprintf("内部错误：%v", r), "", nil)
		}
		s.mu.Lock()
		s.running = false
		s.mu.Unlock()
	}()

	s.app.emit("autosync:started", map[string]any{
		"time":    time.Now().Format("15:04:05"),
		"trigger": trigger,
	})

	if strings.TrimSpace(cfg.PanelURL) == "" {
		s.finish("error", "尚未设置面板地址", "", nil)
		return
	}
	if password == "" {
		s.finish("error", "尚未设置面板密码", "", nil)
		return
	}

	client, err := task.NewPanelClient(cfg.PanelURL, cfg.UserAgent)
	if err != nil {
		s.finish("error", err.Error(), "", nil)
		return
	}

	// 1. 预检面板：登录 + 校验优选订阅开关（只读，不改面板配置）
	preCtx, cancelPre := context.WithTimeout(context.Background(), 30*time.Second)
	ready, err := client.Preflight(preCtx, password)
	cancelPre()
	if err != nil {
		s.finish("error", "面板预检失败："+err.Error(), "", nil)
		return
	}
	warn := strings.Join(ready.Warnings, "；")
	if warn != "" {
		s.mu.Lock()
		s.lastWarn = warn
		s.mu.Unlock()
		s.app.emit("autosync:panel", map[string]any{"readiness": ready})
	}

	// 2. 复用当前保存的测速配置跑一次测速（与手动测速共用互斥）
	testCfg := s.app.GetConfig()
	results, err := s.app.runAutoTest(testCfg)
	if err != nil {
		if err == task.ErrCancelled {
			s.finish("canceled", "", warn, nil)
			return
		}
		s.finish("error", err.Error(), warn, nil)
		return
	}

	// 3. 多条件筛选
	selected, report := selectAutoSyncResults(results, cfg.Filters)
	s.app.emit("autosync:filtered", map[string]any{
		"input":         report.Input,
		"afterCountry":  report.AfterCountry,
		"afterMinSpeed": report.AfterMinSpeed,
		"afterMaxDelay": report.AfterMaxDelay,
		"selected":      report.Selected,
		"warnings":      report.Warnings,
	})
	if len(selected) == 0 {
		msg := "本次没有 IP 满足筛选条件，已保留面板上一批 IP"
		if len(results) == 0 {
			msg = "本次测速没有产生结果，已保留面板上一批 IP"
		}
		s.finish("skipped", "", msg, nil)
		return
	}

	// 4. 推送
	lines := make([]string, 0, len(selected))
	for _, r := range selected {
		lines = append(lines, task.FormatPanelLine(r.IP, r.Port, task.ColoLabel(r.Colo), r.Speed))
	}
	pushCtx, cancelPush := context.WithTimeout(context.Background(), 30*time.Second)
	err = client.PushADD(pushCtx, strings.Join(lines, "\n"))
	cancelPush()
	if err != nil {
		s.finish("error", "推送失败："+err.Error(), warn, nil)
		return
	}

	items := make([]AutoSyncPushItem, 0, len(selected))
	for _, r := range selected {
		items = append(items, AutoSyncPushItem{
			IP: r.IP, Port: r.Port, Colo: r.Colo, Speed: r.Speed, Delay: r.Delay,
		})
	}

	status := "success"
	if warn != "" {
		status = "warn"
	}
	s.finish(status, "", warn, items)
}

// finish 统一记录结果并发出结束事件。
func (s *AutoScheduler) finish(status, errMsg, warn string, pushed []AutoSyncPushItem) {
	now := time.Now()

	s.mu.Lock()
	s.lastRun = now
	s.lastStatus = status
	if errMsg != "" {
		s.lastError = errMsg
	}
	if warn != "" {
		s.lastWarn = warn
	}
	if pushed != nil {
		s.lastPushed = pushed
	}
	if s.enabled {
		s.nextRun = nextRunFrom(now, s.cfg)
	}
	next := s.nextRun
	s.mu.Unlock()

	if errMsg != "" {
		s.app.emit("autosync:error", map[string]any{"message": errMsg})
	}
	if status == "skipped" {
		s.app.emit("autosync:skipped", map[string]any{"reason": "no_match", "message": warn})
	}
	nextStr := ""
	if !next.IsZero() {
		nextStr = next.Format(time.RFC3339)
	}
	s.app.emit("autosync:finished", map[string]any{
		"status":  status,
		"error":   errMsg,
		"warn":    warn,
		"pushed":  len(pushed),
		"nextRun": nextStr,
	})
}

// runAutoTest 以定时任务模式执行一次测速：不导出 CSV、不写历史。
func (a *App) runAutoTest(cfg Config) ([]ResultItem, error) {
	cfg = normalizeConfig(cfg)
	if err := a.beginTest(cfg); err != nil {
		return nil, err
	}
	return a.runTestWithOptions(cfg, autoTestOptions)
}

// ---------------------------------------------------------------------------
// 筛选
// ---------------------------------------------------------------------------

// selectAutoSyncResults 按多条件筛选优选 IP。
//
// 顺序：国家/地区 → 速度下限 → 延迟上限 → 按下载速度降序取前 N。
// 例：速度下限 15、延迟上限 200、速度 TopN 30 = 速度≥15MB/s 且延迟≤200ms 的最快 30 个。
// 国家筛选采用严格匹配：无法识别地区的 IP 不会被推送。
func selectAutoSyncResults(items []ResultItem, f AutoSyncFilters) ([]ResultItem, SelectionReport) {
	rep := SelectionReport{Input: len(items)}
	if len(items) == 0 {
		rep.Warnings = append(rep.Warnings, "本次测速没有产生结果")
		return nil, rep
	}

	filtered := make([]ResultItem, 0, len(items))
	for _, r := range items {
		if len(f.Countries) > 0 {
			country := task.CountryOfColo(r.Colo)
			if country == "" {
				continue
			}
			matched := false
			for _, code := range f.Countries {
				if strings.EqualFold(country, code) {
					matched = true
					break
				}
			}
			if !matched {
				continue
			}
		}
		filtered = append(filtered, r)
	}
	rep.AfterCountry = len(filtered)

	if f.MinSpeed > 0 {
		speedy := make([]ResultItem, 0, len(filtered))
		for _, r := range filtered {
			if r.Speed >= f.MinSpeed {
				speedy = append(speedy, r)
			}
		}
		filtered = speedy
	}
	rep.AfterMinSpeed = len(filtered)

	if f.MaxDelay > 0 {
		delayOK := make([]ResultItem, 0, len(filtered))
		for _, r := range filtered {
			d := resultDelay(r)
			// 延迟未知（0）视为不达标，避免把没测到延迟的 IP 当成"很快"。
			if d > 0 && d <= float64(f.MaxDelay) {
				delayOK = append(delayOK, r)
			}
		}
		filtered = delayOK
	}
	rep.AfterMaxDelay = len(filtered)

	if len(filtered) == 0 {
		return nil, rep
	}

	// 按下载速度降序；速度相同时按 ip:port 保证可复现。
	sort.SliceStable(filtered, func(i, j int) bool {
		if filtered[i].Speed != filtered[j].Speed {
			return filtered[i].Speed > filtered[j].Speed
		}
		return resultKey(filtered[i]) < resultKey(filtered[j])
	})

	selected := filtered
	if f.SpeedTopN > 0 && f.SpeedTopN < len(selected) {
		selected = selected[:f.SpeedTopN]
	}
	if len(selected) > maxAutoSyncPush {
		selected = selected[:maxAutoSyncPush]
	}
	rep.Selected = len(selected)
	return selected, rep
}

func resultKey(r ResultItem) string {
	return fmt.Sprintf("%s:%d", r.IP, r.Port)
}

// resultDelay 优先使用端到端真实延迟，没有时退回 L1 延迟。
func resultDelay(r ResultItem) float64 {
	if r.E2E != nil && r.E2E.OK && r.E2E.Best > 0 {
		return r.E2E.Best
	}
	return r.Delay
}

// ---------------------------------------------------------------------------
// 配置持久化
// ---------------------------------------------------------------------------

func defaultAutoSyncConfig() AutoSyncConfig {
	return AutoSyncConfig{
		DailyTime: "03:00",
		Filters:   AutoSyncFilters{SpeedTopN: 10},
	}
}

func normalizeAutoSyncConfig(c AutoSyncConfig) AutoSyncConfig {
	c.DailyTime = normalizeDailyTime(c.DailyTime)
	c.PanelURL = strings.TrimSpace(c.PanelURL)
	c.UserAgent = strings.TrimSpace(c.UserAgent)
	if c.UserAgent == "" {
		c.UserAgent = task.DefaultPanelUA
	}
	if c.Filters.SpeedTopN < 0 {
		c.Filters.SpeedTopN = 0
	}
	if c.Filters.SpeedTopN > maxAutoSyncPush {
		c.Filters.SpeedTopN = maxAutoSyncPush
	}
	if c.Filters.MaxDelay < 0 {
		c.Filters.MaxDelay = 0
	}
	if c.Filters.MinSpeed < 0 {
		c.Filters.MinSpeed = 0
	}
	c.Filters.Countries = task.NormalizeCountryFilter(c.Filters.Countries)
	return c
}

// normalizeDailyTime 校验并规范化 HH:MM，非法值回退到凌晨 3 点。
func normalizeDailyTime(s string) string {
	s = strings.TrimSpace(s)
	if t, err := time.Parse("15:04", s); err == nil {
		return t.Format("15:04")
	}
	return "03:00"
}

// nextRunFrom 计算下一次运行时刻：今天该时刻已过则顺延到明天。
func nextRunFrom(now time.Time, cfg AutoSyncConfig) time.Time {
	t, err := time.Parse("15:04", normalizeDailyTime(cfg.DailyTime))
	if err != nil {
		t = time.Date(0, 1, 1, 3, 0, 0, 0, time.UTC)
	}
	next := time.Date(now.Year(), now.Month(), now.Day(), t.Hour(), t.Minute(), 0, 0, now.Location())
	if !next.After(now) {
		next = next.AddDate(0, 0, 1)
	}
	return next
}

// GetAutoSyncConfig 返回脱敏后的配置。
func (a *App) GetAutoSyncConfig() AutoSyncConfigView {
	var cfg AutoSyncConfig
	if err := readJSONFile(a.schedulePath(), &cfg); err != nil {
		cfg = defaultAutoSyncConfig()
	}
	cfg = normalizeAutoSyncConfig(cfg)
	return AutoSyncConfigView{
		AutoSyncConfig: cfg,
		HasPassword:    a.readPanelPassword() != "",
	}
}

// SaveAutoSync 持久化配置与密码（密码为空表示不修改）。
func (a *App) SaveAutoSync(cfg AutoSyncConfig, password string) error {
	cfg = normalizeAutoSyncConfig(cfg)
	if err := writeFileJSON0600(a.schedulePath(), cfg); err != nil {
		return err
	}
	if password != "" {
		if err := writeFileJSON0600(a.panelSecretsPath(), map[string]string{"password": password}); err != nil {
			return err
		}
	}
	if a.auto != nil {
		a.auto.Apply(cfg, password)
	}
	return nil
}

// ClearAutoSync 关闭自动同步并清除面板密码。
//
// 不停止调度循环：循环保持运行（enabled=false 时只是等待），
// 这样用户再次启用时无需重启程序。
func (a *App) ClearAutoSync() error {
	cfg := defaultAutoSyncConfig()
	cfg.Enabled = false
	_ = writeFileJSON0600(a.schedulePath(), cfg)
	if err := os.Remove(a.panelSecretsPath()); err != nil && !os.IsNotExist(err) {
		return err
	}
	if a.auto != nil {
		a.auto.clearPassword()
		a.auto.Apply(cfg, "")
	}
	return nil
}

// clearPassword 清空内存中的面板密码。
func (s *AutoScheduler) clearPassword() {
	s.mu.Lock()
	s.password = ""
	s.mu.Unlock()
}

// GetAutoSyncStatus 返回带密码状态标记的调度器快照。
//
// 调度器未启动时（如 -serve 模式）内存状态为空，这时用磁盘配置补齐
// enabled / interval，避免前端显示 0。
func (a *App) GetAutoSyncStatus() AutoSyncStatus {
	st := AutoSyncStatus{LastStatus: "never"}
	if a.auto != nil {
		st = a.auto.Status()
	}
	cfg := a.GetAutoSyncConfig()
	if !st.Enabled {
		st.Enabled = cfg.Enabled
	}
	if st.DailyTime == "" {
		st.DailyTime = cfg.DailyTime
	}
	st.HasPassword = cfg.HasPassword
	return st
}

func (a *App) readPanelPassword() string {
	var s struct {
		Password string `json:"password"`
	}
	if err := readJSONFile(a.panelSecretsPath(), &s); err != nil {
		return ""
	}
	return s.Password
}

func (a *App) schedulePath() string {
	return filepath.Join(configDir(), "schedule.json")
}

func (a *App) panelSecretsPath() string {
	return filepath.Join(configDir(), "panel_secrets.json")
}

// writeFileJSON0600 与 writeFileJSON 相同，但把权限收紧到仅当前用户。
func writeFileJSON0600(path string, v any) error {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o600)
}
