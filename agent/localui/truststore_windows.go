//go:build windows

package localui

import (
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"os"
	"unsafe"

	"golang.org/x/sys/windows"
)

// Windows cert store constants.
const (
	certStoreProvSystem      = 10
	certStoreLocalMachine    = 0x00020000
	certEncodingX509ASN      = 1
	certStoreName            = "ROOT"
	certCloseStoreForceFlag  = 1
	certStoreAddReplaceExist = 3
)

var (
	crypt32                       = windows.NewLazySystemDLL("crypt32.dll")
	procCertOpenStore             = crypt32.NewProc("CertOpenStore")
	procCertAddEncodedCertToStore = crypt32.NewProc("CertAddEncodedCertificateToStore")
	procCertCloseStore            = crypt32.NewProc("CertCloseStore")
	procCertFindCertInStore       = crypt32.NewProc("CertFindCertificateInStore")
	procCertDeleteCertFromStore   = crypt32.NewProc("CertDeleteCertificateFromStore")
	procCertFreeCertCtx           = crypt32.NewProc("CertFreeCertificateContext")
)

// InstallCATrustStore installs the CA certificate into the Windows
// Local Machine\Root certificate store.
func InstallCATrustStore(caCertPath string) error {
	certPEM, err := os.ReadFile(caCertPath) //nolint:gosec // agent-controlled path
	if err != nil {
		return fmt.Errorf("read CA cert: %w", err)
	}

	block, _ := pem.Decode(certPEM)
	if block == nil {
		return fmt.Errorf("no PEM block in CA cert")
	}

	// Verify it parses.
	if _, err := x509.ParseCertificate(block.Bytes); err != nil {
		return fmt.Errorf("parse CA cert: %w", err)
	}

	storeNamePtr, err := windows.UTF16PtrFromString(certStoreName)
	if err != nil {
		return err
	}

	storeHandle, _, callErr := procCertOpenStore.Call(
		certStoreProvSystem,
		0,
		0,
		certStoreLocalMachine,
		uintptr(unsafe.Pointer(storeNamePtr)),
	)
	if storeHandle == 0 {
		return fmt.Errorf("CertOpenStore: %w", callErr)
	}
	defer procCertCloseStore.Call(storeHandle, certCloseStoreForceFlag) //nolint:errcheck

	ret, _, callErr := procCertAddEncodedCertToStore.Call(
		storeHandle,
		certEncodingX509ASN,
		uintptr(unsafe.Pointer(&block.Bytes[0])),
		uintptr(len(block.Bytes)),
		certStoreAddReplaceExist,
		0,
	)
	if ret == 0 {
		return fmt.Errorf("CertAddEncodedCertificateToStore: %w", callErr)
	}

	return nil
}

// RemoveCATrustStore removes the Moebius Agent local CA from the Windows
// Local Machine\Root certificate store by matching the subject CN.
func RemoveCATrustStore() error {
	storeNamePtr, err := windows.UTF16PtrFromString(certStoreName)
	if err != nil {
		return err
	}

	storeHandle, _, callErr := procCertOpenStore.Call(
		certStoreProvSystem,
		0,
		0,
		certStoreLocalMachine,
		uintptr(unsafe.Pointer(storeNamePtr)),
	)
	if storeHandle == 0 {
		return fmt.Errorf("CertOpenStore: %w", callErr)
	}
	defer procCertCloseStore.Call(storeHandle, certCloseStoreForceFlag) //nolint:errcheck

	// Enumerate and find cert by subject containing "Moebius Agent Local CA".
	// For now, this is a placeholder — full implementation would use
	// CertFindCertificateInStore with CERT_FIND_SUBJECT_STR.
	_ = procCertFindCertInStore
	_ = procCertDeleteCertFromStore
	_ = procCertFreeCertCtx

	return fmt.Errorf("RemoveCATrustStore: not yet fully implemented on Windows")
}

func isDir(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}
