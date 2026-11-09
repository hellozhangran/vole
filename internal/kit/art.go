package kit

import (
	"embed"
	"sync"

	"github.com/egoist/mygo/ui"
)

// 田鼠插画（Vole 自己的素材，透明 PNG）。原图 1024px，按最大显示尺寸的 2 倍缩小后嵌入：
// clean-home 440、clean-done / coming-soon 384、analyze-idle / error 288、health-* 192。
//
//go:embed art/*.png
var artFS embed.FS

// ArtName 是插画名，对应 art/<name>.png。
type ArtName string

const (
	ArtCleanHome   ArtName = "clean-home"   // 清理首屏：拿铲子
	ArtCleanDone   ArtName = "clean-done"   // 清理完成：头顶灯泡
	ArtAnalyzeIdle ArtName = "analyze-idle" // 分析静止：拿放大镜
	ArtComingSoon  ArtName = "coming-soon"  // 即将推出：戴安全帽拿扳手
	ArtHealthGood  ArtName = "health-good"  // 健康度：好
	ArtHealthFair  ArtName = "health-fair"  // 健康度：一般
	ArtHealthPoor  ArtName = "health-poor"  // 健康度：差
	ArtError       ArtName = "error"        // 出错 / 缺权限：头顶问号
)

var artCache sync.Map // ArtName → *ui.Bitmap

// artBitmap 第一次用到时解码；嵌入的图坏了只可能是构建问题，直接 panic 暴露。
func artBitmap(name ArtName) *ui.Bitmap {
	if b, ok := artCache.Load(name); ok {
		return b.(*ui.Bitmap)
	}
	data, err := artFS.ReadFile("art/" + string(name) + ".png")
	if err != nil {
		panic("kit: 没有插画 " + string(name))
	}
	b, err := ui.DecodeBitmap(data)
	if err != nil {
		panic("kit: 插画解码失败 " + string(name) + ": " + err.Error())
	}
	artCache.Store(name, b)
	return b
}

// Art 画一张 size×size 的田鼠插画（静止，不请求重绘）。
func Art(c *ui.Context, name ArtName, size float32) ui.Element {
	b := artBitmap(name)
	return ui.Box(c).Size(size, size).Shrink(0).Draw(func(p *ui.Painter, r ui.Rect) {
		p.Image(b, r, ui.Contain)
	})
}
