package platform

// WindowRect 是窗口外框, 单位为物理像素。
type WindowRect struct {
	Left   int32 `json:"left"`
	Top    int32 `json:"top"`
	Right  int32 `json:"right"`
	Bottom int32 `json:"bottom"`
}

func (r WindowRect) empty() bool { return r.Right <= r.Left || r.Bottom <= r.Top }

// WindowPlacement 是主窗口此刻的状态。
type WindowPlacement struct {
	// Normal 是「向下还原」时的位置与大小 (工作区坐标, 物理像素)。
	Normal WindowRect
	// Maximized 为 true 表示窗口最大化 (藏在托盘或最小化时指唤出 / 还原后是否最大化)。
	Maximized bool
	// Visible 为 false 表示窗口还藏着: 启动时还没显示, 或缩在托盘里。
	Visible bool
}
