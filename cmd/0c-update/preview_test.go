package main

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/0c-biggo/0c-ime/internal/settings"
)

// 0.2.0 正式開放（#128）：設定契約與狀態一律回報 features.vocab（舊外殼與設定程式才顯示入口），
// 設定檔沒有、或舊檔寫著 preview_0_2=false 都一樣；設定項目不列 preview-0-2。
func TestPreview02FeaturesAlwaysOn(t *testing.T) {
	for _, body := range []string{"", `{"preview_0_2":false}`, `{"preview_0_2":true}`} {
		base := t.TempDir()
		if body != "" {
			writeSettings(t, base, body)
		}
		doc := buildSettingsDoc(base, modelStatusDial(map[string]string{"preview_0_2": "false"}, true))
		if !doc.Features.Vocab {
			t.Fatalf("%q：設定契約要回報 features.vocab", body)
		}
		for _, it := range doc.Items {
			if it.Key == settings.PreviewRetiredKey {
				t.Fatalf("%q：已拿掉的開關不列進設定項目", body)
			}
		}
		if doc.PendingRestart {
			t.Fatalf("%q：舊引擎回報的 preview_0_2 不讓設定程式顯示待套用", body)
		}
		f, ok := statusMap(base, "", modelStatusDial(nil, false))["features"].(features)
		if !ok || !f.Vocab || !f.EngineService {
			t.Fatalf("%q：status -json 要回報 features.vocab 與常駐引擎：%+v", body, f)
		}
	}
}

// set preview-0-2 on 照舊成功、不改其他值，寫回時順便移除舊鍵；off 回清楚的錯誤。
func TestSetPreview02IsRetired(t *testing.T) {
	base := t.TempDir()
	writeSettings(t, base, `{"schema":1,"selection":"statistical","preview_0_2":false}`)
	path := settings.Path(filepath.Join(base, "user.json"))
	res, err := runSet(base, modelStatusDial(nil, false), setOptions{pairs: [][2]string{{settings.PreviewRetiredKey, "on"}}}, func(string) {})
	if err != nil || len(res.Changes) != 1 || res.Changes[0].To != "on" {
		t.Fatalf("on 照舊成功：%+v %v", res, err)
	}
	b, _ := os.ReadFile(path)
	if strings.Contains(string(b), "preview") || !strings.Contains(string(b), `"selection": "statistical"`) {
		t.Fatalf("寫回不含 preview_0_2、其他值不變：%s", b)
	}
	_, err = runSet(base, modelStatusDial(nil, false), setOptions{pairs: [][2]string{{settings.PreviewRetiredKey, "off"}}}, func(string) {})
	var se *settings.SetError
	if !errors.As(err, &se) || se.Code != settings.CodeInvalidValue || !strings.Contains(err.Error(), "正式開放") {
		t.Fatalf("off 回清楚的錯誤：%v", err)
	}
}

// vocab、api 子指令列在用法裡，也不再因為設定檔沒打開開關而拒絕。
func TestVocabCommandsListedAndOpen(t *testing.T) {
	err := run(nil)
	if err == nil || !strings.Contains(err.Error(), "|vocab|api|") {
		t.Fatalf("用法要列出 vocab 與 api：%v", err)
	}
	if runtime.GOOS != "linux" {
		t.Skip("只有 Linux 的個人資料目錄跟著 HOME，測試裡換得掉")
	}
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_RUNTIME_DIR", t.TempDir()) // 沒有引擎：vocab 走檔案鎖模式
	if err := os.MkdirAll(basePath(), 0o700); err != nil {
		t.Fatal(err)
	}
	writeSettings(t, basePath(), `{"preview_0_2":false}`)
	stdout := os.Stdout
	devnull, err := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = devnull
	verr := vocabCommand([]string{"status", "-json"})
	aerr := apiCommand([]string{"status", "-json"})
	os.Stdout = stdout
	devnull.Close()
	if verr != nil {
		t.Fatalf("舊檔寫 preview_0_2=false 仍可用 vocab：%v", verr)
	}
	if aerr != nil {
		t.Fatalf("api 不再拒絕：%v", aerr)
	}
}
