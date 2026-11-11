package net.czl.mail.core;

import android.app.Service;
import android.content.Context;
import android.content.Intent;
import android.content.pm.ServiceInfo;
import android.os.Build;
import android.os.IBinder;
import android.util.Log;
import androidx.core.app.ServiceCompat;
import androidx.core.content.ContextCompat;

/**
 * 后台同步的前台服务。邮件服务器 (Stalwart) 的推送只有 EventSource 长连接, 没有能直达手机的推送通道;
 * 界面退到后台后系统很快会冻结或结束进程, 长连接一断就收不到新邮件通知。
 * 前台服务让进程与长连接一直在, 同步与通知都由 Go 内核照常处理, 本服务只负责"活着"。
 * 开机与程序更新后由 BootReceiver 拉起。
 */
public class SyncService extends Service {
    private static final String TAG = "CzlSync";

    public static void start(Context ctx) {
        try {
            ContextCompat.startForegroundService(ctx, new Intent(ctx, SyncService.class));
        } catch (RuntimeException e) {
            // Android 12+ 不允许后台启动前台服务的场景 (ForegroundServiceStartNotAllowedException), 等下次打开程序再启动
            Log.w(TAG, "start sync service", e);
        }
    }

    @Override
    public void onCreate() {
        super.onCreate();
        Core.start(this);
        int type = Build.VERSION.SDK_INT >= Build.VERSION_CODES.UPSIDE_DOWN_CAKE ? ServiceInfo.FOREGROUND_SERVICE_TYPE_SPECIAL_USE : 0;
        try {
            ServiceCompat.startForeground(this, Notifications.SYNC_NOTIFICATION_ID, Notifications.syncNotification(this), type);
        } catch (RuntimeException e) {
            Log.w(TAG, "start foreground", e);
            stopSelf();
        }
    }

    @Override
    public int onStartCommand(Intent intent, int flags, int startId) {
        return START_STICKY;
    }

    @Override
    public IBinder onBind(Intent intent) {
        return null;
    }
}
