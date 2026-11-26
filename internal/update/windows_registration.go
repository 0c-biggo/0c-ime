package update

// 註冊不變的外殼更新（#294）：TSF 的註冊只能寫在 HKLM，需要系統管理員；但 0.1.0-dev.62 起 InprocServer32 永遠指向
// 固定路徑（#121），註冊的內容（CLSID、固定路徑、profile、圖示、類別、有沒有 32 位元 DLL）很少改變。套件的 release.json
// 帶著這些內容的指紋（tsf_registration，由 platforms/windows/tsf/src/register.rs 的 registration_spec 算出）；
// 與已安裝版本相同、而且登錄確實是那份註冊時，更新只要以一般權限換固定路徑的 DLL 與收據，不必 UAC。
// 固定路徑本來就在使用者可寫的目錄（repairFixedDLL 一直以一般權限換這個檔），所以不會比提權安裝更不安全。
import (
	"archive/zip"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"path"
	"path/filepath"
	"strings"
)

// ErrRegistrationChanged：這次更新要改 TSF 註冊（或無法確認不必改），需要系統管理員。
var ErrRegistrationChanged = errors.New("這次更新要改輸入法的註冊，需要系統管理員確認")

// ErrDLLProbe：換上的固定路徑 DLL 載入核對失敗；已換回原本的版本，收據與註冊都沒有改。
var ErrDLLProbe = errors.New("新版輸入法元件無法載入")

// RegistrationVerifier 由平台的 Registrar 實作：核對 installed 沒看的那部分註冊（32 位元視圖的 InprocServer32、語言
// profile）確實在登錄裡。x86 表示這一版有 32 位元 DLL。沒有實作的 Registrar 一律當作需要重新註冊。
type RegistrationVerifier interface {
	RegistrationIntact(base string, x86 bool) error
}

// DLLProbe 核對換上的固定路徑 DLL 載入得了（x86 為空字串表示這一版沒有 32 位元 DLL）。
type DLLProbe func(x64, x86 string) error

func registrationChanged(format string, a ...any) error {
	return fmt.Errorf("%w（%s）", ErrRegistrationChanged, fmt.Sprintf(format, a...))
}

// installedRelease 讀取收據版本目錄裡的 release.json，並確認它就是收據記錄的那一版。
func installedRelease(base string, r *Receipt) (*Release, error) {
	dir, err := receiptDirectory(base, r)
	if err != nil {
		return nil, err
	}
	p, err := regular(dir, "release.json")
	if err != nil {
		return nil, err
	}
	b, err := readLimited(p)
	if err != nil {
		return nil, err
	}
	var release Release
	if err = json.Unmarshal(b, &release); err != nil {
		return nil, err
	}
	if release.Version != r.Version || !maps.Equal(release.Files, r.Files) {
		return nil, fmt.Errorf("版本目錄的 release.json 與收據不符")
	}
	return &release, nil
}

func readLimited(p string) ([]byte, error) {
	f, err := os.Open(p)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, (1<<20)+1))
	if err != nil {
		return nil, err
	}
	if len(b) > 1<<20 {
		return nil, fmt.Errorf("release metadata too large")
	}
	return b, nil
}

// keepsRegistration 回報把 previous（目前的收據，已由 installed 與實際註冊核對過）換成 next 是否不必改註冊；
// 不行時回傳包著 ErrRegistrationChanged 的原因。
func keepsRegistration(base string, previous *Receipt, next *Release, reg Registrar) error {
	if next.TSFRegistration == "" {
		return registrationChanged("新版套件沒有註冊指紋")
	}
	if !usesFixed(&Receipt{Version: next.Version, Directory: "versions/next", Files: next.Files}) {
		return registrationChanged("新版不從固定路徑載入")
	}
	switch {
	case previous == nil || !previous.Active:
		return registrationChanged("目前沒有安裝")
	case !usesFixed(previous):
		return registrationChanged("目前的版本不是從固定路徑載入")
	case previous.registered != nil:
		return registrationChanged("登錄裡的註冊與安裝收據不一致")
	}
	have, err := installedRelease(base, previous)
	if err != nil {
		return registrationChanged("讀不到目前版本的註冊指紋：%v", err)
	}
	if have.TSFRegistration != next.TSFRegistration {
		return registrationChanged("註冊指紋不同")
	}
	verifier, ok := reg.(RegistrationVerifier)
	if !ok {
		return registrationChanged("無法核對登錄")
	}
	_, x86 := next.Files[WindowsDLL32]
	if err := verifier.RegistrationIntact(base, x86); err != nil {
		return registrationChanged("登錄裡的註冊不完整：%v", err)
	}
	return nil
}

