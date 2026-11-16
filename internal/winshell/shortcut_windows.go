//go:build windows

package winshell

import (
	"runtime"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	clsidShellLink  = guid("{00021401-0000-0000-C000-000000000046}")
	iidIShellLinkW  = guid("{000214F9-0000-0000-C000-000000000046}")
	iidIPersistFile = guid("{0000010B-0000-0000-C000-000000000046}")
)

// IShellLinkW 與 IPersistFile 的 vtable 位置（IUnknown 佔 0–2）。
const (
	slSetDescription      = 7
	slSetWorkingDirectory = 9
	slSetArguments        = 11
	slSetIconLocation     = 17
	slSetPath             = 20
	pfSave                = 6 // IPersist::GetClassID 是 3，之後 IsDirty、Load、Save
)

// Shortcut 是 .lnk 的內容；每個欄位都明確寫入（新建的物件，不讀入既有的同名捷徑）。
type Shortcut struct {
	Path        string // 捷徑檔
	Target      string
	Args        string
	Dir         string
	Icon        string // 圖示檔，取第 0 個圖示
	Description string
}

// CreateShortcut 以 IShellLinkW 建立（或覆寫）捷徑，與 WScript.Shell 的 CreateShortcut 產生的是同一種 .lnk。
func CreateShortcut(s Shortcut) error {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	owned, err := initialized("CoInitializeEx", mustCall(procCoInitializeEx, 0, coinitApartment|coinitNoOLE1DDE))
	if err != nil {
		return err
	}
	if owned {
		defer procCoUninitialize.Call()
	}
	var link unsafe.Pointer
	if err = hresult("CoCreateInstance(ShellLink)", mustCall(procCoCreateInstance, uintptr(unsafe.Pointer(clsidShellLink)), 0,
		clsctxInproc, uintptr(unsafe.Pointer(iidIShellLinkW)), uintptr(unsafe.Pointer(&link)))); err != nil {
		return err
	}
	defer release(link)
	for _, f := range []struct {
		name  string
		idx   int
		value string
	}{
		{"SetPath", slSetPath, s.Target},
		{"SetArguments", slSetArguments, s.Args},
		{"SetWorkingDirectory", slSetWorkingDirectory, s.Dir},
		{"SetDescription", slSetDescription, s.Description},
		{"SetIconLocation", slSetIconLocation, s.Icon},
	} {
		p, err := windows.UTF16PtrFromString(f.value)
		if err != nil {
			return err
		}
		args := []uintptr{uintptr(unsafe.Pointer(p))}
		if f.idx == slSetIconLocation {
			args = append(args, 0)
		}
		r := call(link, f.idx, args...)
		runtime.KeepAlive(p) // 參數經 uintptr 傳遞，呼叫期間要留著
		if err = hresult("IShellLinkW::"+f.name, r); err != nil {
			return err
		}
	}
	file, err := queryInterface(link, iidIPersistFile, "QueryInterface(IPersistFile)")
	if err != nil {
		return err
	}
	defer release(file)
	path, err := windows.UTF16PtrFromString(s.Path)
	if err != nil {
		return err
	}
	r := call(file, pfSave, uintptr(unsafe.Pointer(path)), 1)
	runtime.KeepAlive(path)
	return hresult("IPersistFile::Save", r)
}
