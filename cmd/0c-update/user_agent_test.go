package main

import (
	"runtime"
	"testing"

	"github.com/0c-biggo/0c-ime/internal/update"
)

// 0c-update 啟動時就設好 User-Agent：版本（打包時寫入，開發建置為 dev）與平台，不帶其他資訊。
func TestUpdaterUserAgent(t *testing.T) {
	want := "0c-update/" + buildVersion + " (" + runtime.GOOS + "; " + runtime.GOARCH + ")"
	if update.UserAgent != want {
		t.Fatalf("User-Agent = %q，應為 %q", update.UserAgent, want)
	}
}
