package net.czl.mail.core;

/** 本地路径 (/czl-*) 的一次响应, 由 JNI 构造。headers 为 UTF-8 编码的 JSON 对象。 */
public final class LocalResponse {
    public final int status;
    public final byte[] headers;
    public final byte[] body;

    LocalResponse(int status, byte[] headers, byte[] body) {
        this.status = status;
        this.headers = headers;
        this.body = body;
    }
}
