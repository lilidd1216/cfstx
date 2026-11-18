package task

import (
	"sort"
	"strings"
	"sync"
	"time"

	"net"

	"gitee.com/liliaic/cfstx/utils"
)

// 优选域名体检的默认参数。
const (
	defaultDomainConcurrency = 20
	maxDomainConcurrency     = 50
)

// DomainCheckConfig 描述一次「优选域名体检」。
//
// 为什么需要单独的 Host：CF 边缘是按 Host 决定后端路由的。
// 用 speed.cloudflare.com 做 Host 测出来的延迟，和用你自己的
// Worker 域名测出来的，可以差好几倍——前者不执行 Worker，后者要。
// 所以这里必须让用户填真实的入口域名。
type DomainCheckConfig struct {
	Host        string   `json:"host"`        // 测试用的 Host/SNI，通常是用户自己的入口域名
	Domains     []string `json:"domains"`     // 待测的优选域名
	Concurrency int      `json:"concurrency"` // 并发数
}

func (c *DomainCheckConfig) normalize() {
	if c.Concurrency <= 0 {
		c.Concurrency = defaultDomainConcurrency
	}
	if c.Concurrency > maxDomainConcurrency {
		c.Concurrency = maxDomainConcurrency
	}
}

// DomainResult 是单个优选域名的体检结果。
// 所有时延单位均为毫秒。
type DomainResult struct {
	Domain      string   `json:"domain"`
	IPs         []string `json:"ips"`
	Total       int      `json:"total"`       // 解析出的 IP 总数
	Available   int      `json:"available"`   // 能正常完成 TLS 握手并响应的数量
	SuccessRate float64  `json:"successRate"` // 0~1
	BestTLS     float64  `json:"bestTls"`
	AvgTLS      float64  `json:"avgTls"`
	WorstTLS    float64  `json:"worstTls"`
	Rating      string   `json:"rating"` // 推荐 / 可用 / 一般 / 不推荐 / 无IP
	Error       string   `json:"error"`
}

// rateDomain 把体检结果折算成可读的评级。
//
// 权重和稳定性测试保持一致：先看成功率，再看 TLS 耗时。
// 一个域名即使有一个很快的 IP，只要大半是死的，也不该推荐——
// 因为 DNS 轮询是随机的，你没法控制连到哪个。
func rateDomain(r *DomainResult) string {
	if r.Total == 0 {
		return "无IP"
	}
	if r.SuccessRate < 0.5 {
		return "不推荐"
	}
	if r.SuccessRate < 0.8 {
		return "一般"
	}
	if r.AvgTLS > 1500 {
		return "一般"
	}
	if r.AvgTLS > 800 {
		return "可用"
	}
	return "推荐"
}

