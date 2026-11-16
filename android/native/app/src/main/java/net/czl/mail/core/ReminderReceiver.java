package net.czl.mail.core;

import android.content.BroadcastReceiver;
import android.content.Context;
import android.content.Intent;

/** 日程提醒到点 (Reminders 设的闹钟): 弹通知。不需要内核, 进程是被闹钟拉起来的也能弹。 */
public class ReminderReceiver extends BroadcastReceiver {
    @Override
    public void onReceive(Context ctx, Intent intent) {
        if (!Reminders.ACTION_FIRE.equals(intent.getAction())) return;
        String id = intent.getStringExtra(Reminders.EXTRA_ID);
        String title = intent.getStringExtra(Reminders.EXTRA_TITLE);
        String body = intent.getStringExtra(Reminders.EXTRA_BODY);
        String actionId = intent.getStringExtra(Notifications.EXTRA_ACTION_ID);
        Notifications.postReminder(ctx, id == null ? "" : id, title == null ? "" : title, body == null ? "" : body, actionId == null ? "" : actionId);
    }
}
