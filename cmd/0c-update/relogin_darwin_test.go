package main

import (
	"os"
	"path/filepath"
	"testing"
)

// #195：之前的安裝留下、還沒啟用成功的輸入來源記號，不能讓之後每次更新都要求重新登入；
// 同一個登入工作階段要不要重新登入只看 relogin-required（記下輸入來源的安裝都會同時寫它）。
func TestStaleInputSourceMarkerDoesNotRequireRelogin(t *testing.T) {
	base := t.TempDir()
	if err := os.MkdirAll(filepath.Join(base, "update"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(inputSourceMarker(base), []byte("/x/0c.app/Contents/MacOS/0c\n/x/0c.app\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if macReloginRequired(base) {
		t.Fatal("an input-source marker alone must not require a relogin")
	}
	markRelogin(base)
	if !macReloginRequired(base) {
		t.Fatal("a relogin marker from this login session still requires a relogin")
	}
	if !inputSourcePending(base) {
		t.Fatal("the marker stays pending until the input source is enabled")
	}
}
