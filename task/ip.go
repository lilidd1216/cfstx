package task

import (
	"bufio"
	"log"
	"math/rand"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const defaultInputFile = "ip.txt"

var (
	// TestAll test all ip
	TestAll = false
	// IPFile is the filename of IP Rangs
	IPFile = defaultInputFile
	IPText string

	// SourceIPv4 是否加载内置 ip.txt
	SourceIPv4 = true
	// SourceIPv6 是否加载内置 ipv6.txt
	SourceIPv6 = false
	// ExtraTargets 由上层注入的额外测速目标（在线优选 IP 等）
	ExtraTargets []*Target
	// OverrideTargets 不为 nil 时，LoadTargets 直接返回它，
	// 用于「先加载全部目标 → 域名探测筛选 → 再测速」的流程。
	OverrideTargets []*Target
)

// Target 表示一个待测速目标。每条目标自带端口，
// 因此同一个 IP 的不同端口列表可以共存，也能与内置 IP 段混用。
type Target struct {
	IP   *net.IPAddr
	Port int
}

// Addr 返回 host:port 形式的拨号地址，IPv6 会自动加上方括号。
func (t *Target) Addr() string {
	if t == nil || t.IP == nil {
		return ""
	}
	return net.JoinHostPort(t.IP.String(), strconv.Itoa(t.Port))
}

func InitRandSeed() {
	rand.Seed(time.Now().UnixNano())
}

func isIPv4(ip string) bool {
	return strings.Contains(ip, ".")
}

func randIPEndWith(num byte) byte {
	if num == 0 { // 对于 /32 这种单独的 IP
		return byte(0)
	}
	return byte(rand.Intn(int(num)))
}

// SplitHostPort 从一行文本中分离出 IP/CIDR 与端口。
// 支持的写法：
//
//	1.2.3.4          1.2.3.4:8443          1.2.3.4:8443#备注
//	1.2.3.4/24       2606:4700::1          [2606:4700::1]:8443#备注
//
// 没有显式端口时返回 defaultPort。
func SplitHostPort(line string, defaultPort int) (host string, port int) {
	port = defaultPort

	// # 之后是备注，直接丢弃（在线列表常见 113.1.1.1:443#香港 HK）
	if i := strings.IndexByte(line, '#'); i >= 0 {
		line = line[:i]
	}
	line = strings.TrimSpace(line)
	if line == "" {
		return "", port
	}

	// IPv6 带方括号：[addr]:port
	if strings.HasPrefix(line, "[") {
		end := strings.IndexByte(line, ']')
		if end < 0 {
			return "", port
		}
		host = line[1:end]
		if rest := strings.TrimSpace(line[end+1:]); strings.HasPrefix(rest, ":") {
			if p, err := strconv.Atoi(rest[1:]); err == nil && p > 0 && p <= 65535 {
				port = p
			}
		}
		return host, port
	}

	// 原生 IPv6（多个冒号且不是 CIDR 前缀）不含端口
	if strings.Count(line, ":") > 1 && !strings.Contains(line, "/") {
		return line, port
	}

	// IPv4 或 IPv4:port
	if i := strings.LastIndexByte(line, ':'); i > 0 {
		if p, err := strconv.Atoi(line[i+1:]); err == nil && p > 0 && p <= 65535 {
			return line[:i], p
		}
	}
	return line, port
}

type IPRanges struct {
	ips     []*Target
	mask    string
	firstIP net.IP
	ipNet   *net.IPNet
	port    int
}

func newIPRanges() *IPRanges {
	port := TCPPort
	if port <= 0 || port > 65535 {
		port = defaultPort
	}
	return &IPRanges{
		ips:  make([]*Target, 0),
		port: port,
	}
}

// 如果是单独 IP 则加上子网掩码，反之则获取子网掩码(r.mask)
func (r *IPRanges) fixIP(ip string) string {
	// 如果不含有 '/' 则代表不是 IP 段，而是一个单独的 IP，因此需要加上 /32 /128 子网掩码
	if i := strings.IndexByte(ip, '/'); i < 0 {
		if isIPv4(ip) {
			r.mask = "/32"
		} else {
			r.mask = "/128"
		}
		ip += r.mask
	} else {
		r.mask = ip[i:]
	}
	return ip
}

// 解析 IP 段，获得 IP、IP 范围、子网掩码。
// 解析失败只返回 false，不再终止进程——在线列表里可能混入脏数据。
func (r *IPRanges) parseCIDR(ip string) bool {
	var err error
	if r.firstIP, r.ipNet, err = net.ParseCIDR(r.fixIP(ip)); err != nil {
		return false
	}
	return true
}

func (r *IPRanges) appendIPv4(d byte) {
	r.appendIP(net.IPv4(r.firstIP[12], r.firstIP[13], r.firstIP[14], d))
}

func (r *IPRanges) appendIP(ip net.IP) {
	port := r.port
	if port <= 0 || port > 65535 {
		port = defaultPort
	}
	r.ips = append(r.ips, &Target{IP: &net.IPAddr{IP: ip}, Port: port})
}

// 返回第四段 ip 的最小值及可用数目
func (r *IPRanges) getIPRange() (minIP, hosts byte) {
	minIP = r.firstIP[15] & r.ipNet.Mask[3] // IP 第四段最小值

	// 根据子网掩码获取主机数量
	m := net.IPv4Mask(255, 255, 255, 255)
	for i, v := range r.ipNet.Mask {
		m[i] ^= v
	}
	total, _ := strconv.ParseInt(m.String(), 16, 32) // 总可用 IP 数
	if total > 255 {                                 // 矫正 第四段 可用 IP 数
		hosts = 255
		return
	}
	hosts = byte(total)
	return
}

func (r *IPRanges) chooseIPv4() {
	if r.mask == "/32" { // 单个 IP 则无需随机，直接加入自身即可
		r.appendIP(r.firstIP)
	} else {
		minIP, hosts := r.getIPRange()    // 返回第四段 IP 的最小值及可用数目
		for r.ipNet.Contains(r.firstIP) { // 只要该 IP 没有超出 IP 网段范围，就继续循环随机
			if TestAll { // 如果是测速全部 IP
				for i := 0; i <= int(hosts); i++ { // 遍历 IP 最后一段最小值到最大值
					r.appendIPv4(byte(i) + minIP)
				}
			} else { // 随机 IP 的最后一段 0.0.0.X
				r.appendIPv4(minIP + randIPEndWith(hosts))
			}
			r.firstIP[14]++ // 0.0.(X+1).X
			if r.firstIP[14] == 0 {
				r.firstIP[13]++ // 0.(X+1).X.X
				if r.firstIP[13] == 0 {
					r.firstIP[12]++ // (X+1).X.X.X
				}
			}
		}
	}
}

func (r *IPRanges) chooseIPv6() {
	if r.mask == "/128" { // 单个 IP 则无需随机，直接加入自身即可
		r.appendIP(r.firstIP)
	} else {
		var tempIP uint8                  // 临时变量，用于记录前一位的值
		for r.ipNet.Contains(r.firstIP) { // 只要该 IP 没有超出 IP 网段范围，就继续循环随机
			r.firstIP[15] = randIPEndWith(255) // 随机 IP 的最后一段
			r.firstIP[14] = randIPEndWith(255) // 随机 IP 的最后一段

			targetIP := make([]byte, len(r.firstIP))
			copy(targetIP, r.firstIP)
			r.appendIP(targetIP) // 加入 IP 地址池

			for i := 13; i >= 0; i-- { // 从倒数第三位开始往前随机
				tempIP = r.firstIP[i]              // 保存前一位的值
				r.firstIP[i] += randIPEndWith(255) // 随机 0~255，加到当前位上
				if r.firstIP[i] >= tempIP {        // 如果当前位的值大于等于前一位的值，说明随机成功了，可以退出该循环
					break
				}
			}
		}
	}
}

// addLine 解析一行 IP 并展开为测速目标。
func (r *IPRanges) addLine(line string, defaultPort int) {
	host, port := SplitHostPort(line, defaultPort)
	if host == "" {
		return
	}
	if port <= 0 || port > 65535 {
		port = defaultPort
	}
	r.port = port
	if !r.parseCIDR(host) {
		return
	}
	if isIPv4(host) {
		r.chooseIPv4()
	} else {
		r.chooseIPv6()
	}
}

// ResolveDataPath 把数据文件名解析成实际路径。
//
// GUI 可能从任意工作目录启动（双击、快捷方式、命令行），而 ip.txt 等
// 数据文件总是和 exe 放在一起，所以这里按「当前目录 → exe 目录」的顺序查找。
func ResolveDataPath(name string) string {
	if name == "" || filepath.IsAbs(name) {
		return name
	}
	if _, err := os.Stat(name); err == nil {
		return name
	}
	exe, err := os.Executable()
	if err != nil {
		return name
	}
	candidate := filepath.Join(filepath.Dir(exe), name)
	if _, err := os.Stat(candidate); err == nil {
		return candidate
	}
	return name
}

// loadFile 读取一个 IP 列表文件。文件缺失只记录日志，不中断测速。
func (r *IPRanges) loadFile(name string) {
	path := ResolveDataPath(name)
	file, err := os.Open(path)
	if err != nil {
		log.Printf("读取 IP 文件 %s 失败：%v", path, err)
		return
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, "//") {
			continue
		}
		r.addLine(line, TCPPort)
	}
}

