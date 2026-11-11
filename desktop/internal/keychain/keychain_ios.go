//go:build ios

package keychain

/*
#cgo LDFLAGS: -framework Security -framework CoreFoundation
#include <Security/Security.h>
#include <stdlib.h>
#include <string.h>

static CFStringRef czlStr(const char *s) {
	return CFStringCreateWithCString(kCFAllocatorDefault, s, kCFStringEncodingUTF8);
}

static CFMutableDictionaryRef czlQuery(const char *service, const char *user) {
	CFMutableDictionaryRef q = CFDictionaryCreateMutable(kCFAllocatorDefault, 0,
		&kCFTypeDictionaryKeyCallBacks, &kCFTypeDictionaryValueCallBacks);
	CFStringRef s = czlStr(service), u = czlStr(user);
	CFDictionarySetValue(q, kSecClass, kSecClassGenericPassword);
	CFDictionarySetValue(q, kSecAttrService, s);
	CFDictionarySetValue(q, kSecAttrAccount, u);
	CFRelease(s);
	CFRelease(u);
	return q;
}

// czlGet 返回 OSStatus; 成功时 *out 指向 malloc 出来的内容, 由调用方 free。
static OSStatus czlGet(const char *service, const char *user, char **out, int *outLen) {
	CFMutableDictionaryRef q = czlQuery(service, user);
	CFDictionarySetValue(q, kSecReturnData, kCFBooleanTrue);
	CFDictionarySetValue(q, kSecMatchLimit, kSecMatchLimitOne);
	CFTypeRef result = NULL;
	OSStatus st = SecItemCopyMatching(q, &result);
	CFRelease(q);
	if (st != errSecSuccess) return st;
	CFDataRef data = (CFDataRef)result;
	CFIndex n = CFDataGetLength(data);
	*out = malloc(n > 0 ? n : 1);
	memcpy(*out, CFDataGetBytePtr(data), n);
	*outLen = (int)n;
	CFRelease(result);
	return errSecSuccess;
}

static OSStatus czlSet(const char *service, const char *user, const char *secret, int n) {
	CFDataRef data = CFDataCreate(kCFAllocatorDefault, (const UInt8 *)secret, n);
	CFMutableDictionaryRef q = czlQuery(service, user);
	CFMutableDictionaryRef attrs = CFDictionaryCreateMutable(kCFAllocatorDefault, 0,
		&kCFTypeDictionaryKeyCallBacks, &kCFTypeDictionaryValueCallBacks);
	CFDictionarySetValue(attrs, kSecValueData, data);
	OSStatus st = SecItemUpdate(q, attrs);
	if (st == errSecItemNotFound) {
		CFDictionarySetValue(q, kSecValueData, data);
		// 解锁过一次后可读: 后台同步 (锁屏时) 也要用到凭据, 但不随备份迁移到别的设备。
		CFDictionarySetValue(q, kSecAttrAccessible, kSecAttrAccessibleAfterFirstUnlockThisDeviceOnly);
		st = SecItemAdd(q, NULL);
	}
	CFRelease(attrs);
	CFRelease(q);
	CFRelease(data);
	return st;
}

static OSStatus czlDelete(const char *service, const char *user) {
	CFMutableDictionaryRef q = czlQuery(service, user);
	OSStatus st = SecItemDelete(q);
	CFRelease(q);
	return st;
}
*/
import "C"

import (
	"fmt"
	"unsafe"
)

func get(service, user string) (string, error) {
	cs, cu := C.CString(service), C.CString(user)
	defer C.free(unsafe.Pointer(cs))
	defer C.free(unsafe.Pointer(cu))
	var out *C.char
	var n C.int
	st := C.czlGet(cs, cu, &out, &n)
	if err := statusErr(st); err != nil {
		return "", err
	}
	defer C.free(unsafe.Pointer(out))
	return C.GoStringN(out, n), nil
}

func set(service, user, secret string) error {
	cs, cu := C.CString(service), C.CString(user)
	defer C.free(unsafe.Pointer(cs))
	defer C.free(unsafe.Pointer(cu))
	cv := C.CString(secret)
	defer C.free(unsafe.Pointer(cv))
	return statusErr(C.czlSet(cs, cu, cv, C.int(len(secret))))
}

func del(service, user string) error {
	cs, cu := C.CString(service), C.CString(user)
	defer C.free(unsafe.Pointer(cs))
	defer C.free(unsafe.Pointer(cu))
	return statusErr(C.czlDelete(cs, cu))
}

func statusErr(st C.OSStatus) error {
	switch st {
	case C.errSecSuccess:
		return nil
	case C.errSecItemNotFound:
		return ErrNotFound
	default:
		return fmt.Errorf("keychain status %d", int(st))
	}
}
