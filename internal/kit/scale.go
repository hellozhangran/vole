package kit

// 字号档位（pt），参考 macOS 习惯。所有界面只引用这里的档位，不直接写数字。
const (
	TextCaption    float32 = 11 // 说明：底部提示、标签、表头
	TextSecondary  float32 = 12 // 次要：大小、补充信息、标题栏状态
	TextBody       float32 = 13 // 正文：tab、列表名称、统计、按钮
	TextTitle      float32 = 14 // 小标题：页标题 / 面包屑
	TextHeadline   float32 = 16 // 空态、等待态的一句话标题
	TextLargeTitle float32 = 22 // 整页的大标题（如「准备开始清理」）
	TextDisplay    float32 = 30 // 页面大数字（状态卡片主数值）
)

// 字重。
const (
	WeightSemibold = 600
)

// 尺寸档位（pt）。
const (
	IconS float32 = 12 // 行尾箭头、面包屑分隔
	IconM float32 = 14 // 卡片标题、提示条
	IconL float32 = 16 // 列表行、表格行图标

	ControlS float32 = 22 // 分段切换
	ControlM float32 = 24 // 圆形图标按钮
	ControlL float32 = 28 // 胶囊按钮
	TabH     float32 = 30 // 顶部 tab 胶囊
	CheckS   float32 = 18 // 勾选框
	TopBarH  float32 = 86 // 顶栏（含 tab 条）高度，按 Mole 的垂直节奏：tab 条中心约 54pt

	RowH     float32 = 40 // 列表行
	RowTable float32 = 38 // 表格行

	RadiusS float32 = 6
	RadiusM float32 = 8
	RadiusL float32 = 14

	Gutter  float32 = 16 // 页面四周留白、主要分栏间距
	PageX   float32 = 28 // 整页模式的左右留白
	PageTop float32 = 44 // 整页模式顶部留白（给红绿灯让位）
	ButtonL float32 = 44 // 首屏大按钮高度
	Hero    float32 = 96 // 侧栏 / 卡片里的主视觉（插画、奔跑田鼠）

	// 插画尺寸（kit.Art）。嵌入的图按各自最大显示尺寸的 2 倍缩好，改这里时要一起重新缩图（见 art.go）。
	ArtS float32 = 144 // 主区空状态、出错
	ArtM float32 = 192 // 即将推出、清理完成
	ArtL float32 = 220 // 首屏大图（清理首屏）
)
