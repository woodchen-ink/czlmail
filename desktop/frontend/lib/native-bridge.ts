/**
 * 移动端 (Capacitor 壳) 的绑定适配。
 *
 * 界面代码一律经 `wailsjs/go/main/App` 与 `wailsjs/runtime/runtime` 调后端,
 * 这两个生成文件最终读的是 `window.go.main.App` 与 `window.runtime`。
 * 在 Capacitor 里没有 Wails, 由这段首帧前的内联脚本把这两个对象装上,
 * 转给 iOS 原生壳的插件 `CzlCore`, 原生插件再调 Go 内核 —— 界面代码两端不用分支。
 *
 * 约定与 Wails 一致: 方法参数按位置传数组, 失败时 Promise 以错误文本 reject;
 * 事件参数是数组, 按位置展开交给回调。
 *
 * 写成字符串而不是模块: 要赶在任何组件调绑定之前同步执行,
 * 且只能用原生桥注入的全局 `window.Capacitor` (它在页面脚本之前注入)。
 */
export const nativeBootScript = `(function () {
  var cap = window.Capacitor;
  if (!cap || !cap.isNativePlatform || !cap.isNativePlatform()) return;
  document.documentElement.classList.add("native", "platform-" + cap.getPlatform());

  function call(method, args) {
    return cap.nativePromise("CzlCore", "call", { method: method, args: JSON.stringify(args) }).then(
      function (res) { return res && res.result != null ? JSON.parse(res.result) : null; },
      function (err) { throw (err && err.message) || String(err); }
    );
  }
  window.go = { main: { App: new Proxy({}, {
    get: function (_, name) {
      if (typeof name !== "string") return undefined;
      return function () { return call(name, Array.prototype.slice.call(arguments)); };
    }
  }) } };

  var listeners = {};
  cap.addListener("CzlCore", "event", function (ev) {
    if (!ev) return;
    var list = listeners[ev.name];
    if (!list) return;
    var args = [];
    try { args = JSON.parse(ev.args || "[]"); } catch (e) {}
    list.slice().forEach(function (l) {
      if (l.left === 0) return;
      if (l.left > 0) l.left--;
      try { l.cb.apply(null, args); } catch (e) { console.error(e); }
    });
    listeners[ev.name] = list.filter(function (l) { return l.left !== 0; });
  });

  function off(name, l) {
    var list = listeners[name];
    if (list) listeners[name] = list.filter(function (x) { return x !== l; });
  }
  window.runtime = {
    EventsOnMultiple: function (name, cb, max) {
      var l = { cb: cb, left: max };
      (listeners[name] = listeners[name] || []).push(l);
      return function () { off(name, l); };
    },
    EventsOff: function () {
      for (var i = 0; i < arguments.length; i++) delete listeners[arguments[i]];
    },
    EventsOffAll: function () { listeners = {}; },
    BrowserOpenURL: function (url) { cap.nativePromise("CzlCore", "openURL", { url: url }); },
    OnFileDrop: function () {},
    OnFileDropOff: function () {},
    Environment: function () {
      return Promise.resolve({ buildType: "production", platform: cap.getPlatform(), arch: "arm64" });
    }
  };
})();`;

/**
 * 本地路径 (/czl-*) 的访问地址。
 *
 * 桌面端由 Wails AssetServer 在同源下处理, 用相对路径即可。
 * iOS 的页面由 Capacitor 自己的 scheme 提供, 无法挂额外的路径处理器,
 * 原生壳另注册了 `czl-local://` 交给 Go 处理 (CzlServe)。
 */
export const LOCAL_SCHEME_ORIGIN = "czl-local://app";

export function isNative(): boolean {
  return typeof document !== "undefined" && document.documentElement.classList.contains("native");
}

/** 本地处理器的源: 桌面端为页面自身的源。 */
export function localOrigin(): string {
  return isNative() ? LOCAL_SCHEME_ORIGIN : window.location.origin;
}

/** 把 "/czl-..." 换成当前平台能访问的地址。桌面端原样返回相对路径。 */
export function localUrl(path: string): string {
  return isNative() ? LOCAL_SCHEME_ORIGIN + path : path;
}
