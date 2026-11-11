package net.czl.mail.core;

import android.content.Context;
import java.io.File;
import java.nio.charset.StandardCharsets;
import java.util.TimeZone;
import java.util.concurrent.CountDownLatch;
import java.util.concurrent.ExecutorService;
import java.util.concurrent.Executors;
import org.json.JSONException;
import org.json.JSONObject;

/**
 * Go 内核的包装。内核在进程里只启动一次, 由界面 (MainActivity) 或后台同步服务 (SyncService) 先到者启动;
 * 启动 (读配置、开缓存库、恢复会话) 在后台线程进行, 之前到达的调用与本地路径请求等待启动完成,
 * 与桌面端 Wails 先跑 OnStartup 再处理绑定调用的顺序一致。
 */
public final class Core {
    private static final CountDownLatch started = new CountDownLatch(1);
    private static final ExecutorService calls = Executors.newCachedThreadPool();
    private static boolean didStart;
    private static Context app;

    /** 接收事件的插件; 页面加载前发出的事件没有监听者, 丢弃即可。 */
    static volatile CzlCorePlugin plugin;

    private Core() {}

    public static synchronized void start(Context context) {
        if (didStart) return;
        didStart = true;
        app = context.getApplicationContext();
        System.loadLibrary("czlcore");
        configureEnvironment(app);
        new Thread(() -> {
            Native.start();
            started.countDown();
        }, "czl-core-start").start();
    }

    static Context context() {
        return app;
    }

    /**
     * 内核用到的环境变量, 经 Native.setenv 交给 Go (加载 .so 时 Go 取的是进程启动时的环境, 之后 Os.setenv 它看不到):
     * HOME 决定 os.UserConfigDir (配置、缓存库、日志; 未设置时 Go 取 /sdcard);
     * XDG_CACHE_HOME / TMPDIR 让附件、图片缓存、更新包落进系统可回收的缓存目录;
     * TZ: Android 没有 /etc/localtime, 不设时 Go 的本地时间是 UTC。
     */
    private static void configureEnvironment(Context ctx) {
        File cache = ctx.getCacheDir();
        setenv("HOME", ctx.getFilesDir().getAbsolutePath());
        setenv("XDG_CACHE_HOME", cache.getAbsolutePath());
        setenv("TMPDIR", cache.getAbsolutePath());
        setenv("TZ", TimeZone.getDefault().getID());
    }

    private static void setenv(String key, String value) {
        Native.setenv(bytes(key), bytes(value));
    }

    static void awaitStarted() {
        boolean interrupted = false;
        while (true) {
            try {
                started.await();
                break;
            } catch (InterruptedException e) {
                interrupted = true;
            }
        }
        if (interrupted) Thread.currentThread().interrupt();
    }

    public interface Callback {
        void done(String result, String error);
    }

    /** 调绑定方法。args 为参数数组的 JSON; 回调在后台线程执行。 */
    public static void call(String method, String args, Callback cb) {
        calls.execute(() -> {
            awaitStarted();
            byte[] out = Native.call(bytes(method), bytes(args));
            String text = out.length > 1 ? new String(out, 1, out.length - 1, StandardCharsets.UTF_8) : "null";
            if (out.length > 0 && out[0] == '1') cb.done(null, text);
            else cb.done(text, null);
        });
    }

    /** 处理一次本地路径请求, 在调用方线程同步返回。 */
    static LocalResponse serve(String method, String url, String headersJSON) {
        awaitStarted();
        return Native.serve(bytes(method), bytes(url), bytes(headersJSON));
    }

    /** 系统交来的链接 (czlmail:// / mailto:), 由内核分派给界面。 */
    public static void openURL(String url) {
        calls.execute(() -> {
            awaitStarted();
            Native.openURL(bytes(url));
        });
    }

    /** 用户点开了本程序发出的通知。 */
    public static void notificationActivated(String actionId) {
        calls.execute(() -> {
            awaitStarted();
            Native.notificationActivated(bytes(actionId));
        });
    }

    static void emit(String name, String args) {
        CzlCorePlugin p = plugin;
        if (p != null) p.emit(name, args);
    }

    /** 答复 Go 的宿主请求。每个请求必须答复一次。 */
    static void reply(long id, Object result, String error) {
        JSONObject obj = new JSONObject();
        try {
            if (result != null) obj.put("result", result);
            if (error != null) obj.put("error", error);
        } catch (JSONException e) {
            obj = new JSONObject();
            try {
                obj.put("error", "encode reply");
            } catch (JSONException ignored) {
                // 常量键值不会失败
            }
        }
        Native.hostReply(id, bytes(obj.toString()));
    }

    static byte[] bytes(String s) {
        return (s == null ? "" : s).getBytes(StandardCharsets.UTF_8);
    }

    static String text(byte[] b) {
        return b == null ? "" : new String(b, StandardCharsets.UTF_8);
    }
}
