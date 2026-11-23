package platform

// WindowRect 是窗口外框, 单位为物理像素。
type WindowRect struct {
	Left   int32 `json:"left"`
	Top    int32 `json:"top"`
	Right  int32 `json:"right"`
	Bottom int32 `json:"bottom"`
}

func (r WindowRect) empty() bool { return r.Right <= r.Left || r.Bottom <= r.Top }
