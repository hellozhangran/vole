package kit

import (
	"embed"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/egoist/mygo/ui"
)

// 田鼠奔跑循环：8 帧，256px 透明 PNG（显示到 128pt 都清晰）。
// mygo 只解码 GIF 首帧，所以用帧序列在 Draw 里轮播。
//
//go:embed running/run-*.png
var runningFS embed.FS

// runningFrames 第一次用到时解码一次；嵌入的图坏了只可能是构建问题，直接 panic 暴露。
var runningFrames = sync.OnceValue(func() []*ui.Bitmap {
	var frames []*ui.Bitmap
	for i := 1; i <= 8; i++ {
		data, err := runningFS.ReadFile(fmt.Sprintf("running/run-%02d.png", i))
		if err != nil {
			panic(err)
		}
		b, err := ui.DecodeBitmap(data)
		if err != nil {
			panic("kit: 奔跑帧解码失败: " + err.Error())
		}
		frames = append(frames, b)
	}
	return frames
})

// RunningFPS 是素材设计的帧率（80ms 一帧）。
const RunningFPS = 12.5

// runningPaints 统计奔跑动画的绘制次数，供测试确认「停了就不再重绘」。
var runningPaints atomic.Int64

// RunningVole 画一只奔跑的田鼠，size×size。running 为 false 时静止在第一帧，且不请求任何新帧；
// 为 true 时按 fps 换帧，只在下一帧到点时请求重绘（Painter.After），不跑满屏幕刷新率。
// 元素不在屏幕上时不绘制，也就不会再请求帧。
func RunningVole(c *ui.Context, size, fps float32, running bool) ui.Element {
	frames := runningFrames()
	if fps <= 0 {
		fps = RunningFPS
	}
	period := time.Duration(float64(time.Second) / float64(fps))
	return ui.Box(c).Size(size, size).Shrink(0).Draw(func(p *ui.Painter, r ui.Rect) {
		runningPaints.Add(1)
		i := 0
		if running {
			now := p.Now().UnixNano()
			n := now / int64(period)
			i = int(n % int64(len(frames)))
			p.After(time.Duration((n+1)*int64(period) - now))
		}
		p.Image(frames[i], r, ui.Contain)
	})
}
