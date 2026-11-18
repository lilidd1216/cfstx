package task

import (
	"fmt"
	"net"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

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

	// 阶段一：并发解析所有域名。
	//
	// 之前是「解析一个测一个」，78 个域名光解析就要几分钟——
	// DoH 查询失败的域名还要吃满两次超时。并发解析把这一段压到十几秒。
	type parsedEntry struct {
		domain string
		ips    []net.IP
		ok     bool
	}
	parsed := make([]parsedEntry, len(cfg.Domains))
	{
		var (
			pwg sync.WaitGroup
			psm = make(chan struct{}, 12)
		)
		for i, raw := range cfg.Domains {
			domain := strings.TrimSpace(raw)
			parsed[i] = parsedEntry{domain: domain}
			if domain == "" {
				continue
			}
			pwg.Add(1)
			psm <- struct{}{}
			go func(i int, d string) {
				defer pwg.Done()
				defer func() { <-psm }()
				// 用户点「停止」后，排队中的域名不再发起 DoH 查询
				if cancelled() {
					return
				}
				ips := resolveOne(client, d, false)
				if len(ips) > 0 {
					parsed[i].ips = ips
					parsed[i].ok = true
				}
			}(i, domain)
		}
		pwg.Wait()
	}

	// 阶段二：所有域名的所有 IP 共享同一个并发池。
	//
	// 早先的写法是「域名串行、域名内 IP 并发」，实测 78 个域名要 400 秒。
	// 原因是某个域名的 5 个 IP 里有 4 个吃满 8 秒超时，后面的域名只能
	// 干等——而这时候并发槽位其实是空的。现在 390 个 IP 任务统一排队，
	// 槽位一空就补下一个，同样并发上限下耗时降到 1/3 左右。
	type domainAcc struct {
		res     DomainResult
		tlsVals []float64
		pending int32 // 尚未测完的 IP 数
		emitted int32 // 防止重复收尾
		mu      sync.Mutex
	}

	accs := make([]*domainAcc, len(parsed))
	for i, entry := range parsed {
		if entry.domain == "" || !entry.ok {
			continue
		}
		acc := &domainAcc{pending: int32(len(entry.ips))}
		acc.res = DomainResult{Domain: entry.domain, Total: len(entry.ips)}
		acc.res.IPs = make([]string, 0, len(entry.ips))
		for _, ip := range entry.ips {
			acc.res.IPs = append(acc.res.IPs, ip.String())
		}
		accs[i] = acc
	}

	var (
		wg    sync.WaitGroup
		sem   = make(chan struct{}, cfg.Concurrency)
		outMu sync.Mutex
	)

	// finish 汇总单个域名的结果并回调。可重复调用（幂等）。
	finish := func(acc *domainAcc) {
		if !atomic.CompareAndSwapInt32(&acc.emitted, 0, 1) {
			return
		}
		r := &acc.res
		if r.Total > 0 {
			r.SuccessRate = float64(r.Available) / float64(r.Total)
		}
		if len(acc.tlsVals) > 0 {
			sort.Float64s(acc.tlsVals)
			r.BestTLS = acc.tlsVals[0]
			r.WorstTLS = acc.tlsVals[len(acc.tlsVals)-1]
			var sum float64
			for _, v := range acc.tlsVals {
				sum += v
			}
			r.AvgTLS = sum / float64(len(acc.tlsVals))
		}
		r.Rating = rateDomain(r)

		outMu.Lock()
		results = append(results, *r)
		outMu.Unlock()
		if onResult != nil {
			onResult(r)
		}
	}

	for _, acc := range accs {
		if acc == nil {
			continue
		}
		for _, ipStr := range acc.res.IPs {
			if cancelled() {
				break
			}
			ip := net.ParseIP(ipStr)
			if ip == nil {
				atomic.AddInt32(&acc.pending, -1)
				continue
			}
			wg.Add(1)
			sem <- struct{}{}
			go func(acc *domainAcc, ip net.IP) {
				defer wg.Done()
				defer func() { <-sem }()

				target := &Target{IP: &net.IPAddr{IP: ip}, Port: 443}
				_, tlsDur, _, err := measureOnce(target, url, cfg.Host)

				acc.mu.Lock()
				if err == nil && tlsDur > 0 {
					acc.res.Available++
					acc.tlsVals = append(acc.tlsVals, float64(tlsDur.Milliseconds()))
				}
				acc.mu.Unlock()

				if atomic.AddInt32(&acc.pending, -1) == 0 {
					finish(acc)
				}
			}(acc, ip)
		}
	}
	wg.Wait()

	// 收尾：解析失败的域名，以及被「停止」打断、pending 没归零的域名。
	for i, entry := range parsed {
		if entry.domain == "" {
			continue
		}
		acc := accs[i]
		if acc == nil {
			r := DomainResult{
				Domain: entry.domain,
				Error:  "DoH 解析失败或无 A 记录",
				Rating: "无IP",
			}
			results = append(results, r)
			if onResult != nil {
				onResult(&r)
			}
			continue
		}
		finish(acc)
	}

	results = SortDomainResults(results)
	return results
}

