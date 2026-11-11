// 移动端原生壳 (iOS / Android) 与 Go 内核之间的回调类型。实现见 mobile_core.go / mobile_callbacks.go。
// Android 经 mobile_jni_android.c 把 JNI 调用转成这些 C 接口。
#ifndef CZL_MOBILE_CORE_H
#define CZL_MOBILE_CORE_H

#include <stdint.h>

// 事件推送给界面: name 为事件名, args 为参数数组的 JSON (与 Wails EventsEmit 的可变参数一致)。
typedef void (*CzlEventFn)(const char *name, const char *args);

// Go 向原生壳发起请求 (文件选择、打开链接、通知、角标等): kind 为请求类型, payload 为 JSON。
// 原生壳处理完后必须以同一 id 调用 CzlHostReply, 否则发起方会一直等待。
typedef void (*CzlHostFn)(int64_t id, const char *kind, const char *payload);

#endif
