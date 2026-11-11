// Android 的 JNI 适配: 把 Kotlin 侧 net.czl.mail.core.Native 的 native 方法转成 mobile_core.go 导出的 C 接口,
// 再把 Go 的事件与宿主请求回调到 Kotlin。
//
// 字符串一律以 UTF-8 字节数组 (byte[]) 过 JNI, 不用 jstring: JNI 的 NewStringUTF / GetStringUTFChars 用的是
// "modified UTF-8", 邮件里常见的 emoji (4 字节 UTF-8) 进出都会出错, CheckJNI 下直接让进程崩溃。

#include <jni.h>
#include <stdlib.h>
#include <string.h>
#include "_cgo_export.h"

static JavaVM *czlVM;
static jclass czlNative;
static jmethodID czlOnEvent;
static jmethodID czlOnHost;
static jclass czlResponse;
static jmethodID czlResponseInit;

JNIEXPORT jint JNI_OnLoad(JavaVM *vm, void *reserved) {
	JNIEnv *env;
	if ((*vm)->GetEnv(vm, (void **)&env, JNI_VERSION_1_6) != JNI_OK) return JNI_ERR;
	czlVM = vm;
	// 类要在这里找: Go 线程上调 FindClass 用的是系统类加载器, 找不到应用的类。
	jclass c = (*env)->FindClass(env, "net/czl/mail/core/Native");
	if (!c) return JNI_ERR;
	czlNative = (*env)->NewGlobalRef(env, c);
	czlOnEvent = (*env)->GetStaticMethodID(env, c, "onEvent", "([B[B)V");
	czlOnHost = (*env)->GetStaticMethodID(env, c, "onHost", "(J[B[B)V");
	jclass r = (*env)->FindClass(env, "net/czl/mail/core/LocalResponse");
	if (!r) return JNI_ERR;
	czlResponse = (*env)->NewGlobalRef(env, r);
	czlResponseInit = (*env)->GetMethodID(env, r, "<init>", "(I[B[B)V");
	if (!czlOnEvent || !czlOnHost || !czlResponseInit) return JNI_ERR;
	return JNI_VERSION_1_6;
}

// 取当前线程的 JNIEnv; Go 线程首次回调时挂到 JVM 上, *attached 置 1 由调用方在用完后卸下。
static JNIEnv *czlEnv(int *attached) {
	JNIEnv *env = NULL;
	*attached = 0;
	jint st = (*czlVM)->GetEnv(czlVM, (void **)&env, JNI_VERSION_1_6);
	if (st == JNI_EDETACHED) {
		if ((*czlVM)->AttachCurrentThread(czlVM, &env, NULL) != JNI_OK) return NULL;
		*attached = 1;
	} else if (st != JNI_OK) {
		return NULL;
	}
	return env;
}

static jbyteArray czlBytes(JNIEnv *env, const char *s, size_t n) {
	jbyteArray a = (*env)->NewByteArray(env, (jsize)n);
	if (a && n > 0) (*env)->SetByteArrayRegion(env, a, 0, (jsize)n, (const jbyte *)s);
	return a;
}

// czlCString 把 byte[] 复制成以 0 结尾的 C 字符串 (malloc 分配, 调用方 free)。
static char *czlCString(JNIEnv *env, jbyteArray a) {
	jsize n = a ? (*env)->GetArrayLength(env, a) : 0;
	char *s = malloc((size_t)n + 1);
	if (n > 0) (*env)->GetByteArrayRegion(env, a, 0, n, (jbyte *)s);
	s[n] = 0;
	return s;
}

static void czlEventCallback(const char *name, const char *args) {
	int attached;
	JNIEnv *env = czlEnv(&attached);
	if (!env) return;
	jbyteArray n = czlBytes(env, name, strlen(name));
	jbyteArray a = czlBytes(env, args, strlen(args));
	(*env)->CallStaticVoidMethod(env, czlNative, czlOnEvent, n, a);
	if ((*env)->ExceptionCheck(env)) (*env)->ExceptionClear(env);
	(*env)->DeleteLocalRef(env, n);
	(*env)->DeleteLocalRef(env, a);
	if (attached) (*czlVM)->DetachCurrentThread(czlVM);
}