func (r *IPRanges) appendTarget(t *Target) {
	if t == nil || t.IP == nil {
		return
	}
	port := t.Port
	if port <= 0 || port > 65535 {
		port = TCPPort
	}
	if port <= 0 || port > 65535 {
		port = defaultPort
	}
	r.ips = append(r.ips, &Target{IP: t.IP, Port: port})
}

// dedupeTargets 按 IP 去重，保留先出现的端口（ip.txt/ipv6.txt 在前，
// 因此同一 IP 优先使用内置列表的默认端口）。
func dedupeTargets(targets []*Target) []*Target {
	seen := make(map[string]struct{}, len(targets))
	out := make([]*Target, 0, len(targets))
	for _, t := range targets {
		if t == nil || t.IP == nil {
			continue
		}
		key := t.IP.String()
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, t)
	}
	return out
}

// LoadTargets 汇总所有启用的 IP 来源并去重。
func LoadTargets() []*Target {
	if OverrideTargets != nil {
		return OverrideTargets
	}

	ranges := newIPRanges()

	// 手动指定的 IP 段（只解析、不随机化 CIDR 之外的来源）
	if IPText != "" {
		for _, part := range strings.Split(IPText, ",") {
			part = strings.TrimSpace(part)
			if part == "" {
				continue
			}
			ranges.addLine(part, TCPPort)
		}
	}

	// 内置文件
	if SourceIPv4 {
		ranges.loadFile(defaultInputFile)
	}
	if SourceIPv6 {
		ranges.loadFile("ipv6.txt")
	}

	// 上层注入（在线优选 IP）
	for _, t := range ExtraTargets {
		ranges.appendTarget(t)
	}

	return dedupeTargets(ranges.ips)
}

// loadIPRanges 保留给内部调用。
func loadIPRanges() []*Target {
	return LoadTargets()
}
