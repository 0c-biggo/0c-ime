//go:build windows

package main

import (
	"fmt"
	"os"
	"unsafe"

	"golang.org/x/sys/windows"
)

// 在執行或換上新版的 0c-update 之前，以 WinVerifyTrust 核對它的 Authenticode 簽章，並要求簽章者與目前這一份相同
// （#292、#295）。發行信任仍由已簽章索引的 ed25519 金鑰與套件雜湊承擔；這一步多擋「暫存目錄裡的檔案在核對雜湊之後
// 被換掉」與「簽章者不同的檔案」，也是端點防護期待看到的：提權執行的程式先核對過發行者。
//
// 目前這一份在這台電腦上驗不過時（開發與測試建置沒有簽章；或這台電腦的根憑證過舊）不核對：沒有可以比對的發行者，
// 而且同樣的環境問題會讓之後每一版都驗不過，不能因此永遠停在原地。不檢查撤銷（不必連 CRL），建構憑證鏈時照常讓
// Windows 補根憑證。簽章者以完整主體比對；發行者名稱真的要換時，要先發一版同時認得新舊名稱的更新器。

var (
	wintrust                           = windows.NewLazySystemDLL("wintrust.dll")
	procWTHelperProvDataFromStateData  = wintrust.NewProc("WTHelperProvDataFromStateData")
	procWTHelperGetProvSignerFromChain = wintrust.NewProc("WTHelperGetProvSignerFromChain")
	procWTHelperGetProvCertFromChain   = wintrust.NewProc("WTHelperGetProvCertFromChain")
)

// cryptProviderCert 是 CRYPT_PROVIDER_CERT 的開頭兩個欄位（cbStruct、pCert）。
type cryptProviderCert struct {
	size uint32
	cert *windows.CertContext
}

const (
	certNameRDNType = 2 // CERT_NAME_RDN_TYPE
	certX500NameStr = 3 // CERT_X500_NAME_STR
)

// authenticodeSigner 核對 path 的簽章（不檢查撤銷），回傳簽章者憑證的完整主體名稱。
func authenticodeSigner(path string) (string, error) {
	p, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return "", err
	}
	file := &windows.WinTrustFileInfo{Size: uint32(unsafe.Sizeof(windows.WinTrustFileInfo{})), FilePath: p}
	data := &windows.WinTrustData{
		Size:                            uint32(unsafe.Sizeof(windows.WinTrustData{})),
		UIChoice:                        windows.WTD_UI_NONE,
		RevocationChecks:                windows.WTD_REVOKE_NONE,
		UnionChoice:                     windows.WTD_CHOICE_FILE,
		StateAction:                     windows.WTD_STATEACTION_VERIFY,
		FileOrCatalogOrBlobOrSgnrOrCert: unsafe.Pointer(file),
	}
	verifyErr := windows.WinVerifyTrustEx(windows.InvalidHWND, &windows.WINTRUST_ACTION_GENERIC_VERIFY_V2, data)
	defer func() {
		data.StateAction = windows.WTD_STATEACTION_CLOSE
		windows.WinVerifyTrustEx(windows.InvalidHWND, &windows.WINTRUST_ACTION_GENERIC_VERIFY_V2, data)
	}()
	if verifyErr != nil {
		return "", fmt.Errorf("數位簽章無效：%w", verifyErr)
	}
	provData, _, _ := procWTHelperProvDataFromStateData.Call(uintptr(data.StateData))
	if provData == 0 {
		return "", fmt.Errorf("讀不到簽章資料")
	}
	signer, _, _ := procWTHelperGetProvSignerFromChain.Call(provData, 0, 0, 0)
	if signer == 0 {
		return "", fmt.Errorf("讀不到簽章者")
	}
	certPtr, _, _ := procWTHelperGetProvCertFromChain.Call(signer, 0)
	if certPtr == 0 {
		return "", fmt.Errorf("讀不到簽章憑證")
	}
	// WTHelper 回傳的是 wintrust 自己的記憶體（隨 StateData 存在到 CLOSE），不是 Go 的物件。
	cert := (*(**cryptProviderCert)(unsafe.Pointer(&certPtr))).cert
	if cert == nil {
		return "", fmt.Errorf("讀不到簽章憑證")
	}
	strType := uint32(certX500NameStr)
	n := windows.CertGetNameString(cert, certNameRDNType, 0, unsafe.Pointer(&strType), nil, 0)
	if n <= 1 {
		return "", fmt.Errorf("簽章憑證沒有主體名稱")
	}
	buf := make([]uint16, n)
	windows.CertGetNameString(cert, certNameRDNType, 0, unsafe.Pointer(&strType), &buf[0], n)
	return windows.UTF16ToString(buf), nil
}

// verifyPublisher：candidate 要有有效簽章，而且簽章者與目前執行的這一份相同。目前這一份沒有有效簽章時不核對。
func verifyPublisher(candidate string) error {
	self, err := os.Executable()
	if err != nil {
		return err
	}
	want, err := authenticodeSigner(self)
	if err != nil {
		return nil // 開發與測試建置：沒有發行者可比對
	}
	got, err := authenticodeSigner(candidate)
	if err != nil {
		return fmt.Errorf("新版 0c-update %w", err)
	}
	if got != want {
		return fmt.Errorf("新版 0c-update 的簽章者（%s）與目前的（%s）不同", got, want)
	}
	return nil
}
