package task

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// fakeE2EOpts 控制假服务端的行为。
type fakeE2EOpts struct {
	protocol   string // vless / trojan
	wsStatus   int    // 非 0 且非 101 时直接返回该状态码
	response   string // 隧道内目标响应（默认 204）
	closeAfter int    // >0 时在发完第 N 个响应后关闭
	noResponse bool   // 101 后不响应（测超时）
	splitFrame bool   // 把响应拆成两个 WS 帧
	sendPing   bool   // 响应前先发 ping
	failAccept bool   // 返回错误的 Sec-WebSocket-Accept
}

func fakeE2EServer(t *testing.T, opts fakeE2EOpts) *httptest.Server {
	t.Helper()
	if opts.response == "" {
		opts.response = "HTTP/1.1 204 No Content\r\n\r\n"
	}
	ts := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if opts.wsStatus != 0 && opts.wsStatus != 101 {
			http.Error(w, "no", opts.wsStatus)
			return
		}
		if !strings.EqualFold(r.Header.Get("Upgrade"), "websocket") {
			http.Error(w, "not websocket", http.StatusBadRequest)
			return
		}
		hj, ok := w.(http.Hijacker)
		if !ok {
			t.Error("服务器不支持 Hijack")
			return
		}
		conn, brw, err := hj.Hijack()
		if err != nil {
			t.Error(err)
			return
		}
		defer conn.Close()

		accept := ExpectedWSAccept(r.Header.Get("Sec-WebSocket-Key"))
		if opts.failAccept {
			accept = "wrong"
		}
		fmt.Fprintf(conn, "HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Accept: %s\r\n\r\n", accept)

		// 先读客户端的第一帧（协议头 + 首个 HTTP 请求）
		if _, _, err := ReadWSFrame(brw.Reader); err != nil {
			return
		}
		if opts.noResponse {
			time.Sleep(300 * time.Millisecond)
			return
		}
		if opts.sendPing {
			_ = writeFull(conn, EncodeWSFrame(0x9, []byte("ping")))
			// 读掉客户端的 pong（可选，不阻塞）
		}
		send := func(body string, withHeader bool) {
			payload := []byte(body)
			if opts.protocol == "vless" && withHeader {
				payload = append([]byte{0, 0}, payload...)
			}
			if opts.splitFrame && len(payload) > 4 {
				cut := len(payload) / 2
				_ = writeFull(conn, EncodeWSFrame(0x2, payload[:cut]))
				_ = writeFull(conn, EncodeWSFrame(0x0, payload[cut:]))
				return
			}
			_ = writeFull(conn, EncodeWSFrame(0x2, payload))
		}
		send(opts.response, true)

		// 再读第二帧（warm 的 HTTP 请求）；同一 VLESS 流内的响应没有 VLESS 头
		if _, _, err := ReadWSFrame(brw.Reader); err == nil {
			send(opts.response, false)
		}
	}))
	ts.StartTLS()
	return ts
}

func runFakeProbe(t *testing.T, ts *httptest.Server, opts fakeE2EOpts) *E2EResult {
	t.Helper()
	cfg := E2EConfig{
		Protocol:       opts.protocol,
		Host:           "fake.example.com",
		UUID:           "11111111-1111-1111-1111-111111111111",
		Password:       "password",
		WSPath:         "/ws",
		TargetURL:      "http://www.gstatic.com/generate_204",
		Attempts:       1,
		AttemptTimeout: 2 * time.Second,
		Insecure:       true,
		dialOverride: func(ctx context.Context, _ string) (net.Conn, error) {
			d := &net.Dialer{Timeout: time.Second}
			return d.DialContext(ctx, "tcp", ts.Listener.Addr().String())
		},
	}
	target := &Target{IP: &net.IPAddr{IP: net.ParseIP("1.2.3.4")}, Port: 443}
	return ProbeE2EOne(context.Background(), target, cfg)
}

func TestProbeE2EVLESS(t *testing.T) {
	opts := fakeE2EOpts{protocol: "vless"}
	ts := fakeE2EServer(t, opts)
	defer ts.Close()
	r := runFakeProbe(t, ts, opts)
	if !r.OK {
		t.Fatalf("应成功，得到 %+v", r)
	}
	if r.HTTPStatus != 204 {
		t.Fatalf("状态码应为 204，得到 %d", r.HTTPStatus)
	}
	if r.Cold <= 0 {
		t.Fatalf("cold 应大于 0")
	}
	// 本地回环可能落在 Windows 时钟的同一个 tick 内，warm 为 0 是允许的；
	// 真实环境（100ms+）由 TestE2EReal 覆盖。
	if r.Warm > 0 && r.Best != r.Warm {
		t.Fatalf("Best 应优先取 warm: best=%v warm=%v", r.Best, r.Warm)
	}
	if r.Warm == 0 && r.Best != r.Cold {
		t.Fatalf("warm 未测到时 Best 应取 cold: best=%v cold=%v", r.Best, r.Cold)
	}
}

func TestProbeE2ETrojan(t *testing.T) {
	opts := fakeE2EOpts{protocol: "trojan"}
	ts := fakeE2EServer(t, opts)
	defer ts.Close()
	r := runFakeProbe(t, ts, opts)
	if !r.OK {
		t.Fatalf("应成功，得到 %+v", r)
	}
	if r.HTTPStatus != 204 {
		t.Fatalf("状态码应为 204，得到 %d", r.HTTPStatus)
	}
}