static void czlHostCallback(int64_t id, const char *kind, const char *payload) {
	int attached;
	JNIEnv *env = czlEnv(&attached);
	if (!env) return;
	jbyteArray k = czlBytes(env, kind, strlen(kind));
	jbyteArray p = czlBytes(env, payload, strlen(payload));
	(*env)->CallStaticVoidMethod(env, czlNative, czlOnHost, (jlong)id, k, p);
	if ((*env)->ExceptionCheck(env)) (*env)->ExceptionClear(env);
	(*env)->DeleteLocalRef(env, k);
	(*env)->DeleteLocalRef(env, p);
	if (attached) (*czlVM)->DetachCurrentThread(czlVM);
}

JNIEXPORT void JNICALL Java_net_czl_mail_core_Native_setenv(JNIEnv *env, jclass cls, jbyteArray key, jbyteArray value) {
	char *k = czlCString(env, key);
	char *v = czlCString(env, value);
	CzlSetenv(k, v);
	free(k);
	free(v);
}

JNIEXPORT void JNICALL Java_net_czl_mail_core_Native_start(JNIEnv *env, jclass cls) {
	CzlStart(czlEventCallback, czlHostCallback);
}

// call 返回结果 JSON 或错误文本; 首字节 '0' 表示成功, '1' 表示失败。
JNIEXPORT jbyteArray JNICALL Java_net_czl_mail_core_Native_call(JNIEnv *env, jclass cls, jbyteArray method, jbyteArray args) {
	char *m = czlCString(env, method);
	char *a = czlCString(env, args);
	int failed = 0;
	char *out = CzlCall(m, a, &failed);
	free(m);
	free(a);
	size_t n = out ? strlen(out) : 0;
	jbyteArray res = (*env)->NewByteArray(env, (jsize)(n + 1));
	jbyte flag = failed ? '1' : '0';
	(*env)->SetByteArrayRegion(env, res, 0, 1, &flag);
	if (n > 0) (*env)->SetByteArrayRegion(env, res, 1, (jsize)n, (const jbyte *)out);
	if (out) free(out);
	return res;
}

JNIEXPORT jobject JNICALL Java_net_czl_mail_core_Native_serve(JNIEnv *env, jclass cls,
		jbyteArray method, jbyteArray url, jbyteArray headers) {
	char *m = czlCString(env, method);
	char *u = czlCString(env, url);
	char *h = czlCString(env, headers);
	int status = 0, respLen = 0;
	char *respHeaders = NULL;
	void *respBody = NULL;
	CzlServe(m, u, h, NULL, 0, &status, &respHeaders, &respBody, &respLen);
	free(m);
	free(u);
	free(h);
	jbyteArray hs = czlBytes(env, respHeaders ? respHeaders : "{}", respHeaders ? strlen(respHeaders) : 2);
	jbyteArray body = czlBytes(env, respBody, (size_t)respLen);
	if (respHeaders) free(respHeaders);
	if (respBody) free(respBody);
	return (*env)->NewObject(env, czlResponse, czlResponseInit, (jint)status, hs, body);
}

JNIEXPORT void JNICALL Java_net_czl_mail_core_Native_hostReply(JNIEnv *env, jclass cls, jlong id, jbyteArray reply) {
	char *r = czlCString(env, reply);
	CzlHostReply((int64_t)id, r);
	free(r);
}

JNIEXPORT void JNICALL Java_net_czl_mail_core_Native_openURL(JNIEnv *env, jclass cls, jbyteArray url) {
	char *u = czlCString(env, url);
	CzlOpenURL(u);
	free(u);
}

JNIEXPORT void JNICALL Java_net_czl_mail_core_Native_notificationActivated(JNIEnv *env, jclass cls, jbyteArray actionID) {
	char *a = czlCString(env, actionID);
	CzlNotificationActivated(a);
	free(a);
}
