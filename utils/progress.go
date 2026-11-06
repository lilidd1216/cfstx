package utils

import (
	"fmt"

	"github.com/cheggaaa/pb/v3"
)

// Silent 为 true 时不输出终端进度条（GUI 模式使用）。
var Silent bool

type Bar struct {
	pb *pb.ProgressBar
}

func NewBar(count int, MyStrStart, MyStrEnd string) *Bar {
	if Silent {
		return &Bar{}
	}
	tmpl := fmt.Sprintf(`{{counters . }} {{ bar . "[" "-" (cycle . "↖" "↗" "↘" "↙" ) "_" "]"}} %s {{string . "MyStr" | green}} %s `, MyStrStart, MyStrEnd)
	bar := pb.ProgressBarTemplate(tmpl).Start(count)
	return &Bar{pb: bar}
}

func (b *Bar) Grow(num int, MyStrVal string) {
	if b.pb == nil {
		return
	}
	b.pb.Set("MyStr", MyStrVal).Add(num)
}

func (b *Bar) Done() {
	if b.pb == nil {
		return
	}
	b.pb.Finish()
}
