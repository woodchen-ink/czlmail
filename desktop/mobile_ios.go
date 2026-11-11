//go:build ios

package main

// iOS 入口: 以 c-archive 编进 iOS 原生壳, 原生壳经这些导出函数驱动 Go 内核。
//
//   - CzlStart     启动内核 (读配置、开缓存库、恢复会话), 在后台队列调用
//   - CzlCall      调 App 的绑定方法, 语义与 Wails 绑定一致 (internal/rpc)
//   - CzlServe     处理 /czl-* 本地路径 (头像、附件预览、正文图片、笔记资源), 对应桌面端 AssetServer 的 Handler
//   - CzlHostReply 原生壳答复 Go 发起的请求 (文件选择等)
//   - CzlOpenURL / CzlNotificationActivated  系统交来的链接与通知点击
//
// 返回给原生壳的字符串与字节都由 C.malloc 分配, 用完调 CzlFree 释放。

/*
#include <stdlib.h>
#include "mobile_ios.h"
*/
import "C"

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"unsafe"

	"github.com/woodchen-ink/czlmail/desktop/internal/auth"
	"github.com/woodchen-ink/czlmail/desktop/internal/platform"
	"github.com/woodchen-ink/czlmail/desktop/internal/rpc"
	"github.com/woodchen-ink/czlmail/desktop/notify"
)

// c-archive 需要 main 包有 main 函数, 原生壳不会调用它。
func main() {}

var mobile struct {
	once   sync.Once
	app    *App
	rpc    *rpc.Dispatcher
	assets http.Handler
}

//export CzlStart
func CzlStart(ev C.CzlEventFn, host C.CzlHostFn) {
	mobile.once.Do(func() {
		setNativeCallbacks(ev, host)
		a := NewApp()
		mobile.app = a
		mobile.rpc = rpc.New(a)
		mobile.assets = a.assetHandler()

		notify.SetPoster(func(n notify.Notification) error {
			_, err := hostRequest(context.Background(), "notify", map[string]string{
				"title": n.Title, "body": n.Body, "actionId": n.ActionID,
			})
			return err
		})
		// OAuth 授权页在应用内弹出 (ASWebAuthenticationSession), 回调到回环端口后由这里关掉。
		auth.OpenBrowser = func(url string) error {
			_, err := hostRequest(context.Background(), "authSession", map[string]string{"url": url})
			return err
		}
		auth.CloseBrowser = func() {
			go func() { _, _ = hostRequest(context.Background(), "authSessionClose", struct{}{}) }()
		}
		platform.Opener = func(path string, reveal bool) error {
			_, err := hostRequest(context.Background(), "openPath", map[string]any{"path": path, "reveal": reveal})
			return err
		}

		a.startup(context.Background())
		a.domReady(context.Background())
	})
}

//export CzlCall
func CzlCall(method *C.char, args *C.char, failed *C.int) *C.char {
	*failed = 0
	if mobile.rpc == nil {
		*failed = 1
		return C.CString("2261 core not started")
	}
	out, err := mobile.rpc.Call(C.GoString(method), json.RawMessage(C.GoString(args)))
	if err != nil {
		*failed = 1
		return C.CString(err.Error())
	}
	return C.CString(string(out))
}

//export CzlFree
func CzlFree(p unsafe.Pointer) { C.free(p) }

//export CzlHostReply
func CzlHostReply(id C.int64_t, reply *C.char) {
	deliverHostReply(int64(id), []byte(C.GoString(reply)))
}

//export CzlOpenURL
func CzlOpenURL(url *C.char) {
	if mobile.app != nil {
		mobile.app.handleArgs([]string{C.GoString(url)})
	}
}

//export CzlNotificationActivated
func CzlNotificationActivated(actionID *C.char) {
	notify.Activated(C.GoString(actionID))
}

// CzlServe 处理一次本地路径请求。url 为完整地址 (如 czl-local://app/czl-blob?...), 只取路径与查询;
// headers 为请求头 JSON 对象。返回状态码, 响应头 JSON 与响应体经出参交回。
//
//export CzlServe
func CzlServe(method, url, headers *C.char, body unsafe.Pointer, bodyLen C.int,
	status *C.int, respHeaders **C.char, respBody *unsafe.Pointer, respLen *C.int) {
	rec := httptest.NewRecorder()
	serveLocal(rec, C.GoString(method), C.GoString(url), C.GoString(headers), C.GoBytes(body, bodyLen))

	h := make(map[string]string, len(rec.Header()))
	for k, v := range rec.Header() {
		if len(v) > 0 {
			h[k] = v[0]
		}
	}
	hj, _ := json.Marshal(h)
	*status = C.int(rec.Code)
	*respHeaders = C.CString(string(hj))
	b := rec.Body.Bytes()
	*respLen = C.int(len(b))
	if len(b) == 0 {
		*respBody = nil
		return
	}
	*respBody = C.CBytes(b)
}

func serveLocal(w http.ResponseWriter, method, url, headers string, body []byte) {
	if mobile.assets == nil {
		http.Error(w, "core not started", http.StatusServiceUnavailable)
		return
	}
	var rd io.Reader
	if len(body) > 0 {
		rd = bytes.NewReader(body)
	}
	req, err := http.NewRequest(method, url, rd)
	if err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	var hs map[string]string
	if json.Unmarshal([]byte(headers), &hs) == nil {
		for k, v := range hs {
			req.Header.Set(k, v)
		}
	}
	// 处理器只认路径与查询; 自定义 scheme 的主机名没有意义。
	req.RequestURI = req.URL.RequestURI()
	mobile.assets.ServeHTTP(w, req)
}
