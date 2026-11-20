package task

// 历史优质 IP 池：跨运行记住「上次端到端通过」的地址，下次优先测。
//
// 为什么需要：公共 IP 列表衰减快，每次从零开始采样命中率低。
// 池里只存 IP/端口/成功次数/最佳延迟，不存任何凭据。
// 30 天未再被验证的条目自动过期；容量上限 5000。

import (
	"encoding/json"
	"fmt"
	"net"
	"os"
	"sort"
	"time"
)

const (
	poolVersion    = 1
	poolMaxEntries = 5000
	poolExpire     = 30 * 24 * time.Hour
)

// PoolEntry 是一个历史候选的统计。
type PoolEntry struct {
	IP       string  `json:"ip"`
	Port     int     `json:"port"`
	Success  int     `json:"success"`
	Fail     int     `json:"fail"`
	Best     float64 `json:"best"`     // 历史最佳端到端延迟（ms）
	LastSeen int64   `json:"lastSeen"` // Unix 秒
}

// IPPool 是池的持久化结构。
type IPPool struct {
	Version   int         `json:"version"`
	UpdatedAt int64       `json:"updatedAt"`
	Entries   []PoolEntry `json:"entries"`
}

func NewIPPool() *IPPool { return &IPPool{Version: poolVersion} }

// LoadIPPool 读取池；文件不存在/损坏/版本不符时返回 nil。
func LoadIPPool(path string) *IPPool {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var p IPPool
	if err := json.Unmarshal(data, &p); err != nil || p.Version != poolVersion {
		return nil
	}
	return &p
}

// SaveIPPool 原子写入（临时文件 + rename）。
func SaveIPPool(path string, p *IPPool) error {
	if p == nil {
		return nil
	}
	p.UpdatedAt = time.Now().Unix()
	data, err := json.MarshalIndent(p, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func poolKey(ip string, port int) string { return fmt.Sprintf("%s:%d", ip, port) }

// MergeResults 把一次端到端探测的结果并入池。
func (p *IPPool) MergeResults(results []E2EResult) {
	if p == nil {
		return
	}
	now := time.Now().Unix()
	index := make(map[string]int, len(p.Entries))
	for i := range p.Entries {
		index[poolKey(p.Entries[i].IP, p.Entries[i].Port)] = i
	}
	for _, r := range results {
		k := poolKey(r.IP, r.Port)
		if i, ok := index[k]; ok {
			e := &p.Entries[i]
			e.LastSeen = now
			if r.OK {
				e.Success++
				if e.Best <= 0 || (r.Best > 0 && r.Best < e.Best) {
					e.Best = r.Best
				}
			} else {
				e.Fail++
			}
			continue
		}
		e := PoolEntry{IP: r.IP, Port: r.Port, LastSeen: now}
		if r.OK {
			e.Success = 1
			e.Best = r.Best
		} else {
			e.Fail = 1
		}
		p.Entries = append(p.Entries, e)
		index[k] = len(p.Entries) - 1
	}
	p.trim(now)
}

func (p *IPPool) trim(now int64) {
	expire := int64(poolExpire / time.Second)
	kept := p.Entries[:0]
	for _, e := range p.Entries {
		if now-e.LastSeen > expire {
			continue
		}
		kept = append(kept, e)
	}
	p.Entries = kept
	if len(p.Entries) > poolMaxEntries {
		sort.SliceStable(p.Entries, func(i, j int) bool {
			if p.Entries[i].Success != p.Entries[j].Success {
				return p.Entries[i].Success > p.Entries[j].Success
			}
			if p.Entries[i].Best > 0 && p.Entries[j].Best > 0 && p.Entries[i].Best != p.Entries[j].Best {
				return p.Entries[i].Best < p.Entries[j].Best
			}
			return p.Entries[i].LastSeen > p.Entries[j].LastSeen
		})
		p.Entries = p.Entries[:poolMaxEntries]
	}
}

// Targets 返回可以直接注入测速的地址：只取曾经成功过、未过期的，按
// 成功次数降序、最佳延迟升序、最近出现优先。
func (p *IPPool) Targets(limit int) []*Target {
	if p == nil {
		return nil
	}
	now := time.Now().Unix()
	expire := int64(poolExpire / time.Second)
	valid := make([]PoolEntry, 0, len(p.Entries))
	for _, e := range p.Entries {
		if now-e.LastSeen > expire || e.Success <= 0 {
			continue
		}
		valid = append(valid, e)
	}
	sort.SliceStable(valid, func(i, j int) bool {
		if valid[i].Success != valid[j].Success {
			return valid[i].Success > valid[j].Success
		}
		if valid[i].Best > 0 && valid[j].Best > 0 && valid[i].Best != valid[j].Best {
			return valid[i].Best < valid[j].Best
		}
		return valid[i].LastSeen > valid[j].LastSeen
	})
	if limit > 0 && len(valid) > limit {
		valid = valid[:limit]
	}
	out := make([]*Target, 0, len(valid))
	for _, e := range valid {
		ip := net.ParseIP(e.IP)
		if ip == nil || e.Port <= 0 || e.Port > 65535 {
			continue
		}
		out = append(out, &Target{IP: &net.IPAddr{IP: ip}, Port: e.Port})
	}
	return out
}

// Count 返回当前有效的（成功过且未过期）条目数。
func (p *IPPool) Count() int {
	if p == nil {
		return 0
	}
	now := time.Now().Unix()
	expire := int64(poolExpire / time.Second)
	n := 0
	for _, e := range p.Entries {
		if now-e.LastSeen <= expire && e.Success > 0 {
			n++
		}
	}
	return n
}
