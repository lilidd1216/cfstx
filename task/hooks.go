package task

import (
	"sync/atomic"

	"gitee.com/liliaic/cfstx/utils"
)

// ProgressEvent 描述一次测速进度，供 GUI 等调用方消费。
type ProgressEvent struct {
	Stage     string  `json:"stage"`     // fetch / ping / download
	Current   int     `json:"current"`   // 已完成数量
	Total     int     `json:"total"`     // 总数
	Available int     `json:"available"` // 延迟测速中可用的 IP 数
	IP        string  `json:"ip"`        // 当前处理的 IP
	Port      int     `json:"port"`      // 当前处理 IP 使用的端口
	Speed     float64 `json:"speed"`     // 下载速度 MB/s
	Colo      string  `json:"colo"`      // 地区码
	Msg       string  `json:"msg"`       // 阶段附加说明（抓取阶段用）
}

// LogEvent 描述一条测速日志。
type LogEvent struct {
	Level string `json:"level"` // info / warn / error / debug
	Msg   string `json:"msg"`
}

var (
	// ProgressHook 不为 nil 时，每完成一个 IP 的测速都会回调。
	// CLI 模式保持 nil，行为与原版完全一致。
	ProgressHook func(ProgressEvent)

	// LogHook 不为 nil 时，关键日志会回调。
	LogHook func(LogEvent)

	// ResultHook 不为 nil 时，每完成一个达到筛选条件的 IP 下载测速都会回调，
	// 供界面实时追加结果。最终仍会以 TestDownloadSpeed 的返回值为准。
	ResultHook func(utils.CloudflareIPData)

	// cancelFlag 由 GUI 置位，用于中途停止测速。
	cancelFlag int32
)

func emitProgress(e ProgressEvent) {
	if ProgressHook != nil {
		ProgressHook(e)
	}
}

func emitLog(level, msg string) {
	if LogHook != nil {
		LogHook(LogEvent{Level: level, Msg: msg})
	}
}

// Cancel 请求停止当前测速（仅 GUI 使用）。
func Cancel() {
	atomic.StoreInt32(&cancelFlag, 1)
}

// ResetCancel 清除停止标志，开始新一轮测速前调用。
func ResetCancel() {
	atomic.StoreInt32(&cancelFlag, 0)
}

// cancelled 返回是否已请求停止。
func cancelled() bool {
	return atomic.LoadInt32(&cancelFlag) == 1
}
