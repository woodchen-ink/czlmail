package net.czl.mail.core;

/**
 * Go 内核的 JNI 入口, 实现在 desktop/mobile_jni_android.c。
 * 字符串一律以 UTF-8 字节数组传递 (JNI 的 modified UTF-8 处理不了 emoji)。
 */
final class Native {
    private Native() {}

    /** 在 start 之前设置 Go 看到的环境变量 (Go 运行时不读加载之后 setenv 的值)。 */
    static native void setenv(byte[] key, byte[] value);

    static native void start();

    /** 返回值首字节 '0' 表示成功 (其后是结果 JSON), '1' 表示失败 (其后是错误文本)。 */
    static native byte[] call(byte[] method, byte[] args);

    static native LocalResponse serve(byte[] method, byte[] url, byte[] headers);

    static native void hostReply(long id, byte[] reply);

    static native void openURL(byte[] url);

    static native void notificationActivated(byte[] actionId);

    // 以下由 Go 线程回调, 线程不固定。

    static void onEvent(byte[] name, byte[] args) {
        Core.emit(Core.text(name), Core.text(args));
    }

    static void onHost(long id, byte[] kind, byte[] payload) {
        HostHandler.handle(id, Core.text(kind), Core.text(payload));
    }
}
