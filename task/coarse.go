package task

// 两轮测速的第一轮：对端到端通过的候选做短时下载粗排。
//
// 为什么需要：端到端只回答「能不能用、延迟多少」，不回答「带宽多大」。
// 直接按端到端取前 N 做 10 秒精测，可能把「延迟低但带宽差」的 IP 送进去，
// 而真正快的 IP 因为端到端稍慢被截断。
//
// 粗排约束（与 CFPilot 一致）：
//   - **串行**：带宽是共享资源，并行粗排会互相抢，相对排序同样失真；
//   - 粗排数值只用于排序，**绝不写进结果**；
//   - 单次时长很短（默认 1.5s），失败按 0 处理，交给精测或端到端排序兜底。

import (
	"fmt"
	"time"
)

// CoarseResult 是单个候选的粗排结果。
type CoarseResult struct {
	IP    string
	Port  int
	Speed float64 // MB/s
	OK    bool
}

// CoarseSpeed 串行测一批候选的短时下载速度，返回 ip:port → MB/s。
func CoarseSpeed(targets []*Target, timeout time.Duration, limit int, onProgress func(done, total int, r CoarseResult)) map[string]float64 {
	if timeout <= 0 {
		timeout = 1500 * time.Millisecond
	}
	if limit <= 0 || limit > len(targets) {
		limit = len(targets)
	}
	out := make(map[string]float64, limit)
	for i := 0; i < limit; i++ {
		if cancelled() {
			break
		}
		t := targets[i]
		speed, _, _ := downloadHandlerFor(t, timeout)
		mbps := speed / 1024 / 1024
		out[fmt.Sprintf("%s:%d", t.IP.String(), t.Port)] = mbps
		if onProgress != nil {
			onProgress(i+1, limit, CoarseResult{
				IP:    t.IP.String(),
				Port:  t.Port,
				Speed: mbps,
				OK:    speed > 0,
			})
		}
	}
	return out
}
