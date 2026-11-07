package main

import (
	"fmt"
	"time"

	"gitee.com/liliaic/cfstx/task"
)

// maxProxyIPCandidates 是单次检测的候选上限。
// 每条都要做 TCP + TLS + HTTP 三次往返，几千条会跑很久，
// 也没必要 —— 筛出几十个就足够用了。
const maxProxyIPCandidates = 2000

// ProxyIPCheckRequest 是一次 ProxyIP 检测的入参。
type ProxyIPCheckRequest struct {
	Text        string `json:"text"`        // 粘贴的候选列表，一行一个
	Domain      string `json:"domain"`      // TLS 探测域名，留空用 speed.cloudflare.com
	Concurrency int    `json:"concurrency"` // 并发数，0 用默认值
}

// ProxyIPStatus 是检测状态快照。
type ProxyIPStatus struct {
	Running bool                 `json:"running"`
	Done    int                  `json:"done"`
	Total   int                  `json:"total"`
	Domain  string               `json:"domain"`
	Results []task.ProxyIPResult `json:"results"`
}

// StartProxyIPCheck 启动一次检测。进度通过 proxyip:* 事件推送。
func (a *App) StartProxyIPCheck(req ProxyIPCheckRequest) error {
	cands := task.ParseProxyIPCandidates(req.Text)
	if len(cands) == 0 {
		return fmt.Errorf("没有解析到有效的候选地址，请检查格式（支持 IP、IP:端口、域名）")
	}
	if len(cands) > maxProxyIPCandidates {
		return fmt.Errorf("候选地址太多（%d 个），单次最多 %d 个，请分批检测",
			len(cands), maxProxyIPCandidates)
	}

	domain := task.NormalizeProbeDomain(req.Domain)
	if domain == "" {
		domain = task.DefaultProxyProbeDomain
	}

	a.proxyMu.Lock()
	if a.proxyRunning {
		a.proxyMu.Unlock()
		return fmt.Errorf("ProxyIP 检测正在进行中，请先停止")
	}
	a.proxyRunning = true
	a.proxyDone = 0
	a.proxyTotal = len(cands)
	a.proxyDomain = domain
	a.proxyResults = nil
	a.proxyMu.Unlock()

	task.ResetProxyIPProbe()
	go a.runProxyIPCheck(cands, req.Concurrency, domain)
	return nil
}

// StopProxyIPCheck 请求停止当前检测。
func (a *App) StopProxyIPCheck() {
	task.CancelProxyIPProbe()
}

// ProxyIPStatus 返回当前检测状态。
func (a *App) GetProxyIPStatus() ProxyIPStatus {
	a.proxyMu.Lock()
	defer a.proxyMu.Unlock()

	results := a.proxyResults
	if results == nil {
		results = []task.ProxyIPResult{}
	}
	return ProxyIPStatus{
		Running: a.proxyRunning,
		Done:    a.proxyDone,
		Total:   a.proxyTotal,
		Domain:  a.proxyDomain,
		Results: results,
	}
}

// runProxyIPCheck 是检测的后台执行体。
func (a *App) runProxyIPCheck(cands []task.ProxyIPCandidate, concurrency int, domain string) {
	start := time.Now()
	defer func() {
		a.proxyMu.Lock()
		a.proxyRunning = false
		a.proxyMu.Unlock()
	}()

	a.appendLog("info", "proxyip", fmt.Sprintf(
		"开始 ProxyIP 检测 | 候选=%d 探测域名=%s", len(cands), domain))
	a.emit("proxyip:started", map[string]any{
		"total":  len(cands),
		"domain": domain,
	})

	done := 0
	okCount := 0
	results := task.ProbeProxyIPs(cands, domain, concurrency, func(r task.ProxyIPResult) {
		done++
		if r.OK {
			okCount++
		}
		a.proxyMu.Lock()
		a.proxyDone = done
		a.proxyMu.Unlock()

		a.emit("proxyip:progress", map[string]any{
			"done":    done,
			"total":   len(cands),
			"okCount": okCount,
			"result":  r,
		})
	})

	a.proxyMu.Lock()
	a.proxyResults = results
	a.proxyDone = done
	a.proxyMu.Unlock()

	stopped := done < len(cands)
	a.appendLog("info", "proxyip", fmt.Sprintf(
		"ProxyIP 检测完成 | %d 个候选，%d 个可用%s",
		len(cands), okCount, map[bool]string{true: "（已手动停止）", false: ""}[stopped]))

	a.emit("proxyip:finished", map[string]any{
		"results": results,
		"okCount": okCount,
		"done":    done,
		"total":   len(cands),
		"seconds": time.Since(start).Seconds(),
		"stopped": stopped,
	})
}

// ProxyIPSourceDTO 是给前端的精简数据源描述。
type ProxyIPSourceDTO struct {
	Key         string `json:"key"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Count       int    `json:"count"`
}

// GetProxyIPSources 返回内置数据源列表。
func (a *App) GetProxyIPSources() []ProxyIPSourceDTO {
	out := make([]ProxyIPSourceDTO, 0, len(task.BuiltinProxyIPSources))
	for _, s := range task.BuiltinProxyIPSources {
		out = append(out, ProxyIPSourceDTO{
			Key:         s.Key,
			Name:        s.Name,
			Description: s.Description,
			Count:       s.Count,
		})
	}
	return out
}

// ImportProxyIPSource 抓取一个数据源并把候选列表以文本形式返回给前端，
// 前端填进输入框后用户还能自己删减。
func (a *App) ImportProxyIPSource(key string) (string, int, error) {
	src, ok := task.FindProxyIPSource(key)
	if !ok {
		return "", 0, fmt.Errorf("未知的数据源：%s", key)
	}

	cands, err := task.FetchProxyIPCandidates(src)
	if err != nil {
		return "", 0, err
	}

	text := FormatProxyIPCandidates(cands)
	a.appendLog("info", "proxyip", fmt.Sprintf(
		"已导入数据源「%s」：%d 个候选", src.Name, len(cands)))
	return text, len(cands), nil
}

// FormatProxyIPCandidates 把候选列表还原成一行一个的文本。
// 保留原始备注，用户导入后仍能看到来源信息。
func FormatProxyIPCandidates(cands []task.ProxyIPCandidate) string {
	var b []byte
	for _, c := range cands {
		if c.Note != "" {
			b = append(b, c.Addr()+"#"+c.Note+"\n"...)
		} else {
			b = append(b, c.Addr()+"\n"...)
		}
	}
	return string(b)
}
