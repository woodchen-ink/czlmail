package net.czl.mail.core;

import android.content.BroadcastReceiver;
import android.content.Context;
import android.content.Intent;

/** 开机、程序更新后拉起后台同步服务, 不用等用户打开程序才开始收信。 */
public class BootReceiver extends BroadcastReceiver {
    @Override
    public void onReceive(Context ctx, Intent intent) {
        String a = intent.getAction();
        if (Intent.ACTION_BOOT_COMPLETED.equals(a) || Intent.ACTION_MY_PACKAGE_REPLACED.equals(a)) {
            SyncService.start(ctx);
        }
    }
}
