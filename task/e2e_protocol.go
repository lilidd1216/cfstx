package task

// 端到端（真实隧道）探测的协议层。
//
// 这里只放纯函数：UUID/口令、目标地址编码、VLESS/Trojan 请求构造、
// WebSocket 握手与帧编解码。全部可单测，不依赖网络。
//
// 设计目标：与 Clash Verge（mihomo）/ V2RayN 的「真连接延迟」同口径——
// TLS（SNI=Worker 域名）→ WebSocket 101 → VLESS/Trojan → 在隧道内请求目标
// → 读回目标响应。只有完整走通，才算这个 IP 在客户端里可用。

import (
	"bufio"
	"bytes"
	"crypto/rand"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"io"
	"net"
	"net/url"
	"strconv"
	"strings"
)

const (
	// DefaultE2ETarget 默认目标：Cloudflare 自己的 204 端点，国内可达性好。
	DefaultE2ETarget = "http://cp.cloudflare.com/generate_204"
	// AlternateE2ETarget 与 Clash url-test 默认值一致，便于和客户端数字对齐。
	AlternateE2ETarget = "http://www.gstatic.com/generate_204"

	// 单个 WS 帧的载荷上限，防止异常服务端把内存吃光。
	maxWSFramePayload = 1 << 20
	// WS 握手响应头上限。
	maxWSHandshakeBytes = 16 << 10
	// WebSocket 协议固定的 GUID。
	wsAcceptGUID = "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"

	// 探测请求的 UA：用常见浏览器标识，避免在用户自己的域名上留下扫描器指纹。
	e2eUserAgent = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36"
)

// TargetURL 是隧道内请求的目标地址。第一版只支持 http://。
type TargetURL struct {
	Scheme     string // 恒为 http
	Host       string // 目标域名或 IP
	Port       int    // 默认 80
	RequestURI string // 路径 + query
}

// ParseTargetURL 解析目标地址。
//
// 为什么只允许 http：
//   - 隧道本身是 TCP，https 目标意味着要在隧道里再做一次 TLS，
//     第一版不做，避免把「协议支持不全」误判成「IP 不可用」；
//   - Clash / V2RayN 的 url-test 默认也是 http://...generate_204。
func ParseTargetURL(raw string) (*TargetURL, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, fmt.Errorf("目标地址为空")
	}
	if strings.ContainsAny(raw, " \t\r\n") {
		return nil, fmt.Errorf("目标地址包含空白字符")
	}
	if !strings.HasPrefix(strings.ToLower(raw), "http://") {
		return nil, fmt.Errorf("第一版只支持 http:// 目标")
	}
	u, err := url.Parse(raw)
	if err != nil {
		return nil, fmt.Errorf("目标地址无法解析: %w", err)
	}
	if u.Scheme != "http" {
		return nil, fmt.Errorf("第一版只支持 http:// 目标")
	}
	if u.User != nil {
		return nil, fmt.Errorf("目标地址不能包含用户名或密码")
	}
	if u.Hostname() == "" {
		return nil, fmt.Errorf("目标地址缺少域名")
	}
	port := 80
	if p := u.Port(); p != "" {
		n, err := strconv.Atoi(p)
		if err != nil || n < 1 || n > 65535 {
			return nil, fmt.Errorf("目标端口无效: %s", p)
		}
		port = n
	}
	uri := u.RequestURI()
	if uri == "" {
		uri = "/"
	}
	return &TargetURL{Scheme: "http", Host: u.Hostname(), Port: port, RequestURI: uri}, nil
}

// ParseUUID 解析 VLESS UUID，接受 36 位带横线或 32 位裸 hex。
func ParseUUID(s string) ([16]byte, error) {
	var out [16]byte
	clean := strings.ReplaceAll(strings.TrimSpace(s), "-", "")
	if len(clean) != 32 {
		return out, fmt.Errorf("UUID 长度应为 32 或 36 个字符")
	}
	b, err := hex.DecodeString(clean)
	if err != nil {
		return out, fmt.Errorf("UUID 不是合法的十六进制")
	}
	copy(out[:], b)
	return out, nil
}

// HashTrojanPassword 返回 Trojan 认证用的 SHA224 十六进制串（56 字符）。
func HashTrojanPassword(password string) string {
	sum := sha256.Sum224([]byte(password))
	return hex.EncodeToString(sum[:])
}

