package main

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"net/http"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"gitee.com/liliaic/cfstx/version"
)

// API 提供本地 HTTP 接口，前端通过 fetch 调用。
type API struct {
	app    *App
	hub    *EventHub
	assets fs.FS
}

// NewAPI 创建 API 实例。assets 用 fs.FS 而不是 embed.FS，
// 这样开发时可以直接指向磁盘目录调试前端。
func NewAPI(app *App, hub *EventHub, assets fs.FS) *API {
	return &API{app: app, hub: hub, assets: assets}
}

// Routes 返回完整的 HTTP 路由。
func (a *API) Routes() http.Handler {
	mux := http.NewServeMux()

	distFS, err := fs.Sub(a.assets, "frontend/dist")
	if err != nil {
		distFS = a.assets
	}
	mux.Handle("/", http.FileServer(http.FS(distFS)))
	mux.HandleFunc("/api/", a.handleAPI)

	return mux
}

func (a *API) handleAPI(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Path

	switch {
	case path == "/api/config" && r.Method == http.MethodGet:
		writeJSON(w, a.app.GetConfig())

	case path == "/api/config" && r.Method == http.MethodPost:
		var cfg Config
		if err := json.NewDecoder(r.Body).Decode(&cfg); err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
		if err := a.app.SaveConfig(cfg); err != nil {
			writeError(w, http.StatusInternalServerError, err)
			return
		}
		writeJSON(w, map[string]bool{"ok": true})

	case path == "/api/config/reset" && r.Method == http.MethodPost:
		writeJSON(w, a.app.ResetConfig())

	case path == "/api/proxy" && r.Method == http.MethodGet:
		// 测速前检测系统代理，前端据此提示用户关闭代理
		writeJSON(w, a.app.CheckProxy())

	case path == "/api/hosts" && r.Method == http.MethodGet:
		writeJSON(w, a.app.GetHostsStatus())

	case path == "/api/hosts/apply" && r.Method == http.MethodPost:
		var body struct {
			Domain string `json:"domain"`
			IP     string `json:"ip"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
		backup, err := a.app.ApplyHosts(body.Domain, body.IP)
		if err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
		writeJSON(w, map[string]string{"backup": backup})

	case path == "/api/hosts/remove" && r.Method == http.MethodPost:
		if err := a.app.RemoveHosts(); err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
		writeJSON(w, map[string]bool{"ok": true})

	case path == "/api/proxyip/sources" && r.Method == http.MethodGet:
		writeJSON(w, a.app.GetProxyIPSources())

	case path == "/api/proxyip/import" && r.Method == http.MethodPost:
		var body struct {
			Key string `json:"key"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
		text, count, err := a.app.ImportProxyIPSource(body.Key)
		if err != nil {
			writeError(w, http.StatusBadGateway, err)
			return
		}
		writeJSON(w, map[string]any{"text": text, "count": count})

	case path == "/api/proxyip/check" && r.Method == http.MethodPost:
		var req ProxyIPCheckRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
		if err := a.app.StartProxyIPCheck(req); err != nil {
			writeError(w, http.StatusConflict, err)
			return
		}
		writeJSON(w, map[string]bool{"ok": true})

	case path == "/api/proxyip/stop" && r.Method == http.MethodPost:
		a.app.StopProxyIPCheck()
		writeJSON(w, map[string]bool{"ok": true})

	case path == "/api/proxyip/status" && r.Method == http.MethodGet:
		writeJSON(w, a.app.GetProxyIPStatus())

	case path == "/api/test/start" && r.Method == http.MethodPost:
		var cfg Config
		if err := json.NewDecoder(r.Body).Decode(&cfg); err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
		if err := a.app.StartTest(cfg); err != nil {
			writeError(w, http.StatusConflict, err)
			return
		}
		writeJSON(w, map[string]bool{"ok": true})

	case path == "/api/test/stop" && r.Method == http.MethodPost:
		a.app.StopTest()
		writeJSON(w, map[string]bool{"ok": true})

	case path == "/api/test/status" && r.Method == http.MethodGet:
		writeJSON(w, a.app.Status())

	case path == "/api/results" && r.Method == http.MethodGet:
		writeJSON(w, a.app.GetResults())

	case path == "/api/export" && r.Method == http.MethodPost:
		path, err := a.app.ExportCSV("")
		if err != nil {
			writeError(w, http.StatusInternalServerError, err)
			return
		}
		writeJSON(w, map[string]string{"path": path})

	case path == "/api/export/txt" && r.Method == http.MethodPost:
		path, err := a.app.ExportTXT("")
		if err != nil {
			writeError(w, http.StatusInternalServerError, err)
			return
		}
		writeJSON(w, map[string]string{"path": path})

	case path == "/api/export/log" && r.Method == http.MethodPost:
		path, err := a.app.ExportLog("")
		if err != nil {
			writeError(w, http.StatusInternalServerError, err)
			return
		}
		writeJSON(w, map[string]string{"path": path})

	case path == "/api/history" && r.Method == http.MethodGet:
		writeJSON(w, a.app.GetHistory())

	case path == "/api/history" && r.Method == http.MethodDelete:
		if err := a.app.ClearHistory(); err != nil {
			writeError(w, http.StatusInternalServerError, err)
			return
		}
		writeJSON(w, map[string]bool{"ok": true})

	case strings.HasPrefix(path, "/api/history/") && r.Method == http.MethodDelete:
		id, err := strconv.ParseInt(strings.TrimPrefix(path, "/api/history/"), 10, 64)
		if err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
		if err := a.app.DeleteHistory(id); err != nil {
			writeError(w, http.StatusInternalServerError, err)
			return
		}
		writeJSON(w, map[string]bool{"ok": true})

	case path == "/api/version" && r.Method == http.MethodGet:
		writeJSON(w, map[string]string{
			"version": a.app.GetVersion(),
			"latest":  a.app.CheckUpdate(),
		})

	case path == "/api/update/check" && r.Method == http.MethodGet:
		info, err := a.app.CheckRelease()
		if err != nil || info == nil {
			writeJSON(w, map[string]any{"hasUpdate": false})
			return
		}
		// 统一前端使用的字段名，避免直接暴露 Gitee 的 browser_download_url
		type assetDTO struct {
			Name string `json:"name"`
			URL  string `json:"url"`
			Size int64  `json:"size"`
		}
		assets := make([]assetDTO, 0, len(info.Assets))
		for _, as := range info.Assets {
			assets = append(assets, assetDTO{Name: as.Name, URL: as.URL, Size: as.Size})
		}
		writeJSON(w, map[string]any{
			"hasUpdate": version.IsNewer(version.Current, info.TagName),
			"current":   version.Current,
			"latest":    info.TagName,
			"name":      info.Name,
			"body":      info.Body,
			"url":       info.HTMLURL,
			"assets":    assets,
		})

	case path == "/api/update/download" && r.Method == http.MethodPost:
		var body struct {
			URL      string `json:"url"`
			Filename string `json:"filename"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
		dest, err := a.app.DownloadUpdate(body.URL, body.Filename)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err)
			return
		}
		writeJSON(w, map[string]string{"path": dest})

	case path == "/api/update/install" && r.Method == http.MethodPost:
		var body struct {
			Path string `json:"path"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
		if err := a.app.InstallUpdate(body.Path); err != nil {
			writeError(w, http.StatusInternalServerError, err)
			return
		}
		writeJSON(w, map[string]bool{"ok": true})

	case path == "/api/events" && r.Method == http.MethodGet:
		a.handleEvents(w, r)

	case path == "/api/open-url" && r.Method == http.MethodPost:
		var body struct {
			URL string `json:"url"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
		openExternalURL(body.URL)
		writeJSON(w, map[string]bool{"ok": true})

	default:
		http.NotFound(w, r)
	}
}

// handleEvents 通过 SSE 推送测速进度。
func (a *API) handleEvents(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")

	ch := a.hub.Subscribe()
	defer a.hub.Unsubscribe(ch)

	// 首帧注释，确保浏览器立即建立连接
	fmt.Fprintf(w, ": connected\n\n")
	flusher.Flush()

	for {
		select {
		case msg, open := <-ch:
			if !open {
				return
			}
			fmt.Fprintf(w, "data: %s\n\n", msg)
			flusher.Flush()
		case <-r.Context().Done():
			return
		}
	}
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, code int, err error) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
}

// openExternalURL 用系统默认浏览器打开链接。
func openExternalURL(url string) {
	if url == "" {
		return
	}
	_ = exec.Command("rundll32", "url.dll,FileProtocolHandler", url).Start()
}

// absOutputPath 把相对路径转换为绝对路径。
func absOutputPath(name string) string {
	abs, err := filepath.Abs(name)
	if err != nil {
		return name
	}
	return abs
}
