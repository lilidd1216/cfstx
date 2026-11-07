package main

import (
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"gitee.com/liliaic/cfstx/task"
	"golang.org/x/sys/windows"
)

const (
	// 区块标记：只维护自己这一段，用户原有的 hosts 内容一行不动
	hostsBeginMarker = "# === CFSTX BEGIN ==="
	hostsEndMarker   = "# === CFSTX END ==="
)

// HostsEntry 是 hosts 文件中的一条域名映射。
type HostsEntry struct {
	Domain string `json:"domain"`
	IP     string `json:"ip"`
}

// HostsStatus 描述当前 hosts 的可操作状态。
type HostsStatus struct {
	IsAdmin   bool         `json:"isAdmin"`   // 是否具备写入权限
	HostsPath string       `json:"hostsPath"` // hosts 文件路径
	Entries   []HostsEntry `json:"entries"`   // CFSTX 区块内现有条目
	BackupDir string       `json:"backupDir"` // 备份目录
	// ExePath 是当前程序路径。非管理员时界面会显示它，
	// 方便用户关掉后去这个位置右键「以管理员身份运行」。
	ExePath string `json:"exePath"`
}

// hostsPath 返回系统 hosts 文件路径。
func hostsPath() string {
	root := os.Getenv("SystemRoot")
	if root == "" {
		root = `C:\Windows`
	}
	return filepath.Join(root, "System32", "drivers", "etc", "hosts")
}

// isElevated 判断当前进程是否以管理员身份运行。
func isElevated() bool {
	var sid *windows.SID
	err := windows.AllocateAndInitializeSid(
		&windows.SECURITY_NT_AUTHORITY, 2,
		windows.SECURITY_BUILTIN_DOMAIN_RID,
		windows.DOMAIN_ALIAS_RID_ADMINS,
		0, 0, 0, 0, 0, 0, &sid,
	)
	if err != nil {
		return false
	}
	defer windows.FreeSid(sid)

	member, err := windows.GetCurrentProcessToken().IsMember(sid)
	return err == nil && member
}

// GetHostsStatus 返回 hosts 当前状态，供界面展示。
func (a *App) GetHostsStatus() HostsStatus {
	p := hostsPath()
	entries, err := readHostsEntries(p)
	if err != nil || entries == nil {
		// 读不到 hosts 时也要给出空数组，不能让前端拿到 null
		entries = make([]HostsEntry, 0)
	}
	exe, _ := os.Executable()
	return HostsStatus{
		IsAdmin:   isElevated(),
		HostsPath: p,
		Entries:   entries,
		BackupDir: filepath.Join(configDir(), "hosts-backup"),
		ExePath:   exe,
	}
}

// validateHostsInput 规范化并校验写入 hosts 的域名与 IP。
//
// 用户多半是直接从浏览器地址栏复制过来的（带 https:// 和末尾斜杠），
// 先归一化成纯 hostname。否则会卡在格式校验上，报「域名格式不正确」，
// 用户还不知道该怎么改。
func validateHostsInput(domain, ip string) (string, string, error) {
	domain = task.NormalizeProbeDomain(domain)
	ip = strings.TrimSpace(ip)

	// 用户可能直接粘了 TXT 导出里的格式：1.2.3.4:10001#HKG 39.84MB/s。
	// 先把 #备注 去掉，否则端口会解析成 "10001#HKG"。
	if i := strings.IndexAny(ip, "# "); i >= 0 {
		ip = strings.TrimSpace(ip[:i])
	}

	// hosts 只做域名解析，不能指定端口：浏览器访问 https 固定走 443。
	// 粘了 443 的地址可以照用，其它端口必须明确拒绝 ——
	// 静默去掉端口会写出一条「看起来成功、实际连不上」的记录。
	if h, port, err := net.SplitHostPort(ip); err == nil {
		if port != "443" {
			return "", "", fmt.Errorf(
				"%s 端口不能用：hosts 只做域名解析，浏览器访问 https 固定走 443，写进去会连不上", port)
		}
		ip = h
	}
	ip = strings.Trim(ip, "[]")

	if domain == "" {
		return "", "", fmt.Errorf("请填写域名")
	}
	if ip == "" {
		return "", "", fmt.Errorf("请选择一个 IP")
	}
	if strings.ContainsAny(domain, " \t\r\n/:?#") || strings.Contains(domain, "..") {
		return "", "", fmt.Errorf("域名格式不正确：%s", domain)
	}
	if net.ParseIP(ip) == nil {
		return "", "", fmt.Errorf("IP 格式不正确：%s", ip)
	}
	return domain, ip, nil
}

