package net.czl.mail.core;

import android.app.Notification;
import android.app.NotificationChannel;
import android.app.NotificationManager;
import android.app.PendingIntent;
import android.content.Context;
import android.content.Intent;
import android.os.Build;
import androidx.core.app.NotificationCompat;
import androidx.core.app.NotificationManagerCompat;
import java.util.concurrent.atomic.AtomicInteger;
import net.czl.mail.MainActivity;
import net.czl.mail.R;

/**
 * 系统通知。两个渠道: 新邮件与日程提醒 (默认重要性, 有声音); 后台同步服务的常驻通知 (最低重要性, 不打扰),
 * 用户可以在系统设置里单独关掉后者而不影响新邮件通知。
 */
public final class Notifications {
    static final String CHANNEL_MAIL = "mail";
    static final String CHANNEL_SYNC = "sync";
    /** 点通知打开程序时带上 Go 给的 ActionID (email:/event:/update: 前缀), 交回内核分派。 */
    public static final String EXTRA_ACTION_ID = "net.czl.mail.ACTION_ID";
    static final int SYNC_NOTIFICATION_ID = 1;

    private static final AtomicInteger nextId = new AtomicInteger(100);

    private Notifications() {}

    static void ensureChannels(Context ctx) {
        if (Build.VERSION.SDK_INT < Build.VERSION_CODES.O) return;
        NotificationManager nm = ctx.getSystemService(NotificationManager.class);
        NotificationChannel mail = new NotificationChannel(CHANNEL_MAIL, ctx.getString(R.string.channel_mail), NotificationManager.IMPORTANCE_DEFAULT);
        mail.setDescription(ctx.getString(R.string.channel_mail_desc));
        NotificationChannel sync = new NotificationChannel(CHANNEL_SYNC, ctx.getString(R.string.channel_sync), NotificationManager.IMPORTANCE_MIN);
        sync.setDescription(ctx.getString(R.string.channel_sync_desc));
        sync.setShowBadge(false);
        nm.createNotificationChannel(mail);
        nm.createNotificationChannel(sync);
    }

    /** 发一条通知。没有通知权限时系统静默丢弃, 不算错误。 */
    static void post(Context ctx, String title, String body, String actionId) {
        ensureChannels(ctx);
        int id = nextId.incrementAndGet();
        NotificationCompat.Builder b = new NotificationCompat.Builder(ctx, CHANNEL_MAIL)
            .setSmallIcon(R.drawable.ic_stat_mail)
            .setContentTitle(title)
            .setContentText(body)
            .setStyle(new NotificationCompat.BigTextStyle().bigText(body))
            .setCategory(actionId.startsWith("event:") ? NotificationCompat.CATEGORY_EVENT : NotificationCompat.CATEGORY_EMAIL)
            .setAutoCancel(true)
            .setContentIntent(openIntent(ctx, id, actionId));
        try {
            NotificationManagerCompat.from(ctx).notify(id, b.build());
        } catch (SecurityException ignored) {
            // Android 13+ 未授予 POST_NOTIFICATIONS
        }
    }

    /** 后台同步服务的常驻通知, 点开回到程序。 */
    static Notification syncNotification(Context ctx) {
        ensureChannels(ctx);
        return new NotificationCompat.Builder(ctx, CHANNEL_SYNC)
            .setSmallIcon(R.drawable.ic_stat_mail)
            .setContentTitle(ctx.getString(R.string.sync_title))
            .setContentText(ctx.getString(R.string.sync_text))
            .setPriority(NotificationCompat.PRIORITY_MIN)
            .setOngoing(true)
            .setShowWhen(false)
            .setContentIntent(openIntent(ctx, SYNC_NOTIFICATION_ID, ""))
            .build();
    }

    private static PendingIntent openIntent(Context ctx, int requestCode, String actionId) {
        Intent i = new Intent(ctx, MainActivity.class)
            .addFlags(Intent.FLAG_ACTIVITY_NEW_TASK | Intent.FLAG_ACTIVITY_SINGLE_TOP);
        if (!actionId.isEmpty()) i.putExtra(EXTRA_ACTION_ID, actionId);
        return PendingIntent.getActivity(ctx, requestCode, i, PendingIntent.FLAG_UPDATE_CURRENT | PendingIntent.FLAG_IMMUTABLE);
    }
}
