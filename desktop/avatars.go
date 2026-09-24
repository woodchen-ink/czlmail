package main

import (
	"net/http"

	"github.com/woodchen-ink/czlmail/desktop/internal/avatar"
	"github.com/woodchen-ink/czlmail/desktop/internal/store"
)

// assetHandler 处理嵌入资源之外的本地路径: 头像代理、附件预览与远程图片代理。
//
// 在 Wails 启动前调用, 此时缓存库尚未打开, 因此各处理器按需取 store 而不是在这里捕获。
func (a *App) assetHandler() http.Handler {
	mux := http.NewServeMux()
	mux.Handle(avatar.Path, a.avatarHandler())
	mux.HandleFunc(BlobPath, a.serveBlob)
	mux.HandleFunc(RemoteImagePath, a.serveRemoteImage)
	mux.HandleFunc(ContactPhotoPath, a.serveContactPhoto)
	return mux
}

func (a *App) avatarHandler() http.Handler {
	return avatar.New(
		func() *store.Store {
			a.mu.RLock()
			defer a.mu.RUnlock()
			return a.store
		},
		func() bool {
			st, err := a.currentStore()
			if err != nil {
				return false
			}
			on, err := st.BoolSetting(a.ctx, store.SettingSenderAvatars, true)
			return err == nil && on
		},
		a.log,
	)
}
