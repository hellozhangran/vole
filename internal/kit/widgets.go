package kit

import "github.com/egoist/mygo/ui"

// IconButton 是圆形图标按钮，label 用于无障碍与悬停提示。
func IconButton(c *ui.Context, t Theme, label string, icon *ui.SVG, size float32) ui.Element {
	b := ui.ButtonBase(c).Label(label).Tooltip(label).Size(size, size).Radius(size / 2).TextColor(t.Muted)
	if b.Hovered() {
		b.Background(t.Hover).TextColor(t.Text)
	}
	b.Children(func() { ui.Icon(c, icon).Size(size*0.55, size*0.55) })
	return b
}

// PillButton 是圆角胶囊按钮；primary 为白底黑字的主按钮。
func PillButton(c *ui.Context, t Theme, text string, primary bool) ui.Element {
	b := ui.ButtonBase(c).Label(text).Height(ControlL).Padding(0, 14).Radius(ControlL / 2).FontSize(TextBody).Background(t.Surface).TextColor(t.Text)
	if primary {
		b.Background(ui.Hex("#ffffff")).TextColor(ui.Hex("#111111"))
	} else if b.Hovered() {
		b.Background(t.Selected)
	}
	b.Children(func() { ui.Text(c, text).SingleLine() })
	return b
}

// Dot 是一个实心小圆点（状态指示）。
func Dot(c *ui.Context, color ui.Color, size float32) ui.Element {
	return ui.Box(c).Size(size, size).Shrink(0).Radius(size / 2).Background(color)
}

// Segment 是小号分段切换中的一段：选中时为浅色底。
func Segment(c *ui.Context, t Theme, text string, active bool) ui.Element {
	b := ui.ButtonBase(c).Label(text).Height(ControlS).Padding(0, 10).Radius(ControlS / 2).FontSize(TextSecondary).TextColor(t.Muted)
	if active {
		b.Background(t.Selected).TextColor(t.Text)
	} else if b.Hovered() {
		b.TextColor(t.Text)
	}
	b.Children(func() { ui.Text(c, text).SingleLine() })
	return b
}
