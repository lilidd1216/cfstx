package task

import (
	"bufio"
	"bytes"
	"encoding/hex"
	"net"
	"strings"
	"testing"
	"time"

	"gitee.com/liliaic/cfstx/utils"
)

func TestParseUUID(t *testing.T) {
	cases := []struct {
		name string
		in   string
		ok   bool
	}{
		{"36 位带横线", "11111111-1111-1111-1111-111111111111", true},
		{"32 位裸 hex", "11111111111111111111111111111111", true},
		{"大写", "ABCDEF01-2345-6789-ABCD-EF0123456789", true},
		{"空", "", false},
		{"长度不足", "11111111", false},
		{"非 hex", "zzz11111-1111-1111-1111-111111111111", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			u, err := ParseUUID(c.in)
			if c.ok && err != nil {
				t.Fatalf("期望成功，得到 %v", err)
			}
			if !c.ok && err == nil {
				t.Fatalf("期望失败，却成功")
			}
			if c.ok && u == ([16]byte{}) {
				t.Fatalf("UUID 不应为全零")
			}
		})
	}
}

func TestHashTrojanPassword(t *testing.T) {
	got := HashTrojanPassword("password")
	if len(got) != 56 {
		t.Fatalf("SHA224 hex 长度应为 56，得到 %d", len(got))
	}
	if _, err := hex.DecodeString(got); err != nil {
		t.Fatalf("不是合法 hex: %v", err)
	}
}

func TestParseTargetURL(t *testing.T) {
	cases := []struct {
		name string
		in   string
		host string
		port int
		uri  string
		ok   bool
	}{
		{"默认 80", "http://cp.cloudflare.com/generate_204", "cp.cloudflare.com", 80, "/generate_204", true},
		{"显式端口", "http://example.com:8080/a?b=1", "example.com", 8080, "/a?b=1", true},
		{"IP", "http://1.2.3.4/", "1.2.3.4", 80, "/", true},
		{"https 拒绝", "https://example.com/", "", 0, "", false},
		{"ftp 拒绝", "ftp://example.com/", "", 0, "", false},
		{"空", "", "", 0, "", false},
		{"空白", "http://example.com/a b", "", 0, "", false},
		{"带 userinfo", "http://u:p@example.com/", "", 0, "", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := ParseTargetURL(c.in)
			if c.ok != (err == nil) {
				t.Fatalf("ok=%v err=%v", c.ok, err)
			}
			if !c.ok {
				return
			}
			if got.Host != c.host || got.Port != c.port || got.RequestURI != c.uri {
				t.Fatalf("得到 %+v，期望 %s:%d%s", got, c.host, c.port, c.uri)
			}
		})
	}
}

func TestBuildVLESSRequest(t *testing.T) {
	var uuid [16]byte
	for i := range uuid {
		uuid[i] = byte(i)
	}
	payload := []byte("GET / HTTP/1.1\r\n\r\n")
	got, err := buildVLESSRequest(uuid, "example.com", 80, payload)
	if err != nil {
		t.Fatal(err)
	}
	if got[0] != 0 {
		t.Fatalf("version 应为 0，得到 %d", got[0])
	}
	if !bytes.Equal(got[1:17], uuid[:]) {
		t.Fatalf("UUID 位置错误")
	}
	if got[17] != 0 {
		t.Fatalf("addonLen 应为 0")
	}
	if got[18] != 1 {
		t.Fatalf("cmd 应为 1")
	}
	if got[19] != 0 || got[20] != 80 {
		t.Fatalf("端口编码错误: %d %d", got[19], got[20])
	}
	if got[21] != 2 {
		t.Fatalf("域名 atyp 应为 2，得到 %d", got[21])
	}
	if int(got[22]) != len("example.com") {
		t.Fatalf("域名长度错误")
	}
	if string(got[23:23+len("example.com")]) != "example.com" {
		t.Fatalf("域名内容错误")
	}
	if !bytes.HasSuffix(got, payload) {
		t.Fatalf("payload 未附加")
	}
}

