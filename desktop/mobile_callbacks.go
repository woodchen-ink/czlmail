//go:build ios || android

package main

// 保存原生壳注册的回调并从 Go 调用它们。
// cgo 规定带 //export 的文件序言里不能有定义, 所以 C 端的静态变量与跳板函数单独放在这里。

/*
#include <stdlib.h>
#include "mobile_core.h"

static CzlEventFn czlEventCb;
static CzlHostFn czlHostCb;

static void czlSetCallbacks(CzlEventFn ev, CzlHostFn host) {
	czlEventCb = ev;
	czlHostCb = host;
}

static void czlEmit(const char *name, const char *args) {
	if (czlEventCb) czlEventCb(name, args);
}

static int czlHost(int64_t id, const char *kind, const char *payload) {
	if (!czlHostCb) return 0;
	czlHostCb(id, kind, payload);
	return 1;
}
*/
import "C"

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"unsafe"
)

func setNativeCallbacks(ev C.CzlEventFn, host C.CzlHostFn) {
	C.czlSetCallbacks(ev, host)
}

func nativeEmit(name string, argsJSON []byte) {
	cn := C.CString(name)
	ca := C.CString(string(argsJSON))
	defer C.free(unsafe.Pointer(cn))
	defer C.free(unsafe.Pointer(ca))
	C.czlEmit(cn, ca)
}

// hostReply 是原生壳对一次请求的答复。
type hostReply struct {
	Result json.RawMessage `json:"result"`
	Error  string          `json:"error"`
}

var pendingHost struct {
	next atomic.Int64
	mu   sync.Mutex
	m    map[int64]chan hostReply
}

var errHostUnavailable = errors.New("2261 native shell not ready")

// hostRequest 发请求给原生壳并等它答复。对话框可能开着很久, 只在 ctx 结束时放弃等待。
func hostRequest(ctx context.Context, kind string, payload any) (json.RawMessage, error) {
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	id := pendingHost.next.Add(1)
	ch := make(chan hostReply, 1)
	pendingHost.mu.Lock()
	if pendingHost.m == nil {
		pendingHost.m = make(map[int64]chan hostReply)
	}
	pendingHost.m[id] = ch
	pendingHost.mu.Unlock()
	defer func() {
		pendingHost.mu.Lock()
		delete(pendingHost.m, id)
		pendingHost.mu.Unlock()
	}()

	ck := C.CString(kind)
	cp := C.CString(string(body))
	ok := C.czlHost(C.int64_t(id), ck, cp)
	C.free(unsafe.Pointer(ck))
	C.free(unsafe.Pointer(cp))
	if ok == 0 {
		return nil, errHostUnavailable
	}

	select {
	case r := <-ch:
		if r.Error != "" {
			return nil, fmt.Errorf("%s", r.Error)
		}
		return r.Result, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// deliverHostReply 把原生壳的答复交给等待者; 等待者已放弃时丢弃。
func deliverHostReply(id int64, raw []byte) {
	var r hostReply
	if err := json.Unmarshal(raw, &r); err != nil {
		r = hostReply{Error: "2262 malformed native reply: " + err.Error()}
	}
	pendingHost.mu.Lock()
	ch := pendingHost.m[id]
	pendingHost.mu.Unlock()
	if ch != nil {
		ch <- r
	}
}
