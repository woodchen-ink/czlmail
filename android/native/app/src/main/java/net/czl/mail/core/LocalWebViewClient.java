package net.czl.mail.core;

import android.net.Uri;
import android.webkit.WebResourceRequest;
import android.webkit.WebResourceResponse;
import android.webkit.WebView;
import com.getcapacitor.Bridge;
import com.getcapacitor.BridgeWebViewClient;
import java.io.ByteArrayInputStream;
import java.nio.charset.StandardCharsets;
import java.util.HashMap;
import java.util.Iterator;
import java.util.Map;
import org.json.JSONException;
import org.json.JSONObject;

/**
 * 把页面同源下的 /czl-* 请求 (头像、附件预览、正文图片、笔记资源) 交给 Go 的本地路径处理器,
 * 即桌面端 AssetServer 的同一个 Handler。其余请求照旧由 Capacitor 提供页面文件。
 *
 * 页面源是 https://localhost, 本地路径与页面同源, 不需要 iOS 那样另开 scheme 与 CORS。
 * 拦截只认 localhost 主机: 外部网站上同名路径不会进到这里。
 */
public class LocalWebViewClient extends BridgeWebViewClient {
    public LocalWebViewClient(Bridge bridge) {
        super(bridge);
    }

    @Override
    public WebResourceResponse shouldInterceptRequest(WebView view, WebResourceRequest request) {
        Uri url = request.getUrl();
        String path = url.getPath();
        if ("localhost".equals(url.getHost()) && path != null && path.startsWith("/czl-")) {
            return serve(request);
        }
        return super.shouldInterceptRequest(view, request);
    }

    private static WebResourceResponse serve(WebResourceRequest request) {
        JSONObject headers = new JSONObject();
        try {
            for (Map.Entry<String, String> h : request.getRequestHeaders().entrySet()) headers.put(h.getKey(), h.getValue());
        } catch (JSONException ignored) {
            // 键值都是字符串, 不会失败
        }
        LocalResponse r = Core.serve(request.getMethod(), request.getUrl().toString(), headers.toString());

        Map<String, String> fields = new HashMap<>();
        try {
            JSONObject h = new JSONObject(new String(r.headers, StandardCharsets.UTF_8));
            for (Iterator<String> it = h.keys(); it.hasNext(); ) {
                String k = it.next();
                fields.put(k, h.optString(k));
            }
        } catch (JSONException ignored) {
            // 内核总是给合法 JSON
        }
        String contentType = fields.get("Content-Type");
        String mime = "application/octet-stream", charset = null;
        if (contentType != null && !contentType.isEmpty()) {
            String[] parts = contentType.split(";");
            mime = parts[0].trim();
            for (int i = 1; i < parts.length; i++) {
                String p = parts[i].trim();
                if (p.toLowerCase().startsWith("charset=")) charset = p.substring(8);
            }
        }
        // WebResourceResponse 不接受 3xx 状态码 (抛异常); 本地处理器不会重定向, 万一出现按 404 处理。
        int status = r.status;
        if (status < 100 || status > 599 || (status >= 300 && status < 400)) status = 404;
        String reason = status < 400 ? "OK" : "Error";
        return new WebResourceResponse(mime, charset, status, reason, fields, new ByteArrayInputStream(r.body));
    }
}