func TestBuildTrojanRequest(t *testing.T) {
	payload := []byte("GET / HTTP/1.1\r\n\r\n")
	got, err := buildTrojanRequest("password", "example.com", 443, payload)
	if err != nil {
		t.Fatal(err)
	}
	hashLen := 56
	if string(got[:hashLen]) != HashTrojanPassword("password") {
		t.Fatalf("密码哈希错误")
	}
	if got[hashLen] != '\r' || got[hashLen+1] != '\n' {
		t.Fatalf("CRLF 位置错误")
	}
	if got[hashLen+2] != 1 {
		t.Fatalf("cmd 应为 1")
	}
	// Trojan 的域名 atyp 是 SOCKS5 的 3，不是 VLESS 的 2
	if got[hashLen+3] != 3 {
		t.Fatalf("Trojan 域名 atyp 应为 3，得到 %d", got[hashLen+3])
	}
}

func TestWSFrameRoundTrip(t *testing.T) {
	for _, n := range []int{0, 1, 125, 126, 65535, 65536} {
		payload := bytes.Repeat([]byte("a"), n)
		frame := EncodeWSFrame(0x2, payload)
		op, got, err := ReadWSFrame(bufio.NewReader(bytes.NewReader(frame)))
		if err != nil {
			t.Fatalf("n=%d 解码失败: %v", n, err)
		}
		if op != 0x2 {
			t.Fatalf("n=%d opcode 错误: %d", n, op)
		}
		if !bytes.Equal(got, payload) {
			t.Fatalf("n=%d payload 不一致", n)
		}
	}
}

func TestWSFrameRejectsOversize(t *testing.T) {
	// 伪造一个 127 长度、声称 2MB 的帧头（只有头，没有载荷）
	hdr := []byte{0x82, 127, 0, 0, 0, 0, 0, 0x20, 0, 0}
	_, _, err := ReadWSFrame(bufio.NewReader(bytes.NewReader(hdr)))
	if err == nil {
		t.Fatalf("应拒绝超大帧")
	}
}

func TestExpectedWSAccept(t *testing.T) {
	// RFC 6455 的示例
	got := ExpectedWSAccept("dGhlIHNhbXBsZSBub25jZQ==")
	want := "s3pPLMBiTxaQ9kYGzzhZRbK+xOo="
	if got != want {
		t.Fatalf("得到 %s，期望 %s", got, want)
	}
}

func TestBuildWSHandshakeHostHeader(t *testing.T) {
	h443 := BuildWSHandshake("example.com", 443, "/proxyip=a:443,b:443", "key")
	if !strings.Contains(h443, "Host: example.com\r\n") {
		t.Fatalf("443 端口不应带端口: %s", h443)
	}
	if !strings.Contains(h443, "GET /proxyip=a:443,b:443 HTTP/1.1") {
		t.Fatalf("路径应原样保留")
	}
	h2053 := BuildWSHandshake("example.com", 2053, "/", "key")
	if !strings.Contains(h2053, "Host: example.com:2053\r\n") {
		t.Fatalf("非 443 端口应带端口: %s", h2053)
	}
}

func TestStripVLESSResponseHeader(t *testing.T) {
	if got := StripVLESSResponseHeader([]byte{0, 0, 'H', 'T'}); string(got) != "HT" {
		t.Fatalf("addon=0 剥离错误: %q", got)
	}
	if got := StripVLESSResponseHeader([]byte{0, 2, 'x', 'y', 'H'}); string(got) != "H" {
		t.Fatalf("addon=2 剥离错误: %q", got)
	}
	if got := StripVLESSResponseHeader([]byte{0}); got != nil {
		t.Fatalf("数据不足应返回 nil")
	}
}

func TestSelectE2ECandidates(t *testing.T) {
	var set utils.PingDelaySet
	for i := 0; i < 3; i++ {
		for _, prefix := range []string{"104.16", "104.17", "104.18", "104.19"} {
			ip := net.ParseIP(prefix + ".0.1")
			set = append(set, utils.CloudflareIPData{PingData: &utils.PingData{
				IP: &net.IPAddr{IP: ip}, Port: 443, Sended: 1, Received: 1,
				Delay: time.Duration(i) * time.Millisecond,
			}})
		}
	}
	got := SelectE2ECandidates(set, 8, 5)
	if len(got) != 8 {
		t.Fatalf("应取 8 条，得到 %d", len(got))
	}
	seen := map[string]bool{}
	for i := 0; i < 4; i++ {
		ip := got[i].IP.IP.To4()
		seen[net.IPv4(ip[0], ip[1], 0, 0).String()] = true
	}
	if len(seen) != 4 {
		t.Fatalf("前 4 条应覆盖 4 个网段，实际 %d", len(seen))
	}
}