// WindowsKeepsRegistration（不上鎖的預先判斷）回報安裝 next 是否不必改註冊，用來決定要不要請使用者確認 UAC。
// 真正安裝時 InstallWindowsKeepingRegistration 會在安裝鎖內再核對一次。
func WindowsKeepsRegistration(base string, reg Registrar, next *Release) error {
	base, err := filepath.Abs(base)
	if err != nil {
		return err
	}
	previous, err := installed(base, reg)
	if err != nil {
		return registrationChanged("%v", err)
	}
	return keepsRegistration(base, previous, next, reg)
}

// PeekRelease 讀出 Windows 套件壓縮檔（.zip，單一根目錄）裡的 release.json，不解開其他檔案。只用來事先判斷要不要
// 系統管理員；套件本身仍由 StageIn 與 Verify 核對雜湊後才安裝。
func PeekRelease(archive string) (*Release, error) {
	if !strings.HasSuffix(strings.ToLower(archive), ".zip") {
		return nil, fmt.Errorf("只讀得了 .zip 套件")
	}
	z, err := zip.OpenReader(archive)
	if err != nil {
		return nil, err
	}
	defer z.Close()
	var found *zip.File
	for _, f := range z.File {
		dir, name := path.Split(f.Name)
		if name != "release.json" || strings.Count(dir, "/") != 1 {
			continue
		}
		if found != nil {
			return nil, fmt.Errorf("壓縮檔有多份 release.json")
		}
		found = f
	}
	if found == nil {
		return nil, fmt.Errorf("壓縮檔沒有 release.json")
	}
	if found.UncompressedSize64 > 1<<20 {
		return nil, fmt.Errorf("release metadata too large")
	}
	rc, err := found.Open()
	if err != nil {
		return nil, err
	}
	defer rc.Close()
	b, err := io.ReadAll(io.LimitReader(rc, (1<<20)+1))
	if err != nil {
		return nil, err
	}
	var r Release
	if err = json.Unmarshal(b, &r); err != nil {
		return nil, err
	}
	return &r, nil
}

// swapFixed 是註冊不變時的切換：只換固定路徑的 DLL 與收據，不動登錄，不需要系統管理員。換上的 DLL 載入不了時換回
// 上一版（從它的版本目錄補回，被換下的新檔改名成 .old 後清掉），收據維持原本的版本。
func swapFixed(base string, previous, next *Receipt, probe DLLProbe) error {
	tag := func(r *Receipt) string { return filepath.Base(filepath.FromSlash(r.Directory)) }
	restore := func(cause error) error {
		return errors.Join(cause, DeployFixed(base, previous, tag(next)))
	}
	if err := DeployFixed(base, next, tag(previous)); err != nil {
		return restore(err)
	}
	if probe != nil {
		x86 := ""
		if _, ok := next.Files[WindowsDLL32]; ok {
			x86 = WindowsDLL32Of(FixedDLL(base))
		}
		if err := probe(FixedDLL(base), x86); err != nil {
			return restore(fmt.Errorf("%w：%v", ErrDLLProbe, err))
		}
	}
	if err := writeJSON(filepath.Join(base, "install-receipt.json"), next); err != nil {
		return restore(err)
	}
	return applyEnginePointer(base, next)
}

// InstallWindowsKeepingRegistration 以一般權限安裝註冊內容不變的新版（#294）：核對與 InstallWindows 相同，切換時只換
// 固定路徑的 DLL（換上後以 probe 核對載入得了，不行就換回原本的版本）。要改註冊時不做任何變更，回傳 ErrRegistrationChanged。
func InstallWindowsKeepingRegistration(bundle, base, arch string, reg Registrar, probe DLLProbe) (string, []string, error) {
	return installWindows(bundle, base, arch, reg, &keepMode{probe: probe})
}

// keepMode：安裝時不改註冊（見 InstallWindowsKeepingRegistration）。
type keepMode struct{ probe DLLProbe }
