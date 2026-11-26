//go:build windows

package winshell

import (
	"fmt"
	"path/filepath"
	"runtime"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

// 在行程內載入 COM 伺服器 DLL（TSF 文字服務），取代 regsvr32.exe 子行程（#294）。regsvr32 註冊使用者可寫目錄
// （%LOCALAPPDATA%）裡的 DLL 是端點防護規則在找的命令列（Sigma「Regsvr32 Execution From Potential Suspicious Location」）；
// regsvr32 本身做的也只是 OleInitialize、LoadLibraryEx、呼叫匯出函式、FreeLibrary。

// loadLibraryEx 以 DLL 所在目錄找它的相依 DLL（與 regsvr32 相同）。
func loadLibraryEx(dll string) (windows.Handle, error) {
	h, err := windows.LoadLibraryEx(dll, 0, windows.LOAD_WITH_ALTERED_SEARCH_PATH)
	if err != nil {
		return 0, fmt.Errorf("無法載入 %s：%w", filepath.Base(dll), err)
	}
	return h, nil
}

// RegisterServer 呼叫 dll 的 DllRegisterServer（unregister 時 DllUnregisterServer）。在鎖定的執行緒上以單一執行緒
// apartment 初始化 COM（regsvr32 的 OleInitialize 也是 STA）：DLL 註冊時會建立 TSF 的 COM 物件。
func RegisterServer(dll string, unregister bool) error {
	export := "DllRegisterServer"
	if unregister {
		export = "DllUnregisterServer"
	}
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	owned, err := initialized("CoInitializeEx", mustCall(procCoInitializeEx, 0, coinitApartment|coinitNoOLE1DDE))
	if err != nil {
		return err
	}
	if owned {
		defer procCoUninitialize.Call()
	}
	h, err := loadLibraryEx(dll)
	if err != nil {
		return err
	}
	defer windows.FreeLibrary(h)
	proc, err := windows.GetProcAddress(h, export)
	if err != nil {
		return fmt.Errorf("%s 沒有 %s：%w", filepath.Base(dll), export, err)
	}
	r, _, _ := syscall.SyscallN(proc)
	return hresult(filepath.Base(dll)+" "+export, r)
}

var iidIClassFactory = guid("{00000001-0000-0000-C000-000000000046}")

// ProbeInprocServer 核對 x64 的 COM 伺服器 DLL 載入得了、而且給得出 clsid 的 class factory（DllGetClassObject）；
// 不建立文字服務、不碰登錄。x86 不為空字串時另核對 32 位元那份能以映像對應（64 位元行程不能真的載入 32 位元 DLL，
// 這裡只確認檔案是完整的 PE 映像）。
func ProbeInprocServer(x64, x86, clsid string) error {
	id, err := windows.GUIDFromString(clsid)
	if err != nil {
		return err
	}
	h, err := loadLibraryEx(x64)
	if err != nil {
		return err
	}
	defer windows.FreeLibrary(h)
	proc, err := windows.GetProcAddress(h, "DllGetClassObject")
	if err != nil {
		return fmt.Errorf("%s 沒有 DllGetClassObject：%w", filepath.Base(x64), err)
	}
	var factory unsafe.Pointer
	r, _, _ := syscall.SyscallN(proc, uintptr(unsafe.Pointer(&id)), uintptr(unsafe.Pointer(iidIClassFactory)), uintptr(unsafe.Pointer(&factory)))
	if err = hresult("DllGetClassObject", r); err != nil {
		return err
	}
	if factory == nil {
		return fmt.Errorf("DllGetClassObject 沒有傳回 class factory")
	}
	release(factory)
	if x86 == "" {
		return nil
	}
	const asImageResource = 0x20 // LOAD_LIBRARY_AS_IMAGE_RESOURCE：依 PE 映像對應，不執行任何程式碼
	h32, err := windows.LoadLibraryEx(x86, 0, asImageResource)
	if err != nil {
		return fmt.Errorf("無法對應 32 位元 %s：%w", filepath.Base(x86), err)
	}
	windows.FreeLibrary(h32)
	return nil
}
