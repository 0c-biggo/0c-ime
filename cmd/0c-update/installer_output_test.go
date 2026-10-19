package main

import (
	"bytes"
	"os"
	"os/exec"
	"strings"
	"testing"
	"unicode/utf16"
)

func decodeUTF16LE(t *testing.T, b []byte) string {
	t.Helper()
	if len(b)%2 != 0 {
		t.Fatalf("UTF-16LE 長度不是偶數：%d", len(b))
	}
	units := make([]uint16, len(b)/2)
	for i := range units {
		units[i] = uint16(b[2*i]) | uint16(b[2*i+1])<<8
	}
	return string(utf16.Decode(units))
}

// #138：安裝程式的錯誤對話框把 UTF-8 中文當 CP950 顯示成亂碼；改送帶 BOM 的 UTF-16LE。
func TestUTF16EncoderKeepsChineseAcrossChunks(t *testing.T) {
	text := "已安裝靈犀注音至 C:\\Users\\Fw4Ri\\AppData\\Local\\Zuin\n0c-update: 配對詞庫檢查失敗 😀\r\n"
	raw := []byte(text)
	var e utf16Encoder
	var out []byte
	// 逐位元組送入：多位元組字元一定會被切開。
	for i := range raw {
		out = append(out, e.encode(raw[i:i+1], false)...)
	}
	out = append(out, e.encode(nil, true)...)
	if !bytes.HasPrefix(out, []byte{0xff, 0xfe}) {
		t.Fatalf("缺少 BOM：% x", out[:4])
	}
	got := decodeUTF16LE(t, out[2:])
	want := "已安裝靈犀注音至 C:\\Users\\Fw4Ri\\AppData\\Local\\Zuin\r\n0c-update: 配對詞庫檢查失敗 😀\r\n"
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
	if bytes.Count(out, []byte{0xff, 0xfe}) != 1 {
		t.Fatalf("BOM 只能出現一次")
	}
}

func TestUTF16EncoderBOMTravelsWithFirstCharacter(t *testing.T) {
	var e utf16Encoder
	if out := e.encode([]byte{0xe5}, false); out != nil {
		t.Fatalf("不完整的字元不應先送出 BOM：% x", out)
	}
	out := e.encode([]byte{0xb7, 0xb2}, false) // 「已」
	if !bytes.Equal(out, []byte{0xff, 0xfe, 0xf2, 0x5d}) {
		t.Fatalf("got % x", out)
	}
	if out := e.encode([]byte{0xff}, true); !bytes.Equal(out, []byte{0xfd, 0xff}) {
		t.Fatalf("無效位元組應換成 U+FFFD：% x", out)
	}
}

// 整個程式：安裝程式設定環境變數時，stdout 與 stderr 依先後順序合併成 UTF-16LE，結束碼不變。
func TestInstallerOutputConvertsWholeProcess(t *testing.T) {
	if os.Getenv("ZC_TEST_INSTALLER_OUTPUT_CHILD") == "1" {
		flush := installerOutput()
		os.Stdout.WriteString("已建立登入啟動引擎的排程\n")
		os.Stderr.WriteString("0c-update: 安裝失敗\n")
		if os.Getenv(utf16OutputEnv) != "" {
			os.Stdout.WriteString("環境變數沒有清掉\n")
		}
		flush()
		os.Exit(3)
	}
	cmd := exec.Command(os.Args[0], "-test.run=^TestInstallerOutputConvertsWholeProcess$")
	cmd.Env = append(os.Environ(), "ZC_TEST_INSTALLER_OUTPUT_CHILD=1", utf16OutputEnv+"=1")
	var buf bytes.Buffer
	cmd.Stdout, cmd.Stderr = &buf, &buf
	err := cmd.Run()
	if exit, ok := err.(*exec.ExitError); !ok || exit.ExitCode() != 3 {
		t.Fatalf("結束碼應保留：%v", err)
	}
	out := buf.Bytes()
	if !bytes.HasPrefix(out, []byte{0xff, 0xfe}) {
		t.Fatalf("缺少 BOM：%q", out)
	}
	got := decodeUTF16LE(t, out[2:])
	if want := "已建立登入啟動引擎的排程\r\n0c-update: 安裝失敗\r\n"; got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

// 安裝程式與解除安裝程式都要用同一個環境變數要求 UTF-16 輸出。
func TestSetupRequestsUTF16Output(t *testing.T) {
	b, err := os.ReadFile("../../platforms/windows/installer/setup.nsi")
	if err != nil {
		t.Fatal(err)
	}
	s := string(b)
	if !strings.Contains(s, `SetEnvironmentVariable(t "`+utf16OutputEnv+`", t "1")`) {
		t.Fatalf("setup.nsi 沒有設定 %s", utf16OutputEnv)
	}
	for _, f := range []string{"Function .onInit", "Function un.onInit"} {
		body := s[strings.Index(s, f):]
		body = body[:strings.Index(body, "FunctionEnd")]
		if !strings.Contains(body, "RequestUnicodeOutput") {
			t.Fatalf("%s 沒有要求 UTF-16 輸出", f)
		}
	}
}

func TestInstallerOutputOffByDefault(t *testing.T) {
	t.Setenv(utf16OutputEnv, "")
	before := os.Stdout
	flush := installerOutput()
	flush()
	if os.Stdout != before || strings.Contains(os.Getenv(utf16OutputEnv), "1") {
		t.Fatal("沒有要求時不應改動輸出")
	}
}
