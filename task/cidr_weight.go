package task

// Cloudflare IPv4 网段的可用率加权采样。
//
// 背景：Cloudflare 的 IPv4 空间里大部分是区域/租户专用分配，对普通站点
// 没有配置。等权展开官方 15 个网段时，一半以上的候选会来自实测 0% 可用的段，
// 表现为「L1 过后只剩零星候选、端到端全军覆没」。
//
// 权重来自 CFPilot 的真机实测（每段 12 个 IP，WS 101 校验）以及本项目的
// 常识性复测。它**会随 Cloudflare 的分区调整过期**，所以只用于「在线 CIDR
// 来源」的预算分配；用户自己给的 ip.txt / 手动 IP 不受影响。
//
// 权重 0 的段仍保留 1 个探针（不彻底放弃），避免一次误判把可用段永久排除。

import (
	"encoding/binary"
	"math/rand"
	"net"
)

// ipv4CidrWeights：CIDR → 可用率权重（0~1）。最长前缀匹配。
var ipv4CidrWeights = map[string]float64{
	"104.16.0.0/13":    0.90, // 104.16–104.23，主力 Anycast 段
	"104.24.0.0/14":    0.95, // 104.24–104.27，实测最好
	"188.114.96.0/20":  0.75,
	"172.64.0.0/13":    0.55,
	"103.21.244.0/22":  0.35,
	"103.31.4.0/22":    0.30,
	"190.93.240.0/20":  0.25,
	"162.158.0.0/16":   0.30,
	"162.159.0.0/16":   0.05, // 多为管理/API 段，403 居多，但偶有可用
	"141.101.64.0/18":  0.05,
	"173.245.48.0/20":  0.05,
	"108.162.192.0/18": 0.02,
	// 实测 0/12 且 TCP 层不可达，保留 1 个探针
	"103.22.200.0/22":  0,
	"197.234.240.0/22": 0,
	"198.41.128.0/17":  0,
	"131.0.72.0/22":    0,
}

const (
	defaultCidrWeight = 0.2
	// 在线 CIDR 展开的总采样预算，避免一次生成上万个目标。
	cidrTargetBudget = 4096
)

// CIDRWeight 返回一个 CIDR 的可用率权重。
func CIDRWeight(cidr string) float64 {
	_, target, err := net.ParseCIDR(cidr)
	if err != nil || target.IP.To4() == nil {
		return defaultCidrWeight
	}
	bestPrefix := -1
	best := defaultCidrWeight
	for c, w := range ipv4CidrWeights {
		_, n, err := net.ParseCIDR(c)
		if err != nil || !n.Contains(target.IP) {
			continue
		}
		ones, _ := n.Mask.Size()
		if ones > bestPrefix {
			bestPrefix = ones
			best = w
		}
	}
	return best
}

// expandCIDRsWeighted 按「网段大小 × 可用率权重」分配采样预算。
// IPv6 不做加权（没有可靠的实测表），退回原有展开逻辑。
func expandCIDRsWeighted(cidrs []string, port int) []*Target {
	type item struct {
		cidr   string
		size24 int
		cap    int
	}
	var (
		items []item
		v6    []string
		seen  = make(map[string]struct{}, len(cidrs))
	)
	for _, cidr := range cidrs {
		cidr = stringsTrimSpace(cidr)
		if cidr == "" {
			continue
		}
		if _, dup := seen[cidr]; dup {
			continue
		}
		seen[cidr] = struct{}{}
		_, n, err := net.ParseCIDR(cidr)
		if err != nil {
			continue
		}
		if n.IP.To4() == nil {
			v6 = append(v6, cidr)
			continue
		}
		ones, _ := n.Mask.Size()
		size24 := 1
		if ones < 24 {
			size24 = 1 << uint(24-ones)
		}
		w := CIDRWeight(cidr)
		alloc := int(float64(size24) * w)
		if alloc < 1 {
			alloc = 1 // 权重 0 也留一个探针
		}
		if alloc > size24 {
			alloc = size24
		}
		items = append(items, item{cidr: cidr, size24: size24, cap: alloc})
	}

	total := 0
	for _, it := range items {
		total += it.cap
	}
	out := make([]*Target, 0, minInt(total, cidrTargetBudget)+len(v6))
	for _, it := range items {
		alloc := it.cap
		if total > cidrTargetBudget {
			alloc = it.cap * cidrTargetBudget / total
			if alloc < 1 {
				alloc = 1
			}
		}
		out = append(out, sampleCIDRTargets(it.cidr, alloc, port)...)
	}

	// IPv6：保留原有展开（数量通常很小，且没有可靠权重表）
	if len(v6) > 0 {
		r := newIPRanges()
		r.port = port
		for _, c := range v6 {
			r.addLine(c, port)
		}
		out = append(out, r.ips...)
	}
	return out
}

// sampleCIDRTargets 在 CIDR 内随机取 sample 个地址。
// 大于 /24 时按 /24 分桶、每桶随机取一个（避免固定步长导致末位全部相同）。
func sampleCIDRTargets(cidr string, sample, port int) []*Target {
	_, n, err := net.ParseCIDR(cidr)
	if err != nil {
		return nil
	}
	ip4 := n.IP.To4()
	if ip4 == nil || sample <= 0 {
		return nil
	}
	ones, bits := n.Mask.Size()
	if bits != 32 {
		return nil
	}
	base := binary.BigEndian.Uint32(ip4)
	out := make([]*Target, 0, sample)

	if ones >= 24 {
		// /24 或更细：地址数很少，直接随机取
		size := 1 << uint(32-ones)
		if sample > size {
			sample = size
		}
		for i := 0; i < sample; i++ {
			offset := uint32(rand.Intn(size))
			ip := make(net.IP, 4)
			binary.BigEndian.PutUint32(ip, base+offset)
			out = append(out, &Target{IP: &net.IPAddr{IP: ip}, Port: port})
		}
		return out
	}

	count24 := 1 << uint(24-ones)
	if sample > count24 {
		sample = count24
	}
	// 随机选不重复的 /24，再在桶内随机取末位（避开 .0/.255）
	for _, idx := range rand.Perm(count24)[:sample] {
		prefix := base + uint32(idx)<<8
		last := uint32(rand.Intn(254) + 1)
		ip := make(net.IP, 4)
		binary.BigEndian.PutUint32(ip, prefix+last)
		out = append(out, &Target{IP: &net.IPAddr{IP: ip}, Port: port})
	}
	return out
}

func stringsTrimSpace(s string) string {
	start, end := 0, len(s)
	for start < end && (s[start] == ' ' || s[start] == '\t' || s[start] == '\r' || s[start] == '\n') {
		start++
	}
	for end > start && (s[end-1] == ' ' || s[end-1] == '\t' || s[end-1] == '\r' || s[end-1] == '\n') {
		end--
	}
	return s[start:end]
}
