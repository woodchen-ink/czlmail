//go:build !ios && !android

package main

import (
	"testing"

	"github.com/wailsapp/wails/v2/pkg/options"

	"github.com/woodchen-ink/czlmail/desktop/internal/platform"
)

func TestWindowStateStartOptions(t *testing.T) {
	base := func() *options.App {
		return &options.App{Width: 1400, Height: 900, MinWidth: 960, MinHeight: 600}
	}

	o := base()
	windowState{}.startOptions(o)
	if o.Width != 1400 || o.Height != 900 || o.WindowStartState != options.Normal {
		t.Fatalf("empty state changed defaults: %+v", o)
	}

	o = base()
	windowState{Width: 1600, Height: 1000, Maximized: true}.startOptions(o)
	if o.Width != 1600 || o.Height != 1000 || o.WindowStartState != options.Maximised {
		t.Fatalf("saved state not applied: %+v", o)
	}

	// 小于最小尺寸的值(手改、旧文件)不用, 保留默认大小。
	o = base()
	windowState{Width: 200, Height: 100}.startOptions(o)
	if o.Width != 1400 || o.Height != 900 {
		t.Fatalf("too small size applied: %+v", o)
	}
}

func TestWindowStateEqual(t *testing.T) {
	a := windowState{Width: 1400, Height: 900, Normal: &platform.WindowRect{Left: 1, Top: 2, Right: 3, Bottom: 4}}
	b := a
	b.Normal = &platform.WindowRect{Left: 1, Top: 2, Right: 3, Bottom: 4}
	if !a.equal(b) {
		t.Fatal("same rect behind different pointers should be equal")
	}
	b.Normal.Right = 5
	if a.equal(b) {
		t.Fatal("different rect should not be equal")
	}
	if a.equal(windowState{Width: 1400, Height: 900}) {
		t.Fatal("missing rect should not be equal")
	}
}
