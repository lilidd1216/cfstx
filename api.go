package main

import (
	"encoding/json"
	"embed"
	"fmt"
	"io/fs"
	"net/http"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

// API 提供本地 HTTP 接口，前端通过 fetch 调用。
type API struct {
	app    *App
	hub    *EventHub
	assets embed.FS
}

// NewAPI 创建 API 实例。
func NewAPI(app *App, hub *EventHub, assets embed.FS) *API {
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
