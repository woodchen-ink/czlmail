package main

import (
	"bytes"
	_ "embed"
	"encoding/binary"
	"image"
	"image/color"
	"image/png"
	"strconv"
	"sync"
	"time"
)

// 托盘未读数: 在托盘图标右上角画红色数字角标。Windows 托盘图标是 16~32px 的小图,
// 系统字体不可用, 数字用内置的 3x5 点阵画, 放大两倍后在高分屏上也能看清。

//go:embed build/appicon.png
var appIconPNG []byte

const trayIconSize = 32

// digitGlyphs 是 0-9 的 3x5 点阵, 每行 3 位。
var digitGlyphs = [10][5]uint8{
	{7, 5, 5, 5, 7}, {2, 6, 2, 2, 7}, {7, 1, 7, 4, 7}, {7, 1, 7, 1, 7}, {5, 5, 7, 1, 1},
	{7, 4, 7, 1, 7}, {7, 4, 7, 5, 7}, {7, 1, 1, 1, 1}, {7, 5, 7, 5, 7}, {7, 5, 7, 1, 7},
}

var (
	baseIconOnce sync.Once
	baseIcon     *image.RGBA
)

// trayBase 把 1024px 的应用图标按区域平均缩到托盘尺寸。只做一次。
func trayBase() *image.RGBA {
	baseIconOnce.Do(func() {
		baseIcon = image.NewRGBA(image.Rect(0, 0, trayIconSize, trayIconSize))
		src, err := png.Decode(bytes.NewReader(appIconPNG))
		if err != nil {
			return
		}
		b := src.Bounds()
		sx, sy := b.Dx()/trayIconSize, b.Dy()/trayIconSize
		if sx == 0 || sy == 0 {
			return
		}
		for y := 0; y < trayIconSize; y++ {
			for x := 0; x < trayIconSize; x++ {
				var r, g, bl, al, n uint32
				for yy := 0; yy < sy; yy++ {
					for xx := 0; xx < sx; xx++ {
						cr, cg, cb, ca := src.At(b.Min.X+x*sx+xx, b.Min.Y+y*sy+yy).RGBA()
						r, g, bl, al, n = r+cr, g+cg, bl+cb, al+ca, n+1
					}
				}
				baseIcon.Set(x, y, color.RGBA64{uint16(r / n), uint16(g / n), uint16(bl / n), uint16(al / n)})
			}
		}
	})
	return baseIcon
}

// badgeIcon 生成带未读数的 ICO(内嵌 PNG)。count 为 0 时返回不带角标的图标, 超过 99 显示 99。
func badgeIcon(count int) []byte {
	img := image.NewRGBA(image.Rect(0, 0, trayIconSize, trayIconSize))
	copy(img.Pix, trayBase().Pix)

	if count > 0 {
		text := strconv.Itoa(min(count, 99))
		const scale = 2
		glyphW, glyphH, gap := 3*scale, 5*scale, scale
		textW := len(text)*glyphW + (len(text)-1)*gap
		padX, padY := 3, 3
		w, h := textW+padX*2, glyphH+padY*2
		x0, y0 := trayIconSize-w, 0

		// 角标底色: 与界面的 destructive 语义一致的红色; 托盘图标在系统任务栏上, 用不到 CSS token。
		red := color.RGBA{0xD1, 0x3B, 0x3B, 0xFF}
		white := color.RGBA{0xFF, 0xFF, 0xFF, 0xFF}
		radius := h / 2
		for y := 0; y < h; y++ {
			for x := 0; x < w; x++ {
				// 圆角胶囊形
				dx := 0
				if x < radius {
					dx = radius - x
				} else if x >= w-radius {
					dx = x - (w - radius - 1)
				}
				dy := y - h/2
				if dx*dx+dy*dy <= radius*radius || (x >= radius && x < w-radius) {
					img.Set(x0+x, y0+y, red)
				}
			}
		}
		cx := x0 + padX
		for _, ch := range text {
			g := digitGlyphs[ch-'0']
			for row := 0; row < 5; row++ {
				for col := 0; col < 3; col++ {
					if g[row]&(1<<(2-col)) == 0 {
						continue
					}
					for sy := 0; sy < scale; sy++ {
						for sx := 0; sx < scale; sx++ {
							img.Set(cx+col*scale+sx, y0+padY+row*scale+sy, white)
						}
					}
				}
			}
			cx += glyphW + gap
		}
	}

	var pngBuf bytes.Buffer
	_ = png.Encode(&pngBuf, img)
	return pngICO(pngBuf.Bytes(), trayIconSize)
}

// pngICO 把 PNG 包成单图 ICO(Windows Vista 起支持 ICO 内嵌 PNG)。
func pngICO(pngData []byte, size int) []byte {
	var buf bytes.Buffer
	_ = binary.Write(&buf, binary.LittleEndian, [3]uint16{0, 1, 1})
	dim := uint8(size)
	if size >= 256 {
		dim = 0
	}
	buf.Write([]byte{dim, dim, 0, 0})
	_ = binary.Write(&buf, binary.LittleEndian, uint16(1))  // planes
	_ = binary.Write(&buf, binary.LittleEndian, uint16(32)) // bpp
	_ = binary.Write(&buf, binary.LittleEndian, uint32(len(pngData)))
	_ = binary.Write(&buf, binary.LittleEndian, uint32(6+16))
	buf.Write(pngData)
	return buf.Bytes()
}

// unreadTotal 统计各账号收件箱的未读数, 关闭了通知的账号不计入。
func (a *App) unreadTotal() int {
	st, err := a.currentStore()
	if err != nil {
		return 0
	}
	accounts, err := st.Accounts(a.ctx)
	if err != nil {
		return 0
	}
	total := 0
	for _, acc := range accounts {
		if a.mailMuted(acc.ID) {
			continue
		}
		boxes, err := st.Mailboxes(a.ctx, acc.ID)
		if err != nil {
			continue
		}
		for _, b := range boxes {
			if b.Kind == "inbox" {
				total += int(b.UnreadEmails)
			}
		}
	}
	return total
}

var trayUnread struct {
	sync.Mutex
	timer *time.Timer
	last  int
}

// refreshTrayUnreadSoon 合并短时间内的多次变化(同步一轮会触发多个账号的变更)后更新托盘。
func (a *App) refreshTrayUnreadSoon() {
	trayUnread.Lock()
	defer trayUnread.Unlock()
	if trayUnread.timer != nil {
		trayUnread.timer.Stop()
	}
	trayUnread.timer = time.AfterFunc(500*time.Millisecond, func() {
		n := a.unreadTotal()
		trayUnread.Lock()
		changed := n != trayUnread.last
		trayUnread.last = n
		trayUnread.Unlock()
		if changed {
			a.setTrayUnread(n)
		}
	})
}
