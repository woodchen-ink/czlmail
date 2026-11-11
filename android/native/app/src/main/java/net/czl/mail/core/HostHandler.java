package net.czl.mail.core;

import android.content.ActivityNotFoundException;
import android.content.ContentResolver;
import android.content.Context;
import android.content.Intent;
import android.database.Cursor;
import android.net.Uri;
import android.os.Build;
import android.os.Environment;
import android.os.Handler;
import android.os.Looper;
import android.provider.OpenableColumns;
import android.provider.Settings;
import android.util.Log;
import android.webkit.MimeTypeMap;
import androidx.core.content.FileProvider;
import java.io.File;
import java.io.FileOutputStream;
import java.io.InputStream;
import java.io.OutputStream;
import java.lang.ref.WeakReference;
import java.util.ArrayList;
import java.util.List;
import java.util.Locale;
import org.json.JSONArray;
import org.json.JSONException;
import org.json.JSONObject;
import net.czl.mail.MainActivity;

/**
 * 处理 Go 内核发来的宿主请求 (desktop/host_mobile.go、host_android.go、mobile_core.go 里的 hostRequest)。
 * 请求类型与 iOS 壳的 HostHandler.swift 一一对应, 另有 Android 专属的 saveDir / secret* / installPackage。
 * 每个请求都必须答复一次, 否则 Go 侧的调用会一直等。
 *
 * 凭据与保存目录在回调线程上直接答复 (内核启动恢复会话时就要读凭据, 不能等主线程);
 * 需要界面的请求切到主线程。
 */
public final class HostHandler {
    private static final String TAG = "CzlHost";
    private static final Handler main = new Handler(Looper.getMainLooper());
    private static WeakReference<MainActivity> activity = new WeakReference<>(null);
    /** 正在进行的文件选择 (同一时间只有一个系统选择器)。 */
    private static long pendingPick = -1;

    private HostHandler() {}

    public static void attach(MainActivity a) {
        activity = new WeakReference<>(a);
    }

    public static void detach(MainActivity a) {
        if (activity.get() == a) activity = new WeakReference<>(null);
    }

    static void handle(long id, String kind, String payload) {
        JSONObject obj;
        try {
            obj = new JSONObject(payload);
        } catch (JSONException e) {
            obj = new JSONObject();
        }
        Context ctx = Core.context();
        switch (kind) {
            case "secretGet":
            case "secretSet":
            case "secretDelete":
                secret(ctx, id, kind, obj);
                return;
            case "saveDir":
                Core.reply(id, saveDir(ctx).getAbsolutePath(), null);
                return;
            default:
                JSONObject o = obj;
                main.post(() -> handleOnMain(ctx, id, kind, o));
        }
    }

    private static void handleOnMain(Context ctx, long id, String kind, JSONObject obj) {
        switch (kind) {
            case "pickFiles":
                pickFiles(id, obj);
                break;
            case "openURL":
            case "authSession":
                // OAuth 授权页开在系统浏览器; 回调后浏览器跳 czlmail://signin 把本程序带回来 (auth.ReturnURL)。
                if (openExternal(ctx, obj.optString("url"))) Core.reply(id, null, null);
                else Core.reply(id, null, "2265 unsupported url");
                break;
            case "authSessionClose":
                Core.reply(id, null, null);
                break;
            case "notify":
                Notifications.post(ctx, obj.optString("title"), obj.optString("body"), obj.optString("actionId"));
                Core.reply(id, null, null);
                break;
            case "badge":
                // 启动器角标由通知数决定, 没有单独设置数字的接口。
                Core.reply(id, null, null);
                break;
            case "openPath":
                openPath(ctx, id, obj);
                break;
            case "installPackage":
                installPackage(ctx, id, obj);
                break;
            default:
                Core.reply(id, null, "2266 unsupported host request: " + kind);
        }
    }

    // ---- 凭据

    private static void secret(Context ctx, long id, String kind, JSONObject obj) {
        String service = obj.optString("service"), user = obj.optString("user");
        try {
            JSONObject r = new JSONObject();
            switch (kind) {
                case "secretGet": {
                    String v = SecretStore.get(ctx, service, user);
                    r.put("found", v != null);
                    if (v != null) r.put("value", v);
                    break;
                }
                case "secretSet":
                    SecretStore.set(ctx, service, user, obj.optString("value"));
                    r.put("found", true);
                    break;
                default:
                    r.put("found", SecretStore.delete(ctx, service, user));
            }
            Core.reply(id, r, null);
        } catch (Exception e) {
            Log.e(TAG, "secret " + kind, e);
            Core.reply(id, null, "2271 secret store: " + e.getMessage());
        }
    }

