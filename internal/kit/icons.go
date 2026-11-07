package kit

import "github.com/egoist/mygo/ui"

// LineIcon 解析 24×24 的线性 SVG 路径为图标（描边 1.6，颜色跟随文字色），供各模块定义自己的图标。
func LineIcon(path string) *ui.SVG { return lineIcon(path) }

// lineIcon 解析 24×24 的线性 SVG 图标，颜色跟随文字色。
func lineIcon(path string) *ui.SVG {
	return ui.MustParseSVG([]byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.6" stroke-linecap="round" stroke-linejoin="round">` + path + `</svg>`))
}

// 图标都是自绘的通用线条图形，不取自 Mole。
var (
	FolderIcon  = lineIcon(`<path d="M3 7a2 2 0 0 1 2-2h4.5l2 2.5H19a2 2 0 0 1 2 2V18a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2V7Z"/>`)
	FileIcon    = lineIcon(`<path d="M6 3h8l4 4v14H6V3Z"/><path d="M14 3v4h4"/>`)
	DiskIcon    = lineIcon(`<rect x="3" y="12" width="18" height="7" rx="2"/><path d="M5.5 12 8 5h8l2.5 7"/><path d="M7 15.5h.01M10 15.5h.01"/>`)
	ChevronIcon = lineIcon(`<path d="m9 6 6 6-6 6"/>`)
	RefreshIcon = lineIcon(`<path d="M20 7v5h-5"/><path d="M19.4 12A7.5 7.5 0 1 1 17 6.6L20 9.5"/>`)
	CloseIcon   = lineIcon(`<path d="m7 7 10 10M17 7 7 17"/>`)
	BackIcon    = lineIcon(`<path d="m15 6-6 6 6 6"/>`)
	LockIcon    = lineIcon(`<rect x="5" y="11" width="14" height="9" rx="2"/><path d="M8 11V8a4 4 0 0 1 8 0v3"/>`)
)
