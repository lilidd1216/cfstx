package task

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// DefaultPanelUA 是访问 edgetunnel 面板时使用的固定 User-Agent。
//
// 面板的 auth cookie 等于 MD5MD5(UA + KEY + 管理员密码)，与 UA 严格绑定：
// 登录和后续请求必须使用完全相同的 UA，否则 cookie 校验会失败。
const DefaultPanelUA = "CFSTX-AutoSync/1.0"

// ErrPanelAuth 表示面板认证失败（密码错误、cookie 失效）。
var ErrPanelAuth = errors.New("面板认证失败")

// PanelReadiness 描述面板当前是否适合接收自定义优选 IP。
type PanelReadiness struct {
	ConfigFound  bool     `json:"configFound"`
	LocalEnabled bool     `json:"localEnabled"`
	RandomIP     bool     `json:"randomIP"`
	Warnings     []string `json:"warnings"`
}

// PanelClient 是 edgetunnel 管理面板的最小客户端。
type PanelClient struct {
	baseURL string
	ua      string
	hc      *http.Client
	cookie  string
}

// NewPanelClient 创建面板客户端。baseURL 缺省协议时补 https://。
func NewPanelClient(baseURL, ua string) (*PanelClient, error) {
	u := strings.TrimSpace(baseURL)
	if u == "" {
		return nil, fmt.Errorf("面板地址为空")
	}
	if !strings.HasPrefix(u, "http://") && !strings.HasPrefix(u, "https://") {
		u = "https://" + u
	}
	u = strings.TrimRight(u, "/")
	parsed, err := url.Parse(u)
	if err != nil || parsed.Host == "" {
		return nil, fmt.Errorf("面板地址无效：%s", baseURL)
	}
	// 面板的 API（/login、/admin/config.json）都在域名根路径下。
	// 用户从浏览器复制的地址常带 /admin 后缀，这里统一剥掉，只保留 scheme://host。
	u = parsed.Scheme + "://" + parsed.Host
	if ua == "" {
		ua = DefaultPanelUA
	}
	return &PanelClient{
		baseURL: u,
		ua:      ua,
		hc: &http.Client{
			Timeout: 15 * time.Second,
			// 不自动跟随重定向：未登录时 /admin/* 会 302 到 /login，
			// 需要显式识别为认证失败，而不是当成 200。
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
	}, nil
}

// Login 用管理员密码换取 auth cookie。
func (c *PanelClient) Login(ctx context.Context, password string) error {
	form := url.Values{}
	form.Set("password", password)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/login",
		strings.NewReader(form.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", c.ua)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := c.hc.Do(req)
	if err != nil {
		return fmt.Errorf("连接面板失败：%w", err)
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)

	// 密码错误时面板会返回登录页 HTML（状态码 200），因此必须检查 cookie。
	for _, ck := range resp.Cookies() {
		if ck.Name == "auth" && ck.Value != "" {
			c.cookie = ck.Value
			return nil
		}
	}
	if resp.StatusCode == http.StatusFound || resp.StatusCode == http.StatusMovedPermanently ||
		resp.StatusCode == http.StatusTemporaryRedirect || resp.StatusCode == http.StatusSeeOther {
		return fmt.Errorf("登录被重定向（HTTP %d），请确认面板地址只填域名，例如 https://fast2.lds1216.ccwu.cc", resp.StatusCode)
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("面板登录失败（HTTP %d），请检查密码", resp.StatusCode)
	}
	return ErrPanelAuth
}

func (c *PanelClient) do(ctx context.Context, method, path, contentType string, body io.Reader) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", c.ua)
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	if c.cookie != "" {
		req.AddCookie(&http.Cookie{Name: "auth", Value: c.cookie})
	}
	return c.hc.Do(req)
}

// ReadConfig 读取面板完整配置。未认证时返回 ErrPanelAuth。
func (c *PanelClient) ReadConfig(ctx context.Context) (map[string]any, error) {
	resp, err := c.do(ctx, http.MethodGet, "/admin/config.json", "", nil)
	if err != nil {
		return nil, fmt.Errorf("连接面板失败：%w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusFound || resp.StatusCode == http.StatusMovedPermanently ||
		resp.StatusCode == http.StatusTemporaryRedirect || resp.StatusCode == http.StatusSeeOther {
		return nil, ErrPanelAuth
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("读取面板配置失败（HTTP %d）", resp.StatusCode)
	}
	var cfg map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&cfg); err != nil {
		return nil, fmt.Errorf("解析面板配置失败：%w", err)
	}
	return cfg, nil
}

// Preflight 登录并检查面板的优选订阅开关。
//
// 只读取配置、绝不修改；「随机IP」开启时只给出警告，由用户手动关闭。
func (c *PanelClient) Preflight(ctx context.Context, password string) (PanelReadiness, error) {
	var r PanelReadiness
	if err := c.Login(ctx, password); err != nil {
		return r, err
	}
	cfg, err := c.ReadConfig(ctx)
	if err != nil {
		return r, err
	}
	r.ConfigFound = true

	local, hasLocal := boolAt(cfg, "优选订阅生成", "local")
	random, hasRandom := boolAt(cfg, "优选订阅生成", "本地IP库", "随机IP")
	r.LocalEnabled = hasLocal && local
	r.RandomIP = hasRandom && random

	switch {
	case !hasLocal:
		r.Warnings = append(r.Warnings, "面板配置中找不到「优选订阅生成.local」，请确认面板版本")
	case !local:
		r.Warnings = append(r.Warnings, "面板未开启本地生成订阅（local=false），写入的 ADD.txt 可能不生效")
	}
	switch {
	case !hasRandom:
		r.Warnings = append(r.Warnings, "无法读取「本地IP库.随机IP」，请确认面板版本")
	case random:
		r.Warnings = append(r.Warnings, "面板已开启「随机IP」，会忽略 ADD.txt 中的优选 IP；请在面板中手动关闭后重新同步")
	}
	return r, nil
}

// PushADD 把优选 IP 列表写入面板的 ADD.txt。
func (c *PanelClient) PushADD(ctx context.Context, text string) error {
	if !strings.HasSuffix(text, "\n") {
		text += "\n"
	}
	resp, err := c.do(ctx, http.MethodPost, "/admin/ADD.txt", "text/plain; charset=utf-8", strings.NewReader(text))
	if err != nil {
		return fmt.Errorf("连接面板失败：%w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusFound || resp.StatusCode == http.StatusMovedPermanently ||
		resp.StatusCode == http.StatusTemporaryRedirect || resp.StatusCode == http.StatusSeeOther {
		return ErrPanelAuth
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("写入 ADD.txt 失败（HTTP %d）：%s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	return nil
}

// ReadADD 读取面板当前的 ADD.txt，用于推送后校验。
func (c *PanelClient) ReadADD(ctx context.Context) (string, error) {
	resp, err := c.do(ctx, http.MethodGet, "/admin/ADD.txt", "", nil)
	if err != nil {
		return "", fmt.Errorf("连接面板失败：%w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("读取 ADD.txt 失败（HTTP %d）", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return "", err
	}
	return string(body), nil
}

// FormatPanelLine 生成一行 ADD.txt 内容，格式为 ip:port#备注 速度。
// IPv6 通过 net.JoinHostPort 自动加上方括号。
func FormatPanelLine(ip string, port int, label string, speed float64) string {
	host := net.JoinHostPort(strings.TrimSpace(ip), strconv.Itoa(port))
	remark := sanitizeRemark(strings.TrimSpace(label))
	if remark == "" {
		remark = "CFSTX"
	}
	if speed > 0 {
		return fmt.Sprintf("%s#%s %.2fMB/s", host, remark, speed)
	}
	return fmt.Sprintf("%s#%s", host, remark)
}

func sanitizeRemark(s string) string {
	return strings.Map(func(r rune) rune {
		if r == '\r' || r == '\n' || r == '\t' || r < 0x20 {
			return ' '
		}
		return r
	}, s)
}

// boolAt 按路径读取嵌套 map 中的 bool 值。
func boolAt(m map[string]any, path ...string) (bool, bool) {
	var cur any = m
	for _, key := range path {
		mm, ok := cur.(map[string]any)
		if !ok {
			return false, false
		}
		v, ok := mm[key]
		if !ok {
			return false, false
		}
		cur = v
	}
	b, ok := cur.(bool)
	return b, ok
}
