//go:build windows

package winshell

import (
	"runtime"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	iidXmlDocumentIO       = guid("{6CD0E74E-EE65-4489-9EBF-CA43E87BA637}")
	iidXmlDocument         = guid("{F7F3A506-1E87-42D6-BCFB-B8C809FA5494}")
	iidToastFactory        = guid("{04124B20-82C6-4229-B109-FD9ED4662B53}")
	iidToastManagerStatics = guid("{50AC103F-D235-4598-BBEF-98FE4D1A3AD4}")
)

// WinRT 介面的方法從 6 開始（IUnknown 0–2、IInspectable 3–5）。
const (
	xmlLoadXml                = 6 // IXmlDocumentIO::LoadXml
	factoryCreateToast        = 6 // IToastNotificationFactory::CreateToastNotification
	managerCreateNotifierWith = 7 // IToastNotificationManagerStatics::CreateToastNotifierWithId
	notifierShow              = 6 // IToastNotifier::Show
)

// ShowToast 以 appID（已在 HKCU\Software\Classes\AppUserModelId 註冊顯示名稱的 AUMID）送出一則通知。
// Show 交給系統後就返回，行程結束不影響通知。
func ShowToast(appID string, t Toast) error {
	return withWinRT(func() error {
		doc, err := loadXMLDocument(ToastXML(t))
		if err != nil {
			return err
		}
		defer release(doc)
		factory, err := activationFactory("Windows.UI.Notifications.ToastNotification", iidToastFactory)
		if err != nil {
			return err
		}
		defer release(factory)
		var toast unsafe.Pointer
		if err = hresult("CreateToastNotification", call(factory, factoryCreateToast, uintptr(doc), uintptr(unsafe.Pointer(&toast)))); err != nil {
			return err
		}
		defer release(toast)
		manager, err := activationFactory("Windows.UI.Notifications.ToastNotificationManager", iidToastManagerStatics)
		if err != nil {
			return err
		}
		defer release(manager)
		id, err := newHString(appID)
		if err != nil {
			return err
		}
		defer id.free()
		var notifier unsafe.Pointer
		if err = hresult("CreateToastNotifierWithId", call(manager, managerCreateNotifierWith, uintptr(id), uintptr(unsafe.Pointer(&notifier)))); err != nil {
			return err
		}
		defer release(notifier)
		return hresult("IToastNotifier::Show", call(notifier, notifierShow, uintptr(toast)))
	})
}

// withWinRT 在鎖定的執行緒上初始化 Windows 執行階段（多執行緒 apartment）後執行 f。
func withWinRT(f func() error) error {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	owned, err := initialized("RoInitialize", mustCall(procRoInitialize, roInitMultithrd))
	if err != nil {
		return err
	}
	if owned {
		defer procRoUninitialize.Call()
	}
	return f()
}

// loadXMLDocument 以 Windows.Data.Xml.Dom.XmlDocument 解析 content，回傳 IXmlDocument（呼叫者 release）。
// 內容不是合法的 XML 時 LoadXml 失敗，通知不會送出。
func loadXMLDocument(content string) (unsafe.Pointer, error) {
	className, err := newHString("Windows.Data.Xml.Dom.XmlDocument")
	if err != nil {
		return nil, err
	}
	defer className.free()
	var inspectable unsafe.Pointer
	if err = hresult("RoActivateInstance(XmlDocument)", mustCall(procRoActivateInstance, uintptr(className), uintptr(unsafe.Pointer(&inspectable)))); err != nil {
		return nil, err
	}
	defer release(inspectable)
	io, err := queryInterface(inspectable, iidXmlDocumentIO, "QueryInterface(IXmlDocumentIO)")
	if err != nil {
		return nil, err
	}
	defer release(io)
	text, err := newHString(content)
	if err != nil {
		return nil, err
	}
	defer text.free()
	if err = hresult("IXmlDocumentIO::LoadXml", call(io, xmlLoadXml, uintptr(text))); err != nil {
		return nil, err
	}
	return queryInterface(inspectable, iidXmlDocument, "QueryInterface(IXmlDocument)")
}

func activationFactory(class string, iid *windows.GUID) (unsafe.Pointer, error) {
	name, err := newHString(class)
	if err != nil {
		return nil, err
	}
	defer name.free()
	var factory unsafe.Pointer
	if err = hresult("RoGetActivationFactory("+class+")", mustCall(procRoGetFactory, uintptr(name), uintptr(unsafe.Pointer(iid)), uintptr(unsafe.Pointer(&factory)))); err != nil {
		return nil, err
	}
	return factory, nil
}
