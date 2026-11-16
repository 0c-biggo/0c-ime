//go:build windows

package winshell

import (
	"errors"
	"runtime"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	oleaut32           = windows.NewLazySystemDLL("oleaut32.dll")
	procSysAllocString = oleaut32.NewProc("SysAllocString")
	procSysFreeString  = oleaut32.NewProc("SysFreeString")
	procSysStringLen   = oleaut32.NewProc("SysStringLen")

	clsidTaskScheduler = guid("{0F87369F-A4E5-4CFC-BD3E-73E6154572DD}")
	iidITaskService    = guid("{2FABA4C7-4DA9-4013-9697-20CC3FD40F85}")
)

// ITaskService、ITaskFolder、IRegisteredTask 的 vtable 位置（IUnknown 0–2、IDispatch 3–6；taskschd.h）。
const (
	tsGetFolder = 7
	tsConnect   = 10

	tfGetTask      = 13
	tfDeleteTask   = 15
	tfRegisterTask = 16

	rtRun                   = 12
	rtGetXML                = 20
	rtGetSecurityDescriptor = 21
	rtSetSecurityDescriptor = 22
)

const (
	taskCreateOrUpdate    = 6 // TASK_CREATE_OR_UPDATE
	taskLogonInteractive  = 3 // TASK_LOGON_INTERACTIVE_TOKEN：使用者登入時以他的權杖執行（與定義的 Principal 相同）
	securityInfoOwnerDACL = windows.OWNER_SECURITY_INFORMATION | windows.GROUP_SECURITY_INFORMATION | windows.DACL_SECURITY_INFORMATION
	vtBSTR                = 8
	hrFileNotFound        = 0x80070002
	hrPathNotFound        = 0x80070003
	hrAccessDenied        = 0x80070005
)

// ErrTaskNotFound：工作排程器裡沒有這個工作。
var ErrTaskNotFound = errors.New("排程不存在")

// ErrAccessDenied：權限不足（例如一般權限改寫或刪除提權建立、只開放讀取的排程）。
var ErrAccessDenied = errors.New("存取被拒")

// Task 是工作排程器裡一個工作目前的內容。
type Task struct {
	XML  string // 系統正規化後的工作定義
	SDDL string // 擁有者、群組與 DACL
}

// variant 是 OLE 的 VARIANT（64 位元 24 位元組、32 位元 16 位元組）。
type variant struct {
	vt        uint16
	_         [3]uint16
	val, val2 uintptr
}

// variantArgs：以值傳遞的 VARIANT 在 x64 與 ARM64 超過暫存器大小，由呼叫者傳指向副本的指標；
// 32 位元 x86 則整個放在堆疊上（4 個 32 位元字）。
func variantArgs(v *variant) []uintptr {
	if unsafe.Sizeof(uintptr(0)) == 8 {
		return []uintptr{uintptr(unsafe.Pointer(v))}
	}
	w := (*[4]uint32)(unsafe.Pointer(v))
	return []uintptr{uintptr(w[0]), uintptr(w[1]), uintptr(w[2]), uintptr(w[3])}
}

// bstr 是 COM 的 BSTR；用完以 free 釋放。
type bstr uintptr

func newBSTR(s string) (bstr, error) {
	u, err := windows.UTF16PtrFromString(s)
	if err != nil {
		return 0, err
	}
	b, _, _ := procSysAllocString.Call(uintptr(unsafe.Pointer(u)))
	runtime.KeepAlive(u)
	if b == 0 {
		return 0, errors.New("SysAllocString 失敗")
	}
	return bstr(b), nil
}

func (b bstr) free() {
	if b != 0 {
		procSysFreeString.Call(uintptr(b))
	}
}

func (b bstr) String() string {
	if b == 0 {
		return ""
	}
	n, _, _ := procSysStringLen.Call(uintptr(b))
	p := *(**uint16)(unsafe.Pointer(&b)) // BSTR 是系統配置的記憶體，不歸 Go 管
	return windows.UTF16ToString(unsafe.Slice(p, n))
}

// taskError 把工作排程器的 HRESULT 轉成可以用 errors.Is 判斷的錯誤。
func taskError(what string, r uintptr) error {
	err := hresult(what, r)
	if err == nil {
		return nil
	}
	switch uint32(r) {
	case hrFileNotFound, hrPathNotFound:
		return errors.Join(ErrTaskNotFound, err)
	case hrAccessDenied:
		return errors.Join(ErrAccessDenied, err)
	}
	return err
}

