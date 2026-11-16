package net.czl.mail.core;

import android.app.AlarmManager;
import android.app.PendingIntent;
import android.content.Context;
import android.content.Intent;
import android.os.Build;
import android.util.Log;
import org.json.JSONArray;
import org.json.JSONException;
import org.json.JSONObject;

/**
 * 日程提醒的系统定时 (desktop/reminders_schedule.go 的 scheduleReminders 请求)。
 * 内核把未来的提醒整表交过来, 这里逐条设成闹钟, 到点由 ReminderReceiver 弹通知 —— 进程被系统回收后照样会响。
 *
 * 每条提醒占一个槽位, 槽位号就是 PendingIntent 的 requestCode: 整表替换时前几个槽位覆盖、其余取消,
 * 不必记住上一批登记了什么。列表另存一份在 SharedPreferences: 重启与程序更新会清掉全部闹钟, 由 BootReceiver 重新登记。
 */
final class Reminders {
    private static final String TAG = "CzlReminders";
    private static final String PREFS = "czl_reminders";
    private static final String KEY_ITEMS = "items";
    /** 槽位数, 不小于内核一次交来的条数上限 (scheduledReminderMax)。 */
    private static final int SLOTS = 64;

    static final String ACTION_FIRE = "net.czl.mail.REMINDER";
    static final String EXTRA_ID = "id";
    static final String EXTRA_TITLE = "title";
    static final String EXTRA_BODY = "body";

    private Reminders() {}

    /** 用这一批替换已登记的全部提醒; 空表即清空。 */
    static synchronized void replace(Context ctx, JSONArray items) {
        ctx.getSharedPreferences(PREFS, Context.MODE_PRIVATE).edit().putString(KEY_ITEMS, items.toString()).apply();
        arm(ctx, items);
    }

    /** 重启或程序更新后按存下的列表重新登记。已经过点的不补发。 */
    static synchronized void restore(Context ctx) {
        String raw = ctx.getSharedPreferences(PREFS, Context.MODE_PRIVATE).getString(KEY_ITEMS, "[]");
        try {
            arm(ctx, new JSONArray(raw));
        } catch (JSONException e) {
            Log.w(TAG, "stored reminders unreadable", e);
        }
    }

    private static void arm(Context ctx, JSONArray items) {
        AlarmManager am = ctx.getSystemService(AlarmManager.class);
        if (am == null) return;
        long now = System.currentTimeMillis();
        int slot = 0;
        for (int i = 0; i < items.length() && slot < SLOTS; i++) {
            JSONObject it = items.optJSONObject(i);
            if (it == null) continue;
            long at = it.optLong("at") * 1000L;
            if (at <= now) continue;
            Intent intent = fireIntent(ctx)
                .putExtra(EXTRA_ID, it.optString("id"))
                .putExtra(EXTRA_TITLE, it.optString("title"))
                .putExtra(EXTRA_BODY, it.optString("body"))
                .putExtra(Notifications.EXTRA_ACTION_ID, it.optString("actionId"));
            // 同一槽位的 PendingIntent 相等 (附加数据不参与比较): 重新设置即覆盖上一批留在这个槽位的闹钟。
            set(am, at, PendingIntent.getBroadcast(ctx, slot, intent, PendingIntent.FLAG_UPDATE_CURRENT | PendingIntent.FLAG_IMMUTABLE));
            slot++;
        }
        for (; slot < SLOTS; slot++) {
            PendingIntent old = PendingIntent.getBroadcast(ctx, slot, fireIntent(ctx), PendingIntent.FLAG_NO_CREATE | PendingIntent.FLAG_IMMUTABLE);
            if (old == null) continue;
            am.cancel(old);
            old.cancel();
        }
    }

    private static Intent fireIntent(Context ctx) {
        return new Intent(ctx, ReminderReceiver.class).setAction(ACTION_FIRE);
    }

    /**
     * 准点闹钟需要「闹钟和提醒」权限: Android 13+ 的 USE_EXACT_ALARM 安装即有, 12 的 SCHEDULE_EXACT_ALARM 默认允许但用户可以关。
     * 拿不到时退回非精确闹钟 (系统省电时可能晚几分钟), 不为此打断用户去设置里授权。
     */
    private static void set(AlarmManager am, long at, PendingIntent pi) {
        boolean exact = Build.VERSION.SDK_INT < Build.VERSION_CODES.S || am.canScheduleExactAlarms();
        if (exact) {
            try {
                am.setExactAndAllowWhileIdle(AlarmManager.RTC_WAKEUP, at, pi);
                return;
            } catch (SecurityException e) {
                Log.w(TAG, "exact alarm refused", e);
            }
        }
        am.setAndAllowWhileIdle(AlarmManager.RTC_WAKEUP, at, pi);
    }
}