// buildVLESSRequest 构造 VLESS 请求头 + 首个 HTTP 请求。
//
// 格式：version(1)=0 | UUID(16) | addonLen(1)=0 | cmd(1)=1
//
//	| port(2, BE) | atyp(1) | addr | payload
//
// VLESS 的 atyp：1=IPv4、2=域名、3=IPv6。
func buildVLESSRequest(uuid [16]byte, host string, port int, payload []byte) ([]byte, error) {
	var b bytes.Buffer
	b.WriteByte(0) // version
	b.Write(uuid[:])
	b.WriteByte(0) // addon 长度
	b.WriteByte(1) // command: TCP
	b.WriteByte(byte(port >> 8))
	b.WriteByte(byte(port))
	if err := writeAddress(&b, host, 1, 2, 3); err != nil {
		return nil, err
	}
	b.Write(payload)
	return b.Bytes(), nil
}

// buildTrojanRequest 构造 Trojan 请求头 + 首个 HTTP 请求。
//
// 格式：hex(SHA224(password)) | CRLF | cmd(1)=1 | atyp(1) | addr | port(2, BE)
//
//	| CRLF | payload
//
// ⚠️ Trojan 的 atyp 是 SOCKS5 编码：1=IPv4、3=域名、4=IPv6，
// 与 VLESS 的 2=域名 / 3=IPv6 不同，这是最常见的踩坑点。
func buildTrojanRequest(password, host string, port int, payload []byte) ([]byte, error) {
	if strings.TrimSpace(password) == "" {
		return nil, fmt.Errorf("Trojan 口令为空")
	}
	var b bytes.Buffer
	b.WriteString(HashTrojanPassword(password))
	b.WriteString("\r\n")
	b.WriteByte(1) // CONNECT
	if err := writeAddress(&b, host, 1, 3, 4); err != nil {
		return nil, err
	}
	b.WriteByte(byte(port >> 8))
	b.WriteByte(byte(port))
	b.WriteString("\r\n")
	b.Write(payload)
	return b.Bytes(), nil
}

// writeAddress 写地址：IPv4 → atypV4；域名 → atypDomain + 长度 + 内容；IPv6 → atypV6。
func writeAddress(b *bytes.Buffer, host string, atypV4, atypDomain, atypV6 byte) error {
	if ip := net.ParseIP(host); ip != nil {
		if v4 := ip.To4(); v4 != nil {
			b.WriteByte(atypV4)
			b.Write(v4)
			return nil
		}
		v6 := ip.To16()
		if v6 == nil {
			return fmt.Errorf("IPv6 地址无效")
		}
		b.WriteByte(atypV6)
		b.Write(v6)
		return nil
	}
	if len(host) == 0 || len(host) > 255 {
		return fmt.Errorf("目标域名长度非法")
	}
	b.WriteByte(atypDomain)
	b.WriteByte(byte(len(host)))
	b.WriteString(host)
	return nil
}

// BuildE2EHTTPRequest 构造隧道内的 HTTP 请求。
//
// 不发 Connection: close —— 否则目标/Worker 可能在首个响应后关闭连接，
// 第二次（warm）就测不到了。warm 正是 Clash/V2Ray 显示 100~200ms 的口径。
func BuildE2EHTTPRequest(target *TargetURL) []byte {
	return []byte("GET " + target.RequestURI + " HTTP/1.1\r\n" +
		"Host: " + target.Host + "\r\n" +
		"User-Agent: " + e2eUserAgent + "\r\n" +
		"Accept: */*\r\n" +
		"Connection: keep-alive\r\n\r\n")
}

// BuildWSHandshake 构造 WebSocket 升级请求。
//
// path 原样发送（不 URL 编码）——edgetunnel 的 `?ed=` 与 `/proxyip=...`
// 都依赖原样路径；编码后服务端解析不到。
func BuildWSHandshake(host string, port int, path, key string) string {
	if path == "" {
		path = "/"
	}
	hostHeader := host
	if port > 0 && port != 443 && port != 80 {
		hostHeader = net.JoinHostPort(host, strconv.Itoa(port))
	}
	return "GET " + path + " HTTP/1.1\r\n" +
		"Host: " + hostHeader + "\r\n" +
		"Upgrade: websocket\r\n" +
		"Connection: Upgrade\r\n" +
		"Sec-WebSocket-Key: " + key + "\r\n" +
		"Sec-WebSocket-Version: 13\r\n" +
		"User-Agent: " + e2eUserAgent + "\r\n\r\n"
}

// ExpectedWSAccept 计算服务端应返回的 Sec-WebSocket-Accept。
func ExpectedWSAccept(key string) string {
	h := sha1.New()
	h.Write([]byte(key + wsAcceptGUID))
	return base64.StdEncoding.EncodeToString(h.Sum(nil))
}