// SortDomainResults 按「评级 → 成功率 → 平均 TLS」排名，返回副本。
//
// 体检过程中结果是按完成顺序流式追加的（这样进度看起来自然），
// 但用户真正需要的是「谁最好」的排名，所以跑完后统一排一次。
// 返回副本而不是就地排序：调用方可能还在等其他 goroutine 读原切片。
func SortDomainResults(rs []DomainResult) []DomainResult {
	out := append([]DomainResult(nil), rs...)
	rank := map[string]int{
		"推荐": 0, "可用": 1, "一般": 2, "不推荐": 3, "无IP": 4,
	}
	sort.SliceStable(out, func(i, j int) bool {
		ri, rj := rank[out[i].Rating], rank[out[j].Rating]
		if ri != rj {
			return ri < rj
		}
		if out[i].SuccessRate != out[j].SuccessRate {
			return out[i].SuccessRate > out[j].SuccessRate
		}
		return out[i].AvgTLS < out[j].AvgTLS
	})
	return out
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

// BestCFDomainListURL 是 BestCF 维护的混合列表（IP:端口 / 域名:端口 + 备注）。
const BestCFDomainListURL = "https://bestcf.pages.dev/domain/all.txt"

// FetchDomainList 抓取远程列表并只保留域名条目。
//
// all.txt 是混合格式，里面既有 162.159.198.1:8443 这种 IP，
// 也有 store.ubi.com:443 这种域名。只有域名能用于 CNAME，
// 所以这里把 IP 条目过滤掉。
func FetchDomainList(rawURL string) ([]string, error) {
	if strings.TrimSpace(rawURL) == "" {
		rawURL = BestCFDomainListURL
	}

	client := newFetchClient()
	defer client.CloseIdleConnections()

	body, err := client.get(rawURL)
	if err != nil {
		return nil, err
	}

	seen := make(map[string]struct{})
	out := make([]string, 0, 64)
	for _, line := range strings.Split(body, "\n") {
		host, _, ok := parseSourceLine(line, 443)
		if !ok {
			continue
		}
		// 只要域名：IP 条目无法用于 CNAME
		if net.ParseIP(host) != nil || !isDomainName(host) {
			continue
		}
		if _, dup := seen[host]; dup {
			continue
		}
		seen[host] = struct{}{}
		out = append(out, host)
	}

	if len(out) == 0 {
		return nil, fmt.Errorf("列表里没有可用的域名条目")
	}
	return out, nil
}

// domainCheckTimeout 是单个 IP 的测试超时（秒），供上层展示用。
const domainCheckTimeout = 8

// DomainCheckTimeoutSec 对外暴露单次测试超时，方便前端展示预计耗时。
func DomainCheckTimeoutSec() int { return domainCheckTimeout }

// 确保 time 包被使用（measureOnce 内部依赖），避免误删导入。
var _ = time.Second