    // ---- 文件

    /**
     * 保存位置: Android 11 起直接写公共「下载/CZL Mail」(文件管理器里可见, 不需要存储权限);
     * 更早的系统写不了公共目录, 退回本程序的外部文件目录。
     */
    private static File saveDir(Context ctx) {
        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.R) {
            return new File(Environment.getExternalStoragePublicDirectory(Environment.DIRECTORY_DOWNLOADS), "CZL Mail");
        }
        File ext = ctx.getExternalFilesDir(Environment.DIRECTORY_DOWNLOADS);
        return ext != null ? ext : new File(ctx.getFilesDir(), "Download");
    }

    private static void pickFiles(long id, JSONObject obj) {
        MainActivity a = activity.get();
        if (a == null || pendingPick >= 0) {
            Core.reply(id, null, "2267 file picker unavailable");
            return;
        }
        if (obj.optBoolean("directory")) {
            Core.reply(id, null, "2270 choosing a folder is not supported on Android");
            return;
        }
        pendingPick = id;
        a.pickDocuments(obj.optBoolean("multiple"), mimeTypes(obj.optJSONArray("filters")));
    }

    /** "*.eml;*.zip" 这类模式换成 MIME 类型; 认不出的扩展名或没给过滤条件时允许任意文件。 */
    static String[] mimeTypes(JSONArray filters) {
        List<String> out = new ArrayList<>();
        if (filters != null) {
            for (int i = 0; i < filters.length(); i++) {
                JSONObject f = filters.optJSONObject(i);
                if (f == null) continue;
                for (String p : f.optString("pattern").split(";")) {
                    String ext = p.trim().replace("*.", "").toLowerCase(Locale.ROOT);
                    if (ext.isEmpty() || ext.equals("*")) return new String[] {"*/*"};
                    String m = MimeTypeMap.getSingleton().getMimeTypeFromExtension(ext);
                    // .eml / .ics 等在部分系统上没有登记, 只能放开; 导入时由内核按内容校验。
                    if (m == null) return new String[] {"*/*"};
                    out.add(m);
                }
            }
        }
        return out.isEmpty() ? new String[] {"*/*"} : out.toArray(new String[0]);
    }

    /** 选择器返回 (主线程)。content:// 文件复制进缓存目录后把路径交给内核 —— Go 读不了 content URI。 */
    public static void onPicked(Context ctx, List<Uri> uris) {
        long id = pendingPick;
        pendingPick = -1;
        if (id < 0) return;
        if (uris == null || uris.isEmpty()) {
            Core.reply(id, new JSONArray(), null);
            return;
        }
        new Thread(() -> {
            JSONArray paths = new JSONArray();
            File dir = new File(ctx.getCacheDir(), "picked/" + System.currentTimeMillis());
            try {
                if (!dir.mkdirs() && !dir.isDirectory()) throw new java.io.IOException("mkdir " + dir);
                for (int i = 0; i < uris.size(); i++) {
                    File dst = uniqueFile(dir, displayName(ctx.getContentResolver(), uris.get(i), "file-" + i));
                    try (InputStream in = ctx.getContentResolver().openInputStream(uris.get(i));
                         OutputStream out = new FileOutputStream(dst)) {
                        if (in == null) throw new java.io.IOException("open " + uris.get(i));
                        byte[] buf = new byte[64 * 1024];
                        for (int n; (n = in.read(buf)) > 0; ) out.write(buf, 0, n);
                    }
                    paths.put(dst.getAbsolutePath());
                }
                Core.reply(id, paths, null);
            } catch (Exception e) {
                Log.e(TAG, "copy picked files", e);
                Core.reply(id, null, "2267 read picked file: " + e.getMessage());
            }
        }, "czl-pick").start();
    }

    private static String displayName(ContentResolver cr, Uri uri, String fallback) {
        try (Cursor c = cr.query(uri, new String[] {OpenableColumns.DISPLAY_NAME}, null, null, null)) {
            if (c != null && c.moveToFirst()) {
                String n = c.getString(0);
                if (n != null && !n.isEmpty()) return new File(n).getName();
            }
        } catch (RuntimeException ignored) {
            // 部分提供方不支持查询文件名
        }
        return fallback;
    }

    private static File uniqueFile(File dir, String name) {
        File f = new File(dir, name);
        int dot = name.lastIndexOf('.');
        String stem = dot > 0 ? name.substring(0, dot) : name, ext = dot > 0 ? name.substring(dot) : "";
        for (int i = 2; f.exists(); i++) f = new File(dir, stem + " (" + i + ")" + ext);
        return f;
    }

    private static Uri shareUri(Context ctx, File f) {
        return FileProvider.getUriForFile(ctx, ctx.getPackageName() + ".fileprovider", f);
    }

    private static String mimeOf(File f) {
        String name = f.getName();
        int dot = name.lastIndexOf('.');
        String m = dot >= 0 ? MimeTypeMap.getSingleton().getMimeTypeFromExtension(name.substring(dot + 1).toLowerCase(Locale.ROOT)) : null;
        return m != null ? m : "application/octet-stream";
    }

    /** 用系统程序打开 (附件、网盘文件); "在文件夹中显示"与没有程序能打开时给分享面板, 可存到别处。 */
    private static void openPath(Context ctx, long id, JSONObject obj) {
        MainActivity a = activity.get();
        Context launcher = a != null ? a : ctx;
        try {
            File f = new File(obj.optString("path"));
            Uri uri = shareUri(ctx, f);
            String mime = mimeOf(f);
            Intent view = new Intent(Intent.ACTION_VIEW).setDataAndType(uri, mime).addFlags(Intent.FLAG_GRANT_READ_URI_PERMISSION);
            Intent send = new Intent(Intent.ACTION_SEND).setType(mime).putExtra(Intent.EXTRA_STREAM, uri).addFlags(Intent.FLAG_GRANT_READ_URI_PERMISSION);
            Intent chooser = Intent.createChooser(send, f.getName());
            if (a == null) {
                view.addFlags(Intent.FLAG_ACTIVITY_NEW_TASK);
                chooser.addFlags(Intent.FLAG_ACTIVITY_NEW_TASK);
            }
            if (obj.optBoolean("reveal")) {
                launcher.startActivity(chooser);
            } else {
                try {
                    launcher.startActivity(view);
                } catch (ActivityNotFoundException e) {
                    launcher.startActivity(chooser);
                }
            }
            Core.reply(id, null, null);
        } catch (Exception e) {
            Log.e(TAG, "open path", e);
            Core.reply(id, null, "2268 cannot open file: " + e.getMessage());
        }
    }

    // ---- 在线更新

    /** 把校验过的 APK 交给系统安装器。没有"安装未知应用"权限时先打开对应设置页, 让用户允许后再点一次。 */
    private static void installPackage(Context ctx, long id, JSONObject obj) {
        MainActivity a = activity.get();
        Context launcher = a != null ? a : ctx;
        int flags = a == null ? Intent.FLAG_ACTIVITY_NEW_TASK : 0;
        try {
            if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.O && !ctx.getPackageManager().canRequestPackageInstalls()) {
                Intent s = new Intent(Settings.ACTION_MANAGE_UNKNOWN_APP_SOURCES, Uri.parse("package:" + ctx.getPackageName())).addFlags(flags);
                launcher.startActivity(s);
                Core.reply(id, null, "2272 请先允许 CZL Mail 安装应用，然后再点一次更新");
                return;
            }
            Uri uri = shareUri(ctx, new File(obj.optString("path")));
            Intent i = new Intent(Intent.ACTION_VIEW)
                .setDataAndType(uri, "application/vnd.android.package-archive")
                .addFlags(Intent.FLAG_GRANT_READ_URI_PERMISSION | flags);
            launcher.startActivity(i);
            Core.reply(id, null, null);
        } catch (Exception e) {
            Log.e(TAG, "install package", e);
            Core.reply(id, null, "2273 cannot start installer: " + e.getMessage());
        }
    }

    // ---- 链接

    /** 只交给系统打开网页与邮件链接, 不让内核或页面借此唤起任意 scheme 或 intent:// 。 */
    public static boolean openExternal(Context ctx, String url) {
        if (url == null) return false;
        Uri u = Uri.parse(url);
        String scheme = u.getScheme() == null ? "" : u.getScheme().toLowerCase(Locale.ROOT);
        if (!scheme.equals("http") && !scheme.equals("https") && !scheme.equals("mailto")) return false;
        MainActivity a = activity.get();
        Intent i = new Intent(Intent.ACTION_VIEW, u).addCategory(Intent.CATEGORY_BROWSABLE);
        try {
            if (a != null) {
                a.startActivity(i);
            } else {
                ctx.startActivity(i.addFlags(Intent.FLAG_ACTIVITY_NEW_TASK));
            }
            return true;
        } catch (ActivityNotFoundException e) {
            return false;
        }
    }
}