// ReadWSHandshake 读取并解析 WS 握手响应，返回状态码与小写化的 header。
func ReadWSHandshake(br *bufio.Reader, max int) (int, map[string]string, error) {
	if max <= 0 {
		max = maxWSHandshakeBytes
	}
	statusLine, err := br.ReadString('\n')
	if err != nil {
		return 0, nil, err
	}
	fields := strings.Fields(strings.TrimSpace(statusLine))
	if len(fields) < 2 {
		return 0, nil, fmt.Errorf("握手响应异常")
	}
	status, err := strconv.Atoi(fields[1])
	if err != nil {
		return 0, nil, fmt.Errorf("握手状态码异常")
	}
	header := make(map[string]string)
	total := len(statusLine)
	for {
		line, err := br.ReadString('\n')
		if err != nil {
			return status, header, err
		}
		total += len(line)
		if total > max {
			return status, header, fmt.Errorf("握手响应头过大")
		}
		trimmed := strings.TrimRight(line, "\r\n")
		if trimmed == "" {
			return status, header, nil
		}
		if i := strings.IndexByte(trimmed, ':'); i > 0 {
			k := strings.ToLower(strings.TrimSpace(trimmed[:i]))
			v := strings.TrimSpace(trimmed[i+1:])
			header[k] = v
		}
	}
}

// EncodeWSFrame 构造客户端帧（必须掩码，RFC 6455 §5.3）。
func EncodeWSFrame(opcode byte, payload []byte) []byte {
	n := len(payload)
	var hdr []byte
	switch {
	case n < 126:
		hdr = []byte{0x80 | opcode, 0x80 | byte(n)}
	case n < 65536:
		hdr = []byte{0x80 | opcode, 0x80 | 126, byte(n >> 8), byte(n)}
	default:
		hdr = make([]byte, 10)
		hdr[0] = 0x80 | opcode
		hdr[1] = 0x80 | 127
		u := uint64(n)
		for i := 0; i < 8; i++ {
			hdr[2+i] = byte(u >> (56 - 8*i))
		}
	}
	mask := make([]byte, 4)
	_, _ = rand.Read(mask)
	out := make([]byte, 0, len(hdr)+4+n)
	out = append(out, hdr...)
	out = append(out, mask...)
	for i := 0; i < n; i++ {
		out = append(out, payload[i]^mask[i%4])
	}
	return out
}

// ReadWSFrame 读一个服务端帧。服务端通常不掩码，但也兼容掩码帧。
func ReadWSFrame(br *bufio.Reader) (byte, []byte, error) {
	b0, err := br.ReadByte()
	if err != nil {
		return 0, nil, err
	}
	b1, err := br.ReadByte()
	if err != nil {
		return 0, nil, err
	}
	opcode := b0 & 0x0f
	masked := b1&0x80 != 0
	n := int(b1 & 0x7f)
	switch n {
	case 126:
		var ext [2]byte
		if _, err := io.ReadFull(br, ext[:]); err != nil {
			return opcode, nil, err
		}
		n = int(ext[0])<<8 | int(ext[1])
	case 127:
		var ext [8]byte
		if _, err := io.ReadFull(br, ext[:]); err != nil {
			return opcode, nil, err
		}
		n = 0
		for _, b := range ext {
			if n > maxWSFramePayload {
				return opcode, nil, fmt.Errorf("帧过大")
			}
			n = n<<8 | int(b)
		}
	}
	if n > maxWSFramePayload {
		return opcode, nil, fmt.Errorf("帧过大: %d", n)
	}
	var mask []byte
	if masked {
		mask = make([]byte, 4)
		if _, err := io.ReadFull(br, mask); err != nil {
			return opcode, nil, err
		}
	}
	payload := make([]byte, n)
	if _, err := io.ReadFull(br, payload); err != nil {
		return opcode, nil, err
	}
	if masked {
		for i := range payload {
			payload[i] ^= mask[i%4]
		}
	}
	return opcode, payload, nil
}

// StripVLESSResponseHeader 跳过 VLESS 响应头（version + addon 长度 + addon）。
// 数据不足时返回 nil，调用方继续累积。
func StripVLESSResponseHeader(b []byte) []byte {
	if len(b) < 2 {
		return nil
	}
	addonLen := int(b[1])
	if 2+addonLen > len(b) {
		return nil
	}
	return b[2+addonLen:]
}

// newWSKey 生成随机 Sec-WebSocket-Key。
func newWSKey() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return base64.StdEncoding.EncodeToString(b)
}

// writeFull 循环写，避免部分写导致协议帧截断。
func writeFull(w io.Writer, b []byte) error {
	for len(b) > 0 {
		n, err := w.Write(b)
		if err != nil {
			return err
		}
		b = b[n:]
	}
	return nil
}
