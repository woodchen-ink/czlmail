package net.czl.mail.core;

import com.getcapacitor.JSObject;
import com.getcapacitor.Plugin;
import com.getcapacitor.PluginCall;
import com.getcapacitor.PluginMethod;
import com.getcapacitor.annotation.CapacitorPlugin;

/**
 * 页面与 Go 内核之间的插件。页面侧由 desktop/frontend/lib/native-bridge.ts 装成
 * window.go.main.App 与 window.runtime, 界面代码照常调 Wails 生成的绑定。
 */
@CapacitorPlugin(name = "CzlCore")
public class CzlCorePlugin extends Plugin {

    @Override
    public void load() {
        Core.plugin = this;
    }

    @PluginMethod
    public void call(PluginCall call) {
        String method = call.getString("method");
        if (method == null || method.isEmpty()) {
            call.reject("2261 missing method");
            return;
        }
        String args = call.getString("args", "[]");
        Core.call(method, args, (result, error) -> {
            if (error != null) {
                call.reject(error);
                return;
            }
            JSObject ret = new JSObject();
            ret.put("result", result);
            call.resolve(ret);
        });
    }

    @PluginMethod
    public void openURL(PluginCall call) {
        if (!HostHandler.openExternal(getContext(), call.getString("url"))) {
            call.reject("2265 unsupported url");
            return;
        }
        call.resolve();
    }

    /** 转发 Go 的事件。args 是参数数组的 JSON, 由页面侧解开。 */
    void emit(String name, String args) {
        JSObject data = new JSObject();
        data.put("name", name);
        data.put("args", args);
        notifyListeners("event", data);
    }
}
