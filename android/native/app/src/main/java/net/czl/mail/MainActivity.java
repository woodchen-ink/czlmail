package net.czl.mail;

import android.Manifest;
import android.content.Intent;
import android.content.pm.PackageManager;
import android.net.Uri;
import android.os.Build;
import android.os.Bundle;
import androidx.activity.OnBackPressedCallback;
import androidx.activity.result.ActivityResultLauncher;
import androidx.activity.result.contract.ActivityResultContracts;
import androidx.core.content.ContextCompat;
import com.getcapacitor.BridgeActivity;
import java.util.Collections;
import net.czl.mail.core.Core;
import net.czl.mail.core.CzlCorePlugin;
import net.czl.mail.core.HostHandler;
import net.czl.mail.core.LocalWebViewClient;
import net.czl.mail.core.Notifications;
import net.czl.mail.core.SyncService;

/**
 * 主窗口: 在 Capacitor 的基础上启动 Go 内核、注册内核插件、接管 /czl-* 本地路径与返回键,
 * 并把系统交来的链接 (czlmail://、mailto:) 与通知点击转给内核。
 */
public class MainActivity extends BridgeActivity {
    /** OAuth 回调后浏览器跳回本程序用的地址 (desktop/host_android.go 的 auth.ReturnURL), 只需把窗口带到前台。 */
    private static final String SIGNIN_RETURN = "czlmail://signin";

    private final ActivityResultLauncher<String[]> pickOne =
        registerForActivityResult(new ActivityResultContracts.OpenDocument(),
            uri -> HostHandler.onPicked(this, uri == null ? Collections.emptyList() : Collections.singletonList(uri)));
    private final ActivityResultLauncher<String[]> pickMany =
        registerForActivityResult(new ActivityResultContracts.OpenMultipleDocuments(), uris -> HostHandler.onPicked(this, uris));
    private final ActivityResultLauncher<String> askNotifications =
        registerForActivityResult(new ActivityResultContracts.RequestPermission(), granted -> {});

    @Override
    public void onCreate(Bundle savedInstanceState) {
        Core.start(this);
        registerPlugin(CzlCorePlugin.class);
        super.onCreate(savedInstanceState);
        bridge.setWebViewClient(new LocalWebViewClient(bridge));
        HostHandler.attach(this);
        SyncService.start(this);
        requestNotificationPermission();

        // 返回键 / 返回手势先交给界面 (关弹窗、回列表、收侧栏), 界面不处理时退到后台而不是结束: 后台还要继续收信。
        getOnBackPressedDispatcher().addCallback(this, new OnBackPressedCallback(true) {
            @Override
            public void handleOnBackPressed() {
                bridge.getWebView().evaluateJavascript("window.__czlBack ? window.__czlBack() : false", r -> {
                    if (!"true".equals(r)) moveTaskToBack(true);
                });
            }
        });

        handleIntent(getIntent());
    }

    @Override
    protected void onNewIntent(Intent intent) {
        super.onNewIntent(intent);
        handleIntent(intent);
    }

    @Override
    public void onDestroy() {
        HostHandler.detach(this);
        super.onDestroy();
    }

    /** 系统只在程序处于前台时弹权限请求: 启动时就问, 不能等新邮件 (多在后台) 到了再问。 */
    private void requestNotificationPermission() {
        if (Build.VERSION.SDK_INT < Build.VERSION_CODES.TIRAMISU) return;
        if (ContextCompat.checkSelfPermission(this, Manifest.permission.POST_NOTIFICATIONS) != PackageManager.PERMISSION_GRANTED) {
            askNotifications.launch(Manifest.permission.POST_NOTIFICATIONS);
        }
    }

    private void handleIntent(Intent intent) {
        if (intent == null) return;
        String actionId = intent.getStringExtra(Notifications.EXTRA_ACTION_ID);
        if (actionId != null && !actionId.isEmpty()) {
            intent.removeExtra(Notifications.EXTRA_ACTION_ID);
            Core.notificationActivated(actionId);
            return;
        }
        Uri data = intent.getData();
        String action = intent.getAction();
        if (data == null || !(Intent.ACTION_VIEW.equals(action) || Intent.ACTION_SENDTO.equals(action))) return;
        // 处理过就清掉: 旋转屏幕等重建 Activity 时不再打开一遍。
        intent.setData(null);
        String url = data.toString();
        if (url.startsWith(SIGNIN_RETURN)) return;
        Core.openURL(url);
    }

    /** 弹系统文件选择器, 结果经 HostHandler.onPicked 交回内核。 */
    public void pickDocuments(boolean multiple, String[] mimeTypes) {
        if (multiple) pickMany.launch(mimeTypes);
        else pickOne.launch(mimeTypes);
    }
}