// withTaskRoot 連上本機的工作排程器，以根資料夾（\）執行 f。行程內呼叫 COM，不開 schtasks.exe（#289 的後續）。
func withTaskRoot(f func(root unsafe.Pointer) error) error {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	owned, err := initialized("CoInitializeEx", mustCall(procCoInitializeEx, 0, coinitApartment|coinitNoOLE1DDE))
	if err != nil {
		return err
	}
	if owned {
		defer procCoUninitialize.Call()
	}
	var service unsafe.Pointer
	if err = hresult("CoCreateInstance(TaskScheduler)", mustCall(procCoCreateInstance, uintptr(unsafe.Pointer(clsidTaskScheduler)), 0,
		clsctxInproc, uintptr(unsafe.Pointer(iidITaskService)), uintptr(unsafe.Pointer(&service)))); err != nil {
		return err
	}
	defer release(service)
	var empty variant // VT_EMPTY：本機、目前的使用者
	args := append(append(append(variantArgs(&empty), variantArgs(&empty)...), variantArgs(&empty)...), variantArgs(&empty)...)
	r := call(service, tsConnect, args...)
	runtime.KeepAlive(&empty)
	if err = hresult("ITaskService::Connect", r); err != nil {
		return err
	}
	path, err := newBSTR(`\`)
	if err != nil {
		return err
	}
	defer path.free()
	var root unsafe.Pointer
	if err = hresult("ITaskService::GetFolder", call(service, tsGetFolder, uintptr(path), uintptr(unsafe.Pointer(&root)))); err != nil {
		return err
	}
	defer release(root)
	return f(root)
}

// withTask 取得根資料夾裡名為 name 的工作（IRegisteredTask）執行 f；不存在時回傳 ErrTaskNotFound。
func withTask(name string, f func(task unsafe.Pointer) error) error {
	return withTaskRoot(func(root unsafe.Pointer) error {
		n, err := newBSTR(name)
		if err != nil {
			return err
		}
		defer n.free()
		var task unsafe.Pointer
		if err = taskError("ITaskFolder::GetTask", call(root, tfGetTask, uintptr(n), uintptr(unsafe.Pointer(&task)))); err != nil {
			return err
		}
		defer release(task)
		return f(task)
	})
}

// GetTask 讀出根資料夾裡名為 name 的工作的定義與安全描述元；不存在時回傳 ErrTaskNotFound。
func GetTask(name string) (Task, error) {
	var t Task
	err := withTask(name, func(task unsafe.Pointer) error {
		var x bstr
		if err := taskError("IRegisteredTask::get_Xml", call(task, rtGetXML, uintptr(unsafe.Pointer(&x)))); err != nil {
			return err
		}
		t.XML = x.String()
		x.free()
		var sd bstr
		if err := taskError("IRegisteredTask::GetSecurityDescriptor", call(task, rtGetSecurityDescriptor, uintptr(securityInfoOwnerDACL), uintptr(unsafe.Pointer(&sd)))); err != nil {
			return err
		}
		t.SDDL = sd.String()
		sd.free()
		return nil
	})
	return t, err
}

// TaskExists 回報根資料夾裡有沒有名為 name 的工作（讀不到時當作沒有）。
func TaskExists(name string) bool {
	return withTask(name, func(unsafe.Pointer) error { return nil }) == nil
}

// RegisterTask 以 XML 定義在根資料夾建立或取代名為 name 的工作，以使用者登入的權杖執行。
// sddl 非空時設定工作的 DACL（例如 TaskOpenSDDL），空字串時保留原本的（新建的工作則用預設值）。
//
// RegisterTask 的 sddl 參數只在新建時生效，取代既有的工作時會被忽略（.14 實測：TASK_CREATE_OR_UPDATE 與
// TASK_UPDATE 都一樣），所以寫完再以 IRegisteredTask::SetSecurityDescriptor 設一次；flags 0 保留工作排程器
// 替 Principal 加的讀取項目，繼承自根資料夾的項目照舊（結果與新建時傳 sddl 相同）。
func RegisterTask(name, xml, sddl string) error {
	return withTaskRoot(func(root unsafe.Pointer) error {
		n, err := newBSTR(name)
		if err != nil {
			return err
		}
		defer n.free()
		text, err := newBSTR(xml)
		if err != nil {
			return err
		}
		defer text.free()
		var empty, sd variant
		if sddl != "" {
			b, err := newBSTR(sddl)
			if err != nil {
				return err
			}
			defer b.free()
			sd.vt, sd.val = vtBSTR, uintptr(b)
		}
		args := []uintptr{uintptr(n), uintptr(text), taskCreateOrUpdate}
		args = append(args, variantArgs(&empty)...) // userId：沿用定義裡的 Principal
		args = append(args, variantArgs(&empty)...) // password
		args = append(args, taskLogonInteractive)
		args = append(args, variantArgs(&sd)...)
		var task unsafe.Pointer
		args = append(args, uintptr(unsafe.Pointer(&task)))
		r := call(root, tfRegisterTask, args...)
		runtime.KeepAlive(&empty)
		runtime.KeepAlive(&sd)
		if err = taskError("ITaskFolder::RegisterTask", r); err != nil {
			return err
		}
		defer release(task)
		if sddl == "" {
			return nil
		}
		return taskError("IRegisteredTask::SetSecurityDescriptor", call(task, rtSetSecurityDescriptor, sd.val, 0))
	})
}

// DeleteTask 刪除根資料夾裡名為 name 的工作；不存在時回傳 ErrTaskNotFound，權限不足時回傳 ErrAccessDenied。
func DeleteTask(name string) error {
	return withTaskRoot(func(root unsafe.Pointer) error {
		n, err := newBSTR(name)
		if err != nil {
			return err
		}
		defer n.free()
		return taskError("ITaskFolder::DeleteTask", call(root, tfDeleteTask, uintptr(n), 0))
	})
}

// RunTask 立即執行名為 name 的工作（與 schtasks /Run 相同，以工作定義的使用者與權限執行）。
func RunTask(name string) error {
	return withTask(name, func(task unsafe.Pointer) error {
		var empty variant
		var running unsafe.Pointer
		args := append(variantArgs(&empty), uintptr(unsafe.Pointer(&running)))
		r := call(task, rtRun, args...)
		runtime.KeepAlive(&empty)
		if err := taskError("IRegisteredTask::Run", r); err != nil {
			return err
		}
		release(running)
		return nil
	})
}