// CheckPreferredDomains 解析并测试一组优选域名。
//
// 每个域名：先用 DoH 解析出全部 A 记录，再逐个 IP 用 cfg.Host 做
// SNI/Host 发 HEAD 请求，统计可用率与 TLS 耗时。
func CheckPreferredDomains(cfg DomainCheckConfig, onResult func(*DomainResult)) []DomainResult {
	cfg.normalize()

	results := make([]DomainResult, 0, len(cfg.Domains))
	if len(cfg.Domains) == 0 || cfg.Host == "" {
		return results
	}

	client := newFetchClient()
	defer client.CloseIdleConnections()

	url := "https://" + cfg.Host + "/"

	utils.Cyan.Printf("开始优选域名体检（Host：%s，域名数：%d）\n", cfg.Host, len(cfg.Domains))

	for _, raw := range cfg.Domains {
		if cancelled() {
			break
		}
		domain := strings.TrimSpace(raw)
		if domain == "" {
			continue
		}

		r := DomainResult{Domain: domain}

		ips := resolveOne(client, domain, false)
		if len(ips) == 0 {
			r.Error = "DoH 解析失败或无 A 记录"
			r.Rating = "无IP"
			results = append(results, r)
			if onResult != nil {
				onResult(&r)
			}
			continue
		}

		r.IPs = make([]string, 0, len(ips))
		for _, ip := range ips {
			r.IPs = append(r.IPs, ip.String())
		}
		r.Total = len(ips)

		var (
			tlsVals []float64
			sem     = make(chan struct{}, cfg.Concurrency)
			wg      sync.WaitGroup
			mu      sync.Mutex
		)

		for _, ip := range ips {
			if cancelled() {
				break
			}
			wg.Add(1)
			sem <- struct{}{}
			go func(ip net.IP) {
				defer wg.Done()
				defer func() { <-sem }()

				target := &Target{IP: &net.IPAddr{IP: ip}, Port: 443}
				_, tlsDur, _, err := measureOnce(target, url, cfg.Host)

				mu.Lock()
				defer mu.Unlock()
				if err == nil && tlsDur > 0 {
					r.Available++
					tlsVals = append(tlsVals, float64(tlsDur.Milliseconds()))
				}
			}(ip)
		}
		wg.Wait()

		if r.Total > 0 {
			r.SuccessRate = float64(r.Available) / float64(r.Total)
		}
		if len(tlsVals) > 0 {
			sort.Float64s(tlsVals)
			r.BestTLS = tlsVals[0]
			r.WorstTLS = tlsVals[len(tlsVals)-1]
			var sum float64
			for _, v := range tlsVals {
				sum += v
			}
			r.AvgTLS = sum / float64(len(tlsVals))
		}
		r.Rating = rateDomain(&r)

		results = append(results, r)
		if onResult != nil {
			onResult(&r)
		}
	}

	sortDomainResults(results)
	return results
}

// sortDomainResults 按「评级 → 成功率 → 平均 TLS」排序。
func sortDomainResults(rs []DomainResult) {
	rank := map[string]int{
		"推荐": 0, "可用": 1, "一般": 2, "不推荐": 3, "无IP": 4,
	}
	sort.SliceStable(rs, func(i, j int) bool {
		ri, rj := rank[rs[i].Rating], rank[rs[j].Rating]
		if ri != rj {
			return ri < rj
		}
		if rs[i].SuccessRate != rs[j].SuccessRate {
			return rs[i].SuccessRate > rs[j].SuccessRate
		}
		return rs[i].AvgTLS < rs[j].AvgTLS
	})
}

// DefaultPreferredDomains 是内置的优选域名列表。
//
// 来源：https://bestcf.pages.dev/domain/all.txt 里的「大佬维护」与
// 「按运营商」两部分。IP:端口 形式的条目被排除（它们不能用于 CNAME）。
//
// 这些域名会随时间失效或被限速，所以只作为起点，用户可以自行增删。
var DefaultPreferredDomains = []string{
	// 按运营商（对国内线路做过针对性优化）
	"ct.877774.xyz",   // 电信 QMS
	"cu.877774.xyz",   // 联通 QMS
	"cmcc.877774.xyz", // 移动 QMS
	// 社区维护
	"saas.sin.fan",          // MIYU
	"skk.moe",               // Sukka
	"cf.090227.xyz",         // CMLiu
	"cf.877774.xyz",         // QMS 通用
	"bestcf.030101.xyz",     // MingYu
	"cloudflare.182682.xyz", // WeTest
	"cf.3666888.xyz",        // HuTao
	"cf.tencentapp.cn",      // KTFF
	"yg1.ygkkk.dpdns.org",   // YGKKK
}

// domainCheckTimeout 是单个 IP 的测试超时（秒），供上层展示用。
const domainCheckTimeout = 8

// DomainCheckTimeoutSec 对外暴露单次测试超时，方便前端展示预计耗时。
func DomainCheckTimeoutSec() int { return domainCheckTimeout }

// 确保 time 包被使用（measureOnce 内部依赖），避免误删导入。
var _ = time.Second