// ApplyHosts 把 domain → ip 写入 hosts 的 CFSTX 区块，覆盖旧条目。
// 返回本次备份文件的路径。
func (a *App) ApplyHosts(domain, ip string) (string, error) {
	domain, ip, err := validateHostsInput(domain, ip)
	if err != nil {
		return "", err
	}
	if !isElevated() {
		return "", fmt.Errorf("修改 hosts 需要管理员权限，请关闭后以管理员身份重新启动 CFSTX")
	}

	p := hostsPath()
	original, err := os.ReadFile(p)
	if err != nil {
		return "", fmt.Errorf("读取 hosts 失败：%w", err)
	}

	backup, err := backupHosts(original)
	if err != nil {
		return "", fmt.Errorf("备份 hosts 失败：%w", err)
	}

	body := stripCFSTXBlock(string(original))
	var b strings.Builder
	b.WriteString(strings.TrimRight(body, "\r\n"))
	b.WriteString("\r\n\r\n")
	b.WriteString(hostsBeginMarker + "\r\n")
	b.WriteString(ip + " " + domain + "\r\n")
	b.WriteString(hostsEndMarker + "\r\n")

	if err := os.WriteFile(p, []byte(b.String()), 0o644); err != nil {
		return "", fmt.Errorf("写入 hosts 失败：%w", err)
	}

	// 写入成功后立即清 DNS 缓存。
	// hosts 的优先级高于 DNS，但 Windows 的 DNS Client 服务仍会缓存
	// 之前的解析结果，不清理的话浏览器可能还连着旧 IP，用户会以为没生效。
	// 这一步需要管理员权限，而上面已经检查过了。
	_ = flushDNS()

	return backup, nil
}

// flushDNS 清理 Windows 的 DNS 客户端缓存。
func flushDNS() error {
	cmd := exec.Command("ipconfig", "/flushdns")
	// 不弹黑框
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%v: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// RemoveHosts 删除 hosts 中的 CFSTX 区块，其余内容保持不变。
func (a *App) RemoveHosts() error {
	if !isElevated() {
		return fmt.Errorf("修改 hosts 需要管理员权限，请关闭后以管理员身份重新启动 CFSTX")
	}

	p := hostsPath()
	original, err := os.ReadFile(p)
	if err != nil {
		return fmt.Errorf("读取 hosts 失败：%w", err)
	}

	body := stripCFSTXBlock(string(original))
	if body == strings.TrimRight(string(original), "\r\n") {
		return nil // 本来就没有 CFSTX 区块
	}

	if _, err := backupHosts(original); err != nil {
		return fmt.Errorf("备份 hosts 失败：%w", err)
	}
	if err := os.WriteFile(p, []byte(strings.TrimRight(body, "\r\n")+"\r\n"), 0o644); err != nil {
		return fmt.Errorf("写入 hosts 失败：%w", err)
	}

	// 删除后同样清缓存，否则域名还会停留在刚删掉的那条记录上
	_ = flushDNS()
	return nil
}

// readHostsEntries 解析 hosts 中 CFSTX 区块内的映射。
func readHostsEntries(path string) ([]HostsEntry, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	// 必须初始化成空切片：nil 切片会被 encoding/json 序列化成 null，
	// 前端读 entries.length 会直接抛 TypeError，把整个 Hosts 卡片渲染挂掉。
	out := make([]HostsEntry, 0)
	inside := false
	for _, line := range strings.Split(string(data), "\n") {
		t := strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(t, hostsBeginMarker):
			inside = true
			continue
		case strings.HasPrefix(t, hostsEndMarker):
			inside = false
			continue
		}
		if !inside || t == "" || strings.HasPrefix(t, "#") {
			continue
		}
		fields := strings.Fields(t)
		if len(fields) >= 2 {
			out = append(out, HostsEntry{IP: fields[0], Domain: fields[1]})
		}
	}
	return out, nil
}

// stripCFSTXBlock 删除 CFSTX 区块（含标记行），保留其余内容。
func stripCFSTXBlock(text string) string {
	lines := strings.Split(text, "\n")
	out := make([]string, 0, len(lines))
	inside := false
	for _, line := range lines {
		t := strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(t, hostsBeginMarker):
			inside = true
			continue
		case strings.HasPrefix(t, hostsEndMarker):
			inside = false
			continue
		}
		if inside {
			continue
		}
		out = append(out, line)
	}
	for len(out) > 0 && strings.TrimSpace(out[len(out)-1]) == "" {
		out = out[:len(out)-1]
	}
	return strings.Join(out, "\n")
}

// backupHosts 把原始 hosts 内容备份到配置目录，返回备份路径。
func backupHosts(data []byte) (string, error) {
	dir := filepath.Join(configDir(), "hosts-backup")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	name := "hosts-" + time.Now().Format("20060102-150405") + ".bak"
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, data, 0o644); err != nil {
		return "", err
	}
	return p, nil
}