func TestProbeE2EWS403(t *testing.T) {
	opts := fakeE2EOpts{protocol: "vless", wsStatus: 403}
	ts := fakeE2EServer(t, opts)
	defer ts.Close()
	r := runFakeProbe(t, ts, opts)
	if r.OK {
		t.Fatalf("403 不应成功")
	}
	if r.Status != E2EStatusWSStatus {
		t.Fatalf("状态应为 ws_status，得到 %s", r.Status)
	}
	if r.HTTPStatus != 403 {
		t.Fatalf("应记录 403，得到 %d", r.HTTPStatus)
	}
}

func TestProbeE2EBadAccept(t *testing.T) {
	opts := fakeE2EOpts{protocol: "vless", failAccept: true}
	ts := fakeE2EServer(t, opts)
	defer ts.Close()
	r := runFakeProbe(t, ts, opts)
	if r.OK {
		t.Fatalf("Accept 错误不应成功")
	}
	if r.Status != E2EStatusWSAccept {
		t.Fatalf("状态应为 ws_accept，得到 %s", r.Status)
	}
}

func TestProbeE2ETarget500(t *testing.T) {
	opts := fakeE2EOpts{protocol: "vless", response: "HTTP/1.1 500 Internal Server Error\r\n\r\n"}
	ts := fakeE2EServer(t, opts)
	defer ts.Close()
	r := runFakeProbe(t, ts, opts)
	if !r.OK {
		t.Fatalf("隧道通了就算成功（目标 500 也要记录），得到 %+v", r)
	}
	if r.HTTPStatus != 500 {
		t.Fatalf("状态码应为 500，得到 %d", r.HTTPStatus)
	}
}

func TestProbeE2ESplitFrame(t *testing.T) {
	opts := fakeE2EOpts{protocol: "vless", splitFrame: true}
	ts := fakeE2EServer(t, opts)
	defer ts.Close()
	r := runFakeProbe(t, ts, opts)
	if !r.OK {
		t.Fatalf("分帧响应应能识别，得到 %+v", r)
	}
}

func TestProbeE2ETimeout(t *testing.T) {
	opts := fakeE2EOpts{protocol: "vless", noResponse: true}
	ts := fakeE2EServer(t, opts)
	defer ts.Close()
	cfgAttempt := 300 * time.Millisecond
	cfg := E2EConfig{
		Protocol:       "vless",
		Host:           "fake.example.com",
		UUID:           "11111111-1111-1111-1111-111111111111",
		WSPath:         "/ws",
		TargetURL:      "http://www.gstatic.com/generate_204",
		Attempts:       1,
		AttemptTimeout: cfgAttempt,
		Insecure:       true,
		dialOverride: func(ctx context.Context, _ string) (net.Conn, error) {
			d := &net.Dialer{Timeout: time.Second}
			return d.DialContext(ctx, "tcp", ts.Listener.Addr().String())
		},
	}
	target := &Target{IP: &net.IPAddr{IP: net.ParseIP("1.2.3.4")}, Port: 443}
	start := time.Now()
	r := ProbeE2EOne(context.Background(), target, cfg)
	if r.OK {
		t.Fatalf("无响应不应成功")
	}
	if r.Status != E2EStatusTargetTimeout {
		t.Fatalf("状态应为 target_timeout，得到 %s (%s)", r.Status, r.Error)
	}
	if elapsed := time.Since(start); elapsed > 3*time.Second {
		t.Fatalf("超时控制失效，耗时 %v", elapsed)
	}
}

func TestProbeE2ECancelled(t *testing.T) {
	opts := fakeE2EOpts{protocol: "vless", noResponse: true}
	ts := fakeE2EServer(t, opts)
	defer ts.Close()
	cfg := E2EConfig{
		Protocol:       "vless",
		Host:           "fake.example.com",
		UUID:           "11111111-1111-1111-1111-111111111111",
		WSPath:         "/ws",
		TargetURL:      "http://www.gstatic.com/generate_204",
		Attempts:       1,
		AttemptTimeout: 5 * time.Second,
		Insecure:       true,
		dialOverride: func(ctx context.Context, _ string) (net.Conn, error) {
			d := &net.Dialer{Timeout: time.Second}
			return d.DialContext(ctx, "tcp", ts.Listener.Addr().String())
		},
	}
	target := &Target{IP: &net.IPAddr{IP: net.ParseIP("1.2.3.4")}, Port: 443}
	ResetCancel()
	go func() {
		time.Sleep(200 * time.Millisecond)
		Cancel()
	}()
	start := time.Now()
	r := ProbeE2EOne(context.Background(), target, cfg)
	ResetCancel()
	if r.OK {
		t.Fatalf("取消后不应成功")
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("取消未及时生效，耗时 %v", elapsed)
	}
}

func TestProbeE2EValidate(t *testing.T) {
	cases := []struct {
		name string
		cfg  E2EConfig
		ok   bool
	}{
		{"合法 vless", E2EConfig{Protocol: "vless", Host: "a.com", UUID: "11111111-1111-1111-1111-111111111111", TargetURL: DefaultE2ETarget}, true},
		{"缺 host", E2EConfig{Protocol: "vless", UUID: "11111111-1111-1111-1111-111111111111"}, false},
		{"UUID 非法", E2EConfig{Protocol: "vless", Host: "a.com", UUID: "bad"}, false},
		{"trojan 缺口令", E2EConfig{Protocol: "trojan", Host: "a.com"}, false},
		{"https 目标", E2EConfig{Protocol: "vless", Host: "a.com", UUID: "11111111-1111-1111-1111-111111111111", TargetURL: "https://a.com/"}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := c.cfg.Validate()
			if c.ok != (err == nil) {
				t.Fatalf("ok=%v err=%v", c.ok, err)
			}
		})
	}
}
