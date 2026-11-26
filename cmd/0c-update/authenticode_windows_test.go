//go:build windows

package main

import (
	"os"
	"strings"
	"testing"
)

// Authenticode：已簽章的發布檔讀得出簽章者主體；沒有簽章的檔（測試執行檔本身）回報錯誤。ZC_SIGNED_EXE 指向一份
// 正式簽章的 0c-update.exe（例如發布套件裡的）才跑，開發機與 CI 沒有就略過。
func TestAuthenticodeSigner(t *testing.T) {
	signed := os.Getenv("ZC_SIGNED_EXE")
	if signed == "" {
		t.Skip("設 ZC_SIGNED_EXE 為正式簽章的 0c-update.exe 才測")
	}
	subject, err := authenticodeSigner(signed)
	if err != nil || !strings.Contains(subject, "CN=") {
		t.Fatalf("signed: %q %v", subject, err)
	}
	t.Logf("簽章者：%s", subject)
	if again, err := authenticodeSigner(signed); err != nil || again != subject {
		t.Fatalf("not stable: %q %v", again, err)
	}
	self, _ := os.Executable()
	if _, err := authenticodeSigner(self); err == nil {
		t.Fatal("unsigned test binary reported as signed")
	}
	// 目前這一份沒有簽章（測試執行檔）：沒有發行者可比對，不擋。
	if err := verifyPublisher(signed); err != nil {
		t.Fatalf("unsigned self must not block: %v", err)
	}
	if unsigned := os.Getenv("ZC_UNSIGNED_EXE"); unsigned != "" {
		if _, err := authenticodeSigner(unsigned); err == nil {
			t.Fatalf("%s reported as signed", unsigned)
		}
	}
}
