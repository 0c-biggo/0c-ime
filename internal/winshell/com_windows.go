//go:build windows

package winshell

import (
	"fmt"
	"runtime"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	ole32   = windows.NewLazySystemDLL("ole32.dll")
	combase = windows.NewLazySystemDLL("combase.dll")

	procCoInitializeEx     = ole32.NewProc("CoInitializeEx")
	procCoUninitialize     = ole32.NewProc("CoUninitialize")
	procCoCreateInstance   = ole32.NewProc("CoCreateInstance")
	procRoInitialize       = combase.NewProc("RoInitialize")
	procRoUninitialize     = combase.NewProc("RoUninitialize")
	procRoActivateInstance = combase.NewProc("RoActivateInstance")
	procRoGetFactory       = combase.NewProc("RoGetActivationFactory")
	procCreateString       = combase.NewProc("WindowsCreateString")
	procDeleteString       = combase.NewProc("WindowsDeleteString")
)

const (
	sFalse           = 1
	rpcEChangedMode  = 0x80010106
	coinitApartment  = 0x2
	coinitNoOLE1DDE  = 0x4
	roInitMultithrd  = 1
	clsctxInproc     = 0x1
	vtQueryInterface = 0
	vtRelease        = 2
)

// hresult 把 COM 的回傳值轉成錯誤；成功（S_OK、S_FALSE）為 nil。
func hresult(what string, r uintptr) error {
	if int32(r) >= 0 {
		return nil
	}
	return fmt.Errorf("%s 失敗（HRESULT 0x%08X）", what, uint32(r))
}

// call 呼叫 COM 物件 obj 的 vtable 第 idx 個方法（第一個參數是 obj 本身）。
func call(obj unsafe.Pointer, idx int, args ...uintptr) uintptr {
	vtbl := *(*unsafe.Pointer)(obj)
	fn := *(*uintptr)(unsafe.Add(vtbl, uintptr(idx)*unsafe.Sizeof(uintptr(0))))
	r, _, _ := syscall.SyscallN(fn, append([]uintptr{uintptr(obj)}, args...)...)
	return r
}

func release(obj unsafe.Pointer) {
	if obj != nil {
		call(obj, vtRelease)
	}
}

func queryInterface(obj unsafe.Pointer, iid *windows.GUID, what string) (unsafe.Pointer, error) {
	var out unsafe.Pointer
	if err := hresult(what, call(obj, vtQueryInterface, uintptr(unsafe.Pointer(iid)), uintptr(unsafe.Pointer(&out)))); err != nil {
		return nil, err
	}
	return out, nil
}

// initialized 回報這次初始化是否要在結束時解除：S_OK／S_FALSE 要；這個執行緒已用另一種模式初始化（RPC_E_CHANGED_MODE）
// 時照樣能用，但不由我們解除。
func initialized(what string, r uintptr) (bool, error) {
	if uint32(r) == rpcEChangedMode {
		return false, nil
	}
	if err := hresult(what, r); err != nil {
		return false, err
	}
	return true, nil
}

// hstring 是 WinRT 字串；用完以 free 釋放。
type hstring uintptr

func newHString(s string) (hstring, error) {
	u, err := windows.UTF16FromString(s)
	if err != nil {
		return 0, err
	}
	var h hstring
	n := len(u) - 1 // 不含結尾的 0
	r := mustCall(procCreateString, uintptr(unsafe.Pointer(&u[0])), uintptr(n), uintptr(unsafe.Pointer(&h)))
	runtime.KeepAlive(u) // 參數經 uintptr 傳遞，呼叫期間要留著
	if err := hresult("WindowsCreateString", r); err != nil {
		return 0, err
	}
	return h, nil
}

func (h hstring) free() {
	if h != 0 {
		procDeleteString.Call(uintptr(h))
	}
}

func mustCall(p *windows.LazyProc, args ...uintptr) uintptr {
	r, _, _ := p.Call(args...)
	return r
}

func guid(s string) *windows.GUID {
	g, err := windows.GUIDFromString(s)
	if err != nil {
		panic(err)
	}
	return &g
}
