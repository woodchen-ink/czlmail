//go:build darwin && !ios && cgo

package smime

/*
#cgo LDFLAGS: -framework Security -framework CoreFoundation
#include <Security/Security.h>

static OSStatus czl_copy_identities(CFArrayRef *identities) {
    const void *keys[] = { kSecClass, kSecMatchLimit, kSecReturnRef };
    const void *values[] = { kSecClassIdentity, kSecMatchLimitAll, kCFBooleanTrue };
    CFDictionaryRef query = CFDictionaryCreate(NULL, keys, values, 3,
        &kCFTypeDictionaryKeyCallBacks, &kCFTypeDictionaryValueCallBacks);
    CFTypeRef result = NULL;
    OSStatus status = SecItemCopyMatching(query, &result);
    CFRelease(query);
    if (status == errSecSuccess) *identities = (CFArrayRef)result;
    return status;
}

static SecIdentityRef czl_identity_at(CFArrayRef identities, CFIndex index) {
    return (SecIdentityRef)CFArrayGetValueAtIndex(identities, index);
}

static SecKeyAlgorithm czl_algorithm(int rsa) {
    return rsa ? kSecKeyAlgorithmRSASignatureDigestPKCS1v15SHA256
               : kSecKeyAlgorithmECDSASignatureDigestX962SHA256;
}
*/
import "C"

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"
	"unsafe"
)

var ErrNotFound = errors.New("smime: certificate not found")

// visitIdentities borrows identities from the user's keychain search list. It does not
// obtain private key handles while rendering the certificate chooser.
func visitIdentities(visit func(C.SecIdentityRef, *x509.Certificate) bool) error {
	var identities C.CFArrayRef
	status := C.czl_copy_identities(&identities)
	if status == C.errSecItemNotFound {
		return nil
	}
	if status != C.errSecSuccess {
		return fmt.Errorf("smime: query keychain identities: OSStatus %d", status)
	}
	defer C.CFRelease(C.CFTypeRef(identities))
	for i, n := C.CFIndex(0), C.CFArrayGetCount(identities); i < n; i++ {
		identity := C.czl_identity_at(identities, i)
		var ref C.SecCertificateRef
		if C.SecIdentityCopyCertificate(identity, &ref) != C.errSecSuccess {
			continue
		}
		der := C.SecCertificateCopyData(ref)
		C.CFRelease(C.CFTypeRef(ref))
		if der == 0 {
			continue
		}
		cert, err := x509.ParseCertificate(copyCFData(der))
		C.CFRelease(C.CFTypeRef(der))
		if err == nil && !visit(identity, cert) {
			break
		}
	}
	return nil
}

func copyCFData(data C.CFDataRef) []byte {
	return C.GoBytes(unsafe.Pointer(C.CFDataGetBytePtr(data)), C.int(C.CFDataGetLength(data)))
}

// List shows email identities with supported key types; duplicate imports share a fingerprint.
func List(now time.Time) ([]Certificate, error) {
	var out []Certificate
	seen := make(map[string]bool)
	err := visitIdentities(func(_ C.SecIdentityRef, cert *x509.Certificate) bool {
		switch cert.PublicKey.(type) {
		case *rsa.PublicKey, *ecdsa.PublicKey:
		default:
			return true
		}
		if len(Emails(cert)) > 0 {
			desc := Describe(cert, now)
			if !seen[desc.Thumbprint] {
				seen[desc.Thumbprint] = true
				out = append(out, desc)
			}
		}
		return true
	})
	return out, err
}

// Open obtains a key handle, never the private key bytes. Access prompts are governed by macOS.
func Open(thumbprint string) (*Signer, error) {
	thumbprint = strings.ToUpper(strings.ReplaceAll(thumbprint, " ", ""))
	if hash, err := hex.DecodeString(thumbprint); err != nil || len(hash) != 20 {
		return nil, errors.New("smime: invalid thumbprint")
	}
	var result *Signer
	var keyErr error
	err := visitIdentities(func(identity C.SecIdentityRef, cert *x509.Certificate) bool {
		if Thumbprint(cert) != thumbprint {
			return true
		}
		var rsaKey C.int
		switch cert.PublicKey.(type) {
		case *rsa.PublicKey:
			rsaKey = 1
		case *ecdsa.PublicKey:
		default:
			keyErr = errors.New("smime: unsupported key type")
			return false
		}
		var key C.SecKeyRef
		if status := C.SecIdentityCopyPrivateKey(identity, &key); status != C.errSecSuccess {
			keyErr = fmt.Errorf("smime: keychain private key: OSStatus %d", status)
			return true
		}
		algorithm := C.czl_algorithm(rsaKey)
		if C.SecKeyIsAlgorithmSupported(key, C.kSecKeyOperationTypeSign, algorithm) == 0 {
			C.CFRelease(C.CFTypeRef(key))
			keyErr = errors.New("smime: key does not support SHA-256 signatures")
			return true
		}
		signer := &keychainSigner{key: key, algorithm: algorithm, public: cert.PublicKey}
		result = &Signer{Cert: cert, Chain: chainFor(cert, nil), Key: signer, Close: signer.close}
		return false
	})
	if err != nil {
		return nil, err
	}
	if result != nil {
		return result, nil
	}
	if keyErr != nil {
		return nil, keyErr
	}
	return nil, ErrNotFound
}

type keychainSigner struct {
	mu        sync.Mutex
	key       C.SecKeyRef
	algorithm C.SecKeyAlgorithm
	public    crypto.PublicKey
}

func (s *keychainSigner) Public() crypto.PublicKey { return s.public }

func (s *keychainSigner) close() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.key != 0 {
		C.CFRelease(C.CFTypeRef(s.key))
		s.key = 0
	}
}

func (s *keychainSigner) Sign(_ io.Reader, digest []byte, opts crypto.SignerOpts) ([]byte, error) {
	_, pss := opts.(*rsa.PSSOptions)
	if opts == nil || pss || opts.HashFunc() != crypto.SHA256 || len(digest) != sha256.Size {
		return nil, errors.New("smime: unsupported signature options")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.key == 0 {
		return nil, errors.New("smime: keychain signer is closed")
	}
	input := C.CFDataCreate(0, (*C.UInt8)(unsafe.Pointer(&digest[0])), C.CFIndex(len(digest)))
	if input == 0 {
		return nil, errors.New("smime: cannot allocate digest data")
	}
	defer C.CFRelease(C.CFTypeRef(input))
	var signErr C.CFErrorRef
	sig := C.SecKeyCreateSignature(s.key, s.algorithm, input, &signErr)
	if signErr != 0 {
		defer C.CFRelease(C.CFTypeRef(signErr))
	}
	if sig == 0 {
		if signErr != 0 {
			return nil, fmt.Errorf("smime: keychain signature failed: status %d", C.CFErrorGetCode(signErr))
		}
		return nil, errors.New("smime: keychain signature failed")
	}
	defer C.CFRelease(C.CFTypeRef(sig))
	return copyCFData(sig), nil
}
